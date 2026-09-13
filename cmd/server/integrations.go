package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 基础读取作为一个整体授权：需求、迭代、缺陷、用例会互相返回关联摘要。
// 将其拆成表面独立的 read scope 会让关联字段成为权限旁路。
var integrationReadScopes = []string{"requirements:read", "iterations:read", "defects:read", "test-cases:read"}
var integrationScopes = []map[string]string{
	{"key": "requirements:read", "label": "读取需求"}, {"key": "iterations:read", "label": "读取迭代"},
	{"key": "defects:read", "label": "读取缺陷"}, {"key": "test-cases:read", "label": "读取测试用例"},
	{"key": "notifications:read", "label": "读取本人通知"},
	{"key": "requirements:write", "label": "创建和更新需求"}, {"key": "iterations:write", "label": "创建和更新迭代"},
	{"key": "defects:write", "label": "创建和更新缺陷"}, {"key": "test-cases:write", "label": "创建和更新测试用例"},
	{"key": "comments:write", "label": "发布协作评论"}, {"key": "executions:read", "label": "读取测试执行"},
	{"key": "executions:write", "label": "回写测试结果"}, {"key": "release-notes:read", "label": "读取升级日志"},
}

type integrationCredential struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"createdAt"`
	ExpiresAt  string   `json:"expiresAt"`
	LastUsedAt string   `json:"lastUsedAt"`
	RevokedAt  string   `json:"revokedAt"`
}

func (a *App) migrateIntegrations() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS integration_tokens(
 id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,
 name TEXT NOT NULL,prefix TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,password_fingerprint TEXT NOT NULL,
 scopes_json TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL,last_used_at TEXT NOT NULL DEFAULT '',revoked_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_integration_tokens_owner ON integration_tokens(tenant_id,project_id,user_id,created_at);
CREATE TABLE IF NOT EXISTS integration_requests(
 id TEXT PRIMARY KEY,token_id TEXT NOT NULL,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,
 method TEXT NOT NULL,path TEXT NOT NULL,status INTEGER NOT NULL DEFAULT 0,replayed INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_integration_requests_token ON integration_requests(token_id,created_at);
CREATE INDEX IF NOT EXISTS idx_integration_requests_scope ON integration_requests(tenant_id,project_id,user_id,created_at);
CREATE TABLE IF NOT EXISTS integration_idempotency(
 token_id TEXT NOT NULL,key_hash TEXT NOT NULL,fingerprint TEXT NOT NULL,status INTEGER NOT NULL DEFAULT 0,
 response_json BLOB,etag TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,PRIMARY KEY(token_id,key_hash)
);`)
	if err != nil {
		return err
	}
	return a.migrateIntegrationRevisions()
}

func integrationRandom(prefix string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func integrationHas(scopes []string, key string) bool {
	for _, value := range scopes {
		if key == value {
			return true
		}
	}
	return false
}

// 不接受跨站浏览器请求；非浏览器客户端没有 Origin，可正常使用 Bearer。
func (a *App) integrationSameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	// Production terminates HTTPS at the reverse proxy. cookieSecure is the
	// explicit external-HTTPS setting; client-controlled forwarding headers
	// are not an authority for accepting a different browser origin.
	scheme := "http"
	if r.TLS != nil || a.cookieSecure {
		scheme = "https"
	}
	return err == nil && u.User == nil && u.Host == r.Host && u.Scheme == scheme && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
}

func integrationDecode(r *http.Request, v any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil {
		return err
	}
	if len(b) > 2<<20 {
		return errors.New("body too large")
	}
	return json.Unmarshal(b, v)
}

func (a *App) integrations(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.impersonation != nil {
		fail(w, 403, "impersonation_forbidden", "代访问期间不能管理 AI 协作凭证")
		return
	}
	if !a.integrationSameOrigin(r) {
		fail(w, 403, "origin_forbidden", "不允许跨站访问协作平台")
		return
	}
	if err := a.requireOperationAccess(r.Context(), a.db); err != nil {
		failOrganization(w, err)
		return
	}
	_, _, role, active, err := a.currentUserState(r)
	if err != nil || !active {
		fail(w, 403, "forbidden", "当前账号不可访问协作平台")
		return
	}
	canWrite := role != "" && role != "viewer"
	var tenantAdmin int
	if err = a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active' AND tm.role='tenant_admin'`, tenantID, a.uid()).Scan(&tenantAdmin); err != nil {
		fail(w, 503, "database_unavailable", "协作权限暂时无法读取")
		return
	}
	availableScopes := integrationScopes
	if tenantAdmin != 1 {
		availableScopes = make([]map[string]string, 0, len(integrationScopes)-1)
		for _, scope := range integrationScopes {
			if scope["key"] != "release-notes:read" {
				availableScopes = append(availableScopes, scope)
			}
		}
	}
	switch {
	case r.URL.Path == "/api/integrations" && r.Method == http.MethodGet:
		tokens, err := a.listIntegrationTokens(r)
		if err != nil {
			fail(w, 503, "database_unavailable", "协作凭证暂时无法读取")
			return
		}
		write(w, 200, map[string]any{"projectId": a.pid(), "userId": a.uid(), "tokens": tokens, "scopes": availableScopes, "canWrite": canWrite, "canManage": true, "mcpPath": "/api/open/mcp", "apiPath": "/api/open/v1", "maxExpiryDays": 90})
	case r.URL.Path == "/api/integrations/tokens" && r.Method == http.MethodPost:
		a.createIntegrationToken(w, r, canWrite)
	case strings.HasPrefix(r.URL.Path, "/api/integrations/tokens/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/api/integrations/tokens/")
		if !regexp.MustCompile(`^it_[A-Za-z0-9_-]{43}$`).MatchString(id) {
			fail(w, 404, "not_found", "凭证不存在")
			return
		}
		res, err := a.db.ExecContext(r.Context(), `UPDATE integration_tokens SET revoked_at=CASE WHEN revoked_at='' THEN ? ELSE revoked_at END WHERE id=? AND tenant_id=? AND project_id=? AND user_id=?`, time.Now().UTC().Format(time.RFC3339), id, tenantID, a.pid(), a.uid())
		if err != nil {
			fail(w, 503, "database_unavailable", "撤销凭证失败，请重试")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "not_found", "凭证不存在")
			return
		}
		write(w, 200, map[string]bool{"revoked": true})
	case r.URL.Path == "/api/integrations/logs" && r.Method == http.MethodGet:
		a.integrationLogs(w, r)
	case r.URL.Path == "/api/integrations/context" && r.Method == http.MethodGet:
		a.integrationContext(w, r)
	case r.URL.Path == "/api/integrations/openapi" && r.Method == http.MethodGet:
		write(w, 200, integrationOpenAPISpec())
	default:
		fail(w, 405, "method_not_allowed", "不支持的协作平台操作")
	}
}

func (a *App) listIntegrationTokens(r *http.Request) ([]integrationCredential, error) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,prefix,scopes_json,created_at,expires_at,last_used_at,revoked_at FROM integration_tokens WHERE tenant_id=? AND project_id=? AND user_id=? ORDER BY created_at DESC,id DESC LIMIT 100`, tenantID, a.pid(), a.uid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []integrationCredential{}
	for rows.Next() {
		var x integrationCredential
		var scopes string
		if err := rows.Scan(&x.ID, &x.Name, &x.Prefix, &scopes, &x.CreatedAt, &x.ExpiresAt, &x.LastUsedAt, &x.RevokedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &x.Scopes); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	return items, rows.Err()
}

func (a *App) createIntegrationToken(w http.ResponseWriter, r *http.Request, canWrite bool) {
	var input struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expiresInDays"`
	}
	if integrationDecode(r, &input) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if len([]rune(input.Name)) < 1 || len([]rune(input.Name)) > 80 || input.ExpiresInDays < 1 || input.ExpiresInDays > 90 || len(input.Scopes) > len(integrationScopes) {
		fail(w, 422, "validation_error", "名称须为 1–80 字，有效期须为 1–90 天")
		return
	}
	seen := map[string]bool{}
	for _, scope := range input.Scopes {
		found := false
		for _, allowed := range integrationScopes {
			if scope == allowed["key"] {
				found = true
			}
		}
		if !found || seen[scope] {
			fail(w, 422, "invalid_scope", "包含无效或重复权限")
			return
		}
		seen[scope] = true
		if strings.HasSuffix(scope, ":write") && !canWrite {
			fail(w, 403, "scope_forbidden", "只读成员不能创建写入凭证")
			return
		}
	}
	for _, scope := range integrationReadScopes {
		if !seen[scope] {
			fail(w, 422, "context_scope_required", "关联研发上下文的四项读取权限须一起授权")
			return
		}
	}
	if seen["executions:write"] && !seen["executions:read"] {
		fail(w, 422, "invalid_scope", "回写测试结果需要同时授权读取测试执行")
		return
	}
	sort.Strings(input.Scopes)
	secret, err := integrationRandom("df_")
	if err != nil {
		fail(w, 503, "token_unavailable", "凭证暂时无法生成")
		return
	}
	id, err := integrationRandom("it_")
	if err != nil {
		fail(w, 503, "token_unavailable", "凭证暂时无法生成")
		return
	}
	now := time.Now().UTC()
	credential := integrationCredential{ID: id, Name: input.Name, Prefix: secret[:11], Scopes: input.Scopes, CreatedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(time.Duration(input.ExpiresInDays) * 24 * time.Hour).Format(time.RFC3339)}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		return
	}
	defer tx.Rollback()
	// 先取得写锁，再核对当前会话及项目角色，防止撤销/改密期间用旧会话
	// 签发出绑定新口令摘要的长期凭据。不能只信入口处已读到的 canWrite。
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		return
	}
	var currentRole, verifiedPassword string
	err = tx.QueryRowContext(r.Context(), `SELECT CASE WHEN tm.role='tenant_admin' THEN tm.role ELSE pm.role END,u.password_hash FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id JOIN projects p ON p.tenant_id=u.tenant_id AND p.id=? LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.project_id=p.id AND pm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND u.must_change_password=0 AND tm.status='active' AND p.status='active' AND (tm.role='tenant_admin' OR pm.user_id IS NOT NULL) AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>? AND NOT EXISTS(SELECT 1 FROM auth_impersonations i WHERE i.tenant_id=u.tenant_id AND i.session_hash=s.token_hash AND i.ended_at IS NULL)`, a.pid(), tenantID, a.uid(), a.sessionToken, credential.CreatedAt).Scan(&currentRole, &verifiedPassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 403, "forbidden", "当前账号不可访问协作平台")
		} else {
			fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		}
		return
	}
	for _, scope := range input.Scopes {
		if strings.HasSuffix(scope, ":write") && (currentRole == "" || currentRole == "viewer") {
			fail(w, 403, "scope_forbidden", "只读成员不能创建写入凭证")
			return
		}
		if scope == "release-notes:read" && currentRole != "tenant_admin" {
			fail(w, 403, "scope_forbidden", "仅企业管理员可签发升级日志读取凭据")
			return
		}
	}
	if err := a.requireOperationAccess(r.Context(), tx); err != nil {
		failOrganization(w, err)
		return
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM integration_tokens WHERE tenant_id=? AND project_id=? AND user_id=? AND revoked_at='' AND expires_at>?`, tenantID, a.pid(), a.uid(), credential.CreatedAt).Scan(&count); err != nil {
		fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		return
	}
	if count >= 20 {
		fail(w, 409, "credential_limit", "最多保留 20 个有效凭证，请先撤销不用的凭证")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO integration_tokens(id,tenant_id,project_id,user_id,name,prefix,token_hash,password_fingerprint,scopes_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, tenantID, a.pid(), a.uid(), input.Name, credential.Prefix, tokenDigest(secret), tokenDigest(verifiedPassword), jsonText(input.Scopes), credential.CreatedAt, credential.ExpiresAt)
	if err != nil {
		fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "database_unavailable", "凭证暂时无法创建")
		return
	}
	// 明文只出现在本次响应，不落日志、不回显旧密钥、不写入页面存储。
	write(w, 201, map[string]any{"token": secret, "credential": credential})
}

func (a *App) integrationLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT r.id,t.name,r.method,r.path,r.status,r.created_at,r.replayed FROM integration_requests r JOIN integration_tokens t ON t.id=r.token_id WHERE r.tenant_id=? AND r.project_id=? AND r.user_id=? ORDER BY r.created_at DESC,r.id DESC LIMIT 100`, tenantID, a.pid(), a.uid())
	if err != nil {
		fail(w, 503, "database_unavailable", "调用记录暂时无法读取")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, method, path, created string
		var status int
		var replayed bool
		if rows.Scan(&id, &name, &method, &path, &status, &created, &replayed) != nil {
			fail(w, 503, "database_unavailable", "调用记录暂时无法读取")
			return
		}
		items = append(items, map[string]any{"id": id, "tokenName": name, "method": method, "path": path, "status": status, "createdAt": created, "replayed": replayed})
	}
	if rows.Err() != nil {
		fail(w, 503, "database_unavailable", "调用记录暂时无法读取")
		return
	}
	write(w, 200, map[string]any{"items": items, "projectId": a.pid()})
}

func (a *App) authenticateIntegration(r *http.Request) (*App, integrationCredential, error) {
	var key integrationCredential
	auth := r.Header.Get("Authorization")
	if !regexp.MustCompile(`^Bearer df_[A-Za-z0-9_-]{43}$`).MatchString(auth) {
		return nil, key, sql.ErrNoRows
	}
	var pid, user, scopes, fingerprint, password string
	err := a.db.QueryRowContext(r.Context(), `SELECT t.id,t.name,t.prefix,t.scopes_json,t.expires_at,t.project_id,t.user_id,t.password_fingerprint,u.password_hash
FROM integration_tokens t JOIN users u ON u.tenant_id=t.tenant_id AND u.id=t.user_id AND u.active=1
JOIN tenant_memberships tm ON tm.tenant_id=t.tenant_id AND tm.user_id=t.user_id AND tm.status='active'
JOIN projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id AND p.status='active'
WHERE t.token_hash=? AND t.tenant_id=? AND t.revoked_at='' AND t.expires_at>? AND u.operation_disabled=0 AND u.must_change_password=0
AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=t.user_id))`, tokenDigest(strings.TrimPrefix(auth, "Bearer ")), tenantID, time.Now().UTC().Format(time.RFC3339)).Scan(&key.ID, &key.Name, &key.Prefix, &scopes, &key.ExpiresAt, &pid, &user, &fingerprint, &password)
	if err != nil {
		return nil, key, err
	}
	if fingerprint != tokenDigest(password) {
		return nil, key, sql.ErrNoRows
	}
	if err = json.Unmarshal([]byte(scopes), &key.Scopes); err != nil {
		return nil, key, err
	}
	scoped := *a
	scoped.project = pid
	scoped.user = user
	scoped.sessionToken = ""
	scoped.impersonation = nil
	return &scoped, key, nil
}

func (a *App) integrationExternal(w http.ResponseWriter, r *http.Request) {
	if !a.integrationSameOrigin(r) {
		fail(w, 403, "origin_forbidden", "不允许跨站访问协作接口")
		return
	}
	scoped, key, err := a.authenticateIntegration(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="TaskLoom"`)
			fail(w, 401, "invalid_api_token", "协作凭证无效、已过期或权限已撤回")
		} else {
			fail(w, 503, "database_unavailable", "凭证服务暂时不可用")
		}
		return
	}
	if project := r.Header.Get("X-TaskLoom-Project"); project != "" && project != scoped.pid() {
		fail(w, 403, "project_forbidden", "凭证只允许访问绑定项目")
		return
	}
	if r.URL.Path == "/api/open/mcp" {
		serveIntegrationMCP(w, r, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) { scoped.integrationServeAuthorized(w, request, key) }), key.Scopes)
		return
	}
	scoped.integrationServeAuthorized(w, r, key)
}

// 只记录方法、固定路径及结果，不记录 Authorization、正文、查询词或密码。
func (a *App) integrationBeginRequest(w http.ResponseWriter, r *http.Request, key integrationCredential) (string, bool) {
	id, err := integrationRandom("ir_")
	if err != nil {
		fail(w, 503, "service_unavailable", "协作接口暂时不可用")
		return "", false
	}
	// 限额判断和占位写入在一条原子语句中完成，并发请求不能一起穿透剩余名额。
	result, err := a.db.ExecContext(r.Context(), `INSERT INTO integration_requests(id,token_id,tenant_id,project_id,user_id,method,path,created_at) SELECT ?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM integration_requests WHERE token_id=? AND created_at>?)<180`, id, key.ID, tenantID, a.pid(), a.uid(), r.Method, r.URL.Path, time.Now().UTC().Format(time.RFC3339Nano), key.ID, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		fail(w, 503, "database_unavailable", "调用审计暂时不可用")
		return "", false
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited", "请求过于频繁，请稍后重试")
		return "", false
	}
	w.Header().Set("X-TaskLoom-Request-Id", id)
	return id, true
}

var integrationIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,14}$`)

func integrationPositive(raw string) (int64, bool) {
	if !integrationIDPattern.MatchString(raw) {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value <= 9007199254740991
}
