package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (a *App) migrateRequirementFavorites() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_favorites(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id,requirement_id),FOREIGN KEY(requirement_id) REFERENCES requirements(id) ON DELETE CASCADE,FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE); CREATE INDEX IF NOT EXISTS idx_requirement_favorites_user ON requirement_favorites(tenant_id,user_id,created_at DESC)`)
	return err
}

// This is deliberately exact: a viewer may change a personal star, never an
// adjacent requirement subresource or the requirement itself.
func requirementFavoriteMutation(r *http.Request) bool {
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		return false
	}
	prefix := "/api/requirements/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/"), "/")
	if len(parts) != 2 || parts[1] != "favorite" {
		return false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	return err == nil && id > 0
}

type requirementFavoriteState struct {
	RequirementID int64  `json:"requirementId"`
	Favorited     bool   `json:"favorited"`
	CreatedAt     string `json:"createdAt,omitempty"`
}
type favoriteQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// A fresh permission/existence check also protects direct handler use and races
// with project revocation. Deleted, foreign and inaccessible records look alike.
func (a *App) readRequirementFavorite(ctx context.Context, query favoriteQuery, id int64) (requirementFavoriteState, error) {
	state := requirementFavoriteState{RequirementID: id}
	var created sql.NullString
	err := query.QueryRowContext(ctx, `SELECT f.created_at FROM requirements r JOIN projects p ON p.tenant_id=r.tenant_id AND p.id=r.project_id JOIN users u ON u.tenant_id=r.tenant_id AND u.id=? AND u.active=1 JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' LEFT JOIN requirement_favorites f ON f.tenant_id=r.tenant_id AND f.project_id=r.project_id AND f.requirement_id=r.id AND f.user_id=u.id WHERE p.status='active' AND r.tenant_id=? AND r.project_id=? AND r.id=? AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=r.tenant_id AND pm.project_id=r.project_id AND pm.user_id=u.id))`, a.uid(), tenantID, a.pid(), id).Scan(&created)
	state.Favorited = created.Valid
	state.CreatedAt = created.String
	return state, err
}
func favoriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "需求不存在")
	} else {
		fail(w, 503, "database_unavailable", "收藏服务暂时不可用，请稍后重试")
	}
}
func (a *App) requirementFavorite(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "private, no-store")
	expected := fmt.Sprintf("/api/requirements/%d/favorite", id)
	if id <= 0 || strings.TrimSuffix(r.URL.Path, "/") != expected {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	if r.Method == http.MethodGet {
		state, err := a.readRequirementFavorite(r.Context(), a.db, id)
		if err != nil {
			favoriteError(w, err)
			return
		}
		write(w, 200, state)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, PUT, DELETE")
		fail(w, 405, "method_not_allowed", "不支持的方法")
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
	if r.Method == http.MethodPut {
		_, err = tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO requirement_favorites(tenant_id,project_id,user_id,requirement_id,created_at) VALUES(?,?,?,?,?)`, tenantID, a.pid(), a.uid(), id, time.Now().UTC().Format(time.RFC3339Nano))
	} else {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM requirement_favorites WHERE tenant_id=? AND project_id=? AND user_id=? AND requirement_id=?`, tenantID, a.pid(), a.uid(), id)
	}
	if err != nil {
		favoriteError(w, err)
		return
	}
	state, err := a.readRequirementFavorite(r.Context(), tx, id)
	if err != nil {
		favoriteError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		favoriteError(w, err)
		return
	}
	write(w, 200, state)
}

type favoriteWorkItem struct {
	workItem
	Favorited   bool   `json:"favorited"`
	FavoritedAt string `json:"favoritedAt"`
}

func (a *App) favoriteWork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	uid, _, _, active, err := a.currentUserState(r)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		favoriteError(w, err)
		return
	}
	if !active {
		fail(w, 403, "account_disabled", "账号已停用")
		return
	}
	scope := r.URL.Query().Get("project")
	if scope == "" {
		scope = a.pid()
	}
	access, err := a.accessibleProjectIDsChecked(r.Context(), uid)
	if err != nil {
		failState(w, err)
		return
	}
	sprintChoices, selectedSprint, validSprint := a.myWorkSprintSelection(w, r, access, scope)
	if !validSprint {
		return
	}
	catalog, err := a.stateCatalogByProject(r.Context())
	if err != nil {
		failState(w, err)
		return
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		fail(w, 422, "invalid_list_query", "排序方向须为 asc 或 desc")
		return
	}
	sortKey := r.URL.Query().Get("sort")
	if sortKey == "" {
		sortKey = "favoritedAt"
	}
	if !validChoice(sortKey, []string{"favoritedAt", "updatedAt", "title", "priority", "code", "dueDate"}) {
		fail(w, 422, "invalid_list_query", "排序字段无效或已停用")
		return
	}
	// Access is filtered in the SQL itself, before any metadata is returned. Being
	// assigned to the requirement is intentionally NOT a predicate for favorites.
	query := `SELECT r.id,r.code,r.title,r.project_id,p.name,r.status,r.priority,r.assignee,r.sprint,r.end_date,r.updated_at,f.created_at FROM requirement_favorites f JOIN requirements r ON r.tenant_id=f.tenant_id AND r.project_id=f.project_id AND r.id=f.requirement_id JOIN projects p ON p.tenant_id=r.tenant_id AND p.id=r.project_id JOIN users u ON u.tenant_id=f.tenant_id AND u.id=f.user_id AND u.active=1 JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' WHERE p.status='active' AND f.tenant_id=? AND f.user_id=? AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=r.tenant_id AND pm.project_id=r.project_id AND pm.user_id=u.id))`
	args := []any{tenantID, uid}
	if scope != "all" {
		query += ` AND f.project_id=?`
		args = append(args, scope)
	}
	rows, err := a.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		favoriteError(w, err)
		return
	}
	defer rows.Close()
	items := []favoriteWorkItem{}
	counts := map[string]int{"active": 0, "all": 0, "todo": 0, "doing": 0, "due": 0, "overdue": 0, "completed": 0, "cancelled": 0}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	queryRequirementID := requirementCodeQueryID(q)
	typ := r.URL.Query().Get("type")
	status := r.URL.Query().Get("status")
	category := r.URL.Query().Get("category")
	for rows.Next() {
		var item favoriteWorkItem
		if err = rows.Scan(&item.ID, &item.Code, &item.Title, &item.ProjectID, &item.ProjectName, &item.Status, &item.Priority, &item.Assignee, &item.Sprint, &item.DueDate, &item.UpdatedAt, &item.FavoritedAt); err != nil {
			favoriteError(w, err)
			return
		}
		item.Type = "需求"
		item.Code = requirementDisplayCode(item.ID, item.Code)
		item.Role = "favorite"
		item.Favorited = true
		item.URL = fmt.Sprintf("/requirements?req=%d", item.ID)
		applyWorkStatus(&item.workItem, catalog[item.ProjectID])
		if !matchesWorkSprint(item.workItem, selectedSprint) {
			continue
		}
		if typ != "" && typ != item.Type || status != "" && status != item.Status || q != "" && !strings.Contains(strings.ToLower(item.Title+" "+item.Code+" "+item.ProjectName), q) && queryRequirementID != item.ID {
			continue
		}
		counts[item.Category]++
		counts["all"]++
		if item.Category != "completed" && item.Category != "cancelled" {
			counts["active"]++
		}
		if category == "" || category == "all" || category == item.Category || category == "active" && item.Category != "completed" && item.Category != "cancelled" {
			items = append(items, item)
		}
	}
	if err = rows.Err(); err != nil {
		favoriteError(w, err)
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		var a, b string
		switch sortKey {
		case "favoritedAt":
			a, b = left.FavoritedAt, right.FavoritedAt
		case "updatedAt":
			a, b = left.UpdatedAt, right.UpdatedAt
		case "title":
			a, b = strings.ToLower(left.Title), strings.ToLower(right.Title)
		case "priority":
			a, b = left.Priority, right.Priority
		case "dueDate":
			a, b = left.DueDate, right.DueDate
		case "code":
			if left.ID != right.ID {
				if order == "asc" {
					return left.ID < right.ID
				}
				return left.ID > right.ID
			}
		}
		if (a == "") != (b == "") {
			return a != ""
		}
		if sortKey == "favoritedAt" || sortKey == "updatedAt" {
			leftTime, leftErr := time.Parse(time.RFC3339Nano, a)
			rightTime, rightErr := time.Parse(time.RFC3339Nano, b)
			if leftErr == nil && rightErr == nil {
				if !leftTime.Equal(rightTime) {
					if order == "asc" {
						return leftTime.Before(rightTime)
					}
					return leftTime.After(rightTime)
				}
				a, b = "", ""
			}
		}
		if a != b {
			if order == "asc" {
				return a < b
			}
			return a > b
		}
		if left.ProjectID != right.ProjectID {
			return left.ProjectID < right.ProjectID
		}
		return left.ID < right.ID
	})
	write(w, 200, map[string]any{"items": items, "total": len(items), "counts": counts, "view": "favorites", "sprints": sprintChoices, "requirementStatuses": statusDefinitionsForScope(catalog, access, scope)})
}
