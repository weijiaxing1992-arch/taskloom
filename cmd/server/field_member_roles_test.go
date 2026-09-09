package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestFieldMemberRolesMetadataAndDefaultTestersPreserveConfiguration(t *testing.T) {
	a := testApp(t)
	defs, err := a.definitions("requirement", false)
	if err != nil {
		t.Fatal(err)
	}
	var testers FieldDefinition
	for _, d := range defs {
		if d.Key == "testers" {
			testers = d
		}
	}
	if testers.ID == 0 || testers.Type != "users" || !reflect.DeepEqual(testers.MemberRoles, []string{"qa"}) {
		t.Fatalf("missing tester default: %+v", testers)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", testers.ID), "u_admin", projectID, `{"name":"验收同事","departmentId":"dept_quality","enabled":false,"memberRoles":["frontend"]}`)
	if w.Code != 200 || jsonMap(t, w)["memberRoles"].([]any)[0] != "qa" {
		t.Fatalf("role broadened: %d %s", w.Code, w.Body.String())
	}
	if err := a.migrateDefaultTestersField(); err != nil {
		t.Fatal(err)
	}
	var name, department string
	var enabled bool
	var count int
	if err := a.db.QueryRow(`SELECT name,department_id,enabled FROM field_definitions WHERE id=?`, testers.ID).Scan(&name, &department, &enabled); err != nil {
		t.Fatal(err)
	}
	a.db.QueryRow(`SELECT count(*) FROM field_definitions WHERE tenant_id=? AND project_id=? AND key='testers'`, tenantID, projectID).Scan(&count)
	if name != "验收同事" || department != "dept_quality" || enabled || count != 1 {
		t.Fatal("migration replaced existing definition")
	}
	for _, d := range requirementSystemFields() {
		if d.Key == "role.frontend.userIds" && !reflect.DeepEqual(d.MemberRoles, []string{"frontend", "frontend_lead"}) {
			t.Fatal("lead candidate missing")
		}
	}
	w = apiRequest(a, "GET", "/api/field-presets?objectType=requirement", "u_viewer", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/projects", "u_admin", projectID, `{"name":"Fresh project","code":"TESTROLE"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var newCount int
	if err := a.db.QueryRow(`SELECT count(*) FROM field_definitions WHERE project_id='prj_testrole' AND key='testers' AND type='users' AND enabled=1`).Scan(&newCount); err != nil || newCount != 1 {
		t.Fatal("new project missing default testers")
	}
}

func TestFieldMemberRolesIntersectDepartmentAndKeepHistoricalBindings(t *testing.T) {
	a := testApp(t)
	defs, _ := a.definitions("requirement", false)
	var tester FieldDefinition
	for _, d := range defs {
		if d.Key == "testers" {
			tester = d
		}
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", tester.ID), "u_admin", projectID, `{"departmentId":"dept_quality"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	x := createPeopleRequirement(t, a, map[string]any{"customFields": map[string]any{"testers": []string{"u_qa"}}})
	// A same-department member with another project role still cannot be added.
	if _, err := a.db.Exec(`INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,status,joined_at,updated_at)VALUES(?,'dept_quality','u_front',0,'active','now','now')`, tenantID); err != nil {
		t.Fatal(err)
	}
	before := tableCount(t, a, "user_notifications")
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"customFields":{"testers":["u_qa","u_front"]}}`)
	if w.Code != 422 || tableCount(t, a, "user_notifications") != before {
		t.Fatalf("wrong-role member accepted: %d %s", w.Code, w.Body.String())
	}
	// Changing a member's role later must not invalidate an unrelated save.
	if _, err := a.db.Exec(`UPDATE project_members SET role='frontend' WHERE tenant_id=? AND project_id=? AND user_id='u_qa'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"remarks":"保留历史","customFields":{"testers":["u_qa"]}}`)
	if w.Code != 200 {
		t.Fatalf("historical binding rejected: %d %s", w.Code, w.Body.String())
	}
	var got Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.CustomFields["testers"].([]any)[0] != "u_qa" {
		t.Fatal("historical tester lost")
	}
}
