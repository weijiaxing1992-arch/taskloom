package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const aiTitleDescriptionLimit = 100000 // UTF-8 bytes, never silently truncated.
const aiTitleLengthLimit = 80          // Unicode code points.

func (a *App) migrateAITitles() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS ai_title_requests(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,requirement_id INTEGER,settings_version INTEGER NOT NULL,model TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL,finished_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_ai_title_rate ON ai_title_requests(tenant_id,user_id,created_at);
CREATE INDEX IF NOT EXISTS idx_ai_title_pending ON ai_title_requests(tenant_id,status,expires_at);`)
	return err
}

var aiTitleMediaMarkup = regexp.MustCompile(`(?is)!\[[^\]]*\]\([^)]*\)|<[^>]*>|(?:https?://|data:)[^\s]+`)
var aiTitleMediaFilename = regexp.MustCompile(`(?i)^[^\r\n]+\.(?:png|jpe?g|gif|webp|svg|bmp|pdf|docx?|xlsx?|pptx?|zip|mp[34]|mov|wav|txt)$`)

func validateAITitleDescription(description string) error {
	if len(description) > aiTitleDescriptionLimit {
		return orgInvalid("需求描述超过 AI 标题生成上限，请精简后重试")
	}
	if !utf8.ValidString(description) {
		return orgInvalid("请补充包含功能范围和开发目标的文字描述")
	}
	for _, r := range description {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return orgInvalid("请补充包含功能范围和开发目标的文字描述")
		}
	}
	plain := aiTitleMediaMarkup.ReplaceAllString(description, "")
	letters := 0
	for _, line := range strings.Split(plain, "\n") {
		if aiTitleMediaFilename.MatchString(strings.TrimSpace(line)) {
			continue
		}
		for _, r := range line {
			if unicode.IsLetter(r) {
				letters++
			}
		}
	}
	if letters < 6 {
		return orgInvalid("请补充包含功能范围和开发目标的文字描述")
	}
	return nil
}

func (a *App) aiTitleRequirementVersion(ctx context.Context, tx *sql.Tx, id *int64) (string, error) {
	if id == nil {
		return "", nil
	}
	if *id <= 0 {
		return "", orgInvalid("需求编号不正确")
	}
	x, values, err := a.requirementSnapshot(tx, *id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", aiFailure(404, "not_found", "需求不存在")
	}
	if err != nil {
		return "", err
	}
	return aiVersion(x, values), nil
}

func (a *App) requirementAITitle(w http.ResponseWriter, r *http.Request) {
	a.requirementAIText(w, r, false)
}
func (a *App) requirementAIRefine(w http.ResponseWriter, r *http.Request) {
	a.requirementAIText(w, r, true)
}

// 两种文本生成共享权限、限流、配置版本与需求版本检查，不持久化生成正文。
func (a *App) requirementAIText(w http.ResponseWriter, r *http.Request, refine bool) {
	defect := r.URL.Path == "/api/ai/defect-refine"
	if r.Method == http.MethodGet {
		s, err := a.readAISettings(r.Context(), a.db)
		if err != nil {
			failAI(w, err)
			return
		}
		permission := a.requireAIGeneration(r.Context(), a.db)
		var denied *organizationError
		if permission != nil && !errors.As(permission, &denied) {
			failAI(w, permission)
			return
		}
		write(w, 200, map[string]any{"configured": len(s.Encrypted) > 0, "enabled": s.Enabled, "model": s.Model, "baseUrl": s.BaseURL, "canGenerate": permission == nil, "maxDescriptionLength": aiTitleDescriptionLimit, "maxTitleLength": aiTitleLengthLimit})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		Description   string          `json:"description"`
		Confirmed     bool            `json:"confirmed"`
		RequirementID *int64          `json:"requirementId"`
		ForceNew      json.RawMessage `json:"forceNew"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	forceNew, err := aiForceNewOption(input.ForceNew)
	if defect && input.RequirementID != nil {
		fail(w, 400, "invalid_input", "缺陷优化仅接收当前草稿文字")
		return
	}
	if err != nil {
		failAI(w, err)
		return
	}
	if !input.Confirmed {
		failAI(w, orgInvalid("请确认将需求描述发送给 OpenAI 生成标题"))
		return
	}
	if err := validateAITitleDescription(input.Description); err != nil {
		failAI(w, err)
		return
	}
	tx, err := a.beginAIWrite(r)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	version, err := a.aiTitleRequirementVersion(r.Context(), tx, input.RequirementID)
	if err != nil {
		failAI(w, err)
		return
	}
	s, err := a.readAISettings(r.Context(), tx)
	if err != nil {
		failAI(w, err)
		return
	}
	if !s.Enabled || len(s.Encrypted) == 0 {
		failAI(w, aiFailure(409, "ai_not_configured", "请先配置并启用 OpenAI API Key"))
		return
	}
	key, err := openAIKey(a.wecomKey, s.Encrypted)
	if err != nil {
		failAI(w, err)
		return
	}
	now := time.Now().UTC()
	var userCount, tenantCount, userPending, tenantPending int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(sum(CASE WHEN user_id=? THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN status='generating' AND expires_at>? THEN 1 ELSE 0 END),0),COALESCE(sum(CASE WHEN status='generating' AND expires_at>? AND user_id=? THEN 1 ELSE 0 END),0) FROM ai_title_requests WHERE tenant_id=? AND created_at>=?`, a.uid(), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), a.uid(), tenantID, now.Add(-time.Hour).Format(time.RFC3339Nano)).Scan(&tenantCount, &userCount, &tenantPending, &userPending); err != nil {
		failAI(w, err)
		return
	}
	if userCount >= 10 || tenantCount >= 80 || userPending >= 1 || tenantPending >= 4 {
		failAI(w, aiFailure(429, "ai_rate_limited", "AI 标题生成过于频繁，请等待当前请求完成后稍后重试"))
		return
	}
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		failAI(w, err)
		return
	}
	requestID := hex.EncodeToString(nonce)
	expires := now.Add(time.Minute).Format(time.RFC3339Nano)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO ai_title_requests(id,tenant_id,project_id,user_id,requirement_id,settings_version,model,status,created_at,expires_at)VALUES(?,?,?,?,?,?,?,'generating',?,?)`, requestID, tenantID, a.pid(), a.uid(), input.RequirementID, s.Version, s.Model, now.Format(time.RFC3339Nano), expires)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	finished := false
	defer func() {
		if !finished {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = a.db.ExecContext(ctx, `UPDATE ai_title_requests SET status='failed',finished_at=? WHERE tenant_id=? AND id=? AND status='generating'`, orgNow(), tenantID, requestID)
		}
	}()
	// No transaction or DB writer is held across the paid network request.
	kind := "title"
	if refine {
		kind = "refinement"
	}
	if defect {
		kind = "defect-refinement"
	}
	cacheKey := a.aiPreviewCacheKey(kind, s, version, map[string]any{"description": input.Description, "requirementId": input.RequirementID})
	title, reused := "", false
	if !forceNew {
		title, reused = aiPreviews.get(cacheKey)
	}
	if !reused {
		title, err = a.generateAIProfessionalText(r.Context(), key, s.Model, s.BaseURL, input.Description, refine, defect)
	}
	if err != nil {
		failAI(w, err)
		return
	}
	tx2, err := a.beginAIWrite(r)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx2.Rollback()
	currentVersion, err := a.aiTitleRequirementVersion(r.Context(), tx2, input.RequirementID)
	if err != nil {
		failAI(w, err)
		return
	}
	config, err := a.readAISettings(r.Context(), tx2)
	if err != nil {
		failAI(w, err)
		return
	}
	var status, storedExpiry string
	if err = tx2.QueryRowContext(r.Context(), `SELECT status,expires_at FROM ai_title_requests WHERE tenant_id=? AND project_id=? AND user_id=? AND id=?`, tenantID, a.pid(), a.uid(), requestID).Scan(&status, &storedExpiry); err != nil {
		failAI(w, err)
		return
	}
	deadline, timeErr := time.Parse(time.RFC3339Nano, storedExpiry)
	if config.Version != s.Version || !config.Enabled || len(config.Encrypted) == 0 || version != currentVersion || status != "generating" || timeErr != nil || !time.Now().UTC().Before(deadline) {
		failAI(w, aiFailure(409, "ai_title_stale", "需求或 AI 配置已变更，或标题生成已过期，请重新生成"))
		return
	}
	_, err = tx2.ExecContext(r.Context(), `UPDATE ai_title_requests SET status='completed',finished_at=? WHERE tenant_id=? AND project_id=? AND user_id=? AND id=? AND status='generating'`, orgNow(), tenantID, a.pid(), a.uid(), requestID)
	if err == nil {
		action := "ai_requirement_title_generated"
		if refine {
			action = "ai_requirement_refined"
		}
		err = a.auditRequirementState(r.Context(), tx2, "ai_title_request", action, requestID, map[string]any{"model": s.Model, "requirementId": input.RequirementID, "reused": reused})
	}
	if err == nil {
		err = tx2.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	finished = true
	if !reused {
		aiPreviews.put(cacheKey, title)
	}
	if refine {
		result, _ := parseAIRefinement(title)
		write(w, 200, map[string]any{"preview": result, "model": s.Model, "reused": reused})
		return
	}
	write(w, 200, map[string]any{"title": title, "model": s.Model, "reused": reused})
}
