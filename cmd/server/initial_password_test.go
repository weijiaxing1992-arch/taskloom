package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const temporaryTestPassword = "stageA"

func forceInitialPassword(t *testing.T, a *App, user string) string {
	t.Helper()
	hash, err := a.encodePassword(temporaryTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`UPDATE users SET password_hash=?,must_change_password=1 WHERE tenant_id=? AND id=?`, hash, tenantID, user); err != nil {
		t.Fatal(err)
	}
	return hash
}
func initialRequest(a *App, cookie *http.Cookie, method, path, body, expected string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept-Language", "en-US")
	r.Header.Set("X-TaskLoom-Expected-User", expected)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}
func initialBody(current, newPassword string) string {
	return fmt.Sprintf(`{"currentPassword":%q,"newPassword":%q,"confirmPassword":%q}`, current, newPassword, newPassword)
}

func TestInitialPasswordGuardWithoutProjectAndSuccessfulChange(t *testing.T) {
	a := testApp(t)
	forceInitialPassword(t, a, "u_member")
	if _, err := a.db.Exec(`DELETE FROM memberships WHERE user_id='u_member'; DELETE FROM project_members WHERE user_id='u_member'`); err != nil {
		t.Fatal(err)
	}
	w, first := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	if w.Code != 200 || first == nil || jsonMap(t, w)["user"].(map[string]any)["mustChangePassword"] != true {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	_, second := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	w = initialRequest(a, first, "GET", "/api/session", "", "u_member")
	if w.Code != 200 || jsonMap(t, w)["project"].(map[string]any)["id"] != "" || len(jsonMap(t, w)["organizationPermissions"].([]any)) != 0 {
		t.Fatalf("minimal session %d %s", w.Code, w.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/requirements"}, {"POST", "/api/requirements"}, {"GET", "/api/projects"}, {"GET", "/api/search?q=secret"},
		{"GET", "/api/organization/admin"}, {"GET", "/api/notifications"}, {"GET", "/api/profile"}, {"POST", "/api/profile/password"},
		{"PATCH", "/api/preferences/locale"}, {"GET", "/api/reports/workload"}, {"POST", "/api/auth/impersonation"}, {"GET", "/api/requirements/1/export/pdf"},
	} {
		w = initialRequest(a, first, route.method, route.path, `{}`, "u_member")
		if w.Code != 403 || jsonMap(t, w)["error"].(map[string]any)["code"] != "password_change_required" || len(w.Result().Cookies()) != 0 {
			t.Fatalf("guard %s: %d %s", route.path, w.Code, w.Body.String())
		}
	}
	for _, expected := range []string{"", "u_admin"} {
		w = initialRequest(a, first, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), expected)
		if w.Code != 409 {
			t.Fatalf("identity guard %d %s", w.Code, w.Body.String())
		}
	}
	w = initialRequest(a, first, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 200 || jsonMap(t, w)["requiresLogin"] != true || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("change %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range []*http.Cookie{first, second} {
		if w := initialRequest(a, cookie, "GET", "/api/session", "", "u_member"); w.Code != 401 {
			t.Fatal("old session remains valid")
		}
	}
	var required bool
	var hash string
	if err := a.db.QueryRow(`SELECT must_change_password,password_hash FROM users WHERE id='u_member'`).Scan(&required, &hash); err != nil || required || !verifyPassword("Personal2027!", hash) || verifyPassword(temporaryTestPassword, hash) {
		t.Fatal("password/flag not changed together", err)
	}
	w, _ = loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	if w.Code != 401 {
		t.Fatal("temporary password still accepted")
	}
	w, _ = loginRequest(a, "zhouyu@devflow.local", "Personal2027!")
	if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["mustChangePassword"] != false {
		t.Fatal("new login failed")
	}
	var audit string
	if err := a.db.QueryRow(`SELECT before_json||after_json FROM audit_logs WHERE action='user.initial_password_changed'`).Scan(&audit); err != nil || strings.Contains(audit, "Personal2027") || strings.Contains(audit, temporaryTestPassword) || strings.Contains(audit, "$2") {
		t.Fatal("audit missing/leaks secret", err)
	}
}

func TestInitialPasswordValidationRateLimitAndBusinessDisable(t *testing.T) {
	a := testApp(t)
	original := forceInitialPassword(t, a, "u_member")
	_, cookie := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	for _, body := range []string{initialBody("wrong", "Personal2027!"), initialBody(temporaryTestPassword, temporaryTestPassword), initialBody(temporaryTestPassword, "allletters"), initialBody(temporaryTestPassword, "Personal2027!") + ` {}`, `{"currentPassword":"stageA","newPassword":"Personal2027!","confirmPassword":"Personal2027!","userId":"u_admin"}`} {
		w := initialRequest(a, cookie, "POST", "/api/auth/initial-password", body, "u_member")
		if w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid body passed: %d %s", w.Code, w.Body.String())
		}
	}
	for i := 0; i < 10; i++ {
		initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody("wrong", "Personal2027!"), "u_member")
	}
	w := initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit %d %s", w.Code, w.Body.String())
	}
	var hash string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_member'`).Scan(&hash)
	if hash != original {
		t.Fatal("validation changed hash")
	}
	a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='u_member'`)
	w = initialRequest(a, cookie, "GET", "/api/session", "", "u_member")
	user := jsonMap(t, w)["user"].(map[string]any)
	if w.Code != 200 || user["operationDisabled"] != true || user["mustChangePassword"] != true {
		t.Fatal("disabled minimal session incorrect")
	}
	w = initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 403 || jsonMap(t, w)["error"].(map[string]any)["code"] != "account_disabled" {
		t.Fatal("disabled operation passed")
	}
	a.db.Exec(`UPDATE users SET active=0,operation_disabled=0 WHERE id='u_member'`)
	w, fresh := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	if w.Code != 401 || fresh != nil {
		t.Fatal("inactive account activated by temporary password")
	}
}

func TestInitialPasswordAuditRollbackAndConcurrentReset(t *testing.T) {
	for _, scenario := range []string{"audit-failure", "concurrent-reset", "session-revoked", "identity-disabled"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			original := forceInitialPassword(t, a, "u_member")
			_, cookie := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
			resetHash, _ := a.encodePassword("ResetAgain2028!")
			switch scenario {
			case "audit-failure":
				a.db.Exec(`CREATE TRIGGER fail_initial_audit BEFORE INSERT ON audit_logs WHEN NEW.action='user.initial_password_changed' BEGIN SELECT RAISE(ABORT,'audit failed'); END`)
			case "concurrent-reset":
				a.db.Exec(`CREATE TRIGGER concurrent_initial_reset AFTER INSERT ON auth_initial_password_attempts BEGIN UPDATE users SET password_hash='` + resetHash + `' WHERE id='u_member'; END`)
			case "session-revoked":
				a.db.Exec(`CREATE TRIGGER concurrent_session_revocation AFTER INSERT ON auth_initial_password_attempts BEGIN UPDATE auth_sessions SET revoked_at='changed' WHERE user_id='u_member'; END`)
			case "identity-disabled":
				a.db.Exec(`CREATE TRIGGER concurrent_account_disable AFTER INSERT ON auth_initial_password_attempts BEGIN UPDATE users SET operation_disabled=1 WHERE id='u_member'; END`)
			}
			w := initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
			want := 409
			if scenario == "audit-failure" {
				want = 503
			}
			if w.Code != want || len(w.Result().Cookies()) != 0 {
				t.Fatalf("%s got %d %s", scenario, w.Code, w.Body.String())
			}
			var required bool
			var hash string
			a.db.QueryRow(`SELECT must_change_password,password_hash FROM users WHERE id='u_member'`).Scan(&required, &hash)
			if !required || verifyPassword("Personal2027!", hash) || (scenario == "audit-failure" && hash != original) || (scenario == "concurrent-reset" && hash != resetHash) {
				t.Fatal("stale operation overwrote state")
			}
			if scenario == "audit-failure" {
				var valid int
				a.db.QueryRow(`SELECT count(*) FROM auth_sessions WHERE user_id='u_member' AND revoked_at IS NULL`).Scan(&valid)
				if valid != 1 {
					t.Fatal("audit rollback revoked original session")
				}
			}
		})
	}
}

func TestInitialPasswordImpersonationCannotChangeTargetOrOwnCredentials(t *testing.T) {
	a := testApp(t)
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"核查首次登录限制"}`, projectID)
	if w.Code != 200 {
		t.Fatalf("impersonation %d %s", w.Code, w.Body.String())
	}
	original := forceInitialPassword(t, a, "u_member")
	w = initialRequest(a, admin, "GET", "/api/session", "", "u_member")
	if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["mustChangePassword"] != true || jsonMap(t, w)["impersonation"] == nil {
		t.Fatal("impersonated identity not gated")
	}
	w = initialRequest(a, admin, "GET", "/api/requirements", "", "u_member")
	if w.Code != 403 {
		t.Fatal("impersonation bypassed gate")
	}
	w = initialRequest(a, admin, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 409 {
		t.Fatal("expected identity not enforced")
	}
	w = initialRequest(a, admin, "POST", "/api/auth/initial-password", initialBody(seedPassword, "Personal2027!"), "u_admin")
	if w.Code != 403 {
		t.Fatalf("impersonation not forbidden %d %s", w.Code, w.Body.String())
	}
	var hash string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_member'`).Scan(&hash)
	if hash != original {
		t.Fatal("target changed")
	}
	w = initialRequest(a, admin, "POST", "/api/auth/impersonation/stop", `{}`, "u_admin")
	if w.Code != 200 {
		t.Fatal("cannot return to administrator")
	}
}

func TestInitialPasswordDefaultsCreationImportApprovalAndMigrationSafety(t *testing.T) {
	t.Setenv("DEVFLOW_INITIAL_PASSWORD", temporaryTestPassword)
	a := testApp(t)
	overview := orgRequest(t, a, "GET", "/api/organization/admin", "u_admin", nil, 200)
	if overview["initialPasswordConfigured"] != true || strings.Contains(fmt.Sprint(overview), temporaryTestPassword) {
		t.Fatal("configuration capability missing or leaks secret")
	}
	member := orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "Temporary Member", "email": "temporary@example.test", "active": false}, 201)
	id := member["id"].(string)
	if member["mustChangePassword"] != true || member["active"] != false {
		t.Fatal("new account unsafe flags")
	}
	var hash string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, id).Scan(&hash)
	if !verifyPassword(temporaryTestPassword, hash) {
		t.Fatal("default not applied")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/"+id, "u_admin", map[string]any{"active": true}, 200)
	w, cookie := loginRequest(a, "temporary@example.test", temporaryTestPassword)
	if w.Code != 200 || cookie == nil {
		t.Fatal("activation/login failed")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/"+id, "u_admin", map[string]any{"initialPassword": "otherB"}, 200)
	w = initialRequest(a, cookie, "GET", "/api/session", "", id)
	if w.Code != 401 {
		t.Fatal("admin reset left old session")
	}
	preview := orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email\nImported,initial-import@example.test\n"}, 200)
	imported := orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 201)
	var required, active bool
	a.db.QueryRow(`SELECT password_hash,must_change_password,active FROM users WHERE id=?`, imported["memberIds"].([]any)[0]).Scan(&hash, &required, &active)
	if !required || active || !verifyPassword(temporaryTestPassword, hash) {
		t.Fatal("CSV did not keep inactive temporary account")
	}
	invite := orgInvitation(t, a, "dept_frontend", 5)
	orgPublic(t, a, "POST", invite["token"].(string), map[string]any{"name": "Applicant", "departmentId": "dept_frontend", "requestedRole": "frontend"}, 202)
	apps := orgRequest(t, a, "GET", "/api/organization/applications", "u_admin", nil, 200)["items"].([]any)
	approved := orgRequest(t, a, "POST", "/api/organization/applications/"+apps[0].(map[string]any)["id"].(string)+"/approve", "u_admin", map[string]any{"email": "initial-applicant@example.test", "projectMemberships": []any{map[string]string{"projectId": projectID, "role": "frontend"}}}, 201)
	a.db.QueryRow(`SELECT password_hash,must_change_password,active FROM users WHERE id=?`, approved["userId"]).Scan(&hash, &required, &active)
	if !required || !active || !verifyPassword(temporaryTestPassword, hash) {
		t.Fatal("approval defaults missing")
	}
	original := forceInitialPassword(t, a, "u_member")
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	a.db.QueryRow(`SELECT password_hash,must_change_password FROM users WHERE id='u_member'`).Scan(&hash, &required)
	if hash != original || !required {
		t.Fatal("migration overwrote credentials")
	}
	a.db.QueryRow(`SELECT must_change_password FROM users WHERE id='u_admin'`).Scan(&required)
	if required {
		t.Fatal("migration marked existing administrator without explicit operation")
	}
	t.Setenv("DEVFLOW_INITIAL_PASSWORD", " bad ")
	orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "Invalid Default", "email": "bad-default@example.test"}, 422)
	orgRequest(t, a, "PATCH", "/api/organization/members/"+id, "u_admin", map[string]any{"mustChangePassword": false}, 422)
}

func TestInitialPasswordConfigurationBoundariesAndUnavailableState(t *testing.T) {
	for _, value := range []string{"short", "bad space", "bad\tvalue", "valid\u200Bvalue", strings.Repeat("a", 73), string([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})} {
		if validateInitialPassword(value) == nil {
			t.Fatal("unsafe temporary format accepted")
		}
	}
	for _, value := range []string{temporaryTestPassword, "中文", strings.Repeat("A", 72)} {
		if err := validateInitialPassword(value); err != nil {
			t.Fatal("valid byte-bounded temporary rejected", err)
		}
	}
	a := testApp(t)
	forceInitialPassword(t, a, "u_member")
	_, cookie := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	if _, err := a.db.Exec(`DROP TABLE auth_initial_password_attempts`); err != nil {
		t.Fatal(err)
	}
	w := initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 503 || strings.Contains(w.Body.String(), "auth_initial_password_attempts") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("storage error leak or false logout %d %s", w.Code, w.Body.String())
	}
	var required bool
	a.db.QueryRow(`SELECT must_change_password FROM users WHERE id='u_member'`).Scan(&required)
	if !required {
		t.Fatal("failed operation cleared guard")
	}
	if _, err := a.db.Exec(`INSERT INTO tenants(id,name)VALUES('foreign','Foreign'); INSERT INTO users(id,tenant_id,name,email,active,must_change_password)VALUES('foreign-user','foreign','Foreign','other@example.test',1,1); INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES('foreign','foreign-user','member','active','now','now')`); err != nil {
		t.Fatal(err)
	}
	w, other := loginRequest(a, "other@example.test", temporaryTestPassword)
	if w.Code != 401 || other != nil {
		t.Fatal("cross-tenant login accepted")
	}
}
