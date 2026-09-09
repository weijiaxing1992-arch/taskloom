package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type RequirementStatus struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Category  string `json:"category"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sortOrder"`
	System    bool   `json:"system"`
}
type stateStore interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type stateError struct {
	code, message string
	status        int
}

func (e stateError) Error() string      { return e.message }
func invalidState(message string) error { return stateError{"invalid_requirement_state", message, 422} }
func failState(w http.ResponseWriter, err error) {
	var specific stateError
	if errors.As(err, &specific) {
		fail(w, specific.status, specific.code, specific.message)
		return
	}
	fail(w, 503, "database_unavailable", "需求状态服务暂时不可用，请稍后重试")
}

func defaultRequirementStatuses() []RequirementStatus {
	names := []string{"规划中", "开发中", "已上线", "已拒绝", "测试中", "待上线", "后端已完成", "前端已完成", "实现中", "开发完成", "流程挂起", "流程终止", "后端完成 | 前端开发中", "前端完成 | 后端开发中", "冒烟测试完成", "草稿", "评审中", "待开发", "已完成", "已取消"}
	items := make([]RequirementStatus, 0, len(names))
	for i, name := range names {
		category, color := "doing", "#2563EB"
		switch name {
		case "规划中":
			category, color = "todo", "#059669"
		case "评审中":
			category, color = "todo", "#D97706"
		case "草稿", "待开发":
			category, color = "todo", "#64748B"
		case "已上线", "已完成":
			category, color = "done", "#059669"
		case "已拒绝", "流程终止":
			category, color = "cancelled", "#DC2626"
		case "已取消":
			category, color = "cancelled", "#64748B"
		case "待上线", "流程挂起":
			color = "#D97706"
		case "后端已完成", "前端已完成", "开发完成":
			color = "#0891B2"
		case "测试中", "冒烟测试完成":
			color = "#7C3AED"
		}
		items = append(items, RequirementStatus{Key: name, Name: name, Color: color, Category: category, Enabled: true, SortOrder: i * 10, System: true})
	}
	return items
}

func (a *App) migrateRequirementStates() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_statuses(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,key TEXT NOT NULL,name TEXT NOT NULL,color TEXT NOT NULL,category TEXT NOT NULL CHECK(category IN ('todo','doing','done','cancelled')),enabled INTEGER NOT NULL DEFAULT 1,sort_order INTEGER NOT NULL DEFAULT 0,system INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,key),UNIQUE(tenant_id,project_id,name));
CREATE INDEX IF NOT EXISTS idx_requirement_statuses_scope ON requirement_statuses(tenant_id,project_id,key);
CREATE TABLE IF NOT EXISTS requirement_workflows(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,initial_status TEXT NOT NULL,end_statuses_json TEXT NOT NULL,transitions_json TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id));`); err != nil {
		return err
	}
	rows, err := a.db.Query(`SELECT tenant_id,id FROM projects`)
	if err != nil {
		return err
	}
	type scope struct{ tenant, project string }
	scopes := []scope{}
	for rows.Next() {
		var s scope
		if err = rows.Scan(&s.tenant, &s.project); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, s := range scopes {
		tx, err := a.db.Begin()
		if err != nil {
			return err
		}
		if err = seedRequirementState(context.Background(), tx, s.tenant, s.project); err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func seedRequirementState(ctx context.Context, q stateStore, tenant, project string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, s := range defaultRequirementStatuses() {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO requirement_statuses(tenant_id,project_id,key,name,color,category,enabled,sort_order,system,created_at,updated_at)VALUES(?,?,?,?,?,?,1,?,1,?,?)`, tenant, project, s.Key, s.Name, s.Color, s.Category, s.SortOrder, now, now); err != nil {
			return err
		}
	}
	// Never change historic requirement strings, including previously custom ones.
	if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO requirement_statuses(tenant_id,project_id,key,name,color,category,enabled,sort_order,system,created_at,updated_at) SELECT DISTINCT tenant_id,project_id,status,status,'#64748B','todo',1,1000,0,?,? FROM requirements WHERE tenant_id=? AND project_id=? AND trim(status)!=''`, now, now, tenant, project); err != nil {
		return err
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM requirement_workflows WHERE tenant_id=? AND project_id=?`, tenant, project).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		items, err := listRequirementStatuses(ctx, q, tenant, project)
		if err != nil {
			return err
		}
		flow := defaultRequirementWorkflow(items)
		_, err = q.ExecContext(ctx, `INSERT INTO requirement_workflows(tenant_id,project_id,initial_status,end_statuses_json,transitions_json,version,updated_at)VALUES(?,?,?,?,?,1,?)`, tenant, project, flow.InitialStatus, jsonText(flow.EndStatuses), jsonText(flow.Transitions), now)
		return err
	}
	return nil
}

func listRequirementStatuses(ctx context.Context, q stateStore, tenant, project string) ([]RequirementStatus, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,key,name,color,category,enabled,sort_order,system FROM requirement_statuses WHERE tenant_id=? AND project_id=? ORDER BY sort_order,id`, tenant, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RequirementStatus{}
	for rows.Next() {
		var s RequirementStatus
		if err = rows.Scan(&s.ID, &s.Key, &s.Name, &s.Color, &s.Category, &s.Enabled, &s.SortOrder, &s.System); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}
func (a *App) requirementStateRole(ctx context.Context, q stateStore) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, `SELECT CASE WHEN tm.role='tenant_admin' THEN 'tenant_admin' ELSE COALESCE(pm.role,'') END FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=? WHERE u.tenant_id=? AND u.id=? AND u.active=1`, a.pid(), tenantID, a.uid()).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && role == "" {
		return "", stateError{"forbidden", "无权访问该项目", 403}
	}
	return role, err
}
func stateManager(role string) bool { return role == "tenant_admin" || role == "project_admin" }
func (a *App) requireStateManager(ctx context.Context, q stateStore) error {
	role, err := a.requirementStateRole(ctx, q)
	if err != nil {
		return err
	}
	if !stateManager(role) {
		return stateError{"admin_required", "仅管理员可配置需求状态和工作流", 403}
	}
	return nil
}
func validStatusDefinition(s *RequirementStatus) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || utf8.RuneCountInString(s.Name) > 80 {
		return invalidState("状态名称须为 1–80 字")
	}
	for _, r := range s.Name {
		if unicode.IsControl(r) {
			return invalidState("状态名称不能包含控制字符")
		}
	}
	if !tagColorPattern.MatchString(s.Color) {
		return invalidState("状态颜色须为 #RRGGBB 格式")
	}
	s.Color = strings.ToUpper(s.Color)
	if !validChoice(s.Category, []string{"todo", "doing", "done", "cancelled"}) {
		return invalidState("状态类别无效")
	}
	if s.SortOrder < 0 || s.SortOrder > 1000000 {
		return invalidState("状态排序须为 0–1000000 的整数")
	}
	return nil
}
func (a *App) auditRequirementState(ctx context.Context, q stateStore, object, action string, id any, value any) error {
	_, err := q.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), object, fmt.Sprint(id), action, jsonText(value), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (a *App) requirementStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		role, err := a.requirementStateRole(r.Context(), a.db)
		if err != nil {
			failState(w, err)
			return
		}
		items, err := listRequirementStatuses(r.Context(), a.db, tenantID, a.pid())
		if err != nil {
			failState(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items, "canManage": stateManager(role)})
		return
	}
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	s := RequirementStatus{Enabled: true}
	if decodeJSON(r, &s) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	s.ID = 0
	s.System = false
	if s.Color == "" {
		s.Color = "#3B82F6"
	}
	if s.Category == "" {
		s.Category = "doing"
	}
	if err := validStatusDefinition(&s); err != nil {
		failState(w, err)
		return
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		failState(w, err)
		return
	}
	s.Key = "status_" + hex.EncodeToString(token[:])
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failState(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireStateManager(r.Context(), tx); err != nil {
		failState(w, err)
		return
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM requirement_statuses WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&count); err != nil {
		failState(w, err)
		return
	}
	if count >= 100 {
		failState(w, invalidState("每个项目最多配置 100 个需求状态"))
		return
	}
	var duplicate int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND name=?`, tenantID, a.pid(), s.Name).Scan(&duplicate); err != nil {
		failState(w, err)
		return
	}
	if duplicate > 0 {
		failState(w, stateError{"status_exists", "状态名称已存在", 409})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(r.Context(), `INSERT INTO requirement_statuses(tenant_id,project_id,key,name,color,category,enabled,sort_order,system,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,0,?,?)`, tenantID, a.pid(), s.Key, s.Name, s.Color, s.Category, s.Enabled, s.SortOrder, now, now)
	if err == nil {
		s.ID, err = res.LastInsertId()
	}
	if err == nil {
		err = a.reconcileRequirementWorkflow(r.Context(), tx)
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "requirement_status", "created", s.ID, s)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failState(w, err)
		return
	}
	write(w, 201, s)
}
func (a *App) requirementStatus(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/requirement-statuses/")
	if path == "defaults" {
		a.requirementStatusDefaults(w, r)
		return
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	if r.Method != "PATCH" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body map[string]json.RawMessage
	if decodeJSON(r, &body) != nil || body == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failState(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireStateManager(r.Context(), tx); err != nil {
		failState(w, err)
		return
	}
	items, err := listRequirementStatuses(r.Context(), tx, tenantID, a.pid())
	if err != nil {
		failState(w, err)
		return
	}
	var s RequirementStatus
	for _, item := range items {
		if item.ID == id {
			s = item
			break
		}
	}
	if s.ID == 0 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	for key, raw := range body {
		target := map[string]any{"name": &s.Name, "color": &s.Color, "category": &s.Category, "enabled": &s.Enabled, "sortOrder": &s.SortOrder}[key]
		if target == nil || string(raw) == "null" {
			failState(w, invalidState("状态配置包含不可修改或无效字段"))
			return
		}
		if json.Unmarshal(raw, target) != nil {
			failState(w, invalidState("状态字段格式不正确"))
			return
		}
	}
	if err = validStatusDefinition(&s); err != nil {
		failState(w, err)
		return
	}
	for _, item := range items {
		if item.ID != s.ID && item.Name == s.Name {
			failState(w, stateError{"status_exists", "状态名称已存在", 409})
			return
		}
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE requirement_statuses SET name=?,color=?,category=?,enabled=?,sort_order=?,updated_at=? WHERE tenant_id=? AND project_id=? AND id=?`, s.Name, s.Color, s.Category, s.Enabled, s.SortOrder, time.Now().UTC().Format(time.RFC3339), tenantID, a.pid(), s.ID)
	if err == nil {
		err = a.reconcileRequirementWorkflow(r.Context(), tx)
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "requirement_status", "updated", s.ID, s)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failState(w, err)
		return
	}
	write(w, 200, s)
}
func (a *App) requirementStatusDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failState(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireStateManager(r.Context(), tx); err == nil {
		err = seedRequirementState(r.Context(), tx, tenantID, a.pid())
	}
	if err == nil {
		err = a.reconcileRequirementWorkflow(r.Context(), tx)
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "requirement_status", "defaults", a.pid(), map[string]bool{"preserveExisting": true})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failState(w, err)
		return
	}
	read := r.Clone(r.Context())
	read.Method = "GET"
	a.requirementStatuses(w, read)
}

func requirementCategoryMap(items []RequirementStatus) map[string]RequirementStatus {
	out := map[string]RequirementStatus{}
	for _, s := range items {
		out[s.Key] = s
	}
	return out
}
func requirementWorkCategory(category, due string) string {
	if category == "done" {
		return "completed"
	}
	if category == "cancelled" {
		return "cancelled"
	}
	if result := workCategory("", "", due); result == "due" || result == "overdue" {
		return result
	}
	if category == "doing" {
		return "doing"
	}
	return "todo"
}

func parseStatusSelection(r *http.Request, known map[string]bool) ([]string, error) {
	values, exists := r.URL.Query()["statuses"]
	single := r.URL.Query().Get("status")
	if len(r.URL.Query()["status"]) > 1 || len(values) > 1 {
		return nil, invalidState("状态筛选格式不正确")
	}
	selected := []string{}
	if exists {
		if len(values) != 1 || json.Unmarshal([]byte(values[0]), &selected) != nil || selected == nil {
			return nil, invalidState("状态筛选必须是字符串数组")
		}
		if single != "" && len(selected) > 0 {
			return nil, invalidState("单选和多选状态筛选不能同时使用")
		}
	}
	if single != "" {
		selected = []string{single}
	}
	if len(selected) > 100 {
		return nil, invalidState("最多选择 100 个筛选状态")
	}
	seen := map[string]bool{}
	result := []string{}
	for _, value := range selected {
		if value == "" || !known[value] {
			return nil, invalidState("筛选状态不存在或不属于当前项目")
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result, nil
}
func addStatusSelection(query string, args []any, selected []string) (string, []any) {
	if len(selected) == 0 {
		return query, args
	}
	placeholders := make([]string, len(selected))
	for i, s := range selected {
		placeholders[i] = "?"
		args = append(args, s)
	}
	return query + " AND status IN (" + strings.Join(placeholders, ",") + ")", args
}

// Shared metadata is also returned for cross-project search/my-work consumers.
func (a *App) stateCatalogByProject(ctx context.Context) (map[string]map[string]RequirementStatus, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT project_id,id,key,name,color,category,enabled,sort_order,system FROM requirement_statuses WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]RequirementStatus{}
	for rows.Next() {
		var project string
		var s RequirementStatus
		if err = rows.Scan(&project, &s.ID, &s.Key, &s.Name, &s.Color, &s.Category, &s.Enabled, &s.SortOrder, &s.System); err != nil {
			return nil, err
		}
		if out[project] == nil {
			out[project] = map[string]RequirementStatus{}
		}
		out[project][s.Key] = s
	}
	return out, rows.Err()
}
func sortedKeys(set map[string]bool) []string {
	result := []string{}
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func statusDefinitionsForScope(catalog map[string]map[string]RequirementStatus, access map[string]bool, scope string) []map[string]any {
	items := []map[string]any{}
	projects := []string{}
	for project := range catalog {
		if access[project] && (scope == "all" || scope == project) {
			projects = append(projects, project)
		}
	}
	sort.Strings(projects)
	for _, project := range projects {
		states := []RequirementStatus{}
		for _, state := range catalog[project] {
			states = append(states, state)
		}
		sort.Slice(states, func(i, j int) bool {
			if states[i].SortOrder == states[j].SortOrder {
				return states[i].ID < states[j].ID
			}
			return states[i].SortOrder < states[j].SortOrder
		})
		for _, state := range states {
			items = append(items, map[string]any{"key": state.Key, "name": state.Name, "color": state.Color, "category": state.Category, "projectId": project, "enabled": state.Enabled, "system": state.System})
		}
	}
	return items
}
func applyWorkStatus(item *workItem, states map[string]RequirementStatus) {
	state, ok := states[item.Status]
	if !ok {
		state = RequirementStatus{Name: item.Status, Category: "todo"}
	}
	item.StatusName, item.StatusColor, item.StatusCategory, item.IsEnd = state.Name, state.Color, state.Category, terminalCategory(state.Category)
	item.StatusSystem = state.System
	item.Category = requirementWorkCategory(state.Category, item.DueDate)
}
