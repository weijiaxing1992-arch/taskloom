package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// 企业微信 access_token 只缓存在当前进程内存，绝不落库、回传接口或写入审计。
// App 会被请求上下文按值复制，所以缓存必须是指针，避免复制已使用的互斥锁。
type wecomAppTokenCache struct {
	mu            sync.Mutex
	value, corpID string
	version       int
	expiresAt     time.Time
}

type wecomAppDeliveryJob struct {
	ID, NotificationID            int64
	User                          string
	ConfigVersion, BindingVersion int
	Attempts, PartIndex           int
}

type wecomAppProviderError struct {
	code  string
	retry bool
}

func (e *wecomAppProviderError) Error() string { return e.code }

func wecomAppLiveMode() bool { return env("DEVFLOW_WECOM_MODE", "mock") == "live" }

func wecomAppProviderMode() string {
	if wecomAppLiveMode() {
		return "live"
	}
	return "mock"
}

func (a *App) wecomAppTokenStore() *wecomAppTokenCache {
	if a.wecomAppTokens != nil {
		return a.wecomAppTokens
	}
	// 单元测试的轻量 App 不一定初始化缓存。此处返回临时缓存是安全的：最多多请求一次
	// token，不会共享或持久化凭据。
	return &wecomAppTokenCache{}
}

// 企业微信配置页只展示投递状态码、计数和标题，不展示 UserId、Secret、access_token 或正文。
func (a *App) wecomAppSettingsResponse(ctx context.Context, settings wecomAppSettings) map[string]any {
	view := wecomAppSettingsView(settings)
	stats := map[string]int{"boundMembers": 0, "pending": 0, "retry": 0, "sending": 0, "sent": 0, "mockSent": 0, "failed": 0, "skipped": 0}
	var boundMembers int
	if err := a.db.QueryRowContext(ctx, `SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=? AND length(encrypted_user_id)>0`, tenantID).Scan(&boundMembers); err != nil {
		return view
	}
	stats["boundMembers"] = boundMembers
	rows, err := a.db.QueryContext(ctx, `SELECT status,count(*) FROM wecom_app_deliveries WHERE tenant_id=? GROUP BY status`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var status string
			var count int
			if rows.Scan(&status, &count) != nil {
				break
			}
			switch status {
			case "pending", "retry", "sending", "sent", "failed", "skipped":
				stats[status] = count
			case "mock_sent":
				stats["mockSent"] = count
			}
		}
	}
	view["deliveryStats"] = stats
	items := []map[string]any{}
	rows, err = a.db.QueryContext(ctx, `SELECT d.id,d.status,d.attempts,d.last_error,d.created_at,d.sent_at,n.title,`+notificationGroupSQL+`
 FROM wecom_app_deliveries d JOIN user_notifications n ON n.tenant_id=d.tenant_id AND n.id=d.notification_id
 WHERE d.tenant_id=? ORDER BY d.id DESC LIMIT 12`, tenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, attempts int
			var status, code, created, sent, title, group string
			if rows.Scan(&id, &status, &attempts, &code, &created, &sent, &title, &group) != nil {
				break
			}
			items = append(items, map[string]any{"id": id, "status": status, "attempts": attempts, "errorCode": code, "createdAt": created, "sentAt": sent, "title": title, "category": notificationGroupNames[group]})
		}
	}
	view["recentDeliveries"] = items
	return view
}

func readWecomAppProviderResponse(response *http.Response, target any) error {
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &wecomAppProviderError{code: "http_error", retry: true}
	}
	const maxResponseBytes = 16 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err = decoder.Decode(target); err != nil {
		return &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	return nil
}

type wecomAppTokenResponse struct {
	ErrCode     *int   `json:"errcode"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func validWecomAppAccessToken(value string) bool {
	if len(value) < 8 || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func (a *App) invalidateWecomAppToken(settings wecomAppSettings) {
	cache := a.wecomAppTokenStore()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.corpID == settings.CorpID && cache.version == settings.Version {
		cache.value, cache.corpID, cache.version, cache.expiresAt = "", "", 0, time.Time{}
	}
}

// access token 请求仅访问企业微信官方 qyapi 域名，客户端复用机器人投递的固定主机、无代理、
// 无重定向和公网地址检查。Secret 仅存在于此函数的短生命周期变量中。
func (a *App) wecomAppAccessToken(ctx context.Context, settings wecomAppSettings) (string, error) {
	if !wecomAppLiveMode() {
		return "", &wecomAppProviderError{code: "mock_mode", retry: false}
	}
	cache := a.wecomAppTokenStore()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	if cache.value != "" && cache.corpID == settings.CorpID && cache.version == settings.Version && now.Before(cache.expiresAt) {
		return cache.value, nil
	}
	secret, err := wecomAppSecret(a.wecomKey, settings.Encrypted, "", false)
	if err != nil {
		return "", &wecomAppProviderError{code: "encryption_key_unavailable", retry: false}
	}
	query := url.Values{"corpid": {settings.CorpID}, "corpsecret": {string(secret)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wecomOfficialAPIBase+"/cgi-bin/gettoken?"+query.Encode(), nil)
	if err != nil {
		return "", &wecomAppProviderError{code: "request_invalid", retry: false}
	}
	client := a.wecomHTTP
	if client == nil {
		client = newWecomHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		return "", &wecomAppProviderError{code: "delivery_unavailable", retry: true}
	}
	defer response.Body.Close()
	var reply wecomAppTokenResponse
	if err = readWecomAppProviderResponse(response, &reply); err != nil {
		return "", err
	}
	if reply.ErrCode == nil {
		return "", &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	if *reply.ErrCode != 0 {
		return "", &wecomAppProviderError{code: "provider_rejected", retry: false}
	}
	if !validWecomAppAccessToken(reply.AccessToken) || reply.ExpiresIn < 60 || reply.ExpiresIn > 24*60*60 {
		return "", &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	// 提前失效防止临界时刻使用已过期 token；短 token 保留至少一秒缓存窗口。
	margin := time.Duration(reply.ExpiresIn/4) * time.Second
	if margin > 2*time.Minute {
		margin = 2 * time.Minute
	}
	if margin < time.Second {
		margin = time.Second
	}
	cache.value, cache.corpID, cache.version, cache.expiresAt = reply.AccessToken, settings.CorpID, settings.Version, now.Add(time.Duration(reply.ExpiresIn)*time.Second-margin)
	return cache.value, nil
}

type wecomAppUserResponse struct {
	ErrCode *int   `json:"errcode"`
	UserID  string `json:"UserId"`
}

// 企业微信授权 code 仅用于当前一次绑定，不存储 code、access_token 或原始 UserId 的日志副本。
func (a *App) exchangeWecomAppUser(ctx context.Context, settings wecomAppSettings, code string) (string, error) {
	if !wecomAppTokenPattern.MatchString(code) {
		return "", &wecomAppProviderError{code: "authorization_invalid", retry: false}
	}
	token, err := a.wecomAppAccessToken(ctx, settings)
	if err != nil {
		return "", err
	}
	query := url.Values{"access_token": {token}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wecomOfficialAPIBase+"/cgi-bin/user/getuserinfo?"+query.Encode(), nil)
	if err != nil {
		return "", &wecomAppProviderError{code: "request_invalid", retry: false}
	}
	client := a.wecomHTTP
	if client == nil {
		client = newWecomHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		return "", &wecomAppProviderError{code: "delivery_unavailable", retry: true}
	}
	defer response.Body.Close()
	var reply wecomAppUserResponse
	if err = readWecomAppProviderResponse(response, &reply); err != nil {
		return "", err
	}
	if reply.ErrCode == nil {
		return "", &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	if *reply.ErrCode != 0 {
		if *reply.ErrCode == 40014 || *reply.ErrCode == 42001 {
			a.invalidateWecomAppToken(settings)
			return "", &wecomAppProviderError{code: "token_rejected", retry: true}
		}
		return "", &wecomAppProviderError{code: "authorization_rejected", retry: false}
	}
	if !wecomAppUserIDPattern.MatchString(reply.UserID) {
		return "", &wecomAppProviderError{code: "authorization_rejected", retry: false}
	}
	return reply.UserID, nil
}

func (a *App) nextWecomAppPart(ctx context.Context, job wecomAppDeliveryJob, settings wecomAppSettings) (string, int, error) {
	var content string
	var index int
	err := a.db.QueryRowContext(ctx, `SELECT content,part_index FROM wecom_app_delivery_parts WHERE delivery_id=? AND status='pending' ORDER BY part_index LIMIT 1`, job.ID).Scan(&content, &index)
	if err == nil {
		return content, index, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	var group, projectName, actor, recipient, title, body, created, subject, project string
	var subjectID int64
	err = a.db.QueryRowContext(ctx, `SELECT `+notificationGroupSQL+`,p.name,COALESCE(actor.name,'系统'),recipient.name,n.title,n.body,n.created_at,n.subject_type,n.subject_id,n.project_id
 FROM user_notifications n JOIN projects p ON p.tenant_id=n.tenant_id AND p.id=n.project_id
 JOIN users recipient ON recipient.tenant_id=n.tenant_id AND recipient.id=n.recipient_user_id
 LEFT JOIN users actor ON actor.tenant_id=n.tenant_id AND actor.id=n.actor_user_id
 WHERE n.tenant_id=? AND n.id=? AND n.recipient_user_id=?`, tenantID, job.NotificationID, job.User).Scan(&group, &projectName, &actor, &recipient, &title, &body, &created, &subject, &subjectID, &project)
	if err != nil {
		return "", 0, err
	}
	summary, err := a.wecomSubjectSummary(ctx, subject, subjectID, project)
	if err != nil {
		return "", 0, err
	}
	full := "通知分类：" + notificationGroupNames[group] + "\n标题：" + title + "\n摘要：" + body + "\n项目：" + projectName + "\n接收人：" + recipient + "\n操作人：" + actor + "\n" + summary + "\n\n后续处理：" + wecomNextAction(group)
	parts := splitWecomNotice(job.NotificationID, group, full, created, subject, subjectID, project, settings.Origin)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM wecom_app_delivery_parts WHERE delivery_id=?`, job.ID).Scan(&exists); err != nil {
		return "", 0, err
	}
	if exists == 0 {
		for i, part := range parts {
			if _, err = tx.ExecContext(ctx, `INSERT INTO wecom_app_delivery_parts(delivery_id,part_index,content) VALUES(?,?,?)`, job.ID, i, part); err != nil {
				return "", 0, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	err = a.db.QueryRowContext(ctx, `SELECT content,part_index FROM wecom_app_delivery_parts WHERE delivery_id=? AND status='pending' ORDER BY part_index LIMIT 1`, job.ID).Scan(&content, &index)
	return content, index, err
}

func (a *App) ackWecomAppPart(ctx context.Context, job wecomAppDeliveryJob, now time.Time) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `UPDATE wecom_app_delivery_parts SET status='sent',sent_at=? WHERE delivery_id=? AND part_index=? AND status='pending' AND EXISTS(SELECT 1 FROM wecom_app_deliveries d WHERE d.id=delivery_id AND d.tenant_id=? AND d.status='sending' AND d.attempts=?)`, stamp, job.ID, job.PartIndex, tenantID, job.Attempts)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM wecom_app_delivery_parts WHERE delivery_id=? AND status='pending'`, job.ID).Scan(&remaining); err != nil {
		return err
	}
	status, sent, next := "sent", stamp, stamp
	if remaining > 0 {
		status, sent, next = "pending", "", now.Add(3*time.Second).UTC().Format(time.RFC3339)
	}
	_, err = tx.ExecContext(ctx, `UPDATE wecom_app_deliveries SET status=?,attempts=CASE WHEN ?='pending' THEN 0 ELSE attempts END,last_error='',lease_until='',next_attempt_at=?,updated_at=?,sent_at=? WHERE id=? AND tenant_id=?`, status, status, next, stamp, sent, job.ID, tenantID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) sendWecomAppText(ctx context.Context, settings wecomAppSettings, userID, content string) error {
	token, err := a.wecomAppAccessToken(ctx, settings)
	if err != nil {
		return err
	}
	agentID, err := strconv.ParseInt(settings.AgentID, 10, 64)
	if err != nil || agentID <= 0 {
		return &wecomAppProviderError{code: "configuration_invalid", retry: false}
	}
	payload, err := json.Marshal(map[string]any{
		"touser": userID, "msgtype": "text", "agentid": agentID,
		"text": map[string]string{"content": content},
		"safe": 0, "enable_id_trans": 0, "enable_duplicate_check": 1, "duplicate_check_interval": 1800,
	})
	if err != nil {
		return &wecomAppProviderError{code: "request_invalid", retry: false}
	}
	endpoint := wecomOfficialAPIBase + "/cgi-bin/message/send?" + url.Values{"access_token": {token}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return &wecomAppProviderError{code: "request_invalid", retry: false}
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.wecomHTTP
	if client == nil {
		client = newWecomHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		return &wecomAppProviderError{code: "delivery_unavailable", retry: true}
	}
	defer response.Body.Close()
	var reply struct {
		ErrCode *int `json:"errcode"`
	}
	if err = readWecomAppProviderResponse(response, &reply); err != nil {
		return err
	}
	if reply.ErrCode == nil {
		return &wecomAppProviderError{code: "invalid_response", retry: true}
	}
	if *reply.ErrCode == 0 {
		return nil
	}
	if *reply.ErrCode == 40014 || *reply.ErrCode == 42001 {
		a.invalidateWecomAppToken(settings)
		return &wecomAppProviderError{code: "token_rejected", retry: true}
	}
	if *reply.ErrCode == 45009 {
		return &wecomAppProviderError{code: "rate_limited", retry: true}
	}
	return &wecomAppProviderError{code: "provider_rejected", retry: false}
}

// 一次节拍最多领取一条投递；网络调用不持有数据库写事务。所有外部失败均只更新队列状态，
// 不会回滚、删除或延迟用户已经看到的站内通知。
func (a *App) processWecomAppDelivery(ctx context.Context, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339)
	if _, err := a.db.ExecContext(ctx, `UPDATE wecom_app_deliveries SET status='failed',last_error='attempts_exhausted',lease_until='',updated_at=? WHERE tenant_id=? AND attempts>=6 AND (status IN ('pending','retry') OR (status='sending' AND lease_until<?))`, stamp, tenantID, stamp); err != nil {
		return err
	}
	job := wecomAppDeliveryJob{PartIndex: -1}
	err := a.db.QueryRowContext(ctx, `UPDATE wecom_app_deliveries SET status='sending',attempts=attempts+1,lease_until=?,updated_at=? WHERE id=(SELECT id FROM wecom_app_deliveries WHERE tenant_id=? AND ((status IN ('pending','retry') AND next_attempt_at<=?) OR (status='sending' AND lease_until<?)) AND attempts<6 ORDER BY id LIMIT 1) RETURNING id,user_id,notification_id,config_version,binding_version,attempts`, now.Add(30*time.Second).UTC().Format(time.RFC3339), stamp, tenantID, stamp, stamp).Scan(&job.ID, &job.User, &job.NotificationID, &job.ConfigVersion, &job.BindingVersion, &job.Attempts)
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
		_, updateErr := a.db.ExecContext(ctx, `UPDATE wecom_app_deliveries SET status=?,last_error=?,next_attempt_at=?,lease_until='',updated_at=?,sent_at=? WHERE id=? AND tenant_id=? AND status='sending' AND attempts=? AND (?<0 OR EXISTS(SELECT 1 FROM wecom_app_delivery_parts WHERE delivery_id=? AND part_index=? AND status='pending'))`, status, code, next, stamp, sent, job.ID, tenantID, job.Attempts, job.PartIndex, job.ID, job.PartIndex)
		return updateErr
	}
	settings, err := readWecomAppSettings(ctx, a.db)
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	if !settings.Enabled || !settings.DeliveryEnabled || !settings.ready() || settings.Version != job.ConfigVersion {
		return finish("skipped", "configuration_changed")
	}
	var encryptedUserID []byte
	var bindingVersion int
	var boundCorpID, project string
	var active, operationDisabled bool
	var membershipStatus string
	err = a.db.QueryRowContext(ctx, `SELECT b.encrypted_user_id,b.version,b.corp_id,u.active,u.operation_disabled,tm.status,n.project_id
 FROM user_wecom_app_bindings b JOIN users u ON u.tenant_id=b.tenant_id AND u.id=b.user_id
 JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
 JOIN user_notifications n ON n.tenant_id=u.tenant_id AND n.recipient_user_id=u.id AND n.id=?
 WHERE b.tenant_id=? AND b.user_id=?`, job.NotificationID, tenantID, job.User).Scan(&encryptedUserID, &bindingVersion, &boundCorpID, &active, &operationDisabled, &membershipStatus, &project)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (len(encryptedUserID) == 0 || bindingVersion != job.BindingVersion || boundCorpID != settings.CorpID || !active || operationDisabled || membershipStatus != "active") {
		return finish("skipped", "recipient_or_binding_changed")
	}
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	var allowed int
	err = a.db.QueryRowContext(ctx, `SELECT count(*) FROM projects p WHERE p.tenant_id=? AND p.id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`, tenantID, project, job.User, job.User).Scan(&allowed)
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	if allowed == 0 {
		return finish("skipped", "project_access_revoked")
	}
	if !wecomAppLiveMode() {
		return finish("mock_sent", "")
	}
	userID, err := decryptWecomAppUserID(a.wecomKey, tenantID, job.User, settings.CorpID, encryptedUserID)
	if err != nil {
		return finish("failed", "encryption_key_unavailable")
	}
	content, partIndex, err := a.nextWecomAppPart(ctx, job, settings)
	if err != nil {
		return finish("retry", "database_unavailable")
	}
	job.PartIndex = partIndex
	err = a.sendWecomAppText(ctx, settings, userID, content)
	if err != nil {
		var provider *wecomAppProviderError
		if errors.As(err, &provider) {
			if provider.retry {
				return finish("retry", provider.code)
			}
			return finish("failed", provider.code)
		}
		return finish("retry", "delivery_unavailable")
	}
	return a.ackWecomAppPart(ctx, job, now)
}

func (a *App) runWecomAppDeliveries(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_ = a.processWecomAppDelivery(ctx, now)
		}
	}
}
