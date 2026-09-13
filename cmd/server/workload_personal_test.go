package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func workloadPersonalRead(t *testing.T, a *App, month string) (WorkloadPersonalReport, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.personalWorkloadReport(w, httptest.NewRequest("GET", "/api/reports/workload/personal?month="+month, nil))
	var out WorkloadPersonalReport
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, w
}

func workloadTeamRead(t *testing.T, a *App, month string) (WorkloadTeamReport, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.teamWorkloadReport(w, httptest.NewRequest("GET", "/api/reports/workload/team?month="+month, nil))
	var out WorkloadTeamReport
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, w
}

func TestPersonalWorkloadIsSelfOnlyAndDoesNotGrantCompanyReport(t *testing.T) {
	a := testApp(t)
	a.user = "u_front"
	workloadSprintFixture(t, a, projectID, "Personal only", "2050-02-20")
	workloadRequirementFixture(t, a, projectID, "Personal only", "规划中", `{"frontend":{"userId":"u_front","value":7},"backend":{"userId":"u_back","value":3}}`)
	personal, w := workloadPersonalRead(t, a, "2050-02")
	if w.Code != 200 || personal.Scope.Type != "personal" || personal.Person.UserID != "u_front" || personal.Person.Weight != 7 || personal.Scope.ProjectCount == 0 {
		t.Fatalf("unsafe personal result: %d %+v %s", w.Code, personal, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "u_back") || strings.Contains(w.Body.String(), "departments") || strings.Contains(w.Body.String(), "people") {
		t.Fatalf("personal response exposed company directory: %s", w.Body.String())
	}
	// Query parameters cannot select another member. The authenticated principal
	// is the only identity used by the personal endpoint.
	w = httptest.NewRecorder()
	a.personalWorkloadReport(w, httptest.NewRequest("GET", "/api/reports/workload/personal?month=2050-02&user=u_back", nil))
	var forged WorkloadPersonalReport
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &forged) != nil || forged.Person.UserID != "u_front" {
		t.Fatalf("personal selector was forgeable: %d %s", w.Code, w.Body.String())
	}
	_, enterprise := workloadRead(t, a, "2050-02")
	if enterprise.Code != 403 {
		t.Fatalf("personal access granted enterprise report: %d %s", enterprise.Code, enterprise.Body.String())
	}
	_, team := workloadTeamRead(t, a, "2050-02")
	if team.Code != 403 {
		t.Fatalf("ordinary member read team workload: %d %s", team.Code, team.Body.String())
	}
}

func TestPersonalWorkloadOnlyIncludesCurrentlyAuthorizedProjects(t *testing.T) {
	a := testApp(t)
	a.user = "u_front"
	// u_front is not currently a member of the insight project. Its old work
	// must not affect the personal report, even if it still references their ID.
	workloadExec(t, a, `DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, insightProjectID)
	workloadSprintFixture(t, a, projectID, "Visible personal", "2050-02-10")
	workloadSprintFixture(t, a, insightProjectID, "Revoked personal", "2050-02-11")
	workloadRequirementFixture(t, a, projectID, "Visible personal", "规划中", `{"frontend":{"userId":"u_front","value":5}}`)
	workloadRequirementFixture(t, a, insightProjectID, "Revoked personal", "规划中", `{"frontend":{"userId":"u_front","value":99}}`)
	personal, w := workloadPersonalRead(t, a, "2050-02")
	if w.Code != 200 || personal.Person.Weight != 5 || personal.Person.RequirementCount != 1 || strings.Contains(w.Body.String(), "99") {
		t.Fatalf("revoked project leaked into personal report: %d %+v %s", w.Code, personal, w.Body.String())
	}
}

func TestTeamWorkloadUsesExplicitLeadRolePrimaryDepartmentAndLeadProjects(t *testing.T) {
	a := testApp(t)
	a.user = "u_front_lead"
	// The seed establishes u_front_lead as a frontend_lead in the frontend
	// department. u_front is an active member of that same real department and
	// project. u_back remains outside this department.
	workloadSprintFixture(t, a, projectID, "Lead visible", "2050-02-10")
	workloadSprintFixture(t, a, insightProjectID, "Lead excluded", "2050-02-11")
	workloadRequirementFixture(t, a, projectID, "Lead visible", "规划中", `{"frontend":{"userIds":["u_front_lead","u_front"],"value":10}}`)
	workloadRequirementFixture(t, a, insightProjectID, "Lead excluded", "规划中", `{"frontend":{"userId":"u_front","value":99}}`)
	// A member can work in another project, but without an explicit lead role
	// for that project the lead's team report may not reveal it.
	workloadExec(t, a, `INSERT OR IGNORE INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'frontend','now','now')`, tenantID, insightProjectID, "u_front")
	team, w := workloadTeamRead(t, a, "2050-02")
	if w.Code != 200 || team.Scope.Type != "team" || team.Scope.ProjectCount != 1 || strings.Contains(w.Body.String(), "99") {
		t.Fatalf("team scope was not restricted to lead projects: %d %+v %s", w.Code, team, w.Body.String())
	}
	ids := map[string]WorkloadPerson{}
	for _, person := range team.People {
		ids[person.UserID] = person
	}
	if len(ids) != 2 || ids["u_front"].Weight != 5 || ids["u_front_lead"].Weight != 5 || ids["u_back"].UserID != "" {
		t.Fatalf("team membership escaped its primary department: %+v", team.People)
	}
	// Removing the explicit lead role must immediately close the team endpoint;
	// department membership alone never creates group-report authority.
	workloadExec(t, a, `UPDATE project_members SET role='frontend' WHERE tenant_id=? AND project_id=? AND user_id='u_front_lead'`, tenantID, projectID)
	_, denied := workloadTeamRead(t, a, "2050-02")
	if denied.Code != 403 {
		t.Fatalf("department membership forged lead access: %d %s", denied.Code, denied.Body.String())
	}
}

func TestPersonalWorkloadIsRoutedForARegularAuthenticatedMember(t *testing.T) {
	a := testApp(t)
	w, cookie := loginRequest(a, "shenxing@devflow.local", seedPassword)
	if w.Code != 200 || cookie == nil {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/api/reports/workload/personal?month=2050-02", nil)
	r.AddCookie(cookie)
	response := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(response, r)
	var out WorkloadPersonalReport
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || out.Scope.Type != "personal" || out.Person.UserID != "u_front" || response.Header().Get("Cache-Control") == "" {
		t.Fatalf("personal route not available to regular member: %d %s", response.Code, response.Body.String())
	}
}
