package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const initialPasswordMessage = "首次登录必须修改临时密码后才能使用系统"

// 迁移只添加安全状态，不重设任何旧账号密码；批量初始化必须运行独立、显式授权的 CLI。
func (a *App) migrateInitialPasswords() error {
	var exists int
	if err := a.db.QueryRow(`SELECT count(*) FROM pragma_table_info('users') WHERE name='must_change_password'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := a.db.Exec(`ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0 CHECK(must_change_password IN (0,1))`); err != nil {
			return err
		}
	}
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS password_initialization_batches(tenant_id TEXT NOT NULL,batch_id TEXT NOT NULL,applied_at TEXT NOT NULL,target_ids_json TEXT NOT NULL,target_count INTEGER NOT NULL,PRIMARY KEY(tenant_id,batch_id));
CREATE TABLE IF NOT EXISTS auth_initial_password_attempts(tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,window_start INTEGER NOT NULL,attempts INTEGER NOT NULL,PRIMARY KEY(tenant_id,user_id));`)
	return err
}

func validateInitialPassword(value string) error {
	if !utf8.ValidString(value) || len(value) < 6 || len(value) > 72 {
		return orgInvalid("临时密码须为 6–72 字节且不能包含空白或控制字符")
	}
	for _, ch := range value {
		if unicode.IsSpace(ch) || unicode.IsControl(ch) || unicode.Is(unicode.Cf, ch) {
			return orgInvalid("临时密码须为 6–72 字节且不能包含空白或控制字符")
		}
	}
	return nil
}

func configuredInitialPassword() (string, error) {
	value := os.Getenv("DEVFLOW_INITIAL_PASSWORD")
	if value == "" {
		return "", nil
	}
	if err := validateInitialPassword(value); err != nil {
		return "", orgInvalid("服务端临时密码配置无效，请联系管理员")
	}
	return value, nil
}

func initialPasswordRequired(ctx context.Context, store stateStore, user string) (bool, error) {
	var required bool
	err := store.QueryRowContext(ctx, `SELECT must_change_password FROM users WHERE tenant_id=? AND id=?`, tenantID, user).Scan(&required)
	return required, err
}

// 必须位于项目选择之前，且对真实登录账号与代访问后的有效身份各检查一次。
// 不清除待改密会话，只给最小 session；初始改密端点本身禁止任何代访问会话。
func (a *App) allowInitialPasswordRequest(w http.ResponseWriter, r *http.Request, p sessionPrincipal, impersonation *impersonationContext) bool {
	required, err := initialPasswordRequired(r.Context(), a.db, p.UserID)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return false
	}
	if !required && r.URL.Path != "/api/auth/initial-password" {
		return true
	}
	if r.URL.Path == "/api/auth/initial-password" && r.Header.Get("X-DevFlow-Expected-User") == "" {
		fail(w, 409, "identity_changed", "账号身份已在其他页面切换，请刷新后继续")
		return false
	}
	if expected := r.Header.Get("X-DevFlow-Expected-User"); expected != "" && expected != p.UserID {
		fail(w, 409, "identity_changed", "账号身份已在其他页面切换，请刷新后继续")
		return false
	}
	if r.URL.Path == "/api/auth/initial-password" {
		a.changeInitialPassword(w, r, p)
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/session" {
		a.initialPasswordSession(w, r, p.UserID, impersonation)
		return false
	}
	fail(w, 403, "password_change_required", initialPasswordMessage)
	return false
}

func (a *App) initialPasswordSession(w http.ResponseWriter, r *http.Request, user string, impersonation *impersonationContext) {
	var name, role, color, locale, timezone, organizationName string
	var disabled bool
	err := a.db.QueryRowContext(r.Context(), `SELECT u.name,tm.role,u.avatar_color,u.locale,u.timezone,t.name,u.operation_disabled FROM users u JOIN tenants t ON t.id=u.tenant_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, user).Scan(&name, &role, &color, &locale, &timezone, &organizationName, &disabled)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), locale))
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, map[string]any{"tenant": map[string]string{"id": tenantID, "name": organizationName}, "project": map[string]string{"id": "", "name": "", "code": ""}, "user": map[string]any{"id": user, "name": name, "role": role, "avatarColor": color, "locale": storedLocale(locale), "timezone": timezone, "operationDisabled": disabled, "mustChangePassword": true}, "supportedLocales": supportedLocales, "impersonation": impersonation, "canImpersonate": false, "organizationPermissions": []string{}})
}

// 这是一次性的本人凭据更换：不依赖项目，不允许管理员代做，成功必须重新登录。
// 昂贵 bcrypt 在写事务外运行；提交前复核账号、会话、原密码摘要和待改密标志，防并发覆盖管理员重置。
func (a *App) changeInitialPassword(w http.ResponseWriter, r *http.Request, p sessionPrincipal) {
	w.Header().Set("Cache-Control", "no-store")
	if expected := r.Header.Get("X-DevFlow-Expected-User"); expected == "" || expected != p.UserID {
		fail(w, 409, "identity_changed", "账号身份已在其他页面切换，请刷新后继续")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, 415, "json_required", "首次改密请求必须使用 JSON")
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if input.NewPassword != input.ConfirmPassword {
		fail(w, 422, "password_mismatch", "两次输入的新密码不一致")
		return
	}
	if err := validatePassword(input.NewPassword); err != nil {
		fail(w, 422, "weak_password", err.Error())
		return
	}
	if strings.TrimSpace(input.NewPassword) != input.NewPassword {
		fail(w, 422, "weak_password", "新密码不能包含首尾空白")
		return
	}
	var currentHash string
	var required bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT password_hash,must_change_password FROM users WHERE tenant_id=? AND id=? AND active=1 AND operation_disabled=0`, tenantID, p.UserID).Scan(&currentHash, &required); err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if active, err := a.activeImpersonation(p.TokenHash); err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	} else if active != nil {
		fail(w, 403, "impersonation_restricted", "代访问期间不能完成首次改密")
		return
	}
	if !required {
		fail(w, 409, "password_change_not_required", "当前账号无需首次改密，请使用个人设置修改密码")
		return
	}
	window := time.Now().Unix() / 900 * 900
	var attempts int
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO auth_initial_password_attempts(tenant_id,user_id,window_start,attempts)VALUES(?,?,?,1) ON CONFLICT(tenant_id,user_id) DO UPDATE SET attempts=CASE WHEN window_start=excluded.window_start THEN attempts+1 ELSE 1 END,window_start=excluded.window_start RETURNING attempts`, tenantID, p.UserID, window).Scan(&attempts)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if attempts > 10 {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "rate_limited", "改密尝试过于频繁，请稍后重试")
		return
	}
	if currentHash == "" || !verifyPassword(input.CurrentPassword, currentHash) {
		fail(w, 422, "current_password_invalid", "当前密码不正确")
		return
	}
	if verifyPassword(input.NewPassword, currentHash) {
		fail(w, 422, "password_unchanged", "新密码不能与当前密码相同")
		return
	}
	if configured := os.Getenv("DEVFLOW_INITIAL_PASSWORD"); configured != "" && input.NewPassword == configured {
		fail(w, 422, "password_unchanged", "新密码不能使用系统临时密码")
		return
	}
	encoded, err := a.encodePassword(input.NewPassword)
	if err != nil {
		fail(w, 503, "password_error", "密码加密失败，请稍后重试")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var valid int
	err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active' AND u.must_change_password=1 AND u.password_hash=? AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>? AND NOT EXISTS(SELECT 1 FROM auth_impersonations i WHERE i.tenant_id=u.tenant_id AND i.session_hash=s.token_hash AND i.ended_at IS NULL)`, tenantID, p.UserID, currentHash, p.TokenHash, now).Scan(&valid)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if valid != 1 {
		fail(w, 409, "credentials_changed", "账号凭据已变化，请重新登录后重试")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE users SET password_hash=?,password_changed_at=?,must_change_password=0 WHERE tenant_id=? AND id=?`, encoded, now, tenantID, p.UserID)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND revoked_at IS NULL`, now, tenantID, p.UserID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (admin_user_id=? OR target_user_id=?) AND ended_at IS NULL`, now, tenantID, p.UserID, p.UserID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM auth_initial_password_attempts WHERE tenant_id=? AND user_id=?`, tenantID, p.UserID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,'',?,'user',?,'user.initial_password_changed','{}','{"mustChangePassword":false,"sessionsRevoked":true}',?)`, tenantID, p.UserID, p.UserID, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure || r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	write(w, 200, map[string]bool{"changed": true, "requiresLogin": true})
}

func (a *App) requirePasswordChanged(ctx context.Context, store stateStore) error {
	required, err := initialPasswordRequired(ctx, store, a.uid())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return orgForbidden()
		}
		return err
	}
	if required {
		return &organizationError{403, "password_change_required", initialPasswordMessage}
	}
	return nil
}
