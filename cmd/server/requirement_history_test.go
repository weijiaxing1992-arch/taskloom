package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func readRequirementHistory(t *testing.T, a *App, id int64, user string) []requirementHistoryItem {
	t.Helper()
	w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/activities", id), user, a.pid(), "")
	if w.Code != 200 {
		t.Fatalf("history %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []requirementHistoryItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Items
}

func TestRequirementHistoryMovesAccumulateAndNoOpsStaySingle(t *testing.T) {
	a := testApp(t)
	for _, name := range []string{"History A", "History B", "History C"} {
		planningSprint(t, a, name, "规划中")
	}
	x := planningRequirement(t, a, `{"title":"history start","iterationDelayCount":99}`)
	if x.IterationDelayCount != 0 {
		t.Fatal("client supplied delay count accepted")
	}
	steps := []struct {
		sprint string
		count  int
	}{{"History A", 0}, {"History B", 1}, {"History B", 1}, {"History C", 2}, {"待规划", 2}, {"History A", 2}, {"History B", 3}}
	for _, step := range steps {
		x = patchPlanningRequirement(t, a, x.ID, jsonText(map[string]any{"sprint": step.sprint, "iterationDelayCount": 9999}))
		if x.IterationDelayCount != step.count {
			t.Fatalf("move to %s got %d want %d", step.sprint, x.IterationDelayCount, step.count)
		}
	}
	before := len(readRequirementHistory(t, a, x.ID, "u_viewer"))
	x = patchPlanningRequirement(t, a, x.ID, `{"title":"history changed","status":"开发中","remarks":"complete field detail"}`)
	items := readRequirementHistory(t, a, x.ID, "u_viewer")
	if len(items) != before+1 {
		t.Fatal("field change created duplicate activity")
	}
	latest := items[0]
	var adminName string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&adminName); err != nil {
		t.Fatal(err)
	}
	if latest.ActorID != "u_admin" || latest.ActorName != adminName || latest.CreatedAt == "" || latest.Sprint == nil || *latest.Sprint != "History B" || latest.Status == nil || *latest.Status != "开发中" || latest.IterationDelayCount != 3 {
		t.Fatalf("missing history context %+v", latest)
	}
	for _, key := range []string{"title", "status", "remarks"} {
		found := false
		for _, change := range latest.Changes {
			if change.Field == key {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing field %s", key)
		}
	}
	created := items[len(items)-1]
	if created.Event != "created" || !created.SnapshotAvailable || created.Sprint == nil || *created.Sprint != "待规划" {
		t.Fatalf("missing creation snapshot %+v", created)
	}
	query := url.QueryEscape(`[{"field":"iterationDelayCount","operator":"gte","value":3}]`)
	w := apiRequest(a, "GET", "/api/requirements?page=1&pageSize=15&projection=list&sort=iterationDelayCount&filters="+query, "u_viewer", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["total"] != float64(1) || !strings.Contains(w.Body.String(), `"iterationDelayCount":3`) {
		t.Fatalf("count filtering/paging %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementHistorySprintCompletionRenameAndRollback(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "History current", "进行中")
	planningSprint(t, a, "History next", "规划中")
	x := planningRequirement(t, a, `{"title":"to migrate","sprint":"History current"}`)
	done := planningRequirement(t, a, `{"title":"already done","sprint":"History current"}`)
	bulkFixtureExec(t, a, `UPDATE requirements SET status='已完成' WHERE id=?`, done.ID)
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/sprints/%d", s.ID), "u_admin", projectID, `{"name":"History renamed"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	x, _ = a.get(x.ID)
	if x.IterationDelayCount != 0 || x.Sprint != "History renamed" {
		t.Fatal("rename counted as a move")
	}
	items := readRequirementHistory(t, a, x.ID, "u_admin")
	if items[0].Event != "sprint_renamed" || items[0].IterationDelay {
		t.Fatal("missing rename history")
	}
	w = apiRequest(a, "POST", fmt.Sprintf("/api/sprints/%d/complete", s.ID), "u_admin", projectID, `{"targetSprint":"History next"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	x, _ = a.get(x.ID)
	done, _ = a.get(done.ID)
	if x.IterationDelayCount != 1 || done.IterationDelayCount != 0 || done.Sprint != "History renamed" {
		t.Fatal("completion moved/counts completed work")
	}
	items = readRequirementHistory(t, a, x.ID, "u_admin")
	if items[0].Event != "sprint_transferred" || !items[0].IterationDelay {
		t.Fatal("missing completion history")
	}
	planningSprint(t, a, "History rejected", "规划中")
	bulkFixtureExec(t, a, `CREATE TRIGGER reject_requirement_history BEFORE INSERT ON requirement_activity_history BEGIN SELECT RAISE(ABORT,'injected history failure'); END`)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"sprint":"History rejected"}`)
	if w.Code < 500 {
		t.Fatal("history write failure accepted")
	}
	after, _ := a.get(x.ID)
	if after.IterationDelayCount != 1 || after.Sprint != x.Sprint || len(readRequirementHistory(t, a, x.ID, "u_admin")) != len(items) {
		t.Fatal("failed history partially saved")
	}
}

func TestRequirementHistoryPermissionsAndScope(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"visible history"}`)
	path := fmt.Sprintf("/api/requirements/%d/activities", x.ID)
	for _, tc := range []struct {
		method, user, project string
		status                int
	}{{"GET", "u_viewer", projectID, 200}, {"POST", "u_admin", projectID, 405}, {"PATCH", "u_viewer", projectID, 403}, {"GET", "u_front", insightProjectID, 403}, {"GET", "u_admin", insightProjectID, 404}} {
		w := apiRequest(a, tc.method, path, tc.user, tc.project, "")
		if w.Code != tc.status {
			t.Fatalf("%+v got %d %s", tc, w.Code, w.Body.String())
		}
	}
	bulkFixtureExec(t, a, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES('foreign',?,?,'foreign actor','updated','FOREIGN HISTORY','2026-09-10T00:00:00Z')`, projectID, x.ID)
	bulkFixtureExec(t, a, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,'other project','updated','FOREIGN PROJECT HISTORY','2026-09-10T00:00:00Z')`, tenantID, insightProjectID, x.ID)
	w := apiRequest(a, "GET", path, "u_viewer", projectID, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "FOREIGN") {
		t.Fatal("cross-scope history leaked")
	}
	bulkFixtureExec(t, a, `UPDATE users SET operation_disabled=1 WHERE id='u_viewer'`)
	w = apiRequest(a, "GET", path, "u_viewer", projectID, "")
	if w.Code != 403 {
		t.Fatal("disabled member could read history")
	}
}

func TestRequirementHistoryResolvesLegacyAccountActorName(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"legacy actor display"}`)
	bulkFixtureExec(t, a, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,'u_admin','updated','更新了需求字段：description','2026-09-10T00:00:00Z')`, tenantID, projectID, x.ID)
	items := readRequirementHistory(t, a, x.ID, "u_viewer")
	var adminName string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&adminName); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Actor != "u_admin" {
			continue
		}
		if item.ActorName != adminName {
			t.Fatalf("legacy actor was not resolved to a member name: %+v", item)
		}
		return
	}
	t.Fatal("legacy actor history was not returned")
}

func TestRequirementHistoryIncludesCategoryChanges(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/requirement-categories", "u_admin", projectID, `{"name":"History category"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	categoryID := int64(jsonMap(t, w)["id"].(float64))
	x := planningRequirement(t, a, `{"title":"category history","category":"History category"}`)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirement-categories/%d", categoryID), "u_admin", projectID, `{"name":"History category renamed"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	items := readRequirementHistory(t, a, x.ID, "u_viewer")
	if items[0].Event != "category_changed" || len(items[0].Changes) != 1 || items[0].Changes[0].Field != "category" || items[0].Changes[0].Before != "History category" || items[0].Changes[0].After != "History category renamed" || items[0].IterationDelay {
		t.Fatalf("missing category history %+v", items[0])
	}
}

func TestRequirementHistoryRecoversOnlyProvenAuditAndIsIdempotent(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"recoverable"}`)
	bulkFixtureExec(t, a, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,'legacy actor','updated','更新了需求字段：sprint','2026-08-01T00:00:00Z')`, tenantID, projectID, x.ID)
	for _, record := range []struct{ before, after string }{{`{"sprint":"A","status":"开发中","title":"old"}`, `{"sprint":"B","status":"开发中","title":"new","password":"MUST_NOT_LEAK"}`}, {`{"sprint":"B"}`, `{"sprint":"C"}`}, {`{}`, `{"sprint":"Unknown origin"}`}, {`{"sprint":"待规划"}`, `{"sprint":"A"}`}, {`broken`, `{"sprint":"B"}`}} {
		bulkFixtureExec(t, a, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,'u_admin','requirement',?,'updated',?,?,'2026-08-02T00:00:00Z')`, tenantID, projectID, fmt.Sprint(x.ID), record.before, record.after)
	}
	bulkFixtureExec(t, a, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES('foreign',?,'u_admin','requirement',?,'updated','{"sprint":"A"}','{"sprint":"FOREIGN"}','2026-08-02T00:00:00Z')`, projectID, fmt.Sprint(x.ID))
	beforeAudits := tableCount(t, a, "audit_logs")
	for repeat := 0; repeat < 2; repeat++ {
		if err := a.migrateRequirementHistory(); err != nil {
			t.Fatal(err)
		}
	}
	x, _ = a.get(x.ID)
	if x.IterationDelayCount != 2 || tableCount(t, a, "audit_logs") != beforeAudits {
		t.Fatal("recovery fabricated counts or rewrote audit")
	}
	items := readRequirementHistory(t, a, x.ID, "u_viewer")
	recovered, unknown := 0, 0
	for _, entry := range items {
		if entry.Recovered {
			recovered++
		}
		if !entry.SnapshotAvailable {
			unknown++
			if entry.Sprint != nil || entry.Status != nil || len(entry.Changes) != 0 {
				t.Fatal("legacy activity assigned guessed values")
			}
		}
	}
	if recovered != 3 || unknown != 1 {
		t.Fatalf("recovered=%d unknown=%d", recovered, unknown)
	}
	w := apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/activities", x.ID), "u_viewer", projectID, "")
	if strings.Contains(w.Body.String(), "MUST_NOT_LEAK") || strings.Contains(w.Body.String(), "FOREIGN") {
		t.Fatal("recovered privileged/foreign snapshot leaked")
	}
}
