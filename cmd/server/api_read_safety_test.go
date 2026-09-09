package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoreCollaborationStorageFailuresReturn503(t *testing.T) {
	for _, test := range []struct{ table, path, method, body string }{
		{"checklist_items", "/api/requirements/1/checklist", "GET", ""},
		{"checklist_items", "/api/requirements/1/checklist", "POST", `{"text":"不能丢失的检查项"}`},
		{"checklist_items", "/api/requirements/1/checklist", "PATCH", `{"id":1,"done":true}`},
		{"activities", "/api/requirements/1/activities", "GET", ""},
		{"notification_outbox", "/api/notifications/outbox", "GET", ""},
		{"notification_outbox", "/api/notifications/outbox/1/retry", "POST", `{}`},
		{"user_notifications", "/api/notifications", "GET", ""},
		{"user_notifications", "/api/notifications/unread-count", "GET", ""},
		{"user_notifications", "/api/notifications/read-all", "POST", `{}`},
		{"user_notifications", "/api/notifications/1", "PATCH", `{"read":true}`},
		{"requirements", "/api/my-work", "GET", ""},
		{"defects", "/api/my-work", "GET", ""},
		{"sprints", "/api/my-work", "GET", ""},
		{"test_cases", "/api/my-work", "GET", ""},
		{"test_executions", "/api/my-work", "GET", ""},
		{"requirements", "/api/search", "GET", ""},
		{"defects", "/api/search", "GET", ""},
		{"sprints", "/api/search", "GET", ""},
		{"test_cases", "/api/search", "GET", ""},
		{"test_plans", "/api/search", "GET", ""},
		{"requirements", "/api/projects", "GET", ""},
		{"test_cases", "/api/projects/" + projectID + "/summary", "GET", ""},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			a := fileSQLiteTestApp(t)
			// Only this test's temporary file DB is altered, never the preview DB.
			if _, err := a.db.Exec("ALTER TABLE " + test.table + " RENAME TO unavailable_test_table"); err != nil {
				t.Fatal(err)
			}
			w := languageRequest(t, a, test.method, test.path, "u_admin", projectID, "en-US", test.body)
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "no such table") || strings.Contains(w.Body.String(), "unavailable_test_table") {
				t.Fatalf("storage failure was exposed or returned a false success: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSearchNegativeOffsetAndAPIBodyLimit(t *testing.T) {
	a := testApp(t)
	w := languageRequest(t, a, "GET", "/api/search?offset=-1", "u_admin", projectID, "en-US", "")
	if w.Code != 200 || jsonMap(t, w)["offset"] != float64(0) {
		t.Fatalf("negative pagination offset was not normalized: %d %s", w.Code, w.Body.String())
	}
	w = languageRequest(t, a, "PATCH", "/api/preferences/locale", "u_admin", projectID, "en-US", strings.Repeat("a", (2<<20)+1))
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "2 MB") {
		t.Fatalf("unbounded API request accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestAssignmentNotificationsUseScopedStableIdentity(t *testing.T) {
	a := testApp(t)
	a.user = "u_pm"
	var duplicateName string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE id='u_admin'`).Scan(&duplicateName); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE users SET name=? WHERE id='u_front'`, duplicateName); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"requirements", "defects"} {
		if _, err := a.db.Exec(`UPDATE `+table+` SET assignee=?,assignee_user_id='u_front' WHERE tenant_id=? AND project_id=? AND id=1`, duplicateName, tenantID, projectID); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []string{"requirement.assigned", "defect.assigned", "defect.assignee_changed"} {
		a.notify(event, 1, duplicateName)
		var count, wrong int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type=? AND actor_user_id='u_pm' AND recipient_user_id='u_front'`, event).Scan(&count); err != nil || count != 1 {
			t.Fatalf("stable assignee did not receive %s: count=%d err=%v", event, count, err)
		}
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type=? AND actor_user_id='u_pm' AND recipient_user_id='u_admin'`, event).Scan(&wrong); err != nil || wrong != 0 {
			t.Fatalf("same-name user incorrectly received %s: count=%d err=%v", event, wrong, err)
		}
	}
}

func TestCoreCollaborationScanFailuresDoNotReturnPartialSuccess(t *testing.T) {
	for _, test := range []struct{ path, sql string }{
		{"/api/requirements/1/checklist", `INSERT INTO checklist_items(tenant_id,project_id,requirement_id,text,done) VALUES('` + tenantID + `','` + projectID + `',1,'invalid boolean','not-a-boolean')`},
		{"/api/notifications/outbox", `INSERT INTO notification_outbox(tenant_id,project_id,event_type,payload,retry_count,created_at) VALUES('` + tenantID + `','` + projectID + `','test.failed','{}','not-a-number','2026-09-03T00:00:00Z')`},
		{"/api/notifications", `INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,event_type,subject_type,subject_id,title,created_at) VALUES('` + tenantID + `','` + projectID + `','u_admin','test.failed','test_execution','not-a-number','测试执行失败','2026-09-03T00:00:00Z')`},
	} {
		t.Run(test.path, func(t *testing.T) {
			a := fileSQLiteTestApp(t)
			if _, err := a.db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			w := languageRequest(t, a, "GET", test.path, "u_admin", projectID, "en-US", "")
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("invalid stored type returned success: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestLocaleProfileAndOutboxDoNotMaskDatabaseFailureAsAuthentication(t *testing.T) {
	a := fileSQLiteTestApp(t)
	a.user = "u_admin"
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{a.localePreferences, a.profile, a.outbox} {
		w := httptest.NewRecorder()
		setResponseLocale(w, "en-US")
		handler(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("database outage became unavailable account/not found: %d %s", w.Code, w.Body.String())
		}
	}
}
