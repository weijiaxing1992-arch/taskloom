package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Fetch full records, not the compact board projection. Both backlog and sprint
// lists share this path so field filtering never depends on a paginated pool.
func (a *App) sprintWorkItems(ctx context.Context, name string) ([]map[string]any, error) {
	first, second := a.scopedSprintAliases(name)
	if name == "待规划" {
		first, second = "待规划", ""
	}
	rows, err := a.db.QueryContext(ctx, `SELECT `+requirementSelectColumns+` FROM requirements WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) ORDER BY id`, tenantID, a.pid(), first, second)
	if err != nil {
		return nil, err
	}
	requirements := []Requirement{}
	for rows.Next() {
		var requirement Requirement
		if err = scanRequirement(rows, &requirement); err != nil {
			rows.Close()
			return nil, err
		}
		requirements = append(requirements, requirement)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, requirement := range requirements {
		if err = a.hydrateRequirementState(ctx, &requirement); err != nil {
			return nil, err
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, id := range requirement.AssigneeUserIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		requirement.AssigneeUserIDs = ids
		if err = a.loadRequirementAssignees(a.db, &requirement); err != nil {
			return nil, err
		}
		requirement.CustomFields, err = a.checkedWorkCustomFields(ctx, "requirement", requirement.ID)
		if err != nil {
			return nil, err
		}
		record, e := workRecord(requirement)
		if e != nil {
			return nil, e
		}
		record["objectType"] = "requirement"
		out = append(out, record)
	}
	rows, err = a.db.QueryContext(ctx, `SELECT id,code,title,description,steps,actual,expected,environment,found_version AS foundVersion,fix_version AS fixVersion,severity,priority,status,assignee,assignee_user_id AS assigneeUserId,verifier,verifier_user_id AS verifierUserId,sprint,discipline,progress,estimated_hours AS estimatedHours,actual_hours AS actualHours,requirement_id AS requirementId,tags,created_at AS createdAt,updated_at AS updatedAt FROM defects WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) ORDER BY id`, tenantID, a.pid(), first, second)
	if err != nil {
		return nil, err
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return nil, err
	}
	bugs := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			rows.Close()
			return nil, err
		}
		record := map[string]any{"objectType": "defect"}
		for i, key := range columns {
			record[key] = values[i]
		}
		bugs = append(bugs, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, record := range bugs {
		custom, e := a.checkedWorkCustomFields(ctx, "defect", record["id"].(int64))
		if e != nil {
			return nil, e
		}
		record["customFields"] = custom
		out = append(out, record)
	}
	// 每类工作项批量计算当前用户关联，避免每行新增一次关系查询。
	for _, object := range []string{"requirement", "defect"} {
		table := map[string]string{"requirement": "requirements", "defect": "defects"}[object]
		predicate, args := workItemRelatedSQL(object, table, a.uid())
		args = append([]any{tenantID, a.pid(), first, second}, args...)
		rows, err := a.db.QueryContext(ctx, "SELECT id FROM "+table+" WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) AND "+predicate, args...)
		if err != nil {
			return nil, err
		}
		ids := map[string]bool{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids[fmt.Sprint(id)] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, record := range out {
			if record["objectType"] == object {
				record["relatedToMe"] = ids[fmt.Sprint(record["id"])]
			}
		}
	}
	return out, nil
}
func (a *App) checkedWorkCustomFields(ctx context.Context, object string, id int64) (map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT d.key,v.value_json FROM field_values v JOIN field_definitions d ON d.id=v.field_definition_id AND d.tenant_id=v.tenant_id AND d.project_id=v.project_id AND d.object_type=v.object_type AND d.deleted_at='' WHERE v.tenant_id=? AND v.project_id=? AND v.object_type=? AND v.object_id=?`, tenantID, a.pid(), object, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var key, raw string
		if err = rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var value any
		if err = json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}
func (a *App) backlogWorkItems(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	items, err := a.sprintWorkItems(r.Context(), "待规划")
	if err != nil {
		fail(w, 503, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": items})
}
