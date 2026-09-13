package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func wecomAppDeliverySettingsBody(version int, delivery bool) string {
	return jsonText(map[string]any{
		"corpId": wecomCustomTestCorpID, "agentId": wecomCustomTestAgentID, "origin": "https://app.example",
		"verifyFilename": wecomCustomTestVerifyFile, "secret": wecomCustomTestSecret, "enabled": true,
		"notificationDeliveryEnabled": delivery, "version": version,
	})
}

func bindWecomAppDeliveryUser(t *testing.T, a *App, user, wecomUserID string) wecomAppSettings {
	t.Helper()
	settings, err := readWecomAppSettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.bindWecomAppIdentity(context.Background(), tx, user, settings, wecomUserID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return settings
}

func wecomAppDeliveryFixture(t *testing.T, delivery bool) (*App, *http.Cookie, wecomAppSettings) {
	t.Helper()
	a, admin := wecomCustomFixture(t)
	if w := wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", wecomAppDeliverySettingsBody(1, delivery), admin); w.Code != http.StatusOK {
		t.Fatalf("enable custom app delivery: %d %s", w.Code, w.Body.String())
	}
	settings := bindWecomAppDeliveryUser(t, a, "u_front", "front.user")
	a.wecomAppTokens = &wecomAppTokenCache{}
	return a, admin, settings
}

func wecomAppDeliveryStatus(t *testing.T, a *App, notificationID int64) (string, string, int) {
	t.Helper()
	var status, code string
	var attempts int
	if err := a.db.QueryRow(`SELECT status,last_error,attempts FROM wecom_app_deliveries WHERE tenant_id=? AND notification_id=?`, tenantID, notificationID).Scan(&status, &code, &attempts); err != nil {
		t.Fatal(err)
	}
	return status, code, attempts
}

func TestWecomAppNotificationDeliveryRequiresExplicitToggleAndMockNeverCallsProvider(t *testing.T) {
	a, _, _ := wecomAppDeliveryFixture(t, false)
	first := insertWecomNotice(t, a)
	var queued int
	if err := a.db.QueryRow(`SELECT count(*) FROM wecom_app_deliveries WHERE notification_id=?`, first).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("delivery queued without explicit toggle: %d %v", queued, err)
	}

	// 重新开启投递后只排队新通知；模拟模式即使绑定有效也绝不调用外部 HTTP。
	admin := wxCookie(t, a, "u_admin")
	if w := wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", wecomAppDeliverySettingsBody(2, true), admin); w.Code != http.StatusOK {
		t.Fatalf("enable delivery: %d %s", w.Code, w.Body.String())
	}
	// 配置版本变更会要求重新绑定，不能偷偷复用旧密文。
	bindWecomAppDeliveryUser(t, a, "u_front", "front.user")
	id := insertWecomNotice(t, a)
	t.Setenv("DEVFLOW_WECOM_MODE", "mock")
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("mock mode must not call enterprise WeChat")
		return nil, fmt.Errorf("unexpected external call")
	})}
	if err := a.processWecomAppDelivery(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if status, code, _ := wecomAppDeliveryStatus(t, a, id); status != "mock_sent" || code != "" {
		t.Fatalf("mock delivery not safely completed: %s %q", status, code)
	}
	if err := a.processWecomAppDelivery(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := wecomAppDeliveryStatus(t, a, id); status != "mock_sent" {
		t.Fatal("completed mock delivery was replayed")
	}
}

func TestWecomAppDeliveryUsesBoundIdentityAndSafeOfficialMessage(t *testing.T) {
	a, _, settings := wecomAppDeliveryFixture(t, true)
	id := insertWecomNotice(t, a)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	calls := 0
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "qyapi.weixin.qq.com" {
			t.Fatal("non-official enterprise WeChat host")
		}
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.Method != http.MethodGet || r.URL.Query().Get("corpid") != wecomCustomTestCorpID || r.URL.Query().Get("corpsecret") != wecomCustomTestSecret {
				t.Fatal("invalid access-token request")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errcode":0,"access_token":"safe_token_123","expires_in":7200}`))}, nil
		case "/cgi-bin/message/send":
			if r.Method != http.MethodPost || r.URL.Query().Get("access_token") != "safe_token_123" {
				t.Fatal("invalid message request")
			}
			var payload struct {
				ToUser  string `json:"touser"`
				MsgType string `json:"msgtype"`
				AgentID int64  `json:"agentid"`
				Text    struct {
					Content string `json:"content"`
				} `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.ToUser != "front.user" || payload.MsgType != "text" || payload.AgentID != 1000001 {
				t.Fatalf("unexpected safe payload: %#v", payload)
			}
			for _, want := range []string{"通知分类：提及与回复", "标题：你在评论中被提及", "摘要：@沈星 请检查具体内容", "查看详情：https://app.example/requirements?req=1&project=" + projectID} {
				if !strings.Contains(payload.Text.Content, want) {
					t.Fatalf("message omitted %q: %s", want, payload.Text.Content)
				}
			}
			if strings.Contains(payload.Text.Content, wecomCustomTestSecret) {
				t.Fatal("secret leaked in message")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errcode":0}`))}, nil
		default:
			t.Fatalf("unexpected official endpoint %s", r.URL.Path)
			return nil, nil
		}
	})}
	if err := a.processWecomAppDelivery(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if status, code, _ := wecomAppDeliveryStatus(t, a, id); status != "sent" || code != "" || calls != 2 {
		t.Fatalf("live delivery failed: status=%s code=%s calls=%d", status, code, calls)
	}
	var encrypted []byte
	if err := a.db.QueryRow(`SELECT encrypted_user_id FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("front.user")) {
		t.Fatalf("UserId was not stored encrypted: %v", err)
	}
	if user, err := decryptWecomAppUserID(a.wecomKey, tenantID, "u_front", settings.CorpID, encrypted); err != nil || user != "front.user" {
		t.Fatalf("cannot decrypt own secure UserId: %q %v", user, err)
	}
	if _, err := decryptWecomAppUserID(a.wecomKey, tenantID, "u_back", settings.CorpID, encrypted); err == nil {
		t.Fatal("encrypted UserId accepted for another TaskLoom account")
	}
}

func TestWecomAppQueuesEveryNotificationCategoryAndStopsOnRecipientChanges(t *testing.T) {
	a, _, _ := wecomAppDeliveryFixture(t, true)
	events := []string{"requirement.assigned", "requirement.mentioned", "requirement.status_changed", "defect.assigned", "test.failed", "automation.requirement_status_changed", "user.password_changed", "future.new_event"}
	for _, event := range events {
		if _, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at)VALUES(?,?,?,'u_admin',?,'requirement',1,'完整分类通知','内容',?)`, tenantID, projectID, "u_front", event, orgNow()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM wecom_app_deliveries WHERE tenant_id=?`, tenantID).Scan(&count); err != nil || count != len(events) {
		t.Fatalf("custom app did not queue all notification categories: %d %v", count, err)
	}
	// 停用接收人后，投递任务仅标记跳过；不会请求外部接口，也不影响原站内通知。
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("disabled recipient must not be notified")
		return nil, nil
	})}
	if err := a.processWecomAppDelivery(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM wecom_app_deliveries WHERE tenant_id=? ORDER BY id LIMIT 1`, tenantID).Scan(&status); err != nil || status != "skipped" {
		t.Fatalf("recipient change not respected: %s %v", status, err)
	}
}

func TestWecomAppOAuthBindingExchangesOnlyOfficialIdentity(t *testing.T) {
	a, _ := wecomCustomFixture(t)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	a.wecomAppTokens = &wecomAppTokenCache{}
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "qyapi.weixin.qq.com" {
			t.Fatal("non-official host during OAuth exchange")
		}
		switch r.URL.Path {
		case "/cgi-bin/gettoken":
			if r.URL.Query().Get("corpsecret") != wecomCustomTestSecret {
				t.Fatal("secret not passed only to official token endpoint")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errcode":0,"access_token":"oauth_token_123","expires_in":7200}`))}, nil
		case "/cgi-bin/user/getuserinfo":
			if r.URL.Query().Get("access_token") != "oauth_token_123" || r.URL.Query().Get("code") != "verified-code" {
				t.Fatal("unsafe UserId exchange")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errcode":0,"UserId":"verified.user"}`))}, nil
		default:
			t.Fatalf("unexpected OAuth path %s", r.URL.Path)
			return nil, nil
		}
	})}
	owner := wxCookie(t, a, "u_front")
	w := wxRequest(a, http.MethodPost, "/api/profile/wecom-app/bind", jsonText(map[string]any{"password": seedPassword}), owner)
	if w.Code != http.StatusOK {
		t.Fatalf("start enterprise WeChat binding: %d %s", w.Code, w.Body.String())
	}
	location, err := url.Parse(jsonMap(t, w)["url"].(string))
	if err != nil || location.Host != "open.weixin.qq.com" || location.Path != "/connect/oauth2/authorize" || location.Query().Get("redirect_uri") != "https://app.example"+wecomAppCallbackPath || location.Query().Get("scope") != "snsapi_base" {
		t.Fatalf("invalid official binding authorization URL: %q %v", location, err)
	}
	var browser *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == wecomAppStateCookie {
			browser = cookie
			if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != wecomAppCallbackPath {
				t.Fatalf("unsafe binding cookie: %#v", cookie)
			}
		}
	}
	if browser == nil {
		t.Fatal("binding state cookie missing")
	}
	callback := wxRequest(a, http.MethodGet, wecomAppCallbackPath+"?state="+url.QueryEscape(location.Query().Get("state"))+"&code=verified-code", "", owner, browser)
	if callback.Code != http.StatusSeeOther || !strings.Contains(callback.Header().Get("Location"), "wecom=bound") {
		t.Fatalf("binding callback failed: %d %s", callback.Code, callback.Header().Get("Location"))
	}
	var encrypted []byte
	if err := a.db.QueryRow(`SELECT encrypted_user_id FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("verified.user")) {
		t.Fatalf("official UserId not encrypted: %v", err)
	}
}
