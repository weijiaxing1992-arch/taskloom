package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const releaseNotesPromptVersion = "devflow.release-notes.v2"
const releaseNotesMaxRequirements = 200
const releaseNotesMaxSourceBytes = 512 << 10
const releaseNotesBatchSize = 20

var releaseNoteCategories = []string{"大模型类型", "智能体类型", "AI呼叫类型", "CRM/短信/账单", "管理端/代理端", "API接口", "其他"}

// 升级日志分类必须由稳定规则兜底，不能把同一需求在不同批次或不同模型下
// 随机分到不同章节。显式业务类型/分类优先于标题，冲突时严格采用产品约定的
// 七类顺序；AI 只负责把已交付事实改写为便于客户阅读的文案。
var releaseNoteCategoryKeywords = [][]string{
	{"大模型", "语言模型", "llm", "rag", "提示词", "prompt", "知识库", "embedding", "向量检索", "多模态", "模型训练", "模型微调", "模型评测"},
	{"智能体", "agent", "multi-agent", "agentic", "技能编排", "工具调用", "智能工作流"},
	{"ai呼叫", "ai 呼叫", "智能呼叫", "电话呼叫", "外呼", "呼入", "通话", "话术", "语音机器人", "asr", "tts"},
	{"crm", "短信", "sms", "账单", "billing", "客户", "线索", "商机", "联系人", "计费", "充值", "余额", "支付", "发票"},
	{"管理端", "代理端", "代理商", "管理后台", "运营后台", "企业管理", "组织管理", "成员管理", "权限管理", "rbac"},
	{"api", "接口", "webhook", "sdk", "oauth", "开放平台", "系统集成"},
}

func releaseNotesCategoryIndex(category string) int {
	for i, value := range releaseNoteCategories {
		if category == value {
			return i
		}
	}
	return len(releaseNoteCategories) - 1
}

func releaseNotesContainsKeyword(value, keyword string) bool {
	value, keyword = strings.ToLower(value), strings.ToLower(keyword)
	if keyword == "api" || keyword == "llm" || keyword == "rag" || keyword == "sms" || keyword == "asr" || keyword == "tts" || keyword == "sdk" || keyword == "rbac" {
		pattern := regexp.MustCompile(`(?:^|[^a-z0-9])` + regexp.QuoteMeta(keyword) + `(?:$|[^a-z0-9])`)
		return pattern.MatchString(value)
	}
	return strings.Contains(value, keyword)
}

func releaseNotesCanonicalCategory(item releaseNoteRequirement) string {
	// 直接采用规范分类名称，避免用户已整理好的类型被二次猜测。
	for _, value := range releaseNoteCategories {
		if item.Type == value || item.Category == value {
			return value
		}
	}
	// 类型和业务分类比标题更可靠；仅在两者没有命中时才用标题兜底。
	for _, source := range []string{item.Type + " " + item.Category, item.Title} {
		for i, keywords := range releaseNoteCategoryKeywords {
			for _, keyword := range keywords {
				if releaseNotesContainsKeyword(source, keyword) {
					return releaseNoteCategories[i]
				}
			}
		}
	}
	return "其他"
}

func normalizeGeneratedReleaseNotes(source releaseNoteSource, entries []releaseNoteEntry) []releaseNoteEntry {
	items := make(map[int64]releaseNoteRequirement, len(source.Requirements))
	for _, item := range source.Requirements {
		items[item.ID] = item
	}
	for i := range entries {
		if len(entries[i].RequirementIDs) == 1 {
			if item, ok := items[entries[i].RequirementIDs[0]]; ok {
				entries[i].Category = releaseNotesCanonicalCategory(item)
			}
		}
	}
	return sortReleaseNotesEntries(entries)
}

// 返回副本，避免只为展示/导出而改变调用方仍在使用的编辑草稿。
func sortReleaseNotesEntries(entries []releaseNoteEntry) []releaseNoteEntry {
	ordered := append([]releaseNoteEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := releaseNotesCategoryIndex(ordered[i].Category), releaseNotesCategoryIndex(ordered[j].Category)
		if left != right {
			return left < right
		}
		if len(ordered[i].RequirementIDs) == 0 || len(ordered[j].RequirementIDs) == 0 {
			return len(ordered[i].RequirementIDs) > len(ordered[j].RequirementIDs)
		}
		return ordered[i].RequirementIDs[0] < ordered[j].RequirementIDs[0]
	})
	return ordered
}

type releaseNoteImage struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	SHA256      string `json:"sha256"`
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
}
type releaseNoteRequirement struct {
	ID          int64              `json:"id"`
	Code        string             `json:"code"`
	Title       string             `json:"title"`
	Type        string             `json:"type"`
	Category    string             `json:"category"`
	Status      string             `json:"status"`
	Description string             `json:"description"`
	Acceptance  string             `json:"acceptance"`
	UpdatedAt   string             `json:"updatedAt"`
	Images      []releaseNoteImage `json:"images"`
}
type releaseNoteSource struct {
	ProjectID    string                   `json:"projectId"`
	SprintID     int64                    `json:"sprintId"`
	VersionName  string                   `json:"versionName"`
	ReleaseDate  string                   `json:"releaseDate"`
	Requirements []releaseNoteRequirement `json:"requirements"`
}
type releaseNoteEntry struct {
	Category       string            `json:"category"`
	Title          string            `json:"title"`
	Description    string            `json:"description"`
	RequirementIDs []int64           `json:"requirementIds"`
	ImageIDs       []int64           `json:"imageIds"`
	ImageCaptions  map[string]string `json:"imageCaptions,omitempty"`
}

func releaseNotesBudgetError() error {
	return aiFailure(422, "release_notes_source_too_large", "迭代内容超过升级日志处理上限，请联系管理员缩小纳入范围或按版本拆分后重试；未截断或生成部分日志")
}
func releaseNotesInvalid() error {
	return aiFailure(422, "release_notes_invalid_content", "升级日志内容不符合规范：请完整保留每项已完成需求、有效分类及本需求图片")
}
func releaseNotesBugClassification(value string) bool {
	return strings.Contains(value, "缺陷") || releaseNotesBugWord.MatchString(value)
}

var releaseNotesBugWord = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:bugs?|defects?|bugfix(?:es)?|defectfix(?:es)?)(?:$|[^a-z])`)

// Only the caller's transaction is used. No defects, comments, credentials,
// owners, history bodies or external image resources enter the source.
// The caller authorizes the project and rechecks this fingerprint on adoption.
func (a *App) releaseNotesSource(ctx context.Context, store stateStore, project string, sprintID int64) (releaseNoteSource, string, error) {
	source := releaseNoteSource{ProjectID: project, SprintID: sprintID, Requirements: []releaseNoteRequirement{}}
	var status, updated string
	if err := store.QueryRowContext(ctx, `SELECT name,status,updated_at FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, project, sprintID).Scan(&source.VersionName, &status, &updated); err != nil {
		return source, "", err
	}
	if status != "已完成" {
		return source, "", aiFailure(409, "release_notes_sprint_not_completed", "完成迭代后才能生成升级日志")
	}
	var completed string
	err := store.QueryRowContext(ctx, `SELECT created_at FROM entity_activities WHERE tenant_id=? AND project_id=? AND object_type='sprint' AND object_id=? AND event='completed' ORDER BY id DESC LIMIT 1`, tenantID, project, sprintID).Scan(&completed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return source, "", err
	}
	if completed == "" {
		completed = updated
	}
	stamp, err := time.Parse(time.RFC3339Nano, completed)
	if err != nil {
		return source, "", aiFailure(422, "release_notes_invalid_source", "迭代完成时间无效，请先核对迭代记录")
	}
	source.ReleaseDate = stamp.UTC().Format(time.RFC3339)
	scoped := *a
	scoped.project = project
	first, second, err := scoped.scopedSprintAliasesFrom(ctx, store, source.VersionName)
	if err != nil {
		return source, "", err
	}
	rows, err := store.QueryContext(ctx, `SELECT r.id,r.code,r.title,r.type,r.category,r.status,r.description,r.acceptance,r.updated_at
FROM requirements r JOIN requirement_statuses s ON s.tenant_id=r.tenant_id AND s.project_id=r.project_id AND s.key=r.status
WHERE r.tenant_id=? AND r.project_id=? AND r.sprint IN (?,?) AND s.category='done'
AND instr(r.type,'缺陷')=0 AND instr(r.category,'缺陷')=0
AND NOT ((' '||lower(r.type)||' ') GLOB '*[^a-z]bug[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]bugs[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]defect[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]defects[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]bugfix[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]bugfixes[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]defectfix[^a-z]*' OR (' '||lower(r.type)||' ') GLOB '*[^a-z]defectfixes[^a-z]*')
AND NOT ((' '||lower(r.category)||' ') GLOB '*[^a-z]bug[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]bugs[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]defect[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]defects[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]bugfix[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]bugfixes[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]defectfix[^a-z]*' OR (' '||lower(r.category)||' ') GLOB '*[^a-z]defectfixes[^a-z]*')
ORDER BY r.id LIMIT ?`, tenantID, project, first, second, releaseNotesMaxRequirements+1)
	if err != nil {
		return source, "", err
	}
	bytesRead := 0
	for rows.Next() {
		var item releaseNoteRequirement
		item.Images = []releaseNoteImage{}
		if err = rows.Scan(&item.ID, &item.Code, &item.Title, &item.Type, &item.Category, &item.Status, &item.Description, &item.Acceptance, &item.UpdatedAt); err != nil {
			rows.Close()
			return source, "", err
		}
		item.Code = requirementDisplayCode(item.ID, item.Code)
		if releaseNotesBugClassification(item.Type) || releaseNotesBugClassification(item.Category) {
			continue
		}
		bytesRead += len(item.Title) + len(item.Description) + len(item.Acceptance) + len(item.Code) + len(item.Type) + len(item.Category)
		if bytesRead > releaseNotesMaxSourceBytes || len(source.Requirements) >= releaseNotesMaxRequirements {
			rows.Close()
			return source, "", releaseNotesBudgetError()
		}
		source.Requirements = append(source.Requirements, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return source, "", err
	}
	if len(source.Requirements) == 0 {
		return source, "", aiFailure(422, "release_notes_empty", "此迭代没有可纳入升级日志的已完成非缺陷需求")
	}
	ids := make([]int64, 0, len(source.Requirements))
	index := map[int64]int{}
	for i, item := range source.Requirements {
		ids = append(ids, item.ID)
		index[item.ID] = i
	}
	// Inspect metadata first. Only actual raster candidates are loaded locally;
	// image bytes never become part of source or the model payload.
	rows, err = store.QueryContext(ctx, `SELECT id,requirement_id,name,category,substr(content,1,512) FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id IN (SELECT value FROM json_each(?)) AND category!='bug' ORDER BY requirement_id,id LIMIT 2001`, tenantID, project, jsonText(ids))
	if err != nil {
		return source, "", err
	}
	type candidate struct {
		id, requirement int64
		name            string
	}
	candidates := []candidate{}
	seenRows := 0
	for rows.Next() {
		var item candidate
		var category string
		var sample []byte
		if err = rows.Scan(&item.id, &item.requirement, &item.name, &category, &sample); err != nil {
			rows.Close()
			return source, "", err
		}
		seenRows++
		if seenRows > 2000 {
			rows.Close()
			return source, "", releaseNotesBudgetError()
		}
		if attachmentCategory(item.name, category) == "bug" || !safeAttachmentFilename(item.name) {
			continue
		}
		if validChoice(http.DetectContentType(sample), []string{"image/png", "image/jpeg", "image/gif"}) {
			candidates = append(candidates, item)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return source, "", err
	}
	imageBytes, imagePixels := 0, int64(0)
	if len(candidates) > 256 {
		return source, "", releaseNotesBudgetError()
	}
	for _, item := range candidates {
		if err := ctx.Err(); err != nil {
			return source, "", err
		}
		var content []byte
		if err = store.QueryRowContext(ctx, `SELECT content FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`, tenantID, project, item.requirement, item.id).Scan(&content); err != nil {
			return source, "", err
		}
		imageBytes += len(content)
		if imageBytes > 64<<20 {
			return source, "", releaseNotesBudgetError()
		}
		config, format, decodeErr := image.DecodeConfig(bytes.NewReader(content))
		if decodeErr != nil {
			continue
		}
		pixels := int64(config.Width) * int64(config.Height)
		if format == "gif" {
			// The existing GIF decoder allows multiple frames. Reserve its full
			// frame-pixel allowance instead of counting only the first frame.
			pixels = 40000000
		}
		imagePixels += pixels
		if imagePixels > 160000000 {
			return source, "", releaseNotesBudgetError()
		}
		if validateRichImage(content) != nil {
			continue
		}
		digest := sha256.Sum256(content)
		pos := index[item.requirement]
		source.Requirements[pos].Images = append(source.Requirements[pos].Images, releaseNoteImage{ID: item.id, Name: item.name, SHA256: hex.EncodeToString(digest[:]), URL: fmt.Sprintf("/api/requirements/%d/attachments/%d", item.requirement, item.id), ContentType: http.DetectContentType(content)})
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return source, "", err
	}
	if len(encoded) > releaseNotesMaxSourceBytes {
		return source, "", releaseNotesBudgetError()
	}
	digest := sha256.Sum256(append([]byte(releaseNotesPromptVersion+"\n"), encoded...))
	return source, hex.EncodeToString(digest[:]), nil
}

func releaseNotesPlainText(text string, limit int) bool {
	if text != strings.TrimSpace(text) || text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	// Fixed renderer owns Markdown and images; no executable HTML, model URLs,
	// raw Markdown links or image syntax is accepted as feature prose.
	lower := strings.ToLower(text)
	return !strings.ContainsAny(text, "<>\r\n") && !strings.Contains(lower, "http:") && !strings.Contains(lower, "https:") && !strings.Contains(lower, "data:") && !strings.Contains(lower, "javascript:") && !strings.Contains(text, "](")
}

// Reject obvious future/cancelled delivery language locally as well as in the
// prompt. This is not a semantic truth oracle; a reviewer still checks facts.
var releaseNotesUndelivered = regexp.MustCompile(`(?i)^(?:(?:下一(?:期|迭代|版本)|(?:本需求|本功能)?(?:预计|计划于|拟于)).{0,24}(?:上线|交付|实现|完成)|(?:已取消|已拒绝|流程终止)(?:$|[，。,:：\s]|(?:该|此)?(?:功能|需求|计划))|(?:本需求|本功能)(?:尚未实现|待开发)|(?:cancelled|canceled|coming soon|will be delivered)\b)`)

func validateReleaseNotesEntries(source releaseNoteSource, entries []releaseNoteEntry) error {
	if len(source.Requirements) == 0 || len(source.Requirements) > releaseNotesMaxRequirements || len(entries) != len(source.Requirements) {
		return releaseNotesInvalid()
	}
	requirements := map[int64]releaseNoteRequirement{}
	for _, item := range source.Requirements {
		if item.ID <= 0 || requirements[item.ID].ID != 0 || releaseNotesBugClassification(item.Type) || releaseNotesBugClassification(item.Category) {
			return releaseNotesInvalid()
		}
		requirements[item.ID] = item
	}
	seen := map[int64]bool{}
	for _, entry := range entries {
		if !validChoice(entry.Category, releaseNoteCategories) || !releaseNotesPlainText(entry.Title, 120) || !releaseNotesPlainText(entry.Description, 2000) || releaseNotesUndelivered.MatchString(entry.Title) || releaseNotesUndelivered.MatchString(entry.Description) || len(entry.RequirementIDs) != 1 || entry.ImageIDs == nil || len(entry.ImageIDs) > 8 {
			return releaseNotesInvalid()
		}
		id := entry.RequirementIDs[0]
		item, ok := requirements[id]
		if !ok || seen[id] {
			return releaseNotesInvalid()
		}
		seen[id] = true
		images := map[int64]bool{}
		for _, img := range item.Images {
			images[img.ID] = true
		}
		selected := map[int64]bool{}
		for _, imageID := range entry.ImageIDs {
			if !images[imageID] || selected[imageID] {
				return releaseNotesInvalid()
			}
			selected[imageID] = true
		}
		for key, caption := range entry.ImageCaptions {
			id, err := strconv.ParseInt(key, 10, 64)
			if err != nil || strconv.FormatInt(id, 10) != key || !selected[id] || !releaseNotesPlainText(caption, 500) {
				return releaseNotesInvalid()
			}
		}
	}
	return nil
}

func releaseNotesMarkdownText(value string) string {
	// Escaping is independent of validation: titles, source names and captions
	// can never inject a Markdown heading, HTML element or a remote image.
	var out strings.Builder
	for _, r := range value {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '\n', '\r':
			out.WriteByte(' ')
		default:
			if strings.ContainsRune("\\`*_{}[]()#+-.!|", r) {
				out.WriteByte('\\')
			}
			if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func renderReleaseNotesMarkdown(source releaseNoteSource, entries []releaseNoteEntry) string {
	normalizeReleaseNoteSourceCodes(&source)
	if validateReleaseNotesEntries(source, entries) != nil {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# %s 升级日志\n\n版本：%s  \n时间：%s\n\n## 分类概览\n\n", releaseNotesMarkdownText(source.VersionName), releaseNotesMarkdownText(source.VersionName), releaseNotesMarkdownText(source.ReleaseDate))
	for _, category := range releaseNoteCategories {
		count := 0
		for _, item := range entries {
			if item.Category == category {
				count++
			}
		}
		fmt.Fprintf(&out, "- %s：%d 项\n", category, count)
	}
	requirements := map[int64]releaseNoteRequirement{}
	for _, item := range source.Requirements {
		requirements[item.ID] = item
	}
	for _, category := range releaseNoteCategories {
		fmt.Fprintf(&out, "\n## %s\n\n", category)
		items := []releaseNoteEntry{}
		for _, entry := range entries {
			if entry.Category == category {
				items = append(items, entry)
			}
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].RequirementIDs[0] < items[j].RequirementIDs[0] })
		if len(items) == 0 {
			out.WriteString("本版本暂无更新。\n")
			continue
		}
		for _, entry := range items {
			item := requirements[entry.RequirementIDs[0]]
			fmt.Fprintf(&out, "### %s\n\n%s\n\n来源需求：%s\n\n", releaseNotesMarkdownText(entry.Title), releaseNotesMarkdownText(entry.Description), releaseNotesMarkdownText(item.Code))
			if len(entry.ImageIDs) == 0 {
				out.WriteString("待补图：尚未选择本需求的真实功能截图。\n\n")
				continue
			}
			for _, id := range entry.ImageIDs {
				for _, img := range item.Images {
					if img.ID != id {
						continue
					}
					caption := entry.ImageCaptions[strconv.FormatInt(id, 10)]
					if caption == "" {
						caption = img.Name
					}
					fmt.Fprintf(&out, "![%s](/api/requirements/%d/attachments/%d)\n\n%s\n\n", releaseNotesMarkdownText(caption), item.ID, img.ID, releaseNotesMarkdownText(caption))
				}
			}
		}
	}
	return out.String()
}
