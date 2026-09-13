package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"time"
)

func (a *App) requireAIGeneration(ctx context.Context, q stateStore) error {
	if a.impersonation != nil {
		return aiFailure(403, "impersonation_restricted", "代访问期间不能调用 AI 服务")
	}
	if err := a.requireOperationAccess(ctx, q); err != nil {
		return err
	}
	role, err := a.requirementStateRole(ctx, q)
	if err != nil {
		return err
	}
	if !validChoice(role, workflowRoleKeys()) {
		return aiFailure(403, "forbidden", "当前角色仅可查看")
	}
	return nil
}
func aiVersion(x Requirement, values map[string]any) string {
	encoded := jsonText(values) + "\n" + x.UpdatedAt
	hash := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(hash[:])
}
func aiStale() error {
	return aiFailure(409, "ai_draft_stale", "需求已变更或 AI 草稿已过期，请重新生成")
}
func (a *App) beginAIWrite(r *http.Request) (*sql.Tx, error) {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID)
	if err == nil {
		err = a.requireAIGeneration(r.Context(), tx)
	}
	if err == nil {
		err = a.requireAdministrationSession(r.Context(), tx)
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (a *App) requirementAITestCases(w http.ResponseWriter, r *http.Request, id int64, parts []string) {
	if len(parts) == 1 && parts[0] == "import" && r.Method == http.MethodPost {
		a.importAITestCases(w, r, id)
		return
	}
	if len(parts) != 0 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	if r.Method == http.MethodGet {
		s, err := a.readAISettings(r.Context(), a.db)
		if err != nil {
			failAI(w, err)
			return
		}
		generationErr := a.requireAIGeneration(r.Context(), a.db)
		var denied *organizationError
		if generationErr != nil && !errors.As(generationErr, &denied) {
			failAI(w, generationErr)
			return
		}
		write(w, 200, map[string]any{"configured": len(s.Encrypted) > 0, "enabled": s.Enabled, "model": s.Model, "baseUrl": s.BaseURL, "canGenerate": generationErr == nil, "inputFields": []string{"title", "description", "acceptance"}, "maxCases": 10, "focusOptions": aiTestFocusKeys})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		Confirmed            bool            `json:"confirmed"`
		RequirementUpdatedAt string          `json:"requirementUpdatedAt"`
		Focus                []string        `json:"focus"`
		ExtraInstructions    string          `json:"extraInstructions"`
		Count                *int            `json:"count"`
		ForceNew             json.RawMessage `json:"forceNew"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	forceNew, err := aiForceNewOption(input.ForceNew)
	if err != nil {
		failAI(w, err)
		return
	}
	if !input.Confirmed || input.RequirementUpdatedAt == "" {
		failAI(w, orgInvalid("请确认发送已保存需求内容并提供需求版本"))
		return
	}
	options, err := normalizeAITestGenerationOptions(input.Focus, input.ExtraInstructions, input.Count)
	if err != nil {
		failAI(w, err)
		return
	}
	tx, err := a.beginAIWrite(r)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	x, values, err := a.requirementSnapshot(tx, id)
	if err != nil {
		failAI(w, err)
		return
	}
	// 生成时确认当前需求仍处于当前项目的有效作用域，避免为已被并发移除的
	// 需求保留草稿。真正的关联写入还会在导入事务中再次校验。
	if err = a.validateTestCaseRequirement(r.Context(), tx, &id); err != nil {
		failAI(w, err)
		return
	}
	if x.UpdatedAt != input.RequirementUpdatedAt {
		failAI(w, aiStale())
		return
	}
	if len(x.Title)+len(x.Description)+len(x.Acceptance) > 100000 {
		failAI(w, orgInvalid("需求文本超过 AI 生成上限，请精简后重试"))
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
	var userCount, tenantCount int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*),COALESCE(sum(CASE WHEN user_id=? THEN 1 ELSE 0 END),0) FROM ai_test_case_drafts WHERE tenant_id=? AND created_at>=?`, a.uid(), tenantID, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)).Scan(&tenantCount, &userCount); err != nil {
		failAI(w, err)
		return
	}
	if userCount >= 5 || tenantCount >= 40 {
		failAI(w, aiFailure(429, "ai_rate_limited", "AI 生成次数已达每小时上限，请稍后重试"))
		return
	}
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		failAI(w, err)
		return
	}
	draftID := hex.EncodeToString(nonce)
	now := orgNow()
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	version := aiVersion(x, values)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO ai_test_case_drafts(id,tenant_id,project_id,requirement_id,user_id,requirement_updated_at,source_hash,settings_version,model,status,created_at,expires_at)VALUES(?,?,?,?,?,?,?,?,?,'generating',?,?)`, draftID, tenantID, a.pid(), id, a.uid(), x.UpdatedAt, version, s.Version, s.Model, now, expires)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	// Reservation is committed before the network call, bounding concurrent
	// charges. Only sanitized status is stored on failures, never upstream bodies.
	ready := false
	defer func() {
		if !ready {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = a.db.ExecContext(ctx, `UPDATE ai_test_case_drafts SET status='failed' WHERE tenant_id=? AND id=? AND status='generating'`, tenantID, draftID)
		}
	}()
	cacheKey := a.aiPreviewCacheKey("test-cases", s, version, map[string]any{"requirementId": id, "title": x.Title, "description": x.Description, "acceptance": x.Acceptance, "options": options})
	cached, reused := "", false
	if !forceNew {
		cached, reused = aiPreviews.get(cacheKey)
	}
	var cases []aiTestCase
	if reused {
		err = json.Unmarshal([]byte(cached), &cases)
	} else {
		cases, err = a.generateAITestCasesWithOptions(r.Context(), key, s.Model, s.BaseURL, x, options)
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
	current, currentValues, err := a.requirementSnapshot(tx2, id)
	if err != nil {
		failAI(w, err)
		return
	}
	config, err := a.readAISettings(r.Context(), tx2)
	if err != nil {
		failAI(w, err)
		return
	}
	if aiVersion(current, currentValues) != version || config.Version != s.Version || !config.Enabled || len(config.Encrypted) == 0 {
		failAI(w, aiStale())
		return
	}
	_, err = tx2.ExecContext(r.Context(), `UPDATE ai_test_case_drafts SET status='ready',cases_json=? WHERE tenant_id=? AND project_id=? AND id=? AND user_id=?`, jsonText(cases), tenantID, a.pid(), draftID, a.uid())
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx2, "requirement", "ai_test_cases_generated", id, map[string]any{"draftId": draftID, "count": len(cases), "requestedCount": options.Count, "focus": options.Focus, "model": s.Model, "reused": reused})
	}
	if err == nil {
		err = tx2.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	ready = true
	if !reused {
		aiPreviews.put(cacheKey, jsonText(cases))
	}
	write(w, 201, map[string]any{"draftId": draftID, "requirementId": id, "requirementUpdatedAt": x.UpdatedAt, "expiresAt": expires, "cases": cases, "reused": reused})
}

func (a *App) aiCaseDefaults(tx *sql.Tx) ([]requirementFieldWrite, error) {
	rows, err := tx.Query(`SELECT id,key,name,type,required,default_value,options FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND enabled=1 ORDER BY sort_order,id`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	writes := []requirementFieldWrite{}
	for rows.Next() {
		var d FieldDefinition
		var value, opts string
		if err = rows.Scan(&d.ID, &d.Key, &d.Name, &d.Type, &d.Required, &value, &opts); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(value), &d.DefaultValue); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(opts), &d.Options); err != nil {
			return nil, err
		}
		if err = validateFieldValue(d, d.DefaultValue); err != nil {
			return nil, customFieldValidationError{err.Error()}
		}
		if d.DefaultValue != nil {
			writes = append(writes, requirementFieldWrite{d.ID, d.DefaultValue})
		}
	}
	return writes, rows.Err()
}

func (a *App) importAITestCases(w http.ResponseWriter, r *http.Request, id int64) {
	var input struct {
		DraftID   string `json:"draftId"`
		Indexes   []int  `json:"indexes"`
		LibraryID *int64 `json:"libraryId"`
		FolderID  *int64 `json:"folderId"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	if len(input.DraftID) != 48 || len(input.Indexes) == 0 || len(input.Indexes) > 10 {
		failAI(w, orgInvalid("请选择有效的 AI 草稿用例"))
		return
	}
	if (input.LibraryID != nil && !testingPositiveID(*input.LibraryID)) || (input.FolderID != nil && (!testingPositiveID(*input.FolderID) || input.LibraryID == nil)) {
		failAI(w, orgInvalid("目标用例库或目录无效"))
		return
	}
	seen := map[int]bool{}
	for _, index := range input.Indexes {
		if index < 0 || index > 9 || seen[index] {
			failAI(w, orgInvalid("请选择有效的 AI 草稿用例"))
			return
		}
		seen[index] = true
	}
	sort.Ints(input.Indexes)
	tx, err := a.beginAIWrite(r)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	var hash, status, casesJSON, selectedJSON, importedJSON, expires string
	err = tx.QueryRowContext(r.Context(), `SELECT source_hash,status,cases_json,selected_json,imported_json,expires_at FROM ai_test_case_drafts WHERE tenant_id=? AND project_id=? AND requirement_id=? AND user_id=? AND id=?`, tenantID, a.pid(), id, a.uid(), input.DraftID).Scan(&hash, &status, &casesJSON, &selectedJSON, &importedJSON, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		failAI(w, aiFailure(404, "not_found", "AI 草稿不存在"))
		return
	}
	if err != nil {
		failAI(w, err)
		return
	}
	var cases []aiTestCase
	var selected []int
	var imported []map[string]any
	if json.Unmarshal([]byte(casesJSON), &cases) != nil || json.Unmarshal([]byte(selectedJSON), &selected) != nil || json.Unmarshal([]byte(importedJSON), &imported) != nil {
		failAI(w, errors.New("invalid draft"))
		return
	}
	if status == "imported" {
		if !reflect.DeepEqual(selected, input.Indexes) {
			failAI(w, aiFailure(409, "ai_already_imported", "该 AI 草稿已按其他选择导入"))
			return
		}
		write(w, 200, map[string]any{"items": imported, "importedCount": len(imported), "replayed": true})
		return
	}
	if status != "ready" || expires <= orgNow() {
		failAI(w, aiStale())
		return
	}
	x, values, err := a.requirementSnapshot(tx, id)
	if err != nil {
		failAI(w, err)
		return
	}
	// 草稿的 project_id 过滤只是第一层保护；在实际插入 test_cases 前再以
	// 同一事务确认关联需求，才能避免跨项目或并发删除造成孤儿用例。
	if err = a.validateTestCaseRequirement(r.Context(), tx, &id); err != nil {
		failAI(w, err)
		return
	}
	if aiVersion(x, values) != hash {
		failAI(w, aiStale())
		return
	}
	defaults, err := a.aiCaseDefaults(tx)
	if err != nil {
		failAI(w, err)
		return
	}
	now := orgNow()
	var actor string
	if err = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		failAI(w, err)
		return
	}
	imported = []map[string]any{}
	for _, index := range input.Indexes {
		if index >= len(cases) {
			failAI(w, orgInvalid("请选择有效的 AI 草稿用例"))
			return
		}
		candidate := cases[index]
		c := TestCase{Title: candidate.Title, Preconditions: candidate.Preconditions, Priority: candidate.Priority, CaseType: candidate.CaseType, StepsDetail: candidate.StepsDetail, RequirementID: &id, Status: "草稿", Enabled: true, Category: "未分类"}
		if err = a.validateCase(&c, false, false); err != nil {
			failAI(w, orgInvalid("AI 草稿用例格式无效，请重新生成"))
			return
		}
		res, insertErr := tx.ExecContext(r.Context(), `INSERT INTO test_cases(tenant_id,project_id,code,category,title,preconditions,steps,expected,priority,status,owner,requirement_id,owner_user_id,type,tags,enabled,steps_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", c.Category, c.Title, c.Preconditions, c.Steps, c.Expected, c.Priority, c.Status, "", id, "", c.CaseType, "", true, jsonText(c.StepsDetail), now, now)
		if insertErr != nil {
			failAI(w, insertErr)
			return
		}
		c.ID, err = res.LastInsertId()
		if err != nil {
			failAI(w, err)
			return
		}
		c.Code = fmt.Sprintf("TC-%04d", c.ID)
		_, err = tx.ExecContext(r.Context(), `UPDATE test_cases SET code=? WHERE tenant_id=? AND project_id=? AND id=?`, c.Code, tenantID, a.pid(), c.ID)
		if err == nil {
			err = a.writeObjectFields(tx, "test_case", c.ID, defaults, now)
		}
		if err == nil && input.LibraryID != nil {
			// 每条导入记录都在同一事务校验目标库/目录。任何一条越权或目录归属
			// 异常都会整体回滚，不能留下只移动了一部分的 AI 用例。
			_, err = a.assignTestingCaseLocation(r.Context(), tx, c.ID, input.LibraryID, input.FolderID, now)
		}
		if err == nil {
			c.UpdatedAt = now
			err = a.recordTestCaseTrace(r.Context(), tx, c, actor, "ai_imported", "确认导入了 AI 测试用例")
		}
		if err != nil {
			failAI(w, err)
			return
		}
		imported = append(imported, map[string]any{"id": c.ID, "code": c.Code, "title": c.Title})
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE ai_test_case_drafts SET status='imported',selected_json=?,imported_json=? WHERE tenant_id=? AND project_id=? AND id=?`, jsonText(input.Indexes), jsonText(imported), tenantID, a.pid(), input.DraftID)
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "requirement", "ai_test_cases_imported", id, map[string]any{"draftId": input.DraftID, "count": len(imported)})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	write(w, 200, map[string]any{"items": imported, "importedCount": len(imported), "replayed": false})
}
