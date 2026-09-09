package main

import (
	"net/http"
	"strings"
	"testing"
)

func insertAuditHistoryRecord(t *testing.T, a *App, tenant, project, actor, objectType, objectID, action, before, after, createdAt string) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenant, project, actor, objectType, objectID, action, before, after, createdAt)
	if err != nil {
		t.Fatalf("insert audit record: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("audit record ID: %v", err)
	}
	return id
}

func auditItems(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	raw, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("audit items missing: %#v", payload)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("invalid audit item: %#v", value)
		}
		items = append(items, item)
	}
	return items
}

func TestAuditHistoryScopesFiltersRedactsAndKeepsAppendOnly(t *testing.T) {
	a := testApp(t)
	var beforeCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	insertAuditHistoryRecord(t, a, tenantID, projectID, "u_admin", "requirement", "41", "audit.history.redaction", `{"title":"旧标题","password":"before-private","密码":"中文密码","访问令牌":"中文令牌","meta":{"webhookUrl":"https://hooks.example/before"}}`, `{"title":"新标题"}`, "2026-04-01T12:00:00Z")
	// 同名动作在其他项目/租户中也不能被项目审计接口读取。
	insertAuditHistoryRecord(t, a, tenantID, insightProjectID, "u_admin", "requirement", "99", "audit.history.redaction", `{"title":"错误项目"}`, `{"title":"不应出现"}`, "2026-04-01T13:00:00Z")
	insertAuditHistoryRecord(t, a, "other_tenant", projectID, "u_admin", "requirement", "98", "audit.history.redaction", `{"title":"错误租户"}`, `{"title":"不应出现"}`, "2026-04-01T14:00:00Z")

	w := apiRequest(a, http.MethodGet, "/api/audit-logs?objectType=requirement&action=audit.history.redaction&actorId=u_admin&from=2026-04-01&to=2026-04-01", "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("audit history query failed: %d %s", w.Code, w.Body.String())
	}
	payload := jsonMap(t, w)
	items := auditItems(t, payload)
	if len(items) != 1 || items[0]["objectId"] != "41" {
		t.Fatalf("scope or filters leaked records: %#v", items)
	}
	// 接口返回的是脱敏后的差异摘要，绝不返回原始审计 JSON。
	if strings.Contains(w.Body.String(), "before-private") || strings.Contains(w.Body.String(), "中文密码") || strings.Contains(w.Body.String(), "中文令牌") || strings.Contains(w.Body.String(), "hooks.example") || strings.Contains(w.Body.String(), "webhookUrl") {
		t.Fatalf("sensitive audit values leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "新标题") || !strings.Contains(w.Body.String(), "已脱敏") {
		t.Fatalf("readable redacted summary missing: %s", w.Body.String())
	}
	var afterCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount != beforeCount+3 {
		t.Fatalf("read-only audit endpoint modified append-only logs: before=%d after=%d", beforeCount, afterCount)
	}
}

func TestAuditHistoryRedactsChineseSensitiveKeys(t *testing.T) {
	changes, _ := auditHistoryChanges(`{"密码":"pw-plain","访问令牌":"token-plain","数据库凭证":"credential-plain","API密钥":"key-plain","Webhook地址":"https://hooks.example/plain","回调地址":"https://callback.example/plain","公开字段":"旧值"}`, `{"公开字段":"新值"}`)
	var summary strings.Builder
	for _, change := range changes {
		summary.WriteString(change.Field)
		summary.WriteString(change.Before)
		summary.WriteString(change.After)
	}
	text := summary.String()
	for _, secret := range []string{"pw-plain", "token-plain", "credential-plain", "key-plain", "hooks.example", "callback.example"} {
		if strings.Contains(text, secret) {
			t.Fatalf("Chinese sensitive key leaked %q in %#v", secret, changes)
		}
	}
	if !strings.Contains(text, "敏感字段") || !strings.Contains(text, "已脱敏") {
		t.Fatalf("redacted sensitive summary is not readable: %#v", changes)
	}
	// 旧版本若把内容整体写成 JSON 字符串，也不能绕开按字段脱敏的保护。
	changes, _ = auditHistoryChanges(`"password=scalar-private"`, `"token=scalar-private"`)
	text = ""
	for _, change := range changes {
		text += change.Field + change.Before + change.After
	}
	if strings.Contains(text, "scalar-private") || !strings.Contains(text, "已隐藏非结构化审计数据") {
		t.Fatalf("scalar audit payload should be hidden: %#v", changes)
	}
}

func TestAuditHistoryCursorAndAuthorization(t *testing.T) {
	a := testApp(t)
	firstID := insertAuditHistoryRecord(t, a, tenantID, projectID, "u_admin", "requirement", "51", "audit.history.page", `{}`, `{"step":1}`, "2026-04-02T12:00:00Z")
	secondID := insertAuditHistoryRecord(t, a, tenantID, projectID, "u_admin", "requirement", "52", "audit.history.page", `{"step":1}`, `{"step":2}`, "2026-04-02T12:00:00Z")
	thirdID := insertAuditHistoryRecord(t, a, tenantID, projectID, "u_admin", "requirement", "53", "audit.history.page", `{"step":2}`, `{"step":3}`, "2026-04-02T12:00:00Z")

	w := apiRequest(a, http.MethodGet, "/api/audit-logs?action=audit.history.page&limit=2", "u_pm", projectID, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-project-admin read audit history: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/audit-logs?action=audit.history.page&limit=2", "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("first audit page failed: %d %s", w.Code, w.Body.String())
	}
	first := jsonMap(t, w)
	firstItems := auditItems(t, first)
	if len(firstItems) != 2 || int64(firstItems[0]["id"].(float64)) != thirdID || int64(firstItems[1]["id"].(float64)) != secondID {
		t.Fatalf("same-time deterministic ordering failed: %#v", firstItems)
	}
	cursor, _ := first["nextCursor"].(string)
	if cursor == "" {
		t.Fatalf("first page unexpectedly lacks cursor: %#v", first)
	}
	w = apiRequest(a, http.MethodGet, "/api/audit-logs?action=audit.history.page&limit=2&cursor="+cursor, "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("next audit page failed: %d %s", w.Code, w.Body.String())
	}
	next := jsonMap(t, w)
	nextItems := auditItems(t, next)
	if len(nextItems) != 1 || int64(nextItems[0]["id"].(float64)) != firstID || next["nextCursor"] != "" {
		t.Fatalf("cursor page duplicated/skipped records: %#v", next)
	}
	w = apiRequest(a, http.MethodGet, "/api/audit-logs?cursor=not-a-valid-cursor", "u_admin", projectID, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor should be rejected: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/audit-logs", "u_admin", projectID, `{}`)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("audit history accepted write method: %d %s", w.Code, w.Body.String())
	}
}
