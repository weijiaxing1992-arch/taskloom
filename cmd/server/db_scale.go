package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// 限制单批占位符和中间结果，不擅自截断未启用分页的旧接口结果。
const requirementHydrationBatch = 400

func (a *App) migrateDatabaseScale() error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range databaseScaleIndexes {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 新索引建立后更新规划器统计；PRAGMA optimize 自行限制分析工作量。
	_, err = a.db.Exec(`PRAGMA optimize`)
	return err
}

// 按现有 SQL 的租户、项目等值前缀建立索引；不替代权限检查。
// 创建在同一事务中，失败回滚，重复启动不重建或删除业务数据。
var databaseScaleIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_req_scope_sprint_id ON requirements(tenant_id,project_id,sprint,id)`,
	`CREATE INDEX IF NOT EXISTS idx_req_scope_category_id ON requirements(tenant_id,project_id,category,id)`,
	`CREATE INDEX IF NOT EXISTS idx_defect_scope_sprint_id ON defects(tenant_id,project_id,sprint,id)`,
	`CREATE INDEX IF NOT EXISTS idx_notification_recipient_timeline ON user_notifications(tenant_id,recipient_user_id,created_at DESC,id DESC)`,
}

type requirementBatchReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// 批量替代逐条人员/自定义字段读取。历史绑定可能已经离职/退出项目，
// 因而姓名解析不限制 active 或项目成员资格，但仍严格限定租户。
// 自定义字段三重关联同时校验租户、项目、对象类型；已删字段不回显。
func (a *App) hydrateRequirementBatch(ctx context.Context, query requirementBatchReader, items []Requirement) error {
	if len(items) == 0 {
		return nil
	}
	userIDs := []string{}
	seen := map[string]bool{}
	for i := range items {
		x := &items[i]
		x.AssigneeUserIDs = requirementPeopleIDs(x.AssigneeUserIDs, x.AssigneeUserID)
		x.OwnerUserIDs = requirementPeopleIDs(x.OwnerUserIDs, x.OwnerUserID)
		for _, ids := range [][]string{x.AssigneeUserIDs, x.OwnerUserIDs} {
			for _, id := range ids {
				if !seen[id] {
					seen[id] = true
					userIDs = append(userIDs, id)
				}
			}
		}
	}
	names := map[string]string{}
	for start := 0; start < len(userIDs); start += requirementHydrationBatch {
		batch := userIDs[start:min(start+requirementHydrationBatch, len(userIDs))]
		args := []any{tenantID}
		for _, id := range batch {
			args = append(args, id)
		}
		rows, err := query.QueryContext(ctx, `SELECT id,name FROM users WHERE tenant_id=? AND id IN (`+scalePlaceholders(len(batch))+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				return err
			}
			names[id] = name
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for i := range items {
		x := &items[i]
		x.Assignees = requirementNamedPeople(x.AssigneeUserIDs, names)
		x.Owners = requirementNamedPeople(x.OwnerUserIDs, names)
		if len(x.Assignees) > 0 {
			x.AssigneeUserID, x.Assignee = x.Assignees[0].ID, x.Assignees[0].Name
		}
		if len(x.Owners) > 0 {
			x.OwnerUserID, x.Owner = x.Owners[0].ID, x.Owners[0].Name
		}
		x.CustomFields = map[string]any{}
	}
	for start := 0; start < len(items); start += requirementHydrationBatch {
		batch := items[start:min(start+requirementHydrationBatch, len(items))]
		args := []any{tenantID, a.pid()}
		byID := map[int64]*Requirement{}
		for i := range batch {
			args = append(args, batch[i].ID)
			byID[batch[i].ID] = &batch[i]
		}
		rows, err := query.QueryContext(ctx, `SELECT v.object_id,d.key,v.value_json FROM field_values v JOIN field_definitions d ON d.id=v.field_definition_id AND d.tenant_id=v.tenant_id AND d.project_id=v.project_id AND d.object_type=v.object_type AND d.deleted_at='' WHERE v.tenant_id=? AND v.project_id=? AND v.object_type='requirement' AND v.object_id IN (`+scalePlaceholders(len(batch))+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var key, raw string
			if err = rows.Scan(&id, &key, &raw); err != nil {
				rows.Close()
				return err
			}
			var value any
			if err = json.Unmarshal([]byte(raw), &value); err != nil {
				rows.Close()
				return fmt.Errorf("invalid stored custom-field JSON: %w", err)
			}
			if x := byID[id]; x != nil {
				x.CustomFields[key] = value
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func scalePlaceholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func requirementPeopleIDs(ids []string, primary string) []string {
	if len(ids) == 0 && primary != "" {
		return []string{primary}
	}
	if ids == nil {
		return []string{}
	}
	return ids
}

func requirementNamedPeople(ids []string, names map[string]string) []RequirementAssignee {
	people := make([]RequirementAssignee, 0, len(ids))
	for _, id := range ids {
		name := names[id]
		if name == "" {
			name = id
		}
		people = append(people, RequirementAssignee{ID: id, Name: name})
	}
	return people
}

type requirementPage struct {
	enabled      bool
	number, size int
	projection   string
}

func parseRequirementPage(r *http.Request) (requirementPage, error) {
	values := r.URL.Query()
	page := requirementPage{enabled: values.Has("page") || values.Has("pageSize"), number: 1, size: 30, projection: values.Get("projection")}
	if !validChoice(page.projection, []string{"", "list", "reference"}) {
		return page, workQueryValidationError("筛选条件格式无效")
	}
	for _, entry := range []struct {
		key  string
		dest *int
		max  int
	}{{"page", &page.number, 100000}, {"pageSize", &page.size, 200}} {
		if values.Has(entry.key) {
			value, err := strconv.Atoi(values.Get(entry.key))
			if err != nil || value < 1 || value > entry.max {
				return page, workQueryValidationError("筛选条件格式无效")
			}
			*entry.dest = value
		}
	}
	return page, nil
}

func (p requirementPage) selectColumns() string {
	if p.projection != "" {
		// 列表/父需求候选不渲染富文档；详情始终独立鉴权 GET。
		// 保留纯文本字段供现有高级筛选和 CSV 使用，不先截断再过滤。
		return strings.Replace(requirementSelectColumns, "description_doc_json", "NULL", 1)
	}
	return requirementSelectColumns
}

func (p requirementPage) response(items []Requirement) map[string]any {
	total := len(items)
	if p.enabled {
		p.number = min(p.number, max(1, (total+p.size-1)/p.size))
		start := (p.number - 1) * p.size
		items = items[start:min(start+p.size, total)]
	}
	response := map[string]any{"items": items, "total": total}
	if p.projection == "reference" {
		references := make([]map[string]any, 0, len(items))
		for _, x := range items {
			references = append(references, map[string]any{"id": x.ID, "code": x.Code, "title": x.Title, "parentId": x.ParentID, "status": x.Status, "category": x.Category, "sprint": x.Sprint})
		}
		response["items"] = references
	}
	if p.enabled {
		response["page"], response["pageSize"] = p.number, p.size
	}
	// 注意：目前只减少网络与浏览器数据量，高级筛选/排序仍在完整作用域
	// 数据上计算。后续 SQL 下推必须验证类型、时区、NULL 和历史人员语义。
	return response
}

// 只把实际参与筛选/排序的字段转换为既有查询引擎的 JSON 类型。
// 旧实现会复制整份富文本、人员快照等，2 万条时造成大量瞬时分配。
// 仍使用相同 JSON 数值/null/数组规范，避免直接类型转换改变历史语义。
func requirementQueryRecord(x Requirement, query *workItemQuery) (map[string]any, error) {
	keys := []string{query.Sort}
	for _, filter := range query.Filters {
		keys = append(keys, filter.Field)
	}
	record := map[string]any{"id": x.ID}
	for _, key := range keys {
		switch {
		case strings.HasPrefix(key, "cf."):
			record["customFields"] = x.CustomFields
		case strings.HasPrefix(key, "role."):
			record["roleWeights"] = x.RoleWeights
		case key == "owner":
			record["owner"], record["ownerUserIds"], record["ownerUserId"] = x.Owner, x.OwnerUserIDs, x.OwnerUserID
		case key == "assignee":
			record["assignee"], record["assigneeUserIds"], record["assigneeUserId"] = x.Assignee, x.AssigneeUserIDs, x.AssigneeUserID
		default:
			switch key {
			case "code":
				record[key] = x.Code
			case "title":
				record[key] = x.Title
			case "type":
				record[key] = x.Type
			case "category":
				record[key] = x.Category
			case "sprint":
				record[key] = x.Sprint
			case "status":
				record[key] = x.Status
			case "priority":
				record[key] = x.Priority
			case "discipline":
				record[key] = x.Discipline
			case "remarks":
				record[key] = x.Remarks
			case "description":
				record[key] = x.Description
			case "acceptance":
				record[key] = x.Acceptance
			case "parentId":
				record[key] = x.ParentID
			case "progress":
				record[key] = x.Progress
			case "estimatedHours":
				record[key] = x.EstimatedHours
			case "actualHours":
				record[key] = x.ActualHours
			case "weightTotal":
				record[key] = x.WeightTotal
			case "startDate":
				record[key] = x.StartDate
			case "endDate":
				record[key] = x.EndDate
			case "createdAt":
				record[key] = x.CreatedAt
			case "updatedAt":
				record[key] = x.UpdatedAt
			case "sensitive":
				record[key] = x.Sensitive
			case "authImpact":
				record[key] = x.AuthImpact
			case "tags":
				record[key] = x.Tags
			default:
				return nil, fmt.Errorf("unsupported requirement query field: %s", key)
			}
		}
	}
	return workRecord(record)
}
