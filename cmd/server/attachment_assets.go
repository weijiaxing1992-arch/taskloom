package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// 分类描述业务用途，语言描述文件格式；两者独立，例如接口文件仍可使用 Go 图标。
func validAssetCategory(value string) bool {
	return validChoice(value, []string{"auto", "bug", "api", "design", "code", "other"})
}
func attachmentLanguage(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	aliases := map[string]string{"vue": "vue", "php": "php", "go": "go", "cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp", "hxx": "cpp", "c": "c", "h": "c", "js": "javascript", "mjs": "javascript", "cjs": "javascript", "jsx": "javascript", "ts": "typescript", "tsx": "typescript", "py": "python", "java": "java", "dart": "dart", "json": "json", "sql": "sql", "sh": "bash", "bash": "bash", "html": "html", "htm": "html", "xml": "xml", "svg": "xml", "css": "css", "scss": "scss", "less": "less", "yaml": "yaml", "yml": "yaml", "md": "markdown", "markdown": "markdown", "rs": "rust", "swift": "swift", "kt": "kotlin", "cs": "csharp", "rb": "ruby", "proto": "protobuf", "graphql": "graphql", "txt": "plaintext", "log": "plaintext", "csv": "plaintext", "diff": "diff", "patch": "diff"}
	if strings.EqualFold(filepath.Base(name), "Dockerfile") {
		return "dockerfile"
	}
	return aliases[ext]
}

// 文件名不明确时仅检查有限前缀，不加载全量附件、不执行代码。
func resolveAttachmentAsset(name, selected string, content []byte) (string, string) {
	language := attachmentLanguage(name)
	if len(content) > 8192 {
		content = content[:8192]
	}
	if (language == "" || language == "plaintext") && !bytes.ContainsRune(content, 0) {
		if guessed := exportCodeLanguage("", string(content)); guessed != "plaintext" {
			language = guessed
		}
	}
	category := attachmentCategory(name, selected)
	if (selected == "" || selected == "auto") && category == "other" && language != "" && language != "plaintext" && language != "markdown" {
		category = "code"
	}
	return category, language
}
func attachmentCategory(name, selected string) string {
	if validAssetCategory(selected) && selected != "auto" {
		return selected
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "bug") || strings.Contains(lower, "缺陷") || strings.Contains(lower, "复现") {
		return "bug"
	}
	if strings.Contains(lower, "openapi") || strings.Contains(lower, "swagger") || strings.Contains(lower, "postman") || strings.Contains(lower, "接口") || strings.HasSuffix(lower, ".proto") {
		return "api"
	}
	if strings.Contains(lower, "设计稿") || strings.HasSuffix(lower, ".fig") || strings.HasSuffix(lower, ".sketch") || strings.HasSuffix(lower, ".psd") || strings.HasSuffix(lower, ".xd") {
		return "design"
	}
	if lang := attachmentLanguage(name); lang != "" && lang != "plaintext" && lang != "markdown" {
		return "code"
	}
	return "other"
}
func (a *App) updateAttachmentCategory(w http.ResponseWriter, r *http.Request, requirementID, id int64) {
	var input struct {
		Category string `json:"category"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		fail(w, 400, "invalid_json", "附件分类格式无效")
		return
	}
	if !validAssetCategory(input.Category) {
		fail(w, 422, "invalid_category", "请选择有效的附件分类")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	defer tx.Rollback()
	var previous, name string
	var sample []byte
	if err = tx.QueryRow(`SELECT category,name,substr(content,1,8192) FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, a.pid(), requirementID, id).Scan(&previous, &name, &sample); err != nil {
		failRequirementResource(w, err)
		return
	}
	_, err = tx.Exec(`UPDATE requirement_attachments SET category=? WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, input.Category, tenantID, a.pid(), requirementID, id)
	if err == nil {
		err = a.auditRequirementResource(tx, requirementID, id, "requirement_attachment", "attachment.classified", map[string]any{"category": previous}, map[string]any{"category": input.Category}, time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	category, language := resolveAttachmentAsset(name, input.Category, sample)
	write(w, 200, map[string]any{"id": id, "category": category, "language": language})
}
