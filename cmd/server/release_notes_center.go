package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// 升级日志中心读取的是生成完成时保存的草稿快照，而不是重新从当前需求
// 拼装的视图。这样迭代完成后的版本、分类、正文、验收标准和选择过的截图
// 可以追溯；真正对外发布前的图文包仍复用原有的指纹校验下载入口。
const releaseNotesCenterDefaultPageSize = 20
const releaseNotesCenterMaxPageSize = 100

type releaseNotesCenterListItem struct {
	SprintID           int64  `json:"sprintId"`
	SprintCode         string `json:"sprintCode"`
	VersionName        string `json:"versionName"`
	ReleaseDate        string `json:"releaseDate"`
	Revision           int64  `json:"revision"`
	State              string `json:"state"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	RequirementCount   int    `json:"requirementCount"`
	EntryCount         int    `json:"entryCount"`
	SelectedImageCount int    `json:"selectedImageCount"`
}

type releaseNotesCenterList struct {
	ProjectID string                       `json:"projectId"`
	Items     []releaseNotesCenterListItem `json:"items"`
	Total     int                          `json:"total"`
	Page      int                          `json:"page"`
	PageSize  int                          `json:"pageSize"`
}

type releaseNotesCenterDetail struct {
	ProjectID    string                   `json:"projectId"`
	SprintID     int64                    `json:"sprintId"`
	SprintCode   string                   `json:"sprintCode"`
	VersionName  string                   `json:"versionName"`
	ReleaseDate  string                   `json:"releaseDate"`
	Revision     int64                    `json:"revision"`
	State        string                   `json:"state"`
	CreatedAt    string                   `json:"createdAt"`
	UpdatedAt    string                   `json:"updatedAt"`
	Snapshot     bool                     `json:"snapshot"`
	Categories   []string                 `json:"categories"`
	Entries      []releaseNoteEntry       `json:"entries"`
	Requirements []releaseNoteRequirement `json:"requirements"`
	Markdown     string                   `json:"markdown"`
}

// 团队洞察不是“当前项目任意成员可见”的项目列表。它同时要求当前项目
// 成员身份和 reports.view 企业权限，外部 Bearer 凭据也会在每次读取时
// 复用同一检查，避免 token 绑定项目成为报告内容的越权通道。
func (a *App) releaseNotesCenterAccess(ctx context.Context, q stateStore) error {
	if err := a.releaseNotesAccess(ctx, q, false); err != nil {
		return err
	}
	_, err := a.requireOrganizationPermission(ctx, q, "reports.view")
	return err
}

// 开放接口中的完整升级日志是企业级导出能力。除项目与 reports.view 边界
// 外，再要求凭据所有者仍是有效企业管理员，旧凭据不能在降权后继续读取。
func (a *App) integrationReleaseNotesAdmin(ctx context.Context, q stateStore) error {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active' AND tm.role='tenant_admin'`, tenantID, a.uid()).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return &organizationError{403, "admin_required", "仅企业管理员可通过开放接口读取完整升级日志"}
	}
	return nil
}

// 升级日志检索限定在已经冻结的项目快照中。它不会回查当前需求，也不使用
// AI 设置；因此搜索结果既可追溯，也不会因为后续编辑而改变历史版本内容。
// q 按字面量匹配，% 与 _ 不作为 SQL 通配符，避免输入一个符号就意外枚举全部
// 版本。分页和筛选均由服务端完成，不能把跨项目筛选留给浏览器处理。
func releaseNotesCenterListQuery(values url.Values) (int, int, string, error) {
	page, size, query := 1, releaseNotesCenterDefaultPageSize, ""
	for key, values := range values {
		if !validChoice(key, []string{"page", "pageSize", "limit", "q"}) || len(values) != 1 {
			return 0, 0, "", errors.New("只支持单个 page、pageSize、limit 或 q 查询参数")
		}
		if key == "q" {
			if len(values[0]) > 480 {
				return 0, 0, "", errors.New("搜索内容不能超过 100 个字符")
			}
			continue
		}
		if len(values[0]) > 20 {
			return 0, 0, "", errors.New("分页参数格式不正确")
		}
	}
	if raw := values.Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000000 {
			return 0, 0, "", errors.New("页码须为 1–1000000")
		}
		page = value
	}
	if values.Get("pageSize") != "" && values.Get("limit") != "" {
		return 0, 0, "", errors.New("pageSize 与 limit 不能同时使用")
	}
	raw := values.Get("pageSize")
	if raw == "" {
		raw = values.Get("limit")
	}
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > releaseNotesCenterMaxPageSize {
			return 0, 0, "", errors.New("每页数量须为 1–100")
		}
		size = value
	}
	query = strings.TrimSpace(values.Get("q"))
	if utf8.RuneCountInString(query) > 100 {
		return 0, 0, "", errors.New("搜索内容不能超过 100 个字符")
	}
	return page, size, query, nil
}

func releaseNotesCenterLike(query string) string {
	literal := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.ToLower(query))
	return "%" + literal + "%"
}

// 存储快照属于服务端受控内容；读出时仍完整校验 ID、项目和附件路径，防止
// 异常数据库记录变成跨项目图片地址或不符合七类规范的日志。
func (a *App) releaseNotesCenterSnapshot(sprint int64, source releaseNoteSource, entries []releaseNoteEntry) error {
	if source.ProjectID != a.pid() || source.SprintID != sprint || strings.TrimSpace(source.VersionName) == "" {
		return releaseNotesInvalid()
	}
	if _, err := time.Parse(time.RFC3339, source.ReleaseDate); err != nil {
		return releaseNotesInvalid()
	}
	for _, requirement := range source.Requirements {
		if requirement.ID <= 0 || releaseNotesBugClassification(requirement.Type) || releaseNotesBugClassification(requirement.Category) {
			return releaseNotesInvalid()
		}
		for _, image := range requirement.Images {
			if image.ID <= 0 || image.URL != fmt.Sprintf("/api/requirements/%d/attachments/%d", requirement.ID, image.ID) || !validChoice(image.ContentType, []string{"image/png", "image/jpeg", "image/gif"}) {
				return releaseNotesInvalid()
			}
		}
	}
	return validateReleaseNotesEntries(source, entries)
}

func releaseNotesCenterDecode(a *App, sprint int64, sourceJSON, entriesJSON string) (releaseNoteSource, []releaseNoteEntry, error) {
	var source releaseNoteSource
	var entries []releaseNoteEntry
	if json.Unmarshal([]byte(sourceJSON), &source) != nil || json.Unmarshal([]byte(entriesJSON), &entries) != nil {
		return source, nil, errors.New("invalid stored release notes")
	}
	normalizeReleaseNoteSourceCodes(&source)
	if err := a.releaseNotesCenterSnapshot(sprint, source, entries); err != nil {
		return source, nil, err
	}
	return source, sortReleaseNotesEntries(entries), nil
}

func releaseNotesCenterImageCount(entries []releaseNoteEntry) int {
	count := 0
	for _, entry := range entries {
		count += len(entry.ImageIDs)
	}
	return count
}

func (a *App) releaseNotesCenterListData(ctx context.Context, q stateStore, page, size int, search string) (releaseNotesCenterList, error) {
	result := releaseNotesCenterList{ProjectID: a.pid(), Items: []releaseNotesCenterListItem{}, Page: page, PageSize: size}
	if err := a.releaseNotesCenterAccess(ctx, q); err != nil {
		return result, err
	}
	const from = ` FROM release_note_drafts d
JOIN release_note_jobs j ON j.tenant_id=d.tenant_id AND j.project_id=d.project_id AND j.sprint_id=d.sprint_id
JOIN sprints s ON s.tenant_id=d.tenant_id AND s.project_id=d.project_id AND s.id=d.sprint_id
WHERE d.tenant_id=? AND d.project_id=?`
	where := from
	args := []any{tenantID, a.pid()}
	if search != "" {
		// 版本号、迭代名称，以及冻结的原始需求/日志条目均能被检索；所有列
		// 始终受前面的 tenant/project 条件保护。
		where += ` AND (LOWER(s.code) LIKE ? ESCAPE '\' OR LOWER(s.name) LIKE ? ESCAPE '\' OR LOWER(d.source_json) LIKE ? ESCAPE '\' OR LOWER(d.entries_json) LIKE ? ESCAPE '\')`
		like := releaseNotesCenterLike(search)
		args = append(args, like, like, like, like)
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&result.Total); err != nil {
		return result, err
	}
	listArgs := append(append([]any(nil), args...), size, (page-1)*size)
	rows, err := q.QueryContext(ctx, `SELECT d.sprint_id,s.code,s.name,j.revision,j.state,d.created_at,d.updated_at,d.source_json,d.entries_json`+where+` ORDER BY d.updated_at DESC,d.sprint_id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item releaseNotesCenterListItem
		var sourceJSON, entriesJSON string
		if err := rows.Scan(&item.SprintID, &item.SprintCode, &item.VersionName, &item.Revision, &item.State, &item.CreatedAt, &item.UpdatedAt, &sourceJSON, &entriesJSON); err != nil {
			return result, err
		}
		source, entries, err := releaseNotesCenterDecode(a, item.SprintID, sourceJSON, entriesJSON)
		if err != nil {
			return result, err
		}
		// 以持久化来源中的版本为准，避免后来改迭代名称而重写已交付版本。
		item.VersionName = source.VersionName
		item.ReleaseDate = source.ReleaseDate
		item.RequirementCount = len(source.Requirements)
		item.EntryCount = len(entries)
		item.SelectedImageCount = releaseNotesCenterImageCount(entries)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (a *App) releaseNotesCenterDetailData(ctx context.Context, q stateStore, sprint int64) (releaseNotesCenterDetail, error) {
	result := releaseNotesCenterDetail{ProjectID: a.pid(), SprintID: sprint, Snapshot: true, Categories: append([]string(nil), releaseNoteCategories...), Entries: []releaseNoteEntry{}, Requirements: []releaseNoteRequirement{}}
	if err := a.releaseNotesCenterAccess(ctx, q); err != nil {
		return result, err
	}
	var sourceJSON, entriesJSON string
	err := q.QueryRowContext(ctx, `SELECT s.code,j.revision,j.state,d.created_at,d.updated_at,d.source_json,d.entries_json
FROM release_note_drafts d
JOIN release_note_jobs j ON j.tenant_id=d.tenant_id AND j.project_id=d.project_id AND j.sprint_id=d.sprint_id
JOIN sprints s ON s.tenant_id=d.tenant_id AND s.project_id=d.project_id AND s.id=d.sprint_id
WHERE d.tenant_id=? AND d.project_id=? AND d.sprint_id=?`, tenantID, a.pid(), sprint).Scan(&result.SprintCode, &result.Revision, &result.State, &result.CreatedAt, &result.UpdatedAt, &sourceJSON, &entriesJSON)
	if err != nil {
		return result, err
	}
	source, entries, err := releaseNotesCenterDecode(a, sprint, sourceJSON, entriesJSON)
	if err != nil {
		return result, err
	}
	result.VersionName = source.VersionName
	result.ReleaseDate = source.ReleaseDate
	result.Entries = entries
	result.Requirements = source.Requirements
	result.Markdown = renderReleaseNotesMarkdown(source, entries)
	if result.Markdown == "" {
		return result, releaseNotesInvalid()
	}
	return result, nil
}

func releaseNotesCenterSprintID(path string) (int64, bool) {
	value := strings.TrimPrefix(path, "/api/reports/release-notes/")
	if value == "" || strings.Contains(value, "/") {
		return 0, false
	}
	return integrationPositive(value)
}

func failReleaseNotesCenterRead(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "升级日志不存在或不属于当前项目")
		return
	}
	var state stateError
	var organization *organizationError
	if errors.As(err, &state) || errors.As(err, &organization) {
		failAI(w, err)
		return
	}
	fail(w, 503, "release_notes_unavailable", "升级日志暂时无法读取，请稍后重试")
}

// GET /api/reports/release-notes[/sprintId]：项目范围的团队洞察页面数据。
// 它故意不读取 ai_settings，因此 AI 密钥、密钥状态和供应商配置不会出现在
// 任何列表或详情响应中。
func (a *App) releaseNotesCenter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	defer tx.Rollback()
	if r.URL.Path == "/api/reports/release-notes" || r.URL.Path == "/api/reports/release-notes/" {
		page, size, search, err := releaseNotesCenterListQuery(r.URL.Query())
		if err != nil {
			fail(w, 400, "invalid_query", err.Error())
			return
		}
		result, err := a.releaseNotesCenterListData(r.Context(), tx, page, size, search)
		if err != nil {
			failReleaseNotesCenterRead(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			failReleaseNotesCenterRead(w, err)
			return
		}
		write(w, 200, result)
		return
	}
	if len(r.URL.Query()) != 0 {
		fail(w, 400, "invalid_query", "详情读取不接受查询参数")
		return
	}
	sprint, ok := releaseNotesCenterSprintID(r.URL.Path)
	if !ok {
		fail(w, 404, "not_found", "升级日志不存在")
		return
	}
	result, err := a.releaseNotesCenterDetailData(r.Context(), tx, sprint)
	if err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	write(w, 200, result)
}

// 对外 API 只返回已经持久化的日志正文和可验证的截图元数据；附件下载仍需
// 使用登录态的需求附件接口，不能把 Cookie 资源 URL 伪装成 Bearer 可访问链接。
func releaseNotesCenterExternalDetail(value releaseNotesCenterDetail) map[string]any {
	requirements := make([]map[string]any, 0, len(value.Requirements))
	for _, requirement := range value.Requirements {
		images := make([]map[string]any, 0, len(requirement.Images))
		for _, image := range requirement.Images {
			images = append(images, map[string]any{"id": image.ID, "name": image.Name, "sha256": image.SHA256, "contentType": image.ContentType})
		}
		requirements = append(requirements, map[string]any{"id": requirement.ID, "code": requirement.Code, "title": requirement.Title, "type": requirement.Type, "category": requirement.Category, "status": requirement.Status, "description": requirement.Description, "acceptance": requirement.Acceptance, "updatedAt": requirement.UpdatedAt, "images": images})
	}
	markdown := value.Markdown
	for _, requirement := range value.Requirements {
		for _, image := range requirement.Images {
			// 对外正文仍保留“这里有一张真实截图”的可读引用，但不把只能靠
			// 浏览器 Cookie 访问的内部附件 URL 暴露给 Bearer 使用者。
			markdown = strings.ReplaceAll(markdown, fmt.Sprintf("](/api/requirements/%d/attachments/%d)", requirement.ID, image.ID), fmt.Sprintf("](image-reference:%d:%d)", requirement.ID, image.ID))
		}
	}
	return map[string]any{"projectId": value.ProjectID, "sprintId": value.SprintID, "sprintCode": value.SprintCode, "versionName": value.VersionName, "releaseDate": value.ReleaseDate, "revision": value.Revision, "state": value.State, "createdAt": value.CreatedAt, "updatedAt": value.UpdatedAt, "snapshot": true, "categories": value.Categories, "entries": value.Entries, "requirements": requirements, "markdown": markdown}
}

func (a *App) integrationReleaseNotes(w http.ResponseWriter, r *http.Request, route integrationRoute) {
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.integrationReleaseNotesAdmin(r.Context(), tx); err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	if route.id == 0 {
		page, size, search, err := releaseNotesCenterListQuery(r.URL.Query())
		if err != nil {
			fail(w, 400, "invalid_query", err.Error())
			return
		}
		result, err := a.releaseNotesCenterListData(r.Context(), tx, page, size, search)
		if err != nil {
			failReleaseNotesCenterRead(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			failReleaseNotesCenterRead(w, err)
			return
		}
		write(w, 200, result)
		return
	}
	if len(r.URL.Query()) != 0 {
		fail(w, 400, "invalid_query", "详情读取不接受查询参数")
		return
	}
	result, err := a.releaseNotesCenterDetailData(r.Context(), tx, route.id)
	if err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failReleaseNotesCenterRead(w, err)
		return
	}
	write(w, 200, releaseNotesCenterExternalDetail(result))
}
