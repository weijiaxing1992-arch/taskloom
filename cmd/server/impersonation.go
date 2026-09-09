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
}

func (a *App) migrateImpersonation() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS auth_impersonations(
 id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,session_hash TEXT NOT NULL,
 admin_user_id TEXT NOT NULL,target_user_id TEXT NOT NULL,project_id TEXT NOT NULL,
 reason TEXT NOT NULL,started_at TEXT NOT NULL,expires_at TEXT NOT NULL,ended_at TEXT);
 CREATE UNIQUE INDEX IF NOT EXISTS idx_impersonation_active ON auth_impersonations(tenant_id,session_hash) WHERE ended_at IS NULL;
 CREATE TABLE IF NOT EXISTS auth_impersonation_actions(
 id INTEGER PRIMARY KEY AUTOINCREMENT,impersonation_id INTEGER NOT NULL,tenant_id TEXT NOT NULL,
 admin_user_id TEXT NOT NULL,target_user_id TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,
 project_id TEXT NOT NULL,created_at TEXT NOT NULL,status_code INTEGER NOT NULL DEFAULT 0);`)
	return err
}

func (a *App) activeImpersonation(session string) (*impersonationContext, error) {
	var value impersonationContext
	err := a.db.QueryRow(`SELECT i.id,i.admin_user_id,COALESCE(ad.name,''),i.target_user_id,COALESCE(u.name,''),i.expires_at,i.project_id
 FROM auth_impersonations i LEFT JOIN users ad ON ad.tenant_id=i.tenant_id AND ad.id=i.admin_user_id
 LEFT JOIN users u ON u.tenant_id=i.tenant_id AND u.id=i.target_user_id
 WHERE i.tenant_id=? AND i.session_hash=? AND i.ended_at IS NULL`, tenantID, session).Scan(&value.ID, &value.AdminID, &value.AdminName, &value.TargetID, &value.TargetName, &value.ExpiresAt, &value.ProjectID)
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
	err = a.db.QueryRow(`SELECT u.name FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, body.UserID).Scan(&targetName)
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
	nextProject := r.Header.Get("X-DevFlow-Project")
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
	res, err := tx.Exec(`INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at)
 SELECT ?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active' AND u.active=1)`, tenantID, p.TokenHash, p.UserID, body.UserID, r.Header.Get("X-DevFlow-Project"), body.Reason, now.Format(time.RFC3339), expires, tenantID, p.UserID)
	if err == nil {
		var n int64
		n, err = res.RowsAffected()
		if err == nil && n != 1 {
			fail(w, 403, "admin_required", "仅企业管理员可代访问成员账号")
			return
		}
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'user',?,'impersonation_started','{}',?,?)`, tenantID, nextProject, p.UserID, body.UserID, jsonText(map[string]any{"targetUserId": body.UserID, "reason": body.Reason, "expiresAt": expires}), now.Format(time.RFC3339))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"started": true, "projectId": nextProject, "targetName": targetName, "expiresAt": expires})
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
