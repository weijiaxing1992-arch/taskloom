package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loginRequest(a *App, email, password string) (*httptest.ResponseRecorder, *http.Cookie) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"email":"`+email+`","password":"`+password+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			session = cookie
		}
	}
	return w, session
}

func TestProtectedAPIRequiresServerSession(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.Header.Set("X-DevFlow-User", "u_admin")
	r.Header.Set("X-DevFlow-Project", projectID)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("forged identity header reached protected API: %d %s", w.Code, w.Body.String())
	}

	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Value == "" {
		t.Fatalf("session cookie flags are unsafe: %#v", cookie)
	}
	parts := strings.Split(cookie.Value, ".")
	var rawTokenRows, digestRows int
	a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash=?`, parts[1]).Scan(&rawTokenRows)
	a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash=?`, tokenDigest(parts[1])).Scan(&digestRows)
	if rawTokenRows != 0 || digestRows != 1 {
		t.Fatalf("server session did not store only the token digest: raw=%d digest=%d", rawTokenRows, digestRows)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"u_admin"`) {
		t.Fatalf("signed session did not establish principal: %d %s", w.Code, w.Body.String())
	}
}

func TestLoginRejectsBadPasswordAndLogoutRevokesSession(t *testing.T) {
	a := testApp(t)
	w, cookie := loginRequest(a, "linxia@devflow.local", "wrong-password")
	if w.Code != http.StatusUnauthorized || cookie != nil {
		t.Fatalf("bad password unexpectedly logged in: %d %s", w.Code, w.Body.String())
	}
	w, cookie = loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", bytes.NewBufferString(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("logout failed: %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session remained valid: %d %s", w.Code, w.Body.String())
	}
}

func TestSignedCookieTamperingAndOrganizationSeed(t *testing.T) {
	a := testApp(t)
	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	cookie.Value += "tampered"
	r := httptest.NewRequest(http.MethodGet, "/api/organization/directory", nil)
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie was accepted: %d %s", w.Code, w.Body.String())
	}

	w = apiRequest(a, http.MethodGet, "/api/organization/directory", "u_admin", projectID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enterprise"`) || !strings.Contains(w.Body.String(), `"name":"产品研发中心"`) || !strings.Contains(w.Body.String(), `"departmentId":"dept_backend"`) {
		t.Fatalf("organization hierarchy seed missing: %d %s", w.Code, w.Body.String())
	}
	if err := a.migrateAuth(); err != nil {
		t.Fatal(err)
	}
	var departments, memberships int
	a.db.QueryRow(`SELECT COUNT(*) FROM departments WHERE tenant_id=?`, tenantID).Scan(&departments)
	a.db.QueryRow(`SELECT COUNT(*) FROM department_memberships WHERE tenant_id=?`, tenantID).Scan(&memberships)
	if departments != 8 || memberships != 11 {
		t.Fatalf("auth migration is not idempotent: departments=%d memberships=%d", departments, memberships)
	}
}

func TestOrganizationDirectoryRequiresEnterpriseReadPermission(t *testing.T) {
	a := testApp(t)
	// u_viewer 可以访问默认项目，但没有企业目录权限；目录不能再通过
	// X-DevFlow-Project 的普通成员校验而泄露全企业邮箱、工号和部门信息。
	w := apiRequest(a, http.MethodGet, "/api/organization/directory", "u_viewer", projectID, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("project member accessed enterprise directory: %d %s", w.Code, w.Body.String())
	}
	orgGroup(t, a, "目录只读", []string{"organization.read"}, []string{"u_viewer"})
	// 企业目录权限不依赖过期、无权或另一项目的选择器，避免把 UI 状态当授权条件。
	w = apiRequest(a, http.MethodGet, "/api/organization/directory", "u_viewer", "unrelated-project", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enterprise"`) || !strings.Contains(w.Body.String(), `"email"`) {
		t.Fatalf("organization.read could not access enterprise directory: %d %s", w.Code, w.Body.String())
	}
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	a := testApp(t)
	w, current := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || current == nil {
		t.Fatalf("first login failed: %d %s", w.Code, w.Body.String())
	}
	w, other := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || other == nil {
		t.Fatalf("second login failed: %d %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodPost, "/api/profile/password", bytes.NewBufferString(`{"currentPassword":"TaskLoom2026!","newPassword":"Rotated2027!","confirmPassword":"Rotated2027!"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(current)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("password change failed: %d %s", w.Code, w.Body.String())
	}

	for name, testCase := range map[string]struct {
		cookie *http.Cookie
		status int
	}{"current": {current, http.StatusOK}, "other": {other, http.StatusUnauthorized}} {
		r = httptest.NewRequest(http.MethodGet, "/api/session", nil)
		r.Header.Set("X-DevFlow-Project", projectID)
		r.AddCookie(testCase.cookie)
		w = httptest.NewRecorder()
		a.scopedAPI().ServeHTTP(w, r)
		if w.Code != testCase.status {
			t.Fatalf("%s session status=%d, want %d: %s", name, w.Code, testCase.status, w.Body.String())
		}
	}
}
