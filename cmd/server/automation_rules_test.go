package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func automationRulePayload(name string, enabled bool) string {
	return jsonText(map[string]any{
		"name":             name,
		"enabled":          enabled,
		"trigger":          automationRequirementStatusChanged,
		"fromStatus":       "规划中",
		"toStatus":         "开发中",
		"action":           "notify",
		"recipientModes":   []string{"assignee", "owner"},
		"recipientUserIds": []string{"u_pm"},
	})
}

func createAutomationRuleForTest(t *testing.T, a *App, body string) AutomationRule {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/automation-rules", "u_admin", projectID, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create automation rule: %d %s", w.Code, w.Body.String())
	}
	var rule AutomationRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil {
		t.Fatal(err)
	}
	return rule
}

func countAutomationRows(t *testing.T, a *App, table string) int {
	t.Helper()
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=?`, tenantID, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAutomationRulesProjectManagerCRUDAndDryRun(t *testing.T) {
	a := testApp(t)

	// 规则属于项目管理配置，普通产品角色不能读取或写入，避免泄露可见成员
	// 和自动化条件；所有 CRUD/演练处理器仍会在事务内二次鉴权。
	w := apiRequest(a, http.MethodGet, "/api/automation-rules", "u_pm", projectID, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-manager listed automation rules: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/automation-rules", "u_pm", projectID, automationRulePayload("越权规则", false))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-manager created automation rule: %d %s", w.Code, w.Body.String())
	}
	// 项目管理员（不是企业管理员）也必须具备完整的配置权限。
	if _, err := a.db.Exec(`UPDATE memberships SET role='project_admin' WHERE tenant_id=? AND project_id=? AND user_id='u_pm'; UPDATE project_members SET role='project_admin' WHERE tenant_id=? AND project_id=? AND user_id='u_pm'`, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodGet, "/api/automation-rules", "u_pm", projectID, "")
	if w.Code != http.StatusOK || jsonMap(t, w)["canManage"] != true {
		t.Fatalf("project manager cannot read automation rules: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/automation-rules", "u_pm", projectID, automationRulePayload("项目管理员规则", false))
	if w.Code != http.StatusCreated {
		t.Fatalf("project manager cannot create automation rule: %d %s", w.Code, w.Body.String())
	}
	var projectManagerRule AutomationRule
	if err := json.Unmarshal(w.Body.Bytes(), &projectManagerRule); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/automation-rules/%d?version=%d", projectManagerRule.ID, projectManagerRule.Version), "u_pm", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("project manager cannot delete automation rule: %d %s", w.Code, w.Body.String())
	}
	// 配置接口只接受白名单字段和单一 JSON 对象；避免客户端把只读审计字段
	// 或被代理拼接的尾随请求混入一次规则保存。
	w = apiRequest(a, http.MethodPost, "/api/automation-rules", "u_admin", projectID, `{"name":"非法字段","id":99}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown automation field accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/automation-rules", "u_admin", projectID, automationRulePayload("尾随 JSON", false)+` {}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("trailing automation JSON accepted: %d %s", w.Code, w.Body.String())
	}

	rule := createAutomationRuleForTest(t, a, automationRulePayload("开发交接提醒", false))
	if rule.Enabled || rule.Version != 1 || rule.Trigger != automationRequirementStatusChanged || rule.Action != "notify" {
		t.Fatalf("new automation rule was not safe by default: %+v", rule)
	}
	if got := countAutomationRows(t, a, "automation_rules"); got != 1 {
		t.Fatalf("rule not persisted: %d", got)
	}

	// 预览必须是纯读操作：规则、事件、执行和通知任意一个发生变化都代表 dry-run 失效。
	beforeRules := countAutomationRows(t, a, "automation_rules")
	beforeEvents := countAutomationRows(t, a, "automation_rule_events")
	beforeExecutions := countAutomationRows(t, a, "automation_rule_executions")
	var beforeNotices int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=?`, tenantID).Scan(&beforeNotices); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/automation-rules/%d/preview", rule.ID), "u_admin", projectID, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rule preview: %d %s", w.Code, w.Body.String())
	}
	var preview automationRulePreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || !preview.DryRun || !preview.FromStatusEvaluatedAtRuntime {
		t.Fatalf("preview response invalid: %#v %v", preview, err)
	}
	if countAutomationRows(t, a, "automation_rules") != beforeRules || countAutomationRows(t, a, "automation_rule_events") != beforeEvents || countAutomationRows(t, a, "automation_rule_executions") != beforeExecutions {
		t.Fatal("dry-run wrote automation records")
	}
	var afterNotices int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=?`, tenantID).Scan(&afterNotices); err != nil || afterNotices != beforeNotices {
		t.Fatalf("dry-run wrote inbox notification: %d -> %d (%v)", beforeNotices, afterNotices, err)
	}

	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/automation-rules/%d", rule.ID), "u_admin", projectID, `{"enabled":true,"version":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("enable rule: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || !rule.Enabled || rule.Version != 2 {
		t.Fatalf("enabled rule response invalid: %#v %v", rule, err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/automation-rules/%d", rule.ID), "u_admin", projectID, `{"name":"过期写入","version":1}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale automation update accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/automation-rules/%d", rule.ID), "u_admin", projectID, `{"enabled":false}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update without optimistic version accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/automation-rules/%d", rule.ID), "u_admin", projectID, `{"enabled":false,"version":0}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-positive optimistic version accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/automation-rules/%d?version=%d", rule.ID, rule.Version), "u_admin", projectID, "")
	if w.Code != http.StatusOK || jsonMap(t, w)["deleted"] != true {
		t.Fatalf("delete rule: %d %s", w.Code, w.Body.String())
	}
}

func TestAutomationRuleStatusEventIsTransactionalAndIdempotent(t *testing.T) {
	a := testApp(t)
	rule := createAutomationRuleForTest(t, a, automationRulePayload("状态变更通知", true))

	// 自动化站内通知也统一进入收件人已启用的个人机器人队列。
	if _, err := a.db.Exec(`INSERT OR REPLACE INTO user_wecom_webhooks(tenant_id,user_id,encrypted_url,enabled,version,updated_at)VALUES(?,?,X'00',1,1,?)`, tenantID, "u_pm", automationStamp()); err != nil {
		t.Fatal(err)
	}
	x := planningRequirement(t, a, `{"title":"自动化状态事件","assigneeUserIds":["u_front"],"ownerUserIds":["u_back"]}`)
	updated := patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)
	if updated.Status != "开发中" {
		t.Fatalf("status transition did not persist: %+v", updated)
	}
	if got := countAutomationRows(t, a, "automation_rule_events"); got != 1 {
		t.Fatalf("expected one durable automation event, got %d", got)
	}
	if got := countAutomationRows(t, a, "automation_rule_executions"); got != 1 {
		t.Fatalf("expected one execution record, got %d", got)
	}
	var notices int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='automation.requirement_status_changed' AND subject_id=?`, tenantID, projectID, x.ID).Scan(&notices); err != nil || notices != 3 {
		t.Fatalf("rule recipients were not deduplicated/validated: %d (%v)", notices, err)
	}
	var body string
	if err := a.db.QueryRow(`SELECT body FROM user_notifications WHERE tenant_id=? AND event_type='automation.requirement_status_changed' AND subject_id=? LIMIT 1`, tenantID, x.ID).Scan(&body); err != nil || body == "" || !containsAll(body, "规划中", "开发中") {
		t.Fatalf("status context missing from automation notice: %q %v", body, err)
	}
	var outbox int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND event_type='automation.requirement_status_changed'`, tenantID).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("automation unexpectedly created external outbox row: %d %v", outbox, err)
	}
	var deliveries int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_wecom_deliveries d JOIN user_notifications n ON n.id=d.notification_id WHERE n.event_type='automation.requirement_status_changed'`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("automation notification did not enter recipient webhook delivery queue: %d %v", deliveries, err)
	}
	var audits int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='automation_rule' AND object_id=? AND action='executed'`, tenantID, projectID, fmt.Sprint(rule.ID)).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("execution audit missing: %d %v", audits, err)
	}

	// 重入整个状态事件处理路径，而不仅是单独重放执行记录。活动 ID 是状态变更
	// 的稳定锚点，因此 event_key 和 rule/event 幂等键都必须命中既有记录。
	var activityID int64
	if err := a.db.QueryRow(`SELECT id FROM activities WHERE tenant_id=? AND project_id=? AND requirement_id=? AND event='updated' ORDER BY id DESC LIMIT 1`, tenantID, projectID, x.ID).Scan(&activityID); err != nil {
		t.Fatal(err)
	}
	var eventKey string
	if err := a.db.QueryRow(`SELECT event_key FROM automation_rule_events WHERE tenant_id=? AND project_id=? AND subject_id=?`, tenantID, projectID, x.ID).Scan(&eventKey); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("requirement-status:%s:%d:%d", projectID, x.ID, activityID); eventKey != want {
		t.Fatalf("event key is not stable: got %q want %q", eventKey, want)
	}
	stored, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeNotices := notices
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.executeRequirementStatusAutomationRules(context.Background(), tx, &stored, "规划中", activityID, automationStamp()); err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := countAutomationRows(t, a, "automation_rule_events"); got != 1 {
		t.Fatalf("stable event replay created another event: %d", got)
	}
	if got := countAutomationRows(t, a, "automation_rule_executions"); got != 1 {
		t.Fatalf("event replay created another execution: %d", got)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='automation.requirement_status_changed' AND subject_id=?`, tenantID, projectID, x.ID).Scan(&notices); err != nil || notices != beforeNotices {
		t.Fatalf("event replay duplicated inbox rows: %d -> %d (%v)", beforeNotices, notices, err)
	}
}

func TestAutomationRuleExecutionsAreManagerOnlyAndRetainDeletedRuleHistory(t *testing.T) {
	a := testApp(t)
	rule := createAutomationRuleForTest(t, a, automationRulePayload("执行日志可见性", true))
	x := planningRequirement(t, a, `{"title":"执行日志关联需求","assigneeUserIds":["u_front"]}`)
	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)

	// 普通项目成员不能借执行日志枚举规则名称、收件人或需求标题。
	w := apiRequest(a, http.MethodGet, "/api/automation-rules/executions", "u_pm", projectID, "")
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), x.Title) {
		t.Fatalf("non-manager read automation executions: %d %s", w.Code, w.Body.String())
	}

	// 删除规则不会删除已发生的执行审计；日志仍应显示受影响需求和当时的收件人。
	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/automation-rules/%d?version=%d", rule.ID, rule.Version), "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete rule before execution log check: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/automation-rules/executions", "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list automation executions: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Items []AutomationRuleExecution `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 {
		t.Fatalf("execution log response: %#v %v", result, err)
	}
	item := result.Items[0]
	if item.RuleID != rule.ID || item.RuleName != "" || item.SubjectID != x.ID || item.SubjectCode != x.Code || item.SubjectTitle != x.Title || item.Status != "notified" || strings.Join(item.Recipients, ",") != "u_front,u_pm" {
		t.Fatalf("execution log row mismatch: %#v", item)
	}
}

func TestAutomationRuleFailureRollsBackRequirementStatus(t *testing.T) {
	a := testApp(t)
	createAutomationRuleForTest(t, a, automationRulePayload("失败时回滚", true))
	x := planningRequirement(t, a, `{"title":"自动化事务回滚"}`)
	// 用数据库触发器模拟站内通知持久化失败；状态、事件和执行记录必须一起回滚。
	if _, err := a.db.Exec(`CREATE TRIGGER reject_automation_notice BEFORE INSERT ON user_notifications WHEN NEW.event_type='automation.requirement_status_changed' BEGIN SELECT RAISE(ABORT,'automation notification blocked'); END;`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"status":"开发中"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("notification failure did not fail transaction: %d %s", w.Code, w.Body.String())
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, x.ID).Scan(&status); err != nil || status != "规划中" {
		t.Fatalf("status persisted despite failed automation notification: %q %v", status, err)
	}
	if got := countAutomationRows(t, a, "automation_rule_events"); got != 0 {
		t.Fatalf("failed transaction left event: %d", got)
	}
	if got := countAutomationRows(t, a, "automation_rule_executions"); got != 0 {
		t.Fatalf("failed transaction left execution: %d", got)
	}
}

func automationRuleRecipientsForTest(t *testing.T, a *App, rule AutomationRule, x *Requirement) []string {
	t.Helper()
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	recipients, err := a.automationRuleRecipients(context.Background(), tx, rule, x)
	if err != nil {
		t.Fatal(err)
	}
	return recipients
}

func TestAutomationRuleRoleAndPresetRecipientsStayBoundAndDeduplicated(t *testing.T) {
	a := testApp(t)
	// 前后端负责人是可选预设；显式安装后，保存时仍会由字段自身按项目角色
	// 校验。自动化只读取这三个固定的人员字段，绝不会按项目角色群发。
	w := apiRequest(a, http.MethodPost, "/api/field-presets/apply", "u_admin", projectID, `{"objectType":"requirement","keys":["frontend_leads","backend_leads"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install lead presets: %d %s", w.Code, w.Body.String())
	}
	x := planningRequirement(t, a, jsonText(map[string]any{
		"title": "自动化职能收件人",
		"roleWeights": map[string]any{
			"frontend":  map[string]any{"userIds": []string{"u_front", "u_front_lead"}, "value": 1},
			"backend":   map[string]any{"userIds": []string{"u_back", "u_back_lead"}, "value": 1},
			"algorithm": map[string]any{"userIds": []string{"u_algo"}, "value": 1},
			"ui":        map[string]any{"userIds": []string{"u_ui"}, "value": 1},
			"product":   map[string]any{"userIds": []string{"u_pm"}, "value": 1},
		},
		"customFields": map[string]any{
			"frontend_leads": []string{"u_front_lead"},
			"backend_leads":  []string{"u_back_lead"},
			"testers":        []string{"u_qa"},
		},
	}))

	// 同一人既可能是需求前端成员，又可能是前端负责人；最终只投递一条，
	// 而未绑定的 u_viewer 即使仍是项目成员也不能收到“角色群发”。
	rule := createAutomationRuleForTest(t, a, jsonText(map[string]any{
		"name": "职能交接提醒", "enabled": true, "trigger": automationRequirementStatusChanged,
		"fromStatus": "规划中", "toStatus": "开发中", "action": "notify",
		"recipientModes": []string{
			automationRecipientFrontend, automationRecipientBackend, automationRecipientAlgorithm,
			automationRecipientUI, automationRecipientProduct, automationRecipientFrontendLead,
			automationRecipientBackendLead, automationRecipientTester,
		},
	}))
	stored, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := automationRuleRecipientsForTest(t, a, rule, &stored)
	want := []string{"u_algo", "u_back", "u_back_lead", "u_front", "u_front_lead", "u_pm", "u_qa", "u_ui"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("role/preset recipients = %v, want %v", got, want)
	}
	if _, err := automationRuleModes([]string{"qa"}); err == nil {
		t.Fatal("a project-wide role/cohort mode was accepted")
	}

	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)
	rows, err := a.db.Query(`SELECT recipient_user_id FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='automation.requirement_status_changed' AND subject_id=? ORDER BY recipient_user_id`, tenantID, projectID, x.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	notified := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		notified = append(notified, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(notified, ",") != strings.Join(want, ",") {
		t.Fatalf("automation notifications = %v, want exactly bound people %v", notified, want)
	}
	var recipientsJSON string
	if err := a.db.QueryRow(`SELECT recipients_json FROM automation_rule_executions WHERE tenant_id=? AND project_id=? AND rule_id=? AND subject_id=?`, tenantID, projectID, rule.ID, x.ID).Scan(&recipientsJSON); err != nil {
		t.Fatal(err)
	}
	if recipientsJSON != jsonText(want) {
		t.Fatalf("execution audit recipients = %s, want %s", recipientsJSON, jsonText(want))
	}
}

func TestAutomationRuleRecipientVisibilitySkipsInactiveAndCrossProjectBindings(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"不应泄漏的自动化接收人","customFields":{"testers":["u_qa"]}}`)

	// 模拟一条历史字段值混入了仅属于另一个项目的成员。该值不应变成当前
	// 项目的投递权限：最终统一的 activeAssignmentRecipients 只认可当前项目
	// 活跃成员（或当前企业管理员），也会排除随后停用的测试人员。
	now := automationStamp()
	if _, err := a.db.Exec(`INSERT INTO users(id,tenant_id,name,email,active)VALUES('u_automation_other',?,?,?,1);
		INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,'u_automation_other','member','active',?,?);
		INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,'u_automation_other','viewer');
		INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,'u_automation_other','viewer',?,?)`, tenantID, "跨项目成员", "automation-other@devflow.local", tenantID, now, now, tenantID, insightProjectID, tenantID, insightProjectID, now, now); err != nil {
		t.Fatal(err)
	}
	var testersID int64
	if err := a.db.QueryRow(`SELECT id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key='testers' AND deleted_at=''`, tenantID, projectID).Scan(&testersID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE field_values SET value_json=? WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND object_id=? AND field_definition_id=?`, jsonText([]string{"u_qa", "u_automation_other"}), tenantID, projectID, x.ID, testersID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE tenant_id=? AND id='u_qa'`, tenantID); err != nil {
		t.Fatal(err)
	}

	rule := createAutomationRuleForTest(t, a, jsonText(map[string]any{
		"name": "测试接收人可见性", "enabled": true, "trigger": automationRequirementStatusChanged,
		"fromStatus": "规划中", "toStatus": "开发中", "action": "notify",
		"recipientModes": []string{automationRecipientTester},
	}))
	stored, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recipients := automationRuleRecipientsForTest(t, a, rule, &stored); len(recipients) != 0 {
		t.Fatalf("inactive/cross-project IDs leaked into recipients: %v", recipients)
	}

	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)
	var status, recipientsJSON string
	if err := a.db.QueryRow(`SELECT status,recipients_json FROM automation_rule_executions WHERE tenant_id=? AND project_id=? AND rule_id=? AND subject_id=?`, tenantID, projectID, rule.ID, x.ID).Scan(&status, &recipientsJSON); err != nil {
		t.Fatal(err)
	}
	if status != "skipped_no_recipient" || recipientsJSON != "[]" {
		t.Fatalf("no-recipient execution was not audited as skipped: %q %s", status, recipientsJSON)
	}
	var notices int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='automation.requirement_status_changed' AND subject_id=?`, tenantID, projectID, x.ID).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 0 {
		t.Fatalf("skipped automation wrote notices: %d", notices)
	}
}

func TestAutomationRuleRejectsInvisibleExplicitRecipient(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodPost, "/api/automation-rules", "u_admin", projectID, jsonText(map[string]any{
		"name": "无效收件人", "trigger": automationRequirementStatusChanged, "action": "notify", "recipientUserIds": []string{"u_not_found"},
	}))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid explicit recipient accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestAutomationRuleNotificationLocalizesOnlySystemGrammar(t *testing.T) {
	// 规则名称、需求编号和标题来自业务数据。英文通知只能翻译这一类事件
	// 固定的连接语和内置状态，不能对自由文本做全局替换。
	title, body := localizedNotification("en-US", "automation.requirement_status_changed", "自动化规则：研发交接 @原样保留", "000101 客户自定义：规划中 的状态已从「规划中」变更为「开发中」")
	if title != "Automation rule: 研发交接 @原样保留" {
		t.Fatalf("automation title localized unexpectedly: %q", title)
	}
	if body != "Status changed from “Planning” to “In development”: 000101 客户自定义：规划中" {
		t.Fatalf("automation body localized unexpectedly: %q", body)
	}
	// 不是自动化模板的内容（即便带有相似的中文片段）必须完全保留。
	title, body = localizedNotification("en-US", "automation.requirement_status_changed", "用户自定义标题", "用户填写：状态已从「规划中」变更为「开发中」")
	if title != "用户自定义标题" || body != "用户填写：状态已从「规划中」变更为「开发中」" {
		t.Fatalf("free-form automation content was changed: %q %q", title, body)
	}
}

func TestAutomationRulesMigrationIsRepeatable(t *testing.T) {
	a := testApp(t)
	rule := createAutomationRuleForTest(t, a, automationRulePayload("重复迁移保留规则", false))
	if err := a.migrateAutomationRules(); err != nil {
		t.Fatalf("repeat automation migration failed: %v", err)
	}
	stored, err := a.getAutomationRule(context.Background(), a.db, rule.ID)
	if err != nil || stored.Name != rule.Name || stored.Enabled {
		t.Fatalf("repeat migration changed an existing rule: %#v %v", stored, err)
	}
}

func TestAutomationRuleRoleErrorKeepsForbiddenStatus(t *testing.T) {
	w := httptest.NewRecorder()
	failAutomationRule(w, stateError{code: "forbidden", message: "无权访问该项目", status: http.StatusForbidden})
	if w.Code != http.StatusForbidden || jsonMap(t, w)["error"].(map[string]any)["code"] != "forbidden" {
		t.Fatalf("project visibility error was masked: %d %s", w.Code, w.Body.String())
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
