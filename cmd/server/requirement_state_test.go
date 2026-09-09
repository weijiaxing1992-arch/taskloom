package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func statusList(t *testing.T, a *App, project string) []RequirementStatus {
	t.Helper()
	w := apiRequest(a, "GET", "/api/requirement-statuses", "u_admin", project, "")
	if w.Code != 200 {
		t.Fatalf("states: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []RequirementStatus `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Items
}
func statusByKey(t *testing.T, a *App, project, key string) RequirementStatus {
	t.Helper()
	for _, s := range statusList(t, a, project) {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("state %s missing", key)
	return RequirementStatus{}
}
func statePatch(t *testing.T, a *App, project string, s RequirementStatus, body string) RequirementStatus {
	t.Helper()
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirement-statuses/%d", s.ID), "u_admin", project, body)
	if w.Code != 200 {
		t.Fatalf("status patch: %d %s", w.Code, w.Body.String())
	}
	var out RequirementStatus
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("bad state response")
	}
	return out
}
func getWorkflow(t *testing.T, a *App, project string) RequirementWorkflow {
	t.Helper()
	w := apiRequest(a, "GET", "/api/requirement-workflow", "u_admin", project, "")
	if w.Code != 200 {
		t.Fatalf("workflow: %d %s", w.Code, w.Body.String())
	}
	var out RequirementWorkflow
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("bad workflow response")
	}
	return out
}

func TestRequirementStateDefaultsMigrationPreservesExisting(t *testing.T) {
	a := testApp(t)
	states := statusList(t, a, projectID)
	if len(states) != 20 {
		t.Fatalf("expected 15 requested and 5 compatibility states, got %d", len(states))
	}
	for _, key := range []string{"前端已完成", "后端已完成", "开发完成", "后端完成 | 前端开发中", "前端完成 | 后端开发中", "冒烟测试完成"} {
		if statusByKey(t, a, projectID, key).Category != "doing" {
			t.Fatalf("partial completion must remain doing: %s", key)
		}
	}
	s := statePatch(t, a, projectID, statusByKey(t, a, projectID, "开发中"), `{"name":"研发实施","color":"#112233","sortOrder":123}`)
	f := getWorkflow(t, a, projectID)
	var before string
	if err := a.db.QueryRow(`SELECT title||'|'||status||'|'||sprint||'|'||tags FROM requirements WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	again := statusByKey(t, a, projectID, "开发中")
	if again.Name != s.Name || again.Color != s.Color || again.SortOrder != 123 {
		t.Fatal("migration reset administrator config")
	}
	if getWorkflow(t, a, projectID).Version != f.Version {
		t.Fatal("migration reset or rewrote workflow")
	}
	var after string
	a.db.QueryRow(`SELECT title||'|'||status||'|'||sprint||'|'||tags FROM requirements WHERE id=1`).Scan(&after)
	if before != after {
		t.Fatal("state migration rewrote business data")
	}
	w := apiRequest(a, "POST", "/api/requirement-statuses/defaults", "u_admin", projectID, `{}`)
	if w.Code != 200 || statusByKey(t, a, projectID, "开发中").Name != s.Name {
		t.Fatalf("defaults overwrote customization: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementStateDefaultColorsMatchProcessCategories(t *testing.T) {
	groups := []struct {
		keys            []string
		category, color string
	}{
		{[]string{"规划中"}, "todo", "#059669"},
		{[]string{"评审中"}, "todo", "#D97706"},
		{[]string{"草稿", "待开发"}, "todo", "#64748B"},
		{[]string{"待上线", "流程挂起"}, "doing", "#D97706"},
		{[]string{"后端已完成", "前端已完成", "开发完成"}, "doing", "#0891B2"},
		{[]string{"测试中", "冒烟测试完成"}, "doing", "#7C3AED"},
		{[]string{"开发中", "实现中", "后端完成 | 前端开发中", "前端完成 | 后端开发中"}, "doing", "#2563EB"},
		{[]string{"已上线", "已完成"}, "done", "#059669"},
		{[]string{"已拒绝", "流程终止"}, "cancelled", "#DC2626"},
		{[]string{"已取消"}, "cancelled", "#64748B"},
	}
	states := requirementCategoryMap(defaultRequirementStatuses())
	checked := 0
	for _, group := range groups {
		for _, key := range group.keys {
			state, ok := states[key]
			if !ok || state.Color != group.color || state.Category != group.category {
				t.Fatalf("incorrect process color/category for %s: %+v", key, state)
			}
			checked++
		}
	}
	if checked != len(states) {
		t.Fatal("a default status is missing color coverage")
	}
}

func TestRequirementStatePermissionsAndProjectIsolation(t *testing.T) {
	a := testApp(t)
	for _, user := range []string{"u_viewer", "u_front", "u_pm"} {
		w := apiRequest(a, "GET", "/api/requirement-statuses", user, projectID, "")
		if w.Code != 200 || jsonMap(t, w)["canManage"] != false {
			t.Fatalf("member read failed: %d %s", w.Code, w.Body.String())
		}
		w = apiRequest(a, "POST", "/api/requirement-statuses", user, projectID, `{"name":"越权状态"}`)
		if w.Code != 403 {
			t.Fatalf("non-admin managed states: %d", w.Code)
		}
	}
	if _, err := a.db.Exec(`UPDATE memberships SET role='viewer' WHERE user_id='u_front';UPDATE project_members SET role='viewer' WHERE user_id='u_front'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/requirement-statuses", "/api/requirement-workflow"} {
		w := apiRequest(a, "GET", path, "u_front", projectID, "")
		if w.Code != 200 {
			t.Fatalf("viewer cannot read: %d", w.Code)
		}
	}
	w := apiRequest(a, "PUT", "/api/requirement-workflow", "u_front", projectID, jsonText(getWorkflow(t, a, projectID)))
	if w.Code != 403 {
		t.Fatal("viewer wrote workflow")
	}
	s := statusByKey(t, a, insightProjectID, "开发中")
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirement-statuses/%d", s.ID), "u_admin", projectID, `{"name":"越项目改名"}`)
	if w.Code != 404 {
		t.Fatalf("cross-project config allowed: %d", w.Code)
	}
	w = apiRequest(a, "GET", "/api/requirement-statuses", "u_front", insightProjectID, "")
	if w.Code != 403 {
		t.Fatal("nonmember read foreign config")
	}
}

func TestRequirementStateCustomStableKeyDisabledAndInitialGuards(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/requirement-statuses", "u_admin", projectID, `{"name":"联合验收","color":"#abc123","category":"doing"}`)
	if w.Code != 201 {
		t.Fatalf("create status: %d %s", w.Code, w.Body.String())
	}
	var s RequirementStatus
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.System || s.Key == s.Name || s.Color != "#ABC123" {
		t.Fatal("custom stable key metadata invalid")
	}
	x := planningRequirement(t, a, `{"title":"自定义状态需求"}`)
	if x.Status != "规划中" {
		t.Fatal("new requirement did not use initial state")
	}
	f := getWorkflow(t, a, projectID)
	f.Transitions = append(f.Transitions, RequirementTransition{"规划中", s.Key, []string{"tenant_admin"}}, RequirementTransition{s.Key, "规划中", []string{"tenant_admin"}})
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 200 {
		t.Fatalf("custom edge: %d %s", w.Code, w.Body.String())
	}
	x = patchPlanningRequirement(t, a, x.ID, jsonText(map[string]string{"status": s.Key}))
	if x.StatusName != "联合验收" || x.StatusCategory != "doing" {
		t.Fatal("custom metadata missing")
	}
	s = statePatch(t, a, projectID, s, `{"name":"联合验收通过前","enabled":false}`)
	x = patchPlanningRequirement(t, a, x.ID, `{"remarks":"停用后仍可维护历史需求"}`)
	if x.Status != s.Key || x.StatusName != s.Name {
		t.Fatal("disabled historical state lost")
	}
	patchPlanningRequirement(t, a, x.ID, `{"status":"规划中"}`)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(map[string]string{"status": s.Key}))
	if w.Code != 422 {
		t.Fatalf("disabled state selectable: %d", w.Code)
	}
	initial := statusByKey(t, a, projectID, "规划中")
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirement-statuses/%d", initial.ID), "u_admin", projectID, `{"enabled":false}`)
	if w.Code != 422 || !statusByKey(t, a, projectID, "规划中").Enabled {
		t.Fatal("initial state disabled")
	}
	for _, body := range []string{`{"title":"越过流程","status":"已上线"}`, `{"title":"未知状态","status":"missing"}`} {
		w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatalf("creation bypassed workflow: %d", w.Code)
		}
	}
	if draft := planningRequirement(t, a, `{"title":"合法草稿","status":"草稿"}`); draft.Status != "草稿" {
		t.Fatal("draft creation removed")
	}
}

func TestRequirementWorkflowRoleMatrixVersionAndAtomicDenial(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"受控流转","assigneeUserIds":["u_front"]}`)
	f := getWorkflow(t, a, projectID)
	f.Transitions = []RequirementTransition{{"规划中", "开发中", []string{"frontend"}}, {"开发中", "规划中", []string{"product"}}}
	w := apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 200 {
		t.Fatalf("save flow: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 409 {
		t.Fatalf("stale flow overwrote config: %d", w.Code)
	}
	var before int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&before)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"status":"开发中","remarks":"必须回滚"}`)
	if w.Code != 403 {
		t.Fatalf("admin bypassed explicit graph: %d", w.Code)
	}
	stored, err := a.get(x.ID)
	if err != nil || stored.Status != "规划中" || stored.Remarks != "" {
		t.Fatal("denied transition changed requirement")
	}
	var after int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&after)
	if after != before {
		t.Fatal("denied transition notified")
	}
	w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/transitions", x.ID), "u_front", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "开发中") {
		t.Fatal("allowed transitions missing")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_front", projectID, `{"status":"开发中"}`)
	if w.Code != 200 {
		t.Fatalf("authorized transition denied: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_front", projectID, `{"status":"规划中"}`)
	if w.Code != 403 {
		t.Fatal("frontend gained product rollback edge")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_pm", projectID, `{"status":"规划中"}`)
	if w.Code != 200 {
		t.Fatalf("product rollback denied: %d", w.Code)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"status":"规划中","remarks":"同状态正常编辑"}`)
	if w.Code != 200 {
		t.Fatal("same state edit incorrectly requires edge")
	}
	f = getWorkflow(t, a, projectID)
	f.Transitions[0].Roles = []string{"viewer"}
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 422 {
		t.Fatal("viewer role admitted to transition graph")
	}
}

func TestRequirementStateStatisticsAndCrossProjectMetadata(t *testing.T) {
	a := testApp(t)
	statePatch(t, a, insightProjectID, statusByKey(t, a, insightProjectID, "开发中"), `{"name":"模型评估中","color":"#123456"}`)
	w := apiRequest(a, "GET", "/api/my-work?project=all", "u_algo", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"statusName":"模型评估中"`) || !strings.Contains(w.Body.String(), `"requirementStatuses"`) {
		t.Fatalf("cross-project work metadata missing: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", "/api/my-work?view=favorites&project=all&status=not-selected", "u_front", projectID, "")
	if w.Code != 200 {
		t.Fatal("empty favorites failed")
	}
	if strings.Contains(w.Body.String(), insightProjectID) || !strings.Contains(w.Body.String(), `"requirementStatuses"`) {
		t.Fatal("favorite definitions absent or leak inaccessible project")
	}
	w = apiRequest(a, "GET", "/api/search?project=all&type="+url.QueryEscape("需求")+"&q="+url.QueryEscape("洞察报告"), "u_algo", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"statusName":"模型评估中"`) {
		t.Fatalf("search used current scope config: %s", w.Body.String())
	}
	x := planningRequirement(t, a, `{"title":"部分完成仍待办","sprint":"V1.0","assigneeUserIds":["u_front"]}`)
	x = patchPlanningRequirement(t, a, x.ID, `{"status":"前端已完成"}`)
	if x.StatusCategory != "doing" || x.IsEnd {
		t.Fatal("partial status considered completed")
	}
	patchPlanningRequirement(t, a, x.ID, `{"status":"已上线"}`)
	w = apiRequest(a, "GET", "/api/requirements?statusCategory=done", "u_admin", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), x.Title) {
		t.Fatal("done category filter omitted deployed requirement")
	}
	patchPlanningRequirement(t, a, x.ID, `{"status":"已拒绝"}`)
	w = apiRequest(a, "GET", "/api/my-work?category=completed", "u_front", projectID, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), x.Title) {
		t.Fatal("cancelled counted as completed")
	}
	w = apiRequest(a, "GET", "/api/sprints/1", "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	summary := jsonMap(t, w)["summary"].(map[string]any)
	if summary["cancelled"].(float64) < 1 {
		t.Fatal("sprint did not distinguish cancelled")
	}
}

func TestRequirementAndDefectStatusMultiSelectValidation(t *testing.T) {
	a := testApp(t)
	for _, test := range []struct {
		path   string
		states []string
	}{{"/api/requirements", []string{"开发中", "测试中"}}, {"/api/defects", []string{"新建", "已确认"}}} {
		w := apiRequest(a, "GET", test.path+"?statuses="+url.QueryEscape(jsonText(test.states)), "u_admin", projectID, "")
		if w.Code != 200 {
			t.Fatalf("multi-select failed: %d %s", w.Code, w.Body.String())
		}
		for _, raw := range []string{`null`, `[null]`, `[5]`, `["unknown"]`, `{"status":"开发中"}`} {
			w = apiRequest(a, "GET", test.path+"?statuses="+url.QueryEscape(raw), "u_admin", projectID, "")
			if w.Code != 422 {
				t.Fatalf("bad states accepted %s: %d", raw, w.Code)
			}
		}
		w = apiRequest(a, "GET", test.path+"?status="+url.QueryEscape(test.states[0])+"&statuses="+url.QueryEscape(jsonText(test.states)), "u_admin", projectID, "")
		if w.Code != 422 {
			t.Fatal("ambiguous single and multi accepted")
		}
	}
}

func TestRequirementStateAuditFailureRollsBackAndReadsFailClosed(t *testing.T) {
	a := testApp(t)
	s := statusByKey(t, a, projectID, "开发中")
	flow := getWorkflow(t, a, projectID)
	if _, err := a.db.Exec(`CREATE TRIGGER fail_state_audit BEFORE INSERT ON audit_logs WHEN NEW.object_type IN ('requirement_status','requirement_workflow') BEGIN SELECT RAISE(ABORT,'state audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirement-statuses/%d", s.ID), "u_admin", projectID, `{"name":"不得保存"}`)
	if w.Code != 503 {
		t.Fatalf("audit failure: %d %s", w.Code, w.Body.String())
	}
	if statusByKey(t, a, projectID, "开发中").Name != s.Name || getWorkflow(t, a, projectID).Version != flow.Version {
		t.Fatal("failed state config transaction committed")
	}
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(flow))
	if w.Code != 503 || getWorkflow(t, a, projectID).Version != flow.Version {
		t.Fatal("failed workflow audit committed")
	}
	if _, err := a.db.Exec(`DROP TABLE requirement_statuses`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/requirement-statuses", "/api/meta", "/api/requirements", "/api/my-work", "/api/my-work?view=favorites", "/api/search?q=test"} {
		w = apiRequest(a, "GET", path, "u_admin", projectID, "")
		if w.Code != 503 || strings.Contains(w.Body.String(), "no such table") {
			t.Fatalf("read did not fail closed %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestRequirementStateTenantAdminProjectViewerManagesOnlyConfiguration(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE memberships SET role='viewer' WHERE user_id='u_admin' AND project_id=?;UPDATE project_members SET role='viewer' WHERE user_id='u_admin' AND project_id=?`, projectID, projectID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/requirement-statuses", "u_admin", projectID, `{"name":"管理员项目配置"}`)
	if w.Code != 201 {
		t.Fatalf("tenant admin config blocked by project viewer: %d %s", w.Code, w.Body.String())
	}
	f := getWorkflow(t, a, projectID)
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 200 {
		t.Fatal("tenant admin cannot configure workflow")
	}
	w = apiRequest(a, "POST", "/api/requirement-statuses", "u_viewer", projectID, `{"name":"viewer bypass"}`)
	if w.Code != 403 {
		t.Fatal("viewer gained configuration permission")
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, `{"title":"业务权限不能扩大"}`)
	if w.Code != 403 {
		t.Fatal("configuration exception widened business-write authority")
	}
}

func TestRequirementWorkflowNotificationsAreAtomicAndNotRepeated(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"状态通知事务","assigneeUserIds":["u_front","u_back"]}`)
	if _, err := a.db.Exec(`CREATE TRIGGER fail_state_outbox BEFORE INSERT ON notification_outbox WHEN NEW.event_type='requirement.status_changed' BEGIN SELECT RAISE(ABORT,'outbox failed'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"status":"开发中","remarks":"不应保存"}`)
	if w.Code < 500 {
		t.Fatalf("notification failure should roll back: %d", w.Code)
	}
	stored, err := a.get(x.ID)
	if err != nil || stored.Status != "规划中" || stored.Remarks != "" {
		t.Fatal("failed outbox committed transition")
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE subject_id=? AND event_type='requirement.status_changed'`, x.ID).Scan(&count)
	if count != 0 {
		t.Fatal("failed transaction left partial notifications")
	}
	if _, err = a.db.Exec(`DROP TRIGGER fail_state_outbox`); err != nil {
		t.Fatal(err)
	}
	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)
	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中","remarks":"同状态不重复通知"}`)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE subject_id=? AND event_type='requirement.status_changed'`, x.ID).Scan(&count)
	if count != 2 {
		t.Fatalf("expected one notice per assignee: %d", count)
	}
	patchPlanningRequirement(t, a, x.ID, `{"status":"规划中"}`)
	patchPlanningRequirement(t, a, x.ID, `{"status":"开发中"}`)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE subject_id=? AND event_type='requirement.status_changed'`, x.ID).Scan(&count)
	if count != 6 {
		t.Fatalf("distinct later transitions were incorrectly deduplicated: %d", count)
	}
	_, body := localizedNotification("en-US", "requirement.status_changed", "工作项有新动态", "需求状态已变更为「开发中」")
	if body != "Requirement status changed to “开发中”" {
		t.Fatal("custom status name was translated")
	}
}

func TestRequirementWorkflowChangedInitialAndInvalidConfiguration(t *testing.T) {
	a := testApp(t)
	f := getWorkflow(t, a, projectID)
	f.InitialStatus = "待开发"
	w := apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	x := planningRequirement(t, a, `{"title":"配置后的初始状态"}`)
	if x.Status != "待开发" {
		t.Fatal("configured initial status ignored")
	}
	for _, body := range []string{`{"name":"","color":"#123456"}`, `{"name":"bad","color":"red"}`, `{"name":"bad","category":"unknown"}`, `{"name":"bad","sortOrder":-1}`} {
		w = apiRequest(a, "POST", "/api/requirement-statuses", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatalf("invalid config accepted: %s %d", body, w.Code)
		}
	}
	f = getWorkflow(t, a, projectID)
	f.EndStatuses = []string{}
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 422 {
		t.Fatal("workflow without end states accepted")
	}
	f = getWorkflow(t, a, projectID)
	f.Transitions = append(f.Transitions, f.Transitions[0])
	w = apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(f))
	if w.Code != 422 {
		t.Fatal("duplicate matrix edges accepted")
	}
}
