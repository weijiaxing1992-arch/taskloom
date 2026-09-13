package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// AutomationRule 是项目级自动化的最小可交付模型。当前只支持需求状态变更，
// 动作只会写入站内通知；任何 Webhook、邮件或第三方调用都不能从该模型触发。
type AutomationRule struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	Trigger          string   `json:"trigger"`
	FromStatus       string   `json:"fromStatus"`
	ToStatus         string   `json:"toStatus"`
	Action           string   `json:"action"`
	RecipientModes   []string `json:"recipientModes"`
	RecipientUserIDs []string `json:"recipientUserIds"`
	Version          int      `json:"version"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

type automationRulePreview struct {
	DryRun              bool `json:"dryRun"`
	MatchingSampleCount int  `json:"matchingSampleCount"`
	SampleItems         []struct {
		ID     int64  `json:"id"`
		Code   string `json:"code"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"sampleItems"`
	// 历史需求仅保留当前状态，预览不能臆造变更前状态；有来源状态时只明确
	// 标记为运行期校验，真实执行仍会在同一事务内同时校验来源和目标状态。
	FromStatusEvaluatedAtRuntime bool `json:"fromStatusEvaluatedAtRuntime"`
}

// AutomationRuleExecution 是规则真正执行后的只读审计视图。规则本身可以被删除，
// 因而 RuleName 与需求摘要都允许为空；不能因为历史记录不完整就让执行日志失效。
type AutomationRuleExecution struct {
	ID           int64    `json:"id"`
	RuleID       int64    `json:"ruleId"`
	RuleName     string   `json:"ruleName"`
	SubjectID    int64    `json:"subjectId"`
	SubjectCode  string   `json:"subjectCode"`
	SubjectTitle string   `json:"subjectTitle"`
	Status       string   `json:"status"`
	Recipients   []string `json:"recipients"`
	CreatedAt    string   `json:"createdAt"`
}

type automationRuleError struct {
	code, message string
	status        int
}

func (e automationRuleError) Error() string { return e.message }

func invalidAutomationRule(message string) error {
	return automationRuleError{code: "invalid_automation_rule", message: message, status: http.StatusUnprocessableEntity}
}

func failAutomationRule(w http.ResponseWriter, err error) {
	var specific automationRuleError
	if errors.As(err, &specific) {
		fail(w, specific.status, specific.code, specific.message)
		return
	}
	// requirementStateRole 复用状态配置的项目可见性判断；它的 403 不是数据库
	// 故障，必须原样返回，不能被自动化模块错误地包装成 503。
	var stateSpecific stateError
	if errors.As(err, &stateSpecific) {
		fail(w, stateSpecific.status, stateSpecific.code, stateSpecific.message)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "自动化规则不存在")
		return
	}
	fail(w, http.StatusServiceUnavailable, "database_unavailable", "自动化规则服务暂时不可用，请稍后重试")
}

const automationRequirementStatusChanged = "requirement.status_changed"

// 收件人模式只保存稳定的内部 key；展示名称由前端本地化。角色模式读取需求
// role_weights_json 中已经绑定的成员，不会因为某个项目拥有某种角色就广播给整组人。
const (
	automationRecipientAssignee     = "assignee"
	automationRecipientOwner        = "owner"
	automationRecipientFrontend     = "frontend"
	automationRecipientBackend      = "backend"
	automationRecipientAlgorithm    = "algorithm"
	automationRecipientUI           = "ui"
	automationRecipientProduct      = "product"
	automationRecipientFrontendLead = "frontend_lead"
	automationRecipientBackendLead  = "backend_lead"
	automationRecipientTester       = "tester"
)

var automationRecipientModeValues = []string{
	automationRecipientAssignee,
	automationRecipientOwner,
	automationRecipientFrontend,
	automationRecipientBackend,
	automationRecipientAlgorithm,
	automationRecipientUI,
	automationRecipientProduct,
	automationRecipientFrontendLead,
	automationRecipientBackendLead,
	automationRecipientTester,
}

func (a *App) migrateAutomationRules() error {
	// 规则、事件和执行记录分表保存：规则可以删除或停用，但已经发生的执行审计
	// 不能跟随消失；执行表用事件 ID 联合唯一键抵抗重试/重复投递。
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS automation_rules(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
		trigger_event TEXT NOT NULL CHECK(trigger_event='requirement.status_changed'),
		from_status TEXT NOT NULL DEFAULT '',
		to_status TEXT NOT NULL DEFAULT '',
		action_type TEXT NOT NULL CHECK(action_type='notify'),
		recipient_modes_json TEXT NOT NULL DEFAULT '[]',
		recipient_user_ids_json TEXT NOT NULL DEFAULT '[]',
		version INTEGER NOT NULL DEFAULT 1,
		created_by TEXT NOT NULL,
		updated_by TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(tenant_id,project_id,name)
	);
	CREATE INDEX IF NOT EXISTS idx_automation_rules_scope_trigger ON automation_rules(tenant_id,project_id,trigger_event,enabled,id);
	CREATE TABLE IF NOT EXISTS automation_rule_events(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		event_key TEXT NOT NULL,
		event_type TEXT NOT NULL CHECK(event_type='requirement.status_changed'),
		subject_type TEXT NOT NULL CHECK(subject_type='requirement'),
		subject_id INTEGER NOT NULL,
		actor_user_id TEXT NOT NULL,
		from_status TEXT NOT NULL,
		to_status TEXT NOT NULL,
		occurred_at TEXT NOT NULL,
		UNIQUE(tenant_id,event_key)
	);
	CREATE INDEX IF NOT EXISTS idx_automation_events_subject ON automation_rule_events(tenant_id,project_id,subject_type,subject_id,id DESC);
	CREATE TABLE IF NOT EXISTS automation_rule_executions(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id TEXT NOT NULL,
		project_id TEXT NOT NULL,
		rule_id INTEGER NOT NULL,
		event_id INTEGER NOT NULL,
		subject_type TEXT NOT NULL CHECK(subject_type='requirement'),
		subject_id INTEGER NOT NULL,
		action_type TEXT NOT NULL CHECK(action_type='notify'),
		recipients_json TEXT NOT NULL DEFAULT '[]',
		status TEXT NOT NULL CHECK(status IN ('notified','skipped_no_recipient')),
		created_at TEXT NOT NULL,
		UNIQUE(tenant_id,project_id,rule_id,event_id)
	);
	CREATE INDEX IF NOT EXISTS idx_automation_executions_rule ON automation_rule_executions(tenant_id,project_id,rule_id,id DESC);`)
	return err
}

func (a *App) requireAutomationManager(ctx context.Context, q stateStore) error {
	role, err := a.requirementStateRole(ctx, q)
	if err != nil {
		return err
	}
	if !stateManager(role) {
		return automationRuleError{code: "automation_manager_required", message: "仅企业管理员和项目管理员可以管理自动化规则", status: http.StatusForbidden}
	}
	return nil
}

func automationRuleModes(value []string) ([]string, error) {
	seen := map[string]bool{}
	items := make([]string, 0, len(value))
	for _, raw := range value {
		item := strings.TrimSpace(raw)
		if !validChoice(item, automationRecipientModeValues) {
			return nil, invalidAutomationRule("自动化规则接收人模式数据无效")
		}
		if !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	sort.Strings(items)
	return items, nil
}

func automationRuleUserIDs(value []string) ([]string, error) {
	seen := map[string]bool{}
	items := make([]string, 0, len(value))
	for _, raw := range value {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, invalidAutomationRule("指定成员不能为空")
		}
		if !seen[id] {
			seen[id] = true
			items = append(items, id)
		}
	}
	if len(items) > 50 {
		return nil, invalidAutomationRule("每条自动化规则最多指定 50 位成员")
	}
	sort.Strings(items)
	return items, nil
}

func (a *App) validateAutomationRule(ctx context.Context, tx *sql.Tx, rule *AutomationRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if runeCount := utf8.RuneCountInString(rule.Name); runeCount == 0 || runeCount > 100 {
		return invalidAutomationRule("规则名称须为 1–100 个字符")
	}
	for _, value := range rule.Name {
		if unicode.IsControl(value) {
			return invalidAutomationRule("规则名称不能包含控制字符")
		}
	}
	if rule.Trigger == "" {
		rule.Trigger = automationRequirementStatusChanged
	}
	if rule.Action == "" {
		rule.Action = "notify"
	}
	if rule.Trigger != automationRequirementStatusChanged || rule.Action != "notify" {
		return invalidAutomationRule("当前仅支持需求状态变更后的站内通知")
	}
	statuses, err := listRequirementStatuses(ctx, tx, tenantID, a.pid())
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, status := range statuses {
		known[status.Key] = true
	}
	for _, value := range []string{rule.FromStatus, rule.ToStatus} {
		if value != "" && !known[value] {
			return invalidAutomationRule("规则条件中的状态不存在或不属于当前项目")
		}
	}
	if rule.FromStatus != "" && rule.FromStatus == rule.ToStatus {
		return invalidAutomationRule("来源状态和目标状态不能相同")
	}
	rule.RecipientModes, err = automationRuleModes(rule.RecipientModes)
	if err != nil {
		return err
	}
	rule.RecipientUserIDs, err = automationRuleUserIDs(rule.RecipientUserIDs)
	if err != nil {
		return err
	}
	if len(rule.RecipientModes) == 0 && len(rule.RecipientUserIDs) == 0 {
		return invalidAutomationRule("请至少选择一种通知接收人")
	}
	validUsers, err := a.activeAssignmentRecipients(ctx, tx, rule.RecipientUserIDs)
	if err != nil {
		return err
	}
	if len(validUsers) != len(rule.RecipientUserIDs) {
		return invalidAutomationRule("指定成员必须是当前项目可访问的有效成员")
	}
	return nil
}

func scanAutomationRule(row interface{ Scan(...any) error }, rule *AutomationRule) error {
	var modes, users string
	if err := row.Scan(&rule.ID, &rule.Name, &rule.Enabled, &rule.Trigger, &rule.FromStatus, &rule.ToStatus, &rule.Action, &modes, &users, &rule.Version, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(modes), &rule.RecipientModes); err != nil {
		return fmt.Errorf("自动化规则接收人模式数据无效: %w", err)
	}
	if err := json.Unmarshal([]byte(users), &rule.RecipientUserIDs); err != nil {
		return fmt.Errorf("自动化规则指定成员数据无效: %w", err)
	}
	return nil
}

const automationRuleSelectColumns = `id,name,enabled,trigger_event,from_status,to_status,action_type,recipient_modes_json,recipient_user_ids_json,version,created_at,updated_at`

func (a *App) getAutomationRule(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) (AutomationRule, error) {
	var rule AutomationRule
	err := scanAutomationRule(q.QueryRowContext(ctx, `SELECT `+automationRuleSelectColumns+` FROM automation_rules WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id), &rule)
	return rule, err
}

func (a *App) listAutomationRules(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]AutomationRule, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+automationRuleSelectColumns+` FROM automation_rules WHERE tenant_id=? AND project_id=? ORDER BY id DESC`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []AutomationRule{}
	for rows.Next() {
		var rule AutomationRule
		if err := scanAutomationRule(rows, &rule); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (a *App) automationRuleStatusItems(ctx context.Context, q stateStore) ([]RequirementStatus, error) {
	return listRequirementStatuses(ctx, q, tenantID, a.pid())
}

func (a *App) automationRuleMembers(q requirementPeopleQuery) ([]RequirementAssignee, error) {
	// 目录与实际投递共用可见性边界：项目成员以及企业管理员均可被规则指定，
	// 已停用、被运营禁用或退出租户的账号绝不能在这里重新暴露。
	rows, err := q.Query(`SELECT DISTINCT u.id,u.name
		FROM users u
		JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
		WHERE u.tenant_id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'
		AND (tm.role='tenant_admin' OR EXISTS(
			SELECT 1 FROM project_members pm
			WHERE pm.tenant_id=u.tenant_id AND pm.project_id=? AND pm.user_id=u.id
		))`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RequirementAssignee{}
	for rows.Next() {
		var item RequirementAssignee
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (a *App) automationRules(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/automation-rules"), "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			a.listAutomationRulesAPI(w, r)
		case http.MethodPost:
			a.createAutomationRule(w, r)
		default:
			fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		}
		return
	}
	if path == "preview" {
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
			return
		}
		a.previewAutomationRule(w, r, nil)
		return
	}
	if path == "executions" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
			return
		}
		a.listAutomationRuleExecutionsAPI(w, r)
		return
	}
	parts := strings.Split(path, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "invalid_id", "自动化规则编号不正确")
		return
	}
	if len(parts) == 2 && parts[1] == "preview" {
		if r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
			return
		}
		a.previewAutomationRule(w, r, &id)
		return
	}
	if len(parts) != 1 {
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		a.updateAutomationRule(w, r, id)
	case http.MethodDelete:
		a.deleteAutomationRule(w, r, id)
	default:
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
	}
}

// listAutomationRuleExecutionsAPI 只让项目管理者读取。执行记录会暴露当时的
// 收件人和需求标题，不能把它当作普通项目成员可浏览的通知目录。
func (a *App) listAutomationRuleExecutionsAPI(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAutomationManager(r.Context(), a.db); err != nil {
		failAutomationRule(w, err)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT e.id,e.rule_id,COALESCE(rule.name,''),e.subject_id,COALESCE(req.code,''),COALESCE(req.title,''),e.status,e.recipients_json,e.created_at
		FROM automation_rule_executions e
		LEFT JOIN automation_rules rule ON rule.tenant_id=e.tenant_id AND rule.project_id=e.project_id AND rule.id=e.rule_id
		LEFT JOIN requirements req ON req.tenant_id=e.tenant_id AND req.project_id=e.project_id AND req.id=e.subject_id
		WHERE e.tenant_id=? AND e.project_id=? ORDER BY e.id DESC LIMIT 100`, tenantID, a.pid())
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	defer rows.Close()
	items := []AutomationRuleExecution{}
	for rows.Next() {
		var item AutomationRuleExecution
		var recipients string
		if err := rows.Scan(&item.ID, &item.RuleID, &item.RuleName, &item.SubjectID, &item.SubjectCode, &item.SubjectTitle, &item.Status, &recipients, &item.CreatedAt); err != nil {
			failAutomationRule(w, err)
			return
		}
		if err := json.Unmarshal([]byte(recipients), &item.Recipients); err != nil {
			// recipients_json 由受控执行器写入；若历史数据已损坏，宁可失败也不把
			// 不可信字段当成一条空执行记录，避免审计界面给出错误结论。
			failAutomationRule(w, err)
			return
		}
		if item.Recipients == nil {
			item.Recipients = []string{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) listAutomationRulesAPI(w http.ResponseWriter, r *http.Request) {
	if err := a.requireAutomationManager(r.Context(), a.db); err != nil {
		failAutomationRule(w, err)
		return
	}
	rules, err := a.listAutomationRules(r.Context(), a.db)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	statuses, err := a.automationRuleStatusItems(r.Context(), a.db)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	members, err := a.automationRuleMembers(a.db)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": rules, "statuses": statuses, "members": members, "canManage": true})
}

func decodeAutomationRule(r *http.Request, target *AutomationRule, patch bool) error {
	var values map[string]json.RawMessage
	// 自动化规则配置很小；限制读取量并拒绝尾随 JSON，避免一个配置接口被用作
	// 大请求缓冲区，也避免代理重放拼接两个 JSON 时只悄悄采纳第一个对象。
	payload, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(payload) > 64<<10 || json.Unmarshal(payload, &values) != nil || values == nil {
		return automationRuleError{code: "invalid_json", message: "请求格式不正确", status: http.StatusBadRequest}
	}
	allowed := map[string]any{
		"name":             &target.Name,
		"enabled":          &target.Enabled,
		"trigger":          &target.Trigger,
		"fromStatus":       &target.FromStatus,
		"toStatus":         &target.ToStatus,
		"action":           &target.Action,
		"recipientModes":   &target.RecipientModes,
		"recipientUserIds": &target.RecipientUserIDs,
	}
	if patch {
		allowed["version"] = &target.Version
		if _, ok := values["version"]; !ok {
			return invalidAutomationRule("更新规则需要当前版本号")
		}
		if len(values) == 1 {
			return invalidAutomationRule("至少修改一项规则配置")
		}
	}
	for key, raw := range values {
		field, ok := allowed[key]
		if !ok {
			return invalidAutomationRule("自动化规则包含不支持的字段")
		}
		if string(raw) == "null" {
			return invalidAutomationRule("自动化规则字段不能为 null")
		}
		if err := json.Unmarshal(raw, field); err != nil {
			return invalidAutomationRule("自动化规则字段格式不正确")
		}
	}
	if patch && target.Version < 1 {
		return invalidAutomationRule("更新规则需要当前版本号")
	}
	return nil
}

func (a *App) createAutomationRule(w http.ResponseWriter, r *http.Request) {
	var rule AutomationRule
	if err := decodeAutomationRule(r, &rule, false); err != nil {
		failAutomationRule(w, err)
		return
	}
	// 新规则默认关闭；调用方明确传 true 时才允许在一次经过权限校验的保存中启用。
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireAutomationManager(r.Context(), tx); err == nil {
		err = a.validateAutomationRule(r.Context(), tx, &rule)
	}
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	now := automationStamp()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO automation_rules(tenant_id,project_id,name,enabled,trigger_event,from_status,to_status,action_type,recipient_modes_json,recipient_user_ids_json,version,created_by,updated_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,1,?,?,?,?)`, tenantID, a.pid(), rule.Name, rule.Enabled, rule.Trigger, rule.FromStatus, rule.ToStatus, rule.Action, jsonText(rule.RecipientModes), jsonText(rule.RecipientUserIDs), a.uid(), a.uid(), now, now)
	if err == nil {
		rule.ID, err = res.LastInsertId()
	}
	if err == nil {
		rule.Version, rule.CreatedAt, rule.UpdatedAt = 1, now, now
		err = a.auditAutomationRule(r.Context(), tx, "created", rule.ID, nil, rule)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			fail(w, http.StatusConflict, "automation_rule_exists", "规则名称已存在")
			return
		}
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusCreated, rule)
}

func (a *App) updateAutomationRule(w http.ResponseWriter, r *http.Request, id int64) {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireAutomationManager(r.Context(), tx); err != nil {
		failAutomationRule(w, err)
		return
	}
	rule, err := a.getAutomationRule(r.Context(), tx, id)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	before := rule
	if err = decodeAutomationRule(r, &rule, true); err != nil {
		failAutomationRule(w, err)
		return
	}
	if rule.Version != before.Version {
		fail(w, http.StatusConflict, "automation_rule_conflict", "规则已被其他管理员修改，请刷新后重试")
		return
	}
	if err = a.validateAutomationRule(r.Context(), tx, &rule); err != nil {
		failAutomationRule(w, err)
		return
	}
	now := automationStamp()
	res, err := tx.ExecContext(r.Context(), `UPDATE automation_rules SET name=?,enabled=?,trigger_event=?,from_status=?,to_status=?,action_type=?,recipient_modes_json=?,recipient_user_ids_json=?,version=version+1,updated_by=?,updated_at=? WHERE tenant_id=? AND project_id=? AND id=? AND version=?`, rule.Name, rule.Enabled, rule.Trigger, rule.FromStatus, rule.ToStatus, rule.Action, jsonText(rule.RecipientModes), jsonText(rule.RecipientUserIDs), a.uid(), now, tenantID, a.pid(), id, before.Version)
	if err == nil {
		var changed int64
		changed, err = res.RowsAffected()
		if err == nil && changed != 1 {
			err = automationRuleError{code: "automation_rule_conflict", message: "规则已被其他管理员修改，请刷新后重试", status: http.StatusConflict}
		}
	}
	if err == nil {
		rule.Version++
		rule.CreatedAt, rule.UpdatedAt = before.CreatedAt, now
		err = a.auditAutomationRule(r.Context(), tx, "updated", rule.ID, before, rule)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			fail(w, http.StatusConflict, "automation_rule_exists", "规则名称已存在")
			return
		}
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusOK, rule)
}

func (a *App) deleteAutomationRule(w http.ResponseWriter, r *http.Request, id int64) {
	version, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil || version < 1 {
		fail(w, http.StatusUnprocessableEntity, "invalid_automation_rule", "删除规则需要当前版本号")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireAutomationManager(r.Context(), tx); err != nil {
		failAutomationRule(w, err)
		return
	}
	rule, err := a.getAutomationRule(r.Context(), tx, id)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	if rule.Version != version {
		fail(w, http.StatusConflict, "automation_rule_conflict", "规则已被其他管理员修改，请刷新后重试")
		return
	}
	if err = a.auditAutomationRule(r.Context(), tx, "deleted", rule.ID, rule, nil); err == nil {
		res, deleteErr := tx.ExecContext(r.Context(), `DELETE FROM automation_rules WHERE tenant_id=? AND project_id=? AND id=? AND version=?`, tenantID, a.pid(), id, version)
		if deleteErr != nil {
			err = deleteErr
		} else {
			var changed int64
			changed, err = res.RowsAffected()
			if err == nil && changed != 1 {
				err = automationRuleError{code: "automation_rule_conflict", message: "规则已被其他管理员修改，请刷新后重试", status: http.StatusConflict}
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (a *App) previewAutomationRule(w http.ResponseWriter, r *http.Request, id *int64) {
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.requireAutomationManager(r.Context(), tx); err != nil {
		failAutomationRule(w, err)
		return
	}
	rule := AutomationRule{}
	if id != nil {
		rule, err = a.getAutomationRule(r.Context(), tx, *id)
	} else {
		err = decodeAutomationRule(r, &rule, false)
	}
	if err == nil {
		err = a.validateAutomationRule(r.Context(), tx, &rule)
	}
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	preview, err := a.automationRulePreview(r.Context(), tx, rule)
	if err != nil {
		failAutomationRule(w, err)
		return
	}
	write(w, http.StatusOK, preview)
}

func (a *App) automationRulePreview(ctx context.Context, q stateStore, rule AutomationRule) (automationRulePreview, error) {
	preview := automationRulePreview{DryRun: true, SampleItems: []struct {
		ID     int64  `json:"id"`
		Code   string `json:"code"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}{}, FromStatusEvaluatedAtRuntime: rule.FromStatus != ""}
	query := `SELECT id,code,title,status FROM requirements WHERE tenant_id=? AND project_id=?`
	args := []any{tenantID, a.pid()}
	// 预览没有历史“变更前状态”快照，目标状态仍可作为有用的样本代理；来源状态
	// 仅在结果中说明运行时会校验，不能从可变活动记录中猜测。
	if rule.ToStatus != "" {
		query += ` AND status=?`
		args = append(args, rule.ToStatus)
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+`)`, args...).Scan(&preview.MatchingSampleCount); err != nil {
		return preview, err
	}
	query += ` ORDER BY id DESC LIMIT 5`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return preview, err
	}
	defer rows.Close()
	for rows.Next() {
		var item struct {
			ID     int64  `json:"id"`
			Code   string `json:"code"`
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		if err := rows.Scan(&item.ID, &item.Code, &item.Title, &item.Status); err != nil {
			return preview, err
		}
		item.Code = requirementDisplayCode(item.ID, item.Code)
		preview.SampleItems = append(preview.SampleItems, item)
	}
	return preview, rows.Err()
}

func (a *App) auditAutomationRule(ctx context.Context, tx *sql.Tx, action string, id int64, before, after any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "automation_rule", strconv.FormatInt(id, 10), action, jsonText(before), jsonText(after), automationStamp())
	return err
}

func automationStamp() string { return timeNowUTC().Format(time.RFC3339Nano) }

// timeNowUTC 单独抽出，便于后续注入可控时钟；事件 ID 不依赖秒级 UI 时间戳。
func timeNowUTC() time.Time { return time.Now().UTC() }

// recordRequirementStatusAutomationEvent 与需求更新放在同一事务中写入稳定事件。
// activityID 是本次已保存的需求变更活动主键：同一事务内的重入、事务包装器的
// 重试都会派生同一个 event_key；不同的状态变更即使 from/to 相同也有不同活动 ID。
func (a *App) recordRequirementStatusAutomationEvent(ctx context.Context, tx *sql.Tx, requirementID, activityID int64, from, to, now string) (int64, error) {
	if activityID <= 0 {
		return 0, fmt.Errorf("automation status event requires a persisted activity id")
	}
	key := fmt.Sprintf("requirement-status:%s:%d:%d", a.pid(), requirementID, activityID)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO automation_rule_events(tenant_id,project_id,event_key,event_type,subject_type,subject_id,actor_user_id,from_status,to_status,occurred_at)VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), key, automationRequirementStatusChanged, "requirement", requirementID, a.uid(), from, to, now); err != nil {
		return 0, err
	}
	var eventID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM automation_rule_events WHERE tenant_id=? AND project_id=? AND event_key=?`, tenantID, a.pid(), key).Scan(&eventID); err != nil {
		return 0, err
	}
	return eventID, nil
}

func (a *App) automationRuleRecipients(ctx context.Context, tx *sql.Tx, rule AutomationRule, x *Requirement) ([]string, error) {
	candidates := append([]string{}, rule.RecipientUserIDs...)
	fieldModes := map[string]bool{}
	for _, mode := range rule.RecipientModes {
		switch mode {
		case automationRecipientAssignee:
			candidates = append(candidates, x.AssigneeUserIDs...)
			if len(x.AssigneeUserIDs) == 0 && x.AssigneeUserID != "" {
				candidates = append(candidates, x.AssigneeUserID)
			}
		case automationRecipientOwner:
			candidates = append(candidates, x.OwnerUserIDs...)
			if len(x.OwnerUserIDs) == 0 && x.OwnerUserID != "" {
				candidates = append(candidates, x.OwnerUserID)
			}
		case automationRecipientFrontend, automationRecipientBackend, automationRecipientAlgorithm, automationRecipientUI, automationRecipientProduct:
			// RoleWeights 是需求保存时验证过的稳定 ID；兼容旧记录中只保存
			// userId 的形式，避免历史需求因为升级而漏发。
			weight := x.RoleWeights[mode]
			candidates = append(candidates, weight.UserIDs...)
			if len(weight.UserIDs) == 0 && weight.UserID != "" {
				candidates = append(candidates, weight.UserID)
			}
		case automationRecipientFrontendLead, automationRecipientBackendLead, automationRecipientTester:
			fieldModes[mode] = true
		}
	}
	fieldRecipients, err := a.automationRequirementFieldRecipients(ctx, tx, x.ID, fieldModes)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, fieldRecipients...)
	// 这里统一按“当前”可见性复核，而不是相信历史绑定：已停用、被运营禁用、
	// 已退出本项目的成员都不会收到无法打开的通知。显式成员、职能成员和字段
	// 成员共用同一批稳定 ID，因此最终也会去重并稳定排序。
	return a.activeAssignmentRecipients(ctx, tx, candidates)
}

// automationRequirementFieldRecipients 只读取三个系统预设的人员字段。字段 key
// 是协议的一部分，用户改显示名称不会扩大收件人范围；同时要求定义仍启用且类型
// 为 user/users，避免把同名普通文本或历史软删除数据误当作成员 ID。
func (a *App) automationRequirementFieldRecipients(ctx context.Context, tx *sql.Tx, requirementID int64, modes map[string]bool) ([]string, error) {
	if requirementID <= 0 || len(modes) == 0 {
		return []string{}, nil
	}
	keys := map[string]bool{}
	if modes[automationRecipientFrontendLead] {
		keys["frontend_leads"] = true
	}
	if modes[automationRecipientBackendLead] {
		keys["backend_leads"] = true
	}
	if modes[automationRecipientTester] {
		keys["testers"] = true
	}
	if len(keys) == 0 {
		return []string{}, nil
	}

	// 三个固定 key 让查询始终有明确上限；不根据项目全量成员或自由字段扫描，
	// 防止规则配置被用作枚举组织目录的旁路。
	rows, err := tx.QueryContext(ctx, `SELECT d.key,d.type,v.value_json
		FROM field_values v
		JOIN field_definitions d ON d.id=v.field_definition_id
			AND d.tenant_id=v.tenant_id AND d.project_id=v.project_id
			AND d.object_type=v.object_type AND d.deleted_at='' AND d.enabled=1
		WHERE v.tenant_id=? AND v.project_id=? AND v.object_type='requirement' AND v.object_id=?
			AND d.key IN ('frontend_leads','backend_leads','testers')
			AND d.type IN ('user','users')
		ORDER BY d.key,d.id`, tenantID, a.pid(), requirementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []string{}
	for rows.Next() {
		var key, fieldType, raw string
		if err := rows.Scan(&key, &fieldType, &raw); err != nil {
			return nil, err
		}
		if !keys[key] {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("automation recipient field %s contains invalid JSON: %w", key, err)
		}
		ids, valid := fieldPersonIDs(value, fieldType == "users")
		if !valid {
			return nil, fmt.Errorf("automation recipient field %s contains invalid member IDs", key)
		}
		items = append(items, ids...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *App) executeRequirementStatusAutomationRules(ctx context.Context, tx *sql.Tx, x *Requirement, from string, activityID int64, now string) error {
	if from == x.Status {
		return nil
	}
	eventID, err := a.recordRequirementStatusAutomationEvent(ctx, tx, x.ID, activityID, from, x.Status, now)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+automationRuleSelectColumns+` FROM automation_rules WHERE tenant_id=? AND project_id=? AND enabled=1 AND trigger_event=? AND (from_status='' OR from_status=?) AND (to_status='' OR to_status=?) ORDER BY id`, tenantID, a.pid(), automationRequirementStatusChanged, from, x.Status)
	if err != nil {
		return err
	}
	defer rows.Close()
	rules := []AutomationRule{}
	for rows.Next() {
		var rule AutomationRule
		if err := scanAutomationRule(rows, &rule); err != nil {
			return err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rule := range rules {
		if err := a.executeAutomationRule(ctx, tx, rule, eventID, x, from, now); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) executeAutomationRule(ctx context.Context, tx *sql.Tx, rule AutomationRule, eventID int64, x *Requirement, from, now string) error {
	normalizeRequirementCode(x)
	recipients, err := a.automationRuleRecipients(ctx, tx, rule, x)
	if err != nil {
		return err
	}
	status := "notified"
	if len(recipients) == 0 {
		status = "skipped_no_recipient"
	}
	// 先写执行记录再写通知。联合唯一键是幂等闸门：同一事件/规则再次进入时
	// 直接空操作，不会重复写收件箱。事件自身保存了不可变的需求和状态快照。
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO automation_rule_executions(tenant_id,project_id,rule_id,event_id,subject_type,subject_id,action_type,recipients_json,status,created_at)VALUES(?,?,?,?,'requirement',?,'notify',?,?,?)`, tenantID, a.pid(), rule.ID, eventID, x.ID, jsonText(recipients), status, now)
	if err != nil {
		return err
	}
	written, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if written == 0 || len(recipients) == 0 {
		return nil
	}
	for _, recipient := range recipients {
		dedupe := fmt.Sprintf("automation:%d:%d:%s", rule.ID, eventID, recipient)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), recipient, a.uid(), "automation.requirement_status_changed", "requirement", x.ID, "自动化规则："+rule.Name, fmt.Sprintf("%s %s 的状态已从「%s」变更为「%s」", x.Code, x.Title, from, x.Status), now, dedupe); err != nil {
			return err
		}
	}
	// 执行审计保留规则名、接收人和事件 ID，且完全不写 notification_outbox；
	// 当前切片的 notify 仅限站内通知，不能悄然触发企业微信或其他外部通道。
	return a.auditAutomationRule(ctx, tx, "executed", rule.ID, nil, map[string]any{"eventId": eventID, "requirementId": x.ID, "recipients": recipients, "status": status})
}
