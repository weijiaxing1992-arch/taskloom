package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const wxTestApp = "wx0123456789abcdef"
const wxTestSecret = "0123456789abcdef0123456789abcdef"

func wxRequest(a *App, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		if cookie != nil {
			r.AddCookie(cookie)
		}
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}
func wxCookie(t *testing.T, a *App, user string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	c, err := a.issueSession(w, httptest.NewRequest("GET", "https://app.example/", nil), user)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func wxFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a := testApp(t)
	a.cookieSecure = true
	a.wecomKey = bytes.Repeat([]byte{67}, 32)
	cookie := wxCookie(t, a, "u_admin")
	body := jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "secret": wxTestSecret, "enabled": true, "version": 0})
	w := wxRequest(a, "PATCH", "/api/organization/wechat-login", body, cookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a.wechatHTTP = &http.Client{Transport: wecomRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.weixin.qq.com" || r.URL.Path != "/sns/oauth2/access_token" || r.URL.Query().Get("appid") != wxTestApp || r.URL.Query().Get("secret") != wxTestSecret {
			t.Fatal("unexpected provider request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"openid":"verified-openid","access_token":"must-not-persist","refresh_token":"must-not-persist"}`)), Header: make(http.Header)}, nil
	})}
	return a, cookie
}
func wxStart(t *testing.T, a *App, purpose string, cookie *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	path := "/api/auth/wechat/start"
	body := "{}"
	if purpose == "bind" {
		path = "/api/profile/wechat/bind"
		body = jsonText(map[string]any{"password": seedPassword})
	}
	w := wxRequest(a, "POST", path, body, cookie)
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	location, err := url.Parse(jsonMap(t, w)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := location.Query()
	if location.Host != "open.weixin.qq.com" || q.Get("scope") != "snsapi_login" || q.Get("redirect_uri") != "https://app.example"+wechatCallbackPath {
		t.Fatal("invalid official authorization URL")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == wechatStateCookie {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != wechatCallbackPath {
				t.Fatal("unsafe OAuth cookie")
			}
			return q.Get("state"), c
		}
	}
	t.Fatal("missing browser cookie")
	return "", nil
}
func wxCallback(a *App, state string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return wxRequest(a, "GET", wechatCallbackPath+"?state="+state+"&code=test-code", "", cookies...)
}

func TestWechatBindLoginReplayAndUnbind(t *testing.T) {
	a, _ := wxFixture(t)
	owner := wxCookie(t, a, "u_front")
	state, browser := wxStart(t, a, "bind", owner)
	wrong := *browser
	wrong.Value = strings.Repeat("a", 64)
	if w := wxCallback(a, state, owner, &wrong); !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("wrong browser accepted")
	}
	w := wxCallback(a, state, owner, browser)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "wechat=bound") {
		t.Fatalf("binding failed %d %s", w.Code, w.Header().Get("Location"))
	}
	if w = wxCallback(a, state, owner, browser); !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("callback replay accepted")
	}
	var stored string
	if err := a.db.QueryRow("SELECT identity_hash FROM user_wechat_bindings WHERE user_id='u_front'").Scan(&stored); err != nil || stored == "verified-openid" {
		t.Fatal("identity must be hashed")
	}
	state, browser = wxStart(t, a, "login", nil)
	w = wxCallback(a, state, browser)
	if w.Code != 303 || w.Header().Get("Location") != "/projects" {
		t.Fatal(w.Header().Get("Location"))
	}
	var loginCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			loginCookie = c
		}
	}
	if loginCookie == nil || !loginCookie.HttpOnly || !loginCookie.Secure {
		t.Fatal("missing secure login session")
	}
	if got := wxRequest(a, "GET", "/api/session", "", loginCookie); got.Code != 200 || !strings.Contains(got.Body.String(), `"id":"u_front"`) {
		t.Fatal("wrong login identity")
	}
	// 同一微信不能绑定到第二个企业人员，且不能覆盖原绑定。
	second := wxCookie(t, a, "u_back")
	state, browser = wxStart(t, a, "bind", second)
	if w = wxCallback(a, state, second, browser); !strings.Contains(w.Header().Get("Location"), "conflict") {
		t.Fatal("duplicate binding accepted")
	}
	w = wxRequest(a, "POST", "/api/profile/wechat/unbind", jsonText(map[string]any{"password": seedPassword}), owner)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if got := wxRequest(a, "GET", "/api/session", "", loginCookie); got.Code != 401 {
		t.Fatal("old browser session survived unlink")
	}
	if got := wxRequest(a, "GET", "/api/profile/wechat", "", owner); got.Code != 200 || jsonMap(t, got)["bound"] != false {
		t.Fatal("binding not removed")
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM audit_logs WHERE after_json LIKE '%must-not-persist%' OR after_json LIKE '%verified-openid%' OR after_json LIKE ?", "%"+wxTestSecret+"%").Scan(&n)
	if n != 0 {
		t.Fatal("provider credentials leaked to audit")
	}
}

func TestWechatSettingsEncryptionPermissionsAndVersion(t *testing.T) {
	a, admin := wxFixture(t)
	for _, user := range []string{"u_front", "u_viewer"} {
		if w := wxRequest(a, "GET", "/api/organization/wechat-login", "", wxCookie(t, a, user)); w.Code != 403 {
			t.Fatalf("settings exposed to %s", user)
		}
	}
	w := wxRequest(a, "GET", "/api/organization/wechat-login", "", admin)
	if w.Code != 200 || strings.Contains(w.Body.String(), wxTestSecret) || jsonMap(t, w)["secretConfigured"] != true {
		t.Fatal("secret response unsafe")
	}
	s, err := readWechatSettings(context.Background(), a.db)
	if err != nil || bytes.Contains(s.Encrypted, []byte(wxTestSecret)) {
		t.Fatal("secret not encrypted")
	}
	value, err := wechatSecret(a.wecomKey, s.Encrypted, "", false)
	if err != nil || string(value) != wxTestSecret {
		t.Fatal("secret decrypt failed")
	}
	if _, err = openAIKey(a.wecomKey, s.Encrypted); err == nil {
		t.Fatal("secret accepted under AI AAD")
	}
	if _, err = a.initializeWecomKey(filepath.Join(t.TempDir(), "missing-key")); err == nil {
		t.Fatal("missing key silently replaced")
	}
	bad := jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "enabled": true, "version": 0})
	if w = wxRequest(a, "PATCH", "/api/organization/wechat-login", bad, admin); w.Code != 409 {
		t.Fatal("stale settings accepted")
	}
	for _, origin := range []string{"http://app.example", "https://app.example/evil", "https://user:pass@app.example", "https://127.0.0.1", "https://app.example?x=1", "https://app.example#x", "https://app.example:8443"} {
		body := jsonText(map[string]any{"appId": wxTestApp, "origin": origin, "enabled": true, "version": 1})
		if w = wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 422 {
			t.Fatalf("accepted origin %s", origin)
		}
	}
	// 管理配置更新会作废未完成扫码。
	state, browser := wxStart(t, a, "login", nil)
	body := jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "enabled": false, "version": 1})
	if w = wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = wxCallback(a, state, browser); !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("old state survived setting changes")
	}
}

func TestWechatNoAutoProvisionAndDisabledAccounts(t *testing.T) {
	a, _ := wxFixture(t)
	var before int
	a.db.QueryRow("SELECT count(*) FROM users").Scan(&before)
	state, browser := wxStart(t, a, "login", nil)
	w := wxCallback(a, state, browser)
	if !strings.Contains(w.Header().Get("Location"), "unbound") {
		t.Fatal("unbound identity accepted")
	}
	var after int
	a.db.QueryRow("SELECT count(*) FROM users").Scan(&after)
	if after != before {
		t.Fatal("auto provisioned stranger")
	}
	_, err := a.db.Exec("INSERT INTO user_wechat_bindings VALUES(?,?,?,?,?)", tenantID, "u_front", wxTestApp, tokenDigest(wxTestApp+":verified-openid"), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"active=0", "active=1,operation_disabled=1"} {
		a.db.Exec("UPDATE users SET " + change + " WHERE id='u_front'")
		state, browser = wxStart(t, a, "login", nil)
		w = wxCallback(a, state, browser)
		if !strings.Contains(w.Header().Get("Location"), "unbound") {
			t.Fatal("disabled account logged in")
		}
	}
	// 首次改密不是普通业务权限：扫码仅建立受限会话，仍必须经过强制改密页面。
	a.db.Exec("UPDATE users SET active=1,operation_disabled=0,must_change_password=1 WHERE id='u_front'")
	state, browser = wxStart(t, a, "login", nil)
	w = wxCallback(a, state, browser)
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			session := wxRequest(a, "GET", "/api/session", "", cookie)
			if !strings.Contains(session.Body.String(), `"mustChangePassword":true`) {
				t.Fatal("initial password gate bypass")
			}
			if got := wxRequest(a, "GET", "/api/requirements", "", cookie); got.Code == 200 {
				t.Fatal("business access before password change")
			}
			return
		}
	}
	t.Fatal("restricted login session missing")
}

func TestWechatBindReauthExpiryAndExternalFailure(t *testing.T) {
	a, _ := wxFixture(t)
	owner := wxCookie(t, a, "u_front")
	if w := wxRequest(a, "POST", "/api/profile/wechat/bind", `{"password":"wrong"}`, owner); w.Code != 403 {
		t.Fatal("binding without password accepted")
	}
	state, browser := wxStart(t, a, "bind", owner)
	a.db.Exec("UPDATE users SET password_hash='changed' WHERE id='u_front'")
	if w := wxCallback(a, state, owner, browser); !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("password change race accepted")
	}
	state, browser = wxStart(t, a, "login", nil)
	a.db.Exec("UPDATE wechat_oauth_states SET expires_at=0")
	if w := wxCallback(a, state, browser); !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("expired QR accepted")
	}
	a.wechatHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errcode":40029,"errmsg":"private-provider-debug"}`))}, nil
	})}
	state, browser = wxStart(t, a, "login", nil)
	w := wxCallback(a, state, browser)
	if !strings.Contains(w.Header().Get("Location"), "failed") || strings.Contains(w.Body.String(), "private-provider-debug") {
		t.Fatal("provider failure unsafe")
	}
	if replay := wxCallback(a, state, browser); !strings.Contains(replay.Header().Get("Location"), "expired") {
		t.Fatal("failed callback still reusable")
	}
}

func TestWechatOriginMethodAndRateGuards(t *testing.T) {
	a, _ := wxFixture(t)
	r := httptest.NewRequest("POST", "https://app.example/api/auth/wechat/start", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://attacker.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site request accepted")
	}
	r.Header.Del("Origin")
	w = httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing origin accepted")
	}
	if w = wxRequest(a, "GET", "/api/auth/wechat/start", ""); w.Code != 405 {
		t.Fatal("GET created login state")
	}
	owner := wxCookie(t, a, "u_front")
	for i := 0; i < 10; i++ {
		w = wxRequest(a, "POST", "/api/profile/wechat/bind", `{"password":"wrong"}`, owner)
		if w.Code != 403 {
			t.Fatalf("unexpected attempt %d: %d", i, w.Code)
		}
	}
	if w = wxRequest(a, "POST", "/api/profile/wechat/bind", `{"password":"wrong"}`, owner); w.Code != 429 {
		t.Fatal("password attempts not rate limited")
	}
}

func TestWechatImpersonationAndInFlightAccountRevocation(t *testing.T) {
	a, admin := wxFixture(t)
	w := impersonationRequest(a, admin, "POST", "/api/auth/impersonation", `{"userId":"u_front","reason":"test account support"}`, projectID)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/api/profile/wechat", "/api/organization/wechat-login"} {
		if got := wxRequest(a, "GET", path, "", admin); got.Code != 403 {
			t.Fatalf("impersonation accessed %s: %d", path, got.Code)
		}
	}
	if got := wxRequest(a, "POST", "/api/profile/wechat/bind", jsonText(map[string]string{"password": seedPassword}), admin); got.Code != 403 {
		t.Fatal("impersonated binding accepted")
	}
	// 在微信兑换期间停用账号，回调不得利用此前的会话快照建立绑定。
	owner := wxCookie(t, a, "u_back")
	state, browser := wxStart(t, a, "bind", owner)
	a.wechatHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		if _, err := a.db.Exec("UPDATE users SET active=0 WHERE id='u_back'"); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"openid":"verified-openid"}`))}, nil
	})}
	w = wxCallback(a, state, owner, browser)
	if !strings.Contains(w.Header().Get("Location"), "expired") {
		t.Fatal("revocation race accepted")
	}
	var n int
	a.db.QueryRow("SELECT count(*) FROM user_wechat_bindings WHERE user_id='u_back'").Scan(&n)
	if n != 0 {
		t.Fatal("revoked account was bound")
	}
}

func TestWechatCannotSwapApplicationWithBindings(t *testing.T) {
	a, admin := wxFixture(t)
	_, err := a.db.Exec("INSERT INTO user_wechat_bindings VALUES(?,?,?,?,?)", tenantID, "u_front", wxTestApp, "test-identity", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	body := jsonText(map[string]any{"appId": "wxabcdef0123456789", "origin": "https://app.example", "secret": wxTestSecret, "enabled": true, "version": 1})
	if w := wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 409 {
		t.Fatal("application changed with existing bindings")
	}
	// 配置 Secret 保留与明确清除是不同操作。
	body = jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "secret": "", "enabled": true, "version": 1})
	if w := wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	settings, err := readWechatSettings(context.Background(), a.db)
	if err != nil || len(settings.Encrypted) == 0 {
		t.Fatal("blank input cleared secret")
	}
	body = jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "clearSecret": true, "enabled": true, "version": 2})
	if w := wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 422 {
		t.Fatal("enabled without secret")
	}
	body = jsonText(map[string]any{"appId": wxTestApp, "origin": "https://app.example", "clearSecret": true, "enabled": false, "version": 2})
	if w := wxRequest(a, "PATCH", "/api/organization/wechat-login", body, admin); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	settings, _ = readWechatSettings(context.Background(), a.db)
	if len(settings.Encrypted) != 0 || settings.Enabled {
		t.Fatal("clear did not disable")
	}
}

func TestWechatRemovalReleasesIdentityButSuspensionPreservesIt(t *testing.T) {
	for _, remove := range []string{"UPDATE tenant_memberships SET status='removed' WHERE user_id='u_front'", "DELETE FROM tenant_memberships WHERE user_id='u_front'", "DELETE FROM users WHERE id='u_front'"} {
		t.Run(remove, func(t *testing.T) {
			a, _ := wxFixture(t)
			if _, err := a.db.Exec("INSERT INTO user_wechat_bindings VALUES(?,?,?,?,?)", tenantID, "u_front", wxTestApp, "test-identity", time.Now().UTC().Format(time.RFC3339)); err != nil {
				t.Fatal(err)
			}
			if _, err := a.db.Exec("UPDATE tenant_memberships SET status='disabled' WHERE user_id='u_front'"); err != nil {
				t.Fatal(err)
			}
			var n int
			a.db.QueryRow("SELECT count(*) FROM user_wechat_bindings WHERE user_id='u_front'").Scan(&n)
			if n != 1 {
				t.Fatal("suspension lost binding")
			}
			if _, err := a.db.Exec(remove); err != nil {
				t.Fatal(err)
			}
			a.db.QueryRow("SELECT count(*) FROM user_wechat_bindings WHERE user_id='u_front'").Scan(&n)
			if n != 0 {
				t.Fatal("removed account retained identity")
			}
		})
	}
}

func TestWechatCancelledAndMalformedCodeNeverReachProvider(t *testing.T) {
	a, _ := wxFixture(t)
	a.wechatHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid callback reached provider")
		return nil, nil
	})}
	state, browser := wxStart(t, a, "login", nil)
	w := wxRequest(a, "GET", wechatCallbackPath+"?state="+state, "", browser)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "wechat=cancelled") {
		t.Fatal("missing cancellation feedback")
	}
	if replay := wxCallback(a, state, browser); !strings.Contains(replay.Header().Get("Location"), "expired") {
		t.Fatal("cancelled state still usable")
	}
	state, browser = wxStart(t, a, "login", nil)
	w = wxRequest(a, "GET", wechatCallbackPath+"?state="+state+"&code="+url.QueryEscape("<script>bad</script>"), "", browser)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "wechat=failed") {
		t.Fatal("malformed code accepted")
	}
	if strings.Contains(w.Body.String(), "<script>") {
		t.Fatal("callback reflected code")
	}
}
