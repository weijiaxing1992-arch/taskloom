package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func apiRequest(a *App, method, path, user, project, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-DevFlow-Project", project)
	cookieWriter := httptest.NewRecorder()
	cookie, err := a.issueSession(cookieWriter, r, user)
	if err != nil {
		panic(err)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}

func jsonMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatalf("invalid JSON: %s: %v", w.Body.String(), err)
	}
	return value
}

func TestProjectContextIsolationAndAccess(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/projects", "u_admin", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) < 2 {
		t.Fatalf("expected at least two accessible projects: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements", "u_algo", insightProjectID, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("洞察报告支持多模型对比")) || bytes.Contains(w.Body.Bytes(), []byte("统一身份认证")) {
		t.Fatalf("insight project scope leaked or missing: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements", "u_front", insightProjectID, "")
	if w.Code != 403 {
		t.Fatalf("non-member accessed insight project: %d %s", w.Code, w.Body.String())
	}
}

func TestProjectAdministrationPermissions(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodPost, "/api/projects", "u_pm", projectID, `{"name":"越权项目","code":"NOPE"}`)
	if w.Code != 403 {
		t.Fatalf("member created project: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/projects", "u_admin", projectID, `{"name":"交付平台","code":"DLP","description":"测试创建项目"}`)
	if w.Code != 201 || !bytes.Contains(w.Body.Bytes(), []byte("prj_dlp")) {
		t.Fatalf("admin project creation failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/projects/prj_dlp/archive", "u_admin", "prj_dlp", `{}`)
	if w.Code != 200 {
		t.Fatalf("admin archive failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/projects/prj_dlp/summary", "u_admin", "prj_dlp", "")
	if w.Code != 200 || jsonMap(t, w)["members"].(float64) != 1 {
		t.Fatalf("project summary failed: %d %s", w.Code, w.Body.String())
	}
	var status string
	a.db.QueryRow(`SELECT status FROM projects WHERE id='prj_dlp'`).Scan(&status)
	if status != "archived" {
		t.Fatalf("project status = %q", status)
	}
}

func TestMyWorkUsesStableUserIdentity(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/my-work?project=all", "u_algo", insightProjectID, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("洞察报告支持多模型对比")) || !bytes.Contains(w.Body.Bytes(), []byte(`"type":"迭代"`)) {
		t.Fatalf("algorithm work aggregation missing: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/my-work?project=all", "u_viewer", insightProjectID, "")
	if w.Code != 200 {
		t.Fatalf("viewer cannot read own work: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("洞察报告支持多模型对比")) {
		t.Fatalf("viewer received another user's assigned work: %s", w.Body.String())
	}
}

func TestGlobalSearchHonorsAccessAndTypeFilters(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/search?q=洞察", "u_front", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["total"].(float64) != 0 {
		t.Fatalf("inaccessible project leaked in search: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/search?q=洞察&type=需求&project="+insightProjectID, "u_algo", insightProjectID, "")
	payload := jsonMap(t, w)
	if w.Code != 200 || payload["total"].(float64) < 1 {
		t.Fatalf("accessible filtered search missing: %d %s", w.Code, w.Body.String())
	}
	for _, raw := range payload["items"].([]any) {
		if raw.(map[string]any)["type"] != "需求" {
			t.Fatalf("type filter ignored: %v", raw)
		}
	}
}

func TestPersonalNotificationsReadStateAndOutboxAuthorization(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/notifications?read=unread", "u_pm", projectID, "")
	payload := jsonMap(t, w)
	if w.Code != 200 || payload["unread"].(float64) < 1 {
		t.Fatalf("seed notification missing: %d %s", w.Code, w.Body.String())
	}
	id := int64(payload["items"].([]any)[0].(map[string]any)["id"].(float64))
	w = apiRequest(a, http.MethodPatch, "/api/notifications/"+formatID(id), "u_pm", projectID, `{"read":true}`)
	if w.Code != 200 {
		t.Fatalf("mark read failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/notifications/read-all", "u_pm", projectID, `{}`)
	if w.Code != 200 {
		t.Fatalf("read all failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/notifications/unread-count", "u_pm", projectID, "")
	if jsonMap(t, w)["unread"].(float64) != 0 {
		t.Fatalf("unread count did not clear: %s", w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/notifications/outbox", "u_viewer", projectID, "")
	if w.Code != 403 {
		t.Fatalf("viewer accessed outbox: %d %s", w.Code, w.Body.String())
	}
	result, _ := a.db.Exec(`INSERT INTO notification_outbox(tenant_id,project_id,event_type,payload,status,created_at,updated_at)VALUES(?,?,?,'{}','failed','x','x')`, tenantID, projectID, "test.failed")
	outboxID, _ := result.LastInsertId()
	w = apiRequest(a, http.MethodPost, "/api/notifications/outbox/"+formatID(outboxID)+"/retry", "u_admin", projectID, `{}`)
	if w.Code != 200 {
		t.Fatalf("admin outbox retry failed: %d %s", w.Code, w.Body.String())
	}
	var retryCount int
	var outboxStatus string
	a.db.QueryRow(`SELECT retry_count,status FROM notification_outbox WHERE id=?`, outboxID).Scan(&retryCount, &outboxStatus)
	if retryCount != 1 || outboxStatus != "mock_pending" {
		t.Fatalf("retry was not persisted: %d %s", retryCount, outboxStatus)
	}
}

func TestMentionDedupeCaseCopyAndExecutionHistory(t *testing.T) {
	a := testApp(t)
	for i := 0; i < 2; i++ {
		w := apiRequest(a, http.MethodPost, "/api/requirements/1/comments", "u_admin", projectID, `{"body":"@陈澄 请确认验收口径"}`)
		if w.Code != 201 {
			t.Fatalf("comment failed: %d %s", w.Code, w.Body.String())
		}
	}
	var mentions int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE recipient_user_id='u_pm' AND event_type='requirement.mentioned' AND subject_id=1`).Scan(&mentions)
	if mentions != 2 {
		t.Fatalf("each saved comment should notify once, expected 2, got %d", mentions)
	}
	w := apiRequest(a, http.MethodPost, "/api/test-cases/1/copy", "u_qa", projectID, `{}`)
	if w.Code != 201 || !bytes.Contains(w.Body.Bytes(), []byte("副本")) {
		t.Fatalf("case copy failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"失败","actualResult":"提交后页面无响应","note":"Chrome 复现"}`)
	if w.Code != 200 {
		t.Fatalf("execution update failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/test-executions/1/history", "u_qa", projectID, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("提交后页面无响应")) {
		t.Fatalf("execution history missing: %d %s", w.Code, w.Body.String())
	}
}

func TestDefectWorkflowTraceabilityAndTransactionalNotifications(t *testing.T) {
	a := testApp(t)
	// 测试夹具中的旧测试计划来自兼容数据，不依赖迁移何时补齐负责人。
	// 这里显式写入稳定 ID，验证通知只会交给实际归属人，绝不回退给演示账户。
	if _, err := a.db.Exec(`UPDATE test_plans SET owner_user_id='u_pm'
		WHERE tenant_id=? AND project_id=? AND id=(
			SELECT plan_id FROM test_executions WHERE tenant_id=? AND project_id=? AND id=1
		)`, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatalf("seed stable plan owner: %v", err)
	}
	if _, err := a.db.Exec(`UPDATE test_cases SET owner_user_id='u_front'
		WHERE tenant_id=? AND project_id=? AND id=(
			SELECT case_id FROM test_executions WHERE tenant_id=? AND project_id=? AND id=1
		)`, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatalf("seed stable case owner: %v", err)
	}
	var planOwnerID string
	if err := a.db.QueryRow(`SELECT p.owner_user_id FROM test_executions e JOIN test_plans p ON p.tenant_id=e.tenant_id AND p.project_id=e.project_id AND p.id=e.plan_id WHERE e.tenant_id=? AND e.project_id=? AND e.id=1`, tenantID, projectID).Scan(&planOwnerID); err != nil || planOwnerID == "" {
		t.Fatalf("missing stable test-plan owner: %q %v", planOwnerID, err)
	}
	w := apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"失败","actualResult":"支付按钮无响应","note":"Chrome 稳定复现"}`)
	if w.Code != 200 {
		t.Fatalf("failed execution update: %d %s", w.Code, w.Body.String())
	}
	// The stable dedupe key keeps repeated delivery attempts from creating duplicate outbox rows.
	w = apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"失败","actualResult":"支付按钮无响应","note":"Chrome 稳定复现"}`)
	if w.Code != 200 {
		t.Fatalf("repeated failed execution update: %d %s", w.Code, w.Body.String())
	}
	var failedOutbox int
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='test.failed'`, tenantID, projectID).Scan(&failedOutbox)
	if failedOutbox != 1 {
		t.Fatalf("test.failed outbox dedupe expected 1, got %d", failedOutbox)
	}
	var failedStation int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='test.failed' AND subject_id=1`, tenantID, projectID).Scan(&failedStation)
	if failedStation != 1 {
		t.Fatalf("test.failed station notification expected 1, got %d", failedStation)
	}
	if assignmentNoticeCount(t, a, "test.failed", "test_execution", 1, planOwnerID) != 1 || assignmentNoticeCount(t, a, "test.failed", "test_execution", 1, "u_qa") != 0 {
		t.Fatal("failed execution did not use the persisted plan owner")
	}

	w = apiRequest(a, http.MethodPost, "/api/test-executions/1/create-defect", "u_qa", projectID, `{}`)
	if w.Code != 201 {
		t.Fatalf("create linked defect: %d %s", w.Code, w.Body.String())
	}
	defectID := int64(jsonMap(t, w)["defectId"].(float64))
	w = apiRequest(a, http.MethodGet, "/api/defects/"+formatID(defectID), "u_qa", projectID, "")
	detail := jsonMap(t, w)
	if w.Code != 200 || int64(detail["sourceExecutionId"].(float64)) != 1 || detail["sourcePlanId"] == nil || detail["sourceCaseId"] == nil {
		t.Fatalf("defect traceability missing: %d %s", w.Code, w.Body.String())
	}
	allowed := detail["allowedTransitions"].([]any)
	if len(allowed) != 2 || allowed[0] != "已确认" || allowed[1] != "已拒绝" {
		t.Fatalf("unexpected transitions for new defect: %v", allowed)
	}
	w = apiRequest(a, http.MethodPatch, "/api/defects/"+formatID(defectID), "u_qa", projectID, `{"status":"已解决"}`)
	if w.Code != 422 || !bytes.Contains(w.Body.Bytes(), []byte("不能流转")) {
		t.Fatalf("invalid transition should be friendly 422: %d %s", w.Code, w.Body.String())
	}

	for _, next := range []string{"已确认", "修复中", "已解决", "待验证", "已关闭", "重新打开"} {
		w = apiRequest(a, http.MethodPatch, "/api/defects/"+formatID(defectID), "u_qa", projectID, `{"status":"`+next+`"}`)
		if w.Code != 200 {
			t.Fatalf("transition to %s failed: %d %s", next, w.Code, w.Body.String())
		}
		payload := jsonMap(t, w)
		if payload["status"] != next || payload["allowedTransitions"] == nil {
			t.Fatalf("transition response missing state contract: %v", payload)
		}
	}
	var createdOutbox, statusOutbox, statusStation int
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='defect.created_from_execution'`, tenantID, projectID).Scan(&createdOutbox)
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='defect.status_changed' AND payload LIKE ?`, tenantID, projectID, "%BUG-%").Scan(&statusOutbox)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type='defect.status_changed' AND subject_id=?`, tenantID, projectID, defectID).Scan(&statusStation)
	if createdOutbox != 1 || statusOutbox != 6 || statusStation != 0 {
		t.Fatalf("workflow notifications incomplete: created=%d statusOutbox=%d statusStation=%d", createdOutbox, statusOutbox, statusStation)
	}
	if assignmentNoticeCount(t, a, "defect.created_from_execution", "defect", defectID, planOwnerID) != 1 || assignmentNoticeCount(t, a, "defect.created_from_execution", "defect", defectID, "u_qa") != 0 {
		t.Fatal("created defect did not use the persisted plan owner")
	}
	// 同状态重试不会重复通知；重新打开后再次进入同一个状态则是新的真实流转，
	// 应当拥有新的活动 ID 和新的 outbox 事件。
	w = apiRequest(a, http.MethodPatch, "/api/defects/"+formatID(defectID), "u_qa", projectID, `{"status":"已确认"}`)
	if w.Code != 200 {
		t.Fatalf("reconfirm defect: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/defects/"+formatID(defectID), "u_qa", projectID, `{"status":"已确认"}`)
	if w.Code != 200 {
		t.Fatalf("repeat defect status retry: %d %s", w.Code, w.Body.String())
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='defect.status_changed' AND payload LIKE ?`, tenantID, projectID, "%BUG-%").Scan(&statusOutbox)
	if statusOutbox != 7 {
		t.Fatalf("status event idempotency/reentry count=%d", statusOutbox)
	}
	w = apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"通过","actualResult":"修复验证通过","note":""}`)
	if w.Code != 200 {
		t.Fatalf("recover execution: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"失败","actualResult":"二次失败","note":"回归再次失败"}`)
	if w.Code != 200 {
		t.Fatalf("fail execution again: %d %s", w.Code, w.Body.String())
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='test.failed'`, tenantID, projectID).Scan(&failedOutbox)
	if failedOutbox != 2 || assignmentNoticeCount(t, a, "test.failed", "test_execution", 1, planOwnerID) != 2 {
		t.Fatalf("second real execution failure was suppressed: outbox=%d", failedOutbox)
	}
	w = apiRequest(a, http.MethodGet, "/api/test-executions/1/history", "u_qa", projectID, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"defectId":`+formatID(defectID))) {
		t.Fatalf("execution history missing defect link: %d %s", w.Code, w.Body.String())
	}
}

func TestCORSAllowsProjectContextHeader(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest(http.MethodOptions, "/api/requirements", nil)
	w := httptest.NewRecorder()
	withJSON(a.scopedAPI()).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-DevFlow-Project") || strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-DevFlow-User") {
		t.Fatalf("project context header missing from CORS preflight: %d %q", w.Code, w.Header().Get("Access-Control-Allow-Headers"))
	}
}

func formatID(id int64) string {
	const digits = "0123456789"
	if id == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = digits[id%10]
		id /= 10
	}
	return string(buf[i:])
}
