package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const requirementAttachmentMaxSize int64 = 10 << 20
const requirementAttachmentRequestMaxSize int64 = requirementAttachmentMaxSize + (64 << 10)

type requirementAttachment struct {
	ID            int64  `json:"id"`
	RequirementID int64  `json:"requirementId"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	ContentType   string `json:"contentType"`
	Category      string `json:"category"`
	Language      string `json:"language,omitempty"`
	SHA256        string `json:"sha256"`
	CreatedBy     string `json:"createdBy"`
	CreatedByName string `json:"createdByName"`
	CreatedAt     string `json:"createdAt"`
	DownloadURL   string `json:"downloadUrl"`
}

type requirementDesignLink struct {
	ID            int64  `json:"id"`
	RequirementID int64  `json:"requirementId"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	CreatedBy     string `json:"createdBy"`
	CreatedByName string `json:"createdByName"`
	CreatedAt     string `json:"createdAt"`
}

func (a *App) migrateRequirementResources() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_attachments(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,name TEXT NOT NULL,size_bytes INTEGER NOT NULL CHECK(size_bytes>=0 AND size_bytes<=10485760),content_type TEXT NOT NULL,sha256 TEXT NOT NULL,content BLOB NOT NULL CHECK(length(content)=size_bytes),created_by TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_requirement_attachments_scope ON requirement_attachments(tenant_id,project_id,requirement_id,id);
CREATE TABLE IF NOT EXISTS requirement_design_links(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,title TEXT NOT NULL,url TEXT NOT NULL,created_by TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,requirement_id,url));
CREATE INDEX IF NOT EXISTS idx_requirement_design_links_scope ON requirement_design_links(tenant_id,project_id,requirement_id,id);`)
	if err != nil {
		return err
	}
	var exists int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('requirement_attachments') WHERE name='category'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err = a.db.Exec(`ALTER TABLE requirement_attachments ADD COLUMN category TEXT NOT NULL DEFAULT 'auto' CHECK(category IN ('auto','bug','api','design','code','other'))`)
	}
	return err
}

func isRequirementAttachmentUpload(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "requirements" || parts[3] != "attachments" {
		return false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	return err == nil && id > 0
}

func safeAttachmentFilename(name string) bool {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\:") {
		return false
	}
	for _, char := range name {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			return false
		}
	}
	return true
}

func parseRequirementResourceID(tail []string) (int64, error) {
	if len(tail) != 1 {
		return 0, fmt.Errorf("invalid resource path")
	}
	id, err := strconv.ParseInt(tail[0], 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid resource id")
	}
	return id, nil
}

func (a *App) verifyRequirementResourceParent(tx *sql.Tx, id int64) error {
	var found int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&found); err != nil {
		return err
	}
	if found != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (a *App) auditRequirementResource(tx *sql.Tx, requirementID, resourceID int64, typ, action string, before, after any, now string) error {
	_, err := tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), typ, strconv.FormatInt(resourceID, 10), action, jsonText(before), jsonText(after), now)
	if err != nil {
		return err
	}
	actor := a.uid()
	if err = tx.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		return err
	}
	activity, err := tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, actor, action, jsonText(map[string]any{"resourceId": resourceID, "type": typ}), now)
	if err != nil {
		return err
	}
	activityID, err := activity.LastInsertId()
	if err != nil {
		return err
	}
	key := "attachment"
	if typ == "requirement_design_link" {
		key = "designLink"
	}
	return a.recordRequirementRelatedHistory(tx, activityID, requirementID, key, before, after)
}

func failRequirementResource(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
}

func (a *App) requirementAttachments(w http.ResponseWriter, r *http.Request, requirementID int64, tail []string) {
	if len(tail) == 0 && r.Method == http.MethodPost {
		a.uploadRequirementAttachment(w, r, requirementID)
		return
	}
	if len(tail) == 0 && r.Method == http.MethodGet {
		rows, err := a.db.QueryContext(r.Context(), `SELECT f.id,f.requirement_id,f.name,f.size_bytes,f.content_type,f.category,f.sha256,f.created_by,COALESCE(u.name,f.created_by),f.created_at,substr(f.content,1,8192) FROM requirement_attachments f LEFT JOIN users u ON u.tenant_id=f.tenant_id AND u.id=f.created_by WHERE f.tenant_id=? AND f.project_id=? AND f.requirement_id=? ORDER BY f.id DESC`, tenantID, a.pid(), requirementID)
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		defer rows.Close()
		items := []requirementAttachment{}
		for rows.Next() {
			var item requirementAttachment
			var sample []byte
			if err := rows.Scan(&item.ID, &item.RequirementID, &item.Name, &item.Size, &item.ContentType, &item.Category, &item.SHA256, &item.CreatedBy, &item.CreatedByName, &item.CreatedAt, &sample); err != nil {
				failRequirementResource(w, err)
				return
			}
			item.Category, item.Language = resolveAttachmentAsset(item.Name, item.Category, sample)
			item.DownloadURL = fmt.Sprintf("/api/requirements/%d/attachments/%d", requirementID, item.ID)
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			failRequirementResource(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items, "maxFileSize": requirementAttachmentMaxSize})
		return
	}
	if len(tail) == 0 {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	id, err := parseRequirementResourceID(tail)
	if err != nil {
		fail(w, 400, "invalid_id", "附件编号无效")
		return
	}
	if r.Method == http.MethodPatch {
		a.updateAttachmentCategory(w, r, requirementID, id)
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("metadata") == "1" {
		var item requirementAttachment
		var sample []byte
		err := a.db.QueryRowContext(r.Context(), `SELECT id,name,size_bytes,content_type,category,substr(content,1,8192) FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&item.ID, &item.Name, &item.Size, &item.ContentType, &item.Category, &sample)
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		item.RequirementID = requirementID
		item.Category, item.Language = resolveAttachmentAsset(item.Name, item.Category, sample)
		write(w, 200, item)
		return
	}
	if r.Method == http.MethodGet {
		var name string
		var content []byte
		if err := a.db.QueryRowContext(r.Context(), `SELECT name,content FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&name, &content); err != nil {
			failRequirementResource(w, err)
			return
		}
		if !safeAttachmentFilename(name) {
			fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
		return
	}
	if r.Method != http.MethodDelete {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	defer tx.Rollback()
	var name string
	var size int64
	if err := tx.QueryRow(`SELECT name,size_bytes FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&name, &size); err != nil {
		failRequirementResource(w, err)
		return
	}
	inUse, err := a.richAttachmentInUse(tx, requirementID, id)
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	if inUse {
		fail(w, 409, "attachment_in_use", "附件正在被正文或评论使用，请先移除引用")
		return
	}
	_, err = tx.Exec(`DELETE FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id)
	if err == nil {
		err = a.auditRequirementResource(tx, requirementID, id, "requirement_attachment", "attachment.deleted", map[string]any{"name": name, "size": size, "requirementId": requirementID}, nil, time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	write(w, 200, map[string]any{"deleted": true, "id": id})
}

func (a *App) uploadRequirementAttachment(w http.ResponseWriter, r *http.Request, requirementID int64) {
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "auto"
	}
	if !validAssetCategory(category) {
		fail(w, 422, "invalid_category", "请选择有效的附件分类")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, requirementAttachmentRequestMaxSize)
	reader, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, "invalid_multipart", "请使用 multipart/form-data 上传附件")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		fail(w, 400, "invalid_multipart", "请上传单个 file 文件字段")
		return
	}
	defer part.Close()
	_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	name := disposition["filename"] // Do not use Part.FileName: it strips unsafe path prefixes.
	if err != nil || part.FormName() != "file" || !safeAttachmentFilename(name) {
		fail(w, 422, "invalid_attachment_name", "附件文件名无效或包含路径")
		return
	}
	content, err := io.ReadAll(io.LimitReader(part, requirementAttachmentMaxSize+1))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) || int64(len(content)) > requirementAttachmentMaxSize {
		fail(w, 413, "attachment_too_large", "单个附件不能超过 10 MB")
		return
	}
	if err != nil {
		fail(w, 400, "invalid_multipart", "附件内容读取失败")
		return
	}
	if extra, err := reader.NextPart(); err != io.EOF {
		if extra != nil {
			extra.Close()
		}
		if errors.As(err, &tooLarge) {
			fail(w, 413, "attachment_too_large", "单个附件不能超过 10 MB")
		} else {
			fail(w, 400, "invalid_multipart", "请上传单个 file 文件字段")
		}
		return
	}
	if content == nil {
		content = []byte{}
	}
	digest := sha256.Sum256(content)
	item := requirementAttachment{RequirementID: requirementID, Name: name, Size: int64(len(content)), ContentType: http.DetectContentType(content), SHA256: hex.EncodeToString(digest[:]), CreatedBy: a.uid(), CreatedByName: a.actorName(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	item.Category, item.Language = resolveAttachmentAsset(name, category, content)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	defer tx.Rollback()
	if err := a.verifyRequirementResourceParent(tx, requirementID); err != nil {
		failRequirementResource(w, err)
		return
	}
	res, err := tx.Exec(`INSERT INTO requirement_attachments(tenant_id,project_id,requirement_id,name,size_bytes,content_type,sha256,content,created_by,created_at,category)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, item.Name, item.Size, item.ContentType, item.SHA256, content, a.uid(), item.CreatedAt, category)
	if err == nil {
		item.ID, err = res.LastInsertId()
	}
	if err == nil {
		item.DownloadURL = fmt.Sprintf("/api/requirements/%d/attachments/%d", requirementID, item.ID)
		err = a.auditRequirementResource(tx, requirementID, item.ID, "requirement_attachment", "attachment.uploaded", nil, item, item.CreatedAt)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	write(w, 201, item)
}

func normalizedFigmaURL(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", fmt.Errorf("invalid URL")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Scheme, "https") || u.User != nil || !validChoice(strings.ToLower(u.Host), []string{"figma.com", "www.figma.com"}) {
		return "", fmt.Errorf("invalid Figma origin")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || !validChoice(parts[0], []string{"design", "file", "proto"}) || parts[1] == "" {
		return "", fmt.Errorf("invalid Figma path")
	}
	for _, part := range parts {
		if part == "." || part == ".." || strings.ContainsAny(part, "\\\r\n") {
			return "", fmt.Errorf("invalid Figma path")
		}
		for _, char := range part {
			if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
				return "", fmt.Errorf("invalid Figma path")
			}
		}
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

func (a *App) requirementDesignLinks(w http.ResponseWriter, r *http.Request, requirementID int64, tail []string) {
	if len(tail) == 0 && r.Method == http.MethodGet {
		rows, err := a.db.QueryContext(r.Context(), `SELECT d.id,d.requirement_id,d.title,d.url,d.created_by,COALESCE(u.name,d.created_by),d.created_at FROM requirement_design_links d LEFT JOIN users u ON u.tenant_id=d.tenant_id AND u.id=d.created_by WHERE d.tenant_id=? AND d.project_id=? AND d.requirement_id=? ORDER BY d.id DESC`, tenantID, a.pid(), requirementID)
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		defer rows.Close()
		items := []requirementDesignLink{}
		for rows.Next() {
			var item requirementDesignLink
			if err := rows.Scan(&item.ID, &item.RequirementID, &item.Title, &item.URL, &item.CreatedBy, &item.CreatedByName, &item.CreatedAt); err != nil {
				failRequirementResource(w, err)
				return
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			failRequirementResource(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if len(tail) == 0 && r.Method == http.MethodPost {
		var body struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		}
		if decodeJSON(r, &body) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		body.Title = strings.TrimSpace(body.Title)
		if body.Title == "" {
			body.Title = "Figma"
		}
		if utf8.RuneCountInString(body.Title) > 200 {
			fail(w, 422, "validation_error", "设计链接标题不能超过 200 字")
			return
		}
		normalized, err := normalizedFigmaURL(body.URL)
		if err != nil {
			fail(w, 422, "invalid_design_link", "仅支持 Figma 的 HTTPS design、file 或 proto 链接")
			return
		}
		item := requirementDesignLink{RequirementID: requirementID, Title: body.Title, URL: normalized, CreatedBy: a.uid(), CreatedByName: a.actorName(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		defer tx.Rollback()
		if err := a.verifyRequirementResourceParent(tx, requirementID); err != nil {
			failRequirementResource(w, err)
			return
		}
		res, err := tx.Exec(`INSERT INTO requirement_design_links(tenant_id,project_id,requirement_id,title,url,created_by,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, item.Title, item.URL, a.uid(), item.CreatedAt)
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint") {
			fail(w, 409, "design_link_exists", "该设计链接已经关联")
			return
		}
		if err == nil {
			item.ID, err = res.LastInsertId()
		}
		if err == nil {
			err = a.auditRequirementResource(tx, requirementID, item.ID, "requirement_design_link", "design_link.added", nil, item, item.CreatedAt)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		write(w, 201, item)
		return
	}
	if len(tail) > 0 && r.Method == http.MethodDelete {
		id, err := parseRequirementResourceID(tail)
		if err != nil {
			fail(w, 400, "invalid_id", "设计链接编号无效")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		defer tx.Rollback()
		var title, link string
		if err := tx.QueryRow(`SELECT title,url FROM requirement_design_links WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&title, &link); err != nil {
			failRequirementResource(w, err)
			return
		}
		_, err = tx.Exec(`DELETE FROM requirement_design_links WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id)
		if err == nil {
			err = a.auditRequirementResource(tx, requirementID, id, "requirement_design_link", "design_link.deleted", map[string]any{"title": title, "url": link, "requirementId": requirementID}, nil, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failRequirementResource(w, err)
			return
		}
		write(w, 200, map[string]any{"deleted": true, "id": id})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
