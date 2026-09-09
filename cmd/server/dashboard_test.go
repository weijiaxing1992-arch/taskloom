package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func dashboardFixture(t *testing.T) *App {
	t.Helper()
	a := testApp(t)
	for _, table := range []string{"test_executions", "test_plans", "test_cases", "defects", "requirements", "sprints"} {
		if _, err := a.db.Exec(`DELETE FROM `+table+` WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()); err != nil {
			t.Fatal(err)
		}
	}
	return a
}
func readDashboard(t *testing.T, a *App) ProjectDashboard {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/dashboard?days=7", "u_viewer", a.pid(), "")
	if w.Code != 200 {
		t.Fatalf("dashboard %d %s", w.Code, w.Body.String())
	}
	var result ProjectDashboard
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestDashboardEmptyViewerAndValidation(t *testing.T) {
	a := dashboardFixture(t)
	result := readDashboard(t, a)
	if result.Project.ID != a.pid() || result.Days != 7 || len(result.Trend) != 7 || result.TrendAvailable || result.Totals.Requirements.Total != 0 || result.CurrentSprints == nil || result.Members == nil {
		t.Fatalf("empty report: %+v", result)
	}
	for _, query := range []string{"days=0", "days=100000", "days=8", "days=7x", "days=-7"} {
		w := apiRequest(a, "GET", "/api/dashboard?"+query, "u_viewer", a.pid(), "")
		if w.Code != 422 {
			t.Fatalf("invalid range %s => %d", query, w.Code)
		}
	}
	w := apiRequest(a, "POST", "/api/dashboard", "u_admin", a.pid(), "{}")
	if w.Code != 405 {
		t.Fatalf("mutating method %d", w.Code)
	}
}
func TestDashboardFullCountsWeightsStatusesAndParticipation(t *testing.T) {
	a := dashboardFixture(t)
	s := planningSprint(t, a, "Dashboard sprint full name", "进行中")
	if _, err := a.db.Exec(`INSERT INTO requirement_statuses(tenant_id,project_id,key,name,color,category,enabled,sort_order,system,created_at,updated_at)VALUES(?,?,'custom-done','Custom 中文完成','#123456','done',1,99,0,'now','now')`, tenantID, a.pid()); err != nil {
		t.Fatal(err)
	}
	parent := insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "custom-done", `{"frontend":{"value":0.1,"userIds":["u_front","u_pm"]},"backend":{"value":0.2,"userId":"u_front"}}`, nil)
	child := insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "已取消", `{"ui":{"value":0}}`, &parent)
	if _, err := a.db.Exec(`UPDATE requirements SET assignee_user_ids_json='["u_front","u_front","u_pm"]',owner_user_ids_json='["u_pm"]' WHERE id=?`, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,status,severity,sprint,assignee_user_id,verifier_user_id,created_at,updated_at)VALUES(?,?,'BUG-D','defect','修复中','严重',?,'u_front','u_front','2026-09-03','2026-09-03'),(?,?,'BUG-C','closed','已关闭','致命',?,'u_pm','u_qa','2026-09-03','2026-09-03'),(?,?,'BUG-R','rejected','已拒绝','严重',?,'','','2026-09-03','2026-09-03')`, tenantID, a.pid(), s.Name, tenantID, a.pid(), s.Name, tenantID, a.pid(), s.Name); err != nil {
		t.Fatal(err)
	}
	result := readDashboard(t, a)
	if result.Totals.Requirements.Total != 2 || result.Totals.Requirements.Done != 1 || result.Totals.Requirements.Cancelled != 1 {
		t.Fatalf("requirement categories: %+v", result.Totals.Requirements)
	}
	if result.Totals.Defects.Total != 3 || result.Totals.Defects.Open != 1 || result.Totals.Defects.Closed != 1 || result.Totals.Defects.Critical != 1 {
		t.Fatalf("defect counts: %+v", result.Totals.Defects)
	}
	if len(result.CurrentSprints) != 1 || result.CurrentSprints[0].Total != 5 || result.CurrentSprints[0].Done != 2 || result.CurrentSprints[0].WeightTotal != .3 {
		t.Fatalf("sprint count or decimal weight: %+v", result.CurrentSprints)
	}
	for _, p := range result.Members {
		if p.ID == "u_front" && (p.RequirementCount != 1 || p.DefectCount != 1) {
			t.Fatalf("same person must not multiply: %+v", p)
		}
		if p.ID == "u_pm" && p.RequirementCount != 1 {
			t.Fatalf("second assignee missing: %+v", p)
		}
	}
	found := false
	for _, status := range result.Statuses.Requirements {
		if status.Key == "custom-done" {
			found = true
			if status.Name != "Custom 中文完成" || status.System || status.Count != 1 {
				t.Fatalf("custom status changed: %+v", status)
			}
		}
	}
	if !found {
		t.Fatal("custom status missing")
	}
	if _, err := a.db.Exec(`UPDATE requirements SET sprint='待规划' WHERE id=?`, child); err != nil {
		t.Fatal(err)
	}
	result = readDashboard(t, a)
	if result.CurrentSprints[0].Total != 4 || result.Totals.Requirements.Total != 2 {
		t.Fatal("moving requirement did not leave sprint while remaining in project")
	}
}
func TestDashboardAllRowsScopeAndLegacySprintAmbiguity(t *testing.T) {
	a := dashboardFixture(t)
	s := planningSprint(t, a, "Dash all rows", "进行中")
	planningSprint(t, a, "Dash another", "进行中")
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO requirements(tenant_id,project_id,code,title,status,sprint,role_weights_json,created_at,updated_at)VALUES(?,?,'REQ','r','已完成',?,'{"frontend":{"value":0.1}}','2026-09-03','2026-09-03')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1201; i++ {
		if _, err = stmt.Exec(tenantID, a.pid(), s.Name); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	insertWeightRequirement(t, a, tenantID, a.pid(), "Dash", "规划中", `{"frontend":{"value":500}}`, nil)
	insertWeightRequirement(t, a, tenantID, insightProjectID, s.Name, "已完成", `{"frontend":{"value":600}}`, nil)
	insertWeightRequirement(t, a, "other-tenant", a.pid(), s.Name, "已完成", `{"frontend":{"value":700}}`, nil)
	result := readDashboard(t, a)
	if result.Totals.Requirements.Total != 1202 {
		t.Fatalf("page limit/scope leak %d", result.Totals.Requirements.Total)
	}
	for _, sprint := range result.CurrentSprints {
		if sprint.ID == s.ID && (sprint.Total != 1201 || sprint.WeightTotal != 120.1) {
			t.Fatalf("ambiguous alias/multiplication: %+v", sprint)
		}
	}
}
func TestDashboardCreationTrendActualDatesAndTimezone(t *testing.T) {
	a := dashboardFixture(t)
	for _, date := range []string{"2026-09-02T16:00:00Z", "2026-09-02T15:59:59Z", "2026-08-01", "bad-date", "2026-09-04T00:00:00Z"} {
		id := insertWeightRequirement(t, a, tenantID, a.pid(), "待规划", "规划中", "{}", nil)
		if _, err := a.db.Exec(`UPDATE requirements SET created_at=? WHERE id=?`, date, id); err != nil {
			t.Fatal(err)
		}
	}
	now, _ := time.Parse(time.RFC3339, "2026-09-03T08:00:00Z")
	result, err := a.projectDashboard(context.Background(), a.db, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trend) != 7 || result.Trend[6].Date != "2026-09-03" || result.Trend[6].RequirementsCreated != 1 || result.Trend[5].RequirementsCreated != 1 || result.MissingCreationDates != 1 || !result.TrendAvailable {
		t.Fatalf("actual creation dates: %+v", result)
	}
	sum := 0
	for _, day := range result.Trend {
		sum += day.RequirementsCreated
	}
	if sum != 2 {
		t.Fatalf("out of range/future trend leaked: %d", sum)
	}
}
func TestDashboardQualityExecutionsUseExistingScopedRelations(t *testing.T) {
	a := dashboardFixture(t)
	res, err := a.db.Exec(`INSERT INTO test_cases(tenant_id,project_id,code,title,created_at,updated_at,enabled)VALUES(?,?,'TC','case','now','now',1)`, tenantID, a.pid())
	if err != nil {
		t.Fatal(err)
	}
	caseID, _ := res.LastInsertId()
	for _, status := range []string{"未执行", "通过", "失败", "阻塞", "跳过"} {
		res, err = a.db.Exec(`INSERT INTO test_plans(tenant_id,project_id,code,name,created_at,updated_at)VALUES(?,?,'TP','plan','now','now')`, tenantID, a.pid())
		if err != nil {
			t.Fatal(err)
		}
		planID, _ := res.LastInsertId()
		if _, err = a.db.Exec(`INSERT INTO test_executions(tenant_id,project_id,plan_id,case_id,status,executor_user_id)VALUES(?,?,?,?,?,'u_qa')`, tenantID, a.pid(), planID, caseID, status); err != nil {
			t.Fatal(err)
		}
	}
	// A corrupted cross-project relation is not counted as an accessible run.
	if _, err = a.db.Exec(`INSERT INTO test_executions(tenant_id,project_id,plan_id,case_id,status)VALUES(?,?,999999,?,'通过')`, tenantID, a.pid(), caseID); err != nil {
		t.Fatal(err)
	}
	result := readDashboard(t, a)
	executions := result.Totals.Executions
	if executions.Total != 5 || executions.Passed != 1 || executions.Failed != 1 || executions.Blocked != 1 || executions.NotRun != 1 || executions.Skipped != 1 || result.Totals.TestCases.Total != 1 || result.Totals.TestPlans.Total != 5 {
		t.Fatalf("quality totals: %+v", result.Totals)
	}
}
func TestDashboardAuthorizationArchiveAndReadFailures(t *testing.T) {
	for _, scenario := range []string{"unassigned", "archived", "query", "scan", "weights"} {
		t.Run(scenario, func(t *testing.T) {
			a := dashboardFixture(t)
			expected := 503
			switch scenario {
			case "unassigned":
				expected = 403
				if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_viewer'`, tenantID, a.pid()); err != nil {
					t.Fatal(err)
				}
			case "archived":
				expected = 403
				if _, err := a.db.Exec(`UPDATE projects SET status='archived' WHERE id=?`, a.pid()); err != nil {
					t.Fatal(err)
				}
			case "query":
				if _, err := a.db.Exec(`ALTER TABLE test_cases RENAME TO dashboard_missing_cases`); err != nil {
					t.Fatal(err)
				}
			case "scan":
				if _, err := a.db.Exec(`UPDATE users SET active='broken' WHERE id='u_front'`); err != nil {
					t.Fatal(err)
				}
			case "weights":
				insertWeightRequirement(t, a, tenantID, a.pid(), "待规划", "规划中", `{"frontend":{"value":"bad"}}`, nil)
			}
			w := apiRequest(a, "GET", "/api/dashboard", "u_viewer", a.pid(), "")
			if w.Code != expected || strings.Contains(w.Body.String(), "currentSprints") {
				t.Fatalf("%s => %d %s", scenario, w.Code, w.Body.String())
			}
		})
	}
}
