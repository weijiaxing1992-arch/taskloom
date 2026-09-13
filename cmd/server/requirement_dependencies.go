package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// requirement_dependencies 独立于历史 work_item_relations。历史的 relates_to
// 关联不迁移、不改写；新表保存带方向的交付依赖，并显式保存两端作用域。
const dependencyRelationBlocks = "blocks"
const dependencyRelationRelates = "relates_to"

// requirementDependencyState 是需求池、路线图等只读视图使用的交付依赖摘要。
// 只统计当前账号可访问的两端项目，不能将跨项目依赖当作目录枚举入口。
type requirementDependencyState struct {
	State          string `json:"state"`
	BlockedByCount int    `json:"blockedByCount"`
	BlockingCount  int    `json:"blockingCount"`
	RelatedCount   int    `json:"relatedCount"`
}

// 每条边要携带源、目标两个项目与需求编号；200 条时参数数低于 SQLite 常见的 999 上限。
const requirementDependencyStateBatch = 200

type dependencyScope struct {
	TenantID      string
	ProjectID     string
	RequirementID int64
}

type dependencyEndpoint struct {
	TenantID       string `json:"tenantId"`
	ProjectID      string `json:"projectId"`
	ProjectName    string `json:"projectName"`
	RequirementID  int64  `json:"requirementId"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	StatusName     string `json:"statusName,omitempty"`
	StatusColor    string `json:"statusColor,omitempty"`
	StatusCategory string `json:"statusCategory,omitempty"`
	StatusSystem   bool   `json:"statusSystem"`
	IsEnd          bool   `json:"isEnd"`
}

// requirementDependencyItem 的 relationType 始终以当前打开的需求为参照。
// 例如数据库中 B blocks A，读取 A 时会返回 blocked_by，避免调用方猜测边方向。
type requirementDependencyItem struct {
	ID           int64              `json:"id"`
	RelationType string             `json:"relationType"`
	Source       dependencyEndpoint `json:"source"`
	Target       dependencyEndpoint `json:"target"`
	Counterpart  dependencyEndpoint `json:"counterpart"`
	CrossProject bool               `json:"crossProject"`
	CreatedAt    string             `json:"createdAt"`
	UpdatedAt    string             `json:"updatedAt"`
}

type dependencyWriteRequest struct {
	TargetTenantID      string `json:"targetTenantId"`
	TargetProjectID     string `json:"targetProjectId"`
	TargetRequirementID int64  `json:"targetRequirementId"`
	RelationType        string `json:"relationType"`
}

type dependencyError struct {
	status  int
	code    string
	message string
}

func (e dependencyError) Error() string { return e.message }

func dependencyInvalid(message string) error {
	return dependencyError{status: http.StatusUnprocessableEntity, code: "invalid_dependency", message: message}
}

func dependencyConflict(message string) error {
	return dependencyError{status: http.StatusConflict, code: "dependency_conflict", message: message}
}

func dependencyNotFound() error {
	// 对目标不存在、跨项目撤权、归档项目统一返回同一个结果，不能让错误文本成为目录枚举入口。
	return dependencyError{status: http.StatusNotFound, code: "dependency_target_unavailable", message: "依赖目标不存在或无权访问"}
}

func failDependency(w http.ResponseWriter, err error) {
	var problem dependencyError
	if errors.As(err, &problem) {
		fail(w, problem.status, problem.code, problem.message)
		return
	}
	var organization *organizationError
	if errors.As(err, &organization) {
		fail(w, organization.Status, organization.Code, organization.Message)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	fail(w, http.StatusServiceUnavailable, "dependency_unavailable", "需求依赖服务暂时不可用，请稍后重试")
}

// 这是纯新增表：不需要迁移或重写历史 work_item_relations，升级和回滚都不会改变旧关联。
func (a *App) migrateRequirementDependencies() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS requirement_dependencies(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 source_tenant_id TEXT NOT NULL,
 source_project_id TEXT NOT NULL,
 source_requirement_id INTEGER NOT NULL,
 target_tenant_id TEXT NOT NULL,
 target_project_id TEXT NOT NULL,
 target_requirement_id INTEGER NOT NULL,
 relation_type TEXT NOT NULL CHECK(relation_type IN ('blocks','relates_to')),
 created_by TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(source_tenant_id,source_project_id,source_requirement_id,target_tenant_id,target_project_id,target_requirement_id,relation_type)
);
CREATE INDEX IF NOT EXISTS idx_requirement_dependencies_source ON requirement_dependencies(source_tenant_id,source_project_id,source_requirement_id,relation_type,id DESC);
CREATE INDEX IF NOT EXISTS idx_requirement_dependencies_target ON requirement_dependencies(target_tenant_id,target_project_id,target_requirement_id,relation_type,id DESC);`)
	return err
}

// hydrateRequirementDependencyStates 为需求池完整候选集计算依赖摘要。
// 调用方会在此之后再进行高级筛选和分页，因而“仅被阻塞”不会只检查当前页。
// 每条依赖都同时校验源、目标项目的可见权限；看不到前置项时不返回其存在或数量。
func (a *App) hydrateRequirementDependencyStates(ctx context.Context, items []Requirement) error {
	if len(items) == 0 {
		return nil
	}
	if _, err := a.dependencyAccessibleProjects(ctx); err != nil {
		return err
	}
	catalog, err := a.stateCatalogByProject(ctx)
	if err != nil {
		return err
	}
	for index := range items {
		items[index].DependencyStatus = &requirementDependencyState{State: "clear"}
	}
	for start := 0; start < len(items); start += requirementDependencyStateBatch {
		end := min(start+requirementDependencyStateBatch, len(items))
		batch := items[start:end]
		byKey := make(map[string]*Requirement, len(batch))
		predicates := make([]string, 0, len(batch)*2)
		args := []any{tenantID, tenantID}
		for index := range batch {
			item := &batch[index]
			byKey[dependencyStateKey(a.pid(), item.ID)] = item
			predicates = append(predicates,
				`(d.source_project_id=? AND d.source_requirement_id=?)`,
				`(d.target_project_id=? AND d.target_requirement_id=?)`,
			)
			args = append(args, a.pid(), item.ID, a.pid(), item.ID)
		}
		args = append(args, a.uid(), a.uid(), a.uid(), a.uid())
		rows, err := a.db.QueryContext(ctx, `SELECT d.source_project_id,d.source_requirement_id,d.target_project_id,d.target_requirement_id,d.relation_type,sr.status,tr.status
 FROM requirement_dependencies d
 JOIN requirements sr ON sr.tenant_id=d.source_tenant_id AND sr.project_id=d.source_project_id AND sr.id=d.source_requirement_id
 JOIN requirements tr ON tr.tenant_id=d.target_tenant_id AND tr.project_id=d.target_project_id AND tr.id=d.target_requirement_id
 JOIN projects sp ON sp.tenant_id=d.source_tenant_id AND sp.id=d.source_project_id AND sp.status='active'
 JOIN projects tp ON tp.tenant_id=d.target_tenant_id AND tp.id=d.target_project_id AND tp.status='active'
 WHERE d.source_tenant_id=? AND d.target_tenant_id=? AND (`+strings.Join(predicates, " OR ")+`)
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.source_tenant_id AND pm.project_id=d.source_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.source_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.target_tenant_id AND pm.project_id=d.target_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.target_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sourceProject, targetProject, relation, sourceStatus, targetStatus string
			var sourceID, targetID int64
			if err = rows.Scan(&sourceProject, &sourceID, &targetProject, &targetID, &relation, &sourceStatus, &targetStatus); err != nil {
				rows.Close()
				return err
			}
			source, target := byKey[dependencyStateKey(sourceProject, sourceID)], byKey[dependencyStateKey(targetProject, targetID)]
			if relation == dependencyRelationRelates {
				if source != nil {
					source.DependencyStatus.RelatedCount++
				}
				if target != nil {
					target.DependencyStatus.RelatedCount++
				}
				continue
			}
			isTerminal := func(project, status string) bool {
				state, found := catalog[project][status]
				return found && terminalCategory(state.Category)
			}
			if source != nil && !isTerminal(targetProject, targetStatus) {
				source.DependencyStatus.BlockingCount++
			}
			if target != nil && !isTerminal(sourceProject, sourceStatus) {
				target.DependencyStatus.BlockedByCount++
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	for index := range items {
		state := items[index].DependencyStatus
		if state.BlockedByCount > 0 {
			state.State = "blocked"
		} else if state.BlockingCount > 0 {
			state.State = "blocking"
		} else if state.RelatedCount > 0 {
			state.State = "related"
		}
	}
	return nil
}

func dependencyStateKey(project string, id int64) string {
	return project + "\x00" + strconv.FormatInt(id, 10)
}

func dependencyPage(r *http.Request, defaultSize, maxSize int) (page, size int, err error) {
	page, size = 1, defaultSize
	for _, item := range []struct {
		key  string
		dest *int
		max  int
	}{{"page", &page, 100000}, {"pageSize", &size, maxSize}} {
		if !r.URL.Query().Has(item.key) {
			continue
		}
		value, parseErr := strconv.Atoi(r.URL.Query().Get(item.key))
		if parseErr != nil || value < 1 || value > item.max {
			return 0, 0, dependencyInvalid("分页参数无效")
		}
		*item.dest = value
	}
	return page, size, nil
}

func dependencyLike(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 120 {
		return "", dependencyInvalid("搜索关键词不能超过 120 个字符")
	}
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	value = strings.ReplaceAll(value, "_", "\\_")
	return value, nil
}

func dependencyScopeKey(scope dependencyScope) string {
	return scope.TenantID + "\x00" + scope.ProjectID + "\x00" + strconv.FormatInt(scope.RequirementID, 10)
}

func dependencyCanonicalRelated(left, right dependencyScope) (dependencyScope, dependencyScope) {
	if dependencyScopeKey(left) > dependencyScopeKey(right) {
		return right, left
	}
	return left, right
}

func dependencyStoredScope(current, counterpart dependencyScope, relation string) (dependencyScope, dependencyScope, string, error) {
	switch relation {
	case "blocks":
		return current, counterpart, dependencyRelationBlocks, nil
	case "blocked_by":
		return counterpart, current, dependencyRelationBlocks, nil
	case "relates_to":
		left, right := dependencyCanonicalRelated(current, counterpart)
		return left, right, dependencyRelationRelates, nil
	default:
		return dependencyScope{}, dependencyScope{}, "", dependencyInvalid("依赖类型仅支持 blocks、blocked_by 或 relates_to")
	}
}

func dependencyRelativeType(current dependencyScope, source dependencyScope, storedType string) string {
	if storedType == dependencyRelationRelates {
		return "relates_to"
	}
	if dependencyScopeKey(current) == dependencyScopeKey(source) {
		return "blocks"
	}
	return "blocked_by"
}

func (a *App) dependencyAccessibleProjects(ctx context.Context) (map[string]bool, error) {
	projects, err := a.accessibleProjectIDsChecked(ctx, a.uid())
	if err != nil {
		return nil, err
	}
	if !projects[a.pid()] {
		// scopedAPI 正常情况下已保证该条件；保留它是为了直接 handler 调用或授权撤销竞态时 fail closed。
		return nil, dependencyNotFound()
	}
	return projects, nil
}

// 跨项目依赖必须在同一事务内复查两端项目权限。不得先读取目标标题再鉴权，
// 否则用户可借由候选、报错或 timing 枚举无权项目中的需求。
func (a *App) dependencyProjectAllowed(ctx context.Context, tx stateStore, projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return dependencyNotFound()
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects p
 JOIN users u ON u.tenant_id=p.tenant_id AND u.id=? AND u.active=1
 JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active'
 WHERE p.tenant_id=? AND p.id=? AND p.status='active'
 AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=u.id))`, a.uid(), tenantID, projectID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return dependencyNotFound()
	}
	return nil
}

func dependencyRequirementExists(ctx context.Context, tx stateStore, scope dependencyScope) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, scope.TenantID, scope.ProjectID, scope.RequirementID).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return dependencyNotFound()
	}
	return nil
}

// GET /api/requirement-dependency-candidates 为当前需求返回受限的候选项。scope=all 是显式选择，
// 仍只包含当前用户有 active membership 或 tenant-admin 权限的项目。
func (a *App) requirementDependencyCandidates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	currentID, err := strconv.ParseInt(r.URL.Query().Get("requirementId"), 10, 64)
	if err != nil || currentID <= 0 {
		failDependency(w, dependencyInvalid("需求编号不正确"))
		return
	}
	if !a.requireEntity(w, "requirement", currentID) {
		return
	}
	page, size, err := dependencyPage(r, 1, 50)
	if err != nil {
		failDependency(w, err)
		return
	}
	query, err := dependencyLike(r.URL.Query().Get("q"))
	if err != nil {
		failDependency(w, err)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "current"
	}
	if scope != "current" && scope != "all" {
		failDependency(w, dependencyInvalid("候选范围无效"))
		return
	}
	if _, err := a.dependencyAccessibleProjects(r.Context()); err != nil {
		failDependency(w, err)
		return
	}
	args := []any{tenantID, a.pid(), currentID}
	where := `r.tenant_id=? AND NOT (r.project_id=? AND r.id=?)`
	if scope == "current" {
		where += ` AND r.project_id=?`
		args = append(args, a.pid())
	} else {
		// 这里把权限条件放进同一条候选查询，避免可访问项目很多时拼出无界 IN 参数。
		where += ` AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=r.tenant_id AND pm.project_id=r.project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=r.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`
		args = append(args, a.uid(), a.uid())
	}
	if query != "" {
		// Existing deployments may still have REQ-xxxx values in storage while
		// the UI now exposes the concise numeric serial. Match the stable ID as
		// well so both 000123 and REQ-000123 can find the same candidate.
		where += ` AND (r.code LIKE '%' || ? || '%' ESCAPE '\' OR r.title LIKE '%' || ? || '%' ESCAPE '\'`
		args = append(args, query, query)
		if codeID := requirementCodeQueryID(query); codeID > 0 {
			where += ` OR r.id=?`
			args = append(args, codeID)
		}
		where += `)`
	}
	args = append(args, size+1, (page-1)*size)
	queryArgs := append([]any{}, args[:len(args)-2]...)
	queryArgs = append(queryArgs, a.pid(), args[len(args)-2], args[len(args)-1])
	rows, err := a.db.QueryContext(r.Context(), `SELECT r.id,r.project_id,p.name,r.code,r.title,r.status
	 FROM requirements r JOIN projects p ON p.tenant_id=r.tenant_id AND p.id=r.project_id AND p.status='active'
 WHERE `+where+` ORDER BY CASE WHEN r.project_id=? THEN 0 ELSE 1 END,r.updated_at DESC,r.id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		failDependency(w, err)
		return
	}
	defer rows.Close()
	catalog, err := a.stateCatalogByProject(r.Context())
	if err != nil {
		failDependency(w, err)
		return
	}
	items := []dependencyEndpoint{}
	for rows.Next() {
		var item dependencyEndpoint
		if err = rows.Scan(&item.RequirementID, &item.ProjectID, &item.ProjectName, &item.Code, &item.Title, &item.Status); err != nil {
			failDependency(w, err)
			return
		}
		item.Code = requirementDisplayCode(item.RequirementID, item.Code)
		item.TenantID = tenantID
		applyDependencyStatus(&item, catalog[item.ProjectID])
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		failDependency(w, err)
		return
	}
	hasMore := len(items) > size
	if hasMore {
		items = items[:size]
	}
	write(w, http.StatusOK, map[string]any{"items": items, "page": page, "pageSize": size, "hasMore": hasMore, "scope": scope})
}

func applyDependencyStatus(item *dependencyEndpoint, catalog map[string]RequirementStatus) {
	state, ok := catalog[item.Status]
	if !ok {
		state = RequirementStatus{Key: item.Status, Name: item.Status, Category: "todo"}
	}
	item.StatusName, item.StatusColor, item.StatusCategory, item.StatusSystem = state.Name, state.Color, state.Category, state.System
	item.IsEnd = terminalCategory(state.Category)
}

func (a *App) requirementDependencies(w http.ResponseWriter, r *http.Request, currentID int64, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		a.listRequirementDependencies(w, r, currentID)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodPost {
		a.createRequirementDependency(w, r, currentID)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		dependencyID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || dependencyID <= 0 {
			failDependency(w, dependencyInvalid("依赖编号不正确"))
			return
		}
		a.updateRequirementDependency(w, r, currentID, dependencyID)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		dependencyID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || dependencyID <= 0 {
			failDependency(w, dependencyInvalid("依赖编号不正确"))
			return
		}
		a.deleteRequirementDependency(w, r, currentID, dependencyID)
		return
	}
	w.Header().Set("Allow", "GET, POST, PATCH, DELETE")
	fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
}

func (a *App) listRequirementDependencies(w http.ResponseWriter, r *http.Request, currentID int64) {
	page, size, err := dependencyPage(r, 1, 50)
	if err != nil {
		failDependency(w, err)
		return
	}
	_, err = a.dependencyAccessibleProjects(r.Context())
	if err != nil {
		failDependency(w, err)
		return
	}
	items, hasMore, err := a.readDependencyItems(r.Context(), a.db, dependencyScope{TenantID: tenantID, ProjectID: a.pid(), RequirementID: currentID}, 0, size, (page-1)*size)
	if err != nil {
		failDependency(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "page": page, "pageSize": size, "hasMore": hasMore})
}

func (a *App) readDependencyItems(ctx context.Context, q stateStore, current dependencyScope, onlyID int64, limit, offset int) ([]requirementDependencyItem, bool, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, false, dependencyNotFound()
	}
	args := []any{current.TenantID, current.TenantID, current.ProjectID, current.RequirementID, current.ProjectID, current.RequirementID, onlyID, onlyID}
	args = append(args, a.uid(), a.uid(), a.uid(), a.uid())
	args = append(args, limit+1, offset)
	rows, err := q.QueryContext(ctx, `SELECT d.id,d.source_tenant_id,d.source_project_id,d.source_requirement_id,d.target_tenant_id,d.target_project_id,d.target_requirement_id,d.relation_type,d.created_at,d.updated_at,
 sp.name,sr.code,sr.title,sr.status,tp.name,tr.code,tr.title,tr.status
 FROM requirement_dependencies d
 JOIN requirements sr ON sr.tenant_id=d.source_tenant_id AND sr.project_id=d.source_project_id AND sr.id=d.source_requirement_id
 JOIN requirements tr ON tr.tenant_id=d.target_tenant_id AND tr.project_id=d.target_project_id AND tr.id=d.target_requirement_id
 JOIN projects sp ON sp.tenant_id=d.source_tenant_id AND sp.id=d.source_project_id AND sp.status='active'
 JOIN projects tp ON tp.tenant_id=d.target_tenant_id AND tp.id=d.target_project_id AND tp.status='active'
 WHERE d.source_tenant_id=? AND d.target_tenant_id=?
 AND ((d.source_project_id=? AND d.source_requirement_id=?) OR (d.target_project_id=? AND d.target_requirement_id=?))
	 AND (?=0 OR d.id=?)
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.source_tenant_id AND pm.project_id=d.source_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.source_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.target_tenant_id AND pm.project_id=d.target_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.target_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))
 ORDER BY d.updated_at DESC,d.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	catalog, err := a.stateCatalogByProject(ctx)
	if err != nil {
		return nil, false, err
	}
	items := []requirementDependencyItem{}
	for rows.Next() {
		var item requirementDependencyItem
		if err = rows.Scan(&item.ID, &item.Source.TenantID, &item.Source.ProjectID, &item.Source.RequirementID, &item.Target.TenantID, &item.Target.ProjectID, &item.Target.RequirementID, &item.RelationType, &item.CreatedAt, &item.UpdatedAt,
			&item.Source.ProjectName, &item.Source.Code, &item.Source.Title, &item.Source.Status, &item.Target.ProjectName, &item.Target.Code, &item.Target.Title, &item.Target.Status); err != nil {
			return nil, false, err
		}
		item.Source.Code = requirementDisplayCode(item.Source.RequirementID, item.Source.Code)
		item.Target.Code = requirementDisplayCode(item.Target.RequirementID, item.Target.Code)
		applyDependencyStatus(&item.Source, catalog[item.Source.ProjectID])
		applyDependencyStatus(&item.Target, catalog[item.Target.ProjectID])
		item.RelationType = dependencyRelativeType(current, dependencyScope{TenantID: item.Source.TenantID, ProjectID: item.Source.ProjectID, RequirementID: item.Source.RequirementID}, item.RelationType)
		if dependencyScopeKey(current) == dependencyScopeKey(dependencyScope{TenantID: item.Source.TenantID, ProjectID: item.Source.ProjectID, RequirementID: item.Source.RequirementID}) {
			item.Counterpart = item.Target
		} else {
			item.Counterpart = item.Source
		}
		item.CrossProject = item.Counterpart.ProjectID != current.ProjectID
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return items, hasMore, nil
}

func decodeDependencyRequest(w http.ResponseWriter, r *http.Request, targetRequired bool) (dependencyWriteRequest, error) {
	var request dependencyWriteRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return request, dependencyInvalid("请求格式不正确")
	}
	request.TargetTenantID = strings.TrimSpace(request.TargetTenantID)
	request.TargetProjectID = strings.TrimSpace(request.TargetProjectID)
	request.RelationType = strings.TrimSpace(request.RelationType)
	if targetRequired && request.TargetRequirementID <= 0 {
		return request, dependencyInvalid("请选择有效的依赖需求")
	}
	return request, nil
}

func (a *App) createRequirementDependency(w http.ResponseWriter, r *http.Request, currentID int64) {
	request, err := decodeDependencyRequest(w, r, true)
	if err != nil {
		failDependency(w, err)
		return
	}
	if request.TargetTenantID == "" {
		request.TargetTenantID = tenantID
	}
	if request.TargetProjectID == "" {
		request.TargetProjectID = a.pid()
	}
	if request.TargetTenantID != tenantID {
		failDependency(w, dependencyNotFound())
		return
	}
	current := dependencyScope{TenantID: tenantID, ProjectID: a.pid(), RequirementID: currentID}
	counterpart := dependencyScope{TenantID: request.TargetTenantID, ProjectID: request.TargetProjectID, RequirementID: request.TargetRequirementID}
	if dependencyScopeKey(current) == dependencyScopeKey(counterpart) {
		failDependency(w, dependencyInvalid("需求不能依赖自身"))
		return
	}
	source, target, storedType, err := dependencyStoredScope(current, counterpart, request.RelationType)
	if err != nil {
		failDependency(w, err)
		return
	}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failDependency(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.dependencyProjectAllowed(r.Context(), tx, current.ProjectID); err == nil {
		err = a.dependencyProjectAllowed(r.Context(), tx, counterpart.ProjectID)
	}
	if err == nil {
		err = dependencyRequirementExists(r.Context(), tx, current)
	}
	if err == nil {
		err = dependencyRequirementExists(r.Context(), tx, counterpart)
	}
	if err != nil {
		failDependency(w, err)
		return
	}
	var existingID int64
	err = tx.QueryRowContext(r.Context(), `SELECT id FROM requirement_dependencies WHERE source_tenant_id=? AND source_project_id=? AND source_requirement_id=? AND target_tenant_id=? AND target_project_id=? AND target_requirement_id=? AND relation_type=?`, source.TenantID, source.ProjectID, source.RequirementID, target.TenantID, target.ProjectID, target.RequirementID, storedType).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failDependency(w, err)
		return
	}
	created := errors.Is(err, sql.ErrNoRows)
	if created && storedType == dependencyRelationBlocks {
		if err = a.rejectDependencyCycle(r.Context(), tx, source, target, 0); err != nil {
			failDependency(w, err)
			return
		}
	}
	if created {
		now := orgNow()
		result, insertErr := tx.ExecContext(r.Context(), `INSERT INTO requirement_dependencies(source_tenant_id,source_project_id,source_requirement_id,target_tenant_id,target_project_id,target_requirement_id,relation_type,created_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?)`, source.TenantID, source.ProjectID, source.RequirementID, target.TenantID, target.ProjectID, target.RequirementID, storedType, a.uid(), now, now)
		if insertErr != nil {
			failDependency(w, insertErr)
			return
		}
		existingID, err = result.LastInsertId()
		if err == nil {
			err = a.auditRequirementDependency(r.Context(), tx, "requirement_dependency_created", existingID, nil, dependencyAuditValue(source, target, storedType))
		}
		if err != nil {
			failDependency(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		failDependency(w, err)
		return
	}
	item, err := a.readSingleDependency(r.Context(), current, existingID)
	if err != nil {
		failDependency(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	write(w, status, map[string]any{"item": item, "created": created})
}

func dependencyAuditValue(source, target dependencyScope, relationType string) map[string]any {
	return map[string]any{
		"source":       map[string]any{"tenantId": source.TenantID, "projectId": source.ProjectID, "requirementId": source.RequirementID},
		"target":       map[string]any{"tenantId": target.TenantID, "projectId": target.ProjectID, "requirementId": target.RequirementID},
		"relationType": relationType,
	}
}

func (a *App) auditRequirementDependency(ctx context.Context, tx stateStore, action string, id int64, before, after map[string]any) error {
	if before == nil {
		before = map[string]any{}
	}
	if after == nil {
		after = map[string]any{}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "requirement_dependency", fmt.Sprint(id), action, jsonText(before), jsonText(after), orgNow())
	return err
}

// rejectDependencyCycle 检查“target 能否已到达 source”。路径被限制为 256 个节点，并用
// path 防止已有坏数据中的环重复展开；达到上限时拒绝写入而不是冒险放行未知闭环。
func (a *App) rejectDependencyCycle(ctx context.Context, tx stateStore, source, target dependencyScope, excludingID int64) error {
	var reachesSource, reachedLimit bool
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE reach(project_id,requirement_id,depth,path) AS (
 SELECT ?,?,0,printf('|%s:%d|',?,?)
 UNION ALL
 SELECT d.target_project_id,d.target_requirement_id,reach.depth+1,
        reach.path || printf('%s:%d|',d.target_project_id,d.target_requirement_id)
 FROM requirement_dependencies d JOIN reach
   ON d.source_tenant_id=? AND d.source_project_id=reach.project_id AND d.source_requirement_id=reach.requirement_id
 WHERE d.target_tenant_id=? AND d.relation_type='blocks' AND d.id<>?
   AND reach.depth<256
   AND instr(reach.path,printf('|%s:%d|',d.target_project_id,d.target_requirement_id))=0
 )
 SELECT EXISTS(SELECT 1 FROM reach WHERE project_id=? AND requirement_id=?),
        EXISTS(SELECT 1 FROM reach WHERE depth>=256)`,
		target.ProjectID, target.RequirementID, target.ProjectID, target.RequirementID,
		tenantID, tenantID, excludingID, source.ProjectID, source.RequirementID).Scan(&reachesSource, &reachedLimit)
	if err != nil {
		return err
	}
	if reachesSource {
		return dependencyConflict("该阻塞关系会形成循环依赖")
	}
	if reachedLimit {
		return dependencyInvalid("依赖链过长，请拆分后重试")
	}
	return nil
}

type dependencyRaw struct {
	ID       int64
	Source   dependencyScope
	Target   dependencyScope
	Relation string
}

func (a *App) dependencyForCurrent(ctx context.Context, tx stateStore, current dependencyScope, id int64) (dependencyRaw, error) {
	var item dependencyRaw
	err := tx.QueryRowContext(ctx, `SELECT id,source_tenant_id,source_project_id,source_requirement_id,target_tenant_id,target_project_id,target_requirement_id,relation_type
 FROM requirement_dependencies WHERE id=? AND source_tenant_id=? AND target_tenant_id=?
 AND ((source_project_id=? AND source_requirement_id=?) OR (target_project_id=? AND target_requirement_id=?))`, id, tenantID, tenantID, current.ProjectID, current.RequirementID, current.ProjectID, current.RequirementID).
		Scan(&item.ID, &item.Source.TenantID, &item.Source.ProjectID, &item.Source.RequirementID, &item.Target.TenantID, &item.Target.ProjectID, &item.Target.RequirementID, &item.Relation)
	if errors.Is(err, sql.ErrNoRows) {
		return item, dependencyNotFound()
	}
	return item, err
}

func (a *App) readSingleDependency(ctx context.Context, current dependencyScope, id int64) (requirementDependencyItem, error) {
	if _, err := a.dependencyAccessibleProjects(ctx); err != nil {
		return requirementDependencyItem{}, err
	}
	// 读取刚新增/更新的那一条时必须按 id 定向查询，不能依赖时间排序的首项。
	items, _, err := a.readDependencyItems(ctx, a.db, current, id, 1, 0)
	if err != nil {
		return requirementDependencyItem{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return requirementDependencyItem{}, dependencyNotFound()
}

func (a *App) updateRequirementDependency(w http.ResponseWriter, r *http.Request, currentID, dependencyID int64) {
	request, err := decodeDependencyRequest(w, r, false)
	if err != nil {
		failDependency(w, err)
		return
	}
	if request.RelationType == "" {
		failDependency(w, dependencyInvalid("请选择依赖类型"))
		return
	}
	current := dependencyScope{TenantID: tenantID, ProjectID: a.pid(), RequirementID: currentID}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failDependency(w, err)
		return
	}
	defer tx.Rollback()
	old, err := a.dependencyForCurrent(r.Context(), tx, current, dependencyID)
	if err != nil {
		failDependency(w, err)
		return
	}
	counterpart := old.Source
	if dependencyScopeKey(current) == dependencyScopeKey(old.Source) {
		counterpart = old.Target
	}
	if err = a.dependencyProjectAllowed(r.Context(), tx, current.ProjectID); err == nil {
		err = a.dependencyProjectAllowed(r.Context(), tx, counterpart.ProjectID)
	}
	if err != nil {
		failDependency(w, err)
		return
	}
	source, target, storedType, err := dependencyStoredScope(current, counterpart, request.RelationType)
	if err != nil {
		failDependency(w, err)
		return
	}
	if dependencyScopeKey(source) == dependencyScopeKey(target) {
		failDependency(w, dependencyInvalid("需求不能依赖自身"))
		return
	}
	changed := dependencyScopeKey(source) != dependencyScopeKey(old.Source) || dependencyScopeKey(target) != dependencyScopeKey(old.Target) || storedType != old.Relation
	if changed && storedType == dependencyRelationBlocks {
		if err = a.rejectDependencyCycle(r.Context(), tx, source, target, dependencyID); err != nil {
			failDependency(w, err)
			return
		}
	}
	if changed {
		var duplicate int64
		err = tx.QueryRowContext(r.Context(), `SELECT id FROM requirement_dependencies WHERE source_tenant_id=? AND source_project_id=? AND source_requirement_id=? AND target_tenant_id=? AND target_project_id=? AND target_requirement_id=? AND relation_type=? AND id<>?`, source.TenantID, source.ProjectID, source.RequirementID, target.TenantID, target.ProjectID, target.RequirementID, storedType, dependencyID).Scan(&duplicate)
		if err == nil {
			failDependency(w, dependencyConflict("相同的依赖关系已存在"))
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			failDependency(w, err)
			return
		}
		before := dependencyAuditValue(old.Source, old.Target, old.Relation)
		after := dependencyAuditValue(source, target, storedType)
		_, err = tx.ExecContext(r.Context(), `UPDATE requirement_dependencies SET source_tenant_id=?,source_project_id=?,source_requirement_id=?,target_tenant_id=?,target_project_id=?,target_requirement_id=?,relation_type=?,updated_at=? WHERE id=?`, source.TenantID, source.ProjectID, source.RequirementID, target.TenantID, target.ProjectID, target.RequirementID, storedType, orgNow(), dependencyID)
		if err == nil {
			err = a.auditRequirementDependency(r.Context(), tx, "requirement_dependency_updated", dependencyID, before, after)
		}
		if err != nil {
			failDependency(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		failDependency(w, err)
		return
	}
	item, err := a.readSingleDependency(r.Context(), current, dependencyID)
	if err != nil {
		failDependency(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": item, "updated": changed})
}

func (a *App) deleteRequirementDependency(w http.ResponseWriter, r *http.Request, currentID, dependencyID int64) {
	current := dependencyScope{TenantID: tenantID, ProjectID: a.pid(), RequirementID: currentID}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failDependency(w, err)
		return
	}
	defer tx.Rollback()
	old, err := a.dependencyForCurrent(r.Context(), tx, current, dependencyID)
	if err != nil {
		failDependency(w, err)
		return
	}
	counterpart := old.Source
	if dependencyScopeKey(current) == dependencyScopeKey(old.Source) {
		counterpart = old.Target
	}
	if err = a.dependencyProjectAllowed(r.Context(), tx, current.ProjectID); err == nil {
		err = a.dependencyProjectAllowed(r.Context(), tx, counterpart.ProjectID)
	}
	if err != nil {
		failDependency(w, err)
		return
	}
	before := dependencyAuditValue(old.Source, old.Target, old.Relation)
	_, err = tx.ExecContext(r.Context(), `DELETE FROM requirement_dependencies WHERE id=? AND source_tenant_id=? AND target_tenant_id=?`, dependencyID, tenantID, tenantID)
	if err == nil {
		err = a.auditRequirementDependency(r.Context(), tx, "requirement_dependency_deleted", dependencyID, before, nil)
	}
	if err != nil {
		failDependency(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failDependency(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"deleted": true, "id": dependencyID})
}
