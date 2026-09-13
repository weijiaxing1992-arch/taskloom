package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func impersonationRequest(a *App, cookie *http.Cookie, method, path, body, project string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-TaskLoom-Project", project)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}
func TestImpersonationPermissionAndAudit(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	w := impersonationRequest(a, member, "POST", "/api/auth/impersonation", `{"userId":"u_admin","reason":"核对通知记录"}`, projectID)
	if w.Code != 403 {
		t.Fatalf("member escalated: %d %s", w.Code, w.Body)
	}
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"核对通知记录"}`, projectID)
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("must not issue a member credential")
	}
	w = impersonationRequest(a, admin, "GET", "/api/session", "", projectID)
	body := jsonMap(t, w)
	if w.Code != 200 || body["user"].(map[string]any)["id"] != "u_member" || body["impersonation"].(map[string]any)["adminId"] != "u_admin" || body["canImpersonate"] != false {
		t.Fatalf("effective session: %d %s", w.Code, w.Body)
	}
	w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_pm","reason":"再次切换成员"}`, projectID)
	if w.Code != 409 {
		t.Fatalf("nested allowed: %d", w.Code)
	}
	for _, path := range []string{"/api/profile/password", "/api/profile", "/api/members", "/api/projects"} {
		w = impersonationRequest(a, admin, "PATCH", path, `{}`, projectID)
		if w.Code != 403 {
			t.Fatalf("security mutation %s: %d %s", path, w.Code, w.Body)
		}
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonation_actions WHERE admin_user_id='u_admin' AND target_user_id='u_member' AND path='/api/session' AND status_code=200`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing dual-identity audit: %d %v", count, err)
	}
	// Another tab's prior identity cannot accidentally perform member writes.
	r := httptest.NewRequest("POST", "/api/requirements", strings.NewReader(`{"title":"must not exist"}`))
	r.AddCookie(admin)
	r.Header.Set("X-TaskLoom-Expected-User", "u_admin")
	r.Header.Set("X-TaskLoom-Project", projectID)
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "identity_changed") {
		t.Fatalf("stale tab not stopped: %d %s", w.Code, w.Body)
	}
	w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation/stop", `{}`, "stale-project")
	if w.Code != 200 {
		t.Fatalf("return must bypass target scope: %d %s", w.Code, w.Body)
	}
	w = impersonationRequest(a, admin, "GET", "/api/session", "", projectID)
	if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["id"] != "u_admin" {
		t.Fatalf("return: %d %s", w.Code, w.Body)
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action IN ('impersonation_started','impersonation_stopped') AND actor_id='u_admin' AND object_id='u_member'`).Scan(&count)
	if count != 2 {
		t.Fatalf("start/stop audit=%d", count)
	}
}

func TestImpersonationPendingInitialPasswordUsesReadOnlyReview(t *testing.T) {
	a := testApp(t)
	forceInitialPassword(t, a, "u_member")
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)

	// The administrator can select another project, but u_member cannot. The
	// review must persist the verified target project rather than the stale
	// browser header so the landing page is immediately usable.
	w := impersonationRequest(a, admin, http.MethodPost, "/api/auth/impersonation", `{"userId":"u_member","reason":"核查待改密账号可见范围"}`, insightProjectID)
	started := jsonMap(t, w)
	if w.Code != http.StatusOK || started["readOnly"] != true || started["projectId"] != projectID {
		t.Fatalf("read-only review did not start safely: %d %s", w.Code, w.Body.String())
	}
	var storedProject string
	var storedReadOnly bool
	if err := a.db.QueryRow(`SELECT project_id,read_only FROM auth_impersonations WHERE tenant_id=? AND session_hash=(SELECT token_hash FROM auth_sessions WHERE user_id='u_admin' ORDER BY created_at DESC LIMIT 1)`, tenantID).Scan(&storedProject, &storedReadOnly); err != nil || storedProject != projectID || !storedReadOnly {
		t.Fatalf("review context was not stored against the target project: project=%q readonly=%v err=%v", storedProject, storedReadOnly, err)
	}
	w = impersonationRequest(a, admin, http.MethodGet, "/api/session", "", projectID)
	session := jsonMap(t, w)
	user, userOK := session["user"].(map[string]any)
	impersonation, impersonationOK := session["impersonation"].(map[string]any)
	if !userOK || !impersonationOK {
		t.Fatalf("pending-password review did not expose an effective review session: %d %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusOK || user["id"] != "u_member" || user["mustChangePassword"] != false || impersonation["readOnly"] != true {
		t.Fatalf("pending-password review was sent to the first-password screen: %d %s", w.Code, w.Body.String())
	}
	if w = impersonationRequest(a, admin, http.MethodGet, "/api/requirements", "", projectID); w.Code != http.StatusOK {
		t.Fatalf("read-only review cannot load target work: %d %s", w.Code, w.Body.String())
	}
	// Notifications may belong to another project. Opening one first records a
	// visit, which is the sole non-read exception in review mode. It remains
	// bound to the target member's project membership and is audited just like
	// every other delegated request.
	if w = impersonationRequest(a, admin, http.MethodPost, "/api/projects/"+projectID+"/visit", `{}`, projectID); w.Code != http.StatusOK {
		t.Fatalf("read-only review cannot enter the target member's project: %d %s", w.Code, w.Body.String())
	}
	if w = impersonationRequest(a, admin, http.MethodPost, "/api/projects/"+insightProjectID+"/visit", `{}`, projectID); w.Code != http.StatusForbidden {
		t.Fatalf("read-only review used administrator project access: %d %s", w.Code, w.Body.String())
	}

	// No business mutation, notification state, or personal display preference
	// can be changed in this mode. The router denies these before any handler
	// sees their payload, including endpoints that are normally personal writes.
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/requirements", `{"title":"must not be created"}`},
		{http.MethodPatch, "/api/notifications/999999", `{"read":true}`},
		{http.MethodPatch, "/api/preferences/display", `{"fontSize":"large"}`},
	} {
		w = impersonationRequest(a, admin, request.method, request.path, request.body, projectID)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"impersonation_read_only"`) {
			t.Fatalf("read-only review allowed %s: %d %s", request.path, w.Code, w.Body.String())
		}
	}
	var actions int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonation_actions WHERE tenant_id=? AND admin_user_id='u_admin' AND target_user_id='u_member' AND status_code=403`, tenantID).Scan(&actions); err != nil || actions != 4 {
		t.Fatalf("denied read-only writes lost dual-identity audit: count=%d err=%v", actions, err)
	}
	var stillRequired bool
	if err := a.db.QueryRow(`SELECT must_change_password FROM users WHERE tenant_id=? AND id='u_member'`, tenantID).Scan(&stillRequired); err != nil || !stillRequired {
		t.Fatalf("review changed the member credential boundary: required=%v err=%v", stillRequired, err)
	}

	w = impersonationRequest(a, admin, http.MethodPost, "/api/auth/impersonation/stop", `{}`, insightProjectID)
	if w.Code != http.StatusOK || jsonMap(t, w)["projectId"] != projectID {
		t.Fatalf("administrator could not return from read-only review: %d %s", w.Code, w.Body.String())
	}
}

func TestImpersonationExpiresAndReturnsWithoutPrivilegeChange(t *testing.T) {
	for _, mode := range []string{"expired", "target_disabled", "admin_demoted", "target_project_revoked"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
			if w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"协助测试权限"}`, projectID); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			switch mode {
			case "expired":
				a.db.Exec(`UPDATE auth_impersonations SET expires_at=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
			case "target_disabled":
				a.db.Exec(`UPDATE users SET active=0 WHERE id='u_member'`)
			case "admin_demoted":
				a.db.Exec(`UPDATE tenant_memberships SET role='member' WHERE user_id='u_admin'`)
			case "target_project_revoked":
				a.db.Exec(`DELETE FROM project_members WHERE user_id='u_member' AND project_id=?`, projectID)
			}
			w := impersonationRequest(a, admin, "POST", "/api/requirements", `{"title":"should fail"}`, projectID)
			if w.Code != 403 {
				t.Fatalf("unsafe continuation: %d %s", w.Code, w.Body)
			}
			w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation/stop", `{}`, "stale-project")
			if w.Code != 200 {
				t.Fatalf("cannot safely return: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestImpersonationValidationAndTransactionalStart(t *testing.T) {
	a := testApp(t)
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	for _, body := range []string{`{"userId":"u_admin","reason":"不能切换自己"}`, `{"userId":"u_member","reason":"短"}`} {
		if w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", body, projectID); w.Code != 422 {
			t.Fatalf("validation: %d %s", w.Code, w.Body)
		}
	}
	w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"foreign","reason":"不可访问账号"}`, projectID)
	if w.Code != 404 {
		t.Fatalf("unknown target: %d", w.Code)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER fail_impersonation_audit BEFORE INSERT ON audit_logs WHEN NEW.action='impersonation_started' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"事务回滚检查"}`, projectID)
	if w.Code != 503 {
		t.Fatalf("fault: %d %s", w.Code, w.Body)
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM auth_impersonations`).Scan(&count)
	if count != 0 {
		t.Fatalf("partial session=%d", count)
	}
}

func TestImpersonationUsesTargetNotificationInbox(t *testing.T) {
	a := testApp(t)
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	a.stationNotify(projectID, "u_member", "u_admin", "requirement.description_mentioned", "requirement", 1, "你在需求正文中被提及", "@周屿 请确认", "impersonation-inbox-check")
	if w := impersonationRequest(a, admin, "GET", "/api/notifications?eventType=requirement.description_mentioned", "", projectID); strings.Contains(w.Body.String(), "请确认") {
		t.Fatal("admin sees another inbox before switch")
	}
	w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_member","reason":"核对提及通知"}`, projectID)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = impersonationRequest(a, admin, "GET", "/api/notifications?eventType=requirement.description_mentioned", "", projectID)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "请确认") {
		t.Fatalf("member inbox unavailable: %d %s", w.Code, w.Body)
	}
}
