package main

import (
	"context"
	"net/http"
	"testing"
)

func TestOperationDisabledCanLoginButCannotUseBusinessAPIs(t *testing.T) {
	a := testApp(t)
	_, original := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	if original == nil {
		t.Fatal("initial login failed")
	}
	body := orgRequest(t, a, "PATCH", "/api/organization/members/u_member", "u_admin", map[string]any{"operationDisabled": true}, 200)
	if body["operationDisabled"] != true || body["active"] != true {
		t.Fatalf("business disable changed activation: %+v", body)
	}
	w, fresh := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	if w.Code != 200 || fresh == nil || jsonMap(t, w)["user"].(map[string]any)["operationDisabled"] != true {
		t.Fatalf("disabled user cannot authenticate: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range []*http.Cookie{original, fresh} {
		w = impersonationRequest(a, cookie, "GET", "/api/session", "", "missing-project")
		if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["operationDisabled"] != true || len(jsonMap(t, w)["organizationPermissions"].([]any)) != 0 {
			t.Fatalf("disabled session page unavailable/leaks grants: %d %s", w.Code, w.Body.String())
		}
		for _, endpoint := range []struct{ method, path, body string }{
			{"GET", "/api/requirements", ""}, {"POST", "/api/requirements", `{"title":"禁止写"}`},
			{"GET", "/api/search?q=test", ""}, {"GET", "/api/members", ""}, {"GET", "/api/organization/admin", ""},
			{"GET", "/api/notifications", ""}, {"PATCH", "/api/preferences/locale", `{"locale":"en-US"}`},
			{"GET", "/api/profile/wecom-webhook", ""}, {"GET", "/api/reports/workload", ""},
			{"POST", "/api/auth/impersonation", `{"userId":"u_front","reason":"不能绕过业务禁用"}`},
		} {
			w = impersonationRequest(a, cookie, endpoint.method, endpoint.path, endpoint.body, projectID)
			if w.Code != 403 || jsonMap(t, w)["error"].(map[string]any)["code"] != "account_disabled" || len(w.Result().Cookies()) != 0 {
				t.Fatalf("disabled access/session invalidation: %s %d %s", endpoint.path, w.Code, w.Body.String())
			}
		}
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_member", "u_admin", map[string]any{"operationDisabled": false}, 200)
	w = impersonationRequest(a, original, "GET", "/api/requirements", "", projectID)
	if w.Code != 200 {
		t.Fatalf("reenabling did not restore existing session: %d %s", w.Code, w.Body.String())
	}
	w = impersonationRequest(a, original, "GET", "/api/session", "", projectID)
	if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["operationDisabled"] != false {
		t.Fatalf("reenabled session incorrect: %s", w.Body.String())
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_member", "u_admin", map[string]any{"operationDisabled": true}, 200)
	w = impersonationRequest(a, original, "POST", "/api/auth/logout", `{}`, projectID)
	if w.Code != 200 {
		t.Fatal("disabled user cannot logout")
	}
}

func TestOperationDisabledGovernanceAndActivationRemainSeparate(t *testing.T) {
	a := testApp(t)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_admin", map[string]any{"operationDisabled": true}, 409)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_back", map[string]any{"operationDisabled": true}, 403)
	orgRequest(t, a, "PATCH", "/api/organization/members/missing", "u_admin", map[string]any{"operationDisabled": true}, 404)
	if _, err := a.db.Exec(`INSERT INTO tenants(id,name)VALUES('other-tenant','Other'); INSERT INTO users(id,tenant_id,name,email)VALUES('foreign','other-tenant','Foreign','foreign@example.test'); INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES('other-tenant','foreign','member','active','now','now')`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/foreign", "u_admin", map[string]any{"operationDisabled": true}, 404)
	var disabled bool
	if err := a.db.QueryRow(`SELECT operation_disabled FROM users WHERE id='foreign'`).Scan(&disabled); err != nil || disabled {
		t.Fatal("cross-tenant user modified")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"active": false, "operationDisabled": true}, 200)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"operationDisabled": false}, 200)
	var email string
	a.db.QueryRow(`SELECT email FROM users WHERE id='u_front'`).Scan(&email)
	w, cookie := loginRequest(a, email, seedPassword)
	if w.Code != 401 || cookie != nil {
		t.Fatal("business reenable opened a security-deactivated account")
	}
	// Disabled administrative grants are not usable even in direct helpers.
	orgGroup(t, a, "委派管理", []string{"members.manage"}, []string{"u_back"})
	orgRequest(t, a, "PATCH", "/api/organization/members/u_back", "u_admin", map[string]any{"operationDisabled": true}, 200)
	other := *a
	other.user = "u_back"
	if _, _, err := other.organizationAccess(context.Background(), a.db); err == nil {
		t.Fatal("disabled manager retained direct organization authority")
	}
	if err := a.migrateOperationAccess(); err != nil {
		t.Fatal(err)
	}
	if disabled, err := operationDisabled(context.Background(), a.db, "u_back"); err != nil || !disabled {
		t.Fatal("migration reset business suspension")
	}
}

func TestOperationDisabledDoesNotCountAsAvailableLastAdministrator(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE tenant_memberships SET role='tenant_admin' WHERE user_id='u_pm'; UPDATE users SET operation_disabled=1 WHERE id='u_pm'`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_admin", map[string]any{"tenantRole": "member"}, 409)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_pm", "u_admin", map[string]any{"operationDisabled": false}, 200)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_pm", map[string]any{"operationDisabled": true}, 200)
	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != 200 || cookie == nil {
		t.Fatal("business-disabled admin cannot login")
	}
	w = impersonationRequest(a, cookie, "PATCH", "/api/organization/members/u_pm", `{"operationDisabled":true}`, projectID)
	if w.Code != 403 {
		t.Fatalf("disabled admin modified last usable admin: %d", w.Code)
	}
}
