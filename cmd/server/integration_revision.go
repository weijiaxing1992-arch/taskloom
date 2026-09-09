package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Revisions belong to stored entities, not clients. Triggers also observe the
// browser, automation rules and administrative edits, including same-second
// writes that cannot be distinguished by the existing updated_at columns.
var integrationRevisionTables = map[string]string{
	"requirements":    "requirements",
	"sprints":         "sprints",
	"defects":         "defects",
	"test-cases":      "test_cases",
	"test-executions": "test_executions",
}

func (a *App) migrateIntegrationRevisions() error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS integration_entity_revisions(
		tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,resource TEXT NOT NULL,
		entity_id INTEGER NOT NULL,revision INTEGER NOT NULL CHECK(revision>0),
		PRIMARY KEY(tenant_id,project_id,resource,entity_id));`); err != nil {
		return err
	}
	for resource, table := range integrationRevisionTables {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO integration_entity_revisions(tenant_id,project_id,resource,entity_id,revision)
			SELECT tenant_id,project_id,?,id,1 FROM `+table, resource); err != nil {
			return err
		}
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW"
			if operation == "DELETE" {
				row = "OLD"
			}
			query := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS integration_revision_%s_%s AFTER %s ON %s BEGIN
				INSERT INTO integration_entity_revisions(tenant_id,project_id,resource,entity_id,revision)
				VALUES(%s.tenant_id,%s.project_id,'%s',%s.id,1)
				ON CONFLICT(tenant_id,project_id,resource,entity_id) DO UPDATE SET revision=revision+1;
			END;`, table, strings.ToLower(operation), operation, table, row, row, resource, row)
			if _, err = tx.Exec(query); err != nil {
				return err
			}
		}
	}
	// A custom-field-only edit still changes the entity. Metadata and library
	// location are likewise part of a testcase's public editable representation.
	for _, source := range []struct{ table, idColumn, resource string }{
		{"field_values", "object_id", ""},
		{"testing_case_metadata", "case_id", "test-cases"},
		{"testing_case_locations", "case_id", "test-cases"},
	} {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			rows := []string{"NEW"}
			if operation == "DELETE" {
				rows = []string{"OLD"}
			} else if operation == "UPDATE" {
				// Moving a field value between entities invalidates both versions.
				rows = []string{"OLD", "NEW"}
			}
			var body strings.Builder
			for _, row := range rows {
				resourceSQL, condition := "'"+source.resource+"'", "1=1"
				if source.table == "field_values" {
					resourceSQL = "CASE " + row + ".object_type WHEN 'requirement' THEN 'requirements' WHEN 'sprint' THEN 'sprints' WHEN 'defect' THEN 'defects' WHEN 'test_case' THEN 'test-cases' WHEN 'test_execution' THEN 'test-executions' END"
					condition = row + ".object_type IN ('requirement','sprint','defect','test_case','test_execution')"
				}
				fmt.Fprintf(&body, `INSERT INTO integration_entity_revisions(tenant_id,project_id,resource,entity_id,revision)
					SELECT %s.tenant_id,%s.project_id,%s,%s.%s,1 WHERE %s
					ON CONFLICT(tenant_id,project_id,resource,entity_id) DO UPDATE SET revision=revision+1;`,
					row, row, resourceSQL, row, source.idColumn, condition)
			}
			query := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS integration_revision_%s_%s AFTER %s ON %s BEGIN %s END;`,
				source.table, strings.ToLower(operation), operation, source.table, body.String())
			if _, err = tx.Exec(query); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (a *App) integrationEntityETag(ctx context.Context, store stateStore, resource string, id int64) (string, error) {
	table, ok := integrationRevisionTables[resource]
	if !ok || id < 1 {
		return "", sql.ErrNoRows
	}
	var revision int64
	err := store.QueryRowContext(ctx, `SELECT v.revision FROM `+table+` e
		JOIN integration_entity_revisions v ON v.tenant_id=e.tenant_id AND v.project_id=e.project_id AND v.entity_id=e.id AND v.resource=?
		WHERE e.tenant_id=? AND e.project_id=? AND e.id=?`, resource, tenantID, a.pid(), id).Scan(&revision)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`"%s:%d:%d"`, resource, id, revision), nil
}

type integrationPreconditionKey struct{}
type integrationPrecondition struct {
	resource string
	id       int64
	etag     string
}

// Only the authenticated integration gateway can install this context marker.
// A browser sending If-Match on a legacy route retains legacy behavior.
func withIntegrationPrecondition(r *http.Request, resource string, id int64, etag string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), integrationPreconditionKey{}, integrationPrecondition{resource, id, etag}))
}

func hasIntegrationPrecondition(r *http.Request) bool {
	_, ok := r.Context().Value(integrationPreconditionKey{}).(integrationPrecondition)
	return ok
}

// Call immediately after beginning the mutation transaction, before any write.
// Production connections use BEGIN IMMEDIATE, so a successful comparison and
// the subsequent mutation share the writer reservation. Never perform this
// check only in gateway middleware, which would leave a read/write race.
func (a *App) checkIntegrationPrecondition(w http.ResponseWriter, r *http.Request, tx *sql.Tx) bool {
	condition, ok := r.Context().Value(integrationPreconditionKey{}).(integrationPrecondition)
	if !ok {
		return true
	}
	if condition.etag == "" {
		fail(w, http.StatusPreconditionRequired, "precondition_required", "更新资源前请先读取当前版本，并提交 If-Match")
		return false
	}
	current, err := a.integrationEntityETag(r.Context(), tx, condition.resource, condition.id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "资源不存在")
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "资源版本暂时无法读取，请稍后重试")
		}
		return false
	}
	if current != condition.etag {
		w.Header().Set("ETag", current)
		fail(w, http.StatusPreconditionFailed, "precondition_failed", "资源已被修改，请重新读取后再提交")
		return false
	}
	return true
}
