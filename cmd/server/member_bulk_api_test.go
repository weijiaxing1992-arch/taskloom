package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bulkFixtureExec(t *testing.T, a *App, query string, args ...any) {
	t.Helper()
	if _, err := a.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func bulkFixtureCount(t *testing.T, a *App, query string, args ...any) int {
	t.Helper()
	var count int
	if err := a.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func bulkCall(t *testing.T, a *App, actor, action string, ids []string, webhook any, status int) map[string]any {
	t.Helper()
	body := map[string]any{"action": action, "userIds": ids}
	if webhook != nil {
		body["webhook"] = webhook
	}
	return orgRequest(t, a, "POST", "/api/organization/members/bulk", actor, body, status)
}

func TestMemberBulkActivationDeactivationAndSessionInvalidation(t *testing.T) {
	a := testApp(t)
	cookie, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "u_front")
	if err != nil {
		t.Fatal(err)
	}
	bulkFixtureExec(t, a, `INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at) VALUES(?,'bulk-fixture','u_admin','u_front',?,'isolated fixture',?,?)`, tenantID, projectID, orgNow(), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	beforeRole := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE tenant_id=? AND user_id IN ('u_front','u_back')`, tenantID)
	payload := bulkCall(t, a, "u_admin", "deactivate", []string{"u_front", "u_back", "u_front"}, nil, 200)
	if payload["affected"] != float64(2) || jsonText(payload["userIds"]) != `["u_front","u_back"]` {
		t.Fatalf("incorrect deduplication: %v", payload)
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id IN ('u_front','u_back') AND active=0`) != 2 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM tenant_memberships WHERE user_id IN ('u_front','u_back') AND status='disabled'`) != 2 {
		t.Fatal("targets not deactivated")
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM auth_impersonations WHERE session_hash='bulk-fixture' AND ended_at IS NOT NULL`) != 1 {
		t.Fatal("impersonation survived deactivation")
	}
	r := httptest.NewRequest("GET", "/api/session", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("old session survived: %d", w.Code)
	}
	bulkFixtureExec(t, a, `UPDATE users SET operation_disabled=1 WHERE id='u_front'`)
	bulkCall(t, a, "u_admin", "activate", []string{"u_front", "u_back"}, nil, 200)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id IN ('u_front','u_back') AND active=1 AND operation_disabled=0`) != 2 {
		t.Fatal("activation did not clear operation suspension")
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE tenant_id=? AND user_id IN ('u_front','u_back')`, tenantID) != beforeRole {
		t.Fatal("activation changed project assignments")
	}
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("reactivation restored revoked cookie")
	}
	bulkCall(t, a, "u_admin", "activate", []string{"u_front"}, nil, 200) // no-op is still one processed target
}

func TestMemberBulkPermissionsProtectionAndValidation(t *testing.T) {
	a := testApp(t)
	bulkCall(t, a, "u_viewer", "activate", []string{"u_front"}, nil, 403)
	orgGroup(t, a, "Bulk delegated manager", []string{"members.manage"}, []string{"u_viewer"})
	bulkCall(t, a, "u_viewer", "activate", []string{"u_front"}, nil, 200)
	bulkCall(t, a, "u_viewer", "delete", []string{"u_front"}, nil, 403)
	orgGroup(t, a, "Bulk protected manager", []string{"organization.read"}, []string{"u_back"})
	for _, action := range []string{"activate", "deactivate"} {
		bulkCall(t, a, "u_viewer", action, []string{"u_front", "u_back"}, nil, 403)
	}
	bulkCall(t, a, "u_viewer", "wecom-config", []string{"u_back"}, map[string]any{"enabled": false}, 403)
	bulkCall(t, a, "u_viewer", "activate", []string{"u_admin"}, nil, 403)
	for _, action := range []string{"deactivate", "delete"} {
		bulkCall(t, a, "u_admin", action, []string{"u_front", "u_admin"}, nil, 409)
	}
	// Protect any administrator, not just the current or last one.
	bulkFixtureExec(t, a, `UPDATE tenant_memberships SET role='tenant_admin' WHERE user_id='u_member'`)
	bulkCall(t, a, "u_admin", "deactivate", []string{"u_member"}, nil, 409)
	bulkCall(t, a, "u_admin", "delete", []string{"u_member"}, nil, 409)
	bulkCall(t, a, "u_viewer", "deactivate", []string{"u_viewer"}, nil, 409)
	bulkFixtureExec(t, a, `INSERT INTO tenants(id,name) VALUES('bulk-foreign','Other'); INSERT INTO users(id,tenant_id,name,email) VALUES('bulk-foreign-user','bulk-foreign','Other','foreign-bulk@example.com'); INSERT INTO tenant_memberships VALUES('bulk-foreign','bulk-foreign-user','member','active',?,?)`, orgNow(), orgNow())
	for _, id := range []string{"missing", "bulk-foreign-user"} {
		bulkCall(t, a, "u_admin", "deactivate", []string{"u_front", id}, nil, 404)
	}
	if bulkFixtureCount(t, a, `SELECT active FROM users WHERE id='u_front'`) != 1 {
		t.Fatal("invalid batch changed valid first target")
	}
	for _, body := range []any{
		map[string]any{"action": "activate", "userIds": []string{}},
		map[string]any{"action": "unsupported", "userIds": []string{"u_front"}},
		map[string]any{"action": "activate", "userIds": []string{" u_front"}},
		map[string]any{"action": "activate", "userIds": []string{"u_front"}, "webhook": map[string]any{"enabled": true}},
		map[string]any{"action": "wecom-config", "userIds": []string{"u_front"}},
		map[string]any{"action": "wecom-config", "userIds": []string{"u_front"}, "webhook": map[string]any{}},
		map[string]any{"action": "activate", "userIds": []string{"u_front"}, "tenantId": "bulk-foreign"},
	} {
		orgRequest(t, a, "POST", "/api/organization/members/bulk", "u_admin", body, 422)
	}
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = "u_front"
	}
	bulkCall(t, a, "u_admin", "activate", ids, nil, 422)
	orgRequest(t, a, "GET", "/api/organization/members/bulk", "u_admin", nil, 405)
}

func TestMemberBulkDeletePreservesHistoryAndRollsBack(t *testing.T) {
	for _, action := range []string{"deactivate", "delete"} {
		t.Run(action, func(t *testing.T) {
			a := wecomApp(t)
			setTestWebhook(t, a)
			notice := insertWecomNotice(t, a)
			before := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM requirements`)
			auditAction := "organization_member_saved"
			if action == "delete" {
				auditAction = "organization_member_removed"
			}
			bulkFixtureExec(t, a, fmt.Sprintf(`CREATE TRIGGER bulk_fail_second_audit BEFORE INSERT ON audit_logs WHEN NEW.action='%s' AND NEW.object_id='u_back' BEGIN SELECT RAISE(ABORT,'isolated test failure'); END`, auditAction))
			bulkCall(t, a, "u_admin", action, []string{"u_front", "u_back"}, nil, 503)
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id IN ('u_front','u_back') AND active=1`) != 2 {
				t.Fatal("partial state committed")
			}
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM user_wecom_webhooks WHERE user_id='u_front'`) != 1 {
				t.Fatal("rollback lost webhook")
			}
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs WHERE action=?`, auditAction) != 0 {
				t.Fatal("partial audit committed")
			}
			bulkFixtureExec(t, a, `DROP TRIGGER bulk_fail_second_audit`)
			bulkCall(t, a, "u_admin", action, []string{"u_front", "u_back"}, nil, 200)
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users WHERE id IN ('u_front','u_back')`) != 2 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM requirements`) != before || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM user_notifications WHERE id=?`, notice) != 1 {
				t.Fatal("history deleted")
			}
			if action == "delete" {
				if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM tenant_memberships WHERE user_id IN ('u_front','u_back') AND status='removed'`) != 2 {
					t.Fatal("not soft removed")
				}
				for _, table := range []string{"memberships", "project_members", "department_memberships", "organization_group_members", "user_wecom_webhooks"} {
					if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND user_id IN ('u_front','u_back')`, tenantID) != 0 {
						t.Fatalf("authorization retained: %s", table)
					}
				}
				bulkCall(t, a, "u_admin", "activate", []string{"u_front"}, nil, 404)
			}
		})
	}
}

func TestMemberBulkWecomIsEncryptedAtomicAndDoesNotSend(t *testing.T) {
	a := wecomApp(t)
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("configuration sent an external message")
		return nil, nil
	})}
	bulkCall(t, a, "u_admin", "wecom-config", []string{"u_front"}, map[string]any{"url": testWebhookURL, "enabled": true}, 200)
	notice := insertWecomNotice(t, a)
	bulkCall(t, a, "u_admin", "wecom-config", []string{"u_front", "u_back"}, map[string]any{"enabled": true}, 422)
	if bulkFixtureCount(t, a, `SELECT version FROM user_wecom_webhooks WHERE user_id='u_front'`) != 1 || deliveryStatus(t, a, notice) != "pending" {
		t.Fatal("failed enable advanced webhook generation or consumed delivery")
	}
	for _, raw := range []string{"https://127.0.0.1/private", testWebhookURL + "&key=duplicate", "http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=01234567890123456789"} {
		bulkCall(t, a, "u_admin", "wecom-config", []string{"u_front", "u_back"}, map[string]any{"url": raw}, 422)
	}
	payload := bulkCall(t, a, "u_admin", "wecom-config", []string{"u_front", "u_back"}, map[string]any{"url": testWebhookURL, "enabled": true}, 200)
	if strings.Contains(jsonText(payload), "01234567") {
		t.Fatal("response leaked secret")
	}
	for _, id := range []string{"u_front", "u_back"} {
		var ciphertext []byte
		if err := a.db.QueryRow(`SELECT encrypted_url FROM user_wecom_webhooks WHERE user_id=?`, id).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(ciphertext, []byte("01234567")) {
			t.Fatal("plaintext persisted")
		}
		raw, err := decryptWebhook(a.wecomKey, tenantID, id, ciphertext)
		if err != nil || raw != testWebhookURL {
			t.Fatal("encrypted URL did not roundtrip")
		}
		if _, err = decryptWebhook(a.wecomKey, tenantID, "other-user", ciphertext); err == nil {
			t.Fatal("ciphertext not recipient bound")
		}
	}
	if deliveryStatus(t, a, notice) != "skipped" {
		t.Fatal("old pending delivery not canceled")
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs WHERE before_json LIKE '%01234567%' OR after_json LIKE '%01234567%'`) != 0 {
		t.Fatal("audit leaked URL")
	}
	bulkCall(t, a, "u_admin", "wecom-config", []string{"u_member"}, map[string]any{"enabled": false}, 200)
	if bulkFixtureCount(t, a, `SELECT length(encrypted_url) FROM user_wecom_webhooks WHERE user_id='u_member'`) != 0 {
		t.Fatal("disable generated a URL")
	}
	a.wecomKey = nil
	bulkCall(t, a, "u_admin", "wecom-config", []string{"u_front"}, map[string]any{"url": testWebhookURL}, 503)
}

func TestMemberBulkAndProjectManagementDenyImpersonationAndAnonymous(t *testing.T) {
	a := testApp(t)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/organization/members/bulk", `{"action":"activate","userIds":["u_front"]}`},
		{"GET", "/api/projects/" + projectID + "/members", ""},
		{"PATCH", "/api/projects/" + projectID + "/members", `{"addUserIds":["u_front"]}`},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.scopedAPI().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("anonymous accepted: %d", w.Code)
		}
		scoped := *a
		scoped.user = "u_admin"
		scoped.impersonation = &impersonationContext{AdminID: "u_admin", TargetID: "u_admin"}
		r = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		if strings.Contains(tc.path, "organization") {
			scoped.organizationMembersBulk(w, r)
		} else {
			scoped.manageProjectMembers(w, r, projectID)
		}
		if w.Code != 403 {
			t.Fatalf("impersonation accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestMemberBulkRechecksRevokedGrantAfterWriterLock(t *testing.T) {
	a := fileSQLiteTestApp(t)
	group := orgGroup(t, a, "Concurrent member manager", []string{"members.manage"}, []string{"u_viewer"})
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
	r := httptest.NewRequest("POST", "/api/organization/members/bulk", strings.NewReader(`{"action":"deactivate","userIds":["u_front"]}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	scoped := *a
	scoped.user = "u_viewer"
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); scoped.organizationMembersBulk(w, r); done <- w }()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.Code != 403 {
			t.Fatalf("revoked grant used: %d %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	if bulkFixtureCount(t, a, `SELECT active FROM users WHERE id='u_front'`) != 1 {
		t.Fatal("revoked manager changed account")
	}
}
