package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func queryRequirementsForTest(t *testing.T, a *App, query string) []Requirement {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/requirements?"+query, "u_admin", a.pid(), "")
	if w.Code != 200 {
		t.Fatalf("list %s: %d %s", query, w.Code, w.Body.String())
	}
	var data struct {
		Items []Requirement `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	return data.Items
}
func TestWorkItemQueryNumericSortAndTypedAND(t *testing.T) {
	a := testApp(t)
	for _, body := range []string{`{"objectType":"requirement","key":"query_number","name":"查询数值","type":"number"}`, `{"objectType":"requirement","key":"query_multi","name":"查询多选","type":"multi_select","options":["A","B"]}`} {
		w := apiRequest(a, http.MethodPost, "/api/field-definitions", "u_admin", a.pid(), body)
		if w.Code != 201 {
			t.Fatal(w.Body.String())
		}
	}
	first := planningRequirement(t, a, `{"title":"列表查询甲","assigneeUserIds":["u_front","u_back"],"ownerUserIds":["u_pm","u_back"],"roleWeights":{"frontend":{"userIds":["u_front","u_back"],"value":2},"ui":{"value":0}},"sensitive":false,"tags":"tag1,tag2","customFields":{"query_number":10,"query_multi":["A","B"]}}`)
	second := planningRequirement(t, a, `{"title":"列表查询乙","roleWeights":{"frontend":{"value":10}},"sensitive":true,"customFields":{"query_number":2}}`)
	third := planningRequirement(t, a, `{"title":"列表查询空"}`)
	prefix := "q=" + url.QueryEscape("列表查询")
	for _, tt := range []struct {
		sort string
		ids  []int64
	}{{"role.frontend.value&order=asc", []int64{first.ID, second.ID, third.ID}}, {"role.frontend.value&order=desc", []int64{second.ID, first.ID, third.ID}}, {"cf.query_number&order=asc", []int64{second.ID, first.ID, third.ID}}} {
		items := queryRequirementsForTest(t, a, prefix+"&sort="+tt.sort)
		ids := []int64{}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if !reflect.DeepEqual(ids, tt.ids) {
			t.Fatalf("sort %s got %v want %v", tt.sort, ids, tt.ids)
		}
	}
	filters := []workItemFilter{{Field: "assignee", Operator: "includes", Value: "u_back"}, {Field: "owner", Operator: "includes", Value: "u_back"}, {Field: "role.frontend.userId", Operator: "includes", Value: "u_back"}, {Field: "role.ui.value", Operator: "eq", Value: 0}, {Field: "sensitive", Operator: "eq", Value: false}, {Field: "tags", Operator: "includes", Value: "tag2"}, {Field: "cf.query_number", Operator: "gte", Value: 10}, {Field: "cf.query_multi", Operator: "includes", Value: "B"}}
	items := queryRequirementsForTest(t, a, prefix+"&filters="+url.QueryEscape(jsonText(filters)))
	if len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("typed AND: %+v", items)
	}
	for key := range baseWorkQueryFields() {
		queryRequirementsForTest(t, a, prefix+"&sort="+url.QueryEscape(key))
		queryRequirementsForTest(t, a, prefix+"&filters="+url.QueryEscape(jsonText([]workItemFilter{{Field: key, Operator: "is_empty"}})))
	}
	if _, err := a.db.Exec(`UPDATE users SET timezone='Asia/Shanghai' WHERE tenant_id=? AND id='u_admin'`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE requirements SET created_at='2026-09-03T20:00:00Z' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	dateItems := queryRequirementsForTest(t, a, prefix+"&filters="+url.QueryEscape(`[{"field":"createdAt","operator":"eq","value":"2026-09-04"}]`))
	foundDate := false
	for _, item := range dateItems {
		if item.ID == first.ID {
			foundDate = true
		}
	}
	if !foundDate {
		t.Fatal("date filter ignored the user's displayed timezone")
	}
}
func TestWorkItemQueryRejectsUnknownFieldsTypesAndCrossProjectCustom(t *testing.T) {
	a := testApp(t)
	invalid := []string{"sort=" + url.QueryEscape("title;DROP TABLE requirements"), "sort=cf.foreign_project_field", "order=sideways", "filters=" + url.QueryEscape("null trailing"), "filters=" + url.QueryEscape(`[{"field":"weightTotal","operator":"eq","value":"0"}]`), "filters=" + url.QueryEscape(`[{"field":"sensitive","operator":"eq","value":"false"}]`), "filters=" + url.QueryEscape(`[{"field":"createdAt","operator":"eq","value":"2026-02-30"}]`), "filters=" + url.QueryEscape(`[{"field":"owner","operator":"gt","value":"u_front"}]`), "filters=" + url.QueryEscape(`[{"field":"title","operator":"is_empty","value":"x"}]`), "filters=" + url.QueryEscape(`[{"field":"title","operator":"eq","value":"x","or":"1=1"}]`)}
	for _, query := range invalid {
		w := apiRequest(a, http.MethodGet, "/api/requirements?"+query, "u_admin", a.pid(), "")
		if w.Code != 422 {
			t.Fatalf("invalid query accepted %s: %d %s", query, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, http.MethodPost, "/api/field-definitions", "u_admin", insightProjectID, `{"objectType":"requirement","key":"foreign_project_field","name":"隔离字段","type":"number"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements?sort=cf.foreign_project_field", "u_admin", a.pid(), "")
	if w.Code != 422 {
		t.Fatalf("cross-project definition accepted: %s", w.Body.String())
	}
	a2 := *a
	a2.project = insightProjectID
	foreign := planningRequirement(t, &a2, `{"title":"跨项目不可见"}`)
	items := queryRequirementsForTest(t, a, "sort=code&order=asc")
	for _, item := range items {
		if item.ID == foreign.ID {
			t.Fatal("query leaked foreign project")
		}
	}
}
func TestSprintFullFieldsAndBacklogRoundTrip(t *testing.T) {
	a := testApp(t)
	s := planningSprint(t, a, "完整字段迭代", "进行中")
	w := apiRequest(a, http.MethodPost, "/api/field-definitions", "u_admin", a.pid(), `{"objectType":"requirement","key":"sprint_custom","name":"迭代自定义","type":"number"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	item := planningRequirement(t, a, `{"title":"完整字段","remarks":"原备注","tags":"红标","ownerUserIds":["u_pm","u_front"],"roleWeights":{"frontend":{"value":12},"ui":{"value":0}},"customFields":{"sprint_custom":3.14}}`)
	for _, target := range []string{"待规划", s.Name, "待规划"} {
		patchPlanningRequirement(t, a, item.ID, jsonText(map[string]any{"sprint": target}))
		path := "/api/sprints/backlog/items"
		if target != "待规划" {
			path = fmt.Sprintf("/api/sprints/%d", s.ID)
		}
		w = apiRequest(a, http.MethodGet, path, "u_admin", a.pid(), "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var data struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range data.Items {
			if record["objectType"] == "requirement" && record["id"] == float64(item.ID) {
				found = true
				if record["remarks"] != "原备注" || record["weightTotal"] != float64(12) || record["createdAt"] == "" || record["customFields"].(map[string]any)["sprint_custom"] != 3.14 || len(record["ownerUserIds"].([]any)) != 2 {
					t.Fatalf("truncated fields: %+v", record)
				}
			}
		}
		if !found {
			t.Fatal("missing full requirement")
		}
	}
}
func TestSprintScopedColumnPreferences(t *testing.T) {
	a := testApp(t)
	pref := "/api/preferences/requirement-list?view=sprint-list"
	w := apiRequest(a, http.MethodPatch, pref, "u_front", a.pid(), `{"columns":["code","title","role.ui.value"]}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/api/preferences/requirement-list", pref} {
		w = apiRequest(a, http.MethodGet, path, "u_pm", a.pid(), "")
		if w.Code != 200 || jsonMap(t, w)["columns"] != nil {
			t.Fatal("preferences leaked account")
		}
	}
	w = apiRequest(a, http.MethodGet, "/api/preferences/requirement-list?view=other", "u_admin", a.pid(), "")
	if w.Code != 422 {
		t.Fatal("unknown preference view accepted")
	}
}
