package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 图文包不使用远端资源：只导出经当前项目鉴权、指纹核验的已有位图。
// 附件原始名称不参与路径拼接，避免 ZIP 路径穿越和特殊字符导致图片丢失。
func buildReleaseNotesBundle(ctx context.Context, store stateStore, source releaseNoteSource, entries []releaseNoteEntry, revision int) ([]byte, error) {
	normalizeReleaseNoteSourceCodes(&source)
	if err := validateReleaseNotesEntries(source, entries); err != nil {
		return nil, err
	}
	entries = sortReleaseNotesEntries(entries)
	markdown := renderReleaseNotesMarkdown(source, entries)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	stamp, _ := time.Parse(time.RFC3339, source.ReleaseDate)
	if stamp.Year() < 1980 {
		stamp = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	add := func(name string, body []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetModTime(stamp)
		file, err := archive.CreateHeader(header)
		if err == nil {
			_, err = file.Write(body)
		}
		return err
	}
	images := map[int64]struct {
		requirementID int64
		image         releaseNoteImage
	}{}
	sources := []map[string]any{}
	for _, requirement := range source.Requirements {
		sources = append(sources, map[string]any{"id": requirement.ID, "code": requirement.Code, "title": requirement.Title})
		for _, image := range requirement.Images {
			images[image.ID] = struct {
				requirementID int64
				image         releaseNoteImage
			}{requirement.ID, image}
		}
	}
	exportedImages := []map[string]any{}
	seen := map[int64]bool{}
	total := 0
	for _, entry := range entries {
		for _, id := range entry.ImageIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			item := images[id]
			var content []byte
			if err := store.QueryRowContext(ctx, `SELECT content FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=? AND category!='bug'`, tenantID, source.ProjectID, item.requirementID, id).Scan(&content); err != nil {
				return nil, aiFailure(409, "release_notes_source_changed", "截图已变化或移除，请刷新升级日志后重试")
			}
			total += len(content)
			if total > 64<<20 {
				return nil, releaseNotesBudgetError()
			}
			digest := sha256.Sum256(content)
			if hex.EncodeToString(digest[:]) != item.image.SHA256 || validateRichImage(content) != nil {
				return nil, aiFailure(409, "release_notes_source_changed", "截图已变化，请刷新升级日志后重试")
			}
			ext := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif"}[http.DetectContentType(content)]
			if ext == "" {
				return nil, releaseNotesInvalid()
			}
			path := fmt.Sprintf("images/requirement-%d-image-%d.%s", item.requirementID, id, ext)
			if err := add(path, content); err != nil {
				return nil, err
			}
			original := fmt.Sprintf("](/api/requirements/%d/attachments/%d)", item.requirementID, id)
			markdown = strings.ReplaceAll(markdown, original, "]("+path+")")
			exportedImages = append(exportedImages, map[string]any{"id": id, "requirementId": item.requirementID, "name": item.image.Name, "path": path, "sha256": item.image.SHA256})
		}
	}
	// 可交付 JSON 只含日志与来源索引，不夹带内部需求正文、验收标准或人员资料。
	data, err := json.MarshalIndent(map[string]any{"versionName": source.VersionName, "releaseDate": source.ReleaseDate, "promptVersion": releaseNotesPromptVersion, "revision": revision, "status": "draft", "categories": releaseNoteCategories, "entries": entries, "requirements": sources, "images": exportedImages}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = add("upgrade-notes.md", []byte(markdown)); err != nil {
		return nil, err
	}
	if err = add("upgrade-notes.json", data); err != nil {
		return nil, err
	}
	if err = add("README.md", []byte("# 升级日志图文包\n\n解压后使用 Markdown 阅读器打开 upgrade-notes.md，配图位于 images 目录，无需登录即可离线查看。upgrade-notes.json 为同一份结构化日志。\n\n当前为审核草稿；待补图条目尚不满足完整配图规范。对外发布前请核对功能已上线、分类、文案与图片内容，并检查截图是否包含账号、客户数据或其他敏感信息。此操作只下载，不发布到门户或飞书。\n")); err != nil {
		return nil, err
	}
	if err = archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
