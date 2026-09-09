package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	requirementTestCasesDefaultLimit = 30
	requirementTestCasesMaxLimit     = 100
)

// RequirementReference 是测试用例中可安全展示的需求摘要。它刻意不包含需求
// 描述、验收标准、成员或自定义字段，避免质量页面顺带读取到超出其用途的内容。
type RequirementReference struct {
	ID     int64  `json:"id"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// RequirementTestCaseSummary 是需求详情的轻量用例视图。步骤、预期结果和自定义
// 字段只应从已经授权的单个用例详情接口读取，不能因打开需求详情被批量返回。
type RequirementTestCaseSummary struct {
	ID            int64                 `json:"id"`
	Code          string                `json:"code"`
	Category      string                `json:"category"`
	Title         string                `json:"title"`
	Priority      string                `json:"priority"`
	Status        string                `json:"status"`
	Owner         string                `json:"owner"`
	OwnerUserID   string                `json:"ownerUserId,omitempty"`
	CaseType      string                `json:"caseType"`
	Tags          string                `json:"tags"`
	Enabled       bool                  `json:"enabled"`
	RequirementID *int64                `json:"requirementId,omitempty"`
	Requirement   *RequirementReference `json:"requirement,omitempty"`
	UpdatedAt     string                `json:"updatedAt"`
}

type RequirementTestCaseStats struct {
	Total         int `json:"total"`
	Enabled       int `json:"enabled"`
	Draft         int `json:"draft"`
	PendingReview int `json:"pendingReview"`
	Approved      int `json:"approved"`
	Deprecated    int `json:"deprecated"`
}

// requirementTestCasesCursor 保存稳定排序键。即便客户端伪造游标，后续查询仍会
// 固定 tenant_id、project_id 和 requirement_id，游标无法扩大可见范围。
type requirementTestCasesCursor struct {
	Version   int    `json:"v"`
	UpdatedAt string `json:"u"`
	ID        int64  `json:"i"`
}

type requirementTestCasesQuery struct {
	Limit  int
	Cursor *requirementTestCasesCursor
}

type testCaseRowScanner interface {
	Scan(...any) error
}

// scanTestCaseWithRequirement 复用列表和详情的同一条受作用域保护的 LEFT JOIN。
// 当旧数据的 requirement_id 指向别的项目或已不存在的需求时，关联摘要和原始 ID
// 都会被清空；读取端宁可降级为“未关联”，也不能泄漏外部对象标识。
func scanTestCaseWithRequirement(row testCaseRowScanner, c *TestCase) error {
	var stepsJSON string
	var referenceID sql.NullInt64
	var referenceCode, referenceTitle, referenceStatus sql.NullString
	if err := row.Scan(
		&c.ID, &c.Code, &c.Category, &c.Title, &c.Preconditions, &c.Steps, &c.Expected,
		&c.Priority, &c.Status, &c.Owner, &c.RequirementID, &c.OwnerUserID, &c.CaseType,
		&c.Tags, &c.Enabled, &stepsJSON, &c.UpdatedAt,
		&referenceID, &referenceCode, &referenceTitle, &referenceStatus,
	); err != nil {
		return err
	}
	parseJSON(stepsJSON, &c.StepsDetail)
	if referenceID.Valid {
		c.Requirement = &RequirementReference{ID: referenceID.Int64, Code: referenceCode.String, Title: referenceTitle.String, Status: referenceStatus.String}
		return nil
	}
	// 关联对象不存在或不属于当前项目时，不允许旧的 raw foreign key 继续出现在响应中。
	c.RequirementID = nil
	c.Requirement = nil
	return nil
}

// migrateRequirementTestCaseTraceability 只新增联合读取索引；不会改写既有用例
// 或关联数据。需求详情使用这一访问路径时能在大量用例下保持有界读取。
func (a *App) migrateRequirementTestCaseTraceability() error {
	_, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_test_cases_requirement_timeline ON test_cases(tenant_id,project_id,requirement_id,updated_at DESC,id DESC)`)
	return err
}

// requirementTestCaseReference 始终按当前租户与项目读取。即使历史数据曾留下
// 跨项目 requirement_id，也不会把另一项目的标题、状态或编号返回给调用方。
func (a *App) requirementTestCaseReference(ctx context.Context, q stateStore, id int64) (RequirementReference, error) {
	var reference RequirementReference
	err := q.QueryRowContext(ctx, `SELECT id,code,title,status FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&reference.ID, &reference.Code, &reference.Title, &reference.Status)
	return reference, err
}

// validateTestCaseRequirement 在写事务中再次验证关联关系。请求开始前的校验无法
// 阻止需求被并发移动或删除后仍写入孤儿关联，因此此检查必须和 INSERT/UPDATE 同事务。
func (a *App) validateTestCaseRequirement(ctx context.Context, q stateStore, requirementID *int64) error {
	if requirementID == nil {
		return nil
	}
	if *requirementID <= 0 {
		return orgInvalid("关联需求不属于当前项目或不存在")
	}
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM requirements r JOIN projects p ON p.id=r.project_id AND p.tenant_id=r.tenant_id WHERE r.tenant_id=? AND r.project_id=? AND r.id=? AND p.status='active'`, tenantID, a.pid(), *requirementID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return orgInvalid("关联需求不属于当前项目或不存在")
	}
	return nil
}

// recordTestCaseTrace 把业务活动和审计快照放进同一事务。审计快照只保存定位、
// 状态和关联元数据，不重复记录步骤与预期，既保留可追溯性也控制审计数据体积。
func (a *App) recordTestCaseTrace(ctx context.Context, q stateStore, c TestCase, actor, event, detail string) error {
	// 所有创建、复制和 AI 导入最终都会经过此处。没有显式库位置的历史/AI 用例
	// 在同一事务惰性补到默认库，避免前端列表出现“无归属”的不可管理记录。
	if _, err := a.assignTestingCaseLocation(ctx, q, c.ID, nil, nil, c.UpdatedAt); err != nil {
		return err
	}
	if err := a.appendTestingCaseCreationHistory(ctx, q, c.ID, event, detail, c.UpdatedAt); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'test_case',?,?,?,?,?)`, tenantID, a.pid(), c.ID, actor, event, detail, c.UpdatedAt); err != nil {
		return err
	}
	return a.auditRequirementState(ctx, q, "test_case", "test_case."+event, c.ID, map[string]any{
		"code":          c.Code,
		"title":         c.Title,
		"status":        c.Status,
		"priority":      c.Priority,
		"caseType":      c.CaseType,
		"ownerUserId":   c.OwnerUserID,
		"enabled":       c.Enabled,
		"requirementId": c.RequirementID,
	})
}

func parseRequirementTestCasesQuery(r *http.Request) (requirementTestCasesQuery, error) {
	query := requirementTestCasesQuery{Limit: requirementTestCasesDefaultLimit}
	values := r.URL.Query()
	var err error
	if values.Has("limit") {
		query.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || query.Limit < 1 || query.Limit > requirementTestCasesMaxLimit {
			return query, errors.New("分页大小无效")
		}
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := decodeRequirementTestCasesCursor(raw)
		if err != nil {
			return query, errors.New("分页游标无效")
		}
		query.Cursor = &cursor
	}
	return query, nil
}

func encodeRequirementTestCasesCursor(cursor requirementTestCasesCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeRequirementTestCasesCursor(raw string) (requirementTestCasesCursor, error) {
	value, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(value) == 0 || len(value) > 512 {
		return requirementTestCasesCursor{}, errors.New("invalid cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var cursor requirementTestCasesCursor
	if err = decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.ID < 1 || cursor.UpdatedAt == "" {
		return requirementTestCasesCursor{}, errors.New("invalid cursor")
	}
	if _, err = time.Parse(time.RFC3339, cursor.UpdatedAt); err != nil {
		return requirementTestCasesCursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

// requirementTestCases 是需求详情的只读追溯入口。调用方在路由层已经通过
// requireEntity 校验；这里仍读取当前项目的需求摘要并在所有查询中重复作用域条件，
// 让未来复用该处理器时不会意外绕过租户或项目边界。
func (a *App) requirementTestCases(w http.ResponseWriter, r *http.Request, id int64, parts []string) {
	if len(parts) != 0 {
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	query, err := parseRequirementTestCasesQuery(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	requirement, err := a.requirementTestCaseReference(r.Context(), a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	stats, err := a.requirementTestCaseStats(r.Context(), id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}

	where := []string{"c.tenant_id=?", "c.project_id=?", "c.requirement_id=?"}
	args := []any{tenantID, a.pid(), id}
	if query.Cursor != nil {
		// updated_at 与 id 组成确定序，避免同一秒生成的多个 AI 用例翻页时重复或漏项。
		where = append(where, "(c.updated_at<? OR (c.updated_at=? AND c.id<?))")
		args = append(args, query.Cursor.UpdatedAt, query.Cursor.UpdatedAt, query.Cursor.ID)
	}
	args = append(args, query.Limit+1)
	rows, err := a.db.QueryContext(r.Context(), `SELECT c.id,c.code,c.category,c.title,c.priority,c.status,c.owner,c.owner_user_id,c.type,c.tags,c.enabled,c.updated_at
		FROM test_cases c JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.id=c.requirement_id
		WHERE `+strings.Join(where, " AND ")+` ORDER BY c.updated_at DESC,c.id DESC LIMIT ?`, args...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := make([]RequirementTestCaseSummary, 0, query.Limit)
	hasMore := false
	for rows.Next() {
		if len(items) == query.Limit {
			hasMore = true
			break
		}
		item := RequirementTestCaseSummary{RequirementID: &id, Requirement: &requirement}
		if err = rows.Scan(&item.ID, &item.Code, &item.Category, &item.Title, &item.Priority, &item.Status, &item.Owner, &item.OwnerUserID, &item.CaseType, &item.Tags, &item.Enabled, &item.UpdatedAt); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = encodeRequirementTestCasesCursor(requirementTestCasesCursor{Version: 1, UpdatedAt: last.UpdatedAt, ID: last.ID})
	}
	write(w, http.StatusOK, map[string]any{
		"items":      items,
		"stats":      stats,
		"pageSize":   query.Limit,
		"hasMore":    hasMore,
		"nextCursor": nextCursor,
	})
}

func (a *App) requirementTestCaseStats(ctx context.Context, requirementID int64) (RequirementTestCaseStats, error) {
	var stats RequirementTestCaseStats
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN c.enabled=1 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN c.status='草稿' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN c.status='待评审' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN c.status='已通过' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN c.status='已废弃' THEN 1 ELSE 0 END),0)
		FROM test_cases c JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.id=c.requirement_id
		WHERE c.tenant_id=? AND c.project_id=? AND c.requirement_id=?`, tenantID, a.pid(), requirementID).
		Scan(&stats.Total, &stats.Enabled, &stats.Draft, &stats.PendingReview, &stats.Approved, &stats.Deprecated)
	return stats, err
}
