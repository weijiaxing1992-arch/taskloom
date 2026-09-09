package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWecomEveryNotificationCategoryQueuesIncludingAutomation(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	events := []string{"requirement.assigned", "requirement.owner_assigned", "requirement.role_assigned", "requirement.mentioned", "requirement.description_mentioned", "requirement.remarks_mentioned", "requirement.replied", "requirement.status_changed", "requirement.updated", "requirement.backend_completed", "requirement.frontend_completed", "defect.assigned", "defect.assignee_changed", "defect.verifier_assigned", "defect.verifier_changed", "defect.mentioned", "defect.replied", "defect.status_changed", "defect.created_from_execution", "sprint.status_changed", "sprint.completed", "test.failed", "test_case.owner_assigned", "test_case.owner_changed", "test_case.mentioned", "test_case.replied", "test_plan.owner_assigned", "test_plan.owner_changed", "test_plan.executor_assigned", "test_plan.executor_changed", "test_plan.mentioned", "test_plan.replied", "test_execution.mentioned", "test_execution.replied", "automation.requirement_status_changed", "user.password_changed", "future.new_event"}
	for _, event := range events {
		_, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,event_type,subject_type,subject_id,title,body,created_at)VALUES(?,?,'u_front',?,'requirement',1,'分类完整通知','内容',?)`, tenantID, projectID, event, orgNow())
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	a.db.QueryRow(`SELECT count(*) FROM user_wecom_deliveries`).Scan(&count)
	if count != len(events) {
		t.Fatalf("queued %d want %d", count, len(events))
	}
	if err := a.migrateUserWecom(); err != nil {
		t.Fatal(err)
	}
	a.db.QueryRow(`SELECT count(*) FROM user_wecom_deliveries`).Scan(&count)
	if count != len(events) {
		t.Fatal("migration duplicated notices")
	}
}

func TestWecomLongUnicodeNoticeCompleteAndResumesOnlyFailedPart(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	body := strings.Repeat("完整中文😀\n\tcode <literal>\n", 250) + "结尾必须保留"
	a.db.Exec(`UPDATE user_notifications SET body=? WHERE id=?`, body, id)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	t.Setenv("DEVFLOW_PUBLIC_URL", "https://devflow.example")
	received := []string{}
	attempts := 0
	failSecond := true
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		attempts++
		if attempts == 2 && failSecond {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		if len(payload.Text.Content) > 2048 || !utf8.ValidString(payload.Text.Content) {
			t.Fatal("invalid UTF8/size")
		}
		if !strings.Contains(payload.Text.Content, "提及与回复") || !strings.Contains(payload.Text.Content, "查看详情：https://devflow.example/requirements?req=1&project="+projectID) {
			t.Fatal("missing group/link")
		}
		received = append(received, payload.Text.Content)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
	})}
	now := time.Now().Add(time.Minute)
	if err := a.processUserWecom(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus(t, a, id) != "pending" {
		t.Fatal("partial marked sent")
	}
	a.db.Exec(`UPDATE user_notifications SET body='后续变动不得改写已生成的分段' WHERE id=?`, id)
	if err := a.processUserWecom(context.Background(), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus(t, a, id) != "retry" {
		t.Fatal("failed chunk not retryable")
	}
	failSecond = false
	for i := 1; i <= 100 && deliveryStatus(t, a, id) != "sent"; i++ {
		if err := a.processUserWecom(context.Background(), now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if deliveryStatus(t, a, id) != "sent" {
		t.Fatal("never completed")
	}
	joined := ""
	for _, part := range received {
		_, text, _ := strings.Cut(part, "\n\n")
		text, _, _ = strings.Cut(text, "\n\n通知时间：")
		joined += text
	}
	if !strings.Contains(joined, body) {
		t.Fatal("notification body truncated or changed across retries")
	}
	if strings.Contains(joined, "后续变动不得") {
		t.Fatal("snapshot overwritten")
	}
	if len(received) < 2 || attempts != len(received)+1 {
		t.Fatal("successful part repeated")
	}
	before := attempts
	a.processUserWecom(context.Background(), now.Add(24*time.Hour))
	if attempts != before {
		t.Fatal("completed delivery replayed")
	}
}

func TestWecomMultipartStopsAfterPermissionRevoked(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	a.db.Exec(`UPDATE user_notifications SET body=? WHERE id=?`, strings.Repeat("保密业务通知", 1000), id)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	calls := 0
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
	})}
	now := time.Now().Add(time.Minute)
	a.processUserWecom(context.Background(), now)
	a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID)
	a.processUserWecom(context.Background(), now.Add(time.Minute))
	if calls != 1 || deliveryStatus(t, a, id) != "skipped" {
		t.Fatal("revoked user received later parts")
	}
}

func TestWecomChunksKeepCodeWhitespaceAndRejectCredentialLinks(t *testing.T) {
	text := strings.Repeat("\tprintln(\"中文😀\")\n", 400)
	parts := splitWecomNotice(7, "changes", text, orgNow(), "defect", 3, "p", "https://user:secret@example.test")
	joined := ""
	for _, part := range parts {
		if len(part) > 2048 || !utf8.ValidString(part) || strings.Contains(part, "secret") {
			t.Fatal("invalid message")
		}
		_, body, _ := strings.Cut(part, "\n\n")
		body, _, _ = strings.Cut(body, "\n\n通知时间：")
		joined += body
	}
	if joined != text {
		t.Fatal("lost code whitespace")
	}
}
