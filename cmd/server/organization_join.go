package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

type organizationInvitation struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DepartmentIDs []string `json:"departmentIds"`
	AllowedRoles  []string `json:"allowedRoles"`
	ExpiresAt     string   `json:"expiresAt"`
	MaxUses       int      `json:"maxUses"`
	Uses          int      `json:"uses"`
	CreatedAt     string   `json:"createdAt"`
	RevokedAt     *string  `json:"revokedAt"`
}

// 邀请令牌是访问凭证：数据库只存摘要，完整链接不进入审计或管理列表，创建时仅返回一次。
func organizationTokenHash(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

// 每次访问都复核有效期、撤销和使用上限；allowExhausted 仅便于对相同待审申请幂等应答，不免除新申请限额。
func organizationInvitationByToken(ctx context.Context, store stateStore, token string, allowExhausted ...bool) (organizationInvitation, error) {
	var v organizationInvitation
	var departments, roles string
	if len(token) != 64 {
		return v, orgNotFound()
	}
	if _, err := hex.DecodeString(token); err != nil {
		return v, orgNotFound()
	}
	err := store.QueryRowContext(ctx, `SELECT id,name,department_ids_json,allowed_roles_json,expires_at,max_uses,uses,created_at,revoked_at FROM organization_invitations WHERE tenant_id=? AND token_hash=?`, tenantID, organizationTokenHash(token)).Scan(&v.ID, &v.Name, &departments, &roles, &v.ExpiresAt, &v.MaxUses, &v.Uses, &v.CreatedAt, &v.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, orgNotFound()
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal([]byte(departments), &v.DepartmentIDs); err != nil {
		return v, err
	}
	if err = json.Unmarshal([]byte(roles), &v.AllowedRoles); err != nil {
		return v, err
	}
	expires, err := time.Parse(time.RFC3339, v.ExpiresAt)
	if err != nil {
		return v, err
	}
	if v.RevokedAt != nil || !expires.After(time.Now()) || (v.Uses >= v.MaxUses && !(len(allowExhausted) > 0 && allowExhausted[0])) {
		return v, &organizationError{410, "invitation_unavailable", "申请链接已过期、已撤销或已达到使用上限"}
	}
	return v, nil
}
func (a *App) organizationInvitations(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "invitations.manage"); err != nil {
			failOrganization(w, err)
			return
		}
		rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,department_ids_json,allowed_roles_json,expires_at,max_uses,uses,created_at,revoked_at FROM organization_invitations WHERE tenant_id=? ORDER BY created_at DESC`, tenantID)
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer rows.Close()
		out := []organizationInvitation{}
		for rows.Next() {
			var v organizationInvitation
			var deps, roles string
			if err = rows.Scan(&v.ID, &v.Name, &deps, &roles, &v.ExpiresAt, &v.MaxUses, &v.Uses, &v.CreatedAt, &v.RevokedAt); err == nil {
				err = json.Unmarshal([]byte(deps), &v.DepartmentIDs)
			}
			if err == nil {
				err = json.Unmarshal([]byte(roles), &v.AllowedRoles)
			}
			if err != nil {
				failOrganization(w, err)
				return
			}
			out = append(out, v)
		}
		if err = rows.Err(); err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": out})
		return
	}
	if len(parts) == 2 && parts[1] == "revoke" && r.Method == http.MethodPost {
		var b struct{}
		if err := decodeOrganizationJSON(w, r, &b); err != nil {
			failOrganization(w, err)
			return
		}
		tx, _, err := a.beginOrganizationWrite(r, "invitations.manage")
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer tx.Rollback()
		var n int
		err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM organization_invitations WHERE tenant_id=? AND id=?`, tenantID, parts[0]).Scan(&n)
		if err == nil && n == 0 {
			err = orgNotFound()
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE organization_invitations SET revoked_at=COALESCE(revoked_at,?) WHERE tenant_id=? AND id=?`, orgNow(), tenantID, parts[0])
		}
		if err == nil {
			err = a.organizationAudit(r.Context(), tx, "organization_invitation", parts[0], "invitation_revoked", nil, map[string]bool{"revoked": true})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]bool{"revoked": true})
		return
	}
	if len(parts) != 0 || r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b struct {
		Name           string   `json:"name"`
		ExpiresInHours int      `json:"expiresInHours"`
		MaxUses        int      `json:"maxUses"`
		DepartmentIDs  []string `json:"departmentIds"`
		AllowedRoles   []string `json:"allowedRoles"`
	}
	if err := decodeOrganizationJSON(w, r, &b); err != nil {
		failOrganization(w, err)
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if !validOrgText(b.Name, 1, 80) || b.ExpiresInHours < 1 || b.ExpiresInHours > 720 || b.MaxUses < 1 || b.MaxUses > 500 || len(b.DepartmentIDs) == 0 || len(b.AllowedRoles) == 0 {
		failOrganization(w, orgInvalid("申请链接名称、有效期、次数或选项无效"))
		return
	}
	var err error
	b.DepartmentIDs, err = stringSet(b.DepartmentIDs, 100)
	if err == nil {
		b.AllowedRoles, err = stringSet(b.AllowedRoles, 10)
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	for _, role := range b.AllowedRoles {
		if !validProjectRole(role) || role == "project_admin" {
			failOrganization(w, orgInvalid("申请链接不能包含管理员角色"))
			return
		}
	}
	tx, _, err := a.beginOrganizationWrite(r, "invitations.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	for _, dep := range b.DepartmentIDs {
		var n int
		err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM departments WHERE tenant_id=? AND id=? AND status='active'`, tenantID, dep).Scan(&n)
		if err != nil {
			failOrganization(w, err)
			return
		}
		if n != 1 {
			failOrganization(w, orgInvalid("部门不存在、已停用或不属于当前企业"))
			return
		}
	}
	id, err := organizationID("inv_")
	if err != nil {
		failOrganization(w, err)
		return
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		failOrganization(w, err)
		return
	}
	token := hex.EncodeToString(random[:])
	expires := time.Now().UTC().Add(time.Duration(b.ExpiresInHours) * time.Hour).Format(time.RFC3339)
	now := orgNow()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_invitations(id,tenant_id,name,token_hash,department_ids_json,allowed_roles_json,expires_at,max_uses,created_by,created_at)VALUES(?,?,?,?,?,?,?,?,?,?)`, id, tenantID, b.Name, organizationTokenHash(token), jsonText(b.DepartmentIDs), jsonText(b.AllowedRoles), expires, b.MaxUses, a.uid(), now)
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "organization_invitation", id, "invitation_created", nil, map[string]any{"name": b.Name, "departmentIds": b.DepartmentIDs, "allowedRoles": b.AllowedRoles, "expiresAt": expires, "maxUses": b.MaxUses})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 201, map[string]any{"id": id, "token": token, "url": "/join/" + token, "expiresAt": expires, "maxUses": b.MaxUses, "uses": 0})
}

// RemoteAddr is server-controlled; do not trust arbitrary X-Forwarded-For
// headers. The shared token cap also bounds abuse behind a reverse proxy.
// 只取服务端 RemoteAddr，不信任任意转发头；反代后可能共用限流 IP，调整时必须先确定可信代理边界。
type organizationPublicRateLimitWindow struct {
	key   string
	start int64
	max   int
}

// 公开链接先按可信 RemoteAddr 和令牌摘要归一化限流键。令牌原文绝不写入数据库，
// 避免把可直接使用的邀请凭证留在限流表或错误日志中。
func organizationPublicRateLimitWindows(r *http.Request, token string, now int64) []organizationPublicRateLimitWindow {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	return []organizationPublicRateLimitWindow{
		{key: "ip:" + ip, start: now - now%60, max: 40},
		{key: "invite:" + token + ":" + ip, start: now - now%60, max: 10},
		{key: "day:" + token + ":" + ip, start: now - now%86400, max: 100},
	}
}

// 预检只读取已存在的计数，不能让伪造令牌先抢占组织写锁。真正扣减仍在
// 受邀链接重读后的事务内执行，因而两个并发请求同时越过预检也不会突破上限。
func organizationPublicRateLimitPreflight(ctx context.Context, store stateStore, r *http.Request, token string) error {
	for _, limit := range organizationPublicRateLimitWindows(r, token, time.Now().Unix()) {
		var windowStart int64
		var count int
		err := store.QueryRowContext(ctx, `SELECT window_start,count FROM organization_public_rate_limits WHERE key_hash=?`, organizationTokenHash(limit.key)).Scan(&windowStart, &count)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		// 当前窗口已到达最大允许次数时，下一次请求会被事务内计数器拒绝；
		// 此处提前返回可避免已知滥用流量继续争用 SQLite 写者。
		if windowStart == limit.start && count >= limit.max {
			return &organizationError{429, "rate_limited", "提交过于频繁，请稍后重试"}
		}
	}
	return nil
}

func organizationPublicRateLimit(ctx context.Context, tx *sql.Tx, r *http.Request, token string) error {
	for _, limit := range organizationPublicRateLimitWindows(r, token, time.Now().Unix()) {
		key := organizationTokenHash(limit.key)
		_, err := tx.ExecContext(ctx, `INSERT INTO organization_public_rate_limits(key_hash,window_start,count)VALUES(?,?,1) ON CONFLICT(key_hash) DO UPDATE SET count=CASE WHEN window_start=excluded.window_start THEN count+1 ELSE 1 END,window_start=excluded.window_start`, key, limit.start)
		if err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count FROM organization_public_rate_limits WHERE key_hash=?`, key).Scan(&count); err != nil {
			return err
		}
		if count > limit.max {
			return &organizationError{429, "rate_limited", "提交过于频繁，请稍后重试"}
		}
	}
	return nil
}

type organizationPublicApplicationInput struct {
	Name          string `json:"name"`
	DepartmentID  string `json:"departmentId"`
	RequestedRole string `json:"requestedRole"`
}

// 在写事务外先验证令牌授权的部门、角色和部门状态。事务内会以同样函数再次
// 校验，避免邀请撤销、部门停用等并发变化形成 TOCTOU 漏洞。
func organizationPublicApplicationAllowed(ctx context.Context, store stateStore, invite organizationInvitation, input organizationPublicApplicationInput) error {
	if !validChoice(input.DepartmentID, invite.DepartmentIDs) || !validChoice(input.RequestedRole, invite.AllowedRoles) || !validProjectRole(input.RequestedRole) || input.RequestedRole == "project_admin" {
		return orgInvalid("部门或申请角色不在此链接允许范围内")
	}
	var active int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM departments WHERE tenant_id=? AND id=? AND status='active'`, tenantID, input.DepartmentID).Scan(&active); err != nil {
		return err
	}
	if active != 1 {
		return orgInvalid("部门不存在、已停用或不属于当前企业")
	}
	return nil
}

func organizationPublicApplicationDuplicate(ctx context.Context, store stateStore, invite organizationInvitation, input organizationPublicApplicationInput) (bool, error) {
	var duplicate int
	err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM organization_applications WHERE tenant_id=? AND invitation_id=? AND name=? AND department_id=? AND requested_role=? AND status='pending'`, tenantID, invite.ID, input.Name, input.DepartmentID, input.RequestedRole).Scan(&duplicate)
	return duplicate > 0, err
}

// 公开入口只展示链接允许的组织/部门/普通角色，提交只收姓名、部门和申请角色，不收密码、不自动激活账号。
func (a *App) publicOrganizationInvitation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	token := strings.TrimPrefix(r.URL.Path, "/api/public/organization-invitations/")
	if strings.Contains(token, "/") {
		failOrganization(w, orgNotFound())
		return
	}
	if r.Method == http.MethodGet {
		invite, err := organizationInvitationByToken(r.Context(), a.db, token)
		if err != nil {
			failOrganization(w, err)
			return
		}
		var name string
		if err = a.db.QueryRowContext(r.Context(), `SELECT name FROM tenants WHERE id=?`, tenantID).Scan(&name); err != nil {
			failOrganization(w, err)
			return
		}
		departments, err := organizationDepartmentList(r.Context(), a.db)
		if err != nil {
			failOrganization(w, err)
			return
		}
		options := []map[string]any{}
		for _, dep := range departments {
			if dep.Status == "active" && validChoice(dep.ID, invite.DepartmentIDs) {
				options = append(options, map[string]any{"id": dep.ID, "name": dep.Name, "parentId": dep.ParentID})
			}
		}
		roles := []map[string]string{}
		for _, role := range organizationRoles {
			if role["key"] != "project_admin" && validChoice(role["key"], invite.AllowedRoles) {
				roles = append(roles, role)
			}
		}
		write(w, 200, map[string]any{"organizationName": name, "departments": options, "roles": roles})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b organizationPublicApplicationInput
	if err := decodeOrganizationJSON(w, r, &b); err != nil {
		failOrganization(w, err)
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	if !validOrgText(b.Name, 1, 80) {
		failOrganization(w, orgInvalid("姓名须为 1–80 字"))
		return
	}
	// 公开端先只读验证真实邀请，再检查已知限流。伪造、过期或已撤销令牌会
	// 在这里返回，绝不能为了确认“不存在”而进入 organization_write_locks。
	preflightInvite, err := organizationInvitationByToken(r.Context(), a.db, token, true)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if err = organizationPublicApplicationAllowed(r.Context(), a.db, preflightInvite, b); err != nil {
		failOrganization(w, err)
		return
	}
	duplicate, err := organizationPublicApplicationDuplicate(r.Context(), a.db, preflightInvite, b)
	if err != nil {
		failOrganization(w, err)
		return
	}
	// 已用尽的链接仅允许同一待审申请安全重放；其他请求无需进入写事务。
	if !duplicate && preflightInvite.Uses >= preflightInvite.MaxUses {
		failOrganization(w, &organizationError{410, "invitation_unavailable", "申请链接已过期、已撤销或已达到使用上限"})
		return
	}
	if err = organizationPublicRateLimitPreflight(r.Context(), a.db, r, token); err != nil {
		if rate, ok := err.(*organizationError); ok && rate.Status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "60")
		}
		failOrganization(w, err)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		failOrganization(w, err)
		return
	}
	// 写锁取得后必须重新读取邀请并复核同一组业务条件：预检只用于削减
	// 无效流量，不能作为并发撤销、停用部门或额度变化时的授权依据。
	invite, err := organizationInvitationByToken(r.Context(), tx, token, true)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if err = organizationPublicApplicationAllowed(r.Context(), tx, invite, b); err != nil {
		failOrganization(w, err)
		return
	}
	if err = organizationPublicRateLimit(r.Context(), tx, r, token); err != nil {
		if rate, ok := err.(*organizationError); ok && rate.Status == 429 {
			if commitErr := tx.Commit(); commitErr != nil {
				failOrganization(w, commitErr)
				return
			}
			w.Header().Set("Retry-After", "60")
		}
		failOrganization(w, err)
		return
	}
	duplicate, err = organizationPublicApplicationDuplicate(r.Context(), tx, invite, b)
	if err == nil && !duplicate {
		id, idErr := organizationID("app_")
		err = idErr
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_applications(id,tenant_id,invitation_id,name,department_id,requested_role,created_at)VALUES(?,?,?,?,?,?,?)`, id, tenantID, invite.ID, b.Name, b.DepartmentID, b.RequestedRole, orgNow())
		}
		if err == nil {
			// 不依赖事务外读取到的 uses；条件更新是最后一道额度防线，失败时
			// 整个申请插入回滚，保证不会出现“超额申请但次数未增加”。
			var result sql.Result
			result, err = tx.ExecContext(r.Context(), `UPDATE organization_invitations SET uses=uses+1 WHERE tenant_id=? AND id=? AND revoked_at IS NULL AND expires_at>? AND uses<max_uses`, tenantID, invite.ID, orgNow())
			if err == nil {
				var affected int64
				affected, err = result.RowsAffected()
				if err == nil && affected != 1 {
					err = &organizationError{410, "invitation_unavailable", "申请链接已过期、已撤销或已达到使用上限"}
				}
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,'','public','organization_application',?,'application_submitted',?,?)`, tenantID, id, jsonText(map[string]string{"invitationId": invite.ID}), orgNow())
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 202, map[string]bool{"submitted": true})
}

type organizationApplication struct {
	ID            string `json:"id"`
	InvitationID  string `json:"invitationId"`
	Name          string `json:"name"`
	DepartmentID  string `json:"departmentId"`
	RequestedRole string `json:"requestedRole"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	ReviewedBy    string `json:"reviewedBy"`
	ReviewedAt    string `json:"reviewedAt"`
	Reason        string `json:"reason"`
	UserID        string `json:"userId"`
}

// 申请角色只是意向，审批必须显式设置邮箱、初始密码及项目授权；拒绝不建账号，审批与审计原子提交。
func (a *App) organizationApplications(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "applications.review"); err != nil {
			failOrganization(w, err)
			return
		}
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "pending"
		}
		if !validChoice(status, []string{"pending", "approved", "rejected", "all"}) {
			failOrganization(w, orgInvalid("申请状态无效"))
			return
		}
		query := `SELECT id,invitation_id,name,department_id,requested_role,status,created_at,reviewed_by,reviewed_at,reason,user_id FROM organization_applications WHERE tenant_id=?`
		args := []any{tenantID}
		if status != "all" {
			query += ` AND status=?`
			args = append(args, status)
		}
		query += ` ORDER BY created_at DESC`
		rows, err := a.db.QueryContext(r.Context(), query, args...)
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer rows.Close()
		out := []organizationApplication{}
		for rows.Next() {
			var v organizationApplication
			if err = rows.Scan(&v.ID, &v.InvitationID, &v.Name, &v.DepartmentID, &v.RequestedRole, &v.Status, &v.CreatedAt, &v.ReviewedBy, &v.ReviewedAt, &v.Reason, &v.UserID); err != nil {
				failOrganization(w, err)
				return
			}
			out = append(out, v)
		}
		if err = rows.Err(); err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": out})
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost || !validChoice(parts[1], []string{"approve", "reject"}) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b struct {
		Email              string                          `json:"email"`
		InitialPassword    string                          `json:"initialPassword"`
		EmployeeNo         string                          `json:"employeeNo"`
		ProjectMemberships []organizationProjectMembership `json:"projectMemberships"`
		Reason             string                          `json:"reason"`
	}
	if err := decodeOrganizationJSON(w, r, &b); err != nil {
		failOrganization(w, err)
		return
	}
	if parts[1] == "approve" && len(b.ProjectMemberships) == 0 {
		failOrganization(w, orgInvalid("审批激活必须设置初始密码并明确分配项目权限"))
		return
	}
	if parts[1] == "reject" && !validOrgText(strings.TrimSpace(b.Reason), 1, 500) {
		failOrganization(w, orgInvalid("拒绝原因须为 1–500 字"))
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "applications.review")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	var app organizationApplication
	err = tx.QueryRowContext(r.Context(), `SELECT id,invitation_id,name,department_id,requested_role,status,created_at,reviewed_by,reviewed_at,reason,user_id FROM organization_applications WHERE tenant_id=? AND id=?`, tenantID, parts[0]).Scan(&app.ID, &app.InvitationID, &app.Name, &app.DepartmentID, &app.RequestedRole, &app.Status, &app.CreatedAt, &app.ReviewedBy, &app.ReviewedAt, &app.Reason, &app.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		err = orgNotFound()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	if app.Status != "pending" {
		failOrganization(w, orgConflict("申请已处理，请刷新后重试"))
		return
	}
	before := app
	if parts[1] == "approve" {
		deps := []string{app.DepartmentID}
		active := true
		member, saveErr := a.saveOrganizationMember(r.Context(), tx, admin, "", organizationMemberPatch{Name: &app.Name, Email: &b.Email, InitialPassword: &b.InitialPassword, EmployeeNo: &b.EmployeeNo, Active: &active, DepartmentIDs: &deps, PrimaryDepartmentID: &app.DepartmentID, ProjectMemberships: &b.ProjectMemberships})
		err = saveErr
		app.UserID = member.ID
		app.Status = "approved"
	} else {
		app.Status = "rejected"
		app.Reason = strings.TrimSpace(b.Reason)
	}
	app.ReviewedBy = a.uid()
	app.ReviewedAt = orgNow()
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE organization_applications SET status=?,reviewed_by=?,reviewed_at=?,reason=?,user_id=? WHERE tenant_id=? AND id=? AND status='pending'`, app.Status, app.ReviewedBy, app.ReviewedAt, app.Reason, app.UserID, tenantID, app.ID)
	}
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "organization_application", app.ID, "application_"+app.Status, before, app)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	status := 200
	if app.Status == "approved" {
		status = 201
	}
	write(w, status, map[string]string{"userId": app.UserID, "status": app.Status})
}
