package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func integrationRevisionTestApp(t *testing.T) *App {
	t.Helper()
	a := fileSQLiteTestApp(t)
	if err := a.migrateIntegrationRevisions(); err != nil {
		t.Fatal(err)
	}
	return a
}

func integrationTestETag(t *testing.T, a *App, resource string, id int64) string {
	t.Helper()
	etag, err := a.integrationEntityETag(context.Background(), a.db, resource, id)
	if err != nil {
		t.Fatal(err)
	}
	return etag
}

func TestIntegrationRevisionsObserveSameSecondBrowserWritesAndRestart(t *testing.T) {
	a := integrationRevisionTestApp(t)
	for _, test := range []struct{ resource, statement string }{
		{"requirements", `UPDATE requirements SET title=title||' UI',updated_at='2026-09-05T00:00:00Z' WHERE id=1`},
		{"sprints", `UPDATE sprints SET goal=goal||' UI',updated_at='2026-09-05T00:00:00Z' WHERE id=1`},
		{"defects", `UPDATE defects SET title=title||' UI',updated_at='2026-09-05T00:00:00Z' WHERE id=1`},
		{"test-cases", `UPDATE test_cases SET title=title||' UI',updated_at='2026-09-05T00:00:00Z' WHERE id=1`},
		{"test-executions", `UPDATE test_executions SET note=note||' UI',updated_at='2026-09-05T00:00:00Z' WHERE id=1`},
	} {
		t.Run(test.resource, func(t *testing.T) {
			first := integrationTestETag(t, a, test.resource, 1)
			if _, err := a.db.Exec(test.statement); err != nil {
				t.Fatal(err)
			}
			second := integrationTestETag(t, a, test.resource, 1)
			if _, err := a.db.Exec(test.statement); err != nil {
				t.Fatal(err)
			}
			third := integrationTestETag(t, a, test.resource, 1)
			if first == second || second == third {
				t.Fatalf("same-second writes reused an ETag: %s %s %s", first, second, third)
			}
			if err := a.migrateIntegrationRevisions(); err != nil {
				t.Fatal(err)
			}
			if got := integrationTestETag(t, a, test.resource, 1); got != third {
				t.Fatalf("migration reset revision: %s -> %s", third, got)
			}
		})
	}
	foreign := *a
	foreign.project = "not-the-entity-project"
	if _, err := foreign.integrationEntityETag(context.Background(), a.db, "requirements", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign project version lookup: %v", err)
	}
}

func TestIntegrationRevisionsObserveCustomFieldsAndCaseMetadataAtomically(t *testing.T) {
	a := integrationRevisionTestApp(t)
	previous := integrationTestETag(t, a, "requirements", 1)
	for _, statement := range []string{
		`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at) VALUES('tn_acme','prj_orbit','requirement',1,999999,'"first"','2026-09-05T00:00:00Z')`,
		`UPDATE field_values SET value_json='"second"' WHERE field_definition_id=999999`,
		`DELETE FROM field_values WHERE field_definition_id=999999`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		next := integrationTestETag(t, a, "requirements", 1)
		if next == previous {
			t.Fatalf("custom-field mutation did not invalidate %s", previous)
		}
		previous = next
	}
	for _, statement := range []string{
		`INSERT INTO testing_case_metadata(tenant_id,project_id,case_id,description,updated_at) VALUES('tn_acme','prj_orbit',1,'metadata change','2026-09-05T00:00:00Z') ON CONFLICT(tenant_id,project_id,case_id) DO UPDATE SET description=excluded.description`,
		`UPDATE testing_case_locations SET folder_id=folder_id+1 WHERE tenant_id='tn_acme' AND project_id='prj_orbit' AND case_id=1`,
	} {
		before := integrationTestETag(t, a, "test-cases", 1)
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if next := integrationTestETag(t, a, "test-cases", 1); before == next {
			t.Fatalf("case metadata mutation did not invalidate %s", before)
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE requirements SET title='will roll back' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if current := integrationTestETag(t, a, "requirements", 1); current != previous {
		t.Fatalf("rolled-back business write committed its revision: %s -> %s", previous, current)
	}
}

func TestIntegrationPatchPreconditionsProtectEveryResource(t *testing.T) {
	for _, test := range []struct{ resource, body string }{
		{"requirements", `{"title":"Integration requirement"}`},
		{"sprints", `{"goal":"Integration sprint goal"}`},
		{"defects", `{"title":"Integration defect"}`},
		{"test-cases", `{"title":"Integration testcase"}`},
		{"test-executions", `{"status":"通过","actualResult":"Integration execution result"}`},
	} {
		t.Run(test.resource, func(t *testing.T) {
			a := integrationRevisionTestApp(t)
			original := integrationTestETag(t, a, test.resource, 1)
			handler := a.apiMux()
			request := func(etag string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPatch, "/api/"+test.resource+"/1", strings.NewReader(test.body))
				r = withIntegrationPrecondition(r, test.resource, 1, etag)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			if w := request(original); w.Code != http.StatusOK {
				t.Fatalf("current revision rejected: %d %s", w.Code, w.Body.String())
			}
			saved := integrationTestETag(t, a, test.resource, 1)
			if original == saved {
				t.Fatal("successful write kept the old revision")
			}
			counts := map[string]int{}
			for _, table := range []string{"activities", "entity_activities", "audit_logs", "user_notifications", "notification_outbox", "test_execution_history"} {
				counts[table] = tableCount(t, a, table)
			}
			w := request(original)
			if w.Code != http.StatusPreconditionFailed || w.Header().Get("ETag") != saved {
				t.Fatalf("stale revision accepted: %d %s", w.Code, w.Body.String())
			}
			if after := integrationTestETag(t, a, test.resource, 1); after != saved {
				t.Fatalf("rejected mutation changed revision: %s -> %s", saved, after)
			}
			for table, before := range counts {
				if after := tableCount(t, a, table); after != before {
					t.Fatalf("rejected mutation changed %s: %d -> %d", table, before, after)
				}
			}
			if w := request(""); w.Code != http.StatusPreconditionRequired {
				t.Fatalf("missing precondition: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestIntegrationConcurrentPatchHasOneWinner(t *testing.T) {
	a := integrationRevisionTestApp(t)
	etag := integrationTestETag(t, a, "requirements", 1)
	handler := a.apiMux()
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	var workers sync.WaitGroup
	for _, body := range []string{`{"title":"Concurrent A"}`, `{"title":"Concurrent B"}`} {
		workers.Add(1)
		go func(body string) {
			defer workers.Done()
			<-start
			r := httptest.NewRequest(http.MethodPatch, "/api/requirements/1", strings.NewReader(body))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, withIntegrationPrecondition(r, "requirements", 1, etag))
			results <- w
		}(body)
	}
	close(start)
	workers.Wait()
	close(results)
	counts := map[int]int{}
	for result := range results {
		counts[result.Code]++
		if result.Code != http.StatusOK && result.Code != http.StatusPreconditionFailed {
			t.Fatalf("concurrent write failed unexpectedly: %d %s", result.Code, result.Body.String())
		}
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusPreconditionFailed] != 1 {
		t.Fatalf("concurrent writes did not have exactly one winner: %v", counts)
	}
}

func TestIntegrationPreconditionDoesNotChangeLegacyRequests(t *testing.T) {
	a := integrationRevisionTestApp(t)
	r := httptest.NewRequest(http.MethodPatch, "/api/requirements/1", strings.NewReader(`{"title":"Legacy browser write"}`))
	r.Header.Set("If-Match", `"stale-client-header"`)
	w := httptest.NewRecorder()
	a.apiMux().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy request unexpectedly required API precondition: %d %s", w.Code, w.Body.String())
	}
	r = withIntegrationPrecondition(httptest.NewRequest(http.MethodPatch, "/api/requirements/1", strings.NewReader(`{}`)), "requirements", 1, `"stale"`)
	w = httptest.NewRecorder()
	a.apiMux().ServeHTTP(w, r)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("empty integration patch skipped precondition: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationRevisionDeletionAndStorageFailureAreDistinct(t *testing.T) {
	a := integrationRevisionTestApp(t)
	const insert = `INSERT INTO sprints(id,tenant_id,project_id,code,name,start_date,end_date,created_at,updated_at)
		VALUES(999999,'tn_acme','prj_orbit','SPR-999999','Revision identity fixture','2026-09-05','2026-09-06','2026-09-05T00:00:00Z','2026-09-05T00:00:00Z')`
	if _, err := a.db.Exec(insert); err != nil {
		t.Fatal(err)
	}
	before := integrationTestETag(t, a, "sprints", 999999)
	if _, err := a.db.Exec(`DELETE FROM sprints WHERE id=999999`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.integrationEntityETag(context.Background(), a.db, "sprints", 999999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted entity remained readable: %v", err)
	}
	if _, err := a.db.Exec(insert); err != nil {
		t.Fatal(err)
	}
	if after := integrationTestETag(t, a, "sprints", 999999); after == before {
		t.Fatalf("reused ID restored stale ETag %s", before)
	}
	for _, test := range []struct {
		name     string
		id       int64
		prepare  string
		wantCode int
	}{
		{"missing", 888888, "", http.StatusNotFound},
		{"storage", 999999, `ALTER TABLE integration_entity_revisions RENAME TO unavailable_revision_test_table`, http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.prepare != "" {
				if _, err := a.db.Exec(test.prepare); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := a.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			w := httptest.NewRecorder()
			r := withIntegrationPrecondition(httptest.NewRequest(http.MethodPatch, "/", nil), "sprints", test.id, before)
			if a.checkIntegrationPrecondition(w, r, tx) || w.Code != test.wantCode || strings.Contains(w.Body.String(), "no such table") {
				t.Fatalf("precondition failure was masked or leaked SQL: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
