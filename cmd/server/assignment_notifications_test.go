package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// assignmentNoticeCount deliberately queries by the persisted recipient ID.
// Tests must never rely on a display name because names can be renamed or be
// duplicated within an organization.
func assignmentNoticeCount(t *testing.T, a *App, event, subject string, subjectID int64, recipient string) int {
	t.Helper()
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND event_type=? AND subject_type=? AND subject_id=? AND recipient_user_id=?`, tenantID, projectID, event, subject, subjectID, recipient).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assignmentInboxItem(t *testing.T, a *App, recipient, event, subject string, subjectID int64) map[string]any {
	t.Helper()
	w := apiRequest(a, "GET", "/api/notifications?read=unread&eventType="+event, recipient, projectID, "")
	if w.Code != 200 {
		t.Fatalf("read inbox: %d %s", w.Code, w.Body.String())
	}
	for _, raw := range jsonMap(t, w)["items"].([]any) {
		item := raw.(map[string]any)
		if item["subjectType"] == subject && item["subjectId"] == float64(subjectID) {
			return item
		}
	}
	t.Fatalf("missing %s notification for %s#%d: %s", event, subject, subjectID, w.Body.String())
	return nil
}

func unreadNotificationCount(t *testing.T, a *App, recipient string) int {
	t.Helper()
	w := apiRequest(a, "GET", "/api/notifications/unread-count", recipient, projectID, "")
	if w.Code != 200 {
		t.Fatalf("read unread count: %d %s", w.Code, w.Body.String())
	}
	return int(jsonMap(t, w)["unread"].(float64))
}

// concurrentSprintRequest 复用同一个已登录会话，避免并发测试把 session 创建写入
// 混入被测迭代事务；每个请求仍有独立 recorder 和 body，贴近两个浏览器标签重试。
func concurrentSprintRequest(handler http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDefectCommentMentionReachesInboxAndUnreadBadge(t *testing.T) {
	a := testApp(t)
	const defectID = int64(1)
	beforeUnread := unreadNotificationCount(t, a, "u_front")
	name := peopleName(t, a, "u_front")
	w := apiRequest(a, "POST", fmt.Sprintf("/api/defects/%d/comments", defectID), "u_admin", projectID, jsonText(map[string]any{
		"body":           "@" + name + " 请协助回归此缺陷",
		"mentionUserIds": []string{"u_front"},
	}))
	if w.Code != 201 {
		t.Fatalf("post defect mention: %d %s", w.Code, w.Body.String())
	}
	if count := assignmentNoticeCount(t, a, "defect.mentioned", "defect", defectID, "u_front"); count != 1 {
		t.Fatalf("defect mention rows=%d", count)
	}
	item := assignmentInboxItem(t, a, "u_front", "defect.mentioned", "defect", defectID)
	if item["actorUserId"] != "u_admin" || item["url"] != notificationURL("defect", defectID) {
		t.Fatalf("incorrect inbox item: %#v", item)
	}
	if got := unreadNotificationCount(t, a, "u_front"); got != beforeUnread+1 {
		t.Fatalf("unread badge = %d, want %d", got, beforeUnread+1)
	}
	notificationID := int64(item["id"].(float64))
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/notifications/%d", notificationID), "u_front", projectID, `{"read":true}`)
	if w.Code != 200 {
		t.Fatalf("mark mention read: %d %s", w.Code, w.Body.String())
	}
	if got := unreadNotificationCount(t, a, "u_front"); got != beforeUnread {
		t.Fatalf("read mention did not update badge: %d want %d", got, beforeUnread)
	}
}

// 用户可能在自己创建的缺陷中 @ 自己作为后续待办。该通知必须经过和普通提及
// 相同的落库、列表和未读计数链路；仅回复自己的旧评论才应保持静默。
func TestDefectSelfCommentMentionReachesInboxAndUnreadBadge(t *testing.T) {
	a := testApp(t)
	const defectID = int64(1)
	beforeUnread := unreadNotificationCount(t, a, "u_admin")
	name := peopleName(t, a, "u_admin")
	w := apiRequest(a, "POST", fmt.Sprintf("/api/defects/%d/comments", defectID), "u_admin", projectID, jsonText(map[string]any{
		"body":           "@" + name + " 稍后复核这个缺陷",
		"mentionUserIds": []string{"u_admin", "u_admin"},
	}))
	if w.Code != 201 {
		t.Fatalf("post self defect mention: %d %s", w.Code, w.Body.String())
	}
	if count := assignmentNoticeCount(t, a, "defect.mentioned", "defect", defectID, "u_admin"); count != 1 {
		t.Fatalf("self defect mention rows=%d", count)
	}
	item := assignmentInboxItem(t, a, "u_admin", "defect.mentioned", "defect", defectID)
	if item["actorUserId"] != "u_admin" || item["url"] != notificationURL("defect", defectID) {
		t.Fatalf("incorrect self inbox item: %#v", item)
	}
	if got := unreadNotificationCount(t, a, "u_admin"); got != beforeUnread+1 {
		t.Fatalf("self mention unread badge = %d, want %d", got, beforeUnread+1)
	}
}

func TestDefectAssignmentsNotifyAssigneeVerifierAndSelfAtomically(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/defects", "u_admin", projectID, `{"title":"分配通知回归","assigneeUserId":"u_front","verifierUserId":"u_qa"}`)
	if w.Code != 201 {
		t.Fatalf("create defect: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	for _, check := range []struct{ event, recipient string }{
		{"defect.assigned", "u_front"},
		{"defect.verifier_assigned", "u_qa"},
	} {
		if count := assignmentNoticeCount(t, a, check.event, "defect", id, check.recipient); count != 1 {
			t.Fatalf("%s/%s count=%d", check.event, check.recipient, count)
		}
	}
	if item := assignmentInboxItem(t, a, "u_front", "defect.assigned", "defect", id); item["url"] != notificationURL("defect", id) {
		t.Fatalf("assignee deep link: %#v", item)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/defects/%d", id), "u_admin", projectID, `{"assigneeUserId":"u_back","verifierUserId":"u_pm"}`)
	if w.Code != 200 {
		t.Fatalf("reassign defect: %d %s", w.Code, w.Body.String())
	}
	for _, check := range []struct{ event, recipient string }{
		{"defect.assignee_changed", "u_back"},
		{"defect.verifier_changed", "u_pm"},
	} {
		if count := assignmentNoticeCount(t, a, check.event, "defect", id, check.recipient); count != 1 {
			t.Fatalf("%s/%s count=%d", check.event, check.recipient, count)
		}
	}
	// Assignment is intentionally different from comment mentions: a person who
	// assigns work to themselves must still see the task in their own inbox.
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/defects/%d", id), "u_admin", projectID, `{"assigneeUserId":"u_admin"}`)
	if w.Code != 200 || assignmentNoticeCount(t, a, "defect.assignee_changed", "defect", id, "u_admin") != 1 {
		t.Fatalf("self assignment was not visible: %d %s", w.Code, w.Body.String())
	}

	b := testApp(t)
	beforeDefects := tableCount(t, b, "defects")
	beforeNotices := tableCount(t, b, "user_notifications")
	beforeOutbox := tableCount(t, b, "notification_outbox")
	if _, err := b.db.Exec(`CREATE TRIGGER reject_defect_verifier_notice BEFORE INSERT ON user_notifications WHEN NEW.event_type='defect.verifier_assigned' BEGIN SELECT RAISE(ABORT,'injected notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(b, "POST", "/api/defects", "u_admin", projectID, `{"title":"不能部分写入","assigneeUserId":"u_front","verifierUserId":"u_qa"}`)
	if w.Code != 500 {
		t.Fatalf("notification failure returned %d: %s", w.Code, w.Body.String())
	}
	if tableCount(t, b, "defects") != beforeDefects || tableCount(t, b, "user_notifications") != beforeNotices || tableCount(t, b, "notification_outbox") != beforeOutbox {
		t.Fatal("defect assignment notification failure left a partial write")
	}
}

func TestRequirementRolesAndQualityOwnersUseScopedStableNotifications(t *testing.T) {
	a := testApp(t)
	frontName := peopleName(t, a, "u_front")
	backName := peopleName(t, a, "u_back")
	x := createPeopleRequirement(t, a, map[string]any{
		"ownerUserIds": []string{"u_pm"},
		"roleWeights": map[string]any{
			"frontend": map[string]any{"userIds": []string{"u_front"}, "value": 3},
			"backend":  map[string]any{"userIds": []string{"u_back"}, "value": 5},
		},
	})
	for _, check := range []struct{ event, recipient string }{
		{"requirement.owner_assigned", "u_pm"},
		{"requirement.role_assigned", "u_front"},
		{"requirement.role_assigned", "u_back"},
	} {
		if count := assignmentNoticeCount(t, a, check.event, "requirement", x.ID, check.recipient); count != 1 {
			t.Fatalf("%s/%s count=%d", check.event, check.recipient, count)
		}
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"remarks":"角色成员应收到实际变更"}`)
	if w.Code != 200 {
		t.Fatalf("change requirement: %d %s", w.Code, w.Body.String())
	}
	for _, recipient := range []string{"u_pm", "u_front", "u_back"} {
		if count := assignmentNoticeCount(t, a, "requirement.updated", "requirement", x.ID, recipient); count != 1 {
			t.Fatalf("role-aware update for %s count=%d", recipient, count)
		}
	}

	w = apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, jsonText(map[string]any{"title": "负责人通知用例", "steps": "执行", "expected": "成功", "owner": frontName}))
	if w.Code != 201 {
		t.Fatalf("create case: %d %s", w.Code, w.Body.String())
	}
	caseID := int64(jsonMap(t, w)["id"].(float64))
	if assignmentNoticeCount(t, a, "test_case.owner_assigned", "test_case", caseID, "u_front") != 1 {
		t.Fatal("test case owner did not receive assignment")
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/test-cases/%d", caseID), "u_admin", projectID, jsonText(map[string]any{"owner": backName}))
	if w.Code != 200 || assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", caseID, "u_back") != 1 {
		t.Fatalf("test case owner change: %d %s", w.Code, w.Body.String())
	}

	w = apiRequest(a, "POST", "/api/test-plans", "u_admin", projectID, `{"name":"计划负责人通知","ownerUserId":"u_pm","executorUserId":"u_qa"}`)
	if w.Code != 201 {
		t.Fatalf("create plan: %d %s", w.Code, w.Body.String())
	}
	planID := int64(jsonMap(t, w)["id"].(float64))
	for _, check := range []struct{ event, recipient string }{
		{"test_plan.owner_assigned", "u_pm"},
		{"test_plan.executor_assigned", "u_qa"},
	} {
		if count := assignmentNoticeCount(t, a, check.event, "test_plan", planID, check.recipient); count != 1 {
			t.Fatalf("%s/%s count=%d", check.event, check.recipient, count)
		}
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/test-plans/%d", planID), "u_admin", projectID, `{"ownerUserId":"u_front","executorUserId":"u_back"}`)
	if w.Code != 200 {
		t.Fatalf("change plan: %d %s", w.Code, w.Body.String())
	}
	for _, check := range []struct{ event, recipient string }{
		{"test_plan.owner_changed", "u_front"},
		{"test_plan.executor_changed", "u_back"},
	} {
		if count := assignmentNoticeCount(t, a, check.event, "test_plan", planID, check.recipient); count != 1 {
			t.Fatalf("%s/%s count=%d", check.event, check.recipient, count)
		}
	}
	if item := assignmentInboxItem(t, a, "u_front", "test_plan.owner_changed", "test_plan", planID); item["url"] != notificationURL("test_plan", planID) {
		t.Fatalf("plan owner deep link: %#v", item)
	}
}

func TestSprintLifecycleNotifiesProjectOwnerTransactionally(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/sprints", "u_admin", projectID, `{"name":"多人状态通知迭代","startDate":"2026-09-04","endDate":"2026-09-10"}`)
	if w.Code != 201 {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/sprints/%d", id), "u_admin", projectID, `{"status":"进行中"}`)
	if w.Code != 200 {
		t.Fatalf("start sprint: %d %s", w.Code, w.Body.String())
	}
	for _, recipient := range []string{"u_admin"} {
		if count := assignmentNoticeCount(t, a, "sprint.status_changed", "sprint", id, recipient); count != 1 {
			t.Fatalf("status recipient %s count=%d", recipient, count)
		}
		if item := assignmentInboxItem(t, a, recipient, "sprint.status_changed", "sprint", id); item["url"] != notificationURL("sprint", id) {
			t.Fatalf("status deep link for %s: %#v", recipient, item)
		}
	}
	var statusOutbox int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='sprint.status_changed'`, tenantID, projectID).Scan(&statusOutbox); err != nil || statusOutbox != 1 {
		t.Fatalf("status outbox count=%d err=%v", statusOutbox, err)
	}
	w = apiRequest(a, "POST", fmt.Sprintf("/api/sprints/%d/complete", id), "u_admin", projectID, `{"targetSprint":"待规划"}`)
	if w.Code != 200 {
		t.Fatalf("complete sprint: %d %s", w.Code, w.Body.String())
	}
	for _, recipient := range []string{"u_admin"} {
		if count := assignmentNoticeCount(t, a, "sprint.completed", "sprint", id, recipient); count != 1 {
			t.Fatalf("completion recipient %s count=%d", recipient, count)
		}
	}
}

func TestSprintPatchConcurrentRetryWritesOneStatusNotice(t *testing.T) {
	a := fileSQLiteTestApp(t)
	w := apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", projectID, `{"name":"并发状态迭代","startDate":"2026-09-04","endDate":"2026-09-10"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	cookie := fileSQLiteLogin(t, a)
	handler := a.scopedAPI()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			responses <- concurrentSprintRequest(handler, cookie, http.MethodPatch, fmt.Sprintf("/api/sprints/%d", id), `{"status":"进行中"}`)
		}()
	}
	close(start)
	finished := make(chan struct{})
	go func() {
		wait.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent sprint patches did not finish")
	}
	close(responses)
	var succeeded int
	for response := range responses {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		default:
			t.Fatalf("concurrent sprint patch: %d %s", response.Code, response.Body.String())
		}
	}
	if succeeded != 2 {
		t.Fatalf("patch results success=%d, want 2", succeeded)
	}
	if count := assignmentNoticeCount(t, a, "sprint.status_changed", "sprint", id, "u_admin"); count != 1 {
		t.Fatalf("concurrent patch wrote %d status notifications", count)
	}
	var outbox int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type='sprint.status_changed' AND payload LIKE ?`, tenantID, projectID, fmt.Sprintf("%%\"subjectId\":%d%%", id)).Scan(&outbox); err != nil || outbox != 1 {
		t.Fatalf("concurrent patch outbox count=%d err=%v", outbox, err)
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM sprints WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, projectID).Scan(&status); err != nil || status != "进行中" {
		t.Fatalf("final sprint status=%q err=%v", status, err)
	}
}

func TestSprintCompleteConcurrentRetryHasOneWinnerAndOneNotification(t *testing.T) {
	a := fileSQLiteTestApp(t)
	w := apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", projectID, `{"name":"并发完成迭代","status":"进行中","startDate":"2026-09-04","endDate":"2026-09-10"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	cookie := fileSQLiteLogin(t, a)
	handler := a.scopedAPI()
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			responses <- concurrentSprintRequest(handler, cookie, http.MethodPost, fmt.Sprintf("/api/sprints/%d/complete", id), `{"targetSprint":"待规划"}`)
		}()
	}
	close(start)
	finished := make(chan struct{})
	go func() {
		wait.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent sprint completions did not finish")
	}
	close(responses)
	var succeeded, conflict int
	for response := range responses {
		switch response.Code {
		case http.StatusOK:
			succeeded++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("concurrent sprint completion: %d %s", response.Code, response.Body.String())
		}
	}
	if succeeded != 1 || conflict != 1 {
		t.Fatalf("completion results success=%d conflict=%d, want 1/1", succeeded, conflict)
	}
	for _, recipient := range []string{"u_admin"} {
		if count := assignmentNoticeCount(t, a, "sprint.completed", "sprint", id, recipient); count != 1 {
			t.Fatalf("completion recipient %s count=%d", recipient, count)
		}
	}
	for _, check := range []struct {
		table string
		where string
	}{
		{"notification_outbox", `event_type='sprint.completed'`},
		{"entity_activities", `object_type='sprint' AND event='completed'`},
		{"audit_logs", `object_type='sprint' AND action='complete'`},
	} {
		var count int
		query := `SELECT COUNT(*) FROM ` + check.table + ` WHERE tenant_id=? AND project_id=? AND ` + check.where
		if err := a.db.QueryRow(query, tenantID, projectID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", check.table, count, err)
		}
	}
}

func TestSprintStatusNotificationFailureRollsBackState(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/sprints", "u_admin", projectID, `{"name":"通知回滚迭代","startDate":"2026-09-04","endDate":"2026-09-10"}`)
	if w.Code != 201 {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	if _, err := a.db.Exec(`CREATE TRIGGER reject_sprint_status_notice BEFORE INSERT ON user_notifications WHEN NEW.event_type='sprint.status_changed' BEGIN SELECT RAISE(ABORT,'injected notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/sprints/%d", id), "u_admin", projectID, `{"status":"进行中"}`)
	if w.Code != 500 {
		t.Fatalf("notification failure returned %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, id).Scan(&status); err != nil || status != "规划中" {
		t.Fatalf("status was partially committed: %q err=%v", status, err)
	}
	if count := assignmentNoticeCount(t, a, "sprint.status_changed", "sprint", id, "u_admin"); count != 0 {
		t.Fatalf("failed status transition wrote inbox row=%d", count)
	}
}

func TestSprintCompletionNotificationFailureRollsBackState(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/sprints", "u_admin", projectID, `{"name":"完成通知回滚迭代","status":"进行中","startDate":"2026-09-04","endDate":"2026-09-10"}`)
	if w.Code != 201 {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	if _, err := a.db.Exec(`CREATE TRIGGER reject_sprint_completion_notice BEFORE INSERT ON user_notifications WHEN NEW.event_type='sprint.completed' BEGIN SELECT RAISE(ABORT,'injected notification failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", fmt.Sprintf("/api/sprints/%d/complete", id), "u_admin", projectID, `{"targetSprint":"待规划"}`)
	if w.Code != 500 {
		t.Fatalf("completion notification failure returned %d: %s", w.Code, w.Body.String())
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, id).Scan(&status); err != nil || status != "进行中" {
		t.Fatalf("completion was partially committed: %q err=%v", status, err)
	}
	for _, recipient := range []string{"u_admin"} {
		if count := assignmentNoticeCount(t, a, "sprint.completed", "sprint", id, recipient); count != 0 {
			t.Fatalf("failed completion wrote inbox for %s: %d", recipient, count)
		}
	}
}
