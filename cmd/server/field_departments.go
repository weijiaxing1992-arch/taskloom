package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Department constraints are stable directory identities, not display labels.
func (a *App) migrateFieldDepartments() error {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('field_definitions') WHERE name='department_id'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err := a.db.Exec(`ALTER TABLE field_definitions ADD COLUMN department_id TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

type fieldQueryer interface{ QueryRow(string, ...any) *sql.Row }
type customFieldValidationError struct{ message string }

func (e customFieldValidationError) Error() string { return e.message }
func failCustomFieldValidation(w http.ResponseWriter, err error) bool {
	var invalid customFieldValidationError
	if !errors.As(err, &invalid) {
		return false
	}
	fail(w, http.StatusUnprocessableEntity, "custom_field_invalid", invalid.Error())
	return true
}

func fieldPersonIDs(value any, multiple bool) ([]string, bool) {
	if value == nil || value == "" {
		return []string{}, true
	}
	if !multiple {
		id, ok := value.(string)
		return []string{id}, ok
	}
	values, ok := value.([]any)
	if !ok {
		if ids, valid := value.([]string); valid {
			values = make([]any, len(ids))
			for i, id := range ids {
				values[i] = id
			}
			ok = true
		}
	}
	if !ok {
		return nil, false
	}
	ids := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		id, ok := value.(string)
		if !ok || id == "" || id != strings.TrimSpace(id) || len(id) > 128 || seen[id] {
			return nil, false
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, true
}

func (a *App) validateFieldDepartment(q fieldQueryer, d FieldDefinition, previous *FieldDefinition) error {
	if d.DepartmentID == "" {
		return nil
	}
	if d.Type != "user" && d.Type != "users" {
		return fmt.Errorf("仅人员字段可以限定部门")
	}
	var status string
	err := q.QueryRow(`SELECT status FROM departments WHERE tenant_id=? AND id=?`, tenantID, d.DepartmentID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("限定部门不存在或不属于当前企业")
	}
	if err != nil {
		return err
	}
	if status != "active" && (previous == nil || previous.DepartmentID != d.DepartmentID) {
		return fmt.Errorf("限定部门已停用")
	}
	return nil
}

func (a *App) validateFieldPeople(q fieldQueryer, d FieldDefinition, value, previous any) error {
	if d.Type != "user" && d.Type != "users" {
		return nil
	}
	ids, ok := fieldPersonIDs(value, d.Type == "users")
	if !ok || len(ids) > 100 {
		return customFieldValidationError{fmt.Sprintf("%s 人员值格式不正确", d.Name)}
	}
	oldIDs, _ := fieldPersonIDs(previous, d.Type == "users")
	saved := map[string]bool{}
	for _, id := range oldIDs {
		saved[id] = true
	}
	for _, id := range ids {
		// Keep explicit, unchanged historical bindings (including former names).
		// They never grant permission to add a different unavailable identity.
		if saved[id] {
			continue
		}
		if id == "" || id != strings.TrimSpace(id) || len(id) > 128 {
			return customFieldValidationError{fmt.Sprintf("%s 人员值格式不正确", d.Name)}
		}
		var count int
		err := q.QueryRow(`SELECT COUNT(*) FROM users u JOIN memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id AND m.project_id=? JOIN project_members pm ON pm.tenant_id=m.tenant_id AND pm.project_id=m.project_id AND pm.user_id=m.user_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active' AND (?='' OR EXISTS(SELECT 1 FROM department_memberships dm JOIN departments dep ON dep.tenant_id=dm.tenant_id AND dep.id=dm.department_id AND dep.status='active' WHERE dm.tenant_id=u.tenant_id AND dm.user_id=u.id AND dm.department_id=? AND dm.status='active'))`, a.pid(), tenantID, id, d.DepartmentID, d.DepartmentID).Scan(&count)
		if err != nil {
			return err
		}
		if count != 1 {
			return customFieldValidationError{fmt.Sprintf("%s 的新增成员必须为当前项目可用成员且属于限定部门", d.Name)}
		}
		if err := a.validateFieldMemberRole(q, d, id); err != nil {
			return err
		}
	}
	return nil
}

// Re-read both definition and saved values inside the business transaction.
// Concurrent membership/definition changes cannot bypass the constraint.
func (a *App) validateObjectFieldWrite(tx *sql.Tx, object string, id int64, field requirementFieldWrite) error {
	var d FieldDefinition
	var options, previous string
	err := tx.QueryRow(`SELECT name,type,required,options,department_id,key,object_type FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type=? AND id=? AND enabled=1`, tenantID, a.pid(), object, field.definitionID).Scan(&d.Name, &d.Type, &d.Required, &options, &d.DepartmentID, &d.Key, &d.ObjectType)
	if errors.Is(err, sql.ErrNoRows) {
		return customFieldValidationError{"字段不存在或已停用，请刷新后重试"}
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(options), &d.Options); err != nil {
		return err
	}
	if err = validateFieldValue(d, field.value); err != nil {
		return customFieldValidationError{err.Error()}
	}
	err = tx.QueryRow(`SELECT value_json FROM field_values WHERE tenant_id=? AND project_id=? AND object_type=? AND object_id=? AND field_definition_id=?`, tenantID, a.pid(), object, id, field.definitionID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var old any
	if previous != "" {
		if err = json.Unmarshal([]byte(previous), &old); err != nil {
			return err
		}
	}
	return a.validateFieldPeople(tx, d, field.value, old)
}

func (a *App) fieldDepartments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,COALESCE(parent_id,''),status FROM departments WHERE tenant_id=? ORDER BY sort_order,name,id`, tenantID)
	if err != nil {
		fail(w, 503, "database_unavailable", "部门目录暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, name, parent, status string
		if err = rows.Scan(&id, &name, &parent, &status); err != nil {
			fail(w, 503, "database_unavailable", "部门目录暂时无法读取，请稍后重试")
			return
		}
		items = append(items, map[string]string{"id": id, "name": name, "parentId": parent, "status": status})
	}
	if rows.Err() != nil {
		fail(w, 503, "database_unavailable", "部门目录暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": items})
}

func (a *App) addMemberDepartmentIDs(items []map[string]any) error {
	byID := map[string]map[string]any{}
	for _, item := range items {
		id, _ := item["id"].(string)
		byID[id] = item
		item["departmentIds"] = []string{}
		item["departmentNames"] = []string{}
	}
	rows, err := a.db.Query(`SELECT dm.user_id,d.id,d.name FROM department_memberships dm JOIN departments d ON d.tenant_id=dm.tenant_id AND d.id=dm.department_id AND d.status='active' JOIN memberships m ON m.tenant_id=dm.tenant_id AND m.user_id=dm.user_id AND m.project_id=? WHERE dm.tenant_id=? AND dm.status='active' ORDER BY dm.is_primary DESC,d.sort_order,d.id`, a.pid(), tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user, id, name string
		if err = rows.Scan(&user, &id, &name); err != nil {
			return err
		}
		if item := byID[user]; item != nil {
			item["departmentIds"] = append(item["departmentIds"].([]string), id)
			item["departmentNames"] = append(item["departmentNames"].([]string), name)
		}
	}
	return rows.Err()
}
