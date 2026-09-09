package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const wechatCallbackPath = "/api/auth/wechat/callback"
const wechatStateCookie = "devflow_wechat_oauth"

var wechatAppPattern = regexp.MustCompile(`^wx[0-9a-fA-F]{16}$`)
var wechatSecretPattern = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)
var wechatTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)

type wechatSettings struct {
	AppID     string
	Origin    string
	Encrypted []byte
	Enabled   bool
	Version   int
}

func (a *App) migrateWechatLogin() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS organization_wechat_settings(
 tenant_id TEXT PRIMARY KEY,app_id TEXT NOT NULL,origin TEXT NOT NULL,encrypted_secret BLOB NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS user_wechat_bindings(
 tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,app_id TEXT NOT NULL,identity_hash TEXT NOT NULL,created_at TEXT NOT NULL,
 PRIMARY KEY(tenant_id,user_id),UNIQUE(tenant_id,app_id,identity_hash));
 CREATE TABLE IF NOT EXISTS wechat_oauth_states(
 state_hash TEXT PRIMARY KEY,browser_hash TEXT NOT NULL,purpose TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',
 session_hash TEXT NOT NULL DEFAULT '',credential_hash TEXT NOT NULL DEFAULT '',config_version INTEGER NOT NULL,
 expires_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_wechat_state_expiry ON wechat_oauth_states(expires_at);
 CREATE TABLE IF NOT EXISTS wechat_auth_limits(key_hash TEXT PRIMARY KEY,window INTEGER NOT NULL,count INTEGER NOT NULL);
 CREATE TRIGGER IF NOT EXISTS wechat_cleanup_removed_member AFTER UPDATE OF status ON tenant_memberships
 WHEN NEW.status='removed' BEGIN
 DELETE FROM user_wechat_bindings WHERE tenant_id=NEW.tenant_id AND user_id=NEW.user_id;
 DELETE FROM wechat_oauth_states WHERE user_id=NEW.user_id;
 END;
 CREATE TRIGGER IF NOT EXISTS wechat_cleanup_deleted_membership AFTER DELETE ON tenant_memberships BEGIN
 DELETE FROM user_wechat_bindings WHERE tenant_id=OLD.tenant_id AND user_id=OLD.user_id;
 DELETE FROM wechat_oauth_states WHERE user_id=OLD.user_id;
 END;
 CREATE TRIGGER IF NOT EXISTS wechat_cleanup_deleted_user AFTER DELETE ON users BEGIN
 DELETE FROM user_wechat_bindings WHERE tenant_id=OLD.tenant_id AND user_id=OLD.id;
 DELETE FROM wechat_oauth_states WHERE user_id=OLD.id;
 END;`)
	return err
}

func readWechatSettings(ctx context.Context, q stateStore) (wechatSettings, error) {
	s := wechatSettings{Origin: "https://taskloom.example.com", Encrypted: []byte{}}
	err := q.QueryRowContext(ctx, "SELECT app_id,origin,encrypted_secret,enabled,version FROM organization_wechat_settings WHERE tenant_id=?", tenantID).Scan(&s.AppID, &s.Origin, &s.Encrypted, &s.Enabled, &s.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return s, err
}
func wechatSettingsView(s wechatSettings) map[string]any {
	return map[string]any{"appId": s.AppID, "origin": s.Origin, "callbackUrl": s.Origin + wechatCallbackPath, "enabled": s.Enabled, "secretConfigured": len(s.Encrypted) > 0, "version": s.Version}
}
func validWechatOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Host != "" && u.Port() == "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && net.ParseIP(u.Hostname()) == nil && strings.Contains(u.Hostname(), ".") && !strings.HasSuffix(u.Hostname(), ".localhost")
}

// 与 AI、机器人共享独立保存的主密钥，但使用不同 AAD，密文无法跨用途替换。
func wechatSecret(key, encrypted []byte, plain string, seal bool) ([]byte, error) {
	c, err := webhookCipher(key)
	if err != nil {
		return nil, errors.New("微信密钥不可用")
	}
	aad := []byte("devflow:wechat-login:v1:" + tenantID)
	if seal {
		nonce := make([]byte, c.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return nil, err
		}
		return c.Seal(nonce, nonce, []byte(plain), aad), nil
	}
	if len(encrypted) < c.NonceSize() {
		return nil, errors.New("微信密钥不可用")
	}
	return c.Open(nil, encrypted[:c.NonceSize()], encrypted[c.NonceSize():], aad)
}
func (a *App) wechatAdmin(ctx context.Context, q stateStore) error {
	if err := a.requireOperationAccess(ctx, q); err != nil {
		return err
	}
	if a.impersonation != nil {
		return &organizationError{403, "forbidden", "代访问期间不能配置微信登录"}
	}
	_, admin, err := a.organizationAccess(ctx, q)
	if err != nil {
		return err
	}
	if !admin {
		return &organizationError{403, "forbidden", "仅企业管理员可配置微信登录"}
	}
	return nil
}

// 涉及绑定和登录的写请求必须来自同源 JSON fetch，不接受跨站表单或无来源请求。
func (a *App) wechatJSON(w http.ResponseWriter, r *http.Request) bool {
	scheme := "http"
	if a.cookieSecure || r.TLS != nil {
		scheme = "https"
	}
	if r.Header.Get("Origin") != scheme+"://"+r.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "origin_rejected", "请在当前站点重新发起操作")
		return false
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, 415, "json_required", "请求必须使用 JSON")
		return false
	}
	return true
}
func wechatAudit(ctx context.Context, tx *sql.Tx, user, action string, details any) error {
	body, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at) VALUES(?,?,?,?,?,?,?, ?,?)", tenantID, "", user, "wechat_login", user, action, "{}", string(body), time.Now().UTC().Format(time.RFC3339))
	return err
}
func (a *App) organizationWechat(w http.ResponseWriter, r *http.Request) {
	if err := a.wechatAdmin(r.Context(), a.db); err != nil {
		failWechat(w, err)
		return
	}
	if r.Method == http.MethodGet {
		s, err := readWechatSettings(r.Context(), a.db)
		if err != nil {
			failWechat(w, err)
			return
		}
		write(w, 200, wechatSettingsView(s))
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wechatJSON(w, r) {
		return
	}
	var input struct {
		AppID       string `json:"appId"`
		Origin      string `json:"origin"`
		Secret      string `json:"secret"`
		ClearSecret bool   `json:"clearSecret"`
		Enabled     bool   `json:"enabled"`
		Version     int    `json:"version"`
	}
	if decodeOrganizationJSON(w, r, &input) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	input.AppID = strings.TrimSpace(input.AppID)
	input.Origin = strings.TrimRight(strings.TrimSpace(input.Origin), "/")
	if !wechatAppPattern.MatchString(input.AppID) || !validWechatOrigin(input.Origin) || (input.Secret != "" && !wechatSecretPattern.MatchString(input.Secret)) || (input.ClearSecret && input.Secret != "") {
		fail(w, 422, "invalid_wechat_settings", "请填写网站应用 AppID、有效 HTTPS 域名及 AppSecret")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWechat(w, err)
		return
	}
	defer tx.Rollback()
	// 先取得写锁，再重查权限、配置版本及绑定数量，防止并发换应用使绑定失效。
	if _, err = tx.ExecContext(r.Context(), "UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?", tenantID); err != nil {
		failWechat(w, err)
		return
	}
	if err = a.wechatAdmin(r.Context(), tx); err != nil {
		failWechat(w, err)
		return
	}
	s, err := readWechatSettings(r.Context(), tx)
	if err != nil {
		failWechat(w, err)
		return
	}
	if s.Version != input.Version {
		fail(w, 409, "settings_changed", "配置已变化，请刷新后重试")
		return
	}
	if s.AppID != "" && s.AppID != input.AppID {
		if input.Secret == "" && !input.ClearSecret {
			fail(w, 422, "new_app_secret_required", "更换 AppID 时请同时配置新的 AppSecret")
			return
		}
		var n int
		if err = tx.QueryRowContext(r.Context(), "SELECT count(*) FROM user_wechat_bindings WHERE tenant_id=?", tenantID).Scan(&n); err != nil {
			failWechat(w, err)
			return
		}
		if n > 0 {
			fail(w, 409, "wechat_app_bound", "已有微信绑定，不能直接更换 AppID；请先完成账号解绑或迁移")
			return
		}
	}
	if input.ClearSecret {
		s.Encrypted = []byte{}
	}
	if input.Secret != "" {
		s.Encrypted, err = wechatSecret(a.wecomKey, nil, input.Secret, true)
		if err != nil {
			fail(w, 503, "secret_unavailable", "微信密钥不可用，请检查服务器密钥文件")
			return
		}
	}
	if input.Enabled && len(s.Encrypted) == 0 {
		fail(w, 422, "secret_required", "启用微信登录前必须配置 AppSecret")
		return
	}
	s.AppID, s.Origin, s.Enabled, s.Version = input.AppID, input.Origin, input.Enabled, s.Version+1
	_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_wechat_settings(tenant_id,app_id,origin,encrypted_secret,enabled,version,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id) DO UPDATE SET app_id=excluded.app_id,origin=excluded.origin,encrypted_secret=excluded.encrypted_secret,enabled=excluded.enabled,version=excluded.version,updated_at=excluded.updated_at`, tenantID, s.AppID, s.Origin, s.Encrypted, s.Enabled, s.Version, time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM wechat_oauth_states")
	}
	if err == nil {
		err = wechatAudit(r.Context(), tx, a.uid(), "wechat_settings_updated", wechatSettingsView(s))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWechat(w, err)
		return
	}
	write(w, 200, wechatSettingsView(s))
}

func (a *App) wechatRate(r *http.Request, scope string) error {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key := tokenDigest("wechat:" + host + ":" + scope)
	window := time.Now().Unix() / 60
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), "INSERT INTO wechat_auth_limits(key_hash,window,count) VALUES(?,?,1) ON CONFLICT(key_hash) DO UPDATE SET count=CASE WHEN window=excluded.window THEN count+1 ELSE 1 END,window=excluded.window", key, window)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), "SELECT count FROM wechat_auth_limits WHERE key_hash=?", key).Scan(&count); err != nil {
		return err
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM wechat_auth_limits WHERE window<?", window-60); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	limit := 10
	if scope == "login" {
		limit = 60
	}
	if count > limit {
		return &organizationError{429, "rate_limited", "操作过于频繁，请稍后重试"}
	}
	return nil
}
func (a *App) wechatStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	s, err := readWechatSettings(r.Context(), a.db)
	if err != nil {
		failWechat(w, err)
		return
	}
	write(w, 200, map[string]any{"enabled": s.Enabled && len(s.Encrypted) > 0, "origin": s.Origin})
}
func (a *App) profileWechat(w http.ResponseWriter, r *http.Request) {
	if a.impersonation != nil {
		fail(w, 403, "forbidden", "代访问期间不能查看或更改微信绑定")
		return
	}
	if r.URL.Path == "/api/profile/wechat/bind" {
		a.startWechat(w, r, "bind")
		return
	}
	if r.URL.Path == "/api/profile/wechat/unbind" {
		a.unbindWechat(w, r)
		return
	}
	if r.URL.Path != "/api/profile/wechat" || r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	s, err := readWechatSettings(r.Context(), a.db)
	if err != nil {
		failWechat(w, err)
		return
	}
	var created string
	err = a.db.QueryRowContext(r.Context(), "SELECT created_at FROM user_wechat_bindings WHERE tenant_id=? AND user_id=?", tenantID, a.uid()).Scan(&created)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failWechat(w, err)
		return
	}
	write(w, 200, map[string]any{"bound": created != "", "boundAt": created, "enabled": s.Enabled && len(s.Encrypted) > 0, "origin": s.Origin})
}
func randomWechatToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (a *App) startWechat(w http.ResponseWriter, r *http.Request, purpose string) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wechatJSON(w, r) {
		return
	}
	scope := "login"
	if purpose == "bind" {
		scope = "account:" + a.uid()
	}
	if err := a.wechatRate(r, scope); err != nil {
		failWechat(w, err)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if decodeOrganizationJSON(w, r, &input) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	s, err := readWechatSettings(r.Context(), a.db)
	if err != nil {
		failWechat(w, err)
		return
	}
	if !s.Enabled || len(s.Encrypted) == 0 {
		fail(w, 409, "wechat_disabled", "管理员尚未启用微信扫码登录")
		return
	}
	if r.Header.Get("Origin") != s.Origin {
		fail(w, 409, "wechat_origin", "请从管理员配置的正式域名发起微信扫码")
		return
	}
	var user, session, credential string
	if purpose == "bind" {
		user, session = a.uid(), a.sessionToken
		if err = a.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE tenant_id=? AND id=? AND active=1 AND operation_disabled=0 AND must_change_password=0", tenantID, user).Scan(&credential); err != nil || credential == "" || !verifyPassword(input.Password, credential) {
			fail(w, 403, "password_required", "请确认当前账号密码后重试")
			return
		}
		var n int
		if err = a.db.QueryRowContext(r.Context(), "SELECT count(*) FROM user_wechat_bindings WHERE tenant_id=? AND user_id=?", tenantID, user).Scan(&n); err != nil {
			failWechat(w, err)
			return
		}
		if n > 0 {
			fail(w, 409, "already_bound", "当前账号已绑定微信，请先解绑再更换")
			return
		}
	} else if _, err = a.authenticate(r); err == nil {
		fail(w, 409, "already_signed_in", "当前已登录，请先退出或在个人设置中绑定微信")
		return
	}
	state, err := randomWechatToken()
	if err != nil {
		failWechat(w, err)
		return
	}
	browser, err := randomWechatToken()
	if err != nil {
		failWechat(w, err)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWechat(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM wechat_oauth_states WHERE expires_at<=?", time.Now().Unix()); err != nil {
		failWechat(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), "INSERT INTO wechat_oauth_states(state_hash,browser_hash,purpose,user_id,session_hash,credential_hash,config_version,expires_at) VALUES(?,?,?,?,?,?,?,?)", tokenDigest(state), tokenDigest(browser), purpose, user, session, credential, s.Version, time.Now().Add(5*time.Minute).Unix())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWechat(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: wechatStateCookie, Value: browser, Path: wechatCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	query := url.Values{"appid": {s.AppID}, "redirect_uri": {s.Origin + wechatCallbackPath}, "response_type": {"code"}, "scope": {"snsapi_login"}, "state": {state}}
	write(w, 200, map[string]any{"url": "https://open.weixin.qq.com/connect/qrconnect?" + query.Encode() + "#wechat_redirect", "expiresIn": 300})
}

// 只访问微信固定官方端点；不保存或回传 access_token、refresh_token，也不记录包含 Secret 的请求 URL。
func (a *App) exchangeWechat(ctx context.Context, s wechatSettings, code string) (string, error) {
	secret, err := wechatSecret(a.wecomKey, s.Encrypted, "", false)
	if err != nil {
		return "", err
	}
	values := url.Values{"appid": {s.AppID}, "secret": {string(secret)}, "code": {code}, "grant_type": {"authorization_code"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/sns/oauth2/access_token?"+values.Encode(), nil)
	if err != nil {
		return "", err
	}
	client := a.wechatHTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("微信授权服务暂不可用")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("微信授权响应异常")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return "", errors.New("微信授权响应异常")
	}
	var token struct {
		OpenID  string `json:"openid"`
		ErrCode int    `json:"errcode"`
	}
	if json.Unmarshal(raw, &token) != nil || token.ErrCode != 0 || !wechatTokenPattern.MatchString(token.OpenID) {
		return "", errors.New("微信授权已失效")
	}
	return tokenDigest(s.AppID + ":" + token.OpenID), nil
}

type wechatPending struct {
	Purpose, User, Session, Credential string
	Version                            int
}

// 浏览器 Cookie + 服务端一次性 state 同时匹配后消费，错误浏览器不会烧掉合法请求。
func (a *App) consumeWechatState(r *http.Request) (wechatPending, error) {
	var pending wechatPending
	cookie, err := r.Cookie(wechatStateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || len(cookie.Value) != 64 || len(state) != 64 {
		return pending, errors.New("微信授权状态无效")
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return pending, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(r.Context(), `DELETE FROM wechat_oauth_states WHERE state_hash=? AND browser_hash=? AND expires_at>? RETURNING purpose,user_id,session_hash,credential_hash,config_version`, tokenDigest(state), tokenDigest(cookie.Value), time.Now().Unix()).Scan(&pending.Purpose, &pending.User, &pending.Session, &pending.Credential, &pending.Version)
	if err != nil {
		return pending, err
	}
	return pending, tx.Commit()
}
func failWechat(w http.ResponseWriter, err error) {
	var specific *organizationError
	if errors.As(err, &specific) {
		fail(w, specific.Status, specific.Code, specific.Message)
		return
	}
	fail(w, 503, "wechat_unavailable", "微信登录服务暂时不可用，请稍后重试")
}
func (a *App) wechatCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	pending, err := a.consumeWechatState(r)
	target := "/projects"
	if err != nil {
		if _, authErr := a.authenticate(r); authErr == nil {
			target = "/profile?tab=wechat"
		}
	}
	if pending.Purpose == "bind" {
		target = "/profile?tab=wechat"
	}
	finish := func(result string) {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		http.Redirect(w, r, target+separator+"wechat="+result, http.StatusSeeOther)
	}
	if err != nil {
		finish("expired")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: wechatStateCookie, Value: "", Path: wechatCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	code := r.URL.Query().Get("code")
	if code == "" {
		finish("cancelled")
		return
	}
	if !wechatTokenPattern.MatchString(code) {
		finish("failed")
		return
	}
	s, err := readWechatSettings(r.Context(), a.db)
	if err != nil || !s.Enabled || s.Version != pending.Version {
		finish("expired")
		return
	}
	if pending.Purpose == "bind" {
		principal, e := a.authenticate(r)
		if e != nil || principal.UserID != pending.User || principal.TokenHash != pending.Session {
			finish("expired")
			return
		}
		impersonation, ok := a.resolveImpersonation(w, r, &principal)
		if !ok {
			return
		}
		if impersonation != nil {
			finish("failed")
			return
		}
	} else if _, e := a.authenticate(r); e == nil {
		finish("failed")
		return
	}
	identity, err := a.exchangeWechat(r.Context(), s, code)
	if err != nil {
		finish("failed")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		finish("failed")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?", tenantID); err != nil {
		finish("failed")
		return
	}
	fresh, err := readWechatSettings(r.Context(), tx)
	if err != nil || !fresh.Enabled || fresh.Version != pending.Version {
		finish("expired")
		return
	}
	var user string
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339)
	if pending.Purpose == "bind" {
		var allowed int
		err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM users u JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND u.must_change_password=0 AND u.password_hash=? AND m.status='active' AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, tenantID, pending.User, pending.Credential, pending.Session, stamp).Scan(&allowed)
		if err != nil || allowed != 1 {
			finish("expired")
			return
		}
		_, err = tx.ExecContext(r.Context(), "INSERT INTO user_wechat_bindings(tenant_id,user_id,app_id,identity_hash,created_at) VALUES(?,?,?,?,?)", tenantID, pending.User, s.AppID, identity, stamp)
		if err != nil {
			finish("conflict")
			return
		}
		if err = wechatAudit(r.Context(), tx, pending.User, "wechat_bound", map[string]any{"bound": true}); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			finish("failed")
			return
		}
		finish("bound")
		return
	}
	// 身份仅按应用内 OpenID 摘要定位已绑定人员；绝不按昵称、邮箱猜测绑定关系。
	err = tx.QueryRowContext(r.Context(), `SELECT u.id FROM user_wechat_bindings b JOIN users u ON u.tenant_id=b.tenant_id AND u.id=b.user_id JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id WHERE b.tenant_id=? AND b.app_id=? AND b.identity_hash=? AND u.active=1 AND u.operation_disabled=0 AND m.status='active'`, tenantID, s.AppID, identity).Scan(&user)
	if err != nil {
		finish("unbound")
		return
	}
	token, err := randomWechatToken()
	if err != nil {
		finish("failed")
		return
	}
	expires := now.Add(a.authSessionTTL()).Truncate(time.Second)
	_, err = tx.ExecContext(r.Context(), "INSERT INTO auth_sessions(token_hash,tenant_id,user_id,created_at,last_seen_at,expires_at,user_agent,ip_address) VALUES(?,?,?,?,?,?,?,?)", tokenDigest(token), tenantID, user, stamp, stamp, expires.Format(time.RFC3339), truncate(r.UserAgent(), 255), requestIP(r))
	if err == nil {
		err = wechatAudit(r.Context(), tx, user, "wechat_login", map[string]any{"authenticated": true})
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE users SET last_active=? WHERE tenant_id=? AND id=?", stamp, tenantID, user)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		finish("failed")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: a.signSession(token, expires), Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(a.authSessionTTL().Seconds())})
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}
func (a *App) unbindWechat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wechatJSON(w, r) {
		return
	}
	if err := a.wechatRate(r, "account:"+a.uid()); err != nil {
		failWechat(w, err)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if decodeOrganizationJSON(w, r, &input) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	var hash string
	if err := a.db.QueryRowContext(r.Context(), "SELECT password_hash FROM users WHERE tenant_id=? AND id=?", tenantID, a.uid()).Scan(&hash); err != nil || hash == "" || !verifyPassword(input.Password, hash) {
		fail(w, 403, "password_required", "请确认当前账号密码后重试")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWechat(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?", tenantID); err != nil {
		failWechat(w, err)
		return
	}
	var current string
	err = tx.QueryRowContext(r.Context(), `SELECT u.password_hash FROM users u JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND u.must_change_password=0 AND m.status='active' AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, tenantID, a.uid(), a.sessionToken, time.Now().UTC().Format(time.RFC3339)).Scan(&current)
	if err != nil || current != hash {
		fail(w, 409, "credentials_changed", "账号凭据已变化，请重新登录后重试")
		return
	}
	_, err = tx.ExecContext(r.Context(), "DELETE FROM user_wechat_bindings WHERE tenant_id=? AND user_id=?", tenantID, a.uid())
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM wechat_oauth_states WHERE user_id=?", a.uid())
	}
	// 撤销其他浏览器的登录态，封堵解绑与扫码签发会话并发造成的残留登录。
	if err == nil {
		_, err = tx.ExecContext(r.Context(), "UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND token_hash<>? AND revoked_at IS NULL", time.Now().UTC().Format(time.RFC3339), tenantID, a.uid(), a.sessionToken)
	}
	if err == nil {
		err = wechatAudit(r.Context(), tx, a.uid(), "wechat_unbound", map[string]any{"bound": false})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWechat(w, err)
		return
	}
	write(w, 200, map[string]any{"bound": false})
}
