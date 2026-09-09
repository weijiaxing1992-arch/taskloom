package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMigrateExistingDatabaseNeverReseedsUserWork(t *testing.T) {
	a := fileSQLiteTestApp(t)
	x := planningRequirement(t, a, `{"title":"重启后仍待指派","assignee":"","progress":0,"estimatedHours":0,"actualHours":0,"discipline":"frontend","remarks":"用户填写备注","roleWeights":{"frontend":{"value":12}},"tagColors":{"正式":"#123456"}}`)
	if _, err := a.db.Exec(`DELETE FROM field_values WHERE object_type='requirement' AND object_id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	before, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPost, "/api/defects", "u_admin", projectID, `{"title":"缺陷保持待指派","assignee":"","progress":0,"estimatedHours":0,"actualHours":0}`)
	if w.Code != 201 {
		t.Fatalf("defect create failed: %d %s", w.Code, w.Body.String())
	}
	var defect Defect
	if err := json.Unmarshal(w.Body.Bytes(), &defect); err != nil {
		t.Fatal(err)
	}
	planIDs := []int64{}
	for _, body := range []string{`{"name":"仅一个用例","caseIds":[1]}`, `{"name":"自定义空计划","caseIds":[]}`} {
		w := apiRequest(a, http.MethodPost, "/api/test-plans", "u_admin", projectID, body)
		if w.Code != 201 {
			t.Fatalf("test plan create failed: %d %s", w.Code, w.Body.String())
		}
		planIDs = append(planIDs, int64(jsonMap(t, w)["id"].(float64)))
	}
	// Adding a case must not make it appear in every existing plan on restart.
	w = apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, `{"title":"尚未选择进计划的用例","steps":"检查计划关联","expected":"未选择的用例不自动加入计划"}`)
	if w.Code != 201 {
		t.Fatalf("case create failed: %d %s", w.Code, w.Body.String())
	}
	for _, statement := range []string{
		`UPDATE tenants SET name='用户改名的企业' WHERE id='tn_acme'`,
		`UPDATE projects SET name='用户改名的项目',description='用户项目说明',owner_user_id='u_pm',icon='自' WHERE id='prj_orbit'`,
		`DELETE FROM department_memberships WHERE user_id='u_qa'`,
		`DELETE FROM field_definitions WHERE tenant_id='tn_acme' AND project_id='prj_orbit' AND object_type='requirement' AND key='customer_type'`,
		`DELETE FROM requirements WHERE project_id='prj_insight'`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var linksBefore, executionsBefore int
	a.db.QueryRow(`SELECT COUNT(*) FROM test_plan_cases`).Scan(&linksBefore)
	a.db.QueryRow(`SELECT COUNT(*) FROM test_executions`).Scan(&executionsBefore)
	for run := 0; run < 2; run++ {
		if err := a.migrate(); err != nil {
			t.Fatalf("migration %d: %v", run, err)
		}
		after, err := a.get(x.ID)
		if err != nil || jsonText(after) != jsonText(before) {
			t.Fatalf("restart changed user requirement: before=%s after=%s err=%v", jsonText(before), jsonText(after), err)
		}
		gotDefect, err := a.getDefect(defect.ID)
		if err != nil || gotDefect.Assignee != "" || gotDefect.Progress != 0 || gotDefect.EstimatedHours != 0 || gotDefect.ActualHours != 0 {
			t.Fatalf("restart decorated user defect: %+v %v", gotDefect, err)
		}
		for index, id := range planIDs {
			var count, executions int
			a.db.QueryRow(`SELECT COUNT(*) FROM test_plan_cases WHERE plan_id=?`, id).Scan(&count)
			a.db.QueryRow(`SELECT COUNT(*) FROM test_executions WHERE plan_id=?`, id).Scan(&executions)
			if want := 1 - index; count != want || executions != want {
				t.Fatalf("restart filled custom plan %d: links=%d executions=%d want=%d", id, count, executions, want)
			}
		}
		var linksAfter, executionsAfter int
		a.db.QueryRow(`SELECT COUNT(*) FROM test_plan_cases`).Scan(&linksAfter)
		a.db.QueryRow(`SELECT COUNT(*) FROM test_executions`).Scan(&executionsAfter)
		if linksAfter != linksBefore || executionsAfter != executionsBefore {
			t.Fatalf("restart changed plan associations globally: %d->%d / %d->%d", linksBefore, linksAfter, executionsBefore, executionsAfter)
		}
		var tenant, project, description, owner, icon string
		a.db.QueryRow(`SELECT name FROM tenants WHERE id=?`, tenantID).Scan(&tenant)
		a.db.QueryRow(`SELECT name,description,owner_user_id,icon FROM projects WHERE id=?`, projectID).Scan(&project, &description, &owner, &icon)
		if tenant != "用户改名的企业" || project != "用户改名的项目" || description != "用户项目说明" || owner != "u_pm" || icon != "自" {
			t.Fatalf("restart reset organization branding: %q %q %q %q %q", tenant, project, description, owner, icon)
		}
		for _, query := range []string{
			`SELECT COUNT(*) FROM department_memberships WHERE user_id='u_qa'`,
			`SELECT COUNT(*) FROM field_definitions WHERE tenant_id='tn_acme' AND project_id='prj_orbit' AND object_type='requirement' AND key='customer_type'`,
			`SELECT COUNT(*) FROM requirements WHERE project_id='prj_insight'`,
		} {
			var count int
			if err := a.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
				t.Fatalf("restart recreated deliberately removed seed data: count=%d err=%v query=%s", count, err, query)
			}
		}
	}
}

func TestAddMigrationColumnResumesAfterPartialFailure(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`CREATE TABLE migration_column_probe(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	first := migrationColumn{table: "migration_column_probe", name: "first_value", statement: `ALTER TABLE migration_column_probe ADD COLUMN first_value TEXT NOT NULL DEFAULT ''`}
	added, err := addMigrationColumn(a.db, first)
	if err != nil || !added {
		t.Fatalf("first column: added=%v err=%v", added, err)
	}
	// 模拟旧库升级时第二条 DDL 损坏：第一条已经落盘，重启时必须保留它，
	// 返回真实错误而不是吞掉后继续执行后续数据回填。
	broken := migrationColumn{table: "migration_column_probe", name: "retry_value", statement: `ALTER TABLE migration_column_probe ADD COLUMN retry_value TEXT DEFAULT (`}
	if _, err := addMigrationColumn(a.db, broken); err == nil {
		t.Fatal("malformed migration DDL unexpectedly succeeded")
	}
	exists, err := a.databaseHasColumns("migration_column_probe", "first_value")
	if err != nil || !exists {
		t.Fatalf("completed column was lost after failure: exists=%v err=%v", exists, err)
	}
	exists, err = a.databaseHasColumns("migration_column_probe", "retry_value")
	if err != nil || exists {
		t.Fatalf("failed column was recorded as complete: exists=%v err=%v", exists, err)
	}
	second := migrationColumn{table: "migration_column_probe", name: "retry_value", statement: `ALTER TABLE migration_column_probe ADD COLUMN retry_value TEXT NOT NULL DEFAULT ''`}
	added, err = addMigrationColumn(a.db, second)
	if err != nil || !added {
		t.Fatalf("restart did not apply missing column: added=%v err=%v", added, err)
	}
	added, err = addMigrationColumn(a.db, second)
	if err != nil || added {
		t.Fatalf("completed column was not idempotent: added=%v err=%v", added, err)
	}
}

func TestDemoSeedPendingRetriesAfterInterruptedMigration(t *testing.T) {
	a := fileSQLiteTestApp(t)
	if _, err := a.db.Exec(`UPDATE migration_bootstrap SET state='pending' WHERE key='demo_seed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`DELETE FROM projects WHERE tenant_id=? AND id=?`, tenantID, insightProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER reject_insight_seed BEFORE INSERT ON projects WHEN NEW.id='prj_insight' BEGIN SELECT RAISE(ABORT,'injected seed failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err == nil {
		t.Fatal("interrupted demo seed unexpectedly succeeded")
	}
	var state string
	if err := a.db.QueryRow(`SELECT state FROM migration_bootstrap WHERE key='demo_seed'`).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("failed migration completed bootstrap marker: state=%q err=%v", state, err)
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE tenant_id=? AND id=?`, tenantID, insightProjectID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed seed left partial insight project: count=%d err=%v", count, err)
	}
	if _, err := a.db.Exec(`DROP TRIGGER reject_insight_seed`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err != nil {
		t.Fatalf("restart did not recover pending demo seed: %v", err)
	}
	if err := a.db.QueryRow(`SELECT state FROM migration_bootstrap WHERE key='demo_seed'`).Scan(&state); err != nil || state != "complete" {
		t.Fatalf("recovered migration did not complete bootstrap marker: state=%q err=%v", state, err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE tenant_id=? AND id=?`, tenantID, insightProjectID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("recovered seed did not restore insight project: count=%d err=%v", count, err)
	}
}

func TestBootstrapMarkerSurvivesEarlySchemaFailure(t *testing.T) {
	db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "interrupted-bootstrap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &App{db: db, passwordCost: 4, signingKey: []byte("migration-bootstrap-signing-key-at-least-32-bytes")}
	// 名称冲突的旧对象会让建表在中途失败。此时 tenants 已可能创建，
	// 所以专门验证 pending 标记已在整个 schema 之前落盘。
	if _, err := db.Exec(`CREATE VIEW users AS SELECT 'legacy' AS id`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err == nil {
		t.Fatal("schema conflict unexpectedly migrated")
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM migration_bootstrap WHERE key='demo_seed'`).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("early failure lost recovery marker: state=%q err=%v", state, err)
	}
	if _, err := db.Exec(`DROP VIEW users`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrate(); err != nil {
		t.Fatalf("repaired schema did not resume bootstrap: %v", err)
	}
	if err := db.QueryRow(`SELECT state FROM migration_bootstrap WHERE key='demo_seed'`).Scan(&state); err != nil || state != "complete" {
		t.Fatalf("resumed bootstrap not complete: state=%q err=%v", state, err)
	}
}

func TestCriticalListReadersReturn503InsteadOfPanicking(t *testing.T) {
	for _, name := range []string{"sprints", "members", "field-definitions"} {
		for _, failure := range []string{"query", "scan"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				a := testApp(t)
				var handler http.HandlerFunc
				var corrupt string
				switch name {
				case "sprints":
					handler, corrupt = a.sprints, `UPDATE sprints SET capacity='invalid-integer'`
				case "members":
					handler, corrupt = a.members, `UPDATE users SET active='invalid-boolean' WHERE id='u_front'`
				case "field-definitions":
					handler, corrupt = a.fieldDefinitions, `UPDATE field_definitions SET sort_order='invalid-integer'`
				}
				if failure == "query" {
					if err := a.db.Close(); err != nil {
						t.Fatal(err)
					}
				} else if _, err := a.db.Exec(corrupt); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest(http.MethodGet, "/api/"+name, nil))
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("%s %s failure returned %d: %s", name, failure, w.Code, w.Body.String())
				}
			})
		}
	}
	a := testApp(t)
	if _, err := a.db.Exec(`DROP TABLE defects`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.sprints(w, httptest.NewRequest(http.MethodGet, "/api/sprints", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(fmt.Sprintf("failed sprint aggregates must not become false zeros: %d %s", w.Code, w.Body.String()))
	}
}
