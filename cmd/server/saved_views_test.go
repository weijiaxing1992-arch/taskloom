package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func savedViewFixture() map[string]any {
	return map[string]any{"schema": 1, "q": "开发", "statuses": []string{"规划中"}, "statusCategory": "", "assigneeMode": "me", "assigneeUserId": "", "sprint": "", "category": "", "priority": "", "filters": []any{map[string]any{"field": "role.frontend.value", "operator": "gte", "value": 20}}, "columns": []string{"code", "title", "role.frontend.value"}, "sort": "updatedAt", "order": "desc", "view": "board"}
}
func savedViewRequest(a *App, method, path, user, project string, body any) *httptest.ResponseRecorder {
	return apiRequest(a, method, path, user, project, jsonText(body))
}
func createSavedView(t *testing.T, a *App, user, scope, name string) savedView {
	t.Helper()
	w := savedViewRequest(a, "POST", "/api/requirement-views", user, projectID, map[string]any{"name": name, "scope": scope, "config": savedViewFixture()})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var v savedView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestSavedViewsPersonalSharedAndProjectIsolation(t *testing.T) {
	a := testApp(t)
	private := createSavedView(t, a, "u_front", "personal", "My work")
	shared := createSavedView(t, a, "u_admin", "shared", "Team work")
	for _, user := range []string{"u_front", "u_admin", "u_pm"} {
		w := apiRequest(a, "GET", "/api/requirement-views", user, projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		result := jsonMap(t, w)
		items := result["items"].([]any)
		want := 1
		if user == "u_front" {
			want = 2
		}
		if len(items) != want {
			t.Fatal("personal view leaked", user, w.Body)
		}
		for _, entry := range items {
			v := entry.(map[string]any)
			if v["scope"] == "shared" && v["canManage"] != (user == "u_admin") {
				t.Fatal("wrong shared capability", user, v)
			}
		}
	}
	for _, v := range []savedView{private, shared} {
		w := savedViewRequest(a, "PATCH", fmt.Sprint("/api/requirement-views/", v.ID), "u_admin", insightProjectID, map[string]any{"name": "wrong project", "version": v.Version, "config": savedViewFixture()})
		if w.Code != 404 {
			t.Fatal("cross-project mutation", w.Code, w.Body)
		}
	}
	w := savedViewRequest(a, "DELETE", fmt.Sprint("/api/requirement-views/", private.ID), "u_admin", projectID, map[string]any{"version": private.Version})
	if w.Code != 404 {
		t.Fatal("admin must not manage another personal view", w.Code, w.Body)
	}
	w = apiRequest(a, "GET", "/api/requirement-views", "u_admin", insightProjectID, "")
	if len(jsonMap(t, w)["items"].([]any)) != 0 {
		t.Fatal("cross-project listing", w.Body)
	}
}
func TestSavedViewsPermissionsRecheckedAndBusinessDisabled(t *testing.T) {
	a := testApp(t)
	shared := createSavedView(t, a, "u_admin", "shared", "Team")
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := "/api/requirement-views"
		body := map[string]any{"name": "no", "scope": "shared", "config": savedViewFixture()}
		if method != "POST" {
			path = fmt.Sprint(path, "/", shared.ID)
			body = map[string]any{"version": 1}
			if method == "PATCH" {
				body["name"] = "no"
				body["config"] = savedViewFixture()
			}
		}
		w := savedViewRequest(a, method, path, "u_front", projectID, body)
		if w.Code != 403 {
			t.Fatal(method, w.Code, w.Body)
		}
	}
	if _, err := a.db.Exec(`UPDATE project_members SET role='viewer' WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", "/api/requirement-views", "u_front", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["canCreate"] != false {
		t.Fatal("viewer read capability", w.Code, w.Body)
	}
	w = savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "viewer", "scope": "personal", "config": savedViewFixture()})
	if w.Code != 403 {
		t.Fatal("viewer write", w.Code, w.Body)
	}
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_pm'`, tenantID); err != nil {
		t.Fatal(err)
	}
	w = savedViewRequest(a, "POST", "/api/requirement-views", "u_pm", projectID, map[string]any{"name": "disabled", "scope": "personal", "config": savedViewFixture()})
	if w.Code != 403 {
		t.Fatal("disabled write", w.Code, w.Body)
	}
	// A scoped handler can outlive a permission snapshot; its transaction must re-read membership.
	if _, err := a.db.Exec(`UPDATE tenant_memberships SET role='member' WHERE tenant_id=? AND user_id='u_admin';UPDATE project_members SET role='product' WHERE tenant_id=? AND project_id=? AND user_id='u_admin'`, tenantID, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w = savedViewRequest(a, "PATCH", fmt.Sprint("/api/requirement-views/", shared.ID), "u_admin", projectID, map[string]any{"name": "revoked", "version": 1, "config": savedViewFixture()})
	if w.Code != 403 {
		t.Fatal("revoked manager write", w.Code, w.Body)
	}
}
func TestSavedViewsVersionConflictNameUniquenessAndDeletion(t *testing.T) {
	a := testApp(t)
	v := createSavedView(t, a, "u_front", "personal", "Alpha")
	w := savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": " alpha ", "scope": "personal", "config": savedViewFixture()})
	if w.Code != 409 {
		t.Fatal("duplicate name", w.Code, w.Body)
	}
	createSavedView(t, a, "u_pm", "personal", "Alpha")
	createSavedView(t, a, "u_admin", "shared", "Alpha")
	path := fmt.Sprint("/api/requirement-views/", v.ID)
	w = savedViewRequest(a, "PATCH", path, "u_front", projectID, map[string]any{"name": "Beta", "version": 1, "config": savedViewFixture()})
	if w.Code != 200 || jsonMap(t, w)["version"] != float64(2) {
		t.Fatal(w.Code, w.Body)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		body := map[string]any{"version": 1}
		if method == "PATCH" {
			body["name"] = "stale"
			body["config"] = savedViewFixture()
		}
		w = savedViewRequest(a, method, path, "u_front", projectID, body)
		if w.Code != 409 {
			t.Fatal("stale overwrite", method, w.Code, w.Body)
		}
	}
	var name string
	var version int
	if err := a.db.QueryRow(`SELECT name,version FROM requirement_saved_views WHERE id=?`, v.ID).Scan(&name, &version); err != nil || name != "Beta" || version != 2 {
		t.Fatal("conflict changed persisted snapshot", name, version, err)
	}
	w = savedViewRequest(a, "DELETE", path, "u_front", projectID, map[string]any{"version": 2})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = savedViewRequest(a, "DELETE", path, "u_front", projectID, map[string]any{"version": 2})
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestSavedViewsStrictValidationLimitsAndRollback(t *testing.T) {
	a := testApp(t)
	invalid := []map[string]any{{"schema": 2}, {"sort": "id); DROP TABLE users;--"}, {"columns": []string{"title", "code"}}, {"columns": []string{"code", "title", "title"}}, {"sprintId": -1}, {"categoryId": 9007199254740992}, {"assigneeMode": "member", "assigneeUserId": ""}, {"assigneeMode": "any", "assigneeUserId": "u_front"}, {"q": strings.Repeat("x", 2001)}, {"filters": []any{map[string]any{"field": "title", "operator": "eq", "value": map[string]any{"nested": true}}}}, {"filters": []any{map[string]any{"field": "title", "operator": "is_empty", "value": nil}}}, {"statuses": []string{"规划中", "规划中"}}}
	for _, change := range invalid {
		config := savedViewFixture()
		for key, value := range change {
			config[key] = value
		}
		w := savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "bad", "scope": "personal", "config": config})
		if w.Code != 422 {
			t.Fatal("accepted invalid config", change, w.Code, w.Body)
		}
	}
	for _, body := range []string{`null`, `{}`, `{"name":"a","scope":"personal","config":{},"unexpected":true}`, strings.Repeat(" ", 32769) + `{}`} {
		w := apiRequest(a, "POST", "/api/requirement-views", "u_front", projectID, body)
		if w.Code != 422 {
			t.Fatal("invalid request", w.Code, w.Body)
		}
	}
	for _, name := range []string{"", strings.Repeat("中", 31), "bad\nname"} {
		w := savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": name, "scope": "personal", "config": savedViewFixture()})
		if w.Code != 422 {
			t.Fatal(name, w.Code, w.Body)
		}
	}
	v := createSavedView(t, a, "u_front", "personal", "Valid")
	if _, err := a.db.Exec(`CREATE TRIGGER fail_view_write BEFORE UPDATE ON requirement_saved_views BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
		t.Fatal(err)
	}
	w := savedViewRequest(a, "PATCH", fmt.Sprint("/api/requirement-views/", v.ID), "u_front", projectID, map[string]any{"name": "Lost?", "version": 1, "config": savedViewFixture()})
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
	var actual string
	var version int
	if err := a.db.QueryRow(`SELECT name,version FROM requirement_saved_views WHERE id=?`, v.ID).Scan(&actual, &version); err != nil || actual != "Valid" || version != 1 {
		t.Fatal("transaction failed to roll back", actual, version, err)
	}
	for i := 1; i < 50; i++ {
		if _, err := a.db.Exec(`INSERT INTO requirement_saved_views(tenant_id,project_id,scope,owner_user_id,owner_key,name,name_key,config_json,updated_at) VALUES(?,?,'personal','u_front','u_front',?,?,?,'2026-09-04')`, tenantID, projectID, fmt.Sprint(i), fmt.Sprint(i), jsonText(savedViewFixture())); err != nil {
			t.Fatal(err)
		}
	}
	w = savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "51", "scope": "personal", "config": savedViewFixture()})
	if w.Code != 422 {
		t.Fatal("missing view limit", w.Code, w.Body)
	}
}

func TestSavedViewsStableAdvancedReferencesRoundTripAndValidation(t *testing.T) {
	a := testApp(t)
	config := savedViewFixture()
	config["filters"] = []any{map[string]any{"field": "category", "operator": "neq", "value": "客户端", "referenceId": 8}, map[string]any{"field": "sprint", "operator": "eq", "value": "22", "referenceId": 9}}
	w := savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "Stable references", "scope": "personal", "config": config})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	w = apiRequest(a, "GET", "/api/requirement-views", "u_front", projectID, "")
	var result struct {
		Items []savedView `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 {
		t.Fatal(w.Code, w.Body)
	}
	for index, want := range []int64{8, 9} {
		actual := result.Items[0].Config.Filters[index].ReferenceID
		if actual == nil || *actual != want {
			t.Fatal("reference identity lost", w.Body)
		}
	}
	for _, patch := range []map[string]any{{"referenceId": 0}, {"referenceId": -1}, {"referenceId": 0.5}, {"referenceId": 9007199254740992}, {"field": "title", "referenceId": 8}, {"operator": "contains", "referenceId": 8}, {"value": false, "referenceId": 8}} {
		rule := map[string]any{"field": "category", "operator": "neq", "value": "客户端"}
		for key, value := range patch {
			rule[key] = value
		}
		config["filters"] = []any{rule}
		w = savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "invalid", "scope": "personal", "config": config})
		if w.Code != 422 {
			t.Fatal("invalid reference accepted", patch, w.Code, w.Body)
		}
	}
	// Old snapshots remain readable, but the UI must resolve these exact names before applying them.
	config["filters"] = []any{map[string]any{"field": "category", "operator": "neq", "value": "旧模板"}}
	w = savedViewRequest(a, "POST", "/api/requirement-views", "u_front", projectID, map[string]any{"name": "Legacy references", "scope": "personal", "config": config})
	if w.Code != 201 {
		t.Fatal("legacy view rejected", w.Code, w.Body)
	}
}
