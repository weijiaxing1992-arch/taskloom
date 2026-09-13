package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

type roadmapDependencyState struct {
	State          string `json:"state"`
	BlockedByCount int    `json:"blockedByCount"`
	BlockingCount  int    `json:"blockingCount"`
	RelatedCount   int    `json:"relatedCount"`
}

type roadmapRequirement struct {
	ID               int64                  `json:"id"`
	Code             string                 `json:"code"`
	Title            string                 `json:"title"`
	TenantID         string                 `json:"tenantId"`
	ProjectID        string                 `json:"projectId"`
	ProjectName      string                 `json:"projectName"`
	Sprint           string                 `json:"sprint"`
	StartDate        string                 `json:"startDate"`
	EndDate          string                 `json:"endDate"`
	Status           string                 `json:"status"`
	StatusName       string                 `json:"statusName,omitempty"`
	StatusColor      string                 `json:"statusColor,omitempty"`
	StatusCategory   string                 `json:"statusCategory,omitempty"`
	StatusSystem     bool                   `json:"statusSystem"`
	IsEnd            bool                   `json:"isEnd"`
	Priority         string                 `json:"priority"`
	UpdatedAt        string                 `json:"updatedAt"`
	DependencyStatus roadmapDependencyState `json:"dependencyStatus"`
}

func roadmapKey(project string, id int64) string {
	return project + "\x00" + strconv.FormatInt(id, 10)
}

// roadmap 只返回当前用户在请求时仍可访问的项目。scope=all 同样采用关联权限条件，
// 不先读取企业项目目录再在内存筛选，避免大量项目时出现无界 IN 参数或缓存越权。
func (a *App) roadmap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	page, size, err := dependencyPage(r, 1, 100)
	if err != nil {
		failDependency(w, err)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "current"
	}
	if scope != "current" && scope != "all" {
		failDependency(w, dependencyInvalid("路线图范围无效"))
		return
	}
	if _, err = a.dependencyAccessibleProjects(r.Context()); err != nil {
		failDependency(w, err)
		return
	}
	args := []any{tenantID}
	where := `r.tenant_id=?`
	if scope == "current" {
		where += ` AND r.project_id=?`
		args = append(args, a.pid())
	} else {
		where += ` AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=r.tenant_id AND pm.project_id=r.project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=r.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`
		args = append(args, a.uid(), a.uid())
	}
	args = append(args, size+1, (page-1)*size)
	rows, err := a.db.QueryContext(r.Context(), `SELECT r.id,r.code,r.title,r.tenant_id,r.project_id,p.name,r.sprint,r.start_date,r.end_date,r.status,r.priority,r.updated_at
 FROM requirements r JOIN projects p ON p.tenant_id=r.tenant_id AND p.id=r.project_id AND p.status='active'
 WHERE `+where+`
 ORDER BY CASE WHEN trim(r.sprint)='' OR r.sprint='待规划' THEN 1 ELSE 0 END,
          CASE WHEN trim(r.start_date)='' THEN 1 ELSE 0 END,r.start_date,r.end_date,r.sprint,r.id
 LIMIT ? OFFSET ?`, args...)
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
	items := []roadmapRequirement{}
	for rows.Next() {
		var item roadmapRequirement
		if err = rows.Scan(&item.ID, &item.Code, &item.Title, &item.TenantID, &item.ProjectID, &item.ProjectName, &item.Sprint, &item.StartDate, &item.EndDate, &item.Status, &item.Priority, &item.UpdatedAt); err != nil {
			failDependency(w, err)
			return
		}
		item.Code = requirementDisplayCode(item.ID, item.Code)
		state, ok := catalog[item.ProjectID][item.Status]
		if !ok {
			state = RequirementStatus{Key: item.Status, Name: item.Status, Category: "todo"}
		}
		item.StatusName, item.StatusColor, item.StatusCategory, item.StatusSystem = state.Name, state.Color, state.Category, state.System
		item.IsEnd = terminalCategory(state.Category)
		item.DependencyStatus = roadmapDependencyState{State: "clear"}
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
	if err = a.hydrateRoadmapDependencies(r.Context(), items, catalog); err != nil {
		failDependency(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "page": page, "pageSize": size, "hasMore": hasMore, "scope": scope})
}

func dependencyIsTerminal(catalog map[string]map[string]RequirementStatus, project, status string) bool {
	state, ok := catalog[project][status]
	return ok && terminalCategory(state.Category)
}

// hydrateRoadmapDependencies 只对已分页的当前页工作项查询一次依赖摘要；最多 100 个工作项，
// 每项两条作用域谓词，避免为了状态图一次拉取项目的全部依赖边。
func (a *App) hydrateRoadmapDependencies(ctx context.Context, items []roadmapRequirement, catalog map[string]map[string]RequirementStatus) error {
	if len(items) == 0 {
		return nil
	}
	predicates := make([]string, 0, len(items)*2)
	args := []any{tenantID, tenantID}
	for _, item := range items {
		predicates = append(predicates, `(d.source_project_id=? AND d.source_requirement_id=?)`, `(d.target_project_id=? AND d.target_requirement_id=?)`)
		args = append(args, item.ProjectID, item.ID, item.ProjectID, item.ID)
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
	defer rows.Close()
	byKey := map[string]*roadmapRequirement{}
	for index := range items {
		byKey[roadmapKey(items[index].ProjectID, items[index].ID)] = &items[index]
	}
	for rows.Next() {
		var sourceProject, targetProject, relation, sourceStatus, targetStatus string
		var sourceID, targetID int64
		if err = rows.Scan(&sourceProject, &sourceID, &targetProject, &targetID, &relation, &sourceStatus, &targetStatus); err != nil {
			return err
		}
		source, target := byKey[roadmapKey(sourceProject, sourceID)], byKey[roadmapKey(targetProject, targetID)]
		if relation == dependencyRelationRelates {
			if source != nil {
				source.DependencyStatus.RelatedCount++
			}
			if target != nil {
				target.DependencyStatus.RelatedCount++
			}
			continue
		}
		if source != nil && !dependencyIsTerminal(catalog, targetProject, targetStatus) {
			source.DependencyStatus.BlockingCount++
		}
		if target != nil && !dependencyIsTerminal(catalog, sourceProject, sourceStatus) {
			target.DependencyStatus.BlockedByCount++
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, item := range byKey {
		if item.DependencyStatus.BlockedByCount > 0 {
			item.DependencyStatus.State = "blocked"
		} else if item.DependencyStatus.BlockingCount > 0 {
			item.DependencyStatus.State = "blocking"
		} else if item.DependencyStatus.RelatedCount > 0 {
			item.DependencyStatus.State = "related"
		}
	}
	return nil
}
