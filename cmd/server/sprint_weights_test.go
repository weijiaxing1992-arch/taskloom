package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func insertWeightRequirement(t *testing.T, a *App, tenant, project, sprint, status, weights string, parent *int64) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,parent_id,estimated_hours,actual_hours,created_at,updated_at)VALUES(?,?,'REQ-AUDIT','用户原文 Weight audit',?,?,?,?,999,888,'2026-09-03','2026-09-03')`, tenant, project, sprint, status, weights, parent)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func weightSummaryResponse(t *testing.T, a *App, path string) SprintWeightSummary {
	t.Helper()
	w := apiRequest(a, http.MethodGet, path, "u_admin", a.pid(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("weight response: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		WeightSummary SprintWeightSummary `json:"weightSummary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.WeightSummary
}

func TestSprintWeightsManualDimensionsPrecisionNullZeroAndHierarchy(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "权重精度迭代", "已完成")
	parent := insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "已完成", `{"frontend":{"value":0.1},"backend":{"value":0.2},"algorithm":{"value":2},"ui":{"value":3.25},"product":{"value":4.125}}`, nil)
	child := insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "开发中", `{"frontend":{"value":6e-7},"backend":{"value":4e-7}}`, &parent)
	insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "已拒绝", `{"frontend":{"value":0}}`, nil)
	insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "已取消", `{"frontend":{"value":null,"userId":"u_front"},"product":{"userId":"u_pm"}}`, nil)
	summary := weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", s.ID))
	if summary.RequirementCount != 4 || summary.EstimatedCount != 3 || summary.UnestimatedCount != 1 || summary.TotalWeight != 9.675001 || summary.Precision != 6 {
		t.Fatalf("manual counts/total mismatch: %+v", summary)
	}
	for role, want := range map[string]float64{"frontend": .100001, "backend": .2, "algorithm": 2, "ui": 3.25, "product": 4.125} {
		if summary.RoleTotals[role] != want {
			t.Fatalf("%s total=%v want=%v", role, summary.RoleTotals[role], want)
		}
	}
	if summary.RoleEstimatedCounts["frontend"] != 3 || summary.RoleEstimatedCounts["ui"] != 1 || len(summary.Items) != 4 {
		t.Fatalf("zero estimates or detail count lost: %+v", summary)
	}
	if summary.Items[1].ID != child || summary.Items[1].ParentID == nil || *summary.Items[1].ParentID != parent || summary.Items[1].TotalWeight != .000001 {
		t.Fatalf("child should contribute only its own values once: %+v", summary.Items[1])
	}
	if summary.Items[2].Weights["frontend"] == nil || *summary.Items[2].Weights["frontend"] != 0 || !summary.Items[2].Estimated || summary.Items[3].Estimated {
		t.Fatal("null and deliberately entered zero must remain distinct")
	}
}

func TestSprintWeightsAllRowsNotListPageAndScopeIsolation(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "全部权重非分页", "进行中")
	const count = 1507
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES(?,?,'REQ-LARGE','Large pool',?,'已完成','{"frontend":{"value":0.1},"backend":{"value":0.2}}','now','now')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if _, err := statement.Exec(tenantID, a.pid(), s.Name); err != nil {
			t.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	insertWeightRequirement(t, a, "another-tenant", a.pid(), s.Name, "开发中", `{"frontend":{"value":1000000}}`, nil)
	insertWeightRequirement(t, a, tenantID, insightProjectID, s.Name, "开发中", `{"backend":{"value":1000000}}`, nil)
	summary := weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", s.ID))
	if summary.RequirementCount != count || len(summary.Items) != count || summary.TotalWeight != 452.1 || summary.RoleTotals["frontend"] != 150.7 || summary.RoleTotals["backend"] != 301.4 {
		t.Fatalf("scope or list page leaked into summary: count=%d total=%v roles=%v", summary.RequirementCount, summary.TotalWeight, summary.RoleTotals)
	}
	denied := apiRequest(a, http.MethodGet, "/api/sprints/backlog/weights", "u_front", insightProjectID, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("cross-project access: %d %s", denied.Code, denied.Body.String())
	}
	wrong := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", s.ID), "u_admin", insightProjectID, "")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross-project sprint: %d %s", wrong.Code, wrong.Body.String())
	}
}

func TestSprintWeightsBacklogMoveAndRemovalRecompute(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "归属实时核算", "规划中")
	before := weightSummaryResponse(t, a, "/api/sprints/backlog/weights")
	id := insertWeightRequirement(t, a, tenantID, a.pid(), "", "草稿", `{"product":{"value":12.5}}`, nil)
	backlog := weightSummaryResponse(t, a, "/api/sprints/backlog/weights")
	if backlog.RequirementCount != before.RequirementCount+1 || backlog.TotalWeight != before.TotalWeight+12.5 {
		t.Fatal("empty legacy sprint is not included in backlog")
	}
	patchPlanningRequirement(t, a, id, jsonText(map[string]any{"sprint": s.Name}))
	assigned := weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", s.ID))
	backlog = weightSummaryResponse(t, a, "/api/sprints/backlog/weights")
	if assigned.TotalWeight != 12.5 || assigned.RequirementCount != 1 || backlog.TotalWeight != before.TotalWeight || backlog.RequirementCount != before.RequirementCount {
		t.Fatal("move did not leave backlog and enter the exact sprint")
	}
	patchPlanningRequirement(t, a, id, `{"sprint":"待规划"}`)
	assigned = weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", s.ID))
	if assigned.TotalWeight != 0 || assigned.RequirementCount != 0 || assigned.Items == nil {
		t.Fatal("empty sprint must return explicit zeros and empty details")
	}
	if _, err := a.db.Exec(`DELETE FROM requirements WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()); err != nil {
		t.Fatal(err)
	}
	removed := weightSummaryResponse(t, a, "/api/sprints/backlog/weights")
	if removed.TotalWeight != before.TotalWeight || removed.RequirementCount != before.RequirementCount {
		t.Fatal("deleted requirement remains in aggregate")
	}
}

func TestSprintWeightsLegacyAliasesNeverDoubleCount(t *testing.T) {
	a := testApp(t)
	first := planningSprint(t, a, "Release Alpha", "进行中")
	insertWeightRequirement(t, a, tenantID, a.pid(), "Release", "开发中", `{"frontend":{"value":7}}`, nil)
	if weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", first.ID)).TotalWeight != 7 {
		t.Fatal("unambiguous legacy alias missing")
	}
	second := planningSprint(t, a, "Release Beta", "规划中")
	for _, s := range []Sprint{first, second} {
		if weightSummaryResponse(t, a, fmt.Sprintf("/api/sprints/%d", s.ID)).TotalWeight != 0 {
			t.Fatal("ambiguous alias must not be counted in multiple sprints")
		}
	}
}

func TestSprintWeightsInvalidStoredDataDoesNotSilentlyReportZero(t *testing.T) {
	for _, raw := range []string{`{"frontend":{"value":-1}}`, `{"frontend":{"value":1000001}}`, `{"frontend":{"value":"not a number"}}`, `{"unexpected":{"value":20}}`, `{broken`} {
		t.Run(raw, func(t *testing.T) {
			a := testApp(t)
			s := planningSprint(t, a, "坏数据反馈", "进行中")
			insertWeightRequirement(t, a, tenantID, a.pid(), s.Name, "开发中", raw, nil)
			w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", s.ID), "u_admin", a.pid(), "")
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), raw) {
				t.Fatalf("invalid weight must produce a safe error: %d %s", w.Code, w.Body.String())
			}
		})
	}
	a := testApp(t)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.sprintWeightSummary(context.Background(), "待规划"); err == nil {
		t.Fatal("database errors must not look like zero-weight results")
	}
}
