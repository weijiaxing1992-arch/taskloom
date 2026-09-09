package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func fieldTestDefinition(t *testing.T, a *App, body string) FieldDefinition {
	t.Helper()
	w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, body)
	if w.Code != 201 {
		t.Fatalf("definition: %d %s", w.Code, w.Body.String())
	}
	var d FieldDefinition
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFieldPresetsIdempotentScopedAndNoOverwrite(t *testing.T) {
	a := testApp(t)
	old := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"custom_leader_key","name":"前端组长","type":"text","description":"用户自己设置"}`)
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", old.ID), "u_admin", projectID, `{"enabled":false}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, user := range []string{"u_viewer", "u_front", "u_pm"} {
		w = apiRequest(a, "POST", "/api/field-presets/apply", user, projectID, `{"objectType":"requirement"}`)
		if w.Code != 403 {
			t.Fatalf("nonadmin %s: %d", user, w.Code)
		}
	}
	w = apiRequest(a, "GET", "/api/field-presets?objectType=requirement", "u_viewer", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["canManage"] != false {
		t.Fatalf("viewer catalog: %d %s", w.Code, w.Body.String())
	}
	for round := 0; round < 2; round++ {
		w = apiRequest(a, "POST", "/api/field-presets/apply", "u_admin", projectID, `{"objectType":"requirement"}`)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		body := jsonMap(t, w)
		expected := float64(len(requirementFieldPresets()) - 2) // existing renamed leader and built-in testers
		if round == 1 {
			expected = 0
		}
		if body["createdCount"] != expected {
			t.Fatalf("round %d: %s", round, w.Body.String())
		}
	}
	var name, kind, description string
	var enabled bool
	if err := a.db.QueryRow(`SELECT name,type,description,enabled FROM field_definitions WHERE id=?`, old.ID).Scan(&name, &kind, &description, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "前端组长" || kind != "text" || description != "用户自己设置" || enabled {
		t.Fatal("existing disabled custom definition was overwritten")
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM field_definitions WHERE project_id=? AND key='backend_leads'`, insightProjectID).Scan(&count)
	if count != 0 {
		t.Fatal("other project changed")
	}
	for _, entry := range requirementSystemFields() {
		if strings.HasPrefix(entry.Key, "role.") && !entry.System {
			t.Fatal("weight field duplicated")
		}
	}
	w = apiRequest(a, "POST", "/api/field-presets/apply", "u_admin", projectID, `{"keys":["unknown"]}`)
	if w.Code != 422 {
		t.Fatal("unknown key accepted")
	}
	if err := a.migrateFieldDepartments(); err != nil {
		t.Fatal(err)
	}
}

func TestFieldPresetsTenantAdminWithViewerProjectRoleAndOtherObjectCatalogs(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE memberships SET role='viewer' WHERE user_id='u_admin'; UPDATE project_members SET role='viewer' WHERE user_id='u_admin'`); err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{"requirement", "defect", "test_case", "sprint"} {
		w := apiRequest(a, "GET", "/api/field-presets?objectType="+object, "u_admin", projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		body := jsonMap(t, w)
		if body["canManage"] != true {
			t.Fatal("tenant admin masked by project viewer role")
		}
		if object != "requirement" && (len(body["systemFields"].([]any)) != 0 || len(body["presets"].([]any)) != 0) {
			t.Fatal("requirement catalog leaked to another object")
		}
	}
	w := apiRequest(a, "POST", "/api/field-presets/apply", "u_admin", projectID, `{"keys":["testers"]}`)
	if w.Code != 200 || jsonMap(t, w)["createdCount"] != float64(0) || jsonMap(t, w)["skippedCount"] != float64(1) {
		t.Fatal(w.Body.String())
	}
	d := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"admin_field","name":"管理员字段","type":"user"}`)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, `{"description":"企业管理员可配置"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestDepartmentMemberDTODoesNotUseDisplayText(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE users SET department='后端研发组' WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", "/api/members", "u_viewer", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, raw := range jsonMap(t, w)["items"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == "u_front" {
			ids := m["departmentIds"].([]any)
			if len(ids) != 1 || ids[0] != "dept_frontend" {
				t.Fatalf("DTO follows arbitrary display text: %+v", m)
			}
		}
	}
	if _, err := a.db.Exec(`UPDATE department_memberships SET status='inactive' WHERE user_id='u_front'`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/members", "u_viewer", projectID, "")
	for _, raw := range jsonMap(t, w)["items"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == "u_front" && len(m["departmentIds"].([]any)) != 0 {
			t.Fatal("inactive membership offered")
		}
	}
	w = apiRequest(a, "GET", "/api/departments", "u_viewer", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) == 0 {
		t.Fatal("department catalog unavailable")
	}
}

func TestFieldDefaultsValidateDepartmentAndClearExplicitly(t *testing.T) {
	a := testApp(t)
	for _, body := range []string{
		`{"objectType":"requirement","key":"x","name":"人员","type":"text","departmentId":"dept_frontend"}`,
		`{"objectType":"requirement","key":"x","name":"人员","type":"users","departmentId":"foreign"}`,
		`{"objectType":"requirement","key":"x","name":"人员","type":"users","departmentId":"dept_frontend","defaultValue":["u_back"]}`,
		`{"objectType":"requirement","key":"x","name":"人员","type":"users","defaultValue":"u_front"}`,
		`{"objectType":"requirement","key":"x","name":"人员","type":"user","defaultValue":"沈星"}`,
	} {
		w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatalf("invalid definition accepted: %d %s", w.Code, w.Body.String())
		}
	}
	d := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"front_people","name":"前端人员","type":"users","description":"说明","required":false,"filterable":true,"departmentId":"dept_frontend","defaultValue":["u_front"]}`)
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, `{"departmentId":"dept_backend"}`)
	if w.Code != 422 {
		t.Fatal("department change retained out-of-range default")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, `{"defaultValue":null,"description":"","departmentId":"dept_backend"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	body := jsonMap(t, w)
	if body["description"] != "" || body["defaultValue"] != nil || body["filterable"] != true || body["enabled"] != true {
		t.Fatalf("PATCH lost omitted flags / failed clear: %s", w.Body.String())
	}
}

func TestCustomPersonnelScopeAndTransactionRollback(t *testing.T) {
	a := testApp(t)
	fieldTestDefinition(t, a, `{"objectType":"requirement","key":"reviewers","name":"评审人","type":"users","departmentId":"dept_frontend"}`)
	x := planningRequirement(t, a, `{"title":"合法需求","customFields":{"reviewers":["u_front"]}}`)
	for _, value := range []string{`["u_back"]`, `["u_front","u_back"]`, `["u_front","u_front"]`, `[""]`, `"u_front"`, `["沈星"]`} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"title":"不该保存","customFields":{"reviewers":`+value+`}}`)
		if w.Code != 422 {
			t.Fatalf("invalid person %s: %d %s", value, w.Code, w.Body.String())
		}
		saved, err := a.get(x.ID)
		if err != nil || saved.Title != x.Title || jsonText(saved.CustomFields["reviewers"]) != `["u_front"]` {
			t.Fatalf("partial business write: %+v %v", saved, err)
		}
	}
	// Current directory, not cached form membership, is authoritative.
	a.db.Exec(`UPDATE department_memberships SET status='inactive' WHERE user_id='u_front_lead'`)
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewers":["u_front_lead"]}}`)
	if w.Code != 422 {
		t.Fatal("inactive directory relation accepted")
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", insightProjectID, `{"title":"cross project","customFields":{"reviewers":["u_front"]}}`)
	if w.Code != 422 {
		t.Fatal("foreign project definition accepted")
	}
}

func TestCustomPersonnelHistoricalValuesRetainedButCannotBeAdded(t *testing.T) {
	a := testApp(t)
	d := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"reviewer","name":"评审人","type":"user","departmentId":"dept_frontend"}`)
	x := planningRequirement(t, a, `{"title":"历史绑定","customFields":{"reviewer":"u_front"}}`)
	a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`)
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewer":"u_front"},"remarks":"保留历史"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, `{"title":"新需求不可选停用成员","customFields":{"reviewer":"u_front"}}`)
	if w.Code != 422 {
		t.Fatal("new inactive ID accepted")
	}
	a.db.Exec(`UPDATE field_values SET value_json=? WHERE object_id=? AND field_definition_id=?`, jsonText("旧姓名"), x.ID, d.ID)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewer":"旧姓名"}}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewer":"另一旧姓名"}}`)
	if w.Code != 422 {
		t.Fatal("new arbitrary name accepted")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewer":""}}`)
	if w.Code != 200 {
		t.Fatal("clear failed")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"reviewer":"旧姓名"}}`)
	if w.Code != 422 {
		t.Fatal("cleared historical value could be reintroduced")
	}
}

func TestCustomUsersRequiredAndTypedFilter(t *testing.T) {
	a := testApp(t)
	fieldTestDefinition(t, a, `{"objectType":"requirement","key":"reviewers","name":"评审人","type":"users","required":true,"departmentId":"dept_frontend"}`)
	for _, body := range []string{`{"title":"missing"}`, `{"title":"empty","customFields":{"reviewers":[]}}`} {
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatal("required people missing allowed")
		}
	}
	x := planningRequirement(t, a, `{"title":"多人评审","customFields":{"reviewers":["u_front","u_front_lead"]}}`)
	filter := url.QueryEscape(`[{"field":"cf.reviewers","operator":"includes","value":"u_front_lead"}]`)
	w := apiRequest(a, "GET", "/api/requirements?filters="+filter, "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	items := jsonMap(t, w)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != float64(x.ID) {
		t.Fatal("second selected member not queryable")
	}
}

func TestCustomDepartmentConstraintCoversDefectsAndTestCases(t *testing.T) {
	a := testApp(t)
	for _, object := range []string{"defect", "test_case"} {
		fieldTestDefinition(t, a, fmt.Sprintf(`{"objectType":%q,"key":"reviewers","name":"评审人","type":"users","departmentId":"dept_frontend"}`, object))
		path := "/api/defects"
		if object == "test_case" {
			path = "/api/test-cases"
		}
		w := apiRequest(a, "POST", path, "u_admin", projectID, `{"title":"不得保存","customFields":{"reviewers":["u_back"]}}`)
		if w.Code != 422 {
			t.Fatalf("%s custom validation: %d %s", object, w.Code, w.Body.String())
		}
	}
}

func TestDepartmentValidationRejectsForeignTenantAndProjectOnlyDirectoryMembers(t *testing.T) {
	a := testApp(t)
	fieldTestDefinition(t, a, `{"objectType":"requirement","key":"people","name":"项目人员","type":"users","departmentId":"dept_frontend"}`)
	// A directory member with a matching department but no project membership
	// cannot be assigned. Conversely, matching display text grants nothing.
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE user_id='u_front_lead' AND project_id=?`, projectID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u_front_lead", "u_back", "foreign-user"} {
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, fmt.Sprintf(`{"title":"不能建立","customFields":{"people":[%q]}}`, id))
		if w.Code != 422 {
			t.Fatalf("out of scope %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "GET", "/api/members", "u_admin", projectID, "")
	for _, raw := range jsonMap(t, w)["items"].([]any) {
		m := raw.(map[string]any)
		if m["id"] == "u_front_lead" && m["active"] != false {
			t.Fatal("project-revoked person still offered")
		}
	}
}
