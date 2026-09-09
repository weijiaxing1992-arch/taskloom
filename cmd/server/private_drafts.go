package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const privateDraftRequestMaxSize int64 = 30 << 20
const privateDraftLimit = 100

var privateDraftIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type privateDraft struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	TargetID  string          `json:"targetId"`
	Title     string          `json:"title"`
	Version   int64           `json:"version"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Context   json.RawMessage `json:"context,omitempty"`
}

// 私有草稿只有此独立表；不会创建正式工作项、附件、活动或通知。
// 迁移不从正式需求的“草稿”状态导入，以免把共享业务数据误当作个人未发布内容。
func (a *App) migratePrivateDrafts() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS private_workitem_drafts(
tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,id TEXT NOT NULL,
kind TEXT NOT NULL CHECK(kind IN ('requirement','defect')),target_id TEXT NOT NULL DEFAULT '',
title TEXT NOT NULL DEFAULT '',payload_json TEXT NOT NULL,context_json TEXT NOT NULL DEFAULT '{}',
version INTEGER NOT NULL CHECK(version>0),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
PRIMARY KEY(tenant_id,project_id,user_id,id));
CREATE INDEX IF NOT EXISTS idx_private_drafts_owner_updated ON private_workitem_drafts(tenant_id,project_id,user_id,updated_at DESC,id);
-- SQLite 没有跨行计数约束。该轻量锁让同一用户/项目的新增、覆盖与删除串行，
-- 以便可靠执行 100 条上限和乐观版本检查，而不是在高并发下静默覆盖或超额。
CREATE TABLE IF NOT EXISTS private_workitem_draft_locks(
tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0,
PRIMARY KEY(tenant_id,project_id,user_id));`)
	return err
}

func privateDraftPath(path string) (string, bool) {
	if path == "/api/drafts" {
		return "", true
	}
	id := strings.TrimPrefix(path, "/api/drafts/")
	return id, strings.HasPrefix(path, "/api/drafts/") && privateDraftIDPattern.MatchString(id)
}

func isPrivateDraftWrite(r *http.Request) bool {
	id, ok := privateDraftPath(r.URL.Path)
	return r.Method == http.MethodPut && ok && id != ""
}

func failPrivateDraft(w http.ResponseWriter, err error) {
	var problem *organizationError
	if errors.As(err, &problem) {
		fail(w, problem.Status, problem.Code, problem.Message)
	} else {
		fail(w, 503, "draft_unavailable", "草稿服务暂时不可用，请稍后重试")
	}
}

func privateDraftNotFound() error {
	return &organizationError{404, "draft_not_found", "草稿不存在或无权访问"}
}

// 在事务内重复校验实际会话、首次改密、业务禁用和项目权限。
// 即便入口已经认证，写锁等待期间撤权/归档/切换代访问也不能越过草稿的本人边界。
func (a *App) privateDraftAccess(ctx context.Context, tx *sql.Tx, writing bool) error {
	if a.impersonation != nil {
		return &organizationError{403, "draft_impersonation_forbidden", "代访问期间不能访问私人草稿"}
	}
	if err := a.requireOperationAccess(ctx, tx); err != nil {
		return err
	}
	var role string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN tm.role='tenant_admin' THEN 'tenant_admin' ELSE COALESCE(m.role,tm.role) END FROM users u
JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active'
JOIN projects p ON p.tenant_id=u.tenant_id AND p.id=? AND p.status='active'
JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>?
LEFT JOIN project_members m ON m.tenant_id=p.tenant_id AND m.project_id=p.id AND m.user_id=u.id
WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND
(tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=u.id))
AND NOT EXISTS(SELECT 1 FROM auth_impersonations i WHERE i.tenant_id=s.tenant_id AND i.session_hash=s.token_hash AND i.ended_at IS NULL)`, a.pid(), a.sessionToken, orgNow(), tenantID, a.uid()).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return &organizationError{403, "draft_forbidden", "当前账号或项目暂不可访问草稿"}
	}
	if err != nil {
		return err
	}
	if writing && !validChoice(role, []string{"tenant_admin", "project_admin", "product", "frontend", "backend", "algorithm", "ui", "frontend_lead", "backend_lead", "qa"}) {
		return &organizationError{403, "forbidden", "当前角色仅可查看"}
	}
	return nil
}

// privateDraftWriteLock 只在写入草稿前调用。先写入极小的作用域锁行会获取 SQLite
// 写锁，之后同一作用域的版本比较和数量检查都在同一个事务快照里完成。
func (a *App) privateDraftWriteLock(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO private_workitem_draft_locks(tenant_id,project_id,user_id)VALUES(?,?,?)`, tenantID, a.pid(), a.uid()); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE private_workitem_draft_locks SET revision=revision+1 WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, a.pid(), a.uid())
	return err
}

func decodePrivateDraftBody(w http.ResponseWriter, r *http.Request, target any, max int64) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return &organizationError{415, "json_required", "草稿请求必须使用 JSON"}
	}
	if r.ContentLength > max {
		return &organizationError{413, "draft_too_large", "单份草稿不能超过 30 MB"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err == nil {
		if err = decoder.Decode(new(any)); err == io.EOF {
			return nil
		}
	}
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		return &organizationError{413, "draft_too_large", "单份草稿不能超过 30 MB"}
	}
	return &organizationError{400, "invalid_json", "请求格式不正确"}
}

func canonicalDraftObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, orgInvalid("草稿内容和上下文必须为 JSON 对象")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, orgInvalid("草稿内容和上下文必须为 JSON 对象")
	}
	return json.Marshal(object)
}

func privateDraftTitle(payload json.RawMessage) string {
	var input struct {
		Title json.RawMessage `json:"title"`
	}
	_ = json.Unmarshal(payload, &input)
	var value string
	_ = json.Unmarshal(input.Title, &value)
	value = strings.TrimSpace(strings.Map(func(char rune) rune {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			return ' '
		}
		return char
	}, value))
	chars := []rune(value)
	if len(chars) > 180 {
		value = string(chars[:180])
	}
	return value
}

func (a *App) privateDraftTarget(ctx context.Context, tx *sql.Tx, kind, target string) error {
	if target == "" {
		return nil
	}
	id, err := strconv.ParseInt(target, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != target {
		return orgInvalid("草稿目标编号无效")
	}
	table := "requirements"
	if kind == "defect" {
		table = "defects"
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return &organizationError{422, "draft_target_invalid", "草稿关联工作项不存在或不属于当前项目"}
	}
	return nil
}

func (a *App) readPrivateDraft(ctx context.Context, tx *sql.Tx, id string) (privateDraft, error) {
	var item privateDraft
	var payload, metadata string
	err := tx.QueryRowContext(ctx, `SELECT id,kind,target_id,title,version,created_at,updated_at,payload_json,context_json FROM private_workitem_drafts WHERE tenant_id=? AND project_id=? AND user_id=? AND id=?`, tenantID, a.pid(), a.uid(), id).Scan(&item.ID, &item.Kind, &item.TargetID, &item.Title, &item.Version, &item.CreatedAt, &item.UpdatedAt, &payload, &metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return item, privateDraftNotFound()
	}
	if err != nil {
		return item, err
	}
	item.Payload, item.Context = json.RawMessage(payload), json.RawMessage(metadata)
	if len(payload)+len(metadata) > int(privateDraftRequestMaxSize) || !json.Valid(item.Payload) || !json.Valid(item.Context) {
		return item, errors.New("stored draft is invalid")
	}
	return item, nil
}

func (a *App) privateDrafts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, ok := privateDraftPath(r.URL.Path)
	if !ok {
		failPrivateDraft(w, privateDraftNotFound())
		return
	}
	if r.Method != http.MethodGet && !(id != "" && (r.Method == http.MethodPut || r.Method == http.MethodDelete)) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if a.impersonation != nil {
		fail(w, 403, "draft_impersonation_forbidden", "代访问期间不能访问私人草稿")
		return
	}
	var input struct {
		Kind        string          `json:"kind"`
		TargetID    string          `json:"targetId"`
		Context     json.RawMessage `json:"context"`
		Payload     json.RawMessage `json:"payload"`
		BaseVersion *int64          `json:"baseVersion"`
	}
	var deletion struct {
		Version *int64 `json:"version"`
	}
	if r.Method == http.MethodPut {
		if err := decodePrivateDraftBody(w, r, &input, privateDraftRequestMaxSize); err != nil {
			failPrivateDraft(w, err)
			return
		}
		if !validChoice(input.Kind, []string{"requirement", "defect"}) || input.BaseVersion == nil || *input.BaseVersion < 0 || *input.BaseVersion > 9007199254740990 {
			fail(w, 422, "draft_invalid", "草稿类型或版本无效")
			return
		}
		if len(input.Context) == 0 {
			input.Context = json.RawMessage(`{}`)
		}
		var err error
		if input.Payload, err = canonicalDraftObject(input.Payload); err == nil {
			input.Context, err = canonicalDraftObject(input.Context)
		}
		if err != nil {
			failPrivateDraft(w, err)
			return
		}
	} else if r.Method == http.MethodDelete {
		if err := decodePrivateDraftBody(w, r, &deletion, 1024); err != nil {
			failPrivateDraft(w, err)
			return
		}
		if deletion.Version == nil || *deletion.Version < 1 {
			fail(w, 422, "draft_invalid", "删除草稿必须提供当前版本")
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failPrivateDraft(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.privateDraftAccess(r.Context(), tx, r.Method != http.MethodGet); err != nil {
		failPrivateDraft(w, err)
		return
	}
	if r.Method != http.MethodGet {
		if err = a.privateDraftWriteLock(r.Context(), tx); err != nil {
			failPrivateDraft(w, err)
			return
		}
	}
	if id == "" {
		rows, err := tx.QueryContext(r.Context(), `SELECT id,kind,target_id,title,version,created_at,updated_at FROM private_workitem_drafts WHERE tenant_id=? AND project_id=? AND user_id=? ORDER BY updated_at DESC,id LIMIT ?`, tenantID, a.pid(), a.uid(), privateDraftLimit)
		if err != nil {
			failPrivateDraft(w, err)
			return
		}
		items := []privateDraft{}
		for rows.Next() {
			var item privateDraft
			if err = rows.Scan(&item.ID, &item.Kind, &item.TargetID, &item.Title, &item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
				break
			}
			items = append(items, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failPrivateDraft(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items, "limit": privateDraftLimit})
		return
	}
	previous, existingErr := a.readPrivateDraft(r.Context(), tx, id)
	var missing *organizationError
	missingEntry := errors.As(existingErr, &missing) && missing.Code == "draft_not_found"
	if existingErr != nil && (!missingEntry || r.Method != http.MethodPut) {
		failPrivateDraft(w, existingErr)
		return
	}
	if r.Method == http.MethodGet {
		if err = tx.Commit(); err != nil {
			failPrivateDraft(w, err)
			return
		}
		write(w, 200, map[string]any{"draft": previous})
		return
	}
	version := int64(0)
	if r.Method == http.MethodDelete {
		version = *deletion.Version
	} else {
		version = *input.BaseVersion
	}
	if version != previous.Version {
		fail(w, 409, "draft_conflict", "草稿已在其他页面更新，请保留当前内容并另存草稿")
		return
	}
	if r.Method == http.MethodDelete {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), `DELETE FROM private_workitem_drafts WHERE tenant_id=? AND project_id=? AND user_id=? AND id=? AND version=?`, tenantID, a.pid(), a.uid(), id, version)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			if err == nil && changed != 1 {
				fail(w, 409, "draft_conflict", "草稿已在其他页面更新，请保留当前内容并另存草稿")
				return
			}
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failPrivateDraft(w, err)
			return
		}
		write(w, 200, map[string]bool{"deleted": true})
		return
	}
	if existingErr == nil && (input.Kind != previous.Kind || input.TargetID != previous.TargetID) {
		fail(w, 422, "draft_target_changed", "草稿类型与目标不能变更，请另存新草稿")
		return
	}
	if err = a.privateDraftTarget(r.Context(), tx, input.Kind, input.TargetID); err != nil {
		failPrivateDraft(w, err)
		return
	}
	if existingErr == nil && bytes.Equal(previous.Payload, input.Payload) && bytes.Equal(previous.Context, input.Context) {
		if err = tx.Commit(); err != nil {
			failPrivateDraft(w, err)
			return
		}
		write(w, 200, map[string]any{"draft": previous})
		return
	}
	if missingEntry {
		var count int
		if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM private_workitem_drafts WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, a.pid(), a.uid()).Scan(&count); err != nil {
			failPrivateDraft(w, err)
			return
		}
		if count >= privateDraftLimit {
			fail(w, 409, "draft_limit_reached", "当前项目私人草稿已达 100 条，请先整理草稿箱")
			return
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	next := privateDraft{ID: id, Kind: input.Kind, TargetID: input.TargetID, Title: privateDraftTitle(input.Payload), Version: version + 1, CreatedAt: previous.CreatedAt, UpdatedAt: now, Payload: input.Payload, Context: input.Context}
	if missingEntry {
		next.CreatedAt = now
		_, err = tx.ExecContext(r.Context(), `INSERT INTO private_workitem_drafts(tenant_id,project_id,user_id,id,kind,target_id,title,payload_json,context_json,version,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), id, next.Kind, next.TargetID, next.Title, string(next.Payload), string(next.Context), next.Version, now, now)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(r.Context(), `UPDATE private_workitem_drafts SET title=?,payload_json=?,context_json=?,version=?,updated_at=? WHERE tenant_id=? AND project_id=? AND user_id=? AND id=? AND version=?`, next.Title, string(next.Payload), string(next.Context), next.Version, now, tenantID, a.pid(), a.uid(), id, version)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			if err == nil && changed != 1 {
				fail(w, 409, "draft_conflict", "草稿已在其他页面更新，请保留当前内容并另存草稿")
				return
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failPrivateDraft(w, err)
		return
	}
	write(w, 200, map[string]any{"draft": next})
}
