package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func createTraceabilityCase(t *testing.T, a *App, requirementID int64, title, status string) TestCase {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"steps":"执行验证","expected":"验证通过","requirementId":%d,"status":%q}`, title, requirementID, status)
	w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create test case: %d %s", w.Code, w.Body.String())
	}
	var created TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestRequirementTestCasesAreScopedSummaryOnlyAndPageDeterministically(t *testing.T) {
	a := testApp(t)
	requirement := createPeopleRequirement(t, a, map[string]any{"title": "可追溯需求"})
	first := createTraceabilityCase(t, a, requirement.ID, "草稿用例", "草稿")
	second := createTraceabilityCase(t, a, requirement.ID, "评审用例", "待评审")
	third := createTraceabilityCase(t, a, requirement.ID, "通过用例", "已通过")
	if first.Requirement == nil || first.Requirement.ID != requirement.ID {
		t.Fatalf("create response missed safe requirement summary: %+v", first)
	}

	// 注入旧版本可能留下的错误外键：它属于当前测试用例项目，却指向另一项目的需求。
	// 所有读取接口必须隐藏该 ID 和另一项目的需求摘要。
	var foreignRequirementID int64
	var foreignRequirementTitle string
	if err := a.db.QueryRow(`SELECT id,title FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, insightProjectID).Scan(&foreignRequirementID, &foreignRequirementTitle); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO test_cases(tenant_id,project_id,code,category,title,preconditions,steps,expected,priority,status,owner,requirement_id,enabled,steps_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, projectID, "TC-LEGACY", "未分类", "历史错误关联", "", "执行", "成功", "P2", "草稿", "", foreignRequirementID, 1, "[]", "2026-09-04T00:00:00Z", "2026-09-04T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	// 用例详情和列表均返回本项目的需求摘要，避免前端再以不受控 ID 反查需求。
	w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/test-cases/%d", first.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("case detail: %d %s", w.Code, w.Body.String())
	}
	detail := jsonMap(t, w)
	linked, ok := detail["requirement"].(map[string]any)
	if !ok || linked["id"] != float64(requirement.ID) || linked["title"] != requirement.Title || linked["status"] != requirement.Status {
		t.Fatalf("unsafe or incomplete requirement summary: %s", w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/test-cases?q=历史错误关联", "u_admin", projectID, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), foreignRequirementTitle) || strings.Contains(w.Body.String(), fmt.Sprintf(`"requirementId":%d`, foreignRequirementID)) {
		t.Fatalf("legacy foreign link leaked: %d %s", w.Code, w.Body.String())
	}
	legacy := jsonMap(t, w)["items"].([]any)[0].(map[string]any)
	if _, exposed := legacy["requirement"]; exposed {
		t.Fatalf("legacy foreign requirement summary exposed: %#v", legacy)
	}

	path := fmt.Sprintf("/api/requirements/%d/test-cases?limit=2", requirement.ID)
	w = apiRequest(a, http.MethodGet, path, "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("requirement traceability: %d %s", w.Code, w.Body.String())
	}
	page := jsonMap(t, w)
	items := page["items"].([]any)
	if len(items) != 2 || page["hasMore"] != true || page["nextCursor"] == "" {
		t.Fatalf("first traceability page invalid: %s", w.Body.String())
	}
	stats := page["stats"].(map[string]any)
	if stats["total"] != float64(3) || stats["draft"] != float64(1) || stats["pendingReview"] != float64(1) || stats["approved"] != float64(1) || stats["deprecated"] != float64(0) {
		t.Fatalf("incorrect traceability stats: %#v", stats)
	}
	seen := map[float64]bool{}
	for _, raw := range items {
		item := raw.(map[string]any)
		seen[item["id"].(float64)] = true
		if _, unsafe := item["steps"]; unsafe {
			t.Fatalf("summary exposed steps: %#v", item)
		}
		if _, unsafe := item["expected"]; unsafe {
			t.Fatalf("summary exposed expected result: %#v", item)
		}
		if _, unsafe := item["customFields"]; unsafe {
			t.Fatalf("summary exposed custom fields: %#v", item)
		}
		ref, ok := item["requirement"].(map[string]any)
		if !ok || ref["id"] != float64(requirement.ID) {
			t.Fatalf("summary lost requirement reference: %#v", item)
		}
	}
	w = apiRequest(a, http.MethodGet, path+"&cursor="+page["nextCursor"].(string), "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("next traceability page: %d %s", w.Code, w.Body.String())
	}
	next := jsonMap(t, w)
	if len(next["items"].([]any)) != 1 || next["hasMore"] != false {
		t.Fatalf("second traceability page invalid: %s", w.Body.String())
	}
	lastID := next["items"].([]any)[0].(map[string]any)["id"].(float64)
	if seen[lastID] || (lastID != float64(first.ID) && lastID != float64(second.ID) && lastID != float64(third.ID)) {
		t.Fatalf("cursor duplicated or returned an unrelated case: %s", w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/test-cases", foreignRequirementID), "u_admin", projectID, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign requirement traceability leaked: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/test-cases?cursor=not-valid", requirement.ID), "u_admin", projectID, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid traceability cursor accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestTestCaseRequirementWritesValidateAndAuditAtomically(t *testing.T) {
	a := testApp(t)
	requirement := createPeopleRequirement(t, a, map[string]any{"title": "关联写入需求"})
	var foreignRequirementID int64
	if err := a.db.QueryRow(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, insightProjectID).Scan(&foreignRequirementID); err != nil {
		t.Fatal(err)
	}

	before := tableCount(t, a, "test_cases")
	w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, fmt.Sprintf(`{"title":"跨项目关联","steps":"执行","expected":"成功","requirementId":%d}`, foreignRequirementID))
	if w.Code != http.StatusUnprocessableEntity || tableCount(t, a, "test_cases") != before {
		t.Fatalf("foreign requirement persisted: %d %s", w.Code, w.Body.String())
	}
	created := createTraceabilityCase(t, a, requirement.ID, "可审计用例", "草稿")
	var createdActivity, createdAudit int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM entity_activities WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=? AND event='created'`, tenantID, projectID, created.ID).Scan(&createdActivity); err != nil {
		t.Fatal(err)
	}
	var createdAfter string
	if err := a.db.QueryRow(`SELECT after_json FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=? AND action='test_case.created'`, tenantID, projectID, fmt.Sprint(created.ID)).Scan(&createdAfter); err != nil {
		t.Fatal(err)
	}
	createdAudit = 1
	if createdActivity != 1 || createdAudit != 1 || !strings.Contains(createdAfter, fmt.Sprintf(`"requirementId":%d`, requirement.ID)) || strings.Contains(createdAfter, `"steps"`) {
		t.Fatalf("create trace missing or overscoped: activity=%d audit=%d %s", createdActivity, createdAudit, createdAfter)
	}

	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", created.ID), "u_admin", projectID, fmt.Sprintf(`{"requirementId":%d}`, foreignRequirementID))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign patch association accepted: %d %s", w.Code, w.Body.String())
	}
	afterRejected, err := a.getTestCase(created.ID)
	if err != nil || afterRejected.RequirementID == nil || *afterRejected.RequirementID != requirement.ID {
		t.Fatalf("rejected patch changed relation: %+v %v", afterRejected, err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", created.ID), "u_admin", projectID, `{"title":"已更新的可审计用例"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update test case: %d %s", w.Code, w.Body.String())
	}
	var updateActivity, updateAudit int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM entity_activities WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=? AND event='updated'`, tenantID, projectID, created.ID).Scan(&updateActivity); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=? AND action='test_case.updated'`, tenantID, projectID, fmt.Sprint(created.ID)).Scan(&updateAudit); err != nil {
		t.Fatal(err)
	}
	if updateActivity != 1 || updateAudit != 1 {
		t.Fatalf("update trace missing: activity=%d audit=%d", updateActivity, updateAudit)
	}

	counts := map[string]int{}
	for _, table := range []string{"test_cases", "entity_activities", "audit_logs"} {
		counts[table] = tableCount(t, a, table)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER reject_test_case_audit BEFORE INSERT ON audit_logs WHEN NEW.object_type='test_case' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, fmt.Sprintf(`{"title":"不应落库","steps":"执行","expected":"成功","requirementId":%d}`, requirement.ID))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure was not surfaced: %d %s", w.Code, w.Body.String())
	}
	for table, expected := range counts {
		if got := tableCount(t, a, table); got != expected {
			t.Fatalf("%s changed despite audit rollback: %d -> %d", table, expected, got)
		}
	}
}

func TestAIImportedTestCasesHavePerCaseTraceability(t *testing.T) {
	a := aiApp(t)
	requirement := createPeopleRequirement(t, a, map[string]any{"title": "AI 关联需求"})
	aiMock(a, func(*http.Request) (*http.Response, error) { return aiResponse(aiExampleJSON()), nil })
	draft := aiGenerate(t, a, requirement)
	path := fmt.Sprintf("/api/requirements/%d/ai-test-cases/import", requirement.ID)
	payload := jsonText(map[string]any{"draftId": draft["draftId"], "indexes": []int{0, 1}})
	w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, payload)
	if w.Code != http.StatusOK || jsonMap(t, w)["importedCount"] != float64(2) {
		t.Fatalf("AI import: %d %s", w.Code, w.Body.String())
	}
	items := jsonMap(t, w)["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		id := int64(item["id"].(float64))
		caseDetail, err := a.getTestCase(id)
		if err != nil || caseDetail.Requirement == nil || caseDetail.Requirement.ID != requirement.ID {
			t.Fatalf("imported case lost requirement summary: %+v %v", caseDetail, err)
		}
		var event, action, after string
		if err := a.db.QueryRow(`SELECT event FROM entity_activities WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=?`, tenantID, projectID, id).Scan(&event); err != nil {
			t.Fatal(err)
		}
		if err := a.db.QueryRow(`SELECT action,after_json FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=?`, tenantID, projectID, fmt.Sprint(id)).Scan(&action, &after); err != nil {
			t.Fatal(err)
		}
		if event != "ai_imported" || action != "test_case.ai_imported" || !strings.Contains(after, fmt.Sprintf(`"requirementId":%d`, requirement.ID)) || strings.Contains(after, `"steps"`) {
			t.Fatalf("AI trace missing/overscoped: event=%s action=%s after=%s", event, action, after)
		}
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/test-cases", requirement.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK || jsonMap(t, w)["stats"].(map[string]any)["total"] != float64(2) {
		t.Fatalf("imported cases absent from requirement trace: %d %s", w.Code, w.Body.String())
	}

	beforeCases := tableCount(t, a, "test_cases")
	beforeAudits := tableCount(t, a, "audit_logs")
	w = apiRequest(a, http.MethodPost, path, "u_admin", projectID, payload)
	if w.Code != http.StatusOK || jsonMap(t, w)["replayed"] != true || tableCount(t, a, "test_cases") != beforeCases || tableCount(t, a, "audit_logs") != beforeAudits {
		t.Fatalf("replayed import rewrote traceability: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, path, "u_admin", insightProjectID, payload)
	if w.Code != http.StatusNotFound || tableCount(t, a, "test_cases") != beforeCases {
		t.Fatalf("cross-project AI import leaked: %d %s", w.Code, w.Body.String())
	}
}
