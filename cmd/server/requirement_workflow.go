package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
)

// 项目级需求工作流：稳定状态 key + 有向边 + 角色白名单，显示名称不参与权限判断。
// 配置保存有版本冲突检查；实际需求写入仍须在业务事务内重新验证当前状态和角色。
type RequirementTransition struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Roles []string `json:"roles"`
}
type WorkflowRole struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type RequirementWorkflow struct {
	InitialStatus string                  `json:"initialStatus"`
	EndStatuses   []string                `json:"endStatuses"`
	Transitions   []RequirementTransition `json:"transitions"`
	Version       int64                   `json:"version"`
	CanManage     bool                    `json:"canManage"`
	Roles         []WorkflowRole          `json:"roles"`
}

var requirementWorkflowRoles = []WorkflowRole{{"tenant_admin", "企业管理员"}, {"project_admin", "项目管理员"}, {"product", "产品"}, {"frontend", "前端工程师"}, {"backend", "后端工程师"}, {"algorithm", "算法工程师"}, {"ui", "UI 设计师"}, {"frontend_lead", "前端组长"}, {"backend_lead", "后端组长"}, {"qa", "测试工程师"}}

func workflowRoleKeys() []string {
	out := []string{}
	for _, r := range requirementWorkflowRoles {
		out = append(out, r.Key)
	}
	return out
}
func terminalCategory(category string) bool { return category == "done" || category == "cancelled" }

// “结束”包括完成和取消，只用于流程终点；统计完成/上线不能把 cancelled 当 done。

func defaultRequirementWorkflow(items []RequirementStatus) RequirementWorkflow {
	f := RequirementWorkflow{InitialStatus: "规划中", EndStatuses: []string{}, Transitions: []RequirementTransition{}, Version: 1, Roles: requirementWorkflowRoles}
	standard := map[string][]string{
		"草稿": {"规划中"}, "规划中": {"草稿", "评审中", "待开发", "开发中"}, "评审中": {"规划中", "待开发", "开发中"}, "待开发": {"规划中", "开发中"},
		"开发中":   {"规划中", "实现中", "后端已完成", "前端已完成", "后端完成 | 前端开发中", "前端完成 | 后端开发中", "开发完成", "测试中"},
		"后端已完成": {"开发中", "前端已完成", "开发完成", "测试中"}, "前端已完成": {"开发中", "后端已完成", "开发完成", "测试中"},
		"后端完成 | 前端开发中": {"开发中", "开发完成", "测试中"}, "前端完成 | 后端开发中": {"开发中", "开发完成", "测试中"},
		"实现中": {"开发中", "开发完成", "测试中"}, "开发完成": {"开发中", "测试中"}, "测试中": {"开发中", "冒烟测试完成", "待上线", "已完成"},
		"冒烟测试完成": {"开发中", "测试中", "待上线"}, "待上线": {"开发中", "测试中", "已上线"}, "流程挂起": {"规划中", "开发中", "流程终止"},
	}
	for _, s := range items {
		if s.Enabled && terminalCategory(s.Category) {
			f.EndStatuses = append(f.EndStatuses, s.Key)
		}
	}
	// 管理员恢复路径也是显式矩阵规则，不是绕过工作流的后门；替换矩阵可以撤销这些边。
	for _, from := range items {
		for _, to := range items {
			if from.Key == to.Key || !to.Enabled {
				continue
			}
			roles := []string{"tenant_admin", "project_admin"}
			if !terminalCategory(from.Category) && (validChoice(to.Key, standard[from.Key]) || validChoice(to.Key, []string{"流程挂起", "已拒绝", "流程终止", "已取消"})) {
				roles = workflowRoleKeys()
			}
			f.Transitions = append(f.Transitions, RequirementTransition{from.Key, to.Key, roles})
		}
	}
	return f
}
func loadRequirementWorkflow(ctx context.Context, q stateStore, tenant, project string) (RequirementWorkflow, error) {
	f := RequirementWorkflow{Roles: requirementWorkflowRoles}
	var ends, edges string
	err := q.QueryRowContext(ctx, `SELECT initial_status,end_statuses_json,transitions_json,version FROM requirement_workflows WHERE tenant_id=? AND project_id=?`, tenant, project).Scan(&f.InitialStatus, &ends, &edges, &f.Version)
	if err != nil {
		return f, err
	}
	if err = json.Unmarshal([]byte(ends), &f.EndStatuses); err != nil {
		return f, err
	}
	if err = json.Unmarshal([]byte(edges), &f.Transitions); err != nil {
		return f, err
	}
	return f, nil
}
func validateRequirementWorkflow(f *RequirementWorkflow, items []RequirementStatus) error {
	// 初始状态须可用且非终态，结束状态覆盖所有可用终态；边不能重复/自环，
	// 可以保留停用的历史源状态，但不能再流入停用目标，也不能授权 viewer。
	states := requirementCategoryMap(items)
	initial, ok := states[f.InitialStatus]
	if !ok || !initial.Enabled || terminalCategory(initial.Category) {
		return invalidState("初始状态必须是已启用的待办或进行中状态")
	}
	if len(f.EndStatuses) == 0 || len(f.EndStatuses) > 100 {
		return invalidState("至少配置一个有效结束状态")
	}
	ends := map[string]bool{}
	for _, key := range f.EndStatuses {
		s, ok := states[key]
		if !ok || !s.Enabled || !terminalCategory(s.Category) || ends[key] {
			return invalidState("结束状态必须是已启用的完成或取消状态且不能重复")
		}
		ends[key] = true
	}
	for _, s := range items {
		if s.Enabled && terminalCategory(s.Category) && !ends[s.Key] {
			return invalidState("所有完成或取消类别的状态必须设为结束状态")
		}
	}
	if len(f.Transitions) > 10000 {
		return invalidState("工作流最多配置 10000 条流转规则")
	}
	seen := map[string]bool{}
	validRoles := workflowRoleKeys()
	for i, edge := range f.Transitions {
		_, fromOK := states[edge.From]
		to, toOK := states[edge.To]
		pair := edge.From + "\x00" + edge.To
		if !fromOK || !toOK || !to.Enabled || edge.From == edge.To || seen[pair] {
			return invalidState("流转规则包含未知、停用目标或重复路径")
		}
		seen[pair] = true
		if len(edge.Roles) == 0 || len(edge.Roles) > len(validRoles) {
			return invalidState("每条流转规则必须指定有效的可编辑角色")
		}
		roles := map[string]bool{}
		for _, role := range edge.Roles {
			if !validChoice(role, validRoles) || roles[role] {
				return invalidState("流转角色无效、重复或为只读角色")
			}
			roles[role] = true
		}
		f.Transitions[i].Roles = sortedKeys(roles)
	}
	f.EndStatuses = sortedKeys(ends)
	sort.Slice(f.Transitions, func(i, j int) bool {
		if f.Transitions[i].From == f.Transitions[j].From {
			return f.Transitions[i].To < f.Transitions[j].To
		}
		return f.Transitions[i].From < f.Transitions[j].From
	})
	if f.Transitions == nil {
		f.Transitions = []RequirementTransition{}
	}
	return nil
}
func saveRequirementWorkflow(ctx context.Context, q stateStore, tenant, project string, f RequirementWorkflow) error {
	// 乐观锁比较请求版本，不能把其他管理员刚保存的规则直接覆盖；409 需前端保留草稿。
	res, err := q.ExecContext(ctx, `UPDATE requirement_workflows SET initial_status=?,end_statuses_json=?,transitions_json=?,version=version+1,updated_at=? WHERE tenant_id=? AND project_id=? AND version=?`, f.InitialStatus, jsonText(f.EndStatuses), jsonText(f.Transitions), time.Now().UTC().Format(time.RFC3339), tenant, project, f.Version)
	if err != nil {
		return err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return stateError{"workflow_conflict", "工作流已被其他管理员修改，请刷新后重试", 409}
	}
	return nil
}
func (a *App) reconcileRequirementWorkflow(ctx context.Context, q stateStore) error {
	// 状态目录变更时同步终态和失效目标边；不自动猜一个新初始状态替换用户配置。
	f, err := loadRequirementWorkflow(ctx, q, tenantID, a.pid())
	if err != nil {
		return err
	}
	items, err := listRequirementStatuses(ctx, q, tenantID, a.pid())
	if err != nil {
		return err
	}
	states := requirementCategoryMap(items)
	if initial := states[f.InitialStatus]; !initial.Enabled || terminalCategory(initial.Category) {
		return invalidState("请先调整工作流初始状态，再停用或结束该状态")
	}
	f.EndStatuses = []string{}
	for _, s := range items {
		if s.Enabled && terminalCategory(s.Category) {
			f.EndStatuses = append(f.EndStatuses, s.Key)
		}
	}
	if len(f.EndStatuses) == 0 {
		return invalidState("至少保留一个已启用的结束状态")
	}
	edges := []RequirementTransition{}
	for _, edge := range f.Transitions {
		if target, ok := states[edge.To]; ok && target.Enabled {
			edges = append(edges, edge)
		}
	}
	f.Transitions = edges
	if err = validateRequirementWorkflow(&f, items); err != nil {
		return err
	}
	return saveRequirementWorkflow(ctx, q, tenantID, a.pid(), f)
}
func (a *App) requirementWorkflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "PUT" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if r.Method == "GET" {
		role, err := a.requirementStateRole(r.Context(), a.db)
		if err != nil {
			failState(w, err)
			return
		}
		f, err := loadRequirementWorkflow(r.Context(), a.db, tenantID, a.pid())
		if err != nil {
			failState(w, err)
			return
		}
		f.CanManage = stateManager(role)
		write(w, 200, f)
		return
	}
	var f RequirementWorkflow
	if decodeJSON(r, &f) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if f.Version < 1 {
		failState(w, invalidState("保存工作流必须携带当前版本"))
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
	current, err := loadRequirementWorkflow(r.Context(), tx, tenantID, a.pid())
	if err != nil {
		failState(w, err)
		return
	}
	if current.Version != f.Version {
		failState(w, stateError{"workflow_conflict", "工作流已被其他管理员修改，请刷新后重试", 409})
		return
	}
	items, err := listRequirementStatuses(r.Context(), tx, tenantID, a.pid())
	if err == nil {
		err = validateRequirementWorkflow(&f, items)
	}
	if err == nil {
		err = saveRequirementWorkflow(r.Context(), tx, tenantID, a.pid(), f)
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "requirement_workflow", "updated", a.pid(), f)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failState(w, err)
		return
	}
	f.Version++
	f.CanManage = true
	f.Roles = requirementWorkflowRoles
	write(w, 200, f)
}
func (a *App) validateRequirementStateWrite(ctx context.Context, tx *sql.Tx, x *Requirement, creating bool, explicit bool) (string, error) {
	// PATCH 未明确提交状态时沿用事务内最新状态；旧值未改变时允许保留历史停用状态。
	// 真正流转必须命中 from/to/role 边，管理员也不能跳过；新建仅允许初始态或草稿。
	f, err := loadRequirementWorkflow(ctx, tx, tenantID, a.pid())
	if err != nil {
		return "", err
	}
	roles, err := a.requirementStateRoles(ctx, tx)
	if err != nil {
		return "", err
	}
	if !rolesOverlap(roles, workflowRoleKeys()) {
		return "", stateError{"forbidden", "当前角色仅可查看", 403}
	}
	if creating && x.Status == "" {
		x.Status = f.InitialStatus
	}
	current := ""
	if !creating {
		if err = tx.QueryRowContext(ctx, `SELECT status FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), x.ID).Scan(&current); err != nil {
			return "", err
		}
		if !explicit {
			x.Status = current
			return current, nil
		}
		if x.Status == current {
			return current, nil
		}
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND key=?`, tenantID, a.pid(), x.Status).Scan(&enabled); errors.Is(err, sql.ErrNoRows) {
		return current, invalidState("需求状态不存在或不属于当前项目")
	}
	if err != nil {
		return current, err
	}
	if !enabled {
		return current, invalidState("该需求状态已停用，不能再选择")
	}
	if creating {
		if x.Status != f.InitialStatus && x.Status != "草稿" {
			return current, invalidState("新需求只能从初始状态或草稿创建")
		}
		return current, nil
	}
	for _, edge := range f.Transitions {
		if edge.From == current && edge.To == x.Status && rolesOverlap(roles, edge.Roles) {
			return current, nil
		}
	}
	return current, stateError{"transition_forbidden", "当前角色无权执行此需求状态流转", 403}
}

func (a *App) requirementAllowedTransitions(ctx context.Context, id int64) ([]string, string, int64, error) {
	// 下拉可用目标来自一致的只读事务，返回当前状态和规则版本供界面识别迟到结果。
	// 这只是预览权限，不是后续写入通行证，保存时必须再次执行事务内校验。
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", 0, err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&current); err != nil {
		return nil, "", 0, err
	}
	roles, err := a.requirementStateRoles(ctx, tx)
	if err != nil {
		return nil, "", 0, err
	}
	f, err := loadRequirementWorkflow(ctx, tx, tenantID, a.pid())
	if err != nil {
		return nil, "", 0, err
	}
	items, err := listRequirementStatuses(ctx, tx, tenantID, a.pid())
	if err != nil {
		return nil, "", 0, err
	}
	states := requirementCategoryMap(items)
	selected := map[string]bool{}
	if rolesOverlap(roles, workflowRoleKeys()) {
		for _, edge := range f.Transitions {
			if edge.From == current && states[edge.To].Enabled && rolesOverlap(roles, edge.Roles) {
				selected[edge.To] = true
			}
		}
	}
	allowed := []string{}
	for _, item := range items {
		if selected[item.Key] {
			allowed = append(allowed, item.Key)
		}
	}
	return allowed, current, f.Version, nil
}
func (a *App) requirementTransitions(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	allowed, current, version, err := a.requirementAllowedTransitions(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "需求不存在")
		} else {
			failState(w, err)
		}
		return
	}
	write(w, 200, map[string]any{"allowedTransitions": allowed, "currentStatus": current, "version": version})
}

func (a *App) hydrateRequirementState(ctx context.Context, x *Requirement) error {
	var s RequirementStatus
	err := a.db.QueryRowContext(ctx, `SELECT name,color,category,system FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND key=?`, tenantID, a.pid(), x.Status).Scan(&s.Name, &s.Color, &s.Category, &s.System)
	if errors.Is(err, sql.ErrNoRows) {
		x.StatusName = x.Status
		x.StatusCategory = "todo"
		return nil
	}
	if err != nil {
		return err
	}
	x.StatusName, x.StatusColor, x.StatusCategory = s.Name, s.Color, s.Category
	x.StatusSystem = s.System
	x.IsEnd = terminalCategory(s.Category)
	return nil
}

func (a *App) writeRequirementStateNotice(ctx context.Context, tx *sql.Tx, x *Requirement, from string, now string) error {
	// 此状态专用通知排除操作者；与 requirement_change_notices.go 的“全体处理人动态”
	// 口径不同，接新调用点前需选择其一，不能同时调用导致一份变化重复发通知。
	if from == x.Status {
		return nil
	}
	var name string
	var system bool
	if err := tx.QueryRowContext(ctx, `SELECT name,system FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND key=?`, tenantID, a.pid(), x.Status).Scan(&name, &system); err != nil {
		return err
	}
	body := "需求状态已变更为「" + name + "」"
	if system && name == x.Status {
		body = "需求状态已变更为 " + name
	}
	people := []string{}
	seen := map[string]bool{}
	for _, id := range x.AssigneeUserIDs {
		if id == a.uid() || seen[id] {
			continue
		}
		seen[id] = true
		var allowed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=?))`, tenantID, id, a.pid()).Scan(&allowed); err != nil {
			return err
		}
		if allowed > 0 {
			people = append(people, id)
		}
	}
	if err := a.writeRequirementPeopleNotices(tx, x, []requirementPeopleNotice{{Event: "requirement.status_changed", Field: "status", Title: "工作项有新动态", Body: body, Recipients: people}}, now); err != nil {
		return err
	}
	return a.auditRequirementState(ctx, tx, "requirement", "status_changed", x.ID, map[string]string{"from": from, "to": x.Status})
}
