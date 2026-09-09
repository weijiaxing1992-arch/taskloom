package main

import (
	"net/http"
	"strconv"
)

type myWorkSprint struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Status      string `json:"status"`
}

// 迭代选择使用编号而非名称；仅返回当前账号可访问且符合项目筛选的迭代。
// 先读取元数据再开启工作项游标，避免单连接数据库上的嵌套读取阻塞。
func (a *App) myWorkSprintSelection(w http.ResponseWriter, r *http.Request, access map[string]bool, scope string) ([]myWorkSprint, *myWorkSprint, bool) {
	choices := []myWorkSprint{}
	raw := r.URL.Query().Get("sprintId")
	var id int64
	if raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			fail(w, 422, "invalid_sprint", "迭代编号无效")
			return nil, nil, false
		}
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT s.id,s.name,s.project_id,p.name,s.status FROM sprints s JOIN projects p ON p.tenant_id=s.tenant_id AND p.id=s.project_id WHERE s.tenant_id=? AND p.status='active' ORDER BY CASE s.status WHEN '进行中' THEN 0 WHEN '规划中' THEN 1 ELSE 2 END,s.updated_at DESC,s.id DESC`, tenantID)
	if err != nil {
		failRequirementResource(w, err)
		return nil, nil, false
	}
	defer rows.Close()
	var selected *myWorkSprint
	for rows.Next() {
		var item myWorkSprint
		if err = rows.Scan(&item.ID, &item.Name, &item.ProjectID, &item.ProjectName, &item.Status); err != nil {
			failRequirementResource(w, err)
			return nil, nil, false
		}
		if !access[item.ProjectID] || (scope != "all" && scope != item.ProjectID) {
			continue
		}
		choices = append(choices, item)
		if item.ID == id {
			copy := item
			selected = &copy
		}
	}
	if err = rows.Err(); err != nil {
		failRequirementResource(w, err)
		return nil, nil, false
	}
	if id > 0 && selected == nil {
		fail(w, 404, "not_found", "迭代不存在")
		return nil, nil, false
	}
	return choices, selected, true
}
func matchesWorkSprint(item workItem, selected *myWorkSprint) bool {
	if selected == nil {
		return true
	}
	if item.ProjectID != selected.ProjectID {
		return false
	}
	if item.Type == "迭代" {
		return item.ID == selected.ID
	}
	return item.Sprint == selected.Name
}
