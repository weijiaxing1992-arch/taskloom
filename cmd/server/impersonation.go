package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// An impersonation never creates a member login credential. The original
// administrator session remains required for every request and for returning.
type impersonationContext struct {
	ID         int64  `json:"id"`
	AdminID    string `json:"adminId"`
	AdminName  string `json:"adminName"`
	TargetID   string `json:"targetId"`
	TargetName string `json:"targetName"`
	ExpiresAt  string `json:"expiresAt"`
	ProjectID  string `json:"projectId"`
	// ReadOnly is set only when the target is still required to complete their
	// own first password change. It permits an enterprise administrator to
	// inspect the target's effective view without ever becoming a way around
	// that credential boundary. The request router denies every mutation while
	// this flag is present.
	ReadOnly bool `json:"readOnly"`
}

func (a *App) migrateImpersonation() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS auth_impersonations(
 id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,session_hash TEXT NOT NULL,
 admin_user_id TEXT NOT NULL,target_user_id TEXT NOT NULL,project_id TEXT NOT NULL,
 reason TEXT NOT NULL,started_at TEXT NOT NULL,expires_at TEXT NOT NULL,ended_at TEXT,
 read_only INTEGER NOT NULL DEFAULT 0 CHECK(read_only IN (0,1)));
 CREATE UNIQUE INDEX IF NOT EXISTS idx_impersonation_active ON auth_impersonations(tenant_id,session_hash) WHERE ended_at IS NULL;
 CREATE TABLE IF NOT EXISTS auth_impersonation_actions(
 id INTEGER PRIMARY KEY AUTOINCREMENT,impersonation_id INTEGER NOT NULL,tenant_id TEXT NOT NULL,
 admin_user_id TEXT NOT NULL,target_user_id TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,
 project_id TEXT NOT NULL,created_at TEXT NOT NULL,status_code INTEGER NOT NULL DEFAULT 0);`)
	if err != nil {
		return err
	}
	// Older local/production databases already have the table, so CREATE TABLE
	// cannot add the safety-mode marker by itself. The default preserves every
	// historic, completed and normal delegation as its original behaviour.
	var exists int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('auth_impersonations') WHERE name='read_only'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err = a.db.Exec(`ALTER TABLE auth_impersonations ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0 CHECK(read_only IN (0,1))`)
	}
	return err
}

func (a *App) activeImpersonation(session string) (*impersonationContext, error) {
	var value impersonationContext
	err := a.db.QueryRow(`SELECT i.id,i.admin_user_id,COALESCE(ad.name,''),i.target_user_id,COALESCE(u.name,''),i.expires_at,i.project_id,i.read_only
 FROM auth_impersonations i LEFT JOIN users ad ON ad.tenant_id=i.tenant_id AND ad.id=i.admin_user_id
 LEFT JOIN users u ON u.tenant_id=i.tenant_id AND u.id=i.target_user_id
	WHERE i.tenant_id=? AND i.session_hash=? AND i.ended_at IS NULL`, tenantID, session).Scan(&value.ID, &value.AdminID, &value.AdminName, &value.TargetID, &value.TargetName, &value.ExpiresAt, &value.ProjectID, &value.ReadOnly)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (a *App) isTenantAdminChecked(user string) (bool, error) {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active' AND u.active=1 AND u.operation_disabled=0`, tenantID, user).Scan(&count)
	return count == 1, err
}

func (a *App) impersonationAPI(w http.ResponseWriter, r *http.Request, p sessionPrincipal) {
	value, err := a.activeImpersonation(p.TokenHash)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/auth/impersonation" {
		write(w, 200, map[string]any{"active": value != nil, "impersonation": value})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		fail(w, 415, "json_required", "代访问请求必须使用 JSON")
		return
	}
	if r.URL.Path == "/api/auth/impersonation/stop" {
		if value == nil {
			write(w, 200, map[string]any{"stopped": true, "projectId": projectID})
			return
		}
		tx, err := a.db.Begin()
		if err != nil {
			fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
			return
		}
		defer tx.Rollback()
		now := time.Now().UTC().Format(time.RFC3339)
		_, err = tx.Exec(`UPDATE auth_impersonations SET ended_at=? WHERE id=? AND session_hash=? AND ended_at IS NULL`, now, value.ID, p.TokenHash)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'user',?,'impersonation_stopped',?,'{}',?)`, tenantID, value.ProjectID, p.UserID, value.TargetID, jsonText(value), now)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
			return
		}
		write(w, 200, map[string]any{"stopped": true, "projectId": value.ProjectID})
		return
	}
	if r.URL.Path != "/api/auth/impersonation" {
		fail(w, 404, "not_found", "记录不存在")
		return
	}
	admin, err := a.isTenantAdminChecked(p.UserID)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if !admin {
		fail(w, 403, "admin_required", "仅企业管理员可代访问成员账号")
		return
	}
	if value != nil {
		fail(w, 409, "impersonation_active", "请先返回管理员账号，再代访问其他成员")
		return
	}
	var body struct {
		UserID string `json:"userId"`
		Reason string `json:"reason"`
	}
	if decodeJSON(r, &body) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if n := utf8.RuneCountInString(body.Reason); n < 4 || n > 500 {
		fail(w, 422, "validation_error", "代访问理由须为 4–500 字")
		return
	}
	if body.UserID == p.UserID {
		fail(w, 422, "validation_error", "不能代访问自己的账号")
		return
	}
	var targetName string
	var readOnly bool
	err = a.db.QueryRow(`SELECT u.name,u.must_change_password FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, body.UserID).Scan(&targetName, &readOnly)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "账号不可用")
		return
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	access, err := a.accessibleProjectIDsChecked(r.Context(), body.UserID)
	if err != nil {
		fail(w, 503, "database_unavailable", "项目权限服务暂时繁忙，请稍后重试")
		return
	}
	ids := []string{}
	for id := range access {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		fail(w, 422, "validation_error", "该成员没有可访问的项目")
		return
	}
	nextProject := r.Header.Get("X-TaskLoom-Project")
	if !access[nextProject] {
		nextProject = ids[0]
	}
	now := time.Now().UTC()
	expires := now.Add(30 * time.Minute).Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	defer tx.Rollback()
	// Recheck authority in the write transaction, not just before target lookup.
	res, err := tx.Exec(`INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at,read_only)
	 SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active' AND u.active=1)`, tenantID, p.TokenHash, p.UserID, body.UserID, nextProject, body.Reason, now.Format(time.RFC3339), expires, readOnly, tenantID, p.UserID)
	if err == nil {
		var n int64
		n, err = res.RowsAffected()
		if err == nil && n != 1 {
			fail(w, 403, "admin_required", "仅企业管理员可代访问成员账号")
			return
		}
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'user',?,'impersonation_started','{}',?,?)`, tenantID, nextProject, p.UserID, body.UserID, jsonText(map[string]any{"targetUserId": body.UserID, "reason": body.Reason, "expiresAt": expires, "readOnly": readOnly}), now.Format(time.RFC3339))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"started": true, "projectId": nextProject, "targetName": targetName, "expiresAt": expires, "readOnly": readOnly})
}

// Authentication remains the real administrator; only the effective business
// identity changes. Expiry fails closed instead of silently writing as admin.
func (a *App) resolveImpersonation(w http.ResponseWriter, r *http.Request, p *sessionPrincipal) (*impersonationContext, bool) {
	value, err := a.activeImpersonation(p.TokenHash)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return nil, false
	}
	if value == nil {
		return nil, true
	}
	admin, err := a.isTenantAdminChecked(p.UserID)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return nil, false
	}
	expires, parseErr := time.Parse(time.RFC3339, value.ExpiresAt)
	var valid int
	err = a.db.QueryRow(`SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, value.TargetID).Scan(&valid)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return nil, false
	}
	if !admin || parseErr != nil || !expires.After(time.Now()) || valid != 1 {
		fail(w, 403, "impersonation_expired", "代访问已失效，请返回管理员账号")
		return nil, false
	}
	p.UserID = value.TargetID
	// A newly provisioned account has not yet established its personal
	// credential. Enterprise administrators may inspect it only through this
	// explicitly marked review mode; every non-read request is rejected before
	// it reaches a business handler. Returning to the administrator remains
	// handled before impersonation resolution.
	if value.ReadOnly && r.Method != http.MethodGet && r.Method != http.MethodHead && !readOnlyImpersonationProjectVisit(r) {
		// This rejection happens before the normal scoped handler (and therefore
		// before serveImpersonated can write its usual audit row). Record it here
		// with the stored, verified target project so an attempted notification,
		// preference, or business mutation is never invisible to administrators.
		if _, err := a.db.ExecContext(r.Context(), `INSERT INTO auth_impersonation_actions(impersonation_id,tenant_id,admin_user_id,target_user_id,method,path,project_id,created_at,status_code)VALUES(?,?,?,?,?,?,?,?,?)`, value.ID, tenantID, value.AdminID, value.TargetID, r.Method, r.URL.Path, value.ProjectID, time.Now().UTC().Format(time.RFC3339), http.StatusForbidden); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
			return nil, false
		}
		fail(w, 403, "impersonation_read_only", "受限代看仅允许查看，成员完成首次改密后才能进行常规代访问")
		return nil, false
	}
	if r.Method != http.MethodGet && (strings.HasPrefix(r.URL.Path, "/api/profile") || strings.HasPrefix(r.URL.Path, "/api/members") || strings.HasPrefix(r.URL.Path, "/api/organization") || strings.HasPrefix(r.URL.Path, "/api/projects") && !strings.HasSuffix(r.URL.Path, "/visit")) {
		fail(w, 403, "impersonation_restricted", "代访问期间不能修改账号安全或成员权限")
		return nil, false
	}
	return value, true
}

type impersonationResponse struct {
	http.ResponseWriter
	status int
}

func (w *impersonationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *impersonationResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(body)
}

// readOnlyImpersonationProjectVisit is deliberately narrower than a generic
// project write exception. The client records a project visit before opening a
// notification that belongs to a different project; without this one request,
// a read-only review could not follow a notification into the target member's
// own project. projectLifecycleResource still verifies that the effective
// member belongs to the requested active project, and serveImpersonated records
// the dual-identity audit entry. No business data or personal preference is
// changed by this endpoint.
func readOnlyImpersonationProjectVisit(r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		return false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/"), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] == "visit"
}

func (a *App) serveImpersonated(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if a.impersonation == nil {
		next.ServeHTTP(w, r)
		return
	}
	value := a.impersonation
	res, err := a.db.ExecContext(r.Context(), `INSERT INTO auth_impersonation_actions(impersonation_id,tenant_id,admin_user_id,target_user_id,method,path,project_id,created_at)VALUES(?,?,?,?,?,?,?,?)`, value.ID, tenantID, value.AdminID, value.TargetID, r.Method, r.URL.Path, a.pid(), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	response := &impersonationResponse{ResponseWriter: w}
	defer func() {
		_, _ = a.db.Exec(`UPDATE auth_impersonation_actions SET status_code=? WHERE id=?`, response.status, id)
	}()
	next.ServeHTTP(response, r)
}
