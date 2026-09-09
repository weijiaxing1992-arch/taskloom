package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readWorkloadTrends(t *testing.T, a *App, query string) (WorkloadTrends, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.workloadTrends(w, httptest.NewRequest("GET", "/api/reports/workload/trends?"+query, nil))
	var out WorkloadTrends
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, w
}
func TestWorkloadTrendsMonthsZerosMissingAndCurrentSnapshot(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Explicit zero", "2050-01-20")
	workloadSprintFixture(t, a, projectID, "Unestimated", "2050-02-18")
	workloadSprintFixture(t, a, projectID, "Outside range", "2050-03-01")
	workloadRequirementFixture(t, a, projectID, "Explicit zero", "已上线", `{"frontend":{"userId":"u_front","value":0}}`)
	id := workloadRequirementFixture(t, a, projectID, "Unestimated", "规划中", `{"frontend":{"userId":"u_front","value":null}}`)
	workloadRequirementFixture(t, a, projectID, "Outside range", "已上线", `{"frontend":{"value":99}}`)
	r, w := readWorkloadTrends(t, a, "month=2050-02&months=6")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(r.Monthly) != 6 || r.FromMonth != "2049-09" || r.Monthly[0].HasData || r.Monthly[0].Metrics.Weight != 0 || len(r.Iterations) != 2 {
		t.Fatalf("invalid range or missing point: %+v", r)
	}
	zero, unset := r.Monthly[4], r.Monthly[5]
	if !zero.HasData || zero.Metrics.Weight != 0 || zero.Metrics.EstimatedRoleCount != 1 || zero.Metrics.ShippedRequirementCount != 1 || !unset.HasData || unset.Metrics.EstimatedRoleCount != 0 || unset.Metrics.UnestimatedRoleCount != 1 {
		t.Fatalf("null/zero/snapshot conflation: %+v %+v", zero, unset)
	}
	monthly, mw := workloadRead(t, a, "2050-02")
	if mw.Code != 200 || !reflect.DeepEqual(monthly.Totals, unset.Metrics) {
		t.Fatal("trend changed original monthly metrics")
	}
	workloadExec(t, a, `UPDATE requirements SET status='已上线' WHERE id=?`, id)
	changed, _ := readWorkloadTrends(t, a, "month=2050-02")
	if changed.Monthly[5].Metrics.ShippedRequirementCount != 1 || !changed.Snapshot || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("must expose current state, not invented historical publication date")
	}
}
func TestWorkloadTrendsSameNameIterationScopeAliasesAndOrdering(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{projectID, insightProjectID} {
		workloadSprintFixture(t, a, p, "Same name", "2050-02-10")
		workloadRequirementFixture(t, a, p, "Same name", "已上线", `{"backend":{"value":2}}`)
	}
	workloadSprintFixture(t, a, projectID, "First version", "2050-01-05")
	workloadRequirementFixture(t, a, projectID, "First", "规划中", `{"frontend":{"value":1}}`)
	workloadSprintFixture(t, a, projectID, "Ambiguous one", "2050-02-15")
	workloadSprintFixture(t, a, projectID, "Ambiguous two", "2050-03-20")
	workloadRequirementFixture(t, a, projectID, "Ambiguous", "已上线", `{"frontend":{"value":90}}`)
	workloadRequirementFixture(t, a, projectID, "待规划", "已上线", `{"frontend":{"value":900}}`)
	workloadExec(t, a, `INSERT INTO tenants(id,name)VALUES('trend-secret','Secret');INSERT INTO projects(id,tenant_id,name,code)VALUES('trend-foreign','trend-secret','Secret','S');INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,created_at,updated_at)VALUES('trend-secret','trend-foreign','S','Secret','2050-02-01','2050-02-10','now','now');INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES('trend-secret','trend-foreign','R','Secret','Secret','已上线','{"frontend":{"value":999999}}','now','now')`)
	r, w := readWorkloadTrends(t, a, "month=2050-02&months=12")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(r.Monthly) != 12 || len(r.Iterations) != 4 || r.Iterations[0].Name != "First version" || r.AmbiguousSprintItemCount != 1 || r.Monthly[11].Metrics.Weight != 4 {
		t.Fatalf("wrong scope/alias: %+v", r)
	}
	if r.Iterations[1].Name != "Same name" || r.Iterations[2].Name != "Same name" || r.Iterations[1].Key == r.Iterations[2].Key || r.Iterations[1].ProjectID == r.Iterations[2].ProjectID || strings.Contains(w.Body.String(), "trend-secret") {
		t.Fatal("same names merged or foreign tenant leaked")
	}
}
func TestWorkloadTrendFiltersUseExactRoleSharesAndStableDirectoryIDs(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Shares", "2050-02-10")
	workloadExec(t, a, `UPDATE users SET name='Duplicate name' WHERE id IN('u_front','u_back');DELETE FROM department_memberships WHERE tenant_id=? AND user_id IN('u_front','u_back','u_qa')`, tenantID)
	workloadExec(t, a, `INSERT INTO departments(id,tenant_id,name,code,external_id,created_at,updated_at)VALUES('trend-dep',?,'Trend department','TREND','trend','now','now');INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,joined_at,updated_at)VALUES(?,'trend-dep','u_front',1,'now','now')`, tenantID, tenantID)
	workloadRequirementFixture(t, a, projectID, "Shares", "已上线", `{"frontend":{"userIds":["u_front","u_back","u_qa","u_front"],"value":1},"backend":{"userId":"u_front","value":2}}`)
	workloadExec(t, a, `INSERT INTO defects(tenant_id,project_id,code,title,sprint,assignee_user_id,discipline,created_at,updated_at)VALUES(?,?,'B','Bug','Shares','u_front','frontend','now','now')`, tenantID, projectID)
	for _, tc := range []struct {
		query                 string
		weight                float64
		requirements, defects int
	}{{"", 3, 1, 1}, {"&role=frontend", 1, 1, 1}, {"&user=u_front", 2.333333, 1, 1}, {"&department=trend-dep&role=frontend", .333333, 1, 1}, {"&department=&role=frontend", .666667, 1, 0}, {"&user=u_back&department=trend-dep", 0, 0, 0}, {"&role=product", 0, 0, 0}, {"&user=foreign-id", 0, 0, 0}} {
		r, w := readWorkloadTrends(t, a, "month=2050-02"+tc.query)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		m := r.Monthly[5].Metrics
		if m.Weight != tc.weight || m.RequirementCount != tc.requirements || m.DefectCount != tc.defects {
			t.Fatalf("%s got %+v", tc.query, m)
		}
		if !reflect.DeepEqual(m, r.Iterations[0].Metrics) {
			t.Fatal("month and single iteration disagree")
		}
	}
	monthly, _ := workloadRead(t, a, "2050-02")
	trend, _ := readWorkloadTrends(t, a, "month=2050-02&user=u_front")
	if !reflect.DeepEqual(workloadPerson(t, monthly, "u_front").WorkloadMetrics, trend.Monthly[5].Metrics) {
		t.Fatal("person trend disagrees with existing report")
	}
}
func TestWorkloadTrendsPermissionsValidationAndFailureClosed(t *testing.T) {
	a := testApp(t)
	a.user = "u_front"
	_, w := readWorkloadTrends(t, a, "month=2050-02")
	if w.Code != 403 {
		t.Fatal("ordinary project member saw company trends")
	}
	a.user = "u_admin"
	for _, query := range []string{"month=0000-01", "month=2050-13", "month=2050-02&months=7", "month=2050-02&role=bad", "month=2050-02&user=%20u_front"} {
		_, w = readWorkloadTrends(t, a, query)
		if w.Code != 422 {
			t.Fatalf("accepted %s: %d", query, w.Code)
		}
	}
	r, w := readWorkloadTrends(t, a, "month=0001-01&months=12")
	if w.Code != 200 || len(r.Monthly) != 1 || r.FromMonth != "0001-01" {
		t.Fatal("calendar lower bound incorrect")
	}
	workloadSprintFixture(t, a, projectID, "Corrupt trend", "2050-02-10")
	workloadRequirementFixture(t, a, projectID, "Corrupt trend", "规划中", `{"frontend":{"value":"bad"}}`)
	_, w = readWorkloadTrends(t, a, "month=2050-02")
	if w.Code != 503 || strings.Contains(w.Body.String(), "metrics") {
		t.Fatal("returned misleading partial/zero report")
	}
}

type workloadCountingStore struct {
	stateStore
	queries []string
}

func (q *workloadCountingStore) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queries = append(q.queries, query)
	return q.stateStore.QueryContext(ctx, query, args...)
}
func TestWorkloadTrendsFullPoolSingleScanPrecision(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Large trend", "2050-02-28")
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1201; i++ {
		_, err = tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES(?,?,'R','Full pool','Large trend','规划中','{"frontend":{"userId":"u_front","value":0.1}}','now','now')`, tenantID, projectID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	q := &workloadCountingStore{stateStore: a.db}
	start := time.Date(2049, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2050, 3, 1, 0, 0, 0, 0, time.UTC)
	c := newWorkloadTrendCollector(start, end, WorkloadTrendFilters{Department: "*"})
	_, err = a.monthlyWorkload(context.Background(), q, "2050-02", "2049-03-01", "2050-03-01", c)
	if err != nil {
		t.Fatal(err)
	}
	points := trendPoints(c.monthly, false)
	if points[11].Metrics.Weight != 120.1 || points[11].Metrics.RequirementCount != 1201 {
		t.Fatal("pagination truncation or decimal drift")
	}
	for _, table := range []string{"FROM requirements r", "FROM defects d", "FROM sprints s"} {
		count := 0
		for _, query := range q.queries {
			if strings.Contains(query, table) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s scanned %d times for 12 months", table, count)
		}
	}
}
