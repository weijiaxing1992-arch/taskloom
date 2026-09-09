package main

import (
	"fmt"
	"time"
)

// migrateRequirementEditorFields retires fields that are no longer part of the
// requirement editor without deleting historical values already stored in items.
func (a *App) migrateRequirementEditorFields() error {
	_, err := a.db.Exec(`
		UPDATE field_definitions
		SET enabled = 0, updated_at = ?
		WHERE object_type = 'requirement'
		  AND enabled = 1
		  AND (key = 'customer_type' OR TRIM(name) IN ('客户类型', '11'))
	`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("停用需求编辑器已废弃字段: %w", err)
	}
	return nil
}
