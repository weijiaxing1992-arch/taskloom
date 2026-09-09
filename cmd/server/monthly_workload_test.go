package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func workloadExec(t *testing.T, a *App, query string, args ...any) {
	t.Helper()
	if _, err := a.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func workloadSprintFixture(t *testing.T, a *App, project, name, end string) {
	t.Helper()
	workloadExec(t, a, `INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,created_at,updated_at)VALUES(?,?,'WL',?,'2050-01-01',?,'now','now')`, tenantID, project, name, end)
}
func workloadRequirementFixture(t *testing.T, a *App, project, sprint, status, weights string) int64 {
	t.Helper()
	res, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES(?,?,'WL','Monthly test',?,?,?,'now','now')`, tenantID, project, sprint, status, weights)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func workloadRead(t *testing.T, a *App, month string) (WorkloadReport, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	a.workloadReport(w, httptest.NewRequest("GET", "/api/reports/workload?month="+month, nil))
	var result WorkloadReport
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result, w
}
func workloadPerson(t *testing.T, result WorkloadReport, id string) WorkloadPerson {
	t.Helper()
	for _, p := range result.People {
		if p.UserID == id {
			return p
		}
	}
	t.Fatalf("missing person %s: %+v", id, result.People)
	return WorkloadPerson{}
}
func workloadDepartment(t *testing.T, result WorkloadReport, id string) WorkloadDepartment {
	t.Helper()
	for _, department := range result.Departments {
		if department.ID == id {
			return department
		}
	}
	t.Fatalf("missing department %s: %+v", id, result.Departments)
	return WorkloadDepartment{}
}

func TestWorkloadStrictCalendarMonths(t *testing.T) {
	for _, month := range []string{"2050-00", "2050-13", "2050-2", " 2050-02", "2050-02-01", "0000-01", "9999-12"} {
		if _, _, err := workloadMonthRange(month); err == nil {
			t.Fatalf("accepted invalid %q", month)
		}
	}
	start, end, err := workloadMonthRange("2048-02")
	if err != nil || start != "2048-02-01" || end != "2048-03-01" {
		t.Fatalf("bad leap range %s %s %v", start, end, err)
	}
	a := testApp(t)
	for _, date := range []string{"2050-01-31", "2050-02-01", "2050-02-28", "2050-03-01", ""} {
		name := "Date " + date
		workloadSprintFixture(t, a, projectID, name, date)
		workloadRequirementFixture(t, a, projectID, name, "规划中", `{"frontend":{"userId":"u_front","value":5}}`)
	}
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 || r.SprintCount != 2 || r.Totals.RequirementCount != 2 || r.Totals.Weight != 10 {
		t.Fatalf("month boundary: %d %+v %s", w.Code, r, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private report must not cache")
	}
}
func TestWorkloadSplitsRoleWeightButDeduplicatesCompanyDepartmentAndPeople(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Monthly team", "2050-02-28")
	workloadExec(t, a, `DELETE FROM department_memberships WHERE tenant_id=? AND user_id IN('u_front','u_back')`, tenantID)
	workloadExec(t, a, `INSERT INTO departments(id,tenant_id,name,code,external_id,created_at,updated_at)VALUES('wl-main',?,'Shared','WL-MAIN','wl-main','now','now'),('wl-secondary',?,'Secondary','WL-SECOND','wl-secondary','now','now')`, tenantID, tenantID)
	for _, id := range []string{"u_front", "u_back"} {
		workloadExec(t, a, `INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,joined_at,updated_at)VALUES(?,'wl-main',?,1,'now','now'),(?,'wl-secondary',?,0,'now','now')`, tenantID, id, tenantID, id)
	}
	workloadRequirementFixture(t, a, projectID, "Monthly team", "已上线", `{"frontend":{"userIds":["u_front","u_back","u_front"],"value":100},"backend":{"userIds":["u_front"],"value":200},"algorithm":{"userIds":["u_back"],"value":null}}`)
	workloadExec(t, a, `INSERT INTO defects(tenant_id,project_id,code,title,sprint,assignee_user_id,discipline,created_at,updated_at)VALUES(?,?,'WL-BUG','Monthly bug','Monthly team','u_front','frontend','now','now')`, tenantID, projectID)
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	front := workloadPerson(t, r, "u_front")
	back := workloadPerson(t, r, "u_back")
	if front.Weight != 250 || back.Weight != 50 || r.Totals.Weight != 300 {
		t.Fatalf("bad split: %+v %+v %+v", front, back, r.Totals)
	}
	if front.RequirementCount != 1 || front.ShippedRequirementCount != 1 || r.Totals.ShippedRequirementCount != 1 || r.Totals.DefectCount != 1 {
		t.Fatal("work items counted multiple times")
	}
	main := workloadDepartment(t, r, "wl-main")
	if main.Weight != 300 || main.RequirementCount != 1 || main.MemberCount != 2 {
		t.Fatalf("department duplication/primary: %+v", r.Departments)
	}
	secondary := workloadDepartment(t, r, "wl-secondary")
	if secondary.Weight != 0 || secondary.RequirementCount != 0 || secondary.MemberCount != 0 {
		t.Fatalf("idle department omitted or incorrectly attributed: %+v", secondary)
	}
	if back.UnestimatedRoleCount != 1 || r.Totals.UnestimatedRoleCount != 1 {
		t.Fatal("missing estimate was silently zeroed")
	}
}
func TestWorkloadNullZeroUnassignedAndHistoricalSameNameMembers(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Estimates", "2050-02-20")
	workloadExec(t, a, `UPDATE users SET name='Same name',active=CASE id WHEN 'u_back' THEN 0 ELSE 1 END WHERE id IN('u_front','u_back')`)
	workloadExec(t, a, `DELETE FROM department_memberships WHERE tenant_id=? AND user_id='u_back'`, tenantID)
	workloadRequirementFixture(t, a, projectID, "Estimates", "规划中", `{"frontend":{"userIds":["u_front"],"value":0},"backend":{"userIds":["u_back"],"value":null},"ui":{"value":20}}`)
	workloadRequirementFixture(t, a, projectID, "Estimates", "规划中", `{}`)
	workloadExec(t, a, `INSERT INTO defects(tenant_id,project_id,code,title,sprint,assignee,created_at,updated_at)VALUES(?,?,'WL','No ID','Estimates','Same name','now','now')`, tenantID, projectID)
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	front := workloadPerson(t, r, "u_front")
	back := workloadPerson(t, r, "u_back")
	if front.EstimatedRoleCount != 1 || front.Weight != 0 || back.Active || back.UnestimatedRoleCount != 1 || back.DepartmentID != "" {
		t.Fatalf("historical or zero lost: %+v %+v", front, back)
	}
	if r.UnassignedWeight != 20 || r.UnassignedRequirementCount != 1 || r.UnestimatedRequirementCount != 1 || r.UnassignedDefectCount != 1 || r.Totals.DefectCount != 1 || front.DefectCount != 0 {
		t.Fatalf("silent assignment or loss: %+v", r)
	}
}
func TestWorkloadDoneRequiresWorkflowEndAndCrossProjectTenantScope(t *testing.T) {
	a := testApp(t)
	for _, project := range []string{projectID, insightProjectID} {
		workloadSprintFixture(t, a, project, "Shared name", "2050-02-10")
	}
	workloadExec(t, a, `INSERT INTO requirement_statuses(tenant_id,project_id,key,name,color,category,created_at,updated_at)VALUES(?,?,'custom_release','Custom release','#123456','done','now','now'),(?,?,'intermediate_done','Intermediate','#123456','done','now','now')`, tenantID, projectID, tenantID, projectID)
	workloadExec(t, a, `UPDATE requirement_workflows SET end_statuses_json='["custom_release","已上线"]' WHERE tenant_id=? AND project_id=?`, tenantID, projectID)
	workloadRequirementFixture(t, a, projectID, "Shared name", "custom_release", `{"frontend":{"userId":"u_front","value":1}}`)
	workloadRequirementFixture(t, a, projectID, "Shared name", "intermediate_done", `{"frontend":{"userId":"u_front","value":1}}`)
	workloadRequirementFixture(t, a, insightProjectID, "Shared name", "已上线", `{"frontend":{"userId":"u_front","value":1}}`)
	workloadExec(t, a, `INSERT INTO tenants(id,name)VALUES('wl-foreign','Foreign');INSERT INTO projects(id,tenant_id,name,code)VALUES('wl-foreign-project','wl-foreign','Secret','SECRET');INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,created_at,updated_at)VALUES('wl-foreign','wl-foreign-project','S','Secret','2050-02-01','2050-02-28','now','now');INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES('wl-foreign','wl-foreign-project','R','Secret','Secret','已上线','{"frontend":{"value":999999}}','now','now')`)
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 || r.Scope.ProjectCount != 2 || r.Totals.Weight != 3 || r.Totals.RequirementCount != 3 || r.Totals.ShippedRequirementCount != 2 {
		t.Fatalf("wrong scope/end definition: %d %+v %s", w.Code, r, w.Body.String())
	}
}
func TestWorkloadLegacySprintAliasesNeverDoubleCountAmbiguousNames(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Q3 Alpha", "2050-02-01")
	workloadSprintFixture(t, a, projectID, "Q3 Beta", "2050-03-01")
	workloadSprintFixture(t, a, projectID, "Exact", "2050-02-02")
	workloadSprintFixture(t, a, projectID, "Exact other", "2050-03-03")
	workloadSprintFixture(t, a, projectID, "Legacy unique", "2050-02-05")
	for _, name := range []string{"Q3", "Exact", "Legacy"} {
		workloadRequirementFixture(t, a, projectID, name, "规划中", `{"frontend":{"userId":"u_front","value":10}}`)
	}
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 || r.Totals.Weight != 20 || r.Totals.RequirementCount != 2 || r.AmbiguousSprintItemCount != 1 {
		t.Fatalf("bad alias: %d %+v", w.Code, r)
	}
}
func TestWorkloadOrganizationPermissionCannotBeForgedByProjectRole(t *testing.T) {
	a := testApp(t)
	a.user = "u_front"
	workloadExec(t, a, `UPDATE project_members SET role='project_admin' WHERE tenant_id=? AND user_id='u_front'`, tenantID)
	_, w := workloadRead(t, a, "2050-02")
	if w.Code != 403 {
		t.Fatalf("project admin read company: %d %s", w.Code, w.Body.String())
	}
	workloadExec(t, a, `INSERT INTO organization_groups(id,tenant_id,name,created_at,updated_at)VALUES('wl-reporters',?,'Reporters','now','now')`, tenantID)
	workloadExec(t, a, `INSERT INTO organization_group_permissions(tenant_id,group_id,permission)VALUES(?,'wl-reporters','reports.view')`, tenantID)
	workloadExec(t, a, `INSERT INTO organization_group_members(tenant_id,group_id,user_id)VALUES(?,'wl-reporters','u_front')`, tenantID)
	_, w = workloadRead(t, a, "2050-02")
	if w.Code != 200 {
		t.Fatalf("delegated report denied: %d %s", w.Code, w.Body.String())
	}
	workloadExec(t, a, `UPDATE tenant_memberships SET status='inactive' WHERE tenant_id=? AND user_id='u_front'`, tenantID)
	_, w = workloadRead(t, a, "2050-02")
	if w.Code != 403 {
		t.Fatal("inactive delegated member allowed")
	}
	a.user = "u_admin"
	_, w = workloadRead(t, a, "invalid")
	if w.Code != 422 {
		t.Fatal("invalid month accepted")
	}
}
func TestWorkloadCorruptDataNeverBecomesZeroOrLeaksAnotherTenant(t *testing.T) {
	for i, raw := range []string{`{"frontend":{"value":"bad"}}`, `{"frontend":{"value":"20"}}`, `{"frontend":{"value":-1}}`, `{"unknown":{"value":1}}`, `{"frontend":{"value":20,"userIds":["foreign"]}}`} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			a := testApp(t)
			workloadSprintFixture(t, a, projectID, "Corrupt", "2050-02-02")
			workloadRequirementFixture(t, a, projectID, "Corrupt", "规划中", raw)
			_, w := workloadRead(t, a, "2050-02")
			if w.Code != 503 || strings.Contains(w.Body.String(), "foreign") {
				t.Fatalf("unsafe partial report %d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestWorkloadFullPoolDecimalAccumulationAndReadFailure(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Large", "2050-02-28")
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		_, err = tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,sprint,status,role_weights_json,created_at,updated_at)VALUES(?,?,'W','Large','Large','规划中','{"frontend":{"userIds":["u_front","u_back","u_algo"],"value":0.3}}','now','now')`, tenantID, projectID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	r, w := workloadRead(t, a, "2050-02")
	if w.Code != 200 || r.Totals.RequirementCount != 1001 || r.Totals.Weight != 300.3 || workloadPerson(t, r, "u_front").Weight != 100.1 {
		t.Fatalf("truncated/drifting pool: %d %+v", w.Code, r.Totals)
	}
	a.db.Close()
	_, w = workloadRead(t, a, "2050-02")
	if w.Code != 503 {
		t.Fatal("read failure returned empty success")
	}
}
