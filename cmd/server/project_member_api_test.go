package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func projectCandidateMap(t *testing.T, payload map[string]any) map[string]map[string]any {
	t.Helper()
	items := map[string]map[string]any{}
	for _, raw := range payload["items"].([]any) {
		item := raw.(map[string]any)
		items[item["id"].(string)] = item
	}
	return items
}

func TestProjectMemberCandidatesPreserveLegacyReadAndProtectEnterpriseDirectory(t *testing.T) {
	a := testApp(t)
	legacy := orgRequest(t, a, "GET", "/api/projects/"+projectID+"/members", "u_front", nil, 200)
	if legacy["canManage"] != nil || projectCandidateMap(t, legacy)["u_front"] == nil {
		t.Fatal("legacy roster contract changed")
	}
	path := "/api/projects/" + insightProjectID + "/members?candidates=1"
	orgRequest(t, a, "GET", path, "u_front", nil, 403)
	// A project admin is not an enterprise member manager.
	bulkFixtureExec(t, a, `UPDATE project_members SET role='project_admin' WHERE project_id=? AND user_id='u_front'`, projectID)
	orgRequest(t, a, "GET", "/api/projects/"+projectID+"/members?candidates=1", "u_front", nil, 403)
	orgGroup(t, a, "Project candidate manager", []string{"members.manage"}, []string{"u_front"})
	bulkFixtureExec(t, a, `UPDATE users SET active=0 WHERE id='u_back'; UPDATE tenant_memberships SET status='disabled' WHERE user_id='u_back'; UPDATE tenant_memberships SET status='removed' WHERE user_id='u_member'; INSERT INTO tenants(id,name) VALUES('candidate-foreign','Other'); INSERT INTO users(id,tenant_id,name,email) VALUES('candidate-foreign-user','candidate-foreign','Other','foreign-candidate@example.com'); INSERT INTO tenant_memberships VALUES('candidate-foreign','candidate-foreign-user','member','active',?,?)`, orgNow(), orgNow())
	// Delegated organization permission is enough even without membership in the
	// target project, and a stale browser project header is deliberately ignored.
	payload := orgRequest(t, a, "GET", path, "u_front", nil, 200)
	if payload["canManage"] != true || payload["isTenantAdmin"] != false || payload["currentUserId"] != "u_front" {
		t.Fatalf("management context missing: %v", payload)
	}
	items := projectCandidateMap(t, payload)
	if items["u_back"] == nil || items["u_back"]["active"] != false || items["u_back"]["role"] != nil || items["u_back"]["tenantRole"] != "member" {
		t.Fatal("inactive unassigned candidate omitted/misrepresented")
	}
	if items["candidate-foreign-user"] != nil || items["u_member"] != nil {
		t.Fatal("foreign or removed candidate leaked")
	}
	for _, item := range items {
		if len(item) != 6 {
			t.Fatalf("unexpected sensitive candidate field: %v", item)
		}
	}
	for _, query := range []string{"?candidates=0", "?candidates=1&candidates=1"} {
		orgRequest(t, a, "GET", "/api/projects/"+projectID+"/members"+query, "u_admin", nil, 422)
	}
	orgRequest(t, a, "GET", "/api/projects/missing/members?candidates=1", "u_admin", nil, 404)
	bulkFixtureExec(t, a, `INSERT INTO projects(id,tenant_id,name,code,status) VALUES('candidate-foreign-project','candidate-foreign','Other','OTHER','active'); UPDATE projects SET status='archived' WHERE id=?`, insightProjectID)
	orgRequest(t, a, "GET", "/api/projects/candidate-foreign-project/members?candidates=1", "u_admin", nil, 404)
	orgRequest(t, a, "GET", path, "u_admin", nil, 409)
	list := orgRequest(t, a, "GET", "/api/projects", "u_front", nil, 200)
	if list["canManageMembers"] != true {
		t.Fatal("delegated manager entry hidden")
	}
}

func TestProjectMemberPatchIsIncrementalPreservesRolesAndInactiveAccounts(t *testing.T) {
	a := testApp(t)
	path := "/api/projects/" + insightProjectID + "/members"
	bulkFixtureExec(t, a, `UPDATE users SET active=0 WHERE id='u_front'; UPDATE tenant_memberships SET status='disabled' WHERE user_id='u_front'`)
	var existingRole, otherRole string
	if err := a.db.QueryRow(`SELECT role FROM project_members WHERE project_id=? AND user_id='u_algo'`, insightProjectID).Scan(&existingRole); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT role FROM project_members WHERE project_id=? AND user_id='u_front'`, projectID).Scan(&otherRole); err != nil {
		t.Fatal(err)
	}
	payload := orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front", "u_algo", "u_front"}, "role": "viewer"}, 200)
	items := projectCandidateMap(t, payload)
	if items["u_front"]["role"] != "viewer" || items["u_front"]["active"] != false || items["u_algo"]["role"] != existingRole || payload["isTenantAdmin"] != true || payload["currentUserId"] != "u_admin" {
		t.Fatal("incorrect candidates after mutation")
	}
	for _, table := range []string{"memberships", "project_members"} {
		if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE project_id=? AND user_id='u_front' AND role='viewer'`, insightProjectID) != 1 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE project_id=? AND user_id='u_algo' AND role=?`, insightProjectID, existingRole) != 1 {
			t.Fatal("dual membership table out of sync")
		}
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE project_id=? AND user_id='u_front' AND role=?`, projectID, otherRole) != 1 {
		t.Fatal("other project role changed")
	}
	payload = orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"removeUserIds": []string{"u_front"}, "addUserIds": []string{"u_back"}}, 200)
	items = projectCandidateMap(t, payload)
	if items["u_front"]["role"] != nil || items["u_back"]["role"] != "viewer" {
		t.Fatal("mixed incremental update incorrect")
	}
	if bulkFixtureCount(t, a, `SELECT active FROM users WHERE id='u_front'`) != 0 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM tenant_memberships WHERE user_id='u_front' AND status='disabled'`) != 1 {
		t.Fatal("project assignment activated account")
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs WHERE action='project_members_updated' AND object_id=?`, insightProjectID) != 2 {
		t.Fatal("project changes not audited")
	}
}

func TestProjectMemberPatchValidationAndWholeTransactionRollback(t *testing.T) {
	a := testApp(t)
	path := "/api/projects/" + insightProjectID + "/members"
	for _, body := range []any{
		map[string]any{"addUserIds": []string{"u_front"}, "removeUserIds": []string{"u_front"}},
		map[string]any{"addUserIds": []string{"u_front"}, "role": "project_admin"},
		map[string]any{"addUserIds": []string{" u_front"}},
		map[string]any{"addUserIds": []string{"u_front"}, "active": true},
	} {
		orgRequest(t, a, "PATCH", path, "u_admin", body, 422)
	}
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "u_front"
	}
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": ids}, 422)
	for _, id := range []string{"missing", "u_admin"} {
		status := 404
		if id == "u_admin" {
			status = 409
		}
		orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front"}, "removeUserIds": []string{id}}, status)
	}
	bulkFixtureExec(t, a, `UPDATE tenant_memberships SET role='tenant_admin' WHERE user_id='u_back'`)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"removeUserIds": []string{"u_back"}}, 409)
	bulkFixtureExec(t, a, `UPDATE tenant_memberships SET status='removed' WHERE user_id='u_member'`)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front", "u_member"}}, 404)
	bulkFixtureExec(t, a, `CREATE TRIGGER block_project_member_audit BEFORE INSERT ON audit_logs WHEN NEW.action='project_members_updated' BEGIN SELECT RAISE(ABORT,'isolated audit failure'); END`)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front"}, "removeUserIds": []string{"u_algo"}}, 503)
	for _, table := range []string{"memberships", "project_members"} {
		if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE project_id=? AND user_id='u_front'`, insightProjectID) != 0 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE project_id=? AND user_id='u_algo'`, insightProjectID) != 1 {
			t.Fatal("failed audit left partial grant/removal")
		}
	}
	bulkFixtureExec(t, a, `DROP TRIGGER block_project_member_audit; UPDATE projects SET status='archived' WHERE id=?`, insightProjectID)
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front"}}, 409)
}

func TestProjectMemberPatchDelegatedManagerCannotRemoveProtectedRoles(t *testing.T) {
	a := testApp(t)
	path := "/api/projects/" + projectID + "/members"
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_front"}}, 403)
	group := orgGroup(t, a, "Project delegated manager", []string{"members.manage"}, []string{"u_viewer"})
	bulkFixtureExec(t, a, `UPDATE project_members SET role='project_admin' WHERE project_id=? AND user_id='u_front'`, projectID)
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_front"}}, 403)
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_viewer"}}, 409)
	orgGroup(t, a, "Protected project target", []string{"organization.read"}, []string{"u_back"})
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_back"}}, 403)
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_member"}}, 200)
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"addUserIds": []string{"u_member"}}, 200)
	bulkFixtureExec(t, a, `DELETE FROM organization_group_permissions WHERE group_id=?`, group)
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"removeUserIds": []string{"u_member"}}, 403)
	orgRequest(t, a, "GET", path+"?candidates=1", "u_viewer", nil, 403)
}

func TestProjectMemberRemovalImmediatelyRevokesOnlyTargetProjectAccess(t *testing.T) {
	a := testApp(t)
	orgRequest(t, a, "PATCH", "/api/projects/"+insightProjectID+"/members", "u_admin", map[string]any{"addUserIds": []string{"u_front"}}, 200)
	cookie, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "u_front")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := securityIntegrationToken(t, a, "u_front", []string{"requirements:read"})
	orgRequest(t, a, "PATCH", "/api/projects/"+projectID+"/members", "u_admin", map[string]any{"removeUserIds": []string{"u_front"}}, 200)
	for _, tc := range []struct {
		project string
		status  int
	}{{projectID, 403}, {insightProjectID, 200}} {
		r := httptest.NewRequest("GET", "/api/requirements", nil)
		r.Header.Set("X-DevFlow-Project", tc.project)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		a.scopedAPI().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("project access after removal: %d want %d %s", w.Code, tc.status, w.Body.String())
		}
	}
	w := securityIntegrationRequest(t, a, secret, "GET", "/api/open/v1/requirements", "", nil)
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("project token survived membership removal: %d", w.Code)
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id='u_front' AND active=1 AND operation_disabled=0`) != 1 {
		t.Fatal("project removal disabled account")
	}
}

func TestProjectMemberPatchRechecksConcurrentGrantRevocation(t *testing.T) {
	a := fileSQLiteTestApp(t)
	group := orgGroup(t, a, "Concurrent project manager", []string{"members.manage"}, []string{"u_viewer"})
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM organization_group_permissions WHERE group_id=?`, group); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := httptest.NewRequest("PATCH", "/api/projects/"+insightProjectID+"/members", strings.NewReader(`{"addUserIds":["u_front"]}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	scoped := *a
	scoped.user = "u_viewer"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); scoped.manageProjectMembers(w, r, insightProjectID); done <- w }()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 403 {
			t.Fatalf("stale project grant used: %d %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE user_id='u_front' AND project_id=?`, insightProjectID) != 0 {
		t.Fatal("revoked manager changed project")
	}
}

func TestProjectMemberPatchRejectsForeignTargetsAndPreservesLegacyRoles(t *testing.T) {
	a := testApp(t)
	path := "/api/projects/" + insightProjectID + "/members"
	bulkFixtureExec(t, a, `INSERT INTO tenants(id,name) VALUES('patch-foreign','Other'); INSERT INTO projects(id,tenant_id,name,code,status) VALUES('patch-foreign-project','patch-foreign','Other','PATCHOTHER','active'); INSERT INTO users(id,tenant_id,name,email) VALUES('patch-foreign-user','patch-foreign','Other','foreign-patch@example.com'); INSERT INTO tenant_memberships VALUES('patch-foreign','patch-foreign-user','member','active',?,?)`, orgNow(), orgNow())
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front", "patch-foreign-user"}}, 404)
	orgRequest(t, a, "PATCH", "/api/projects/patch-foreign-project/members", "u_admin", map[string]any{"addUserIds": []string{"u_front"}}, 404)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE user_id='u_front' AND project_id=?`, insightProjectID) != 0 {
		t.Fatal("cross-tenant batch partially changed membership")
	}
	// A legacy-only non-admin binding is restored at its existing role, and an
	// administrator binding can only be restored by an actual tenant admin.
	bulkFixtureExec(t, a, `INSERT INTO memberships(tenant_id,project_id,user_id,role) VALUES(?,?,'u_front','frontend'),(?,?,'u_back','project_admin')`, tenantID, insightProjectID, tenantID, insightProjectID)
	orgGroup(t, a, "Legacy project manager", []string{"members.manage"}, []string{"u_viewer"})
	orgRequest(t, a, "PATCH", path, "u_viewer", map[string]any{"addUserIds": []string{"u_front", "u_back"}}, 403)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE user_id='u_front' AND project_id=?`, insightProjectID) != 0 {
		t.Fatal("later protected legacy role failed without rollback")
	}
	orgRequest(t, a, "PATCH", path, "u_admin", map[string]any{"addUserIds": []string{"u_front", "u_back"}}, 200)
	for _, table := range []string{"memberships", "project_members"} {
		if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE project_id=? AND ((user_id='u_front' AND role='frontend') OR (user_id='u_back' AND role='project_admin'))`, insightProjectID) != 2 {
			t.Fatal("existing legacy roles were downgraded")
		}
	}
}
