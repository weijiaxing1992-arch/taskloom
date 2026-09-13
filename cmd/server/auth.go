package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName  = "devflow_session"
	localSigningSecret = "devflow-local-signing-secret-change-in-production"
	seedPassword       = "TaskLoom2026!"
)

var errSessionStoreUnavailable = errors.New("session store unavailable")
var errVerifiedCredentialChanged = errors.New("verified credential changed")

type sessionPrincipal struct {
	TenantID  string
	UserID    string
	TokenHash string
}

func (a *App) authSigningKey() []byte {
	if len(a.signingKey) >= 32 {
		return a.signingKey
	}
	return []byte(localSigningSecret)
}

// 持久登录仍有有效期（默认 30 天）且可撤销，不是永久登录；变更签名密钥会使旧 Cookie 失效。
func (a *App) authSessionTTL() time.Duration {
	if a.sessionTTL > 0 {
		return a.sessionTTL
	}
	return 30 * 24 * time.Hour
}

func (a *App) bcryptCost() int {
	if a.passwordCost >= bcrypt.MinCost && a.passwordCost <= bcrypt.MaxCost {
		return a.passwordCost
	}
	return 12
}

func (a *App) migrateAuth() error {
	if err := a.migrateOperationAccess(); err != nil {
		return err
	}
	if err := a.migrateInitialPasswords(); err != nil {
		return err
	}
	previousAuthSchema, err := a.databaseHasTable("auth_sessions")
	if err != nil {
		return err
	}
	for _, alter := range []string{
		`ALTER TABLE tenants ADD COLUMN directory_source TEXT NOT NULL DEFAULT 'local'`,
		`ALTER TABLE tenants ADD COLUMN directory_external_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN directory_source TEXT NOT NULL DEFAULT 'local'`,
		`ALTER TABLE users ADD COLUMN directory_external_id TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = a.db.Exec(alter)
	}
	schema := `
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_directory_external ON users(tenant_id,directory_source,directory_external_id) WHERE directory_external_id!='';
CREATE TABLE IF NOT EXISTS departments(
 id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,parent_id TEXT,name TEXT NOT NULL,code TEXT NOT NULL,
 source TEXT NOT NULL DEFAULT 'local',external_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'active',
 sort_order INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(tenant_id,code),UNIQUE(tenant_id,source,external_id)
);
CREATE INDEX IF NOT EXISTS idx_departments_tree ON departments(tenant_id,parent_id,status,sort_order);
CREATE TABLE IF NOT EXISTS department_memberships(
 tenant_id TEXT NOT NULL,department_id TEXT NOT NULL,user_id TEXT NOT NULL,is_primary INTEGER NOT NULL DEFAULT 0,
 title TEXT NOT NULL DEFAULT '',source TEXT NOT NULL DEFAULT 'local',external_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'active',joined_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(tenant_id,department_id,user_id)
);
CREATE INDEX IF NOT EXISTS idx_department_members_user ON department_memberships(tenant_id,user_id,is_primary,status);
CREATE TABLE IF NOT EXISTS auth_sessions(
 token_hash TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,created_at TEXT NOT NULL,
 last_seen_at TEXT NOT NULL,expires_at TEXT NOT NULL,revoked_at TEXT,user_agent TEXT NOT NULL DEFAULT '',ip_address TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(tenant_id,user_id,revoked_at,expires_at);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry ON auth_sessions(expires_at,revoked_at);
`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}
	if err := a.migrateLoginProtection(); err != nil {
		return err
	}
	if err := a.migrateImpersonation(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// 已迁移过的业务库重启时只清理过期会话，不重设部门、人员或演示口令。
	if !a.seedDemo && previousAuthSchema {
		_, err := a.db.Exec(`DELETE FROM auth_sessions WHERE expires_at<?`, now)
		return err
	}
	_, _ = a.db.Exec(`UPDATE tenants SET directory_source='seed',directory_external_id='seed:tenant:acme' WHERE id=? AND directory_external_id=''`, tenantID)
	_, _ = a.db.Exec(`UPDATE users SET directory_source='seed',directory_external_id='seed:'||id WHERE tenant_id=? AND directory_external_id='' AND id IN ('u_admin','u_member','u_pm','u_front_lead','u_back_lead','u_front','u_back','u_algo','u_ui','u_qa','u_viewer')`, tenantID)
	departments := []struct {
		id, parent, name, code string
		sort                   int
	}{
		{"dept_rd", "", "产品研发中心", "RD", 10},
		{"dept_product", "dept_rd", "产品部", "PRODUCT", 20},
		{"dept_frontend", "dept_rd", "前端研发组", "FRONTEND", 30},
		{"dept_backend", "dept_rd", "后端研发组", "BACKEND", 40},
		{"dept_algorithm", "dept_rd", "算法平台组", "ALGORITHM", 50},
		{"dept_quality", "dept_rd", "质量保障部", "QUALITY", 60},
		{"dept_design", "dept_rd", "设计体验部", "DESIGN", 70},
		{"dept_operations", "", "业务运营部", "OPERATIONS", 80},
	}
	for _, department := range departments {
		var parent any
		if department.parent != "" {
			parent = department.parent
		}
		if _, err := a.db.Exec(`INSERT OR IGNORE INTO departments(id,tenant_id,parent_id,name,code,source,external_id,status,sort_order,created_at,updated_at)VALUES(?,?,?,?,?,'seed',?,'active',?,?,?)`, department.id, tenantID, parent, department.name, department.code, "seed:"+department.code, department.sort, now, now); err != nil {
			return err
		}
	}
	memberDepartments := map[string]string{
		"u_admin": "dept_rd", "u_member": "dept_backend", "u_pm": "dept_product",
		"u_front": "dept_frontend", "u_front_lead": "dept_frontend", "u_back": "dept_backend",
		"u_back_lead": "dept_backend", "u_algo": "dept_algorithm", "u_qa": "dept_quality",
		"u_ui": "dept_design", "u_viewer": "dept_operations",
	}
	for userID, departmentID := range memberDepartments {
		if _, err := a.db.Exec(`INSERT OR IGNORE INTO department_memberships(tenant_id,department_id,user_id,is_primary,title,source,external_id,status,joined_at,updated_at)VALUES(?,?,?,1,'','seed',?,'active',?,?)`, tenantID, departmentID, userID, "seed:"+userID, now, now); err != nil {
			return err
		}
	}
	rows, err := a.db.Query(`SELECT id FROM users WHERE tenant_id=? AND password_hash='' AND id IN ('u_admin','u_member','u_pm','u_front_lead','u_back_lead','u_front','u_back','u_algo','u_ui','u_qa','u_viewer')`, tenantID)
	if err != nil {
		return err
	}
	var userIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, id)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range userIDs {
		password, err := developerSeedPassword(id)
		if err != nil {
			return err
		}
		hash, err := a.encodePassword(password)
		if err != nil {
			return err
		}
		if _, err = a.db.Exec(`UPDATE users SET password_hash=?,password_changed_at=CASE WHEN password_changed_at='' THEN ? ELSE password_changed_at END WHERE tenant_id=? AND id=? AND password_hash=''`, hash, now, tenantID, id); err != nil {
			return err
		}
	}
	_, _ = a.db.Exec(`DELETE FROM auth_sessions WHERE expires_at<?`, now)
	return nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (a *App) signSession(token string, expires time.Time) string {
	payload := "v1." + token + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, a.authSigningKey())
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) parseSignedSession(value string) (string, time.Time, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != "v1" || len(parts[1]) < 32 {
		return "", time.Time{}, errors.New("invalid session cookie")
	}
	expiresUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", time.Time{}, errors.New("invalid session expiry")
	}
	payload := strings.Join(parts[:3], ".")
	mac := hmac.New(sha256.New, a.authSigningKey())
	_, _ = mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || subtle.ConstantTimeCompare(actual, expected) != 1 {
		return "", time.Time{}, errors.New("invalid session signature")
	}
	expires := time.Unix(expiresUnix, 0).UTC()
	if !expires.After(time.Now().UTC()) {
		return "", time.Time{}, errors.New("session expired")
	}
	return parts[1], expires, nil
}

// 身份仅取已签名 Cookie，并复查数据库会话、账号激活及企业成员资格；不信任前端用户 ID。
// 存储故障与“会话不存在”分开返回，避免 SQLite 暂时繁忙造成错误登出。
func (a *App) authenticate(r *http.Request) (sessionPrincipal, error) {
	var principal sessionPrincipal
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return principal, err
	}
	token, signedExpiry, err := a.parseSignedSession(cookie.Value)
	if err != nil {
		return principal, err
	}
	var expiresAt, lastSeenAt string
	now := time.Now().UTC()
	err = a.db.QueryRowContext(r.Context(), `SELECT s.tenant_id,s.user_id,s.token_hash,s.expires_at,s.last_seen_at
FROM auth_sessions s
JOIN users u ON u.tenant_id=s.tenant_id AND u.id=s.user_id AND u.active=1
JOIN tenant_memberships tm ON tm.tenant_id=s.tenant_id AND tm.user_id=s.user_id AND tm.status='active'
WHERE s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?`, tokenDigest(token), now.Format(time.RFC3339)).Scan(&principal.TenantID, &principal.UserID, &principal.TokenHash, &expiresAt, &lastSeenAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return sessionPrincipal{}, fmt.Errorf("%w: %v", errSessionStoreUnavailable, err)
	}
	if err != nil || principal.TenantID != tenantID {
		return sessionPrincipal{}, errors.New("session not found")
	}
	dbExpiry, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || signedExpiry.Unix() != dbExpiry.Unix() {
		return sessionPrincipal{}, errors.New("session expiry mismatch")
	}
	// Even an UPDATE matching zero rows takes a SQLite writer reservation.
	// Check the timestamp before issuing SQL, keeping normal API GETs read-only.
	// last_seen 只是活跃记录：最多约 5 分钟写一次，失败不影响已经完成的会话认证。
	lastSeen, parseErr := time.Parse(time.RFC3339, lastSeenAt)
	if parseErr != nil || now.Sub(lastSeen) >= 5*time.Minute {
		// Activity bookkeeping is best-effort and must not invalidate a verified
		// session if storage is temporarily busy or the request is cancelled.
		_, _ = a.db.ExecContext(r.Context(), `UPDATE auth_sessions SET last_seen_at=? WHERE token_hash=? AND last_seen_at<?`, now.Format(time.RFC3339), principal.TokenHash, now.Add(-5*time.Minute).Format(time.RFC3339))
	}
	return principal, nil
}

func requestIP(r *http.Request) string {
	value := r.RemoteAddr
	if index := strings.LastIndex(value, ":"); index > 0 {
		value = value[:index]
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

// 库中只保存随机令牌摘要；HTTPS 反代部署须显式开启 DEVFLOW_COOKIE_SECURE，不能依赖后端 r.TLS。
func (a *App) issueSession(w http.ResponseWriter, r *http.Request, userID string) (*http.Cookie, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	now := time.Now().UTC()
	expires := now.Add(a.authSessionTTL()).Truncate(time.Second)
	_, err := a.db.Exec(`INSERT INTO auth_sessions(token_hash,tenant_id,user_id,created_at,last_seen_at,expires_at,user_agent,ip_address)VALUES(?,?,?,?,?,?,?,?)`, tokenDigest(token), tenantID, userID, now.Format(time.RFC3339), now.Format(time.RFC3339), expires.Format(time.RFC3339), truncate(r.UserAgent(), 255), requestIP(r))
	if err != nil {
		return nil, err
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: a.signSession(token, expires), Path: "/", HttpOnly: true, Secure: a.cookieSecure || r.TLS != nil, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(a.authSessionTTL().Seconds())}
	http.SetCookie(w, cookie)
	return cookie, nil
}

// bcrypt 校验在写事务外完成，但签发会话必须在同一写事务内再次核对摘要。
// 否则旧密码已校验的在途登录，可能在管理员重置或首次改密撤销会话之后重新获得有效会话。
func (a *App) issueSessionWithVerifiedHash(w http.ResponseWriter, r *http.Request, userID, verifiedHash, upgradedHash string) (*http.Cookie, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	now := time.Now().UTC()
	expires := now.Add(a.authSessionTTL()).Truncate(time.Second)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var allowed int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active' AND u.password_hash=?`, tenantID, userID, verifiedHash).Scan(&allowed); err != nil {
		return nil, err
	}
	if allowed != 1 {
		return nil, errVerifiedCredentialChanged
	}
	// 旧摘要格式升级也要受同一快照校验保护，不能覆盖并发设置的新密码。
	if upgradedHash != "" {
		if _, err = tx.ExecContext(r.Context(), `UPDATE users SET password_hash=? WHERE tenant_id=? AND id=? AND password_hash=?`, upgradedHash, tenantID, userID, verifiedHash); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO auth_sessions(token_hash,tenant_id,user_id,created_at,last_seen_at,expires_at,user_agent,ip_address)VALUES(?,?,?,?,?,?,?,?)`, tokenDigest(token), tenantID, userID, now.Format(time.RFC3339), now.Format(time.RFC3339), expires.Format(time.RFC3339), truncate(r.UserAgent(), 255), requestIP(r)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: a.signSession(token, expires), Path: "/", HttpOnly: true, Secure: a.cookieSecure || r.TLS != nil, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(a.authSessionTTL().Seconds())}
	http.SetCookie(w, cookie)
	return cookie, nil
}

func truncate(value string, length int) string {
	if len(value) > length {
		return value[:length]
	}
	return value
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, http.StatusUnsupportedMediaType, "json_required", "登录请求必须使用 JSON")
		return
	}
	var body struct {
		Email            string `json:"email"`
		Password         string `json:"password"`
		ChallengeID      string `json:"challengeId"`
		ChallengeAnswer  string `json:"challengeAnswer"`
		RefreshChallenge bool   `json:"refreshChallenge"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decodeJSON(r, &body) != nil {
		fail(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(body.Email))
	// Resolve the community alias before computing the limiter scope, so the
	// account name and email share both password checks and challenge limits.
	if identifier == "admin" {
		identifier = "admin@example.com"
	}
	if len(identifier) > 254 {
		identifier = ""
	}
	guardScope := a.loginScope(r, identifier)
	guard, guardErr := a.beginPasswordLogin(r, guardScope, body.ChallengeID, body.ChallengeAnswer, body.RefreshChallenge)
	if guardErr != nil {
		fail(w, 503, "database_unavailable", "登录服务暂时繁忙，请稍后重试")
		return
	}
	if writeLoginGuard(w, guard) {
		return
	}
	var id, name, hash, locale, timezone string
	var active, disabled, mustChange bool
	err := a.db.QueryRowContext(r.Context(), `SELECT u.id,u.name,u.password_hash,u.active,u.locale,u.timezone,u.operation_disabled,u.must_change_password FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND lower(u.email)=? AND tm.status='active'`, tenantID, identifier).Scan(&id, &name, &hash, &active, &locale, &timezone, &disabled, &mustChange)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "登录服务暂时繁忙，请稍后重试")
		return
	}
	// Unknown/inactive/legacy identities still perform bcrypt work, preventing
	// an immediate missing-user branch from becoming an account timing oracle.
	eligible := err == nil && active && hash != "" && identifier != "" && len(body.Password) <= 72
	verified := false
	if eligible && strings.HasPrefix(hash, "$2") {
		verified = verifyPassword(body.Password, hash)
	} else {
		_ = verifyPassword("dummy-login-attempt", a.dummyLoginHash())
		if eligible {
			verified = verifyPassword(body.Password, hash)
		}
	}
	if !verified {
		guard, guardErr = a.finishPasswordLogin(r, guardScope, false)
		if guardErr != nil {
			fail(w, 503, "database_unavailable", "登录服务暂时繁忙，请稍后重试")
			return
		}
		if writeLoginGuard(w, guard) {
			return
		}
		fail(w, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码不正确")
		return
	}
	setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), locale))
	var upgraded string
	if !strings.HasPrefix(hash, "$2") {
		upgraded, _ = a.encodePassword(body.Password)
	}
	if _, err = a.issueSessionWithVerifiedHash(w, r, id, hash, upgraded); err != nil {
		if errors.Is(err, errVerifiedCredentialChanged) {
			fail(w, http.StatusConflict, "credentials_changed", "账号凭据已变化，请重新登录后重试")
			return
		}
		fail(w, http.StatusServiceUnavailable, "session_error", "登录会话创建失败")
		return
	}
	// A cleanup failure must not report an already issued session as a failed
	// login; only counters are reset, never other sessions or account state.
	_, _ = a.finishPasswordLogin(r, guardScope, true)
	_, _ = a.db.Exec(`UPDATE users SET last_active=? WHERE tenant_id=? AND id=?`, time.Now().UTC().Format(time.RFC3339), tenantID, id)
	write(w, http.StatusOK, map[string]any{"authenticated": true, "user": map[string]any{"id": id, "name": name, "locale": storedLocale(locale), "timezone": timezone, "operationDisabled": disabled, "mustChangePassword": mustChange}, "supportedLocales": supportedLocales})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if token, _, parseErr := a.parseSignedSession(cookie.Value); parseErr == nil {
			// 撤销失败时保留 Cookie 供重试，不能把仍可使用的会话报告为已退出。
			if _, err := a.db.ExecContext(r.Context(), `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND token_hash=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339), tenantID, tokenDigest(token)); err != nil {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "登录服务暂时繁忙，请稍后重试")
				return
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure || r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	write(w, http.StatusOK, map[string]bool{"loggedOut": true})
}

func (a *App) encodePassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), a.bcryptCost())
	return string(hash), err
}

func (a *App) organizationDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	// 企业目录含邮箱、工号和部门归属，不能因用户恰好加入任意项目而泄露。
	// 该处理器也自行复核 organization.read，避免将来被其他路由直接复用时
	// 绕过企业级权限边界。
	if _, err := a.requireOrganizationPermission(r.Context(), a.db, "organization.read"); err != nil {
		failOrganization(w, err)
		return
	}
	rows, err := a.db.Query(`SELECT d.id,COALESCE(d.parent_id,''),d.name,d.code,d.source,d.external_id,d.status,d.sort_order FROM departments d WHERE d.tenant_id=? ORDER BY d.sort_order,d.name`, tenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	depts := []map[string]any{}
	for rows.Next() {
		var id, parentID, name, code, source, externalID, status string
		var sortOrder int
		if err = rows.Scan(&id, &parentID, &name, &code, &source, &externalID, &status, &sortOrder); err != nil {
			rows.Close()
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		depts = append(depts, map[string]any{"id": id, "parentId": parentID, "name": name, "code": code, "source": source, "externalId": externalID, "status": status, "sortOrder": sortOrder})
	}
	rows.Close()
	// 已移除成员只保留 users 历史记录；目录接口不能继续把他们作为可选人员返回。
	members := []map[string]any{}
	rows, err = a.db.Query(`SELECT u.id,u.name,u.email,u.employee_no,tm.role,tm.status,dm.department_id,dm.is_primary,dm.title,u.directory_source,u.directory_external_id
FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id
LEFT JOIN department_memberships dm ON dm.tenant_id=tm.tenant_id AND dm.user_id=tm.user_id AND dm.status='active'
WHERE tm.tenant_id=? AND tm.status!='removed' ORDER BY u.name,dm.is_primary DESC`, tenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, email, employeeNo, role, status string
		var departmentID, title, source, externalID sql.NullString
		var primary sql.NullBool
		if err = rows.Scan(&id, &name, &email, &employeeNo, &role, &status, &departmentID, &primary, &title, &source, &externalID); err != nil {
			fail(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		members = append(members, map[string]any{"id": id, "name": name, "email": email, "employeeNo": employeeNo, "tenantRole": role, "status": status, "departmentId": departmentID.String, "primaryDepartment": primary.Bool, "title": title.String, "source": source.String, "externalId": externalID.String})
	}
	var enterpriseSource, enterpriseExternalID string
	_ = a.db.QueryRow(`SELECT directory_source,directory_external_id FROM tenants WHERE id=?`, tenantID).Scan(&enterpriseSource, &enterpriseExternalID)
	write(w, http.StatusOK, map[string]any{"enterprise": map[string]string{"id": tenantID, "name": tenantName, "source": enterpriseSource, "externalId": enterpriseExternalID}, "departments": depts, "members": members})
}

func (a *App) attachMemberToDepartment(tx *sql.Tx, userID, departmentName, now string) error {
	departmentName = strings.TrimSpace(departmentName)
	if departmentName == "" {
		return nil
	}
	var departmentID string
	err := tx.QueryRow(`SELECT id FROM departments WHERE tenant_id=? AND name=? AND status='active' ORDER BY sort_order LIMIT 1`, tenantID, departmentName).Scan(&departmentID)
	if errors.Is(err, sql.ErrNoRows) {
		digest := sha256.Sum256([]byte(strings.ToLower(departmentName)))
		departmentID = "dept_local_" + base64.RawURLEncoding.EncodeToString(digest[:6])
		_, err = tx.Exec(`INSERT OR IGNORE INTO departments(id,tenant_id,name,code,source,external_id,status,sort_order,created_at,updated_at)VALUES(?,?,?,?,'local',?,'active',900,?,?)`, departmentID, tenantID, departmentName, strings.ToUpper(base64.RawURLEncoding.EncodeToString(digest[:5])), "local:"+base64.RawURLEncoding.EncodeToString(digest[:6]), now, now)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,title,source,external_id,status,joined_at,updated_at)VALUES(?,?,?,1,'','local','','active',?,?) ON CONFLICT(tenant_id,department_id,user_id) DO UPDATE SET is_primary=1,status='active',updated_at=excluded.updated_at`, tenantID, departmentID, userID, now, now)
	return err
}
