package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const testWebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=01234567-89ab-cdef-0123-456789abcdef"

type wecomRoundTrip func(*http.Request) (*http.Response, error)

func (fn wecomRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func wecomApp(t *testing.T) *App {
	a := testApp(t)
	a.wecomKey = bytes.Repeat([]byte{17}, 32)
	return a
}
func setTestWebhook(t *testing.T, a *App) {
	t.Helper()
	orgRequest(t, a, "PATCH", "/api/profile/wecom-webhook", "u_front", map[string]any{"url": testWebhookURL, "enabled": true}, 200)
}
func insertWecomNotice(t *testing.T, a *App) int64 {
	t.Helper()
	r, e := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at)VALUES(?,?,?,'u_admin','requirement.mentioned','requirement',1,?,?,?)`, tenantID, projectID, "u_front", "你在评论中被提及", "@沈星 请检查具体内容", orgNow())
	if e != nil {
		t.Fatal(e)
	}
	id, _ := r.LastInsertId()
	return id
}
func deliveryStatus(t *testing.T, a *App, id int64) string {
	t.Helper()
	var s string
	if e := a.db.QueryRow(`SELECT status FROM user_wecom_deliveries WHERE notification_id=?`, id).Scan(&s); e != nil {
		t.Fatal(e)
	}
	return s
}

func TestUserWecomURLAndEncryptionBoundaries(t *testing.T) {
	for _, raw := range []string{"http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=01234567890123456789", "https://qyapi.weixin.qq.com.evil.test/cgi-bin/webhook/send?key=01234567890123456789", testWebhookURL + "&other=1", testWebhookURL + "&key=duplicate", testWebhookURL + "#fragment", strings.Replace(testWebhookURL, ".com/", ".com:443/", 1), strings.Replace(testWebhookURL, "https://", "https://user@", 1), "https://127.0.0.1/private"} {
		if _, e := validateWecomURL(raw); e == nil {
			t.Fatalf("accepted invalid URL %s", raw)
		}
	}
	key := bytes.Repeat([]byte{1}, 32)
	encrypted, e := encryptWebhook(key, tenantID, "u_front", testWebhookURL)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encrypted, []byte("01234567")) {
		t.Fatal("plaintext stored")
	}
	actual, e := decryptWebhook(key, tenantID, "u_front", encrypted)
	if e != nil || actual != testWebhookURL {
		t.Fatalf("roundtrip %s %v", actual, e)
	}
	if _, e = decryptWebhook(key, tenantID, "u_back", encrypted); e == nil {
		t.Fatal("ciphertext accepted for different member")
	}
	if _, e = decryptWebhook(bytes.Repeat([]byte{2}, 32), tenantID, "u_front", encrypted); e == nil {
		t.Fatal("wrong key accepted")
	}
}
func TestUserWecomKeyIsPrivateAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot.key")
	first, e := loadWecomEncryptionKey(path)
	if e != nil {
		t.Fatal(e)
	}
	second, e := loadWecomEncryptionKey(path)
	if e != nil || !bytes.Equal(first, second) || len(first) != 32 {
		t.Fatal("key changed")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = loadWecomEncryptionKey(path); e == nil {
		t.Fatal("readable key accepted")
	}
}
func TestUserWecomConfigurationAuthorizationAndRedaction(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	own := orgRequest(t, a, "GET", "/api/profile/wecom-webhook", "u_front", nil, 200)
	raw, _ := json.Marshal(own)
	if strings.Contains(string(raw), "01234567") || own["configured"] != true || own["enabled"] != true {
		t.Fatal("bad/redacted configuration")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front/wecom-webhook", "u_back", map[string]any{"clear": true}, 403)
	orgRequest(t, a, "GET", "/api/organization/members/u_front/wecom-webhook", "u_admin", nil, 200)
	var encrypted []byte
	if e := a.db.QueryRow(`SELECT encrypted_url FROM user_wecom_webhooks WHERE user_id='u_front'`).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encrypted, []byte(testWebhookURL)) {
		t.Fatal("secret plaintext")
	}
}
func TestUserWecomQueuesOnlyCommittedNewNotifications(t *testing.T) {
	a := wecomApp(t)
	insertWecomNotice(t, a)
	setTestWebhook(t, a)
	var n int
	a.db.QueryRow(`SELECT count(*) FROM user_wecom_deliveries`).Scan(&n)
	if n != 0 {
		t.Fatal("backfilled old messages")
	}
	id := insertWecomNotice(t, a)
	if deliveryStatus(t, a, id) != "pending" {
		t.Fatal("not queued")
	}
	tx, e := a.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,event_type,subject_type,subject_id,title,created_at)VALUES(?,?,'u_front','test.failed','test_case',1,'rollback',?)`, tenantID, projectID, orgNow())
	if e != nil {
		t.Fatal(e)
	}
	tx.Rollback()
	a.db.QueryRow(`SELECT count(*) FROM user_wecom_deliveries`).Scan(&n)
	if n != 1 {
		t.Fatal("rollback left queued delivery")
	}
}

func TestUserWecomSkipsBusinessSuspendedRecipient(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"operationDisabled": true}, 200)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("sent to business-suspended account")
		return nil, fmt.Errorf("unexpected request")
	})}
	if err := a.processUserWecom(context.Background(), time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus(t, a, id) != "skipped" {
		t.Fatal("suspended recipient not skipped")
	}
}
func TestUserWecomSendsTitleBodyTimeAndScopedLink(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	t.Setenv("DEVFLOW_PUBLIC_URL", "https://devflow.example")
	calls := 0
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != testWebhookURL || r.Method != "POST" {
			t.Fatal("wrong destination")
		}
		var payload struct {
			Msgtype string `json:"msgtype"`
			Text    struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Fatal(e)
		}
		for _, part := range []string{"你在评论中被提及", "@沈星 请检查具体内容", "通知时间：", "/requirements?req=1&project=" + projectID} {
			if !strings.Contains(payload.Text.Content, part) {
				t.Fatalf("missing %s", part)
			}
		}
		if payload.Msgtype != "text" {
			t.Fatal("wrong format")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
	})}
	if e := a.processUserWecom(context.Background(), time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if deliveryStatus(t, a, id) != "sent" || calls != 1 {
		t.Fatal("not sent")
	}
	a.processUserWecom(context.Background(), time.Now().Add(time.Hour))
	if calls != 1 {
		t.Fatal("sent delivery repeated")
	}
}
func TestUserWecomMockRevocationAndGenerationSafety(t *testing.T) {
	for _, scenario := range []string{"mock", "disabled", "project_revoked", "generation"} {
		t.Run(scenario, func(t *testing.T) {
			a := wecomApp(t)
			setTestWebhook(t, a)
			id := insertWecomNotice(t, a)
			t.Setenv("DEVFLOW_WECOM_MODE", "live")
			want := "skipped"
			switch scenario {
			case "mock":
				t.Setenv("DEVFLOW_WECOM_MODE", "mock")
				want = "mock_sent"
			case "disabled":
				a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`)
			case "project_revoked":
				a.db.Exec(`DELETE FROM project_members WHERE user_id='u_front'`)
			case "generation":
				a.db.Exec(`UPDATE user_wecom_webhooks SET version=version+1 WHERE user_id='u_front'`)
			}
			a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected external send"); return nil, nil })}
			if e := a.processUserWecom(context.Background(), time.Now().Add(time.Second)); e != nil {
				t.Fatal(e)
			}
			if deliveryStatus(t, a, id) != want {
				t.Fatalf("wanted %s", want)
			}
		})
	}
}
func TestUserWecomClearRetainsMonotonicGeneration(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	orgRequest(t, a, "PATCH", "/api/profile/wecom-webhook", "u_front", map[string]any{"clear": true}, 200)
	setTestWebhook(t, a)
	var version int
	a.db.QueryRow(`SELECT version FROM user_wecom_webhooks WHERE user_id='u_front'`).Scan(&version)
	if version != 3 {
		t.Fatalf("generation %d", version)
	}
	if deliveryStatus(t, a, id) != "skipped" {
		t.Fatal("old queue still active")
	}
}
func TestUserWecomRetriesBoundedAndErrorsSanitized(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	calls := 0
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":45009,"errmsg":"secret should not be stored"}`))}, nil
	})}
	now := time.Now().Add(time.Hour)
	for i := 0; i < 7; i++ {
		if e := a.processUserWecom(context.Background(), now.Add(time.Duration(i)*time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	if calls != 6 || deliveryStatus(t, a, id) != "failed" {
		t.Fatalf("unbounded attempts %d", calls)
	}
	var message string
	a.db.QueryRow(`SELECT last_error FROM user_wecom_deliveries WHERE notification_id=?`, id).Scan(&message)
	if message != "rate_limited" {
		t.Fatal("unsanitized error")
	}
	a.db.Exec(`UPDATE user_wecom_deliveries SET status='sending',lease_until='2000-01-01T00:00:00Z' WHERE notification_id=?`, id)
	a.processUserWecom(context.Background(), now)
	if deliveryStatus(t, a, id) != "failed" {
		t.Fatal("exhausted lease never finished")
	}
}
func TestUserWecomTextLimitsAndRedirects(t *testing.T) {
	const stamp = "2026-09-03T04:05:06Z"
	footer := "\n\n通知时间：2026-09-03 12:05:06 UTC+08:00\n查看详情：https://example.test/requirements?req=1&project=" + projectID
	for _, text := range []string{strings.Repeat("内容", 2000), strings.Repeat("😀", 2000), strings.Repeat("ASCII", 1000)} {
		msg := wecomMessage(strings.Repeat("标题", 400), text, stamp, "requirement", 1, projectID, "https://example.test")
		if !utf8.ValidString(msg) || len(msg) > 2048 || len(msg) < 2045 || !strings.HasSuffix(msg, footer) {
			t.Fatalf("invalid bounded text/footer: %d bytes %q", len(msg), msg)
		}
	}
	msg := wecomMessage("title", strings.Repeat("正文", 2000), stamp, "requirement", 1, projectID, "https://example.test/"+strings.Repeat("long-origin/", 100))
	if !utf8.ValidString(msg) || len(msg) > 2048 || strings.Contains(msg, "查看详情") || !strings.HasSuffix(msg, "通知时间：2026-09-03 12:05:06 UTC+08:00") {
		t.Fatal("long link consumed the timestamp or produced a broken URL")
	}
	msg = wecomMessage("title", "body", orgNow(), "requirement", 1, projectID, "https://user:secret@example.test")
	if strings.Contains(msg, "secret") {
		t.Fatal("unsafe public URL")
	}
	if newWecomHTTPClient().CheckRedirect(nil, nil) == nil {
		t.Fatal("redirect allowed")
	}
}

func TestUserWecomRejectsMalformedSuccessResponses(t *testing.T) {
	for _, test := range []struct {
		name, body string
	}{
		{"missing", `{}`},
		{"null", `{"errcode":null}`},
		{"string", `{"errcode":"0"}`},
		{"fraction", `{"errcode":0.5}`},
		{"noninteger_number", `{"errcode":0.0}`},
		{"overflow", `{"errcode":999999999999999999999999}`},
		{"second_json", `{"errcode":0}{"errcode":45009}`},
		{"trailing_null", `{"errcode":0} null`},
		{"trailing_garbage", `{"errcode":0} unexpected`},
		{"truncated", `{"errcode":0`},
		{"oversized_whitespace", `{"errcode":0}` + strings.Repeat(" ", 16<<10)},
		{"garbage_after_read_limit", `{"errcode":0}` + strings.Repeat(" ", 16<<10) + `not JSON`},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := wecomApp(t)
			setTestWebhook(t, a)
			id := insertWecomNotice(t, a)
			t.Setenv("DEVFLOW_WECOM_MODE", "live")
			a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			if err := a.processUserWecom(context.Background(), time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var status, code, sent string
			if err := a.db.QueryRow(`SELECT status,last_error,sent_at FROM user_wecom_deliveries WHERE notification_id=?`, id).Scan(&status, &code, &sent); err != nil {
				t.Fatal(err)
			}
			if status != "retry" || code != "invalid_response" || sent != "" {
				t.Fatalf("invalid success accepted: status=%s code=%s sent=%s", status, code, sent)
			}
		})
	}
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(" \n{\"errcode\":0,\"errmsg\":\"ok\"}\r\n\t "))}, nil
	})}
	if err := a.processUserWecom(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if deliveryStatus(t, a, id) != "sent" {
		t.Fatal("valid response with trailing whitespace rejected")
	}
}

func TestUserWecomDelegatedManagerCannotRedirectPrivilegedNotifications(t *testing.T) {
	a := wecomApp(t)
	orgGroup(t, a, "成员维护", []string{"members.manage"}, []string{"u_back"})
	orgGroup(t, a, "统计授权", []string{"reports.view"}, []string{"u_front"})
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front/wecom-webhook", "u_back", map[string]any{"url": testWebhookURL, "enabled": true}, 403)
	setTestWebhook(t, a)
	path := filepath.Join(t.TempDir(), "missing.key")
	if _, err := a.initializeWecomKey(path); err == nil {
		t.Fatal("missing key replaced despite encrypted configuration")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("replacement key was written")
	}
}
