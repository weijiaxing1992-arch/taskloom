package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// PDF 在浏览器本地解析；原始字段与映射快照只是导入数据，不能授予权限或改写审计身份。
type tapdSourceField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type tapdImport struct {
	WorkspaceID string            `json:"workspaceId"`
	SourceID    string            `json:"sourceId"`
	FileName    string            `json:"fileName"`
	PDFBase64   string            `json:"pdfBase64,omitempty"`
	PageCount   int               `json:"pageCount"`
	Fields      []tapdSourceField `json:"fields"`
	RawText     string            `json:"rawText"`
	Warnings    []string          `json:"warnings"`
	Reviewed    bool              `json:"reviewed"`
	content     []byte
	digest      string
}

func (a *App) migrateTapdImports() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_tapd_imports(
tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL REFERENCES requirements(id) ON DELETE CASCADE,
source_key TEXT NOT NULL,sha256 TEXT NOT NULL,created_by TEXT NOT NULL,created_at TEXT NOT NULL,
PRIMARY KEY(tenant_id,project_id,source_key),UNIQUE(tenant_id,project_id,sha256));`)
	return err
}

var tapdNumber = regexp.MustCompile(`^[0-9]{1,30}$`)

func (in *tapdImport) validate() error {
	if !in.Reviewed || !tapdNumber.MatchString(in.WorkspaceID) || !tapdNumber.MatchString(in.SourceID) {
		return fmt.Errorf("请确认 TAPD 来源编号并完成字段预览")
	}
	if !safeAttachmentFilename(in.FileName) || !strings.HasSuffix(strings.ToLower(in.FileName), ".pdf") || in.PageCount < 1 || in.PageCount > 30 {
		return fmt.Errorf("请上传 1–30 页的 PDF，文件名不能包含路径")
	}
	if len(in.Fields) == 0 || len(in.Fields) > 128 || len(in.RawText) > 1<<20 || len(in.Warnings) > 128 {
		return fmt.Errorf("导入字段或原文超过限制，或未识别到字段")
	}
	for _, f := range in.Fields {
		if strings.TrimSpace(f.Label) == "" || len(f.Label) > 300 || len(f.Value) > 12000 {
			return fmt.Errorf("TAPD 字段名称或内容无效")
		}
	}
	for _, warning := range in.Warnings {
		if len(warning) > 4000 {
			return fmt.Errorf("导入提示过长")
		}
	}
	if len(in.PDFBase64) > base64.StdEncoding.EncodedLen(int(requirementAttachmentMaxSize)) {
		return fmt.Errorf("PDF 不能超过 10 MiB")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(in.PDFBase64)
	if err != nil || len(data) < 8 || len(data) > int(requirementAttachmentMaxSize) || !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data[max(0, len(data)-2048):], []byte("%%EOF")) {
		return fmt.Errorf("PDF 文件数据无效或超过 10 MiB")
	}
	in.content = data
	digest := sha256.Sum256(data)
	in.digest = hex.EncodeToString(digest[:])
	return nil
}

// 同一项目中的相同来源仅创建一次；新版 PDF 不会偷偷覆盖已编辑的需求。
func (a *App) existingTapdImport(tx *sql.Tx, in *tapdImport) (id int64, same bool, err error) {
	var digest string
	err = tx.QueryRow(`SELECT requirement_id,sha256 FROM requirement_tapd_imports WHERE tenant_id=? AND project_id=? AND (source_key=? OR sha256=?) LIMIT 1`, tenantID, a.pid(), in.WorkspaceID+":"+in.SourceID, in.digest).Scan(&id, &digest)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return id, digest == in.digest, err
}

// 需求、原始文件、机器可读对照表和幂等键必须共用事务；任何一步失败均不留半条需求。
func (a *App) persistTapdImport(tx *sql.Tx, x *Requirement, in *tapdImport) error {
	source := *in
	source.PDFBase64 = ""
	source.content = nil
	mapped := *x
	mapped.TapdImport = nil
	snapshot, err := json.MarshalIndent(map[string]any{"schemaVersion": 1, "source": source, "sha256": in.digest, "mappedRequirement": mapped, "importedBy": a.uid(), "importedAt": x.CreatedAt}, "", "  ")
	if err != nil {
		return err
	}
	for _, file := range []struct {
		name, mime string
		data       []byte
	}{{in.FileName, "application/pdf", in.content}, {"TAPD-" + in.SourceID + "-字段对照.json", "application/json", snapshot}} {
		sum := sha256.Sum256(file.data)
		_, err = tx.Exec(`INSERT INTO requirement_attachments(tenant_id,project_id,requirement_id,name,size_bytes,content_type,sha256,content,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), x.ID, file.name, len(file.data), file.mime, hex.EncodeToString(sum[:]), file.data, a.uid(), x.CreatedAt)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO requirement_tapd_imports(tenant_id,project_id,requirement_id,source_key,sha256,created_by,created_at) VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), x.ID, in.WorkspaceID+":"+in.SourceID, in.digest, a.uid(), x.CreatedAt)
	return err
}
