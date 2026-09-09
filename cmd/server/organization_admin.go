package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type organizationError struct {
	Status        int
	Code, Message string
}

func (e *organizationError) Error() string { return e.Message }
func orgInvalid(message string) error      { return &organizationError{422, "validation_error", message} }
func orgConflict(message string) error {
	return &organizationError{409, "organization_conflict", message}
}
func orgForbidden() error {
	return &organizationError{403, "organization_forbidden", "没有此企业管理权限"}
}
func orgNotFound() error { return &organizationError{404, "not_found", "记录不存在"} }
func failOrganization(w http.ResponseWriter, err error) {
	var problem *organizationError
	if errors.As(err, &problem) {
		fail(w, problem.Status, problem.Code, problem.Message)
		return
	}
	fail(w, 503, "organization_unavailable", "企业管理服务暂时不可用，请稍后重试")
}
func organizationID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
func orgNow() string { return time.Now().UTC().Format(time.RFC3339) }

type organizationPermission struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Delegable   bool   `json:"delegable"`
}

var organizationPermissions = []organizationPermission{
	{"organization.read", "查看企业目录", "查看企业成员、部门与项目目录", true},
	{"departments.manage", "管理部门", "新建、修改和删除未使用的部门", true},
	{"members.manage", "管理普通成员", "管理普通成员资料、部门与非管理员项目权限", true},
	{"members.import", "导入成员", "批量创建未激活的普通成员", true},
	{"members.export", "导出成员", "导出不含凭据的成员目录", true},
	{"invitations.manage", "管理申请链接", "创建和撤销组织加入申请链接", true},
	{"applications.review", "审批加入申请", "审核申请并为普通成员分配登录和项目权限", true},
	{"reports.view", "查看团队统计", "查看企业人员、部门与职能的月度工作统计", true},
	{"groups.manage", "管理权限组", "仅企业管理员可授予和撤销企业管理权限", false},
	{"members.security", "管理管理员权限", "仅企业管理员可调整企业管理员及项目管理员权限", false},
}
var organizationRoles = []map[string]string{
	{"key": "project_admin", "name": "项目管理员"}, {"key": "product", "name": "产品"}, {"key": "frontend", "name": "前端工程师"},
	{"key": "backend", "name": "后端工程师"}, {"key": "algorithm", "name": "算法工程师"}, {"key": "ui", "name": "UI 设计师"},
	{"key": "frontend_lead", "name": "前端组长"}, {"key": "backend_lead", "name": "后端组长"}, {"key": "qa", "name": "测试"}, {"key": "viewer", "name": "只读成员"},
}

func (a *App) migrateOrganizationAdministration() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS organization_write_locks(tenant_id TEXT PRIMARY KEY,revision INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS organization_groups(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,name));
CREATE TABLE IF NOT EXISTS organization_group_permissions(tenant_id TEXT NOT NULL,group_id TEXT NOT NULL,permission TEXT NOT NULL,PRIMARY KEY(tenant_id,group_id,permission));
CREATE TABLE IF NOT EXISTS organization_group_members(tenant_id TEXT NOT NULL,group_id TEXT NOT NULL,user_id TEXT NOT NULL,PRIMARY KEY(tenant_id,group_id,user_id));
CREATE INDEX IF NOT EXISTS idx_organization_group_user ON organization_group_members(tenant_id,user_id);
CREATE TABLE IF NOT EXISTS organization_invitations(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,name TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,department_ids_json TEXT NOT NULL,allowed_roles_json TEXT NOT NULL,expires_at TEXT NOT NULL,max_uses INTEGER NOT NULL,uses INTEGER NOT NULL DEFAULT 0,created_by TEXT NOT NULL,created_at TEXT NOT NULL,revoked_at TEXT);
CREATE TABLE IF NOT EXISTS organization_applications(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,invitation_id TEXT NOT NULL,name TEXT NOT NULL,department_id TEXT NOT NULL,requested_role TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',created_at TEXT NOT NULL,reviewed_by TEXT NOT NULL DEFAULT '',reviewed_at TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL DEFAULT '',user_id TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS idx_organization_pending_application ON organization_applications(tenant_id,invitation_id,name,department_id,requested_role) WHERE status='pending';
CREATE INDEX IF NOT EXISTS idx_organization_application_status ON organization_applications(tenant_id,status,created_at);
CREATE TABLE IF NOT EXISTS organization_public_rate_limits(key_hash TEXT PRIMARY KEY,window_start INTEGER NOT NULL,count INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS organization_import_previews(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,actor_id TEXT NOT NULL,rows_json TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL,consumed_at TEXT);
INSERT OR IGNORE INTO organization_write_locks(tenant_id) SELECT id FROM tenants;`)
	return err
}

// Every grant is resolved from authoritative tenant membership, never from a
// selected project's role. Unknown/corrupt permission records fail closed.
// 组织权限只从有效企业成员和可委派用户组读取，不能用当前项目角色替代；未知权限不会自动放行。
func (a *App) organizationAccess(ctx context.Context, store stateStore) (map[string]bool, bool, error) {
	if err := a.requireOperationAccess(ctx, store); err != nil {
		return nil, false, err
	}
	var role, status string
	var active bool
	err := store.QueryRowContext(ctx, `SELECT tm.role,tm.status,u.active FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id=?`, tenantID, a.uid()).Scan(&role, &status, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, orgForbidden()
	}
	if err != nil {
		return nil, false, err
	}
	if !active || status != "active" {
		return nil, false, orgForbidden()
	}
	permissions := map[string]bool{}
	if role == "tenant_admin" {
		for _, p := range organizationPermissions {
			permissions[p.Key] = true
		}
		return permissions, true, nil
	}
	rows, err := store.QueryContext(ctx, `SELECT DISTINCT gp.permission FROM organization_group_permissions gp JOIN organization_groups g ON g.tenant_id=gp.tenant_id AND g.id=gp.group_id JOIN organization_group_members gm ON gm.tenant_id=g.tenant_id AND gm.group_id=g.id WHERE gm.tenant_id=? AND gm.user_id=?`, tenantID, a.uid())
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, false, err
		}
		for _, p := range organizationPermissions {
			if p.Delegable && p.Key == key {
				permissions[key] = true
				permissions["organization.read"] = true
			}
		}
	}
	return permissions, false, rows.Err()
}
func (a *App) requireOrganizationPermission(ctx context.Context, store stateStore, permission string) (bool, error) {
	permissions, admin, err := a.organizationAccess(ctx, store)
	if err != nil {
		return false, err
	}
	if !permissions[permission] {
		return false, orgForbidden()
	}
	return admin, nil
}

// 所有组织变更先排除代访问，再在写事务内复核权限及最后管理员等约束，防止校验后并发撤权。
func (a *App) beginOrganizationWrite(r *http.Request, permission string) (*sql.Tx, bool, error) {
	if a.impersonation != nil {
		return nil, false, &organizationError{403, "impersonation_restricted", "代访问期间不能修改账号安全或成员权限"}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, false, err
	}
	// Acquire the SQLite writer before checking grants/invariants: a concurrent
	// group revocation or last-admin update cannot invalidate a stale read.
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		tx.Rollback()
		return nil, false, err
	}
	admin, err := a.requireOrganizationPermission(r.Context(), tx, permission)
	if err == nil {
		err = a.requireAdministrationSession(r.Context(), tx)
	}
	if err != nil {
		tx.Rollback()
		return nil, false, err
	}
	return tx, admin, nil
}

// 审计与业务写入共用事务；调用方只能传脱敏 DTO，不能直接记录带 initialPassword 的原始请求。
func (a *App) organizationAudit(ctx context.Context, tx *sql.Tx, kind, id, action string, before, after any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,'',?,?,?,?,?,?,?)`, tenantID, a.uid(), kind, id, action, jsonText(before), jsonText(after), orgNow())
	return err
}
func decodeOrganizationJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return &organizationError{415, "json_required", "企业管理请求必须使用 JSON"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return orgInvalid("请求格式不正确")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return orgInvalid("请求格式不正确")
	}
	return nil
}
func stringSet(values []string, max int) ([]string, error) {
	if len(values) > max {
		return nil, orgInvalid("选项数量超过限制")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || v != strings.TrimSpace(v) || len(v) > 100 || seen[v] {
			return nil, orgInvalid("选项包含空值、重复项或无效 ID")
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, nil
}
func validOrgText(v string, min, max int) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) >= min && utf8.RuneCountInString(v) <= max && !strings.ContainsAny(v, "\x00\r\n")
}

func (a *App) organizationAdmin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/organization/")
	parts := strings.Split(path, "/")
	if path == "admin" && r.Method == http.MethodGet {
		a.organizationOverview(w, r)
		return
	}
	if path == "permissions" && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "organization.read"); err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": organizationPermissions})
		return
	}
	if path == "server-monitor" {
		a.serverMonitor(w, r)
		return
	}
	switch parts[0] {
	case "departments":
		a.organizationDepartments(w, r, parts[1:])
	case "groups":
		a.organizationGroups(w, r, parts[1:])
	case "members":
		a.organizationMembers(w, r, parts[1:])
	case "invitations":
		a.organizationInvitations(w, r, parts[1:])
	case "applications":
		a.organizationApplications(w, r, parts[1:])
	default:
		fail(w, 404, "not_found", "记录不存在")
	}
}
func (a *App) organizationOverview(w http.ResponseWriter, r *http.Request) {
	permissions, admin, err := a.organizationAccess(r.Context(), a.db)
	if err == nil && !permissions["organization.read"] {
		err = orgForbidden()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	var name string
	if err = a.db.QueryRowContext(r.Context(), `SELECT name FROM tenants WHERE id=?`, tenantID).Scan(&name); err != nil {
		failOrganization(w, err)
		return
	}
	keys := []string{}
	for k := range permissions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	counts := map[string]int{}
	for key, query := range map[string]string{"members": `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND tm.status!='removed'`, "activeMembers": `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.active=1 AND tm.status='active'`, "departments": `SELECT COUNT(*) FROM departments WHERE tenant_id=?`, "groups": `SELECT COUNT(*) FROM organization_groups WHERE tenant_id=?`, "pendingApplications": `SELECT COUNT(*) FROM organization_applications WHERE tenant_id=? AND status='pending'`} {
		var n int
		if err = a.db.QueryRowContext(r.Context(), query, tenantID).Scan(&n); err != nil {
			failOrganization(w, err)
			return
		}
		counts[key] = n
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,code,status FROM projects WHERE tenant_id=? AND (status='active' OR (status='archived' AND ?)) ORDER BY name`, tenantID, admin)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer rows.Close()
	projects := []map[string]string{}
	for rows.Next() {
		var id, n, c, s string
		if err = rows.Scan(&id, &n, &c, &s); err != nil {
			failOrganization(w, err)
			return
		}
		projects = append(projects, map[string]string{"id": id, "name": n, "code": c, "status": s})
	}
	if err = rows.Err(); err != nil {
		failOrganization(w, err)
		return
	}
	temporary, configurationErr := configuredInitialPassword()
	write(w, 200, map[string]any{"organization": map[string]string{"id": tenantID, "name": name}, "permissions": keys, "isTenantAdmin": admin, "counts": counts, "roles": organizationRoles, "projects": projects, "initialPasswordConfigured": configurationErr == nil && temporary != ""})
}

type organizationDepartment struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	ParentID    *string `json:"parentId"`
	Status      string  `json:"status"`
	SortOrder   int     `json:"sortOrder"`
	MemberCount int     `json:"memberCount"`
}

func organizationDepartmentList(ctx context.Context, store stateStore) ([]organizationDepartment, error) {
	rows, err := store.QueryContext(ctx, `SELECT d.id,d.name,d.code,d.parent_id,d.status,d.sort_order,(SELECT COUNT(*) FROM department_memberships dm WHERE dm.tenant_id=d.tenant_id AND dm.department_id=d.id AND dm.status='active') FROM departments d WHERE d.tenant_id=? ORDER BY d.sort_order,d.name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []organizationDepartment{}
	for rows.Next() {
		var d organizationDepartment
		if err = rows.Scan(&d.ID, &d.Name, &d.Code, &d.ParentID, &d.Status, &d.SortOrder, &d.MemberCount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

var organizationCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// 部门层级、跨企业引用、同级重名及删除引用均在事务中校验；删除不应隐式迁移成员或清空字段配置。
func (a *App) organizationDepartments(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "organization.read"); err != nil {
			failOrganization(w, err)
			return
		}
		items, err := organizationDepartmentList(r.Context(), a.db)
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if !((len(parts) == 0 && r.Method == http.MethodPost) || (len(parts) == 1 && (r.Method == http.MethodPatch || r.Method == http.MethodDelete))) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b struct {
		Name      *string         `json:"name"`
		Code      *string         `json:"code"`
		ParentID  json.RawMessage `json:"parentId"`
		Status    *string         `json:"status"`
		SortOrder *int            `json:"sortOrder"`
	}
	if r.Method != http.MethodDelete {
		if err := decodeOrganizationJSON(w, r, &b); err != nil {
			failOrganization(w, err)
			return
		}
	}
	tx, _, err := a.beginOrganizationWrite(r, "departments.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	items, err := organizationDepartmentList(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	byID := map[string]organizationDepartment{}
	for _, d := range items {
		byID[d.ID] = d
	}
	d := organizationDepartment{Status: "active"}
	var before any = nil
	if len(parts) == 1 {
		var ok bool
		d, ok = byID[parts[0]]
		if !ok {
			failOrganization(w, orgNotFound())
			return
		}
		before = d
	} else {
		d.ID, err = organizationID("dept_")
		if err != nil {
			failOrganization(w, err)
			return
		}
		d.Code = "D-" + d.ID[len(d.ID)-12:]
	}
	if r.Method == http.MethodDelete {
		var refs int
		err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM departments WHERE tenant_id=? AND parent_id=?)+(SELECT COUNT(*) FROM department_memberships WHERE tenant_id=? AND department_id=?)+(SELECT COUNT(*) FROM field_definitions WHERE tenant_id=? AND department_id=?)+(SELECT COUNT(*) FROM organization_applications WHERE tenant_id=? AND department_id=? AND status='pending')`, tenantID, d.ID, tenantID, d.ID, tenantID, d.ID, tenantID, d.ID).Scan(&refs)
		if err == nil && refs > 0 {
			err = orgConflict("部门仍有子部门、成员或配置引用，不能删除")
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM departments WHERE tenant_id=? AND id=?`, tenantID, d.ID)
		}
		if err == nil {
			err = a.organizationAudit(r.Context(), tx, "department", d.ID, "department_deleted", before, nil)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]bool{"deleted": true})
		return
	}
	if b.Name != nil {
		d.Name = strings.TrimSpace(*b.Name)
	}
	if b.Code != nil {
		if value := strings.TrimSpace(*b.Code); value != "" || len(parts) != 0 {
			d.Code = value
		}
	}
	if b.Status != nil {
		d.Status = *b.Status
	}
	if b.SortOrder != nil {
		d.SortOrder = *b.SortOrder
	}
	if b.ParentID != nil {
		if err = json.Unmarshal(b.ParentID, &d.ParentID); err != nil {
			failOrganization(w, orgInvalid("上级部门无效"))
			return
		}
		if d.ParentID != nil && *d.ParentID == "" {
			d.ParentID = nil
		}
	}
	if !validOrgText(d.Name, 1, 80) || !organizationCodePattern.MatchString(d.Code) || !validChoice(d.Status, []string{"active", "inactive"}) || d.SortOrder < 0 || d.SortOrder > 100000 {
		failOrganization(w, orgInvalid("部门名称、编码、状态或排序无效"))
		return
	}
	seen := map[string]bool{d.ID: true}
	parent := d.ParentID
	for parent != nil {
		p, ok := byID[*parent]
		if !ok || p.Status != "active" {
			failOrganization(w, orgInvalid("上级部门不存在或已停用"))
			return
		}
		if seen[p.ID] {
			failOrganization(w, orgInvalid("部门层级不能循环引用"))
			return
		}
		seen[p.ID] = true
		parent = p.ParentID
	}
	for _, existing := range items {
		if existing.ID != d.ID && (strings.EqualFold(existing.Code, d.Code) || (existing.Name == d.Name && sameOptionalString(existing.ParentID, d.ParentID))) {
			failOrganization(w, orgConflict("部门编码或同级名称已存在"))
			return
		}
	}
	now := orgNow()
	if len(parts) == 0 {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO departments(id,tenant_id,parent_id,name,code,source,external_id,status,sort_order,created_at,updated_at)VALUES(?,?,?,?,?,'local',?,?,?,?,?)`, d.ID, tenantID, d.ParentID, d.Name, d.Code, "local:"+d.ID, d.Status, d.SortOrder, now, now)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE departments SET parent_id=?,name=?,code=?,status=?,sort_order=?,updated_at=? WHERE tenant_id=? AND id=?`, d.ParentID, d.Name, d.Code, d.Status, d.SortOrder, now, tenantID, d.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE users SET department=? WHERE tenant_id=? AND EXISTS(SELECT 1 FROM department_memberships dm WHERE dm.tenant_id=users.tenant_id AND dm.user_id=users.id AND dm.department_id=? AND dm.is_primary=1 AND dm.status='active')`, d.Name, tenantID, d.ID)
	}
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "department", d.ID, "department_saved", before, d)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	status := 200
	if len(parts) == 0 {
		status = 201
	}
	write(w, status, d)
}
func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type organizationGroup struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	MemberIDs   []string `json:"memberIds"`
	MemberCount int      `json:"memberCount"`
}

func organizationGroupList(ctx context.Context, store stateStore) ([]organizationGroup, error) {
	rows, err := store.QueryContext(ctx, `SELECT id,name,description FROM organization_groups WHERE tenant_id=? ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	out := []organizationGroup{}
	for rows.Next() {
		var g organizationGroup
		if err = rows.Scan(&g.ID, &g.Name, &g.Description); err != nil {
			rows.Close()
			return nil, err
		}
		g.Permissions = []string{}
		g.MemberIDs = []string{}
		out = append(out, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		for _, spec := range []struct {
			query string
			dest  *[]string
		}{{`SELECT permission FROM organization_group_permissions WHERE tenant_id=? AND group_id=? ORDER BY permission`, &out[i].Permissions}, {`SELECT user_id FROM organization_group_members WHERE tenant_id=? AND group_id=? ORDER BY user_id`, &out[i].MemberIDs}} {
			rows, err = store.QueryContext(ctx, spec.query, tenantID, out[i].ID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var s string
				if err = rows.Scan(&s); err != nil {
					rows.Close()
					return nil, err
				}
				*spec.dest = append(*spec.dest, s)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		out[i].MemberCount = len(out[i].MemberIDs)
	}
	return out, nil
}

// 用户组只授予权限目录中标记可委派的能力；安全管理权限不能经用户组间接提升。
func (a *App) organizationGroups(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		if _, err := a.requireOrganizationPermission(r.Context(), a.db, "groups.manage"); err != nil {
			failOrganization(w, err)
			return
		}
		items, err := organizationGroupList(r.Context(), a.db)
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if !((len(parts) == 0 && r.Method == http.MethodPost) || (len(parts) == 1 && (r.Method == http.MethodPatch || r.Method == http.MethodDelete))) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Permissions *[]string `json:"permissions"`
		MemberIDs   *[]string `json:"memberIds"`
	}
	if r.Method != http.MethodDelete {
		if err := decodeOrganizationJSON(w, r, &b); err != nil {
			failOrganization(w, err)
			return
		}
	}
	tx, _, err := a.beginOrganizationWrite(r, "groups.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	groups, err := organizationGroupList(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	g := organizationGroup{Permissions: []string{}, MemberIDs: []string{}}
	var before any
	if len(parts) == 1 {
		found := false
		for _, old := range groups {
			if old.ID == parts[0] {
				g = old
				found = true
			}
		}
		if !found {
			failOrganization(w, orgNotFound())
			return
		}
		before = g
	} else {
		g.ID, err = organizationID("grp_")
		if err != nil {
			failOrganization(w, err)
			return
		}
	}
	if r.Method == http.MethodDelete {
		for _, table := range []string{"organization_group_permissions", "organization_group_members", "organization_groups"} {
			column := "group_id"
			if table == "organization_groups" {
				column = "id"
			}
			if _, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE tenant_id=? AND `+column+`=?`, tenantID, g.ID); err != nil {
				break
			}
		}
		if err == nil {
			err = a.organizationAudit(r.Context(), tx, "organization_group", g.ID, "group_deleted", before, nil)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]bool{"deleted": true})
		return
	}
	if b.Name != nil {
		g.Name = strings.TrimSpace(*b.Name)
	}
	if b.Description != nil {
		g.Description = strings.TrimSpace(*b.Description)
	}
	if b.Permissions != nil {
		g.Permissions = *b.Permissions
	}
	if b.MemberIDs != nil {
		g.MemberIDs = *b.MemberIDs
	}
	if !validOrgText(g.Name, 1, 80) || utf8.RuneCountInString(g.Description) > 500 {
		failOrganization(w, orgInvalid("用户组名称或描述无效"))
		return
	}
	for _, old := range groups {
		if old.ID != g.ID && old.Name == g.Name {
			failOrganization(w, orgConflict("用户组名称已存在"))
			return
		}
	}
	g.Permissions, err = stringSet(g.Permissions, 20)
	if err == nil {
		g.MemberIDs, err = stringSet(g.MemberIDs, 1000)
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	for _, key := range g.Permissions {
		valid := false
		for _, p := range organizationPermissions {
			valid = valid || (p.Delegable && p.Key == key)
		}
		if !valid {
			failOrganization(w, orgInvalid("用户组包含不可委派的权限"))
			return
		}
	}
	for _, id := range g.MemberIDs {
		var n int
		err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=?`, tenantID, id).Scan(&n)
		if err != nil {
			failOrganization(w, err)
			return
		}
		if n != 1 {
			failOrganization(w, orgInvalid("用户组成员不存在或不属于当前企业"))
			return
		}
	}
	now := orgNow()
	if len(parts) == 0 {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_groups(id,tenant_id,name,description,created_at,updated_at)VALUES(?,?,?,?,?,?)`, g.ID, tenantID, g.Name, g.Description, now, now)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE organization_groups SET name=?,description=?,updated_at=? WHERE tenant_id=? AND id=?`, g.Name, g.Description, now, tenantID, g.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM organization_group_permissions WHERE tenant_id=? AND group_id=?`, tenantID, g.ID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM organization_group_members WHERE tenant_id=? AND group_id=?`, tenantID, g.ID)
	}
	for _, key := range g.Permissions {
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_group_permissions VALUES(?,?,?)`, tenantID, g.ID, key)
		}
	}
	for _, id := range g.MemberIDs {
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_group_members VALUES(?,?,?)`, tenantID, g.ID, id)
		}
	}
	g.MemberCount = len(g.MemberIDs)
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "organization_group", g.ID, "group_saved", before, g)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	status := 200
	if len(parts) == 0 {
		status = 201
	}
	write(w, status, g)
}
