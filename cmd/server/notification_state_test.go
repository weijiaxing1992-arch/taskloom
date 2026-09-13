package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func notificationStateFixture(t *testing.T, a *App, tenant, project, recipient, event string) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,event_type,subject_type,subject_id,title,created_at)VALUES(?,?,?,?,'requirement',1,'isolated notification',?)`, tenant, project, recipient, event, orgNow())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func notificationStateRequest(t *testing.T, a *App, ids []int64, read bool, status int) map[string]any {
	t.Helper()
	w := apiRequest(a, "POST", "/api/notifications/bulk-read", "u_front", projectID, jsonText(map[string]any{"ids": ids, "read": read}))
	if w.Code != status {
		t.Fatalf("bulk: got %d want %d: %s", w.Code, status, w.Body)
	}
	return jsonMap(t, w)
}

func notificationReadAt(t *testing.T, a *App, id int64) sql.NullString {
	t.Helper()
	var value sql.NullString
	if err := a.db.QueryRow(`SELECT read_at FROM user_notifications WHERE id=?`, id).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestNotificationStateBatchTransitionsCountsAndFilters(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
	first := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
	second := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.assigned")
	third := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.updated")
	other := notificationStateFixture(t, a, tenantID, projectID, "u_backend", "requirement.mentioned")
	beforeOutbox := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM notification_outbox`)
	v := notificationStateRequest(t, a, []int64{first, second}, true, 200)
	if v["updated"] != float64(2) || v["unread"] != float64(1) {
		t.Fatal(v)
	}
	readAt := notificationReadAt(t, a, first)
	v = notificationStateRequest(t, a, []int64{first, second}, true, 200)
	if v["updated"] != float64(0) || v["unread"] != float64(1) || notificationReadAt(t, a, first) != readAt {
		t.Fatalf("retry must be idempotent and preserve original read timestamp: %v", v)
	}
	v = notificationStateRequest(t, a, []int64{first, third}, false, 200)
	if v["updated"] != float64(1) || v["unread"] != float64(2) {
		t.Fatal(v)
	}
	for _, filter := range []struct {
		query string
		count int
	}{{"", 3}, {"?read=read", 1}, {"?read=unread", 2}, {"?read=unread&group=mentions&eventType=requirement.mentioned&project=" + projectID, 1}, {"?read=read&group=mentions", 0}, {"?limit=1&offset=1", 1}} {
		w := apiRequest(a, "GET", "/api/notifications"+filter.query, "u_front", projectID, "")
		data := jsonMap(t, w)
		if w.Code != 200 || len(data["items"].([]any)) != filter.count || data["unread"] != float64(2) {
			t.Fatalf("filter %s inconsistent: %d %s", filter.query, w.Code, w.Body)
		}
		groups := data["groupUnread"].(map[string]any)
		if groups["mentions"] != float64(1) || groups["changes"] != float64(1) || groups["handoffs"] != float64(0) {
			t.Fatalf("unread groups inconsistent: %v", groups)
		}
	}
	w := apiRequest(a, "POST", "/api/notifications/read-all", "u_front", projectID, "{}")
	if w.Code != 200 || jsonMap(t, w)["updated"] != float64(2) || jsonMap(t, w)["unread"] != float64(0) || notificationReadAt(t, a, other).Valid {
		t.Fatalf("read-all affects own accessible notices only: %d %s", w.Code, w.Body)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/notifications/%d", second), "u_front", projectID, `{"read":false}`)
	if w.Code != 200 || jsonMap(t, w)["unread"] != float64(1) || notificationReadAt(t, a, second).Valid {
		t.Fatalf("single shares state/count semantics: %d %s", w.Code, w.Body)
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM notification_outbox`) != beforeOutbox {
		t.Fatal("read flags must never send notifications")
	}
}

func TestNotificationStateBatchStrictBoundedInput(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
	ids := make([]int64, notificationBatchLimit)
	for i := range ids {
		ids[i] = notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
	}
	tooMany := append(append([]int64{}, ids...), 100000)
	for _, body := range []string{`{}`, `{"ids":[1]}`, `{"ids":[],"read":true}`, `{"ids":[0],"read":true}`, `{"ids":[-1],"read":false}`, `{"ids":[1,1],"read":true}`, `{"ids":[1.5],"read":true}`, `{"ids":["1"],"read":true}`, `{"ids":[1],"read":null}`, `{"ids":[1],"read":"false"}`, `{"ids":[1],"read":true,"recipientUserId":"u_admin"}`, `{"ids":[1],"read":true} {}`, `{"ids":[1],"read":true,"padding":"` + strings.Repeat("x", 9000) + `"}`, jsonText(map[string]any{"ids": tooMany, "read": true})} {
		w := apiRequest(a, "POST", "/api/notifications/bulk-read", "u_front", projectID, body)
		if w.Code != 400 {
			t.Fatalf("invalid input accepted: %d %.200s", w.Code, w.Body)
		}
	}
	for _, path := range []string{"/api/notifications/bulk-read", "/api/notifications/read-all"} {
		w := apiRequest(a, "PATCH", path, "u_front", projectID, `{"read":true}`)
		if w.Code != 405 {
			t.Fatalf("wrong method accepted: %d", w.Code)
		}
	}
	for _, body := range []string{`{}`, `{"read":null}`, `{"read":true} {}`} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/notifications/%d", ids[0]), "u_front", projectID, body)
		if w.Code != 400 {
			t.Fatalf("single defaults missing state to unread: %d", w.Code)
		}
	}
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM user_notifications WHERE read_at IS NOT NULL`) != 0 {
		t.Fatal("invalid input changed state")
	}
	result := notificationStateRequest(t, a, ids, true, 200)
	if result["updated"] != float64(notificationBatchLimit) || result["unread"] != float64(0) {
		t.Fatal(result)
	}
	w := apiRequest(a, "GET", "/api/notifications?read=unknown", "u_front", projectID, "")
	if w.Code != 400 {
		t.Fatalf("unknown read filter silently ignored: %d", w.Code)
	}
}

func TestNotificationStateMixedUnauthorizedIDsRejectWholeBatch(t *testing.T) {
	for _, scenario := range []string{"recipient", "tenant", "project", "archived", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
			allowed := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
			denied := int64(900000)
			switch scenario {
			case "recipient":
				denied = notificationStateFixture(t, a, tenantID, projectID, "u_admin", "requirement.mentioned")
			case "tenant":
				denied = notificationStateFixture(t, a, "other-tenant", projectID, "u_front", "requirement.mentioned")
			case "project", "archived":
				denied = notificationStateFixture(t, a, tenantID, insightProjectID, "u_front", "requirement.mentioned")
				bulkFixtureExec(t, a, `DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, insightProjectID)
				if scenario == "archived" {
					bulkFixtureExec(t, a, `INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,'u_front','frontend',?,?); UPDATE projects SET status='archived' WHERE tenant_id=? AND id=?`, tenantID, insightProjectID, orgNow(), orgNow(), tenantID, insightProjectID)
				}
			}
			for _, read := range []bool{true, false} {
				notificationStateRequest(t, a, []int64{allowed, denied}, read, 404)
				if notificationReadAt(t, a, allowed).Valid {
					t.Fatal("mixed batch partially applied")
				}
			}
		})
	}
}

func TestNotificationStateTransactionsRollbackFailures(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
	first := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
	second := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
	beforeRevision := bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID)
	bulkFixtureExec(t, a, fmt.Sprintf(`CREATE TRIGGER notification_state_fail BEFORE UPDATE ON user_notifications WHEN NEW.id=%d BEGIN SELECT RAISE(ABORT,'private storage error'); END`, second))
	v := notificationStateRequest(t, a, []int64{first, second}, true, 503)
	if strings.Contains(jsonText(v), "private storage") || notificationReadAt(t, a, first).Valid || notificationReadAt(t, a, second).Valid || bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID) != beforeRevision {
		t.Fatalf("partial update or internal error leaked: %v", v)
	}
}

func TestNotificationStateRevocationAfterAuthenticationAndLock(t *testing.T) {
	for _, action := range []string{"bulk-read", "read-all", "single"} {
		for _, revoke := range []string{"session", "operation", "inactive", "membership", "project", "impersonation"} {
			t.Run(action+"/"+revoke, func(t *testing.T) {
				a := testApp(t)
				bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
				id := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
				scoped := administrationSessionFixture(t, a, "u_front")
				want, change := 401, `UPDATE auth_sessions SET revoked_at='2026-01-01T00:00:00Z'`
				switch revoke {
				case "operation":
					want, change = 403, `UPDATE users SET operation_disabled=1 WHERE id='u_front'`
				case "inactive":
					change = `UPDATE users SET active=0 WHERE id='u_front'`
				case "membership":
					change = `UPDATE tenant_memberships SET status='inactive' WHERE user_id='u_front'`
				case "project":
					want, change = 404, `DELETE FROM project_members WHERE user_id='u_front'`
					if action == "read-all" {
						want = 200 // No longer accessible: no rows changed, not a foreign inbox operation.
					}
				case "impersonation":
					want = 403
					change = fmt.Sprintf(`INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at)VALUES(%q,%q,'u_front','u_admin',%q,'isolated fixture','2026-01-01T00:00:00Z','2100-01-01T00:00:00Z')`, tenantID, scoped.sessionToken, projectID)
				}
				// Runs after acquiring the write lock, so a cached middleware authorization is insufficient.
				bulkFixtureExec(t, a, `CREATE TRIGGER notification_revoke_after_lock AFTER UPDATE ON organization_write_locks BEGIN `+change+`; END`)
				method, path, body := "POST", "/api/notifications/"+action, jsonText(map[string]any{"ids": []int64{id}, "read": true})
				if action == "single" {
					method, path, body = "PATCH", fmt.Sprintf("/api/notifications/%d", id), `{"read":true}`
				}
				w := httptest.NewRecorder()
				scoped.notification(w, httptest.NewRequest(method, path, strings.NewReader(body)))
				if w.Code != want || notificationReadAt(t, a, id).Valid {
					t.Fatalf("revoked cached identity mutated inbox: %d want %d: %s", w.Code, want, w.Body)
				}
			})
		}
	}
}

func TestNotificationStateConcurrentRepeatedBatchesStayConsistent(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
	ids := []int64{notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned"), notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.updated")}
	one, two := administrationSessionFixture(t, a, "u_front"), administrationSessionFixture(t, a, "u_front")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan notificationStateResult, 2)
	errors := make(chan error, 2)
	for _, scoped := range []*App{&one, &two} {
		wg.Add(1)
		go func(a *App) {
			defer wg.Done()
			<-start
			result, err := a.setNotificationState(context.Background(), ids, true, false)
			results <- result
			errors <- err
		}(scoped)
	}
	close(start)
	wg.Wait()
	var updated int64
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		result := <-results
		if result.Unread != 0 {
			t.Fatal(result)
		}
		updated += result.Updated
	}
	if updated != 2 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM user_notifications WHERE read_at IS NOT NULL`) != 2 {
		t.Fatalf("concurrent retry changed count more than once: %d", updated)
	}
}

func TestNotificationStateImpersonationViewOnlyAndViewerOwnInbox(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
	id := notificationStateFixture(t, a, tenantID, projectID, "u_viewer", "requirement.mentioned")
	w := apiRequest(a, "POST", "/api/notifications/bulk-read", "u_viewer", projectID, jsonText(map[string]any{"ids": []int64{id}, "read": true}))
	if w.Code != 200 {
		t.Fatalf("viewer must manage own read flags: %d %s", w.Code, w.Body)
	}
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	w = impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_viewer","reason":"isolated notification review"}`, projectID)
	if w.Code != 200 {
		t.Fatalf("fixture impersonation: %d %s", w.Code, w.Body)
	}
	w = impersonationRequest(a, admin, "GET", "/api/notifications", "", projectID)
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("authorized read-only inbox unavailable: %d %s", w.Code, w.Body)
	}
	w = impersonationRequest(a, admin, "POST", "/api/notifications/bulk-read", jsonText(map[string]any{"ids": []int64{id}, "read": false}), projectID)
	if w.Code != 403 || !notificationReadAt(t, a, id).Valid {
		t.Fatalf("impersonator altered another inbox: %d %s", w.Code, w.Body)
	}
}

func TestNotificationStateDoesNotDependOnStaleSelectedProject(t *testing.T) {
	a := testApp(t)
	bulkFixtureExec(t, a, `DELETE FROM user_notifications; DELETE FROM project_members WHERE user_id='u_front' AND project_id=?`, insightProjectID)
	id := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
	for _, path := range []string{"/api/notifications", "/api/notifications/unread-count"} {
		w := apiRequest(a, "GET", path, "u_front", insightProjectID, "")
		if w.Code != 200 || jsonMap(t, w)["unread"] != float64(1) {
			t.Fatalf("stale selection hid accessible inbox: %d %s", w.Code, w.Body)
		}
	}
	w := apiRequest(a, "POST", "/api/notifications/bulk-read", "u_front", insightProjectID, jsonText(map[string]any{"ids": []int64{id}, "read": true}))
	if w.Code != 200 || jsonMap(t, w)["unread"] != float64(0) {
		t.Fatalf("stale selection blocked own notification state: %d %s", w.Code, w.Body)
	}
	// Personal routing must not make operational outboxes accessible to members.
	for _, project := range []string{projectID, insightProjectID} {
		w := apiRequest(a, "GET", "/api/notifications/outbox", "u_front", project, "")
		if w.Code != 403 {
			t.Fatalf("personal route widened outbox permissions: %d %s", w.Code, w.Body)
		}
	}
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, httptest.NewRequest("POST", "/api/notifications/bulk-read", strings.NewReader(jsonText(map[string]any{"ids": []int64{id}, "read": false}))))
	if w.Code != 401 || !notificationReadAt(t, a, id).Valid {
		t.Fatalf("unauthenticated notification write: %d %s", w.Code, w.Body)
	}
	bulkFixtureExec(t, a, `DELETE FROM project_members WHERE user_id='u_front'`)
	w = apiRequest(a, "GET", "/api/notifications", "u_front", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["unread"] != float64(0) || len(jsonMap(t, w)["items"].([]any)) != 0 {
		t.Fatalf("all projects revoked but inbox retained accessible rows: %d %s", w.Code, w.Body)
	}
	notificationStateRequest(t, a, []int64{id}, false, 404)
	w = apiRequest(a, "POST", "/api/notifications/read-all", "u_front", projectID, "{}")
	if w.Code != 200 || jsonMap(t, w)["updated"] != float64(0) || jsonMap(t, w)["unread"] != float64(0) {
		t.Fatalf("read-all changed inaccessible notices: %d %s", w.Code, w.Body)
	}
}

func TestNotificationStatePersonalRoutesKeepOuterIdentityGuards(t *testing.T) {
	for _, guard := range []string{"password", "operation", "session", "identity"} {
		t.Run(guard, func(t *testing.T) {
			a := testApp(t)
			bulkFixtureExec(t, a, `DELETE FROM user_notifications`)
			id := notificationStateFixture(t, a, tenantID, projectID, "u_front", "requirement.mentioned")
			cookie, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), "u_front")
			if err != nil {
				t.Fatal(err)
			}
			status, expected := 403, "u_front"
			switch guard {
			case "password":
				bulkFixtureExec(t, a, `UPDATE users SET must_change_password=1 WHERE id='u_front'`)
			case "operation":
				bulkFixtureExec(t, a, `UPDATE users SET operation_disabled=1 WHERE id='u_front'`)
			case "session":
				status = 401
				bulkFixtureExec(t, a, `UPDATE auth_sessions SET revoked_at=? WHERE user_id='u_front'`, orgNow())
			case "identity":
				status, expected = 409, "u_admin"
			}
			for _, operation := range []struct{ method, path string }{{"GET", "/api/notifications"}, {"GET", "/api/notifications/unread-count"}, {"POST", "/api/notifications/bulk-read"}, {"POST", "/api/notifications/read-all"}, {"PATCH", fmt.Sprintf("/api/notifications/%d", id)}} {
				r := httptest.NewRequest(operation.method, operation.path, strings.NewReader(jsonText(map[string]any{"ids": []int64{id}, "read": true})))
				r.AddCookie(cookie)
				r.Header.Set("X-TaskLoom-Expected-User", expected)
				r.Header.Set("X-TaskLoom-Project", "stale-project")
				w := httptest.NewRecorder()
				a.scopedAPI().ServeHTTP(w, r)
				if w.Code != status || notificationReadAt(t, a, id).Valid {
					t.Fatalf("%s bypassed %s guard: %d %s", operation.path, guard, w.Code, w.Body)
				}
			}
		})
	}
}
