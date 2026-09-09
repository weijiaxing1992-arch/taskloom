package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func planningRequirement(t *testing.T, a *App, body string) Requirement {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", a.pid(), body)
	if w.Code != 201 {
		t.Fatalf("create requirement: %d %s", w.Code, w.Body.String())
	}
	var x Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func patchPlanningRequirement(t *testing.T, a *App, id int64, body string) Requirement {
	t.Helper()
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", id), "u_admin", a.pid(), body)
	if w.Code != 200 {
		t.Fatalf("patch requirement: %d %s", w.Code, w.Body.String())
	}
	var x Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func planningSprint(t *testing.T, a *App, name, status string) Sprint {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", a.pid(), jsonText(map[string]any{"name": name, "status": status, "startDate": "2026-09-03", "endDate": "2026-09-15"}))
	if w.Code != 201 {
		t.Fatalf("create sprint: %d %s", w.Code, w.Body.String())
	}
	var s Sprint
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRequirementPlanningFieldsRoundTripAndComputedTotal(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"跨角色需求权重","tags":"协作,发布","tagColors":{"协作":"#12abEF","发布":"#654321"},"remarks":"产品评审后补充","createdAt":"1900-01-01","weightTotal":999999,"roleWeights":{"frontend":{"userId":"u_front","value":10},"backend":{"userId":"u_back","value":20.5},"algorithm":{"userId":"u_algo","value":0},"ui":{"userId":"u_ui","value":null},"product":{"userId":"u_pm","value":3.5}}}`)
	if x.WeightTotal != 34 || x.CreatedAt == "1900-01-01" || x.CreatedAt == "" || x.TagColors["协作"] != "#12ABEF" || x.Remarks != "产品评审后补充" {
		t.Fatalf("incorrect planning fields: %+v", x)
	}
	if len(x.RoleWeights) != 5 || x.RoleWeights["algorithm"].Value == nil || *x.RoleWeights["algorithm"].Value != 0 || x.RoleWeights["ui"].Value != nil {
		t.Fatalf("null/zero lost: %#v", x.RoleWeights)
	}
	stored, err := a.get(x.ID)
	if err != nil || stored.WeightTotal != 34 || stored.CreatedAt != x.CreatedAt || stored.Tags != "协作,发布" {
		t.Fatalf("detail round trip: %+v %v", stored, err)
	}
	w := apiRequest(a, http.MethodGet, "/api/requirements?q="+url.QueryEscape("跨角色需求权重"), "u_admin", a.pid(), "")
	var list struct {
		Items []Requirement `json:"items"`
	}
	if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].WeightTotal != 34 || list.Items[0].CreatedAt != x.CreatedAt || list.Items[0].TagColors["协作"] != "#12ABEF" {
		t.Fatalf("list response omitted planning fields: %s", w.Body.String())
	}
	updated := patchPlanningRequirement(t, a, x.ID, `{"remarks":"已评审","createdAt":"2000-01-01","weightTotal":500}`)
	if updated.WeightTotal != 34 || updated.CreatedAt != x.CreatedAt || updated.Remarks != "已评审" || len(updated.TagColors) != 2 {
		t.Fatalf("omitted fields should be preserved: %+v", updated)
	}
	updated = patchPlanningRequirement(t, a, x.ID, `{"roleWeights":{"frontend":{"userId":"u_front","value":0.1},"backend":{"value":0.2}},"tagColors":{"协作":"#445566"}}`)
	if updated.WeightTotal != 0.3 || updated.RoleWeights["algorithm"].Value != nil || updated.RoleWeights["product"].UserID != "" || len(updated.TagColors) != 1 {
		t.Fatalf("whole-map replacement semantics failed: %+v", updated)
	}
	updated = patchPlanningRequirement(t, a, x.ID, `{"roleWeights":null,"tagColors":{},"remarks":""}`)
	if updated.WeightTotal != 0 || len(updated.TagColors) != 0 || updated.Remarks != "" || len(updated.RoleWeights) != 5 {
		t.Fatalf("clear semantics failed: %+v", updated)
	}
	for _, weight := range updated.RoleWeights {
		if weight.Value != nil || weight.UserID != "" {
			t.Fatal("cleared weight was not returned as unestimated")
		}
	}
}

func TestRequirementPlanningRejectsInvalidAndCrossProjectPayloads(t *testing.T) {
	a := testApp(t)
	for _, body := range []string{
		`{"title":"invalid","roleWeights":{"frontend":{"value":-1}}}`,
		`{"title":"invalid","roleWeights":{"frontend":{"value":1000001}}}`,
		`{"title":"invalid","roleWeights":{"frontend":{"value":"10"}}}`,
		`{"title":"invalid","roleWeights":{"frontend":{"value":1e999}}}`,
		`{"title":"invalid","roleWeights":{"unknown":{"value":2}}}`,
		`{"title":"invalid","roleWeights":{"frontend":{"userId":"u_missing","value":2}}}`,
		`{"title":"invalid","tagColors":{"hot":"red;display:none"}}`,
		`{"title":"invalid","tagColors":{"hot":"#abcd"}}`,
		`{"title":"invalid","tagColors":{"":"#123456"}}`,
		`{"title":"invalid","sprint":"AIP 1.0 模型评测"}`,
		`{"title":"invalid","sprint":"不存在的迭代"}`,
	} {
		w := apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", projectID, body)
		if w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid payload accepted: %d %s / %s", w.Code, w.Body.String(), body)
		}
	}
	w := apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", insightProjectID, `{"title":"跨项目人员","roleWeights":{"frontend":{"userId":"u_front","value":1}}}`)
	if w.Code != 422 {
		t.Fatalf("non-member binding accepted: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", projectID, `{"title":"停用人员","roleWeights":{"frontend":{"userId":"u_front","value":1}}}`)
	if w.Code != 422 {
		t.Fatalf("inactive member binding accepted: %d %s", w.Code, w.Body.String())
	}
	x := planningRequirement(t, a, `{"title":"原始标题","remarks":"原始备注","roleWeights":{"backend":{"value":3}},"customFields":{"business_value":"中"}}`)
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"title":"不应保存","remarks":"不应保存","roleWeights":{"backend":{"value":-1}},"customFields":{"business_value":"高"}}`)
	if w.Code != 422 {
		t.Fatalf("invalid patch accepted: %d %s", w.Code, w.Body.String())
	}
	stored, _ := a.get(x.ID)
	if stored.Title != x.Title || stored.Remarks != x.Remarks || stored.WeightTotal != 3 || stored.CustomFields["business_value"] != "中" {
		t.Fatalf("invalid patch partially persisted: %+v", stored)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", insightProjectID, `{"remarks":"跨项目越权","customFields":{"business_value":"高"}}`)
	if w.Code != 404 {
		t.Fatalf("cross-project patch accepted: %d %s", w.Code, w.Body.String())
	}
	var leaked int
	a.db.QueryRow(`SELECT COUNT(*) FROM field_values WHERE tenant_id=? AND project_id=? AND object_id=? AND object_type='requirement'`, tenantID, insightProjectID, x.ID).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("cross-project custom field record created")
	}
}

func TestDynamicSprintAssignmentRenameAndLegacyAliases(t *testing.T) {
	a := testApp(t)
	planningSprint(t, a, "123", "规划中")
	s := planningSprint(t, a, "22", "进行中")
	w := apiRequest(a, http.MethodGet, "/api/meta", "u_admin", projectID, "")
	if !bytes.Contains(w.Body.Bytes(), []byte(`"123"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"22"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"V1.0 核心闭环"`)) {
		t.Fatalf("dynamic full sprint names missing: %s", w.Body.String())
	}
	x := planningRequirement(t, a, `{"title":"选择新建迭代","sprint":"123"}`)
	if x.Sprint != "123" {
		t.Fatal("new sprint assignment did not persist")
	}
	x = patchPlanningRequirement(t, a, x.ID, `{"sprint":"22"}`)
	if x.Sprint != "22" {
		t.Fatal("reassignment did not persist")
	}
	for _, table := range []string{"defects", "test_plans"} {
		if _, err := a.db.Exec(`UPDATE `+table+` SET sprint='22' WHERE tenant_id=? AND project_id=?`, tenantID, projectID); err != nil {
			t.Fatal(err)
		}
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/sprints/%d", s.ID), "u_admin", projectID, `{"name":"22 发布迭代"}`)
	if w.Code != 200 {
		t.Fatalf("rename failed: %d %s", w.Code, w.Body.String())
	}
	stored, _ := a.get(x.ID)
	if stored.Sprint != "22 发布迭代" || stored.CreatedAt != x.CreatedAt {
		t.Fatalf("rename detached requirement: %+v", stored)
	}
	for _, table := range []string{"defects", "test_plans"} {
		var name string
		a.db.QueryRow(`SELECT sprint FROM `+table+` WHERE tenant_id=? AND project_id=? LIMIT 1`, tenantID, projectID).Scan(&name)
		if name != "22 发布迭代" {
			t.Fatalf("rename detached %s: %s", table, name)
		}
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements?sprint="+url.QueryEscape("22"), "u_admin", projectID, "")
	if jsonMap(t, w)["total"].(float64) != 1 {
		t.Fatalf("legacy alias filter missed canonical requirement: %s", w.Body.String())
	}
	x = planningRequirement(t, a, `{"title":"旧版简称仍可关联","sprint":"V1.0"}`)
	if x.Sprint != "V1.0 核心闭环" {
		t.Fatalf("legacy alias not canonicalized: %s", x.Sprint)
	}
	for _, name := range []string{"Q3 Alpha", "Q3 Beta"} {
		planningSprint(t, a, name, "规划中")
	}
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", projectID, `{"title":"歧义简称","sprint":"Q3"}`)
	if w.Code != 422 {
		t.Fatalf("ambiguous sprint accepted: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"Q3 Alpha", "Q3 Beta"} {
		_, alias := a.scopedSprintAliases(name)
		if alias == "Q3" {
			t.Fatalf("ambiguous short-name aggregation: %s", name)
		}
	}
	w = apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", projectID, `{"name":"123","startDate":"2026-09-01","endDate":"2026-09-10"}`)
	if w.Code != 422 {
		t.Fatalf("duplicate sprint name accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestSprintClosedRelationsAndSelfCompletion(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "当前迭代", "进行中")
	x := planningRequirement(t, a, `{"title":"保留历史关系","sprint":"当前迭代"}`)
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/sprints/%d/complete", s.ID), "u_admin", projectID, `{"targetSprint":"当前迭代"}`)
	if w.Code != 422 {
		t.Fatalf("sprint migrated to itself: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE sprints SET status='已完成' WHERE id=?`, s.ID); err != nil {
		t.Fatal(err)
	}
	x = patchPlanningRequirement(t, a, x.ID, `{"sprint":"当前迭代","remarks":"历史迭代需求仍能编辑"}`)
	if x.Sprint != "当前迭代" {
		t.Fatal("unchanged historical relation lost")
	}
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", projectID, `{"title":"错误分配历史迭代","sprint":"当前迭代"}`)
	if w.Code != 422 {
		t.Fatalf("completed sprint assignment accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/meta", "u_admin", projectID, "")
	if strings.Contains(w.Body.String(), "当前迭代") {
		t.Fatal("completed sprint appears as an assignable target")
	}
	cleared := patchPlanningRequirement(t, a, x.ID, `{"sprint":"待规划"}`)
	if cleared.Sprint != "待规划" {
		t.Fatal("clearing historical sprint failed")
	}
}

func TestRequirementParentsScopedAndAcyclic(t *testing.T) {
	a := testApp(t)
	parent := planningRequirement(t, a, `{"title":"父需求"}`)
	child := planningRequirement(t, a, fmt.Sprintf(`{"title":"子需求","parentId":%d}`, parent.ID))
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", parent.ID), "u_admin", projectID, fmt.Sprintf(`{"parentId":%d}`, child.ID))
	if w.Code != 422 {
		t.Fatalf("cycle accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", child.ID), "u_admin", projectID, fmt.Sprintf(`{"parentId":%d}`, child.ID))
	if w.Code != 422 {
		t.Fatalf("self parent accepted: %d %s", w.Code, w.Body.String())
	}
	other := *a
	other.project = insightProjectID
	foreign := planningRequirement(t, &other, `{"title":"其他项目父需求"}`)
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", child.ID), "u_admin", projectID, fmt.Sprintf(`{"parentId":%d}`, foreign.ID))
	if w.Code != 422 {
		t.Fatalf("cross-project parent accepted: %d %s", w.Code, w.Body.String())
	}
	child = patchPlanningRequirement(t, a, child.ID, `{"parentId":null}`)
	if child.ParentID != nil {
		t.Fatal("parent clearing failed")
	}
}

func TestRequirementPlanningMigrationPreservesValuesAndCreatedAt(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"迁移保留数据","remarks":"不能丢失","tagColors":{"风险":"#AB1234"},"roleWeights":{"ui":{"value":12}}}`)
	if _, err := a.db.Exec(`UPDATE requirements SET sprint='V1.0' WHERE id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.migrateRequirementPlanning(); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := a.get(x.ID)
	if err != nil || stored.Sprint != "V1.0 核心闭环" || stored.WeightTotal != 12 || stored.Remarks != x.Remarks || stored.CreatedAt != x.CreatedAt || stored.TagColors["风险"] != "#AB1234" {
		t.Fatalf("migration lost values: %+v %v", stored, err)
	}
	// Simulate an earlier database missing the newly introduced columns.
	for _, column := range []string{"role_weights_json", "tag_colors_json", "remarks"} {
		if _, err := a.db.Exec(`ALTER TABLE requirements DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.migrateRequirementPlanning(); err != nil {
		t.Fatal(err)
	}
	stored, err = a.get(x.ID)
	if err != nil || stored.Title != x.Title || stored.CreatedAt != x.CreatedAt || len(stored.RoleWeights) != 5 || len(stored.TagColors) != 0 {
		t.Fatalf("legacy table migration failed: %+v %v", stored, err)
	}
}

func TestRequirementColumnPreferencesScopeAndViewerAccess(t *testing.T) {
	a := testApp(t)
	path := "/api/preferences/requirement-list"
	w := apiRequest(a, http.MethodGet, path, "u_viewer", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["columns"] != nil {
		t.Fatalf("expected default null columns: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, path, "u_viewer", projectID, `{"columns":["code","title","weightTotal","remarks","createdAt","cf.customer_type"]}`)
	if w.Code != 200 {
		t.Fatalf("viewer preference write rejected: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, path, "u_viewer", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["columns"].([]any)) != 6 {
		t.Fatalf("preferences not retained: %d %s", w.Code, w.Body.String())
	}
	for _, identity := range []struct{ user, project string }{{"u_admin", projectID}, {"u_viewer", insightProjectID}} {
		w = apiRequest(a, http.MethodGet, path, identity.user, identity.project, "")
		if w.Code != 200 || jsonMap(t, w)["columns"] != nil {
			t.Fatalf("preferences leaked across identity/project: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"columns":null}`, `{"columns":["title","title"]}`, `{"columns":[""]}`, `{"columns":[23]}`} {
		w = apiRequest(a, http.MethodPatch, path, "u_viewer", projectID, body)
		if w.Code != 422 {
			t.Fatalf("invalid columns accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, http.MethodPatch, path, "u_viewer", projectID, `{"columns":[]}`)
	if w.Code != 200 || jsonMap(t, w)["columns"] == nil {
		t.Fatalf("empty ordered preference not persisted: %d %s", w.Code, w.Body.String())
	}
	// The preference exception must not grant the viewer business write access.
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_viewer", projectID, `{"title":"unauthorized"}`)
	if w.Code != 403 {
		t.Fatalf("viewer acquired business write access: %d", w.Code)
	}
}

func TestPlanningAPIRejectsMalformedPatchWithoutMutation(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"严格字段类型"}`)
	for _, body := range []string{`null`, `{"remarks":42}`, `{"progress":"5"}`, `{"title":null}`, `{"roleWeights":"invalid"}`, `{"parentId":"1"}`} {
		w := httptest.NewRecorder()
		a.requirement(w, request(http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", body))
		if w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid patch accepted: %d %s", w.Code, w.Body.String())
		}
	}
	stored, _ := a.get(x.ID)
	if stored.Title != x.Title {
		t.Fatal("invalid patch mutated requirement")
	}
}

func TestPlanningChangesRollbackTogetherOnValidationFailure(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "不可部分改名", "进行中")
	x := planningRequirement(t, a, `{"title":"事务验证","sprint":"不可部分改名","roleWeights":{"frontend":{"value":4}}}`)
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/sprints/%d", s.ID), "u_admin", projectID, `{"name":"不应改名","status":"规划中"}`)
	if w.Code != 422 {
		t.Fatalf("invalid sprint transition accepted: %d %s", w.Code, w.Body.String())
	}
	var name string
	a.db.QueryRow(`SELECT name FROM sprints WHERE id=?`, s.ID).Scan(&name)
	stored, _ := a.get(x.ID)
	if name != s.Name || stored.Sprint != s.Name {
		t.Fatalf("invalid rename partially persisted: %s %+v", name, stored)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"remarks":"不应保存","roleWeights":{"frontend":{"value":7}},"customFields":{"business_value":"无效选项"}}`)
	if w.Code != 422 {
		t.Fatalf("invalid custom field accepted: %d %s", w.Code, w.Body.String())
	}
	stored, _ = a.get(x.ID)
	if stored.WeightTotal != 4 || stored.Remarks != "" {
		t.Fatalf("weight or remarks persisted despite failed custom field: %+v", stored)
	}
	// Names are matched as literal prefixes, not wildcard SQL LIKE patterns.
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", projectID, `{"title":"不能使用模糊通配名称","sprint":"%"}`)
	if w.Code != 422 {
		t.Fatalf("wildcard sprint matched: %d %s", w.Code, w.Body.String())
	}
}
