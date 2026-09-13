package main

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReplyNotificationInboxRevokedProjectCannotBeReadOrMutated(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'frontend',?,?); INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,'u_front','frontend'); UPDATE user_notifications SET read_at=? WHERE tenant_id=? AND recipient_user_id='u_front'`, tenantID, insightProjectID, "u_front", orgNow(), orgNow(), tenantID, insightProjectID, orgNow(), tenantID); err != nil {
		t.Fatal(err)
	}
	parent := threadPost(t, a, "/api/requirements/1/comments", "u_front", map[string]any{"body": "旧项目父评论"})
	threadPost(t, a, "/api/requirements/1/comments", "u_admin", map[string]any{"body": "已撤权项目的敏感回复内容", "replyToId": parent["id"]})
	var privateID int64
	if err := a.db.QueryRow(`SELECT id FROM user_notifications WHERE recipient_user_id='u_front' AND event_type='requirement.replied' AND project_id=?`, projectID).Scan(&privateID); err != nil {
		t.Fatal(err)
	}
	result, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,'u_front','u_admin','requirement.replied','requirement',1,'你的评论收到回复','保留可访问回复',?,'visible-reply')`, tenantID, insightProjectID, orgNow())
	if err != nil {
		t.Fatal(err)
	}
	publicID, _ := result.LastInsertId()
	if _, err = a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "?project=" + projectID, "?read=unread", "?eventType=requirement.replied"} {
		w := apiRequest(a, "GET", "/api/notifications"+suffix, "u_front", insightProjectID, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "敏感回复内容") {
			t.Fatalf("revoked reply leaked: %d %s", w.Code, w.Body.String())
		}
		v := jsonMap(t, w)
		want := 1
		if strings.HasPrefix(suffix, "?project=") {
			want = 0
		}
		if len(v["items"].([]any)) != want || v["unread"] != float64(1) {
			t.Fatalf("revoked reply included in listing/count: %s", w.Body.String())
		}
	}
	w := apiRequest(a, "GET", "/api/notifications/unread-count", "u_front", insightProjectID, "")
	if w.Code != 200 || jsonMap(t, w)["unread"] != float64(1) {
		t.Fatalf("badge leaks revoked project: %d %s", w.Code, w.Body.String())
	}
	for _, read := range []bool{true, false} {
		w = apiRequest(a, "PATCH", fmt.Sprintf("/api/notifications/%d", privateID), "u_front", insightProjectID, jsonText(map[string]bool{"read": read}))
		if w.Code != 404 {
			t.Fatalf("revoked notification mutated: %d %s", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, "POST", "/api/notifications/read-all", "u_front", insightProjectID, "{}")
	if w.Code != 200 || jsonMap(t, w)["updated"] != float64(1) {
		t.Fatalf("read-all wrote inaccessible rows: %d %s", w.Code, w.Body.String())
	}
	var privateRead, publicRead sql.NullString
	if err = a.db.QueryRow(`SELECT read_at FROM user_notifications WHERE id=?`, privateID).Scan(&privateRead); err != nil {
		t.Fatal(err)
	}
	if err = a.db.QueryRow(`SELECT read_at FROM user_notifications WHERE id=?`, publicID).Scan(&publicRead); err != nil || privateRead.Valid || !publicRead.Valid {
		t.Fatalf("read flags wrong: private=%v public=%v err=%v", privateRead, publicRead, err)
	}
	// Re-grant access exposes the retained history; revocation never deletes it.
	if _, err = a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'frontend',?,?)`, tenantID, projectID, "u_front", orgNow(), orgNow()); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/notifications?project="+projectID, "u_front", insightProjectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "敏感回复内容") {
		t.Fatalf("re-granted history lost: %d %s", w.Code, w.Body.String())
	}
}

func TestReplyNotificationAdminTenantIsolationAndFailedPermissionQuery(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_admin'; INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,'u_admin','u_front','requirement.replied','requirement',1,'你的评论收到回复','管理员跨项目',?,'admin-reply'),('foreign',?,'u_admin','u_front','requirement.replied','requirement',1,'你的评论收到回复','其他租户不许读',?,'foreign-reply')`, tenantID, insightProjectID, tenantID, insightProjectID, orgNow(), insightProjectID, orgNow()); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", "/api/notifications?project="+insightProjectID, "u_admin", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "管理员跨项目") || strings.Contains(w.Body.String(), "其他租户不许读") {
		t.Fatalf("admin tenant isolation wrong: %d %s", w.Code, w.Body.String())
	}
	b := administrationSessionFixture(t, a, "u_admin")
	if _, err := a.db.Exec(`DROP TABLE project_members`); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{{"GET", "/api/notifications", ""}, {"GET", "/api/notifications/unread-count", ""}, {"POST", "/api/notifications/read-all", "{}"}, {"PATCH", "/api/notifications/1", `{"read":true}`}} {
		w = httptest.NewRecorder()
		r := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		if request.path == "/api/notifications" {
			b.notifications(w, r)
		} else {
			b.notification(w, r)
		}
		if w.Code != 503 || strings.Contains(w.Body.String(), "no such table") {
			t.Fatalf("permission DB failure not fail closed: %d %s", w.Code, w.Body.String())
		}
	}
}
