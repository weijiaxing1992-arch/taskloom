package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
)

// HomeRecentContentItem 是首页“最近内容”的最小投影。它只携带打开工作项
// 所需的稳定 ID、项目上下文与展示文案，不返回描述、成员和自定义字段等无关数据。
// 这样首页既能跨项目展示最近动态，也不会把项目详情预加载到浏览器中。
type HomeRecentContentItem struct {
	ID          int64  `json:"id"`
	ObjectType  string `json:"objectType"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ProjectCode string `json:"projectCode"`
	Event       string `json:"event"`
	UpdatedAt   string `json:"updatedAt"`
	URL         string `json:"url"`
}

// homeRecentContent 读取当前成员有权访问的活动项目中最近更新的需求、缺陷和迭代。
// 可见性在 SQL 的 accessible_projects CTE 内收口，避免先查全量工作项再由 Go 侧过滤
// 而产生跨项目标题泄漏；归档/删除项目也不会出现在首页动态中。
func (a *App) homeRecentContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	if err := a.requireOperationAccess(r.Context(), a.db); err != nil {
		failOrganization(w, err)
		return
	}
	limit := 8
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 20 {
			fail(w, http.StatusUnprocessableEntity, "validation_error", "最近内容数量需在 1 到 20 之间")
			return
		}
		limit = value
	}

	// 注意：三个 UNION 分支均以同一可见项目 CTE 连接，并同时匹配 tenant_id。
	// 这比单独用 project_id 关联更能抵御未来项目 ID 迁移或历史脏数据带来的串租户风险。
	rows, err := a.db.QueryContext(r.Context(), `
WITH accessible_projects AS (
  SELECT p.id,p.tenant_id,p.name,p.code
  FROM projects p
  WHERE p.tenant_id=? AND p.status='active'
    AND (
      EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?)
      OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active')
    )
), recent_items AS (
  SELECT 'requirement' AS object_type,r.id,r.code,r.title,r.project_id,p.name,p.code AS project_code,r.updated_at
  FROM requirements r JOIN accessible_projects p ON p.id=r.project_id AND p.tenant_id=r.tenant_id
  UNION ALL
  SELECT 'defect' AS object_type,d.id,d.code,d.title,d.project_id,p.name,p.code AS project_code,d.updated_at
  FROM defects d JOIN accessible_projects p ON p.id=d.project_id AND p.tenant_id=d.tenant_id
  UNION ALL
  SELECT 'sprint' AS object_type,s.id,s.code,s.name,s.project_id,p.name,p.code AS project_code,s.updated_at
  FROM sprints s JOIN accessible_projects p ON p.id=s.project_id AND p.tenant_id=s.tenant_id
)
SELECT object_type,id,code,title,project_id,name,project_code,updated_at
FROM recent_items
ORDER BY updated_at DESC,id DESC
LIMIT ?`, tenantID, a.uid(), a.uid(), limit)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "最近内容暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()

	items := make([]HomeRecentContentItem, 0, limit)
	for rows.Next() {
		var item HomeRecentContentItem
		if err := rows.Scan(&item.ObjectType, &item.ID, &item.Code, &item.Title, &item.ProjectID, &item.ProjectName, &item.ProjectCode, &item.UpdatedAt); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "最近内容暂时无法读取，请稍后重试")
			return
		}
		switch item.ObjectType {
		case "requirement":
			item.Code = requirementDisplayCode(item.ID, item.Code)
			item.Event, item.URL = "更新了需求", "/requirements?req="+strconv.FormatInt(item.ID, 10)
		case "defect":
			item.Event, item.URL = "更新了缺陷", "/defects?bug="+strconv.FormatInt(item.ID, 10)
		case "sprint":
			item.Event, item.URL = "更新了迭代", "/iterations?sprint="+strconv.FormatInt(item.ID, 10)
		default:
			// SQL 常量以外的数据不应生成可导航的未知对象；保守跳过而非猜测 URL。
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "最近内容暂时无法读取，请稍后重试")
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "limit": limit})
}
