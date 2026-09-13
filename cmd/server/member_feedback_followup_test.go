package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMemberFeedbackArchivedProjectChecksCompleteRoleSet(t *testing.T) {
	a := testApp(t)
	setFeedbackRoles(t, a, "u_front", []string{"product"})
	bulkFixtureExec(t, a, `UPDATE projects SET status='archived' WHERE id=?`, projectID)
	audits := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"projectMemberships": []map[string]any{{"projectId": projectID, "roles": []string{"product", "qa"}}}}, 422)
	roles, err := memberProjectRoles(context.Background(), a.db, projectID, "u_front")
	if err != nil || !reflect.DeepEqual(roles, []string{"product"}) || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`) != audits {
		t.Fatalf("archived project acquired secondary permissions: %v %v", roles, err)
	}
	// Unrelated pre-login/account administration must still preserve archived roles.
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"name": "归档项目保留角色"}, 200)
	roles, err = memberProjectRoles(context.Background(), a.db, projectID, "u_front")
	if err != nil || !reflect.DeepEqual(roles, []string{"product"}) {
		t.Fatalf("unrelated edit lost archived membership: %v %v", roles, err)
	}
}

func TestMemberFeedbackImportedRoleUnionSurvivesLifecycle(t *testing.T) {
	t.Setenv("DEVFLOW_INITIAL_PASSWORD", "RoleLifecycleOnly123!")
	a := testApp(t)
	var code string
	if err := a.db.QueryRow(`SELECT code FROM projects WHERE id=?`, projectID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	preview := orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email,projectCode,projectRole\n角色导入,role-lifecycle@example.test," + code + ",qa|product\n"}, 200)
	if preview["canCommit"] != true {
		t.Fatal("multi-role import preview failed", preview)
	}
	orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 201)
	var id, employeeNo string
	var active, pending bool
	if err := a.db.QueryRow(`SELECT id,employee_no,active,must_change_password FROM users WHERE email='role-lifecycle@example.test'`).Scan(&id, &employeeNo, &active, &pending); err != nil || active || !pending || employeeNo == "" {
		t.Fatalf("import onboarding flags: active=%t pending=%t employee=%s err=%v", active, pending, employeeNo, err)
	}
	for _, action := range []string{"activate", "deactivate", "activate"} {
		bulkCall(t, a, "u_admin", action, []string{id}, nil, 200)
		roles, err := memberProjectRoles(context.Background(), a.db, projectID, id)
		if err != nil || !reflect.DeepEqual(roles, []string{"product", "qa"}) {
			t.Fatalf("%s discarded role union: %v %v", action, roles, err)
		}
		if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id=? AND must_change_password=1`, id) != 1 {
			t.Fatalf("%s bypassed mandatory first change", action)
		}
	}
	w := apiRequest(a, "GET", "/api/organization/members/export", "u_admin", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "product|qa") {
		t.Fatalf("CSV export lost role union: %d", w.Code)
	}
	bulkCall(t, a, "u_admin", "delete", []string{id}, nil, 200)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_member_roles WHERE user_id=?`, id) != 0 {
		t.Fatal("removed user retained secondary grants")
	}
	recreated := orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "邮箱重建", "email": "ROLE-LIFECYCLE@example.test"}, 201)
	if recreated["id"] == id || recreated["employeeNo"] == employeeNo || recreated["mustChangePassword"] != true || len(recreated["projectMemberships"].([]any)) != 0 {
		t.Fatal("recreated email inherited the removed identity or roles")
	}
}

func TestMemberFeedbackSecondaryQAReviewUsesHTTPAuthorization(t *testing.T) {
	a := testApp(t)
	setFeedbackRoles(t, a, "u_front", []string{"product", "qa"})
	item := createWorkspaceCase(t, a, "多角色评审 HTTP")
	path := fmt.Sprintf("/api/testing/cases/%d/reviews", item.ID)
	for _, step := range []struct{ user, decision string }{{"u_admin", "submit"}, {"u_front", "approve"}} {
		w := apiRequest(a, "POST", path, step.user, projectID, fmt.Sprintf(`{"decision":%q,"comment":"角色回归"}`, step.decision))
		if w.Code != 201 {
			t.Fatalf("%s review rejected through full route: %d %s", step.decision, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "GET", fmt.Sprintf("/api/test-cases/%d", item.ID), "u_front", projectID, "")
	var saved TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Status != "已通过" {
		t.Fatalf("review result not persisted: %d status=%s err=%v", w.Code, saved.Status, err)
	}
}

func TestMemberFeedbackProjectRemovalAuditsEveryRevokedRole(t *testing.T) {
	a := testApp(t)
	setFeedbackRoles(t, a, "u_front", []string{"product", "qa"})
	orgRequest(t, a, "PATCH", "/api/projects/"+projectID+"/members", "u_admin", map[string]any{"removeUserIds": []string{"u_front"}}, 200)
	var raw string
	if err := a.db.QueryRow(`SELECT after_json FROM audit_logs WHERE action='project_members_updated' AND object_id=? ORDER BY id DESC LIMIT 1`, projectID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var audit struct {
		ProjectRolesBefore map[string][]string `json:"projectRolesBefore"`
		ProjectRolesAfter  map[string][]string `json:"projectRolesAfter"`
	}
	if err := json.Unmarshal([]byte(raw), &audit); err != nil || !reflect.DeepEqual(audit.ProjectRolesBefore["u_front"], []string{"product", "qa"}) || !reflect.DeepEqual(audit.ProjectRolesAfter["u_front"], []string{}) {
		t.Fatalf("audit omits secondary role revocation: before=%v after=%v err=%v", audit.ProjectRolesBefore, audit.ProjectRolesAfter, err)
	}
}

func TestMemberFeedbackViewerCombinationKeepsBusinessWritePermission(t *testing.T) {
	for _, route := range []string{"organization", "project", "legacy", "legacy-project-admin"} {
		for _, granted := range []string{"qa", "product"} {
			t.Run(route+"/"+granted, func(t *testing.T) {
				a := testApp(t)
				roles := []string{"viewer", granted}
				switch route {
				case "organization":
					setFeedbackRoles(t, a, "u_front", roles)
				case "project":
					orgRequest(t, a, "PATCH", "/api/projects/"+projectID+"/members", "u_admin", map[string]any{"roleUpdates": map[string][]string{"u_front": roles}}, 200)
				case "legacy", "legacy-project-admin":
					actor := "u_admin"
					if route == "legacy-project-admin" {
						setFeedbackRoles(t, a, "u_back", []string{"project_admin"})
						actor = "u_back"
					}
					w := apiRequest(a, "PATCH", "/api/members", actor, projectID, jsonText(map[string]any{"id": "u_front", "projectRoles": roles}))
					if w.Code != 200 {
						t.Fatalf("legacy role update: %d %s", w.Code, w.Body.String())
					}
				}
				for _, table := range []string{"memberships", "project_members"} {
					var primary string
					if err := a.db.QueryRow(`SELECT role FROM `+table+` WHERE project_id=? AND user_id='u_front'`, projectID).Scan(&primary); err != nil || primary != granted {
						t.Fatalf("viewer became primary in %s: role=%s err=%v", table, primary, err)
					}
				}
				w := apiRequest(a, "GET", "/api/session", "u_front", projectID, "")
				if w.Code != 200 {
					t.Fatalf("member session: %d", w.Code)
				}
				user := jsonMap(t, w)["user"].(map[string]any)
				if user["role"] != granted || jsonText(user["projectRoles"]) != jsonText([]string{granted, "viewer"}) {
					t.Fatal("frontend received inconsistent role union", user["role"], user["projectRoles"])
				}
				w = apiRequest(a, "POST", "/api/requirements", "u_front", projectID, `{"title":"多角色写权限"}`)
				if w.Code != 201 {
					t.Fatalf("secondary writer denied: %d %s", w.Code, w.Body.String())
				}
				setFeedbackRoles(t, a, "u_front", []string{"viewer"})
				w = apiRequest(a, "POST", "/api/requirements", "u_front", projectID, `{"title":"只读不可写"}`)
				if w.Code != 403 {
					t.Fatalf("revoked writer still allowed: %d", w.Code)
				}
			})
		}
	}
}
