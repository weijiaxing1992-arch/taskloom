package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
)

type organizationProjectMembership struct {
	ProjectID string `json:"projectId"`
	Role      string `json:"role"`
}
type organizationMember struct {
	ID                  string                          `json:"id"`
	Name                string                          `json:"name"`
	Email               string                          `json:"email"`
	EmployeeNo          string                          `json:"employeeNo"`
	Active              bool                            `json:"active"`
	OperationDisabled   bool                            `json:"operationDisabled"`
	MustChangePassword  bool                            `json:"mustChangePassword"`
	TenantRole          string                          `json:"tenantRole"`
	DepartmentIDs       []string                        `json:"departmentIds"`
	PrimaryDepartmentID string                          `json:"primaryDepartmentId"`
	ProjectMemberships  []organizationProjectMembership `json:"projectMemberships"`
	GroupIDs            []string                        `json:"groupIds"`
}
type organizationMemberPatch struct {
	Name                *string                          `json:"name"`
	Email               *string                          `json:"email"`
	EmployeeNo          *string                          `json:"employeeNo"`
	Active              *bool                            `json:"active"`
	OperationDisabled   *bool                            `json:"operationDisabled"`
	TenantRole          *string                          `json:"tenantRole"`
	InitialPassword     *string                          `json:"initialPassword"`
	DepartmentIDs       *[]string                        `json:"departmentIds"`
	PrimaryDepartmentID *string                          `json:"primaryDepartmentId"`
	ProjectMemberships  *[]organizationProjectMembership `json:"projectMemberships"`
}

// 目录关联按企业及稳定用户 ID 读取，逐个关闭游标；输出不含密码摘要、Webhook 密钥或会话。
func organizationMemberList(ctx context.Context, store stateStore, id string) ([]organizationMember, error) {
	// 被移除的账号保留在 users 和审计记录中，不能再出现在可管理成员目录里。
	query := `SELECT u.id,u.name,u.email,u.employee_no,u.active,tm.role,tm.status,u.operation_disabled,u.must_change_password FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND tm.status!='removed'`
	args := []any{tenantID}
	if id != "" {
		query += ` AND u.id=?`
		args = append(args, id)
	}
	query += ` ORDER BY u.name,u.id`
	rows, err := store.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	out := []organizationMember{}
	for rows.Next() {
		m := organizationMember{DepartmentIDs: []string{}, ProjectMemberships: []organizationProjectMembership{}, GroupIDs: []string{}}
		var status string
		if err = rows.Scan(&m.ID, &m.Name, &m.Email, &m.EmployeeNo, &m.Active, &m.TenantRole, &status, &m.OperationDisabled, &m.MustChangePassword); err != nil {
			rows.Close()
			return nil, err
		}
		m.Active = m.Active && status == "active"
		out = append(out, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Queries are tenant constrained even when a corrupt relation mentions a
	// user from another tenant. Close each cursor before opening the next one.
	for i := range out {
		rows, err = store.QueryContext(ctx, `SELECT dm.department_id,dm.is_primary FROM department_memberships dm JOIN departments d ON d.tenant_id=dm.tenant_id AND d.id=dm.department_id WHERE dm.tenant_id=? AND dm.user_id=? AND dm.status='active' ORDER BY dm.is_primary DESC,dm.department_id`, tenantID, out[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var dep string
			var primary bool
			if err = rows.Scan(&dep, &primary); err != nil {
				rows.Close()
				return nil, err
			}
			out[i].DepartmentIDs = append(out[i].DepartmentIDs, dep)
			if primary {
				out[i].PrimaryDepartmentID = dep
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = store.QueryContext(ctx, `SELECT pm.project_id,pm.role FROM project_members pm JOIN projects p ON p.tenant_id=pm.tenant_id AND p.id=pm.project_id WHERE pm.tenant_id=? AND pm.user_id=? AND p.status='active' ORDER BY pm.project_id`, tenantID, out[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p organizationProjectMembership
			if err = rows.Scan(&p.ProjectID, &p.Role); err != nil {
				rows.Close()
				return nil, err
			}
			out[i].ProjectMemberships = append(out[i].ProjectMemberships, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = store.QueryContext(ctx, `SELECT gm.group_id FROM organization_group_members gm JOIN organization_groups g ON g.tenant_id=gm.tenant_id AND g.id=gm.group_id WHERE gm.tenant_id=? AND gm.user_id=? ORDER BY gm.group_id`, tenantID, out[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var group string
			if err = rows.Scan(&group); err != nil {
				rows.Close()
				return nil, err
			}
			out[i].GroupIDs = append(out[i].GroupIDs, group)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func normalizedOrganizationEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || !strings.Contains(email, "@") {
		return "", orgInvalid("登录邮箱格式无效")
	}
	return email, nil
}

// 表单、CSV 和申请审批共用此写路径；完整校验后统一更新用户、部门、企业及项目授权，调用方负责提交事务。
func (a *App) saveOrganizationMember(ctx context.Context, tx *sql.Tx, admin bool, id string, b organizationMemberPatch) (organizationMember, error) {
	creating := id == ""
	m := organizationMember{Active: true, TenantRole: "member", DepartmentIDs: []string{}, ProjectMemberships: []organizationProjectMembership{}, GroupIDs: []string{}}
	var before any
	var old organizationMember
	var existingHash string
	var err error
	if !creating {
		members, err := organizationMemberList(ctx, tx, id)
		if err != nil {
			return m, err
		}
		if len(members) != 1 {
			return m, orgNotFound()
		}
		old = members[0]
		m = old
		before = old
		if err = tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE tenant_id=? AND id=?`, tenantID, id).Scan(&existingHash); err != nil {
			return m, err
		}
	}
	// 拥有普通成员管理权限不等于能接管另一管理员；邮箱/密码变更本身就可获取目标账号权限，必须保护委派管理员。
	if !admin && !creating {
		// Password/email/reset access to another delegated manager would itself
		// grant the attacker's account that manager's permissions.
		if old.TenantRole == "tenant_admin" || len(old.GroupIDs) > 0 {
			return m, orgForbidden()
		}
	}
	if b.Name != nil {
		m.Name = strings.TrimSpace(*b.Name)
	}
	if b.Email != nil {
		m.Email = *b.Email
	}
	if b.EmployeeNo != nil {
		m.EmployeeNo = strings.TrimSpace(*b.EmployeeNo)
	}
	if b.Active != nil {
		m.Active = *b.Active
	}
	if b.OperationDisabled != nil {
		m.OperationDisabled = *b.OperationDisabled
	}
	if b.TenantRole != nil {
		m.TenantRole = *b.TenantRole
	}
	if b.DepartmentIDs != nil {
		m.DepartmentIDs = append([]string{}, (*b.DepartmentIDs)...)
	}
	if b.PrimaryDepartmentID != nil {
		m.PrimaryDepartmentID = *b.PrimaryDepartmentID
	}
	if b.ProjectMemberships != nil {
		m.ProjectMemberships = append([]organizationProjectMembership{}, (*b.ProjectMemberships)...)
	}
	if !validOrgText(m.Name, 1, 80) || !validOrgText(m.EmployeeNo, 0, 64) || !validChoice(m.TenantRole, []string{"member", "tenant_admin"}) {
		return m, orgInvalid("成员姓名、工号或企业角色无效")
	}
	if !admin && m.TenantRole != "member" {
		return m, orgForbidden()
	}
	m.Email, err = normalizedOrganizationEmail(m.Email)
	if err != nil {
		return m, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE tenant_id=? AND lower(email)=? AND id!=?`, tenantID, m.Email, id).Scan(&count); err != nil {
		return m, err
	}
	if count > 0 {
		return m, orgConflict("邮箱或成员已存在")
	}
	if !creating && id == a.uid() && (!m.Active || m.OperationDisabled) {
		return m, &organizationError{409, "cannot_disable_self", "不能停用当前账号"}
	}
	// 最后一名有效且未业务禁用的管理员不能降权或停用；校验须保持在同一写事务中。
	if !creating && old.TenantRole == "tenant_admin" && old.Active && !old.OperationDisabled && (!m.Active || m.OperationDisabled || m.TenantRole != "tenant_admin") {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.role='tenant_admin' AND tm.status='active' AND u.active=1 AND u.operation_disabled=0 AND u.id!=?`, tenantID, id).Scan(&count); err != nil {
			return m, err
		}
		if count == 0 {
			return m, &organizationError{409, "last_admin", "不能降级或停用最后一个企业管理员"}
		}
	}
	passwordHash := existingHash
	passwordChanged := false
	temporaryPassword := ""
	if b.InitialPassword != nil && *b.InitialPassword != "" {
		temporaryPassword = *b.InitialPassword
	} else if creating || (m.Active && !old.Active && existingHash == "") {
		temporaryPassword, err = configuredInitialPassword()
		if err != nil {
			return m, err
		}
	}
	if creating {
		m.MustChangePassword = true
	}
	if temporaryPassword != "" {
		if err = validateInitialPassword(temporaryPassword); err != nil {
			return m, orgInvalid(err.Error())
		}
		passwordHash, err = a.encodePassword(temporaryPassword)
		if err != nil {
			return m, err
		}
		passwordChanged = true
		m.MustChangePassword = true
	}
	if m.Active && passwordHash == "" {
		return m, orgInvalid("激活账号前必须设置初始密码")
	}
	m.DepartmentIDs, err = stringSet(m.DepartmentIDs, 100)
	if err != nil {
		return m, err
	}
	if len(m.DepartmentIDs) == 0 {
		if m.PrimaryDepartmentID != "" {
			return m, orgInvalid("主部门必须包含在成员部门中")
		}
	} else if m.PrimaryDepartmentID == "" {
		m.PrimaryDepartmentID = m.DepartmentIDs[0]
	}
	primaryName := ""
	foundPrimary := len(m.DepartmentIDs) == 0
	for _, dep := range m.DepartmentIDs {
		var name, status string
		err = tx.QueryRowContext(ctx, `SELECT name,status FROM departments WHERE tenant_id=? AND id=?`, tenantID, dep).Scan(&name, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return m, orgInvalid("部门不存在或不属于当前企业")
		}
		if err != nil {
			return m, err
		}
		if status != "active" && !validChoice(dep, old.DepartmentIDs) {
			return m, orgInvalid("不能分配已停用的部门")
		}
		if dep == m.PrimaryDepartmentID {
			primaryName = name
			foundPrimary = true
		}
	}
	if !foundPrimary {
		return m, orgInvalid("主部门必须包含在成员部门中")
	}
	if len(m.ProjectMemberships) > 100 {
		return m, orgInvalid("项目授权数量超过限制")
	}
	seen := map[string]bool{}
	oldProjects := map[string]string{}
	for _, p := range old.ProjectMemberships {
		oldProjects[p.ProjectID] = p.Role
	}
	for _, p := range m.ProjectMemberships {
		if seen[p.ProjectID] || p.ProjectID == "" || (!validProjectRole(p.Role) && !(p.Role == "tenant_admin" && oldProjects[p.ProjectID] == p.Role)) {
			return m, orgInvalid("项目或项目角色无效")
		}
		seen[p.ProjectID] = true
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM projects WHERE tenant_id=? AND id=?`, tenantID, p.ProjectID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return m, orgInvalid("项目不存在或不属于当前企业")
		}
		if err != nil {
			return m, err
		}
		if status != "active" && oldProjects[p.ProjectID] != p.Role {
			return m, orgInvalid("不能授权已归档项目")
		}
		if !admin && (p.Role == "project_admin" || p.Role == "tenant_admin") && oldProjects[p.ProjectID] != p.Role {
			return m, orgForbidden()
		}
	}
	if !admin {
		for pid, role := range oldProjects {
			if role == "project_admin" || role == "tenant_admin" {
				unchanged := false
				for _, p := range m.ProjectMemberships {
					unchanged = unchanged || (p.ProjectID == pid && p.Role == role)
				}
				if !unchanged {
					return m, orgForbidden()
				}
			}
		}
	}
	now := orgNow()
	changedAt := ""
	if passwordChanged {
		changedAt = now
	}
	if creating {
		m.ID, err = organizationID("u_")
		if err != nil {
			return m, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO users(id,tenant_id,name,email,active,department,employee_no,last_active,password_hash,password_changed_at,directory_source,directory_external_id)VALUES(?,?,?,?,?,?,?,'',?,?,'local',?)`, m.ID, tenantID, m.Name, m.Email, m.Active, primaryName, m.EmployeeNo, passwordHash, changedAt, "local:"+m.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE users SET name=?,email=?,active=?,department=?,employee_no=?,password_hash=?,password_changed_at=CASE WHEN ? THEN ? ELSE password_changed_at END WHERE tenant_id=? AND id=?`, m.Name, m.Email, m.Active, primaryName, m.EmployeeNo, passwordHash, passwordChanged, now, tenantID, m.ID)
	}
	if err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET operation_disabled=? WHERE tenant_id=? AND id=?`, m.OperationDisabled, tenantID, m.ID); err != nil {
		return m, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET must_change_password=? WHERE tenant_id=? AND id=?`, m.MustChangePassword, tenantID, m.ID); err != nil {
		return m, err
	}
	status := "active"
	if !m.Active {
		status = "disabled"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,user_id) DO UPDATE SET role=excluded.role,status=excluded.status,updated_at=excluded.updated_at`, tenantID, m.ID, m.TenantRole, status, now, now)
	if err != nil {
		return m, err
	}
	// Replace only this tenant/user's canonical assignments. Never infer a
	// department by display text or accidentally retain an old primary flag.
	if _, err = tx.ExecContext(ctx, `DELETE FROM department_memberships WHERE tenant_id=? AND user_id=?`, tenantID, m.ID); err != nil {
		return m, err
	}
	for _, dep := range m.DepartmentIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,status,joined_at,updated_at)VALUES(?,?,?,?,'active',?,?)`, tenantID, dep, m.ID, dep == m.PrimaryDepartmentID, now, now); err != nil {
			return m, err
		}
	}
	for _, table := range []string{"memberships", "project_members"} {
		// 归档/删除项目的历史绑定不在可编辑目录展示，也不能在保存其他资料时被清掉。
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE tenant_id=? AND user_id=? AND project_id IN (SELECT id FROM projects WHERE tenant_id=? AND status='active')`, tenantID, m.ID, tenantID); err != nil {
			return m, err
		}
	}
	for _, p := range m.ProjectMemberships {
		if _, err = tx.ExecContext(ctx, `INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,?,?)`, tenantID, p.ProjectID, m.ID, p.Role); err != nil {
			return m, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,?,?,?)`, tenantID, p.ProjectID, m.ID, p.Role, now, now); err != nil {
			return m, err
		}
	}
	// 停用、改密码、改邮箱或企业角色会撤销该用户会话及代访问；仅 operationDisabled 则保留登录但结束代访问。
	if !creating && (!m.Active || passwordChanged || old.Email != m.Email || old.TenantRole != m.TenantRole) {
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND revoked_at IS NULL`, now, tenantID, m.ID); err != nil {
			return m, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (target_user_id=? OR admin_user_id=?) AND ended_at IS NULL`, now, tenantID, m.ID, m.ID); err != nil {
			return m, err
		}
	}
	if !creating && m.OperationDisabled && !old.OperationDisabled {
		// Do not revoke login sessions: the suspended user needs the explanatory
		// session page. End delegated identity sessions, which confer operations.
		if _, err = tx.ExecContext(ctx, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (target_user_id=? OR admin_user_id=?) AND ended_at IS NULL`, now, tenantID, m.ID, m.ID); err != nil {
			return m, err
		}
	}
	err = a.organizationAudit(ctx, tx, "user", m.ID, "organization_member_saved", before, map[string]any{"member": m, "passwordChanged": passwordChanged})
	return m, err
}

// removeOrganizationMember 只撤销当前企业内的成员关系：用户、工作项和审计历史仍保留，
// 以便历史负责人、评论和统计可以继续追溯。所有关联解除与会话失效必须同事务提交。
func (a *App) removeOrganizationMember(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return orgInvalid("成员 ID 不能为空")
	}
	if id == a.uid() {
		return &organizationError{409, "cannot_delete_self", "不能删除当前账号"}
	}
	members, err := organizationMemberList(ctx, tx, id)
	if err != nil {
		return err
	}
	if len(members) != 1 {
		return orgNotFound()
	}
	target := members[0]
	// 与成员停用逻辑保持一致：可用管理员至少保留一名，且在写事务内复核，防止并发删除绕过约束。
	if target.TenantRole == "tenant_admin" && target.Active && !target.OperationDisabled {
		var availableAdmins int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.role='tenant_admin' AND tm.status='active' AND u.active=1 AND u.operation_disabled=0 AND u.id!=?`, tenantID, target.ID).Scan(&availableAdmins)
		if err != nil {
			return err
		}
		if availableAdmins == 0 {
			return &organizationError{409, "last_admin", "不能删除最后一个企业管理员"}
		}
	}

	now := orgNow()
	// tenant_memberships 是成员关系的权威记录。使用 removed 软删除而不是删除 users，
	// 可避免破坏需求、缺陷、评论和审计中保存的历史用户 ID。
	if _, err = tx.ExecContext(ctx, `UPDATE tenant_memberships SET status='removed',updated_at=? WHERE tenant_id=? AND user_id=?`, now, tenantID, target.ID); err != nil {
		return err
	}
	// 双重收敛：即使未来有遗漏 status 过滤的读取路径，已移除账号也不会再认证或执行业务操作。
	if _, err = tx.ExecContext(ctx, `UPDATE users SET active=0,operation_disabled=1 WHERE tenant_id=? AND id=?`, tenantID, target.ID); err != nil {
		return err
	}
	// 以下均为实时授权关系，移除后不应继续出现在项目、部门或权限组的成员集合中；
	// 工作项、评论、通知及审计记录刻意不删除。
	for _, statement := range []string{
		`DELETE FROM department_memberships WHERE tenant_id=? AND user_id=?`,
		`DELETE FROM organization_group_members WHERE tenant_id=? AND user_id=?`,
		`DELETE FROM memberships WHERE tenant_id=? AND user_id=?`,
		`DELETE FROM project_members WHERE tenant_id=? AND user_id=?`,
		// Webhook 密钥属于已移除账号的可撤销凭据，需随成员关系一并清除。
		`DELETE FROM user_wecom_webhooks WHERE tenant_id=? AND user_id=?`,
	} {
		if _, err = tx.ExecContext(ctx, statement, tenantID, target.ID); err != nil {
			return err
		}
	}
	// 立即终止登录及所有可能涉及该账号的代访问会话，避免已签发 cookie 继续使用。
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND revoked_at IS NULL`, now, tenantID, target.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (target_user_id=? OR admin_user_id=?) AND ended_at IS NULL`, now, tenantID, target.ID, target.ID); err != nil {
		return err
	}
	return a.organizationAudit(ctx, tx, "user", target.ID, "organization_member_removed", target, map[string]any{"membershipStatus": "removed", "relationshipsRevoked": true})
}

func (a *App) organizationMembers(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 && parts[0] == "bulk" {
		a.organizationMembersBulk(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "wecom-webhook" {
		a.userWecomWebhook(w, r, parts[0])
		return
	}
	if len(parts) > 0 && parts[0] == "import" {
		a.organizationImport(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "export" {
		a.organizationExport(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		tx, admin, err := a.beginOrganizationWrite(r, "members.manage")
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer tx.Rollback()
		// 删除企业成员属于不可由权限组委派的高风险操作，后端不能依赖前端隐藏按钮。
		if !admin {
			failOrganization(w, orgForbidden())
			return
		}
		if err = a.removeOrganizationMember(r.Context(), tx, parts[0]); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, http.StatusOK, map[string]bool{"deleted": true})
		return
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "organization.read"); err != nil {
			failOrganization(w, err)
			return
		}
		items, err := organizationMemberList(r.Context(), a.db, "")
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if !((len(parts) == 0 && r.Method == http.MethodPost) || (len(parts) == 1 && r.Method == http.MethodPatch)) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b organizationMemberPatch
	if err := decodeOrganizationJSON(w, r, &b); err != nil {
		failOrganization(w, err)
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "members.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	id := ""
	if len(parts) == 1 {
		id = parts[0]
	}
	m, err := a.saveOrganizationMember(r.Context(), tx, admin, id, b)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	write(w, status, m)
}

// The legacy project-members endpoint must not be a second, weaker route into
// tenant account administration. A project admin can only adjust an existing
// member's role in that same project; all account changes use enterprise grants.
func (a *App) legacyMemberWrite(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&raw) != nil {
		failOrganization(w, orgInvalid("请求格式不正确"))
		return
	}
	id := ""
	if r.Method == http.MethodPatch {
		if json.Unmarshal(raw["id"], &id) != nil || id == "" {
			failOrganization(w, orgInvalid("成员 ID 不能为空"))
			return
		}
	}
	delete(raw, "id")
	permissions, admin, err := a.organizationAccess(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if !permissions["members.manage"] {
		if a.impersonation != nil || r.Method != http.MethodPatch || len(raw) != 1 || raw["projectRole"] == nil {
			failOrganization(w, orgForbidden())
			return
		}
		var role string
		if json.Unmarshal(raw["projectRole"], &role) != nil || !validProjectRole(role) {
			failOrganization(w, orgInvalid("项目或项目角色无效"))
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
		var n int
		err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM project_members pm JOIN users u ON u.tenant_id=pm.tenant_id AND u.id=pm.user_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE pm.tenant_id=? AND pm.project_id=? AND pm.user_id=? AND pm.role='project_admin' AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), a.uid()).Scan(&n)
		if err == nil && n != 1 {
			err = orgForbidden()
		}
		var before string
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT role FROM project_members WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, a.pid(), id).Scan(&before)
			if errors.Is(err, sql.ErrNoRows) {
				err = orgNotFound()
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE project_members SET role=?,updated_at=? WHERE tenant_id=? AND project_id=? AND user_id=?`, role, orgNow(), tenantID, a.pid(), id)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE memberships SET role=? WHERE tenant_id=? AND project_id=? AND user_id=?`, role, tenantID, a.pid(), id)
		}
		if err == nil {
			err = a.organizationAudit(r.Context(), tx, "user", id, "project_member_role_changed", map[string]string{"projectId": a.pid(), "role": before}, map[string]string{"projectId": a.pid(), "role": role})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]bool{"updated": true})
		return
	}
	var projectRole *string
	if value, ok := raw["projectRole"]; ok {
		var role string
		if json.Unmarshal(value, &role) != nil || !validProjectRole(role) {
			failOrganization(w, orgInvalid("项目或项目角色无效"))
			return
		}
		projectRole = &role
		delete(raw, "projectRole")
	}
	var department *string
	if value, ok := raw["department"]; ok {
		var name string
		if json.Unmarshal(value, &name) != nil {
			failOrganization(w, orgInvalid("部门不存在或不属于当前企业"))
			return
		}
		department = &name
		delete(raw, "department")
	}
	encoded, _ := json.Marshal(raw)
	var b organizationMemberPatch
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&b) != nil {
		failOrganization(w, orgInvalid("请求格式不正确"))
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "members.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	if department != nil {
		ids := []string{}
		primary := ""
		if strings.TrimSpace(*department) != "" {
			rows, err := tx.QueryContext(r.Context(), `SELECT id FROM departments WHERE tenant_id=? AND name=? AND status='active'`, tenantID, strings.TrimSpace(*department))
			if err != nil {
				failOrganization(w, err)
				return
			}
			for rows.Next() {
				var dep string
				if err = rows.Scan(&dep); err != nil {
					rows.Close()
					failOrganization(w, err)
					return
				}
				ids = append(ids, dep)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				failOrganization(w, err)
				return
			}
			if len(ids) != 1 {
				failOrganization(w, orgInvalid("部门不存在或名称不唯一，请选择部门 ID"))
				return
			}
			primary = ids[0]
		}
		b.DepartmentIDs = &ids
		b.PrimaryDepartmentID = &primary
	}
	if projectRole != nil || r.Method == http.MethodPost {
		projects := []organizationProjectMembership{}
		if id != "" {
			members, err := organizationMemberList(r.Context(), tx, id)
			if err != nil {
				failOrganization(w, err)
				return
			}
			if len(members) != 1 {
				failOrganization(w, orgNotFound())
				return
			}
			projects = members[0].ProjectMemberships
		}
		role := "viewer"
		if projectRole != nil {
			role = *projectRole
		}
		found := false
		for i := range projects {
			if projects[i].ProjectID == a.pid() {
				projects[i].Role = role
				found = true
			}
		}
		if !found {
			projects = append(projects, organizationProjectMembership{a.pid(), role})
		}
		b.ProjectMemberships = &projects
	}
	m, err := a.saveOrganizationMember(r.Context(), tx, admin, id, b)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	write(w, status, m)
}
