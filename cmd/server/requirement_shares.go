package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Public links contain a reviewed, read-only text snapshot, never internal people,
// comments, credentials, or authenticated attachment URLs. Only token hashes persist.
func (a *App) migrateRequirementShares() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_shares (
 id INTEGER PRIMARY KEY AUTOINCREMENT, token_hash TEXT NOT NULL UNIQUE,
 tenant_id TEXT NOT NULL, project_id TEXT NOT NULL, requirement_id INTEGER NOT NULL,
 creator_id TEXT NOT NULL, title TEXT NOT NULL, description TEXT NOT NULL,
 acceptance TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS requirement_shares_owner ON requirement_shares(tenant_id,project_id,creator_id,requirement_id)`)
	return err
}
func (a *App) requirementShares(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("requirementId"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 400, "invalid_input", "需求编号无效")
		return
	}
	if a.impersonation != nil {
		fail(w, 403, "forbidden", "代访问期间不能管理公开分享")
		return
	}
	if _, err = a.readRequirementFavorite(r.Context(), a.db, id); err != nil {
		favoriteError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rows, err := a.db.QueryContext(r.Context(), `SELECT id,created_at,expires_at FROM requirement_shares WHERE tenant_id=? AND project_id=? AND creator_id=? AND requirement_id=? AND revoked=0 AND expires_at>? ORDER BY id DESC LIMIT 100`, tenantID, a.pid(), a.uid(), id, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			favoriteError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var shareID int64
			var created, expires string
			if err = rows.Scan(&shareID, &created, &expires); err != nil {
				favoriteError(w, err)
				return
			}
			items = append(items, map[string]any{"id": shareID, "createdAt": created, "expiresAt": expires})
		}
		if err = rows.Err(); err != nil {
			favoriteError(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
	case http.MethodPost:
		var input struct {
			Confirmed bool `json:"confirmed"`
		}
		if err = decodeOrganizationJSON(w, r, &input); err != nil {
			fail(w, 400, "invalid_input", "参数无效")
			return
		}
		if !input.Confirmed {
			fail(w, 400, "confirmation_required", "请确认公开正文快照")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			favoriteError(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = a.readRequirementFavorite(r.Context(), tx, id); err != nil {
			favoriteError(w, err)
			return
		}
		var count int
		if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM requirement_shares WHERE tenant_id=? AND creator_id=? AND revoked=0 AND expires_at>?`, tenantID, a.uid(), time.Now().UTC().Format(time.RFC3339)).Scan(&count); err != nil {
			favoriteError(w, err)
			return
		}
		if count >= 100 {
			fail(w, 429, "share_limit", "最多保留 100 个有效公开分享，请先撤销旧链接")
			return
		}
		var title, description, acceptance string
		if err = tx.QueryRowContext(r.Context(), `SELECT title,description,acceptance FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&title, &description, &acceptance); err != nil {
			favoriteError(w, err)
			return
		}
		tokenBytes := make([]byte, 32)
		if _, err = rand.Read(tokenBytes); err != nil {
			favoriteError(w, err)
			return
		}
		token := hex.EncodeToString(tokenBytes)
		sum := sha256.Sum256([]byte(token))
		now := time.Now().UTC()
		expires := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
		result, err := tx.ExecContext(r.Context(), `INSERT INTO requirement_shares(token_hash,tenant_id,project_id,requirement_id,creator_id,title,description,acceptance,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, hex.EncodeToString(sum[:]), tenantID, a.pid(), id, a.uid(), title, description, acceptance, now.Format(time.RFC3339), expires)
		if err != nil {
			favoriteError(w, err)
			return
		}
		shareID, err := result.LastInsertId()
		if err != nil {
			favoriteError(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			favoriteError(w, err)
			return
		}
		write(w, 201, map[string]any{"id": shareID, "path": "/share/requirements/" + token, "expiresAt": expires})
	case http.MethodDelete:
		shareID, err := strconv.ParseInt(r.URL.Query().Get("shareId"), 10, 64)
		if err != nil || shareID < 1 {
			fail(w, 400, "invalid_input", "分享编号无效")
			return
		}
		_, err = a.db.ExecContext(r.Context(), `UPDATE requirement_shares SET revoked=1 WHERE id=? AND tenant_id=? AND project_id=? AND requirement_id=? AND creator_id=?`, shareID, tenantID, a.pid(), id, a.uid())
		if err != nil {
			favoriteError(w, err)
			return
		}
		write(w, 200, map[string]bool{"revoked": true})
	default:
		fail(w, 405, "method_not_allowed", "不支持的方法")
	}
}
func (a *App) publicRequirementShare(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "仅支持只读访问")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/api/public/requirement-shares/")
	if len(token) != 64 {
		fail(w, 404, "not_found", "分享不存在或已失效")
		return
	}
	if _, err := hex.DecodeString(token); err != nil {
		fail(w, 404, "not_found", "分享不存在或已失效")
		return
	}
	sum := sha256.Sum256([]byte(token))
	var project, creator, title, description, acceptance, created, expires string
	var id int64
	err := a.db.QueryRowContext(r.Context(), `SELECT project_id,creator_id,requirement_id,title,description,acceptance,created_at,expires_at FROM requirement_shares WHERE token_hash=? AND tenant_id=? AND revoked=0 AND expires_at>?`, hex.EncodeToString(sum[:]), tenantID, time.Now().UTC().Format(time.RFC3339)).Scan(&project, &creator, &id, &title, &description, &acceptance, &created, &expires)
	if err != nil {
		favoriteError(w, err)
		return
	}
	scoped := *a
	scoped.user = creator
	scoped.project = project
	if _, err = scoped.readRequirementFavorite(r.Context(), a.db, id); err != nil {
		favoriteError(w, err)
		return
	}
	write(w, 200, map[string]string{"title": title, "description": description, "acceptance": acceptance, "createdAt": created, "expiresAt": expires})
}
