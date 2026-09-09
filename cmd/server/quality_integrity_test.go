package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func tableCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestQualityReadsReportUnavailableWithoutPanicking(t *testing.T) {
	a := testApp(t)
	a.db.Close()
	for _, tc := range []struct {
		name string
		run  func(http.ResponseWriter, *http.Request)
	}{
		{"defects", a.defects}, {"cases", a.testCases}, {"plans", a.testPlans}, {"executions", a.testExecutions},
		{"comments", func(w http.ResponseWriter, r *http.Request) { a.entityComments(w, r, "test_case", 1) }},
		{"activities", func(w http.ResponseWriter, r *http.Request) { a.entityActivities(w, "defect", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.run(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 503 {
				t.Fatalf("want 503, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestInvalidQualityAssociationsNeverCreateRows(t *testing.T) {
	a := testApp(t)
	for _, body := range []string{
		`{"title":"bad","steps":"do","expected":"ok","requirementId":7}`,
		`{"title":"bad","steps":"do","expected":"ok","owner":"不存在的成员"}`,
		`{"title":"bad","stepsDetail":[{"action":"","expected":"ok"}]}`,
		`{"title":"bad","steps":"do","expected":"ok","customFields":{"unknown":"x"}}`,
	} {
		before := tableCount(t, a, "test_cases")
		w := apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, body)
		if w.Code != 422 || tableCount(t, a, "test_cases") != before {
			t.Fatalf("invalid create left data: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{
		`{"name":"bad","caseIds":[9999]}`,
		`{"name":"bad","executorUserId":"not-a-member"}`,
		`{"name":"bad","sprint":"AIP 1.0 模型评测"}`,
		`{"name":"bad","startDate":"2026-09-10","endDate":"2026-09-01"}`,
	} {
		before := tableCount(t, a, "test_plans")
		executions := tableCount(t, a, "test_executions")
		w := apiRequest(a, "POST", "/api/test-plans", "u_admin", projectID, body)
		if w.Code != 422 || tableCount(t, a, "test_plans") != before || tableCount(t, a, "test_executions") != executions {
			t.Fatalf("invalid plan left data: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestCaseFieldsCopyAndStructuredStepsRemainConsistent(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, `{"objectType":"test_case","key":"risk_score","name":"风险分","type":"number","required":true}`)
	if w.Code != 201 {
		t.Fatalf("field: %d %s", w.Code, w.Body.String())
	}
	before := tableCount(t, a, "test_cases")
	w = apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, `{"title":"中文 English","steps":"do","expected":"ok"}`)
	if w.Code != 422 || tableCount(t, a, "test_cases") != before {
		t.Fatal("missing required field must not persist a case")
	}
	w = apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, `{"title":"中文 English","owner":"陈澄","stepsDetail":[{"action":"点击","expected":"成功"}],"customFields":{"risk_score":3}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var c TestCase
	json.Unmarshal(w.Body.Bytes(), &c)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/test-cases/%d", c.ID), "u_admin", projectID, `{"stepsDetail":[{"action":"修改动作","expected":"修改预期"}]}`)
	if w.Code != 200 {
		t.Fatalf("steps patch: %d %s", w.Code, w.Body.String())
	}
	saved, err := a.getTestCase(c.ID)
	if err != nil || saved.Steps != "1. 修改动作" || saved.Expected != "1. 修改预期" {
		t.Fatalf("plain and structured steps differ: %+v %v", saved, err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/test-cases/%d", c.ID), "u_admin", projectID, `{"title":"不应该保存","customFields":{"risk_score":"bad"}}`)
	after, _ := a.getTestCase(c.ID)
	if w.Code != 422 || after.Title != c.Title || after.CustomFields["risk_score"] != float64(3) {
		t.Fatalf("partial invalid case update: %d %+v", w.Code, after)
	}
	w = apiRequest(a, "POST", fmt.Sprintf("/api/test-cases/%d/copy", c.ID), "u_admin", projectID, `{}`)
	if w.Code != 201 {
		t.Fatalf("copy: %d %s", w.Code, w.Body.String())
	}
	var copy TestCase
	json.Unmarshal(w.Body.Bytes(), &copy)
	copied, _ := a.getTestCase(copy.ID)
	if copied.CustomFields["risk_score"] != float64(3) || copied.Steps != after.Steps {
		t.Fatalf("copy lost fields/steps: %+v", copied)
	}
}

func TestPlanTransactionsRollbackAndReassignOnlyUnexecutedCases(t *testing.T) {
	a := testApp(t)
	before := tableCount(t, a, "test_plans")
	_, err := a.db.Exec(`CREATE TRIGGER qa_fail_execution BEFORE INSERT ON test_executions BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/test-plans", "u_admin", projectID, `{"name":"失败回滚","caseIds":[1]}`)
	if w.Code != 500 || tableCount(t, a, "test_plans") != before {
		t.Fatalf("failed transaction persisted plan: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`DROP TRIGGER qa_fail_execution`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", "/api/test-plans", "u_admin", projectID, `{"name":"分配计划","caseIds":[1,1,2],"executorUserId":"u_qa"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM test_executions WHERE plan_id=?`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("duplicate cases created %d executions", n)
	}
	if _, err := a.db.Exec(`UPDATE test_executions SET status='通过' WHERE plan_id=? AND case_id=1`, id); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/test-plans/%d", id), "u_admin", projectID, `{"executorUserId":"u_pm"}`)
	if w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	for _, c := range []struct {
		id   int
		want string
	}{{1, "u_qa"}, {2, "u_pm"}} {
		var uid string
		a.db.QueryRow(`SELECT executor_user_id FROM test_executions WHERE plan_id=? AND case_id=?`, id, c.id).Scan(&uid)
		if uid != c.want {
			t.Fatalf("case %d executor %s want %s", c.id, uid, c.want)
		}
	}
}

func TestQualitySubresourcesRejectMissingAndForeignEntities(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/api/test-cases/9999/comments", "/api/defects/9999/comments", "/api/defects/9999/activities", "/api/test-cases/9999"} {
		method := "GET"
		body := ""
		if path[len(path)-8:] == "comments" {
			method = "POST"
			body = `{"body":"no orphan comments"}`
		}
		w := apiRequest(a, method, path, "u_admin", projectID, body)
		if w.Code != 404 {
			t.Fatalf("missing entity %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "POST", "/api/test-cases/1/comments", "u_admin", insightProjectID, `{"body":"foreign"}`)
	if w.Code != 404 {
		t.Fatalf("foreign comment allowed: %d %s", w.Code, w.Body.String())
	}
}

func TestDefectInvalidStatusCannotPersistCustomFields(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, `{"objectType":"defect","key":"risk_score","name":"风险分","type":"number"}`)
	if w.Code != 201 {
		t.Fatalf("field: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", "/api/defects/1", "u_admin", projectID, `{"status":"已关闭","customFields":{"risk_score":9}}`)
	if w.Code != 422 {
		t.Fatalf("status accepted: %d %s", w.Code, w.Body.String())
	}
	if _, ok := a.customFields("defect", 1)["risk_score"]; ok {
		t.Fatal("rejected status update persisted custom field")
	}
}

func TestDefectTypedPatchCannotPoisonStoredNumbers(t *testing.T) {
	a := testApp(t)
	before, err := a.getDefect(1)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	for _, body := range []string{
		`{"progress":"abc"}`, `{"progress":1.5}`, `{"progress":null}`, `{"progress":true}`,
		`{"estimatedHours":"NaN"}`, `{"estimatedHours":-1}`, `{"actualHours":[]}`,
		`{"title":"  "}`, `{"severity":"invalid"}`, `{"priority":"P9"}`, `{"customFields":[]}`,
	} {
		w := apiRequest(a, "PATCH", "/api/defects/1", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatalf("invalid patch %s: %d %s", body, w.Code, w.Body.String())
		}
		after, err := a.getDefect(1)
		if err != nil {
			t.Fatalf("invalid patch poisoned row: %v", err)
		}
		afterJSON, _ := json.Marshal(after)
		if string(beforeJSON) != string(afterJSON) {
			t.Fatalf("invalid patch changed row: %s", body)
		}
		w = apiRequest(a, "GET", "/api/defects", "u_admin", projectID, "")
		if w.Code != 200 {
			t.Fatalf("list broken after %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "PATCH", "/api/defects/1", "u_admin", projectID, `{"progress":35,"estimatedHours":0.25,"actualHours":0,"severity":"","priority":""}`)
	if w.Code != 200 {
		t.Fatalf("valid numeric patch: %d %s", w.Code, w.Body.String())
	}
	after, _ := a.getDefect(1)
	if after.Progress != 35 || after.EstimatedHours != 0.25 || after.ActualHours != 0 || after.Severity != "一般" || after.Priority != "P2" {
		t.Fatalf("typed values/defaults were not persisted: %+v", after)
	}
}

func TestDefectCreationRollsBackEveryWrite(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"code", `CREATE TRIGGER qa_fail_defect_code BEFORE UPDATE OF code ON defects BEGIN SELECT RAISE(ABORT,'injected code failure'); END`},
		{"fields", `CREATE TRIGGER qa_fail_defect_fields BEFORE INSERT ON field_values WHEN NEW.object_type='defect' BEGIN SELECT RAISE(ABORT,'injected field failure'); END`},
		{"activity", `CREATE TRIGGER qa_fail_defect_activity BEFORE INSERT ON entity_activities WHEN NEW.object_type='defect' BEGIN SELECT RAISE(ABORT,'injected activity failure'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			counts := map[string]int{}
			for _, table := range []string{"defects", "field_values", "entity_activities", "notification_outbox", "user_notifications"} {
				counts[table] = tableCount(t, a, table)
			}
			if _, err := a.db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			w := apiRequest(a, "POST", "/api/defects", "u_admin", projectID, `{"title":"不应留下半条缺陷","assignee":"陈澄","customFields":{"escape_stage":"生产"}}`)
			if w.Code != 500 {
				t.Fatalf("injected failure: %d %s", w.Code, w.Body.String())
			}
			for table, before := range counts {
				if after := tableCount(t, a, table); after != before {
					t.Fatalf("%s changed after rollback: %d -> %d", table, before, after)
				}
			}
		})
	}
}

func TestDefectProjectAssociationsAndExecutionSourceAreProtected(t *testing.T) {
	a := testApp(t)
	var foreignRequirement int64
	var foreignSprint string
	if err := a.db.QueryRow(`SELECT id FROM requirements WHERE project_id=? LIMIT 1`, insightProjectID).Scan(&foreignRequirement); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT name FROM sprints WHERE project_id=? LIMIT 1`, insightProjectID).Scan(&foreignSprint); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		fmt.Sprintf(`{"title":"foreign requirement","requirementId":%d}`, foreignRequirement),
		fmt.Sprintf(`{"title":"foreign sprint","sprint":%q}`, foreignSprint),
		`{"title":"forged source","sourceExecutionId":1}`,
		`{"title":"missing source","sourceExecutionId":9999}`,
	} {
		before := tableCount(t, a, "defects")
		w := apiRequest(a, "POST", "/api/defects", "u_admin", projectID, body)
		if w.Code != 422 || tableCount(t, a, "defects") != before {
			t.Fatalf("bad create association persisted: %d %s", w.Code, w.Body.String())
		}
	}
	before, _ := a.getDefect(1)
	for _, body := range []string{
		fmt.Sprintf(`{"requirementId":%d}`, foreignRequirement),
		fmt.Sprintf(`{"sprint":%q}`, foreignSprint),
		`{"sourceExecutionId":1}`, `{"sourceExecutionId":null}`,
	} {
		w := apiRequest(a, "PATCH", "/api/defects/1", "u_admin", projectID, body)
		if w.Code != 422 {
			t.Fatalf("bad patch association accepted: %d %s", w.Code, w.Body.String())
		}
		after, err := a.getDefect(1)
		if err != nil || after.Sprint != before.Sprint || !sameNullableID(after.RequirementID, before.RequirementID) || !sameNullableID(after.SourceExecutionID, before.SourceExecutionID) {
			t.Fatalf("association changed: %+v %v", after, err)
		}
	}
}

func TestDefectUsesScopedStableMemberIDs(t *testing.T) {
	a := testApp(t)
	const sharedName = "同名测试成员"
	for _, fixture := range []struct{ id, project string }{{"qa_foreign_duplicate", insightProjectID}, {"qa_local_duplicate", projectID}} {
		if _, err := a.db.Exec(`INSERT INTO users(id,tenant_id,name,email,active) VALUES(?,?,?,?,1)`, fixture.id, tenantID, sharedName, fixture.id+"@example.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,'member','active','now','now')`, tenantID, fixture.id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'qa','now','now')`, tenantID, fixture.project, fixture.id); err != nil {
			t.Fatal(err)
		}
	}
	body := fmt.Sprintf(`{"title":"同名不得跨项目绑定","assignee":%q,"verifier":%q,"assigneeUserId":"qa_foreign_duplicate","verifierUserId":"qa_foreign_duplicate"}`, sharedName, sharedName)
	w := apiRequest(a, "POST", "/api/defects", "u_admin", projectID, body)
	if w.Code != 422 {
		t.Fatalf("foreign explicit ID accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/defects", "u_admin", projectID, strings.ReplaceAll(body, "qa_foreign_duplicate", "qa_local_duplicate"))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	for _, target := range []int64{id, 1} {
		w = apiRequest(a, "PATCH", fmt.Sprintf("/api/defects/%d", target), "u_admin", projectID, fmt.Sprintf(`{"assignee":%q,"verifier":%q,"assigneeUserId":"qa_foreign_duplicate","verifierUserId":"qa_foreign_duplicate"}`, sharedName, sharedName))
		if w.Code != 422 {
			t.Fatalf("foreign explicit patch ID accepted: %d %s", w.Code, w.Body.String())
		}
		w = apiRequest(a, "PATCH", fmt.Sprintf("/api/defects/%d", target), "u_admin", projectID, `{"assigneeUserId":"qa_local_duplicate","verifierUserId":"qa_local_duplicate"}`)
		if w.Code != 200 {
			t.Fatalf("patch: %d %s", w.Code, w.Body.String())
		}
		d, err := a.getDefect(target)
		if err != nil || d.AssigneeUserID != "qa_local_duplicate" || d.VerifierUserID != "qa_local_duplicate" {
			t.Fatalf("wrong scoped IDs: %+v %v", d, err)
		}
	}
	// A later same-name project join must not silently rebind an existing owner.
	if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,'qa_foreign_duplicate','qa','now','now')`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/defects/%d", id), "u_admin", projectID, fmt.Sprintf(`{"description":"unrelated edit","assignee":%q}`, sharedName))
	if w.Code != 200 {
		t.Fatalf("preserve historic ID: %d %s", w.Code, w.Body.String())
	}
	d, _ := a.getDefect(id)
	if d.AssigneeUserID != "qa_local_duplicate" {
		t.Fatalf("same-name member rebound: %+v", d)
	}
	w = apiRequest(a, "POST", "/api/defects", "u_admin", projectID, fmt.Sprintf(`{"title":"同名须明确选人","assignee":%q}`, sharedName))
	if w.Code != 422 {
		t.Fatalf("ambiguous new owner accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/defects", "u_admin", projectID, body)
	if w.Code != 201 || jsonMap(t, w)["assigneeUserId"] != "qa_foreign_duplicate" {
		t.Fatalf("explicit same-name ID not honored: %d %s", w.Code, w.Body.String())
	}
}

func TestHistoricalQualityMembersDoNotBlockUnrelatedEdits(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, `{"title":"历史用例","steps":"do","expected":"ok","owner":"苏禾"}`)
	if w.Code != 201 {
		t.Fatalf("case create: %d %s", w.Code, w.Body.String())
	}
	caseID := int64(jsonMap(t, w)["id"].(float64))
	w = apiRequest(a, "POST", "/api/test-plans", "u_admin", projectID, `{"name":"历史计划","owner":"苏禾","executorUserId":"u_qa"}`)
	if w.Code != 201 {
		t.Fatalf("plan create: %d %s", w.Code, w.Body.String())
	}
	planID := int64(jsonMap(t, w)["id"].(float64))
	w = apiRequest(a, "POST", "/api/defects", "u_admin", projectID, `{"title":"历史缺陷","assignee":"苏禾","verifier":"苏禾"}`)
	if w.Code != 201 {
		t.Fatalf("defect create: %d %s", w.Code, w.Body.String())
	}
	defectID := int64(jsonMap(t, w)["id"].(float64))
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_qa'`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, body string }{
		{fmt.Sprintf("/api/test-cases/%d", caseID), `{"status":"已废弃","owner":"苏禾"}`},
		{fmt.Sprintf("/api/test-plans/%d", planID), `{"scope":"仅改范围","owner":"苏禾","executorUserId":"u_qa"}`},
		{fmt.Sprintf("/api/defects/%d", defectID), `{"description":"仅改说明","assignee":"苏禾","verifier":"苏禾"}`},
	} {
		w = apiRequest(a, "PATCH", tc.path, "u_admin", projectID, tc.body)
		if w.Code != 200 {
			t.Fatalf("historic member blocked edit %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	d, _ := a.getDefect(defectID)
	c, _ := a.getTestCase(caseID)
	if d.AssigneeUserID != "u_qa" || d.VerifierUserID != "u_qa" || c.OwnerUserID != "u_qa" {
		t.Fatalf("historic IDs lost: %+v %+v", d, c)
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/test-cases", `{"title":"new","steps":"do","expected":"ok","owner":"苏禾"}`},
		{"/api/test-plans", `{"name":"new","executorUserId":"u_qa"}`},
		{"/api/defects", `{"title":"new","assignee":"苏禾"}`},
	} {
		w = apiRequest(a, "POST", tc.path, "u_admin", projectID, tc.body)
		if w.Code != 422 {
			t.Fatalf("disabled new assignee accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

type qualityBeforeRead struct {
	once   sync.Once
	before func()
	reader io.Reader
}

func (r *qualityBeforeRead) Read(p []byte) (int, error) { r.once.Do(r.before); return r.reader.Read(p) }

func TestPlanPatchRejectsStaleSnapshotWithoutTimestampAssumptions(t *testing.T) {
	a := testApp(t)
	const untouchedTimestamp = "2026-09-03T10:00:00Z"
	if _, err := a.db.Exec(`UPDATE test_plans SET start_date='2026-09-01',end_date='2026-09-10',updated_at=? WHERE id=1`, untouchedTimestamp); err != nil {
		t.Fatal(err)
	}
	activities := tableCount(t, a, "entity_activities")
	// The handler reads the old row before decoding the body. A competing
	// successful writer is injected exactly there, without timing-dependent
	// goroutines and without changing updated_at (both writes can share a second).
	reader := &qualityBeforeRead{reader: strings.NewReader(`{"endDate":"2026-09-05"}`), before: func() {
		if _, err := a.db.Exec(`UPDATE test_plans SET start_date='2026-09-08' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
	}}
	w := httptest.NewRecorder()
	a.patchPlanAtomic(w, httptest.NewRequest("PATCH", "/api/test-plans/1", reader), 1)
	if w.Code != 409 {
		t.Fatalf("stale dates accepted: %d %s", w.Code, w.Body.String())
	}
	var start, end, updated string
	if err := a.db.QueryRow(`SELECT start_date,end_date,updated_at FROM test_plans WHERE id=1`).Scan(&start, &end, &updated); err != nil {
		t.Fatal(err)
	}
	if start != "2026-09-08" || end != "2026-09-10" || updated != untouchedTimestamp || tableCount(t, a, "entity_activities") != activities {
		t.Fatalf("stale patch wrote partial data: %s %s %s", start, end, updated)
	}
	w = apiRequest(a, "PATCH", "/api/test-plans/1", "u_admin", projectID, `{"endDate":"2026-09-11"}`)
	if w.Code != 200 {
		t.Fatalf("fresh valid patch rejected: %d %s", w.Code, w.Body.String())
	}
}
