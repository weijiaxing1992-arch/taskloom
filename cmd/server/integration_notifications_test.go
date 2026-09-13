package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func integrationNotificationFixture(t *testing.T, a *App, project, recipient, event, title, body, readAt string) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,read_at,created_at,dedupe_key)VALUES(?,?,?,'u_admin',?,'requirement',1,?,?,?,?,?)`, tenantID, project, recipient, event, title, body, nullableText(readAt), orgNow(), "integration-"+project+"-"+recipient+"-"+event+"-"+title)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func TestIntegrationNotificationsAreOwnerProjectBoundAndReadOnly(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DELETE FROM user_notifications`); err != nil {
		t.Fatal(err)
	}
	unreadID := integrationNotificationFixture(t, a, projectID, "u_front", "custom.integration", "待确认通知", "完整通知正文 needle %_", "")
	readID := integrationNotificationFixture(t, a, projectID, "u_front", "requirement.updated", "已读通知", "已读完整正文", "2026-09-11T01:00:00Z")
	otherUserID := integrationNotificationFixture(t, a, projectID, "u_pm", "custom.integration", "他人通知", "不能泄露", "")
	otherProjectID := integrationNotificationFixture(t, a, insightProjectID, "u_front", "custom.integration", "其他项目通知", "不能跨项目泄露", "")

	withoutScope, _ := securityIntegrationToken(t, a, "u_front", integrationReadScopes)
	if response := securityIntegrationRequest(t, a, withoutScope, http.MethodGet, "/api/open/v1/notifications", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("notification scope bypassed: %d %s", response.Code, response.Body.String())
	}
	scopes := append(append([]string{}, integrationReadScopes...), "notifications:read")
	token, _ := securityIntegrationToken(t, a, "u_front", scopes)
	list := securityIntegrationRequest(t, a, token, http.MethodGet, "/api/open/v1/notifications?page=1&pageSize=25", "", nil)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "他人通知") || strings.Contains(list.Body.String(), "其他项目通知") || !strings.Contains(list.Body.String(), "完整通知正文 needle %_") {
		t.Fatalf("notification list boundary failed: %d %s", list.Code, list.Body.String())
	}
	data := jsonMap(t, list)
	if data["total"] != float64(2) || len(data["items"].([]any)) != 2 || data["contentTrust"] != "untrusted_business_data" {
		t.Fatalf("unexpected notification collection: %#v", data)
	}
	filtered := securityIntegrationRequest(t, a, token, http.MethodGet, "/api/open/v1/notifications?read=unread&eventType=custom.integration&group=activity&q="+url.QueryEscape("needle %_"), "", nil)
	if filtered.Code != http.StatusOK || jsonMap(t, filtered)["total"] != float64(1) || !strings.Contains(filtered.Body.String(), fmt.Sprint(unreadID)) {
		t.Fatalf("notification filters failed: %d %s", filtered.Code, filtered.Body.String())
	}
	detail := securityIntegrationRequest(t, a, token, http.MethodGet, fmt.Sprintf("/api/open/v1/notifications/%d", unreadID), "", nil)
	if detail.Code != http.StatusOK || jsonMap(t, detail)["id"] != float64(unreadID) || !strings.Contains(detail.Body.String(), "完整通知正文") {
		t.Fatalf("notification detail incomplete: %d %s", detail.Code, detail.Body.String())
	}
	for _, id := range []int64{otherUserID, otherProjectID} {
		response := securityIntegrationRequest(t, a, token, http.MethodGet, fmt.Sprintf("/api/open/v1/notifications/%d", id), "", nil)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "不能") {
			t.Fatalf("foreign notification leaked: %d %s", response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/open/v1/notifications?pageSize=10&limit=10",
		"/api/open/v1/notifications?recipientUserId=u_pm",
		"/api/open/v1/notifications?projectId=" + insightProjectID,
		"/api/open/v1/notifications?read=unknown",
		"/api/open/v1/notifications?group=unknown",
		"/api/open/v1/notifications?q=a&q=b",
		fmt.Sprintf("/api/open/v1/notifications/%d?q=x", readID),
	} {
		if response := securityIntegrationRequest(t, a, token, http.MethodGet, path, "", nil); response.Code != http.StatusBadRequest {
			t.Fatalf("unsafe notification query accepted %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodPatch, fmt.Sprintf("/api/open/v1/notifications/%d", unreadID)},
		{http.MethodPost, "/api/open/v1/notifications"},
		{http.MethodDelete, fmt.Sprintf("/api/open/v1/notifications/%d", unreadID)},
	} {
		if response := securityIntegrationRequest(t, a, token, request.method, request.path, `{}`, nil); response.Code != http.StatusNotFound {
			t.Fatalf("notification mutation exposed: %s %d %s", request.method, response.Code, response.Body.String())
		}
	}
	if response := securityIntegrationRequest(t, a, token, http.MethodGet, "/api/open/v1/notifications/0001", "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("noncanonical notification ID accepted: %d %s", response.Code, response.Body.String())
	}
	if notificationReadAt(t, a, unreadID).Valid || !notificationReadAt(t, a, readID).Valid {
		t.Fatal("read-only API changed notification state")
	}
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	revoked := securityIntegrationRequest(t, a, token, http.MethodGet, "/api/open/v1/notifications", "", nil)
	if revoked.Code != http.StatusUnauthorized || strings.Contains(revoked.Body.String(), "完整通知正文") {
		t.Fatalf("revoked project credential retained notification access: %d %s", revoked.Code, revoked.Body.String())
	}
}
