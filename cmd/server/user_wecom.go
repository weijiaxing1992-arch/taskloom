package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The key lives separately from the database and is never returned by an API.
// A lost key must be restored from backup; silently replacing it would corrupt
// every configured webhook. Exclusive creation also handles concurrent starts.
// 密钥与数据库必须成对备份；已存在 Webhook 或 AI 密文时缺钥会拒绝启动，严禁临时生成新钥“修复”。
func (a *App) initializeWecomKey(path string) ([]byte, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		var existing int
		var wechatTable int
		if err := a.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='organization_wechat_settings'").Scan(&wechatTable); err != nil {
			return nil, err
		}
		if wechatTable > 0 {
			if err := a.db.QueryRow("SELECT count(*) FROM organization_wechat_settings WHERE length(encrypted_secret)>0").Scan(&existing); err != nil {
				return nil, err
			}
			if existing > 0 {
				return nil, errors.New("WeChat encryption key is missing; restore the original key backup")
			}
		}
		if err := a.db.QueryRow(`SELECT count(*) FROM user_wecom_webhooks WHERE length(encrypted_url)>0`).Scan(&existing); err != nil {
			return nil, err
		}
		if existing > 0 {
			return nil, errors.New("webhook encryption key is missing; restore the original key backup")
		}
		var aiTable int
		if err := a.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='organization_ai_settings'`).Scan(&aiTable); err != nil {
			return nil, err
		}
		if aiTable > 0 {
			if err := a.db.QueryRow(`SELECT count(*) FROM organization_ai_settings WHERE length(encrypted_key)>0`).Scan(&existing); err != nil {
				return nil, err
			}
			if existing > 0 {
				return nil, errors.New("AI encryption key is missing; restore the original key backup")
			}
		}
	}
	return loadWecomEncryptionKey(path)
}
func loadWecomEncryptionKey(path string) ([]byte, error) {
	if file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			_, err = file.Write(key)
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("could not persist webhook encryption key")
		}
		return key, nil
	} else if !os.IsExist(err) {
		return nil, fmt.Errorf("could not create webhook encryption key")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("webhook key must be a private regular file (0600)")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 32 {
		return nil, fmt.Errorf("invalid webhook encryption key file")
	}
	return data, nil
}

// 站内通知 INSERT 触发器与业务事务一起入队：事务回滚不会留下外发任务；唯一 notification_id 防重复排队。
// 自动化规则首期只允许站内通知，迁移时重建触发器并显式排除 automation.*，防止后续
// 新增自动化通知类型时被历史触发器悄然转成外部 Webhook 调用。
func (a *App) migrateUserWecom() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS user_wecom_webhooks(tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,encrypted_url BLOB NOT NULL,enabled INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,user_id));
CREATE TABLE IF NOT EXISTS user_wecom_deliveries(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,notification_id INTEGER NOT NULL,hook_version INTEGER NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_attempt_at TEXT NOT NULL,lease_until TEXT NOT NULL DEFAULT '',last_error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,sent_at TEXT NOT NULL DEFAULT '',UNIQUE(tenant_id,notification_id));
CREATE INDEX IF NOT EXISTS idx_user_wecom_pending ON user_wecom_deliveries(status,next_attempt_at);
CREATE TABLE IF NOT EXISTS user_wecom_parts(delivery_id INTEGER NOT NULL REFERENCES user_wecom_deliveries(id) ON DELETE CASCADE,part_index INTEGER NOT NULL,content TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',sent_at TEXT NOT NULL DEFAULT '',PRIMARY KEY(delivery_id,part_index));
DROP TRIGGER IF EXISTS queue_personal_wecom_notification;
CREATE TRIGGER queue_personal_wecom_notification AFTER INSERT ON user_notifications
BEGIN
 INSERT OR IGNORE INTO user_wecom_deliveries(tenant_id,user_id,notification_id,hook_version,next_attempt_at,created_at,updated_at)
 SELECT NEW.tenant_id,NEW.recipient_user_id,NEW.id,w.version,NEW.created_at,NEW.created_at,NEW.created_at FROM user_wecom_webhooks w WHERE w.tenant_id=NEW.tenant_id AND w.user_id=NEW.recipient_user_id AND w.enabled=1;
END;`)
	return err
}

var wecomKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,200}$`)

func validateWecomURL(raw string) (string, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", orgInvalid("请输入有效的企业微信机器人 Webhook 地址")
	}
	if value.Scheme != "https" || value.Host != "qyapi.weixin.qq.com" || value.User != nil || value.Path != "/cgi-bin/webhook/send" || value.RawPath != "" || value.Fragment != "" || value.Opaque != "" {
		return "", orgInvalid("仅支持企业微信官方 HTTPS 机器人地址")
	}
	query, err := url.ParseQuery(value.RawQuery)
	if err != nil || len(query) != 1 || len(query["key"]) != 1 || !wecomKeyPattern.MatchString(query.Get("key")) {
		return "", orgInvalid("企业微信机器人地址的 key 参数不正确")
	}
	return "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + url.QueryEscape(query.Get("key")), nil
}
func webhookCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("webhook encryption is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// AAD 绑定企业与收件人，复制密文到其他账号不能解密；API 和审计只回显是否配置，不回传地址密钥。
func encryptWebhook(key []byte, tenant, user, raw string) ([]byte, error) {
	aead, err := webhookCipher(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(raw), []byte(tenant+":"+user)), nil
}
func decryptWebhook(key []byte, tenant, user string, encrypted []byte) (string, error) {
	aead, err := webhookCipher(key)
	if err != nil || len(encrypted) < 12 {
		return "", errors.New("webhook key is unavailable")
	}
	data, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(tenant+":"+user))
	if err != nil {
		return "", errors.New("webhook key is unavailable")
	}
	return validateWecomURL(string(data))
}

func (a *App) checkWebhookAccess(ctx context.Context, store stateStore, user string) error {
	if a.impersonation != nil {
		return &organizationError{403, "impersonation_restricted", "代访问期间不能配置机器人地址"}
	}
	var active bool
	var targetRole, status string
	err := store.QueryRowContext(ctx, `SELECT u.active,tm.role,tm.status FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=?`, tenantID, user).Scan(&active, &targetRole, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return orgNotFound()
	}
	if err != nil {
		return err
	}
	permissions, admin, err := a.organizationAccess(ctx, store)
	if err != nil {
		return err
	}
	if user == a.uid() {
		if !active || status != "active" {
			return orgForbidden()
		}
		return nil
	}
	if !admin && (!permissions["members.manage"] || targetRole == "tenant_admin") {
		return orgForbidden()
	}
	if !admin {
		var protected int
		if err := store.QueryRowContext(ctx, `SELECT count(*) FROM organization_group_members WHERE tenant_id=? AND user_id=?`, tenantID, user).Scan(&protected); err != nil {
			return err
		}
		if protected > 0 {
			return orgForbidden()
		}
	}
	return nil
}
func (a *App) userWecomWebhook(w http.ResponseWriter, r *http.Request, user string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if err := a.checkWebhookAccess(r.Context(), a.db, user); err != nil {
		failOrganization(w, err)
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			URL     *string `json:"url"`
			Enabled *bool   `json:"enabled"`
			Clear   bool    `json:"clear"`
		}
		if err := decodeOrganizationJSON(w, r, &input); err != nil {
			failOrganization(w, err)
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
			failOrganization(w, err)
			return
		}
		if err = a.checkWebhookAccess(r.Context(), tx, user); err != nil {
			failOrganization(w, err)
			return
		}
		err = a.saveUserWecomConfiguration(r.Context(), tx, user, input.URL, input.Enabled, input.Clear)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
	}
	var enabled bool
	var version int
	var updated string
	var size int
	err := a.db.QueryRowContext(r.Context(), `SELECT enabled,version,updated_at,length(encrypted_url) FROM user_wecom_webhooks WHERE tenant_id=? AND user_id=?`, tenantID, user).Scan(&enabled, &version, &updated, &size)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failOrganization(w, err)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT d.id,d.status,d.attempts,d.last_error,d.created_at,d.sent_at,n.title,(SELECT count(*) FROM user_wecom_parts part WHERE part.delivery_id=d.id),(SELECT count(*) FROM user_wecom_parts part WHERE part.delivery_id=d.id AND part.status='sent'),`+notificationGroupSQL+` FROM user_wecom_deliveries d JOIN user_notifications n ON n.tenant_id=d.tenant_id AND n.id=d.notification_id JOIN projects p ON p.tenant_id=n.tenant_id AND p.id=n.project_id AND p.status='active' WHERE d.tenant_id=? AND d.user_id=?`+visibleNotificationSQL+` ORDER BY d.id DESC LIMIT 20`, tenantID, user)
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer rows.Close()
	deliveries := []map[string]any{}
	for rows.Next() {
		var id, attempts, parts, sentParts int
		var status, lastError, created, sent, group string
		var title sql.NullString
		if err = rows.Scan(&id, &status, &attempts, &lastError, &created, &sent, &title, &parts, &sentParts, &group); err != nil {
			failOrganization(w, err)
			return
		}
		deliveries = append(deliveries, map[string]any{"id": id, "status": status, "attempts": attempts, "errorCode": lastError, "createdAt": created, "sentAt": sent, "title": title.String, "parts": parts, "sentParts": sentParts, "group": notificationGroupNames[group]})
	}
	if err = rows.Err(); err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 200, map[string]any{"configured": size > 0, "enabled": enabled, "version": version, "updatedAt": updated, "maskedUrl": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=••••••••", "deliveryMode": env("DEVFLOW_WECOM_MODE", "mock"), "ready": len(a.wecomKey) == 32, "deliveries": deliveries, "publicUrlConfigured": wecomDetailURL(env("DEVFLOW_PUBLIC_URL", ""), "requirement", 1, a.pid()) != ""})
}

// 限定官方 HTTPS 主机并对实际 DNS 地址做公网检查；禁用系统代理和重定向，防机器人地址变成内网探针。
func newWecomHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 5, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host != "qyapi.weixin.qq.com" || port != "443" {
			return nil, errors.New("untrusted webhook host")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("webhook DNS unavailable")
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("webhook connection unavailable")
	}
	return &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("webhook redirects forbidden") }}
}
func truncateWebhookText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	value = value[:max-3]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
func wecomMessage(title, body, created, subject string, id int64, project, origin string) string {
	const maxTextBytes = 2048
	stamp := created
	if parsed, err := time.Parse(time.RFC3339, created); err == nil {
		stamp = parsed.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05 UTC+08:00")
	}
	// Reserve the footer before truncating the body: text robot messages have
	// a byte limit, and neither the timestamp nor a normal deep link may vanish
	// just because the user supplied a long Unicode comment.
	prefix := truncateWebhookText(title, 300) + "\n\n"
	footer := "\n\n通知时间：" + truncateWebhookText(stamp, 100)
	base, err := url.Parse(origin)
	if err == nil && (base.Scheme == "http" || base.Scheme == "https") && base.Host != "" && base.User == nil && base.RawQuery == "" && base.Fragment == "" {
		link := notificationURL(subject, id)
		separator := "?"
		if strings.Contains(link, "?") {
			separator = "&"
		}
		line := "\n查看详情：" + strings.TrimRight(origin, "/") + link + separator + "project=" + url.QueryEscape(project)
		// Do not emit a truncated/nonfunctional URL. An unusually long deployment
		// origin or project identifier may omit the link while keeping the time.
		if len(line) <= 768 {
			footer += line
		}
	}
	return prefix + truncateWebhookText(body, maxTextBytes-len(prefix)-len(footer)) + footer
}

type wecomJob struct {
	ID                int64
	User              string
	NotificationID    int64
	Version, Attempts int
	PartIndex         int
}

// 先原子领取 30 秒租约，再复核收件人权限/业务禁用/配置版本，网络调用不持有写事务。
// 最多尝试 6 次；外部请求成功但本地落状态前崩溃仍可能重发，不能宣称端到端“恰好一次”。
func (a *App) processUserWecom(ctx context.Context, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	// A crash during the final lease must not leave a delivery "sending" forever.
	if _, err := a.db.ExecContext(ctx, `UPDATE user_wecom_deliveries SET status='failed',last_error='attempts_exhausted',lease_until='',updated_at=? WHERE tenant_id=? AND attempts>=6 AND (status IN ('pending','retry') OR (status='sending' AND lease_until<?))`, stamp, tenantID, stamp); err != nil {
		return err
	}
	var job wecomJob
	job.PartIndex = -1
	// One atomic lease prevents concurrent workers from claiming the same event.
	err := a.db.QueryRowContext(ctx, `UPDATE user_wecom_deliveries SET status='sending',attempts=attempts+1,lease_until=?,updated_at=? WHERE id=(SELECT id FROM user_wecom_deliveries WHERE tenant_id=? AND ((status IN ('pending','retry') AND next_attempt_at<=?) OR (status='sending' AND lease_until<?)) AND attempts<6 ORDER BY id LIMIT 1) RETURNING id,user_id,notification_id,hook_version,attempts`, now.Add(30*time.Second).UTC().Format(time.RFC3339), stamp, tenantID, stamp, stamp).Scan(&job.ID, &job.User, &job.NotificationID, &job.Version, &job.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	finish := func(status, code string) error {
		if status == "retry" && job.Attempts >= 6 {
			status = "failed"
		}
		next := now.Add(time.Duration(1<<min(job.Attempts, 6)) * 30 * time.Second).UTC().Format(time.RFC3339)
		sent := ""
		if status == "sent" || status == "mock_sent" {
			sent = stamp
		}
		_, err := a.db.ExecContext(ctx, `UPDATE user_wecom_deliveries SET status=?,last_error=?,next_attempt_at=?,lease_until='',updated_at=?,sent_at=? WHERE id=? AND tenant_id=? AND status='sending' AND attempts=? AND (?<0 OR EXISTS(SELECT 1 FROM user_wecom_parts WHERE delivery_id=? AND part_index=? AND status='pending'))`, status, code, next, stamp, sent, job.ID, tenantID, job.Attempts, job.PartIndex, job.ID, job.PartIndex)
		return err
	}
	var encrypted []byte
	var enabled, active, operationDisabled bool
	var version int
	var membershipStatus, project, title, body, created, subject string
	var subjectID int64
	err = a.db.QueryRowContext(ctx, `SELECT w.encrypted_url,w.enabled,w.version,u.active,tm.status,n.project_id,n.title,n.body,n.created_at,n.subject_type,n.subject_id,u.operation_disabled FROM user_wecom_webhooks w JOIN users u ON u.tenant_id=w.tenant_id AND u.id=w.user_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id JOIN user_notifications n ON n.tenant_id=u.tenant_id AND n.recipient_user_id=u.id AND n.id=? WHERE w.tenant_id=? AND w.user_id=?`, job.NotificationID, tenantID, job.User).Scan(&encrypted, &enabled, &version, &active, &membershipStatus, &project, &title, &body, &created, &subject, &subjectID, &operationDisabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!enabled || !active || operationDisabled || membershipStatus != "active" || version != job.Version) {
		return finish("skipped", "recipient_or_configuration_changed")
	}
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	var allowed int
	err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects p WHERE p.tenant_id=? AND p.id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`, tenantID, project, job.User, job.User).Scan(&allowed)
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	if allowed == 0 {
		return finish("skipped", "project_access_revoked")
	}
	hook, err := decryptWebhook(a.wecomKey, tenantID, job.User, encrypted)
	if err != nil {
		return finish("failed", "encryption_key_unavailable")
	}
	content, partIndex, err := a.nextWecomPart(ctx, job, title, body, created, subject, subjectID, project)
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	job.PartIndex = partIndex
	// mock_sent 仅本地模拟，绝不代表企业微信已收到；启用真实外发需要运维显式配置 live。
	if env("DEVFLOW_WECOM_MODE", "mock") != "live" {
		return finish("mock_sent", "")
	}
	payload, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": content}})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(payload))
	if err != nil {
		return finish("failed", "invalid_webhook")
	}
	request.Header.Set("Content-Type", "application/json")
	client := a.wecomHTTP
	if client == nil {
		client = newWecomHTTPClient()
	}
	response, err := client.Do(request)
	retry := func(code string) error {
		if job.Attempts >= 6 {
			return finish("failed", code)
		}
		return finish("retry", code)
	}
	if err != nil {
		return retry("delivery_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return retry("http_error")
	}
	const maxReplyBytes = 16 << 10
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxReplyBytes+1))
	if err != nil || len(responseBody) > maxReplyBytes {
		return retry("invalid_response")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	var raw map[string]json.RawMessage
	if err = decoder.Decode(&raw); err != nil {
		return retry("invalid_response")
	}
	// 必须是显式整数 errcode=0 才算成功；缺字段/null/尾随 JSON 不能被误判为默认零值。
	var replyCode *int
	if code, ok := raw["errcode"]; !ok || json.Unmarshal(code, &replyCode) != nil || replyCode == nil {
		return retry("invalid_response")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return retry("invalid_response")
	}
	if *replyCode != 0 {
		if *replyCode == 45009 {
			return retry("rate_limited")
		}
		return finish("failed", "robot_rejected")
	}
	return a.ackWecomPart(ctx, job, now)
}
func (a *App) runUserWecom(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = a.processUserWecom(ctx, now)
		}
	}
}
