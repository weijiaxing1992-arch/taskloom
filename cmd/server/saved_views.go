package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type savedViewFilter struct {
	Field       string          `json:"field"`
	Operator    string          `json:"operator"`
	Value       json.RawMessage `json:"value,omitempty"`
	ReferenceID *int64          `json:"referenceId,omitempty"`
}
type savedViewConfig struct {
	RelatedToMe    bool              `json:"relatedToMe,omitempty"`
	Schema         int               `json:"schema"`
	Q              string            `json:"q"`
	Statuses       []string          `json:"statuses"`
	StatusCategory string            `json:"statusCategory"`
	AssigneeMode   string            `json:"assigneeMode"`
	AssigneeUserID string            `json:"assigneeUserId"`
	Sprint         string            `json:"sprint"`
	SprintID       int64             `json:"sprintId,omitempty"`
	Category       string            `json:"category"`
	CategoryID     int64             `json:"categoryId,omitempty"`
	Priority       string            `json:"priority"`
	Filters        []savedViewFilter `json:"filters"`
	Columns        []string          `json:"columns"`
	Sort           string            `json:"sort"`
	Order          string            `json:"order"`
	View           string            `json:"view"`
}
type savedView struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Scope       string          `json:"scope"`
	OwnerUserID string          `json:"ownerUserId"`
	Version     int64           `json:"version"`
	Config      savedViewConfig `json:"config"`
	CanManage   bool            `json:"canManage"`
	UpdatedAt   string          `json:"updatedAt"`
}

func (a *App) migrateSavedViews() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_saved_views(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,scope TEXT NOT NULL CHECK(scope IN ('personal','shared')),owner_user_id TEXT NOT NULL,owner_key TEXT NOT NULL,name TEXT NOT NULL,name_key TEXT NOT NULL,config_json TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1 CHECK(version>0),updated_at TEXT NOT NULL,CHECK((scope='personal' AND owner_key=owner_user_id) OR (scope='shared' AND owner_key='')),UNIQUE(tenant_id,project_id,scope,owner_key,name_key)); CREATE INDEX IF NOT EXISTS idx_saved_view_access ON requirement_saved_views(tenant_id,project_id,scope,owner_user_id);`)
	return err
}
func (a *App) savedViewAccess(ctx context.Context, store stateStore) (bool, bool, error) {
	var active, disabled, member, admin bool
	var role string
	err := store.QueryRowContext(ctx, `SELECT u.active,u.operation_disabled,EXISTS(SELECT 1 FROM tenant_memberships t WHERE t.tenant_id=u.tenant_id AND t.user_id=u.id AND t.status='active'),EXISTS(SELECT 1 FROM tenant_memberships t WHERE t.tenant_id=u.tenant_id AND t.user_id=u.id AND t.status='active' AND t.role='tenant_admin'),COALESCE((SELECT role FROM project_members p WHERE p.tenant_id=u.tenant_id AND p.project_id=? AND p.user_id=u.id),'') FROM users u WHERE u.tenant_id=? AND u.id=?`, a.pid(), tenantID, a.uid()).Scan(&active, &disabled, &member, &admin, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, &organizationError{403, "forbidden", "账号不可用"}
	}
	if err != nil {
		return false, false, err
	}
	if !active || !member {
		return false, false, &organizationError{403, "forbidden", "账号不可用"}
	}
	if disabled {
		return false, false, &organizationError{403, "account_disabled", operationDisabledMessage}
	}
	var project bool
	if err = store.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE tenant_id=? AND id=?)`, tenantID, a.pid()).Scan(&project); err != nil {
		return false, false, err
	}
	if !project || role == "" && !admin {
		return false, false, &organizationError{403, "project_forbidden", "无权访问该项目"}
	}
	canWrite := role != "viewer" && (role != "" || admin)
	return canWrite, canWrite && (admin || role == "project_admin"), nil
}

var savedViewCustomKey = regexp.MustCompile(`^cf\.[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,120}$`)

func savedViewField(key string) bool {
	if key == "iterationDelayCount" {
		return true
	}
	if savedViewCustomKey.MatchString(key) {
		return true
	}
	if validChoice(key, []string{"code", "title", "type", "category", "status", "priority", "sprint", "owner", "assignee", "discipline", "weightTotal", "tags", "remarks", "description", "acceptance", "parentId", "progress", "estimatedHours", "actualHours", "sensitive", "authImpact", "startDate", "endDate", "createdAt", "updatedAt"}) {
		return true
	}
	for _, role := range []string{"frontend", "backend", "algorithm", "ui", "product"} {
		if key == "role."+role+".userId" || key == "role."+role+".value" {
			return true
		}
	}
	return false
}
func validSavedViewConfig(c *savedViewConfig) bool {
	if c == nil || c.Schema != 1 || !validChoice(c.View, []string{"list", "board"}) || !validChoice(c.Order, []string{"asc", "desc"}) || !savedViewField(c.Sort) || !validChoice(c.AssigneeMode, []string{"any", "me", "member"}) || !validChoice(c.StatusCategory, []string{"", "todo", "doing", "done", "cancelled"}) || !validChoice(c.Priority, []string{"", "P0", "P1", "P2", "P3"}) {
		return false
	}
	if c.SprintID < 0 || c.CategoryID < 0 || c.SprintID > 9007199254740991 || c.CategoryID > 9007199254740991 || len(c.Q) > 2000 || len(c.Sprint) > 500 || len(c.Category) > 500 || len(c.AssigneeUserID) > 128 || c.AssigneeMode == "member" && c.AssigneeUserID == "" || c.AssigneeMode != "member" && c.AssigneeUserID != "" || len(c.Statuses) > 100 || len(c.Filters) > 30 || len(c.Columns) < 2 || len(c.Columns) > 100 || c.Columns[0] != "code" || c.Columns[1] != "title" {
		return false
	}
	seen := map[string]bool{}
	for _, key := range c.Columns {
		if !savedViewField(key) || seen[key] {
			return false
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, key := range c.Statuses {
		if key == "" || len(key) > 128 || seen[key] {
			return false
		}
		seen[key] = true
	}
	for _, f := range c.Filters {
		if f.ReferenceID != nil && (*f.ReferenceID < 1 || *f.ReferenceID > 9007199254740991 || !validChoice(f.Field, []string{"category", "sprint"}) || !validChoice(f.Operator, []string{"eq", "neq"})) {
			return false
		}
		if !savedViewField(f.Field) || !validChoice(f.Operator, []string{"eq", "neq", "contains", "not_contains", "gt", "gte", "lt", "lte", "includes", "not_includes", "is_empty", "not_empty"}) {
			return false
		}
		if f.Operator == "is_empty" || f.Operator == "not_empty" {
			if len(f.Value) != 0 {
				return false
			}
			continue
		}
		var v any
		if json.Unmarshal(f.Value, &v) != nil {
			return false
		}
		if f.ReferenceID != nil {
			if _, ok := v.(string); !ok {
				return false
			}
		}
		switch x := v.(type) {
		case string:
			if x == "" || len(x) > 2000 {
				return false
			}
		case bool:
		case float64:
			if x > 1e15 || x < -1e15 {
				return false
			}
		default:
			return false
		}
	}
	if c.Statuses == nil {
		c.Statuses = []string{}
	}
	if c.Filters == nil {
		c.Filters = []savedViewFilter{}
	}
	return len(jsonText(c)) <= 30000
}
func savedViewName(value string) (string, bool) {
	name := strings.TrimSpace(value)
	return name, utf8.RuneCountInString(name) > 0 && utf8.RuneCountInString(name) <= 30 && strings.IndexFunc(name, unicode.IsControl) < 0
}
func failSavedView(w http.ResponseWriter, err error) {
	var specific *organizationError
	if errors.As(err, &specific) {
		failOrganization(w, err)
		return
	}
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		fail(w, 409, "saved_view_name_conflict", "同一范围内已有同名视图")
		return
	}
	fail(w, 503, "saved_views_unavailable", "视图服务暂时不可用，请稍后重试")
}
func (a *App) requirementViews(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/requirement-views")
	var id int64
	var err error
	if path != "" {
		if !strings.HasPrefix(path, "/") || strings.Contains(path[1:], "/") {
			fail(w, 404, "not_found", "视图不存在或无权访问")
			return
		}
		id, err = strconv.ParseInt(path[1:], 10, 64)
		if err != nil || id < 1 {
			fail(w, 404, "not_found", "视图不存在或无权访问")
			return
		}
	}
	if r.Method == http.MethodGet && id == 0 {
		writeAllowed, manage, err := a.savedViewAccess(r.Context(), a.db)
		if err != nil {
			failSavedView(w, err)
			return
		}
		rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,scope,owner_user_id,version,config_json,updated_at FROM requirement_saved_views WHERE tenant_id=? AND project_id=? AND (scope='shared' OR owner_user_id=?) ORDER BY scope,name_key,id`, tenantID, a.pid(), a.uid())
		if err != nil {
			failSavedView(w, err)
			return
		}
		defer rows.Close()
		items := []savedView{}
		for rows.Next() {
			var v savedView
			var raw string
			if err = rows.Scan(&v.ID, &v.Name, &v.Scope, &v.OwnerUserID, &v.Version, &raw, &v.UpdatedAt); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(raw), &v.Config); err != nil {
				break
			}
			v.CanManage = writeAllowed && (v.Scope == "personal" && v.OwnerUserID == a.uid() || v.Scope == "shared" && manage)
			items = append(items, v)
		}
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			failSavedView(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items, "canCreate": writeAllowed, "canShare": manage})
		return
	}
	if !(r.Method == http.MethodPost && id == 0 || r.Method == http.MethodPatch && id > 0 || r.Method == http.MethodDelete && id > 0) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body *struct {
		Name    *string          `json:"name"`
		Scope   *string          `json:"scope"`
		Version *int64           `json:"version"`
		Config  *savedViewConfig `json:"config"`
	}
	if !decodePreference(w, r, &body) {
		return
	}
	if body == nil {
		fail(w, 422, "validation_error", "请求字段格式不正确")
		return
	}
	name := ""
	if body.Name != nil {
		var ok bool
		name, ok = savedViewName(*body.Name)
		if !ok {
			fail(w, 422, "validation_error", "视图名称需为 1–30 个字符")
			return
		}
	}
	if r.Method != http.MethodDelete && (body.Name == nil || !validSavedViewConfig(body.Config)) {
		fail(w, 422, "validation_error", "视图配置格式不正确")
		return
	}
	if r.Method == http.MethodPost && (body.Scope == nil || !validChoice(*body.Scope, []string{"personal", "shared"}) || body.Version != nil) || r.Method != http.MethodPost && (body.Version == nil || *body.Version < 1 || body.Scope != nil) || r.Method == http.MethodDelete && (body.Name != nil || body.Config != nil) {
		fail(w, 422, "validation_error", "请求字段格式不正确")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failSavedView(w, err)
		return
	}
	defer tx.Rollback()
	canWrite, manage, err := a.savedViewAccess(r.Context(), tx)
	if err != nil {
		failSavedView(w, err)
		return
	}
	if !canWrite {
		fail(w, 403, "forbidden", "当前角色仅可查看")
		return
	}
	v := savedView{ID: id, OwnerUserID: a.uid(), Name: name, Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), CanManage: true}
	if id != 0 {
		var raw string
		err = tx.QueryRowContext(r.Context(), `SELECT name,scope,owner_user_id,version,config_json FROM requirement_saved_views WHERE tenant_id=? AND project_id=? AND id=? AND (scope='shared' OR owner_user_id=?)`, tenantID, a.pid(), id, a.uid()).Scan(&v.Name, &v.Scope, &v.OwnerUserID, &v.Version, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "视图不存在或无权访问")
			return
		}
		if err != nil {
			failSavedView(w, err)
			return
		}
		if v.Scope == "shared" && !manage {
			fail(w, 403, "admin_required", "仅管理员可执行此操作")
			return
		}
		if v.Version != *body.Version {
			fail(w, 409, "saved_view_conflict", "视图已被修改，请刷新后重试；当前草稿已保留")
			return
		}
	}
	if r.Method == http.MethodDelete {
		result, err := tx.ExecContext(r.Context(), `DELETE FROM requirement_saved_views WHERE tenant_id=? AND project_id=? AND id=? AND version=?`, tenantID, a.pid(), id, v.Version)
		if err != nil {
			failSavedView(w, err)
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 409, "saved_view_conflict", "视图已被修改，请刷新后重试；当前草稿已保留")
			return
		}
		if err = tx.Commit(); err != nil {
			failSavedView(w, err)
			return
		}
		write(w, 200, map[string]any{"deleted": true})
		return
	}
	v.Name = name
	v.Config = *body.Config
	if id == 0 {
		v.Scope = *body.Scope
		if v.Scope == "shared" && !manage {
			fail(w, 403, "admin_required", "仅管理员可执行此操作")
			return
		}
		ownerKey := a.uid()
		if v.Scope == "shared" {
			ownerKey = ""
		}
		var count int
		if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM requirement_saved_views WHERE tenant_id=? AND project_id=? AND scope=? AND owner_key=?`, tenantID, a.pid(), v.Scope, ownerKey).Scan(&count); err != nil {
			failSavedView(w, err)
			return
		}
		if count >= 50 {
			fail(w, 422, "saved_view_limit", "每个范围最多保存 50 个视图")
			return
		}
		result, err := tx.ExecContext(r.Context(), `INSERT INTO requirement_saved_views(tenant_id,project_id,scope,owner_user_id,owner_key,name,name_key,config_json,version,updated_at) VALUES(?,?,?,?,?,?,?,?,1,?)`, tenantID, a.pid(), v.Scope, a.uid(), ownerKey, name, strings.ToLower(name), jsonText(v.Config), v.UpdatedAt)
		if err != nil {
			failSavedView(w, err)
			return
		}
		v.ID, err = result.LastInsertId()
		if err != nil {
			failSavedView(w, err)
			return
		}
	} else {
		result, err := tx.ExecContext(r.Context(), `UPDATE requirement_saved_views SET name=?,name_key=?,config_json=?,version=version+1,updated_at=? WHERE tenant_id=? AND project_id=? AND id=? AND version=?`, name, strings.ToLower(name), jsonText(v.Config), v.UpdatedAt, tenantID, a.pid(), id, v.Version)
		if err != nil {
			failSavedView(w, err)
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			fail(w, 409, "saved_view_conflict", "视图已被修改，请刷新后重试；当前草稿已保留")
			return
		}
		v.Version++
	}
	if err = tx.Commit(); err != nil {
		failSavedView(w, err)
		return
	}
	status := 200
	if id == 0 {
		status = 201
	}
	write(w, status, v)
}
