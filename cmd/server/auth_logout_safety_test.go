package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logoutSafetyRequest(a *App, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}

func logoutSafetySessionStatus(a *App, cookie *http.Cookie) int {
	r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w.Code
}

func TestLogoutRevocationFailurePreservesCookieForRetry(t *testing.T) {
	a := testApp(t)
	w, current := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || current == nil {
		t.Fatalf("fixture login failed: %d", w.Code)
	}
	w, other := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || other == nil {
		t.Fatalf("second fixture login failed: %d", w.Code)
	}
	// 故障只注入测试库，验证服务端撤销失败不能被伪装为退出成功。
	if _, err := a.db.Exec(`CREATE TRIGGER logout_fixture_failure BEFORE UPDATE OF revoked_at ON auth_sessions BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = logoutSafetyRequest(a, current)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("failed revocation falsely cleared session: status=%d cookieChanged=%v", w.Code, w.Header().Get("Set-Cookie") != "")
	}
	if strings.Contains(w.Body.String(), "private fixture") || strings.Contains(w.Body.String(), `"loggedOut":true`) {
		t.Fatalf("revocation failure leaked details or claimed success: %s", w.Body.String())
	}
	if status := logoutSafetySessionStatus(a, current); status != http.StatusOK {
		t.Fatalf("fixture did not retain the session after failed revocation: %d", status)
	}
	if _, err := a.db.Exec(`DROP TRIGGER logout_fixture_failure`); err != nil {
		t.Fatal(err)
	}
	w = logoutSafetyRequest(a, current)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"loggedOut":true`) {
		t.Fatalf("logout retry failed: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge != -1 || !cookies[0].HttpOnly {
		t.Fatal("successful logout did not expire its local session cookie safely")
	}
	if status := logoutSafetySessionStatus(a, current); status != http.StatusUnauthorized {
		t.Fatalf("logout retry left its session usable: %d", status)
	}
	if status := logoutSafetySessionStatus(a, other); status != http.StatusOK {
		t.Fatalf("logout revoked an unrelated session: %d", status)
	}
	if w = logoutSafetyRequest(a, current); w.Code != http.StatusOK {
		t.Fatalf("already revoked logout was not idempotent: %d", w.Code)
	}
}

func TestLogoutCanceledRevocationDoesNotClaimSuccess(t *testing.T) {
	a := testApp(t)
	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("fixture login failed: %d", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil).WithContext(ctx)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.logout(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("canceled revocation falsely succeeded: status=%d cookieChanged=%v", w.Code, w.Header().Get("Set-Cookie") != "")
	}
	if status := logoutSafetySessionStatus(a, cookie); status != http.StatusOK {
		t.Fatalf("canceled request unexpectedly changed its session: %d", status)
	}
}

func TestLogoutMissingAndInvalidCookieNeedsNoDatabase(t *testing.T) {
	a := testApp(t)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, cookie := range []*http.Cookie{nil, {Name: sessionCookieName, Value: "invalid-fixture-cookie"}} {
		w := logoutSafetyRequest(a, cookie)
		if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") == "" {
			t.Fatalf("invalid local session could not be cleared: %d", w.Code)
		}
	}
}
