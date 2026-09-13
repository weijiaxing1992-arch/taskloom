package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// 企业微信自建应用与个人微信开放平台是两套隔离的凭据和身份体系。自建应用只会在
// 管理员完成配置、明确打开通知投递、成员完成官方身份绑定且服务处于 live 模式后调用
// 企业微信；站内通知始终先写入本地，不依赖外部请求成功。
const wecomAppCallbackPath = "/api/auth/wecom/callback"
const wecomAppStateCookie = "devflow_wecom_app_oauth"
const wecomOfficialAPIBase = "https://qyapi.weixin.qq.com"

var (
	wecomAppCorpIDPattern     = regexp.MustCompile(`^ww[A-Za-z0-9_-]{6,62}$`)
	wecomAppAgentIDPattern    = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)
	wecomAppSecretPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	wecomAppVerifyFilePattern = regexp.MustCompile(`^WW_verify_[A-Za-z0-9_-]{6,128}\.txt$`)
	wecomAppTokenPattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)
	wecomAppUserIDPattern     = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
)

type wecomAppSettings struct {
	CorpID, AgentID, Origin, VerifyFilename string
	Encrypted                               []byte
	Enabled, DeliveryEnabled                bool
	Version                                 int
}

func (a *App) migrateWecomCustomApp() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS organization_wecom_app_settings(
 tenant_id TEXT PRIMARY KEY,corp_id TEXT NOT NULL DEFAULT '',agent_id TEXT NOT NULL DEFAULT '',origin TEXT NOT NULL,
 verify_filename TEXT NOT NULL DEFAULT '',encrypted_secret BLOB NOT NULL DEFAULT X'',enabled INTEGER NOT NULL DEFAULT 0,
 delivery_enabled INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS user_wecom_app_bindings(
 tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,corp_id TEXT NOT NULL,wecom_user_id_hash TEXT NOT NULL,encrypted_user_id BLOB NOT NULL DEFAULT X'',version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,
 PRIMARY KEY(tenant_id,user_id),UNIQUE(tenant_id,corp_id,wecom_user_id_hash));
 CREATE TABLE IF NOT EXISTS wecom_app_oauth_states(
 state_hash TEXT PRIMARY KEY,browser_hash TEXT NOT NULL,purpose TEXT NOT NULL,user_id TEXT NOT NULL DEFAULT '',
 config_version INTEGER NOT NULL,expires_at INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_wecom_app_state_expiry ON wecom_app_oauth_states(expires_at);
 CREATE TRIGGER IF NOT EXISTS wecom_app_cleanup_removed_member AFTER UPDATE OF status ON tenant_memberships
 WHEN NEW.status='removed' BEGIN
 DELETE FROM user_wecom_app_bindings WHERE tenant_id=NEW.tenant_id AND user_id=NEW.user_id;
 DELETE FROM wecom_app_oauth_states WHERE user_id=NEW.user_id;
 END;
 CREATE TRIGGER IF NOT EXISTS wecom_app_cleanup_deleted_membership AFTER DELETE ON tenant_memberships BEGIN
 DELETE FROM user_wecom_app_bindings WHERE tenant_id=OLD.tenant_id AND user_id=OLD.user_id;
 DELETE FROM wecom_app_oauth_states WHERE user_id=OLD.user_id;
 END;
 -- 早期版本误写为 OLD.user_id。先 DROP 才能修复已经启动过旧版本的数据库；
 -- 重建时保留 IF NOT EXISTS，以容忍两个应用实例同时启动时另一个实例已写入正确版本。
 DROP TRIGGER IF EXISTS wecom_app_cleanup_deleted_user;
 CREATE TRIGGER IF NOT EXISTS wecom_app_cleanup_deleted_user AFTER DELETE ON users BEGIN
 DELETE FROM user_wecom_app_bindings WHERE tenant_id=OLD.tenant_id AND user_id=OLD.id;
 DELETE FROM wecom_app_oauth_states WHERE user_id=OLD.id;
 END;`)
	if err != nil {
		return err
	}
	// 老版本已存在的绑定只有不可逆摘要，不能安全地用于投递。保留摘要用于防冲突，
	// 但必须由成员重新经过官方授权，才会补齐受 AAD 保护的 UserId 密文。
	for _, column := range []migrationColumn{
		{"organization_wecom_app_settings", "delivery_enabled", `ALTER TABLE organization_wecom_app_settings ADD COLUMN delivery_enabled INTEGER NOT NULL DEFAULT 0`},
		{"user_wecom_app_bindings", "encrypted_user_id", `ALTER TABLE user_wecom_app_bindings ADD COLUMN encrypted_user_id BLOB NOT NULL DEFAULT X''`},
		{"user_wecom_app_bindings", "version", `ALTER TABLE user_wecom_app_bindings ADD COLUMN version INTEGER NOT NULL DEFAULT 1`},
		{"wecom_app_oauth_states", "session_hash", `ALTER TABLE wecom_app_oauth_states ADD COLUMN session_hash TEXT NOT NULL DEFAULT ''`},
		{"wecom_app_oauth_states", "credential_hash", `ALTER TABLE wecom_app_oauth_states ADD COLUMN credential_hash TEXT NOT NULL DEFAULT ''`},
	} {
		if _, err = addMigrationColumn(a.db, column); err != nil {
			return err
		}
	}
	_, err = a.db.Exec(`CREATE TABLE IF NOT EXISTS wecom_app_deliveries(
 id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,notification_id INTEGER NOT NULL,
 config_version INTEGER NOT NULL,binding_version INTEGER NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TEXT NOT NULL,lease_until TEXT NOT NULL DEFAULT '',last_error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,sent_at TEXT NOT NULL DEFAULT '',UNIQUE(tenant_id,notification_id));
 CREATE INDEX IF NOT EXISTS idx_wecom_app_delivery_pending ON wecom_app_deliveries(tenant_id,status,next_attempt_at);
 CREATE TABLE IF NOT EXISTS wecom_app_delivery_parts(
 delivery_id INTEGER NOT NULL REFERENCES wecom_app_deliveries(id) ON DELETE CASCADE,part_index INTEGER NOT NULL,content TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'pending',sent_at TEXT NOT NULL DEFAULT '',PRIMARY KEY(delivery_id,part_index));
 DROP TRIGGER IF EXISTS queue_wecom_app_notification;
 CREATE TRIGGER queue_wecom_app_notification AFTER INSERT ON user_notifications
 BEGIN
 INSERT OR IGNORE INTO wecom_app_deliveries(tenant_id,user_id,notification_id,config_version,binding_version,next_attempt_at,created_at,updated_at)
 SELECT NEW.tenant_id,NEW.recipient_user_id,NEW.id,s.version,b.version,NEW.created_at,NEW.created_at,NEW.created_at
 FROM organization_wecom_app_settings s
 JOIN user_wecom_app_bindings b ON b.tenant_id=s.tenant_id AND b.user_id=NEW.recipient_user_id AND b.corp_id=s.corp_id
 JOIN users u ON u.tenant_id=NEW.tenant_id AND u.id=NEW.recipient_user_id
 JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
 WHERE s.tenant_id=NEW.tenant_id AND s.enabled=1 AND s.delivery_enabled=1 AND length(s.encrypted_secret)>0
   AND length(b.encrypted_user_id)>0 AND u.active=1 AND u.operation_disabled=0 AND tm.status='active';
 END;`)
	return err
}

func readWecomAppSettings(ctx context.Context, q stateStore) (wecomAppSettings, error) {
	s := wecomAppSettings{Origin: "https://taskloom.example.com"}
	err := q.QueryRowContext(ctx, `SELECT corp_id,agent_id,origin,verify_filename,encrypted_secret,enabled,delivery_enabled,version FROM organization_wecom_app_settings WHERE tenant_id=?`, tenantID).Scan(&s.CorpID, &s.AgentID, &s.Origin, &s.VerifyFilename, &s.Encrypted, &s.Enabled, &s.DeliveryEnabled, &s.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	return s, err
}

func (s wecomAppSettings) ready() bool {
	return wecomAppCorpIDPattern.MatchString(s.CorpID) && wecomAppAgentIDPattern.MatchString(s.AgentID) && validWecomAppOrigin(s.Origin) && wecomAppVerifyFilePattern.MatchString(s.VerifyFilename) && len(s.Encrypted) > 0
}

func wecomAppSettingsView(s wecomAppSettings) map[string]any {
	origin := strings.TrimRight(s.Origin, "/")
	verifyURL := ""
	if origin != "" && s.VerifyFilename != "" {
		verifyURL = origin + "/" + s.VerifyFilename
	}
	return map[string]any{
		"corpId":                      s.CorpID,
		"agentId":                     s.AgentID,
		"origin":                      s.Origin,
		"callbackUrl":                 origin + wecomAppCallbackPath,
		"verifyFilename":              s.VerifyFilename,
		"verifyUrl":                   verifyURL,
		"apiBase":                     wecomOfficialAPIBase,
		"enabled":                     s.Enabled,
		"notificationDeliveryEnabled": s.DeliveryEnabled,
		"secretConfigured":            len(s.Encrypted) > 0,
		"configurationReady":          s.ready(),
		"deliveryReady":               s.Enabled && s.DeliveryEnabled && s.ready(),
		"externalCallsEnabled":        s.Enabled && s.DeliveryEnabled && s.ready() && wecomAppLiveMode(),
		"providerMode":                wecomAppProviderMode(),
		"version":                     s.Version,
	}
}

func validWecomAppOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Host != "" && u.Port() == "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.Opaque == "" && net.ParseIP(u.Hostname()) == nil && strings.Contains(u.Hostname(), ".") && !strings.HasSuffix(u.Hostname(), ".localhost")
}

// 自建应用 Secret 使用独立 AAD。即使同一主密钥泄露了另一类密文，也不能被替换为本配置密文。
func wecomAppSecret(key, encrypted []byte, plain string, seal bool) ([]byte, error) {
	c, err := webhookCipher(key)
	if err != nil {
		return nil, errors.New("企业微信自建应用密钥不可用")
	}
	aad := []byte("devflow:wecom-custom-app:v1:" + tenantID)
	if seal {
		nonce := make([]byte, c.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return nil, err
		}
		return c.Seal(nonce, nonce, []byte(plain), aad), nil
	}
	if len(encrypted) < c.NonceSize() {
		return nil, errors.New("企业微信自建应用密钥不可用")
	}
	return c.Open(nil, encrypted[:c.NonceSize()], encrypted[c.NonceSize():], aad)
}

func wecomAppUserIDAAD(tenant, user, corpID string) []byte {
	return []byte("devflow:wecom-custom-app-user-id:v1:" + tenant + ":" + user + ":" + corpID)
}

func encryptWecomAppUserID(key []byte, tenant, user, corpID, value string) ([]byte, error) {
	if !wecomAppUserIDPattern.MatchString(value) {
		return nil, errors.New("企业微信成员标识无效")
	}
	c, err := webhookCipher(key)
	if err != nil {
		return nil, errors.New("企业微信绑定密钥不可用")
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.Seal(nonce, nonce, []byte(value), wecomAppUserIDAAD(tenant, user, corpID)), nil
}

func decryptWecomAppUserID(key []byte, tenant, user, corpID string, encrypted []byte) (string, error) {
	c, err := webhookCipher(key)
	if err != nil || len(encrypted) < c.NonceSize() {
		return "", errors.New("企业微信绑定密钥不可用")
	}
	plain, err := c.Open(nil, encrypted[:c.NonceSize()], encrypted[c.NonceSize():], wecomAppUserIDAAD(tenant, user, corpID))
	if err != nil || !wecomAppUserIDPattern.MatchString(string(plain)) {
		return "", errors.New("企业微信绑定密钥不可用")
	}
	return string(plain), nil
}

func (a *App) wecomAppAdmin(ctx context.Context, q stateStore) error {
	if err := a.requireOperationAccess(ctx, q); err != nil {
		return err
	}
	if a.impersonation != nil {
		return &organizationError{403, "forbidden", "代访问期间不能配置企业微信自建应用"}
	}
	_, admin, err := a.organizationAccess(ctx, q)
	if err != nil {
		return err
	}
	if !admin {
		return &organizationError{403, "forbidden", "仅企业管理员可配置企业微信自建应用"}
	}
	return nil
}

// 企业微信配置写入只接受当前站点的 JSON 请求，防止管理员已登录时被跨站表单篡改。
func (a *App) wecomAppJSON(w http.ResponseWriter, r *http.Request) bool {
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

func (a *App) organizationWecomApp(w http.ResponseWriter, r *http.Request) {
	if err := a.wecomAppAdmin(r.Context(), a.db); err != nil {
		failWecomApp(w, err)
		return
	}
	if r.Method == http.MethodGet {
		s, err := readWecomAppSettings(r.Context(), a.db)
		if err != nil {
			failWecomApp(w, err)
			return
		}
		write(w, http.StatusOK, a.wecomAppSettingsResponse(r.Context(), s))
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wecomAppJSON(w, r) {
		return
	}
	var input struct {
		CorpID          string `json:"corpId"`
		AgentID         string `json:"agentId"`
		Origin          string `json:"origin"`
		VerifyFilename  string `json:"verifyFilename"`
		Secret          string `json:"secret"`
		ClearSecret     bool   `json:"clearSecret"`
		Enabled         bool   `json:"enabled"`
		DeliveryEnabled bool   `json:"notificationDeliveryEnabled"`
		Version         int    `json:"version"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failWecomApp(w, err)
		return
	}
	input.CorpID = strings.TrimSpace(input.CorpID)
	input.AgentID = strings.TrimSpace(input.AgentID)
	input.Origin = strings.TrimRight(strings.TrimSpace(input.Origin), "/")
	input.VerifyFilename = strings.TrimSpace(input.VerifyFilename)
	if !wecomAppCorpIDPattern.MatchString(input.CorpID) || !wecomAppAgentIDPattern.MatchString(input.AgentID) || !validWecomAppOrigin(input.Origin) || !wecomAppVerifyFilePattern.MatchString(input.VerifyFilename) || (input.Secret != "" && !wecomAppSecretPattern.MatchString(input.Secret)) || (input.ClearSecret && (input.Secret != "" || input.Enabled || input.DeliveryEnabled)) {
		fail(w, 422, "invalid_wecom_app_settings", "请填写企业 ID、应用 AgentId、有效 HTTPS 域名及 WW_verify 校验文件名")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), "UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?", tenantID); err != nil {
		failWecomApp(w, err)
		return
	}
	if err = a.wecomAppAdmin(r.Context(), tx); err != nil {
		failWecomApp(w, err)
		return
	}
	before, err := readWecomAppSettings(r.Context(), tx)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	if before.Version != input.Version {
		fail(w, 409, "settings_changed", "配置已变化，请刷新后重试")
		return
	}
	appChanged := before.CorpID != "" && (before.CorpID != input.CorpID || before.AgentID != input.AgentID)
	if appChanged {
		if input.Secret == "" && !input.ClearSecret {
			fail(w, 422, "new_wecom_app_secret_required", "更换企业微信应用标识时请同时配置新的应用 Secret")
			return
		}
		var bindings int
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=?`, tenantID).Scan(&bindings); err != nil {
			failWecomApp(w, err)
			return
		}
		if bindings > 0 {
			fail(w, 409, "wecom_app_bound", "已有企业微信绑定，不能直接更换企业或应用标识；请先完成解绑或迁移")
			return
		}
	}
	next := before
	if input.ClearSecret {
		next.Encrypted = nil
	}
	if input.Secret != "" {
		next.Encrypted, err = wecomAppSecret(a.wecomKey, nil, input.Secret, true)
		if err != nil {
			fail(w, 503, "wecom_app_secret_unavailable", "企业微信自建应用密钥不可用，请检查服务器密钥文件")
			return
		}
	}
	next.CorpID, next.AgentID, next.Origin, next.VerifyFilename, next.Enabled, next.DeliveryEnabled, next.Version = input.CorpID, input.AgentID, input.Origin, input.VerifyFilename, input.Enabled, input.DeliveryEnabled, before.Version+1
	if next.Enabled && !next.ready() {
		fail(w, 422, "wecom_app_not_ready", "启用企业微信自建应用前必须完成企业信息、密钥、HTTPS 域名和校验文件配置")
		return
	}
	if next.DeliveryEnabled && (!next.Enabled || !next.ready()) {
		fail(w, 422, "wecom_app_delivery_not_ready", "启用企业微信通知投递前必须完成并启用自建应用配置")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_wecom_app_settings(tenant_id,corp_id,agent_id,origin,verify_filename,encrypted_secret,enabled,delivery_enabled,version,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id) DO UPDATE SET corp_id=excluded.corp_id,agent_id=excluded.agent_id,origin=excluded.origin,verify_filename=excluded.verify_filename,encrypted_secret=excluded.encrypted_secret,enabled=excluded.enabled,delivery_enabled=excluded.delivery_enabled,version=excluded.version,updated_at=excluded.updated_at`, tenantID, next.CorpID, next.AgentID, next.Origin, next.VerifyFilename, next.Encrypted, next.Enabled, next.DeliveryEnabled, next.Version, orgNow())
	if err == nil {
		// 配置调整后，任何尚未完成的授权都必须重新开始，避免旧回调落在新应用配置上。
		_, err = tx.ExecContext(r.Context(), "DELETE FROM wecom_app_oauth_states")
	}
	if err == nil {
		// 旧任务可能携带旧的应用、密钥或站点入口；站内通知本身不受影响，新的通知会按新版本入队。
		_, err = tx.ExecContext(r.Context(), `UPDATE wecom_app_deliveries SET status='skipped',last_error='configuration_changed',lease_until='',updated_at=? WHERE tenant_id=? AND status IN ('pending','retry')`, orgNow(), tenantID)
	}
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "wecom_custom_app", tenantID, "settings_updated", wecomAppSettingsView(before), wecomAppSettingsView(next))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWecomApp(w, err)
		return
	}
	write(w, http.StatusOK, a.wecomAppSettingsResponse(r.Context(), next))
}

func failWecomApp(w http.ResponseWriter, err error) {
	var specific *organizationError
	if errors.As(err, &specific) {
		fail(w, specific.Status, specific.Code, specific.Message)
		return
	}
	fail(w, http.StatusServiceUnavailable, "wecom_app_unavailable", "企业微信自建应用服务暂时不可用，请稍后重试")
}

func (a *App) profileWecomApp(w http.ResponseWriter, r *http.Request) {
	if a.impersonation != nil {
		fail(w, 403, "forbidden", "代访问期间不能查看或更改企业微信绑定")
		return
	}
	if r.URL.Path == "/api/profile/wecom-app/bind" {
		a.startWecomAppBinding(w, r)
		return
	}
	if r.URL.Path == "/api/profile/wecom-app/unbind" {
		a.unbindWecomApp(w, r)
		return
	}
	if r.URL.Path != "/api/profile/wecom-app" || r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	s, err := readWecomAppSettings(r.Context(), a.db)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	var created string
	var encryptedSize int
	err = a.db.QueryRowContext(r.Context(), `SELECT created_at,length(encrypted_user_id) FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id=?`, tenantID, a.uid()).Scan(&created, &encryptedSize)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failWecomApp(w, err)
		return
	}
	bound := created != "" && encryptedSize > 0
	write(w, http.StatusOK, map[string]any{
		"bound": bound, "boundAt": created, "requiresRebind": created != "" && !bound,
		"enabled": s.Enabled && s.ready(), "live": wecomAppLiveMode(), "providerMode": wecomAppProviderMode(),
	})
}

// bindWecomAppIdentity 只接受企业微信服务端已经核验过的 UserId。它不按昵称、姓名或
// 邮箱猜测身份；原始 UserId 以账号、企业和应用三者绑定的独立 AAD 加密存储，摘要仅用于
// 唯一性校验，任何读取接口均不会回显二者。
func (a *App) bindWecomAppIdentity(ctx context.Context, tx *sql.Tx, user string, settings wecomAppSettings, wecomUserID string) error {
	if !settings.Enabled || !settings.ready() || !wecomAppUserIDPattern.MatchString(wecomUserID) {
		return &organizationError{422, "wecom_app_identity_invalid", "企业微信绑定信息无效或服务尚未完成配置"}
	}
	var eligible int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND m.status='active'`, tenantID, user).Scan(&eligible)
	if err != nil {
		return err
	}
	if eligible != 1 {
		return &organizationError{403, "wecom_app_identity_forbidden", "当前账号不可绑定企业微信"}
	}
	identity := tokenDigest("wecom-custom-app:" + settings.CorpID + ":" + wecomUserID)
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_wecom_app_bindings WHERE tenant_id=? AND corp_id=? AND wecom_user_id_hash=?`, tenantID, settings.CorpID, identity).Scan(&owner)
	if err == nil && owner != user {
		return &organizationError{409, "wecom_app_identity_bound", "该企业微信已绑定其他账号，请先解绑"}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	encrypted, err := encryptWecomAppUserID(a.wecomKey, tenantID, user, settings.CorpID, wecomUserID)
	if err != nil {
		return &organizationError{503, "wecom_app_identity_unavailable", "企业微信绑定密钥不可用，请检查服务器密钥文件"}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO user_wecom_app_bindings(tenant_id,user_id,corp_id,wecom_user_id_hash,encrypted_user_id,version,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,user_id) DO UPDATE SET corp_id=excluded.corp_id,wecom_user_id_hash=excluded.wecom_user_id_hash,encrypted_user_id=excluded.encrypted_user_id,version=user_wecom_app_bindings.version+1,created_at=excluded.created_at`, tenantID, user, settings.CorpID, identity, encrypted, 1, orgNow())
	return err
}

type wecomAppPending struct {
	Purpose, User, Session, Credential string
	Version                            int
}

// 将来的扫码入口复用这一单次 state。没有入口调用时，它仍以测试覆盖保证回调不会接受伪造
// state 或跨浏览器回调。所有 state 只存摘要，5 分钟后自动失效。
func (a *App) createWecomAppState(ctx context.Context, purpose, user string, version int) (string, string, error) {
	if purpose != "login" && purpose != "bind" {
		return "", "", &organizationError{422, "wecom_app_state_invalid", "企业微信授权状态无效"}
	}
	state, err := randomWecomAppToken()
	if err != nil {
		return "", "", err
	}
	browser, err := randomWecomAppToken()
	if err != nil {
		return "", "", err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM wecom_app_oauth_states WHERE expires_at<=?`, time.Now().Unix()); err == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO wecom_app_oauth_states(state_hash,browser_hash,purpose,user_id,session_hash,credential_hash,config_version,expires_at) VALUES(?,?,?,?,?,?,?,?)`, tokenDigest(state), tokenDigest(browser), purpose, user, "", "", version, time.Now().Add(5*time.Minute).Unix())
	}
	if err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return state, browser, nil
}

func randomWecomAppToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (a *App) consumeWecomAppState(r *http.Request) (wecomAppPending, error) {
	var pending wecomAppPending
	cookie, err := r.Cookie(wecomAppStateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || len(cookie.Value) != 64 || len(state) != 64 {
		return pending, errors.New("企业微信授权状态无效")
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return pending, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(r.Context(), `DELETE FROM wecom_app_oauth_states WHERE state_hash=? AND browser_hash=? AND expires_at>? RETURNING purpose,user_id,session_hash,credential_hash,config_version`, tokenDigest(state), tokenDigest(cookie.Value), time.Now().Unix()).Scan(&pending.Purpose, &pending.User, &pending.Session, &pending.Credential, &pending.Version)
	if err != nil {
		return pending, err
	}
	return pending, tx.Commit()
}

// 绑定从当前已登录账号发起，并要求再次确认密码。state 同时绑定浏览器、当前会话、密码哈希和
// 应用配置版本，避免授权回调在换号、改密、退出或管理员调整应用后被继续使用。
func (a *App) startWecomAppBinding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wecomAppJSON(w, r) {
		return
	}
	if !wecomAppLiveMode() {
		fail(w, http.StatusConflict, "wecom_app_mock_mode", "当前服务器处于模拟模式，不能发起企业微信真实授权")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failWecomApp(w, err)
		return
	}
	s, err := readWecomAppSettings(r.Context(), a.db)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	if !s.Enabled || !s.ready() {
		fail(w, http.StatusConflict, "wecom_app_disabled", "管理员尚未完成并启用企业微信自建应用")
		return
	}
	if r.Header.Get("Origin") != s.Origin {
		fail(w, http.StatusConflict, "wecom_app_origin", "请从管理员配置的正式域名发起企业微信绑定")
		return
	}
	var credential string
	err = a.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE tenant_id=? AND id=? AND active=1 AND operation_disabled=0 AND must_change_password=0`, tenantID, a.uid()).Scan(&credential)
	if err != nil || credential == "" || !verifyPassword(input.Password, credential) {
		fail(w, http.StatusForbidden, "password_required", "请确认当前账号密码后重试")
		return
	}
	var existing int
	if err = a.db.QueryRowContext(r.Context(), `SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id=? AND length(encrypted_user_id)>0`, tenantID, a.uid()).Scan(&existing); err != nil {
		failWecomApp(w, err)
		return
	}
	if existing > 0 {
		fail(w, http.StatusConflict, "already_bound", "当前账号已绑定企业微信，请先解绑再更换")
		return
	}
	state, err := randomWecomAppToken()
	if err != nil {
		failWecomApp(w, err)
		return
	}
	browser, err := randomWecomAppToken()
	if err != nil {
		failWecomApp(w, err)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM wecom_app_oauth_states WHERE expires_at<=?`, time.Now().Unix()); err == nil {
		// 不在一次性 state 表中复制 bcrypt 密码哈希；只保存其不可逆摘要，以便回调时
		// 验证期间账号未改密。session_hash 本身也是会话 token 的摘要。
		_, err = tx.ExecContext(r.Context(), `INSERT INTO wecom_app_oauth_states(state_hash,browser_hash,purpose,user_id,session_hash,credential_hash,config_version,expires_at) VALUES(?,?,?,?,?,?,?,?)`, tokenDigest(state), tokenDigest(browser), "bind", a.uid(), a.sessionToken, tokenDigest(credential), s.Version, time.Now().Add(5*time.Minute).Unix())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWecomApp(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: wecomAppStateCookie, Value: browser, Path: wecomAppCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 300})
	query := url.Values{"appid": {s.CorpID}, "redirect_uri": {s.Origin + wecomAppCallbackPath}, "response_type": {"code"}, "scope": {"snsapi_base"}, "state": {state}}
	write(w, http.StatusOK, map[string]any{"url": "https://open.weixin.qq.com/connect/oauth2/authorize?" + query.Encode() + "#wechat_redirect", "expiresIn": 300})
}

func (a *App) unbindWecomApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.wecomAppJSON(w, r) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failWecomApp(w, err)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failWecomApp(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		failWecomApp(w, err)
		return
	}
	var credential string
	err = tx.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE tenant_id=? AND id=? AND active=1 AND operation_disabled=0 AND must_change_password=0`, tenantID, a.uid()).Scan(&credential)
	if err != nil || credential == "" || !verifyPassword(input.Password, credential) {
		fail(w, http.StatusForbidden, "password_required", "请确认当前账号密码后重试")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id=?`, tenantID, a.uid()); err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE wecom_app_deliveries SET status='skipped',last_error='binding_removed',lease_until='',updated_at=? WHERE tenant_id=? AND user_id=? AND status IN ('pending','retry')`, orgNow(), tenantID, a.uid())
	}
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "wecom_custom_app", a.uid(), "binding_removed", nil, map[string]any{"bound": false})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failWecomApp(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"bound": false})
}

// 回调只在 live 模式下换取官方 UserId；未带受限会话材料的旧状态保持不连接，避免把历史
// configuration-only 状态误升级为真实绑定。
func (a *App) wecomAppCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	pending, err := a.consumeWecomAppState(r)
	target := "/projects"
	if pending.Purpose == "bind" {
		target = "/profile?tab=wecom-app"
	}
	finish := func(result string) {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		http.Redirect(w, r, target+separator+"wecom="+result, http.StatusSeeOther)
	}
	if err != nil {
		finish("expired")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: wecomAppStateCookie, Value: "", Path: wecomAppCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if !wecomAppTokenPattern.MatchString(r.URL.Query().Get("code")) {
		finish("failed")
		return
	}
	s, err := readWecomAppSettings(r.Context(), a.db)
	if err != nil || !s.Enabled || !s.ready() || s.Version != pending.Version {
		finish("expired")
		return
	}
	if pending.Purpose != "bind" || pending.User == "" || pending.Session == "" || pending.Credential == "" {
		finish("not_connected")
		return
	}
	if !wecomAppLiveMode() {
		finish("mock")
		return
	}
	principal, authErr := a.authenticate(r)
	if authErr != nil || principal.UserID != pending.User || principal.TokenHash != pending.Session {
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
	wecomUserID, err := a.exchangeWecomAppUser(r.Context(), s, r.URL.Query().Get("code"))
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
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		finish("failed")
		return
	}
	fresh, err := readWecomAppSettings(r.Context(), tx)
	if err != nil || !fresh.Enabled || !fresh.ready() || fresh.Version != pending.Version {
		finish("expired")
		return
	}
	var currentCredential string
	stamp := time.Now().UTC().Format(time.RFC3339)
	err = tx.QueryRowContext(r.Context(), `SELECT u.password_hash FROM users u JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND u.must_change_password=0 AND m.status='active' AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, tenantID, pending.User, pending.Session, stamp).Scan(&currentCredential)
	if err != nil || currentCredential == "" || tokenDigest(currentCredential) != pending.Credential {
		finish("expired")
		return
	}
	if err = a.bindWecomAppIdentity(r.Context(), tx, pending.User, fresh, wecomUserID); err != nil {
		if specific := new(organizationError); errors.As(err, &specific) && specific.Status == http.StatusConflict {
			finish("conflict")
			return
		}
		finish("failed")
		return
	}
	if err = a.organizationAudit(r.Context(), tx, "wecom_custom_app", pending.User, "binding_created", nil, map[string]any{"bound": true}); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		finish("failed")
		return
	}
	finish("bound")
}
