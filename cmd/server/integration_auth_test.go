package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func securityIntegrationToken(t *testing.T, a *App, user string, scopes []string) (string, string) {
	t.Helper()
	secret, err := integrationRandom("df_")
	if err != nil {
		t.Fatal(err)
	}
	id, err := integrationRandom("it_")
	if err != nil {
		t.Fatal(err)
	}
	var password string
	if err := a.db.QueryRow(`SELECT password_hash FROM users WHERE tenant_id=? AND id=?`, tenantID, user).Scan(&password); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := a.db.Exec(`INSERT INTO integration_tokens(id,tenant_id,project_id,user_id,name,prefix,token_hash,password_fingerprint,scopes_json,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, tenantID, projectID, user, "Security fixture", secret[:11], tokenDigest(secret), tokenDigest(password), jsonText(scopes), now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	return secret, id
}

func securityIntegrationRequest(t *testing.T, a *App, token, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://devflow.test"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet {
		id, err := integrationRandom("security_")
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Idempotency-Key", id)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	withJSON(a.scopedAPI()).ServeHTTP(w, r)
	return w
}

func TestIntegrationAuthenticationRequiresBearerAndCurrentCredentials(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{"revoked", `UPDATE integration_tokens SET revoked_at='revoked' WHERE id=?`},
		{"expired", `UPDATE integration_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`},
		{"password_changed", `UPDATE users SET password_hash=password_hash||'changed' WHERE id='u_member'`},
		{"inactive", `UPDATE users SET active=0 WHERE id='u_member'`},
		{"business_disabled", `UPDATE users SET operation_disabled=1 WHERE id='u_member'`},
		{"initial_password", `UPDATE users SET must_change_password=1 WHERE id='u_member'`},
		{"tenant_membership_removed", `UPDATE tenant_memberships SET status='removed' WHERE user_id='u_member'`},
		{"project_membership_removed", `DELETE FROM project_members WHERE user_id='u_member' AND project_id='prj_orbit'`},
		{"project_archived", `UPDATE projects SET status='archived' WHERE id='prj_orbit'`},
		{"token_tenant_changed", `UPDATE integration_tokens SET tenant_id='foreign-tenant' WHERE id=?`},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := testApp(t)
			token, id := securityIntegrationToken(t, a, "u_member", integrationReadScopes)
			if w := securityIntegrationRequest(t, a, token, "GET", "/api/open/v1/me", "", nil); w.Code != 200 {
				t.Fatalf("valid token rejected: %d %s", w.Code, w.Body.String())
			}
			var err error
			if strings.Contains(test.statement, "?") {
				_, err = a.db.Exec(test.statement, id)
			} else {
				_, err = a.db.Exec(test.statement)
			}
			if err != nil {
				t.Fatal(err)
			}
			w := securityIntegrationRequest(t, a, token, "GET", "/api/open/v1/me", "", nil)
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
				t.Fatalf("revoked authority remained usable: %d %s", w.Code, w.Body.String())
			}
		})
	}
	a := testApp(t)
	token, _ := securityIntegrationToken(t, a, "u_admin", integrationReadScopes)
	for _, headers := range []map[string]string{
		nil,
		{"Authorization": "Bearer invalid"},
		{"Authorization": "Basic " + token},
		{"X-DevFlow-Expected-User": "u_admin", "X-User-ID": "u_admin"},
	} {
		w := securityIntegrationRequest(t, a, "", "GET", "/api/open/v1/me?access_token="+token, "", headers)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("non-Bearer identity bypassed authentication: %d", w.Code)
		}
	}
	_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if cookie == nil {
		t.Fatal("login fixture failed")
	}
	r := httptest.NewRequest(http.MethodGet, "http://devflow.test/api/open/v1/me", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("browser cookie authenticated external API: %d", w.Code)
	}
}

func TestIntegrationProjectBindingAndScopeCannotBeOverridden(t *testing.T) {
	a := testApp(t)
	token, _ := securityIntegrationToken(t, a, "u_admin", integrationReadScopes)
	res, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,created_at,updated_at)
		VALUES(?,?,?,?,?,?)`, tenantID, insightProjectID, "SEC-FOREIGN", "Must not leak", "2026-09-05T00:00:00Z", "2026-09-05T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	foreignID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, body string
		headers            map[string]string
		status             int
	}{
		{"GET", "/api/open/v1/requirements", "", map[string]string{"X-DevFlow-Project": insightProjectID}, 403},
		{"GET", fmt.Sprintf("/api/open/v1/requirements/%d", foreignID), "", nil, 404},
		{"GET", "/api/open/v1/requirements?projectId=" + insightProjectID, "", nil, 400},
		{"POST", "/api/open/v1/requirements", `{"title":"Denied write"}`, nil, 403},
		{"GET", "/api/open/v1/executions", "", nil, 403},
		{"POST", "/api/open/v1/requirements/1/comments", `{"body":"Denied comment"}`, nil, 403},
		{"DELETE", "/api/open/v1/requirements/1", "", nil, 404},
		{"GET", "/api/open/v1/requirements/1/attachments", "", nil, 404},
		{"GET", "/api/open/v1/requirements/1/comments/extra", "", nil, 404},
		{"GET", "/api/open/v1/organization/members", "", nil, 404},
		{"POST", "/api/integrations/tokens", `{"name":"Should not mint"}`, nil, 401},
	} {
		w := securityIntegrationRequest(t, a, token, test.method, test.path, test.body, test.headers)
		if w.Code != test.status || strings.Contains(w.Body.String(), "Must not leak") {
			t.Fatalf("scope boundary %s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
	}
	writeToken, _ := securityIntegrationToken(t, a, "u_admin", append(append([]string{}, integrationReadScopes...), "requirements:write"))
	for _, field := range []string{"tenantId", "projectId", "createdBy", "id", "code"} {
		w := securityIntegrationRequest(t, a, writeToken, "POST", "/api/open/v1/requirements", jsonText(map[string]any{"title": "Protected", field: "override"}), nil)
		if w.Code != 422 {
			t.Fatalf("server-owned %s accepted: %d %s", field, w.Code, w.Body.String())
		}
	}
}

func TestIntegrationWritesRespectLiveProjectRoleAndSprintManagement(t *testing.T) {
	a := testApp(t)
	token, _ := securityIntegrationToken(t, a, "u_member", append(append([]string{}, integrationReadScopes...), "requirements:write", "iterations:write"))
	if _, err := a.db.Exec(`UPDATE project_members SET role='viewer' WHERE user_id='u_member' AND project_id='prj_orbit'; UPDATE memberships SET role='viewer' WHERE user_id='u_member' AND project_id='prj_orbit'`); err != nil {
		t.Fatal(err)
	}
	if w := securityIntegrationRequest(t, a, token, "GET", "/api/open/v1/requirements/1", "", nil); w.Code != 200 {
		t.Fatalf("viewer token cannot read: %d %s", w.Code, w.Body.String())
	}
	if w := securityIntegrationRequest(t, a, token, "POST", "/api/open/v1/requirements", `{"title":"Viewer token write"}`, nil); w.Code != 403 {
		t.Fatalf("token kept old write role: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE project_members SET role='backend' WHERE user_id='u_member' AND project_id='prj_orbit'; UPDATE memberships SET role='backend' WHERE user_id='u_member' AND project_id='prj_orbit'`); err != nil {
		t.Fatal(err)
	}
	const sprint = `{"name":"Role protected iteration","startDate":"2026-09-05","endDate":"2026-09-10"}`
	if w := securityIntegrationRequest(t, a, token, "POST", "/api/open/v1/iterations", sprint, nil); w.Code != 403 || !strings.Contains(w.Body.String(), "sprint_manager_required") {
		t.Fatalf("rewritten path bypassed sprint role: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE project_members SET role='product' WHERE user_id='u_member' AND project_id='prj_orbit'; UPDATE memberships SET role='product' WHERE user_id='u_member' AND project_id='prj_orbit'`); err != nil {
		t.Fatal(err)
	}
	if w := securityIntegrationRequest(t, a, token, "POST", "/api/open/v1/iterations", sprint, nil); w.Code != 201 {
		t.Fatalf("new live manager role was not honored: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationOriginRespectsConfiguredHTTPSAndRejectsSpoofedForwarding(t *testing.T) {
	a := testApp(t)
	token, _ := securityIntegrationToken(t, a, "u_admin", integrationReadScopes)
	for _, test := range []struct {
		name    string
		secure  bool
		headers map[string]string
		status  int
	}{
		{"non_browser", false, nil, 200},
		{"local_same_origin", false, map[string]string{"Origin": "http://devflow.test", "Sec-Fetch-Site": "same-origin"}, 200},
		{"cross_site", false, map[string]string{"Origin": "http://attacker.test"}, 403},
		{"cross_site_fetch", false, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"null_origin", false, map[string]string{"Origin": "null"}, 403},
		{"origin_credentials", false, map[string]string{"Origin": "http://attacker@devflow.test"}, 403},
		{"origin_path", false, map[string]string{"Origin": "http://devflow.test/path"}, 403},
		{"forwarded_proto_spoof", false, map[string]string{"Origin": "https://devflow.test", "X-Forwarded-Proto": "https"}, 403},
		{"trusted_tls_termination", true, map[string]string{"Origin": "https://devflow.test", "Sec-Fetch-Site": "same-origin"}, 200},
		{"secure_origin_downgrade", true, map[string]string{"Origin": "http://devflow.test", "X-Forwarded-Proto": "http"}, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			a.cookieSecure = test.secure
			w := securityIntegrationRequest(t, a, token, "GET", "/api/open/v1/me", "", test.headers)
			if w.Code != test.status {
				t.Fatalf("origin policy mismatch: %d %s", w.Code, w.Body.String())
			}
		})
	}
	// Cookie-authenticated credential management follows the same proxy rule.
	a.cookieSecure = true
	_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if cookie == nil {
		t.Fatal("login fixture failed")
	}
	r := httptest.NewRequest(http.MethodGet, "http://devflow.test/api/integrations", nil)
	r.Header.Set("Origin", "https://devflow.test")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("HTTPS reverse proxy blocked browser credential management: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationCredentialsArePersonalAndNeverListedInPlaintext(t *testing.T) {
	a := testApp(t)
	adminToken, adminID := securityIntegrationToken(t, a, "u_admin", integrationReadScopes)
	memberToken, memberID := securityIntegrationToken(t, a, "u_member", integrationReadScopes)
	_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if cookie == nil {
		t.Fatal("login fixture failed")
	}
	w := impersonationRequest(a, cookie, "GET", "/api/integrations", "", projectID)
	if w.Code != 200 || strings.Contains(w.Body.String(), adminToken) || strings.Contains(w.Body.String(), memberToken) || strings.Contains(w.Body.String(), memberID) || !strings.Contains(w.Body.String(), adminID) {
		t.Fatalf("personal credential list disclosed secrets or another owner's keys: %d", w.Code)
	}
	w = impersonationRequest(a, cookie, "DELETE", "/api/integrations/tokens/"+memberID, "", projectID)
	if w.Code != 404 {
		t.Fatalf("administrator revoked another owner's personal token through self-service: %d", w.Code)
	}
	if w := impersonationRequest(a, cookie, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"Integration security fixture"}`, projectID); w.Code != 200 {
		t.Fatalf("impersonation fixture failed: %d %s", w.Code, w.Body.String())
	}
	w = impersonationRequest(a, cookie, "GET", "/api/integrations", "", projectID)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "impersonation_forbidden") {
		t.Fatalf("impersonation gained token management access: %d %s", w.Code, w.Body.String())
	}
}
