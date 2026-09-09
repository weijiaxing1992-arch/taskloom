package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func request(method, path, _ string, body string) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestCustomFieldValidationAndTenantIsolation(t *testing.T) {
	a := testApp(t)
	d := FieldDefinition{ObjectType: "requirement", Key: "risk", Name: "风险等级", Type: "single_select", Required: true, Enabled: true, Options: []string{"高", "中", "低"}}
	if err := validateDefinition(&d); err != nil {
		t.Fatal(err)
	}
	if err := validateFieldValue(d, "未知"); err == nil {
		t.Fatal("expected option validation error")
	}
	if err := validateFieldValue(d, nil); err == nil {
		t.Fatal("expected required validation error")
	}
	a.db.Exec(`INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,created_at,updated_at)VALUES('other','other','requirement','secret','越权字段','text','x','x')`)
	defs, err := a.definitions("requirement", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range defs {
		if x.Key == "secret" {
			t.Fatal("cross tenant field leaked")
		}
	}
}

func TestDisabledDefinitionKeepsHistoryButRejectsWrites(t *testing.T) {
	a := testApp(t)
	var id int64
	a.db.QueryRow(`SELECT id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key='business_value'`, tenantID, projectID).Scan(&id)
	before := a.customFields("requirement", 1)["business_value"]
	a.db.Exec(`UPDATE field_definitions SET enabled=0 WHERE id=?`, id)
	if err := a.saveCustomFields("requirement", 1, map[string]any{"business_value": "低"}, false); err == nil {
		t.Fatal("expected disabled field write rejection")
	}
	if got := a.customFields("requirement", 1)["business_value"]; got != before {
		t.Fatalf("history changed: %v -> %v", before, got)
	}
}

func TestLastAdminAndDisabledAccountProtection(t *testing.T) {
	a := testApp(t)
	admin := administrationSessionFixture(t, a, "u_admin")
	w := httptest.NewRecorder()
	admin.members(w, request("PATCH", "/api/members", "u_admin", `{"id":"u_admin","tenantRole":"member"}`))
	if w.Code != 409 {
		t.Fatalf("expected last admin 409, got %d: %s", w.Code, w.Body.String())
	}
	a.db.Exec(`UPDATE users SET active=0 WHERE id='u_member'`)
	called := false
	scoped := *a
	scoped.user = "u_member"
	h := scoped.authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("PATCH", "/api/requirements/1", "u_member", `{"title":"x"}`))
	if w.Code != 403 || called {
		t.Fatalf("disabled account was allowed: %d", w.Code)
	}
	a.db.Exec(`UPDATE users SET active=1 WHERE id='u_member'`)
	a.db.Exec(`UPDATE memberships SET role='viewer' WHERE user_id='u_member'`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("PATCH", "/api/requirements/1", "u_member", `{"title":"x"}`))
	if w.Code != 403 {
		t.Fatalf("viewer write expected 403, got %d", w.Code)
	}
}

func TestSprintAndDefectLifecycle(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.sprint(w, request("PATCH", "/api/sprints/2", "u_admin", `{"status":"已完成"}`))
	if w.Code != 422 {
		t.Fatalf("illegal sprint transition expected 422, got %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.sprint(w, request("PATCH", "/api/sprints/2", "u_admin", `{"status":"进行中"}`))
	if w.Code != 200 {
		t.Fatalf("legal sprint transition failed: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.defect(w, request("PATCH", "/api/defects/1", "u_admin", `{"status":"已关闭"}`))
	if w.Code != 422 {
		t.Fatalf("illegal defect transition expected 422, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.defect(w, request("PATCH", "/api/defects/1", "u_admin", `{"status":"已解决"}`))
	if w.Code != 200 {
		t.Fatalf("legal defect transition failed: %d %s", w.Code, w.Body.String())
	}
}

func TestFailedExecutionCreatesOneDefect(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.testExecution(w, request("PATCH", "/api/test-executions/1", "u_admin", `{"status":"失败","note":"按钮无响应"}`))
	if w.Code != 200 {
		t.Fatalf("execution failed: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.testExecution(w, request("POST", "/api/test-executions/1/create-defect", "u_admin", `{}`))
	if w.Code != 201 {
		t.Fatalf("create defect failed: %d %s", w.Code, w.Body.String())
	}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM defects WHERE tenant_id=? AND project_id=? AND source_execution_id=1`, tenantID, projectID).Scan(&n)
	if n != 1 {
		t.Fatalf("expected one defect, got %d", n)
	}
	w = httptest.NewRecorder()
	a.testExecution(w, request("POST", "/api/test-executions/1/create-defect", "u_admin", `{}`))
	if w.Code != 409 {
		t.Fatalf("duplicate expected 409, got %d", w.Code)
	}
}

func TestSprintDateValidationAndCompletionMigration(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.sprints(w, request("POST", "/api/sprints", "u_admin", `{"name":"倒置日期","startDate":"2026-10-02","endDate":"2026-10-01"}`))
	if w.Code != 422 {
		t.Fatalf("invalid date expected 422, got %d: %s", w.Code, w.Body.String())
	}
	var sprintID int64
	var sprintName string
	if err := a.db.QueryRow(`SELECT id,name FROM sprints WHERE tenant_id=? AND project_id=? AND status='进行中' LIMIT 1`, tenantID, projectID).Scan(&sprintID, &sprintName); err != nil {
		t.Fatal(err)
	}
	a.db.Exec(`UPDATE requirements SET sprint=?,status='开发中' WHERE id=1 AND tenant_id=? AND project_id=?`, sprintName, tenantID, projectID)
	a.db.Exec(`UPDATE defects SET sprint=?,status='修复中' WHERE id=1 AND tenant_id=? AND project_id=?`, sprintName, tenantID, projectID)
	w = httptest.NewRecorder()
	a.sprint(w, request("POST", "/api/sprints/"+fmt.Sprint(sprintID)+"/complete", "u_admin", `{"targetSprint":"待规划"}`))
	if w.Code != 200 {
		t.Fatalf("complete failed: %d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["migratedRequirements"].(float64) < 1 || payload["migratedDefects"].(float64) < 1 {
		t.Fatalf("unexpected migration: %v", payload)
	}
	var reqSprint, bugSprint, status string
	a.db.QueryRow(`SELECT sprint FROM requirements WHERE id=1`).Scan(&reqSprint)
	a.db.QueryRow(`SELECT sprint FROM defects WHERE id=1`).Scan(&bugSprint)
	a.db.QueryRow(`SELECT status FROM sprints WHERE id=?`, sprintID).Scan(&status)
	if reqSprint != "待规划" || bugSprint != "待规划" || status != "已完成" {
		t.Fatalf("transaction result wrong: %s %s %s", reqSprint, bugSprint, status)
	}
}

func TestYunfuRolesAndSprintPermissions(t *testing.T) {
	a := testApp(t)
	for _, role := range []string{"product", "frontend", "backend", "algorithm", "ui", "frontend_lead", "backend_lead", "qa", "project_admin", "viewer"} {
		if !validProjectRole(role) {
			t.Fatalf("role %s should be valid", role)
		}
	}
	if validProjectRole("superhero") {
		t.Fatal("unknown role should be rejected")
	}
	called := false
	qa := *a
	qa.user = "u_qa"
	h := qa.authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("POST", "/api/sprints", "u_qa", `{"name":"x"}`))
	if w.Code != 403 || called {
		t.Fatalf("qa managed sprint: %d", w.Code)
	}
	product := *a
	product.user = "u_pm"
	h = product.authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request("POST", "/api/sprints", "u_pm", `{"name":"x"}`))
	if !called {
		t.Fatalf("product role should reach sprint handler, status %d", w.Code)
	}
}

func TestSprintAggregatesRealHours(t *testing.T) {
	a := testApp(t)
	var sprintID int64
	var sprintName string
	a.db.QueryRow(`SELECT id,name FROM sprints WHERE status='进行中' LIMIT 1`).Scan(&sprintID, &sprintName)
	a.db.Exec(`UPDATE requirements SET sprint=?,estimated_hours=13,actual_hours=5,progress=40 WHERE id=1`, sprintName)
	w := httptest.NewRecorder()
	a.sprint(w, request("GET", "/api/sprints/"+fmt.Sprint(sprintID), "u_admin", ``))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"estimatedHours"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"progress":40`)) {
		t.Fatalf("real aggregation missing: %d %s", w.Code, w.Body.String())
	}
}
