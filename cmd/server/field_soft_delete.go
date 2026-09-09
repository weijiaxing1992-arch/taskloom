package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Tombstones keep stable keys and historical values reserved. Seeds and preset
// installation use INSERT OR IGNORE/existence checks, so they cannot revive them.
func (a *App) migrateFieldSoftDelete() error {
	var exists int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('field_definitions') WHERE name='deleted_at'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err := a.db.Exec(`ALTER TABLE field_definitions ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

func failFieldMutation(w http.ResponseWriter, err error) {
	var organization *organizationError
	var state stateError
	switch {
	case errors.As(err, &organization):
		failOrganization(w, err)
	case errors.As(err, &state):
		failState(w, err)
	default:
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
	}
}

func (a *App) beginFieldConfigurationWrite(r *http.Request) (*sql.Tx, error) {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	// Take the writer before checking the live actor's permission, not a stale
	// capability loaded by another tab before a membership was revoked.
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err == nil {
		err = a.requireOperationAccess(r.Context(), tx)
	}
	if err == nil {
		var role string
		role, err = a.requirementStateRole(r.Context(), tx)
		if err == nil && !stateManager(role) {
			err = &organizationError{403, "admin_required", "仅管理员可执行此操作"}
		}
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (a *App) deleteFieldDefinition(w http.ResponseWriter, r *http.Request, id int64) {
	var confirmation struct {
		ConfirmKey string `json:"confirmKey"`
		ObjectType string `json:"objectType"`
	}
	if !decodePreference(w, r, &confirmation) {
		return
	}
	if confirmation.ConfirmKey == "" || !validChoice(confirmation.ObjectType, []string{"requirement", "defect", "test_case", "sprint"}) {
		fail(w, 422, "validation_error", "删除确认与当前字段不符，请重新打开确认框")
		return
	}
	tx, err := a.beginFieldConfigurationWrite(r)
	if err != nil {
		failFieldMutation(w, err)
		return
	}
	defer tx.Rollback()
	var d FieldDefinition
	var rawDefault, rawOptions string
	d.ID = id
	err = tx.QueryRowContext(r.Context(), `SELECT object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id FROM field_definitions WHERE tenant_id=? AND project_id=? AND id=? AND deleted_at=''`, tenantID, a.pid(), id).Scan(&d.ObjectType, &d.Key, &d.Name, &d.Type, &d.Description, &d.Required, &d.Searchable, &d.Filterable, &d.ListVisible, &d.Enabled, &d.SortOrder, &rawDefault, &rawOptions, &d.DepartmentID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "字段不存在")
		return
	}
	if err != nil {
		failFieldMutation(w, err)
		return
	}
	if d.Key != confirmation.ConfirmKey || d.ObjectType != confirmation.ObjectType {
		fail(w, 409, "field_changed", "删除确认与当前字段不符，请重新打开确认框")
		return
	}
	if json.Unmarshal([]byte(rawDefault), &d.DefaultValue) != nil || json.Unmarshal([]byte(rawOptions), &d.Options) != nil {
		failFieldMutation(w, errors.New("invalid stored field definition"))
		return
	}
	var preserved int64
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM field_values WHERE tenant_id=? AND project_id=? AND field_definition_id=?`, tenantID, a.pid(), id).Scan(&preserved); err != nil {
		failFieldMutation(w, err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(r.Context(), `UPDATE field_definitions SET deleted_at=?,enabled=0,searchable=0,filterable=0,list_visible=0,updated_at=? WHERE tenant_id=? AND project_id=? AND id=? AND deleted_at=''`, now, now, tenantID, a.pid(), id)
	if err != nil {
		failFieldMutation(w, err)
		return
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		failFieldMutation(w, errors.New("field deletion did not affect exactly one row"))
		return
	}
	after := map[string]any{"deletedAt": now, "enabled": false, "searchable": false, "filterable": false, "listVisible": false, "preservedValueCount": preserved}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'field_definition',?,'soft_delete',?,?,?)`, tenantID, a.pid(), a.uid(), fmt.Sprint(id), jsonText(d), jsonText(after), now); err != nil {
		failFieldMutation(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failFieldMutation(w, err)
		return
	}
	write(w, 200, map[string]any{"deleted": true, "id": id, "preservedValueCount": preserved})
}
