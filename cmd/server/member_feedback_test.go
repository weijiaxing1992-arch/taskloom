package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func setFeedbackRoles(t *testing.T, a *App, user string, roles []string) {
	t.Helper()
	orgRequest(t, a, "PATCH", "/api/organization/members/"+user, "u_admin", map[string]any{"projectMemberships": []map[string]any{{"projectId": projectID, "roles": roles}}}, 200)
}

func TestMemberFeedbackAutomaticEmployeeAndPreLoginManagement(t *testing.T) {
	t.Setenv("DEVFLOW_INITIAL_PASSWORD", "123456")
	a := testApp(t)
	bulkFixtureExec(t, a, `UPDATE users SET employee_no='DF120' WHERE id='u_front'`)
	created := orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "入职成员", "email": "feedback-new@example.test"}, 201)
	id := created["id"].(string)
	if created["employeeNo"] != "DF121" || created["mustChangePassword"] != true {
		t.Fatalf("automatic onboarding: %v", created)
	}
	department := orgDepartment(t, a, "入职部门", "FEEDBACK", nil)
	orgRequest(t, a, "PATCH", "/api/organization/members/"+id, "u_admin", map[string]any{"departmentIds": []string{department}, "primaryDepartmentId": department, "projectMemberships": []map[string]any{{"projectId": projectID, "roles": []string{"product", "qa"}}}}, 200)
	var hash string
	var pending bool
	if err := a.db.QueryRow(`SELECT password_hash,must_change_password FROM users WHERE id=?`, id).Scan(&hash, &pending); err != nil || !pending || !verifyPassword("123456", hash) {
		t.Fatalf("pre-login management changed credential: %v", err)
	}
	w, cookie := loginRequest(a, "feedback-new@example.test", "123456")
	if w.Code != 200 || cookie == nil {
		t.Fatalf("default login: %d %s", w.Code, w.Body.String())
	}
	w = initialRequest(a, cookie, "GET", "/api/requirements", "", id)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") {
		t.Fatalf("first-login bypass: %d %s", w.Code, w.Body.String())
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs WHERE object_id=? AND action='organization_member_saved'`, id) != 2 {
		t.Fatal("onboarding edit audit missing")
	}
	orgRequest(t, a, "DELETE", "/api/organization/members/"+id, "u_admin", nil, 200)
	recreated := orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "再次入职", "email": "feedback-new@example.test"}, 201)
	if recreated["employeeNo"] != "DF122" || recreated["id"] == id {
		t.Fatalf("removed employee number reused: %v", recreated)
	}
}

func TestMemberFeedbackRoleUnionAndLegacyCompatibility(t *testing.T) {
	a := testApp(t)
	setFeedbackRoles(t, a, "u_front", []string{"qa", "product", "qa"})
	roles, err := memberProjectRoles(context.Background(), a.db, projectID, "u_front")
	if err != nil || !reflect.DeepEqual(roles, []string{"product", "qa"}) {
		t.Fatalf("canonical role set: %v %v", roles, err)
	}
	var primary string
	a.db.QueryRow(`SELECT role FROM project_members WHERE project_id=? AND user_id='u_front'`, projectID).Scan(&primary)
	if primary != "product" {
		t.Fatal("legacy role lost compatibility", primary)
	}
	a.user = "u_front"
	canReview, err := a.testingReviewPermission(context.Background(), a.db)
	if err != nil || !canReview {
		t.Fatal("secondary qa role lost review permission", err)
	}
	for _, key := range []string{"testers", "ownerUserIds"} {
		if err := a.validateFieldMemberRole(a.db, FieldDefinition{ObjectType: "requirement", Type: "users", Key: key, Name: key}, "u_front"); err != nil {
			t.Fatalf("role candidate %s: %v", key, err)
		}
	}
	if err := a.validateFieldMemberRole(a.db, FieldDefinition{ObjectType: "requirement", Type: "users", Key: "role.backend.userIds", Name: "后端"}, "u_front"); err == nil {
		t.Fatal("ungranted role accepted")
	}
	// Roles scoped to another project must never affect this project's checks.
	bulkFixtureExec(t, a, `INSERT INTO project_member_roles VALUES(?,?,'u_front','backend')`, tenantID, insightProjectID)
	if err := a.validateFieldMemberRole(a.db, FieldDefinition{ObjectType: "requirement", Type: "users", Key: "role.backend.userIds", Name: "后端"}, "u_front"); err == nil {
		t.Fatal("cross-project role accepted")
	}
	bulkFixtureExec(t, a, `UPDATE project_members SET role='viewer' WHERE project_id=? AND user_id='u_front'; UPDATE memberships SET role='viewer' WHERE project_id=? AND user_id='u_front'`, projectID, projectID)
	canReview, err = a.testingReviewPermission(context.Background(), a.db)
	if err != nil || canReview {
		t.Fatal("old client role update retained secondary privilege", err)
	}
	roles, err = memberProjectRoles(context.Background(), a.db, projectID, "u_front")
	if err != nil || !reflect.DeepEqual(roles, []string{"viewer"}) {
		t.Fatal("legacy single role fallback failed", roles, err)
	}
}

func TestMemberFeedbackRoleUpdatesAtomicAndDelegationSafe(t *testing.T) {
	a := testApp(t)
	path := "/api/projects/" + projectID + "/members"
	payload := orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"roleUpdates": map[string][]string{"u_front": {"product", "qa"}}}, 200)
	item := projectCandidateMap(t, payload)["u_front"]
	if !reflect.DeepEqual(item["projectRoles"], []any{"product", "qa"}) {
		t.Fatal("candidate role response missing", item)
	}
	orgGroup(t, a, "成员维护", []string{"members.manage"}, []string{"u_back"})
	for _, roles := range [][]string{{"project_admin", "qa"}, {"tenant_admin"}, {}, {"unknown"}} {
		want := 422
		if len(roles) > 1 {
			want = 403
		}
		orgRequest(t, a, "PATCH", path, "u_back", map[string]any{"roleUpdates": map[string][]string{"u_front": roles}}, want)
	}
	before := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`)
	bulkFixtureExec(t, a, `CREATE TRIGGER reject_feedback_audit BEFORE INSERT ON audit_logs WHEN NEW.action='organization_member_saved' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"roleUpdates": map[string][]string{"u_front": {"backend"}}}, 503)
	roles, err := memberProjectRoles(context.Background(), a.db, projectID, "u_front")
	if err != nil || !reflect.DeepEqual(roles, []string{"product", "qa"}) || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`) != before {
		t.Fatal("failed role update partially committed", roles, err)
	}
	bulkFixtureExec(t, a, `DROP TRIGGER reject_feedback_audit`)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"removeUserIds": []string{"u_front"}}, 200)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_member_roles WHERE project_id=? AND user_id='u_front'`, projectID) != 0 {
		t.Fatal("removed membership retained role grant")
	}
}

func TestMemberFeedbackWorkflowUsesEveryGrantedRole(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"多角色流转"}`)
	flow := getWorkflow(t, a, projectID)
	flow.Transitions = []RequirementTransition{{"规划中", "开发中", []string{"qa"}}, {"开发中", "规划中", []string{"product"}}, {"规划中", "已拒绝", []string{"backend"}}}
	w := apiRequest(a, "PUT", "/api/requirement-workflow", "u_admin", projectID, jsonText(flow))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	setFeedbackRoles(t, a, "u_front", []string{"product", "qa"})
	path := fmt.Sprintf("/api/requirements/%d", x.ID)
	w = apiRequest(a, "GET", path+"/transitions", "u_front", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "开发中") || strings.Contains(w.Body.String(), "已拒绝") {
		t.Fatal("transition preview does not reflect role union", w.Body.String())
	}
	for _, status := range []string{"开发中", "规划中"} {
		w = apiRequest(a, "PATCH", path, "u_front", projectID, jsonText(map[string]string{"status": status}))
		if w.Code != 200 {
			t.Fatalf("granted %s edge denied: %s", status, w.Body.String())
		}
	}
	w = apiRequest(a, "PATCH", path, "u_front", projectID, `{"status":"已拒绝"}`)
	if w.Code != 403 {
		t.Fatal("ungranted backend edge allowed", w.Body.String())
	}
	setFeedbackRoles(t, a, "u_admin", []string{"qa"})
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"status":"开发中"}`)
	if w.Code != 200 {
		t.Fatal("tenant administrator lost explicitly granted qa edge", w.Body.String())
	}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"status":"规划中"}`)
	if w.Code != 403 {
		t.Fatal("tenant administrator gained ungranted product edge", w.Body.String())
	}
}

func TestMemberFeedbackLegacyProjectRoleWriteRevalidatesSession(t *testing.T) {
	for _, scenario := range []string{"revoked", "expired", "missing", "concurrent-reset", "impersonation"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			setFeedbackRoles(t, a, "u_front", []string{"project_admin", "qa"})
			scoped := administrationSessionFixture(t, a, "u_front")
			status := 401
			switch scenario {
			case "revoked":
				bulkFixtureExec(t, a, `UPDATE auth_sessions SET revoked_at=? WHERE token_hash=?`, orgNow(), scoped.sessionToken)
			case "expired":
				bulkFixtureExec(t, a, `UPDATE auth_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, scoped.sessionToken)
			case "missing":
				scoped.sessionToken = ""
			case "concurrent-reset":
				bulkFixtureExec(t, a, `CREATE TRIGGER feedback_reset AFTER UPDATE ON organization_write_locks BEGIN UPDATE users SET must_change_password=1 WHERE id='u_front'; END`)
				status = 403
			case "impersonation":
				bulkFixtureExec(t, a, `INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at)VALUES(?,?,'u_front','u_back',?,'review fixture',?,'2100-01-01T00:00:00Z')`, tenantID, scoped.sessionToken, projectID, orgNow())
				status = 403
			}
			beforeAudit := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`)
			beforeRevision := bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID)
			r := httptest.NewRequest("PATCH", "/api/members", strings.NewReader(`{"id":"u_back","projectRoles":["product","qa"]}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			scoped.legacyMemberWrite(w, r)
			if w.Code != status {
				t.Fatalf("stale snapshot admitted: %d %s", w.Code, w.Body.String())
			}
			roles, err := memberProjectRoles(context.Background(), a.db, projectID, "u_back")
			if err != nil || !reflect.DeepEqual(roles, []string{"backend"}) || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`) != beforeAudit || bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID) != beforeRevision {
				t.Fatal("rejected administration partially committed", roles, err)
			}
		})
	}
}

func TestMemberFeedbackProfileIncludesRoleSetWithLegacyFallback(t *testing.T) {
	a := testApp(t)
	for _, multi := range []bool{false, true} {
		if multi {
			setFeedbackRoles(t, a, "u_front", []string{"product", "qa"})
		}
		w := apiRequest(a, "GET", "/api/profile", "u_front", projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		found := false
		for _, raw := range jsonMap(t, w)["memberships"].([]any) {
			membership := raw.(map[string]any)
			if membership["projectId"] != projectID {
				continue
			}
			found = true
			want := []any{"frontend"}
			if multi {
				want = []any{"product", "qa"}
			}
			if !reflect.DeepEqual(membership["projectRoles"], want) || membership["role"] != want[0] {
				t.Fatalf("profile role contract: %v", membership)
			}
		}
		if !found {
			t.Fatal("profile omitted active project")
		}
	}
}
