package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func reviewImpersonation(t *testing.T, a *App, target string) *http.Cookie {
	t.Helper()
	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != 200 || cookie == nil {
		t.Fatalf("admin login: %d %s", w.Code, w.Body.String())
	}
	w = impersonationRequest(a, cookie, "POST", "/api/auth/impersonation", jsonText(map[string]string{"userId": target, "reason": "独立权限审查回归"}), projectID)
	if w.Code != 200 {
		t.Fatalf("start impersonation: %d %s", w.Code, w.Body.String())
	}
	return cookie
}

func TestImpersonationReviewLocaleWritesHaveDualIdentityAudit(t *testing.T) {
	a := testApp(t)
	cookie := reviewImpersonation(t, a, "u_member")
	w := impersonationRequest(a, cookie, "PATCH", "/api/preferences/locale", `{"locale":"en-US"}`, "inaccessible-stale-project")
	if w.Code != 200 {
		t.Fatalf("personal locale must not depend on project access: %d %s", w.Code, w.Body.String())
	}
	var target, admin string
	if err := a.db.QueryRow(`SELECT locale FROM users WHERE tenant_id=? AND id='u_member'`, tenantID).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT locale FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if target != "en-US" || admin != "zh-CN" {
		t.Fatalf("locale changed wrong identity: target=%s admin=%s", target, admin)
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonation_actions WHERE tenant_id=? AND admin_user_id='u_admin' AND target_user_id='u_member' AND path='/api/preferences/locale' AND method='PATCH' AND status_code=200`, tenantID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("locale mutation omitted dual-identity audit: count=%d err=%v", count, err)
	}
}

func TestImpersonationReviewRevokedOriginalSessionCannotContinue(t *testing.T) {
	a := testApp(t)
	cookie := reviewImpersonation(t, a, "u_member")
	token, _, err := a.parseSignedSession(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE auth_sessions SET revoked_at=? WHERE token_hash=?`, time.Now().UTC().Format(time.RFC3339), tokenDigest(token)); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{{"GET", "/api/session", ""}, {"GET", "/api/notifications", ""}, {"POST", "/api/requirements", `{"title":"revoked session must not write"}`}, {"POST", "/api/auth/impersonation/stop", `{}`}} {
		w := impersonationRequest(a, cookie, request.method, request.path, request.body, projectID)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("revoked real session still usable at %s: %d %s", request.path, w.Code, w.Body.String())
		}
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE title='revoked session must not write'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unexpected write: %d %v", count, err)
	}
}

func TestImpersonationReviewReadOnlyTargetCannotBorrowAdminRights(t *testing.T) {
	a := testApp(t)
	cookie := reviewImpersonation(t, a, "u_viewer")
	if w := impersonationRequest(a, cookie, "GET", "/api/requirements", "", projectID); w.Code != 200 {
		t.Fatalf("viewer read: %d %s", w.Code, w.Body.String())
	}
	for _, request := range []struct{ method, path, body string }{{"POST", "/api/requirements", `{"title":"viewer must not create"}`}, {"PATCH", "/api/requirements/1", `{"title":"viewer must not edit"}`}, {"POST", "/api/sprints", `{"name":"viewer iteration"}`}, {"POST", "/api/field-definitions", `{}`}} {
		w := impersonationRequest(a, cookie, request.method, request.path, request.body, projectID)
		if w.Code != 403 {
			t.Fatalf("viewer borrowed admin rights at %s: %d %s", request.path, w.Code, w.Body.String())
		}
	}
	var denied int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonation_actions WHERE tenant_id=? AND admin_user_id='u_admin' AND target_user_id='u_viewer' AND status_code=403`, tenantID).Scan(&denied); err != nil || denied != 4 {
		t.Fatalf("denied writes not audited: %d %v", denied, err)
	}
}

func TestImpersonationReviewDemotedAdminCanStopButCannotRestart(t *testing.T) {
	a := testApp(t)
	cookie := reviewImpersonation(t, a, "u_member")
	if _, err := a.db.Exec(`UPDATE tenant_memberships SET role='member' WHERE tenant_id=? AND user_id='u_admin'`, tenantID); err != nil {
		t.Fatal(err)
	}
	w := impersonationRequest(a, cookie, "GET", "/api/session", "", projectID)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "impersonation_expired") {
		t.Fatalf("demoted admin retained impersonation: %d %s", w.Code, w.Body.String())
	}
	w = impersonationRequest(a, cookie, "POST", "/api/auth/impersonation/stop", `{}`, "stale-project")
	if w.Code != 200 {
		t.Fatalf("demotion blocked recovery: %d %s", w.Code, w.Body.String())
	}
	w = impersonationRequest(a, cookie, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"不能恢复代访问权"}`, projectID)
	if w.Code != 403 {
		t.Fatalf("demoted admin restarted impersonation: %d %s", w.Code, w.Body.String())
	}
	var active int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonations WHERE tenant_id=? AND ended_at IS NULL`, tenantID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("recovery left active session: %d %v", active, err)
	}
}
