package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Query field names are a closed registry, never interpolated into SQL. Filtering
// and stable sorting run over the complete project-scoped result before paging.
type workItemFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}
type workItemQuery struct {
	Fields      map[string]string
	Filters     []workItemFilter
	Sort, Order string
	Timezone    *time.Location
}
type workQueryValidationError string

func (e workQueryValidationError) Error() string { return string(e) }
func failWorkQuery(w http.ResponseWriter, err error) {
	var validation workQueryValidationError
	if errors.As(err, &validation) {
		fail(w, 422, "invalid_list_query", err.Error())
	} else {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
	}
}
func baseWorkQueryFields() map[string]string {
	fields := map[string]string{}
	for _, key := range []string{"code", "title", "type", "category", "sprint", "status", "priority", "discipline", "remarks", "description", "acceptance"} {
		fields[key] = "text"
	}
	for _, key := range []string{"parentId", "progress", "estimatedHours", "actualHours", "weightTotal"} {
		fields[key] = "number"
	}
	for _, key := range []string{"startDate", "endDate", "createdAt", "updatedAt"} {
		fields[key] = "date"
	}
	for _, key := range []string{"sensitive", "authImpact"} {
		fields[key] = "boolean"
	}
	fields["owner"] = "person"
	fields["assignee"] = "person"
	fields["tags"] = "multi"
	for _, role := range requirementWeightRoles {
		fields["role."+role+".value"] = "number"
		fields["role."+role+".userId"] = "person"
	}
	return fields
}
func (a *App) parseRequirementListQuery(r *http.Request) (*workItemQuery, error) {
	query := &workItemQuery{Fields: baseWorkQueryFields(), Sort: r.URL.Query().Get("sort"), Order: r.URL.Query().Get("order")}
	rows, err := a.db.QueryContext(r.Context(), `SELECT key,type FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND enabled=1`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key, kind string
		if err = rows.Scan(&key, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		switch kind {
		case "number", "date", "boolean":
		case "multi_select", "users":
			kind = "multi"
		default:
			kind = "text"
		}
		query.Fields["cf."+key] = kind
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	raw := r.URL.Query().Get("filters")
	if len(raw) > 32768 {
		return nil, workQueryValidationError("筛选条件过长")
	}
	if raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&query.Filters) != nil {
			return nil, workQueryValidationError("筛选条件格式无效")
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return nil, workQueryValidationError("筛选条件格式无效")
		}
	}
	if len(query.Filters) > 30 {
		return nil, workQueryValidationError("最多 30 个筛选条件")
	}
	for _, filter := range query.Filters {
		if err = validateWorkFilter(filter, query.Fields); err != nil {
			return nil, err
		}
	}
	for _, filter := range query.Filters {
		if query.Fields[filter.Field] == "date" {
			var timezone string
			if err := a.db.QueryRowContext(r.Context(), `SELECT timezone FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&timezone); err != nil {
				return nil, err
			}
			query.Timezone, err = time.LoadLocation(timezone)
			if err != nil {
				query.Timezone = time.UTC
			}
			break
		}
	}
	if query.Sort == "" {
		query.Sort = "updatedAt"
	}
	if _, ok := query.Fields[query.Sort]; !ok {
		return nil, workQueryValidationError("排序字段无效或已停用")
	}
	if query.Order == "" {
		query.Order = "desc"
	}
	if query.Order != "asc" && query.Order != "desc" {
		return nil, workQueryValidationError("排序方向须为 asc 或 desc")
	}
	return query, nil
}
func validateWorkFilter(filter workItemFilter, fields map[string]string) error {
	kind, ok := fields[filter.Field]
	if !ok {
		return workQueryValidationError("筛选字段无效或已停用")
	}
	op := filter.Operator
	if op == "is_empty" || op == "not_empty" {
		if filter.Value != nil {
			return workQueryValidationError("空值条件不能携带筛选值")
		}
		return nil
	}
	allowed := map[string]bool{"eq": true, "neq": true}
	switch kind {
	case "number", "date":
		for _, op := range []string{"gt", "gte", "lt", "lte"} {
			allowed[op] = true
		}
	case "person", "multi":
		allowed = map[string]bool{"includes": true, "not_includes": true}
	case "text":
		allowed["contains"] = true
		allowed["not_contains"] = true
	}
	if !allowed[op] {
		return workQueryValidationError("筛选条件不适用于此字段")
	}
	switch kind {
	case "number":
		value, valid := filter.Value.(float64)
		if !valid || math.IsNaN(value) || math.IsInf(value, 0) {
			return workQueryValidationError("数值筛选须使用有效数字")
		}
	case "boolean":
		if _, valid := filter.Value.(bool); !valid {
			return workQueryValidationError("布尔筛选须使用是或否")
		}
	default:
		value, valid := filter.Value.(string)
		if !valid || strings.TrimSpace(value) == "" || len(value) > 4000 {
			return workQueryValidationError("筛选值须为非空文本")
		}
		if kind == "date" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return workQueryValidationError("日期筛选须为有效日期")
			}
		}
	}
	return nil
}
func workRecord(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	record := map[string]any{}
	err = json.Unmarshal(raw, &record)
	return record, err
}
func workStrings(value any) []string {
	out := []string{}
	if values, ok := value.([]any); ok {
		for _, v := range values {
			if str, ok := v.(string); ok && str != "" {
				out = append(out, str)
			}
		}
	}
	return out
}
func workValue(record map[string]any, key string) any {
	if strings.HasPrefix(key, "cf.") {
		fields, _ := record["customFields"].(map[string]any)
		return fields[strings.TrimPrefix(key, "cf.")]
	}
	if strings.HasPrefix(key, "role.") {
		parts := strings.Split(key, ".")
		weights, _ := record["roleWeights"].(map[string]any)
		role, _ := weights[parts[1]].(map[string]any)
		if parts[2] == "value" {
			return role["value"]
		}
		ids := workStrings(role["userIds"])
		if len(ids) == 0 {
			if id, ok := role["userId"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	if key == "owner" || key == "assignee" {
		ids := workStrings(record[key+"UserIds"])
		if len(ids) == 0 {
			if id, ok := record[key+"UserId"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			if name, ok := record[key].(string); ok && name != "" {
				ids = append(ids, "legacy:"+name)
			}
		}
		return ids
	}
	if key == "tags" {
		tags, _ := record[key].(string)
		parts := strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == '，' || r == '\n' })
		out := []string{}
		for _, part := range parts {
			if tag := strings.TrimSpace(part); tag != "" {
				out = append(out, tag)
			}
		}
		return out
	}
	return record[key]
}
func emptyWorkValue(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}
func compareWorkValue(a, b any, kind string) int {
	if kind == "number" {
		x, _ := a.(float64)
		y, _ := b.(float64)
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	}
	if kind == "boolean" {
		x, _ := a.(bool)
		y, _ := b.(bool)
		if x == y {
			return 0
		}
		if x {
			return 1
		}
		return -1
	}
	text := func(value any) string {
		switch v := value.(type) {
		case []string:
			return strings.Join(v, "\x00")
		case []any:
			parts := []string{}
			for _, p := range v {
				parts = append(parts, fmt.Sprint(p))
			}
			return strings.Join(parts, "\x00")
		case string:
			return strings.ToLower(v)
		default:
			return fmt.Sprint(v)
		}
	}
	return strings.Compare(text(a), text(b))
}
func matchesWorkItem(record map[string]any, filter workItemFilter, kind string, timezone *time.Location) bool {
	value := workValue(record, filter.Field)
	empty := emptyWorkValue(value)
	if filter.Operator == "is_empty" {
		return empty
	}
	if filter.Operator == "not_empty" {
		return !empty
	}
	if empty {
		return false
	}
	if filter.Operator == "includes" || filter.Operator == "not_includes" {
		found := false
		switch values := value.(type) {
		case []string:
			for _, v := range values {
				if v == filter.Value {
					found = true
				}
			}
		case []any:
			for _, v := range values {
				if v == filter.Value {
					found = true
				}
			}
		}
		if filter.Operator == "not_includes" {
			return !found
		}
		return found
	}
	if kind == "date" {
		if text, ok := value.(string); ok && len(text) >= 10 {
			if instant, err := time.Parse(time.RFC3339Nano, text); err == nil && timezone != nil {
				value = instant.In(timezone).Format("2006-01-02")
			} else {
				value = text[:10]
			}
		}
	}
	compared := compareWorkValue(value, filter.Value, kind)
	switch filter.Operator {
	case "eq":
		return compared == 0
	case "neq":
		return compared != 0
	case "gt":
		return compared > 0
	case "gte":
		return compared >= 0
	case "lt":
		return compared < 0
	case "lte":
		return compared <= 0
	case "contains":
		return strings.Contains(strings.ToLower(fmt.Sprint(value)), strings.ToLower(fmt.Sprint(filter.Value)))
	case "not_contains":
		return !strings.Contains(strings.ToLower(fmt.Sprint(value)), strings.ToLower(fmt.Sprint(filter.Value)))
	}
	return false
}
func workSortValue(record map[string]any, key string, names map[string]string) any {
	if key == "code" {
		return record["id"]
	}
	value := workValue(record, key)
	if key == "owner" || key == "assignee" || strings.HasSuffix(key, ".userId") {
		ids, _ := value.([]string)
		out := []string{}
		for _, id := range ids {
			name := names[id]
			if name == "" {
				name = strings.TrimPrefix(id, "legacy:")
			}
			out = append(out, name)
		}
		return out
	}
	return value
}
func (a *App) applyRequirementListQuery(items []Requirement, query *workItemQuery) ([]Requirement, error) {
	type entry struct {
		item   Requirement
		record map[string]any
	}
	entries := []entry{}
	for _, item := range items {
		record, err := requirementQueryRecord(item, query)
		if err != nil {
			return nil, err
		}
		matches := true
		for _, filter := range query.Filters {
			if !matchesWorkItem(record, filter, query.Fields[filter.Field], query.Timezone) {
				matches = false
				break
			}
		}
		if matches {
			entries = append(entries, entry{item, record})
		}
	}
	names := map[string]string{}
	if query.Fields[query.Sort] == "person" {
		rows, err := a.db.Query(`SELECT id,name FROM users WHERE tenant_id=?`, tenantID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				return nil, err
			}
			names[id] = name
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		x, y := workSortValue(left.record, query.Sort, names), workSortValue(right.record, query.Sort, names)
		if emptyWorkValue(x) != emptyWorkValue(y) {
			return !emptyWorkValue(x)
		}
		kind := query.Fields[query.Sort]
		if query.Sort == "code" {
			kind = "number"
		}
		comparison := compareWorkValue(x, y, kind)
		if comparison == 0 {
			return left.item.ID < right.item.ID
		}
		if query.Order == "desc" {
			return comparison > 0
		}
		return comparison < 0
	})
	result := make([]Requirement, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.item)
	}
	return result, nil
}
