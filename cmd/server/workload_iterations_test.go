package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func readIterationAnalysis(t *testing.T, a *App, query string) (IterationAnalysisReport, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.workloadIterationAnalysis(w, httptest.NewRequest("GET", "/api/reports/workload/iterations?"+query, nil))
	var out IterationAnalysisReport
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, w
}
func analysisFixture(t *testing.T, a *App) {
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("Analysis-%d", i)
		workloadSprintFixture(t, a, projectID, name, fmt.Sprintf("2020-01-%02d", i))
		workloadRequirementFixture(t, a, projectID, name, "已上线", `{"frontend":{"userIds":["u_front","u_back","u_front"],"value":3},"backend":{"userId":"u_back","value":4}}`)
	}
	workloadExec(t, a, `UPDATE sprints SET status='已完成',start_date='2020-01-01' WHERE name LIKE 'Analysis-%'`)
	workloadSprintFixture(t, a, projectID, "Not completed", "2020-01-20")
	workloadRequirementFixture(t, a, projectID, "Not completed", "已上线", `{"frontend":{"userId":"u_front","value":999}}`)
}
func TestIterationAnalysisSelectionSharesAndPersonalIsolation(t *testing.T) {
	a := testApp(t)
	analysisFixture(t, a)
	a.user = "u_front"
	out, w := readIterationAnalysis(t, a, "month=2020-01&count=4&user=u_back")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(out.Iterations) != 4 || out.Iterations[0].Name != "Analysis-3" || len(out.People) != 1 || out.People[0].UserID != "u_front" || strings.Contains(w.Body.String(), "u_back") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe report: %s", w.Body.String())
	}
	for _, point := range out.People[0].Roles[0].Points {
		if point.Weight != 1.5 || point.DeliveredWeight != 1.5 || point.RequirementCount != 1 || point.EstimatedRoleCount != 1 {
			t.Fatalf("invalid split: %+v", point)
		}
	}
	_, w = readIterationAnalysis(t, a, "month=2020-01&scope=organization")
	if w.Code != 403 {
		t.Fatalf("ordinary member accessed company: %d", w.Code)
	}
	workloadExec(t, a, `DELETE FROM project_members WHERE user_id='u_front'`)
	out, w = readIterationAnalysis(t, a, "month=2020-01")
	if w.Code != 200 || len(out.Iterations) != 0 || len(out.People) != 1 || len(out.People[0].Roles) != 0 {
		t.Fatalf("revoked scope widened: %s", w.Body.String())
	}
}
func TestIterationAnalysisOrganizationGapsFiltersAndSnapshot(t *testing.T) {
	a := testApp(t)
	analysisFixture(t, a)
	workloadRequirementFixture(t, a, projectID, "Analysis-6", "规划中", `{"frontend":{"userId":"u_front","value":null}}`)
	out, w := readIterationAnalysis(t, a, "month=2020-01&scope=organization&count=2")
	if w.Code != 200 || len(out.Iterations) != 2 || len(out.People) < 2 || !out.Snapshot {
		t.Fatal(w.Body.String())
	}
	found := false
	for _, p := range out.People {
		if p.UserID == "u_front" {
			found = true
			point := p.Roles[0].Points[1]
			if point.UnestimatedRoleCount != 1 || point.RequirementCount != 2 || point.DeliveredWeight != 1.5 {
				t.Fatalf("missing estimate fabricated: %+v", point)
			}
		}
	}
	if !found {
		t.Fatal("missing employee")
	}
	for _, q := range []string{"count=1000", "count=-1", "count=3", "scope=team", "month=2020-13", "count=no"} {
		_, w = readIterationAnalysis(t, a, q)
		if w.Code != 422 {
			t.Fatalf("accepted %s: %d", q, w.Code)
		}
	}
	out, w = readIterationAnalysis(t, a, "month=2020-01&scope=organization&project=foreign")
	if w.Code != 200 || len(out.Iterations) != 0 {
		t.Fatal("project filter ignored")
	}
	workloadExec(t, a, `UPDATE users SET active=0 WHERE id='u_admin'`)
	_, w = readIterationAnalysis(t, a, "scope=organization")
	if w.Code != 403 {
		t.Fatal("inactive principal authorized")
	}
}

func TestIterationAnalysisEarlyCompletionUsesSelectedMonth(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Early complete", "2050-02-28")
	workloadExec(t, a, `UPDATE sprints SET status='已完成' WHERE name='Early complete'`)
	out, w := readIterationAnalysis(t, a, "month=2050-02")
	if w.Code != 200 || len(out.Iterations) != 1 || out.Iterations[0].Name != "Early complete" {
		t.Fatalf("early completion hidden: %s", w.Body.String())
	}
	out, w = readIterationAnalysis(t, a, "month=2050-01")
	if w.Code != 200 || len(out.Iterations) != 0 {
		t.Fatal("month boundary ignored")
	}
}
