package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// 项目生命周期不依赖当前选中的项目：全部归档后，企业管理员仍必须能管理归档目录。
// deleted 是保留业务数据的墓碑状态，不重用代号，不提供恢复入口，不物理删除附件或关联记录。
func projectProblem(status int, code, message string) error {
	return &organizationError{status, code, message}
}

// 企业没有任何活动项目时只返回管理员身份和组织权限；不伪造项目，也不绕过首登改密守卫。
// 返回 false 表示仍应由中央入口给出原来的项目权限错误。
func (a *App) projectlessAdminSession(w http.ResponseWriter, r *http.Request) bool {
	admin, err := a.projectActor(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return true
	}
	if !admin {
		return false
	}
	var active int
	if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM projects WHERE tenant_id=? AND status='active'`, tenantID).Scan(&active); err != nil {
		failOrganization(w, err)
		return true
	}
	if active > 0 {
		return false
	}
	var name, color, locale, timezone, company string
	if err = a.db.QueryRowContext(r.Context(), `SELECT u.name,u.avatar_color,u.locale,u.timezone,t.name FROM users u JOIN tenants t ON t.id=u.tenant_id WHERE u.tenant_id=? AND u.id=?`, tenantID, a.uid()).Scan(&name, &color, &locale, &timezone, &company); err != nil {
		failOrganization(w, err)
		return true
	}
	permissions := []string{}
	for _, permission := range organizationPermissions {
		permissions = append(permissions, permission.Key)
	}
	write(w, 200, map[string]any{"tenant": map[string]string{"id": tenantID, "name": company}, "project": map[string]string{"id": "", "name": "", "code": ""}, "user": map[string]any{"id": a.uid(), "name": name, "role": "tenant_admin", "avatarColor": color, "locale": storedLocale(locale), "timezone": timezone, "operationDisabled": false, "mustChangePassword": false}, "supportedLocales": supportedLocales, "impersonation": nil, "canImpersonate": true, "organizationPermissions": permissions})
	return true
}

func (a *App) projectActor(ctx context.Context, store stateStore) (bool, error) {
	if err := a.requireOperationAccess(ctx, store); err != nil {
		return false, err
	}
	var role string
	err := store.QueryRowContext(ctx, `SELECT tm.role FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id=? AND tm.status='active' AND u.active=1`, tenantID, a.uid()).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, orgForbidden()
	}
	return role == "tenant_admin", err
}

type lifecycleProject struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Status        string `json:"status"`
	OwnerUserID   string `json:"ownerUserId"`
	Owner         string `json:"owner"`
	Icon          string `json:"icon"`
	Color         string `json:"color"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	ArchivedAt    string `json:"archivedAt"`
	LastVisitedAt string `json:"lastVisitedAt"`
	CurrentRole   string `json:"currentRole"`
	CanManage     bool   `json:"canManage"`
	CanRestore    bool   `json:"canRestore"`
	CanDelete     bool   `json:"canDelete"`
	Members       int    `json:"members"`
	Requirements  int    `json:"requirements"`
	Defects       int    `json:"defects"`
	Sprints       int    `json:"sprints"`
	TestCases     int    `json:"testCases"`
}

const lifecycleProjectSelect = `SELECT p.id,p.code,p.name,p.description,p.status,p.owner_user_id,COALESCE(u.name,''),p.icon,p.color,p.created_at,p.updated_at,COALESCE(p.archived_at,''),COALESCE(v.visited_at,''),COALESCE(pm.role,'') FROM projects p LEFT JOIN users u ON u.tenant_id=p.tenant_id AND u.id=p.owner_user_id LEFT JOIN project_visits v ON v.tenant_id=p.tenant_id AND v.project_id=p.id AND v.user_id=? LEFT JOIN project_members pm ON pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=? WHERE p.tenant_id=?`

func scanLifecycleProject(row interface{ Scan(...any) error }) (lifecycleProject, error) {
	var p lifecycleProject
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Description, &p.Status, &p.OwnerUserID, &p.Owner, &p.Icon, &p.Color, &p.CreatedAt, &p.UpdatedAt, &p.ArchivedAt, &p.LastVisitedAt, &p.CurrentRole)
	return p, err
}

func (a *App) listLifecycleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		a.createLifecycleProject(w, r)
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	admin, err := a.projectActor(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), lifecycleProjectSelect+` AND ((p.status='active' AND (? OR pm.user_id IS NOT NULL)) OR (p.status='archived' AND ?)) ORDER BY CASE WHEN v.visited_at IS NULL THEN 1 ELSE 0 END,v.visited_at DESC,p.updated_at DESC,p.id`, a.uid(), a.uid(), tenantID, admin, admin)
	if err != nil {
		failOrganization(w, err)
		return
	}
	items := []lifecycleProject{}
	search, status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))), r.URL.Query().Get("status")
	for rows.Next() {
		p, e := scanLifecycleProject(rows)
		if e != nil {
			rows.Close()
			failOrganization(w, e)
			return
		}
		if status != "" && status != p.Status || search != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Code+" "+p.Description), search) {
			continue
		}
		p.CanManage = p.Status == "active" && (admin || p.CurrentRole == "project_admin") && a.impersonation == nil
		p.CanRestore = p.Status == "archived" && admin && a.impersonation == nil
		p.CanDelete = p.CanRestore
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		failOrganization(w, err)
		return
	}
	// 先关闭目录游标，再读取摘要，兼容单连接及连接池耗尽场景；任何摘要错误不能伪装成零。
	for i := range items {
		counts, e := a.projectSummary(r.Context(), items[i].ID)
		if e != nil {
			failOrganization(w, e)
			return
		}
		items[i].Members, items[i].Requirements, items[i].Defects, items[i].Sprints, items[i].TestCases = counts["members"], counts["requirements"], counts["defects"], counts["sprints"], counts["testCases"]
	}
	permissions, _, err := a.organizationAccess(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": len(items), "canCreate": admin && a.impersonation == nil, "canViewArchived": admin, "canManageMembers": permissions["members.manage"] && a.impersonation == nil})
}

func (a *App) beginProjectWrite(r *http.Request) (*sql.Tx, bool, error) {
	if a.impersonation != nil {
		return nil, false, projectProblem(403, "impersonation_restricted", "代访问期间不能修改项目配置")
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, false, err
	}
	// 写锁先于身份/角色/项目状态读取：恢复与删除并发时只能有一个合法状态转换获胜。
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		tx.Rollback()
		return nil, false, err
	}
	admin, err := a.projectActor(r.Context(), tx)
	if err == nil {
		err = a.requireAdministrationSession(r.Context(), tx)
	}
	if err != nil {
		tx.Rollback()
		return nil, false, err
	}
	return tx, admin, nil
}

func (a *App) lifecycleAudit(ctx context.Context, tx *sql.Tx, id, action string, before, after any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, id, a.uid(), "project", id, action, jsonText(before), jsonText(after), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (a *App) readLifecycleProject(ctx context.Context, store stateStore, id string) (lifecycleProject, error) {
	p, err := scanLifecycleProject(store.QueryRowContext(ctx, lifecycleProjectSelect+` AND p.id=?`, a.uid(), a.uid(), tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, projectProblem(404, "not_found", "项目不存在")
	}
	return p, err
}

func (a *App) projectLifecycleResource(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/"), "/")
	if len(parts) > 2 || parts[0] == "" {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	id, action := parts[0], ""
	if len(parts) == 2 {
		action = parts[1]
	}
	// Enterprise member grants are independent of the current project selector
	// and must be checked before the ordinary project-read membership gate.
	if action == "members" && (r.Method != http.MethodGet || r.URL.Query().Has("candidates")) {
		a.manageProjectMembers(w, r, id)
		return
	}
	if (action == "archive" || action == "restore") && r.Method == http.MethodPost || action == "" && r.Method == http.MethodDelete {
		a.changeProjectLifecycle(w, r, id, action)
		return
	}
	if action == "" && r.Method == http.MethodPatch {
		a.patchLifecycleProject(w, r, id)
		return
	}
	admin, err := a.projectActor(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	p, err := a.readLifecycleProject(r.Context(), a.db, id)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if p.Status == "deleted" {
		fail(w, 404, "not_found", "项目不存在")
		return
	}
	if p.Status != "active" && !admin || !admin && p.CurrentRole == "" {
		fail(w, 403, "project_forbidden", "无权访问该项目")
		return
	}
	if r.Method == http.MethodGet {
		switch action {
		case "":
			p.CanManage = p.Status == "active" && (admin || p.CurrentRole == "project_admin") && a.impersonation == nil
			p.CanRestore = admin && p.Status == "archived" && a.impersonation == nil
			p.CanDelete = p.CanRestore
			write(w, 200, p)
		case "summary":
			summary, e := a.projectSummary(r.Context(), id)
			if e != nil {
				failOrganization(w, e)
				return
			}
			write(w, 200, summary)
		case "members":
			if p.Status != "active" {
				fail(w, 409, "project_inactive", "归档项目仅支持恢复或删除")
				return
			}
			a.projectMembers(w, r, id)
		default:
			fail(w, 404, "not_found", "项目不存在")
		}
		return
	}
	if action == "visit" && r.Method == http.MethodPost {
		// 用同一 INSERT SELECT 在写入时复核活动状态及成员关系，归档后不产生新的访问记录。
		result, e := a.db.ExecContext(r.Context(), `INSERT INTO project_visits(tenant_id,project_id,user_id,visited_at) SELECT p.tenant_id,p.id,?,? FROM projects p WHERE p.tenant_id=? AND p.id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active')) ON CONFLICT(tenant_id,project_id,user_id) DO UPDATE SET visited_at=excluded.visited_at`, a.uid(), orgNow(), tenantID, id, a.uid(), a.uid())
		if e != nil {
			failOrganization(w, e)
			return
		}
		n, e := result.RowsAffected()
		if e != nil {
			failOrganization(w, e)
			return
		}
		if n == 0 {
			fail(w, 409, "project_inactive", "归档项目仅支持恢复或删除")
			return
		}
		write(w, 200, map[string]bool{"visited": true})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}

func (a *App) changeProjectLifecycle(w http.ResponseWriter, r *http.Request, id, action string) {
	var body struct {
		ConfirmCode string `json:"confirmCode"`
	}
	if r.Method == http.MethodDelete {
		action = "delete"
		if decodeOrganizationJSON(w, r, &body) != nil {
			fail(w, 422, "validation_error", "删除项目必须填写完整项目代号")
			return
		}
	}
	tx, admin, err := a.beginProjectWrite(r)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	p, err := a.readLifecycleProject(r.Context(), tx, id)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if action == "archive" {
		if !admin && p.CurrentRole != "project_admin" {
			fail(w, 403, "admin_required", "仅项目管理员可归档项目")
			return
		}
	} else if !admin {
		fail(w, 403, "admin_required", "仅企业管理员可恢复或删除归档项目")
		return
	}
	if action == "delete" && body.ConfirmCode != p.Code {
		fail(w, 422, "confirmation_mismatch", "项目代号不匹配，未删除项目")
		return
	}
	// 仅完全相同的删除重试幂等；墓碑状态无法借 restore/PATCH/创建相同代号变回 active。
	if action == "delete" && p.Status == "deleted" {
		write(w, 200, map[string]any{"id": id, "status": "deleted", "deleted": true, "alreadyDeleted": true})
		return
	}
	from, to := "active", "archived"
	if action == "restore" {
		from, to = "archived", "active"
	}
	if action == "delete" {
		from, to = "archived", "deleted"
	}
	if p.Status != from {
		fail(w, 409, "project_state_conflict", "项目状态已变化，请刷新后重试")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var archived any = p.ArchivedAt
	if to == "archived" {
		archived = now
	}
	if to == "active" {
		archived = nil
	}
	res, err := tx.ExecContext(r.Context(), `UPDATE projects SET status=?,archived_at=?,updated_at=? WHERE tenant_id=? AND id=? AND status=?`, to, archived, now, tenantID, id, from)
	if err != nil {
		failOrganization(w, err)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		failOrganization(w, err)
		return
	}
	if n != 1 {
		fail(w, 409, "project_state_conflict", "项目状态已变化，请刷新后重试")
		return
	}
	if err = a.lifecycleAudit(r.Context(), tx, id, "project."+action, map[string]any{"status": from, "code": p.Code}, map[string]any{"status": to, "code": p.Code, "retainsBusinessData": true}); err != nil {
		failOrganization(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 200, map[string]any{"id": id, "status": to, "deleted": to == "deleted"})
}

type lifecycleProjectInput struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
	OwnerUserID string `json:"ownerUserId"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
}

func validateLifecycleProject(p lifecycleProjectInput) error {
	if strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 120 || utf8.RuneCountInString(p.Description) > 10000 || utf8.RuneCountInString(p.Icon) > 8 {
		return orgInvalid("项目名称或描述长度不正确")
	}
	if len(p.Code) < 2 || len(p.Code) > 12 {
		return orgInvalid("项目代号须为 2–12 位字母、数字、横线或下划线")
	}
	for _, ch := range p.Code {
		if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return orgInvalid("项目代号须为 2–12 位字母、数字、横线或下划线")
		}
	}
	if len(p.Color) != 7 || p.Color[0] != '#' {
		return orgInvalid("项目颜色格式不正确")
	}
	for _, ch := range p.Color[1:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return orgInvalid("项目颜色格式不正确")
		}
	}
	return nil
}

func (a *App) validateProjectOwner(ctx context.Context, tx *sql.Tx, owner string) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, owner).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return orgInvalid("项目负责人必须是当前企业的有效成员")
	}
	return nil
}

func (a *App) createLifecycleProject(w http.ResponseWriter, r *http.Request) {
	var p lifecycleProjectInput
	if err := decodeOrganizationJSON(w, r, &p); err != nil {
		failOrganization(w, err)
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	if p.OwnerUserID == "" {
		p.OwnerUserID = a.uid()
	}
	if p.Icon == "" {
		p.Icon = "项"
	}
	if p.Color == "" {
		p.Color = "#5B5BD6"
	}
	if err := validateLifecycleProject(p); err != nil {
		failOrganization(w, err)
		return
	}
	tx, admin, err := a.beginProjectWrite(r)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	if !admin {
		fail(w, 403, "admin_required", "仅企业管理员可创建项目")
		return
	}
	if err = a.validateProjectOwner(r.Context(), tx, p.OwnerUserID); err != nil {
		failOrganization(w, err)
		return
	}
	var exists int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM projects WHERE tenant_id=? AND code=?`, tenantID, p.Code).Scan(&exists); err != nil {
		failOrganization(w, err)
		return
	}
	if exists > 0 {
		fail(w, 409, "project_code_exists", "项目代号已存在")
		return
	}
	id, now := "prj_"+strings.ToLower(p.Code), time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO projects(id,tenant_id,name,code,description,status,owner_user_id,icon,color,created_at,updated_at)VALUES(?,?,?,?,?,'active',?,?,?,?,?)`, id, tenantID, p.Name, p.Code, p.Description, p.OwnerUserID, p.Icon, p.Color, now, now); err != nil {
		failOrganization(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,?,'project_admin')`, tenantID, id, a.uid()); err != nil {
		failOrganization(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'project_admin',?,?)`, tenantID, id, a.uid(), now, now); err != nil {
		failOrganization(w, err)
		return
	}
	if err = seedDefaultRequirementCategories(r.Context(), tx, tenantID, id, now); err == nil {
		err = markRequirementCategoryOrderingInitialized(r.Context(), tx, tenantID, id, now)
	}
	if err == nil {
		err = seedRequirementState(r.Context(), tx, tenantID, id)
	}
	if err == nil {
		err = seedDefaultTestersField(tx, id)
	}
	if err == nil {
		err = a.lifecycleAudit(r.Context(), tx, id, "create", nil, p)
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 201, map[string]string{"id": id, "name": p.Name, "code": p.Code})
}

func (a *App) patchLifecycleProject(w http.ResponseWriter, r *http.Request, id string) {
	var patch map[string]any
	if decodeJSON(r, &patch) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	tx, admin, err := a.beginProjectWrite(r)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	p, err := a.readLifecycleProject(r.Context(), tx, id)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if !admin && p.CurrentRole != "project_admin" {
		fail(w, 403, "admin_required", "仅项目管理员可编辑项目")
		return
	}
	if p.Status != "active" {
		fail(w, 409, "project_inactive", "归档项目仅支持恢复或删除")
		return
	}
	next := lifecycleProjectInput{p.Name, p.Code, p.Description, p.OwnerUserID, p.Icon, p.Color}
	for key, raw := range patch {
		value, ok := raw.(string)
		if !ok {
			fail(w, 422, "validation_error", "项目字段必须为文本")
			return
		}
		switch key {
		case "name":
			next.Name = strings.TrimSpace(value)
		case "description":
			next.Description = value
		case "ownerUserId":
			next.OwnerUserID = value
		case "icon":
			next.Icon = value
		case "color":
			next.Color = value
		case "status":
			if value != "active" {
				fail(w, 422, "validation_error", "项目状态请使用归档或恢复操作修改")
				return
			}
		default:
			fail(w, 422, "validation_error", "不支持修改该项目字段")
			return
		}
	}
	if err = validateLifecycleProject(next); err != nil {
		failOrganization(w, err)
		return
	}
	if next.OwnerUserID != p.OwnerUserID {
		if err = a.validateProjectOwner(r.Context(), tx, next.OwnerUserID); err != nil {
			failOrganization(w, err)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE projects SET name=?,description=?,owner_user_id=?,icon=?,color=?,updated_at=? WHERE tenant_id=? AND id=? AND status='active'`, next.Name, next.Description, next.OwnerUserID, next.Icon, next.Color, orgNow(), tenantID, id); err != nil {
		failOrganization(w, err)
		return
	}
	if err = a.lifecycleAudit(r.Context(), tx, id, "project.update", p, next); err != nil {
		failOrganization(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 200, map[string]bool{"updated": true})
}
