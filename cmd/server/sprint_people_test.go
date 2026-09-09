package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestSprintItemsIncludeAllAssigneesWithoutMultiplyingWorkOrWeights(t *testing.T) {
	a := testApp(t)
	sprint := planningSprint(t, a, "多人协同迭代", "进行中")
	requirement := planningRequirement(t, a, jsonText(map[string]any{"title": "多人需求原文", "sprint": sprint.Name, "assigneeUserIds": []string{"u_front", "u_back"}, "roleWeights": map[string]any{"frontend": map[string]any{"value": 3}, "backend": map[string]any{"value": 5}}}))
	type sprintResponse struct {
		Items []struct {
			ID              int64                 `json:"id"`
			Assignee        string                `json:"assignee"`
			AssigneeUserIDs []string              `json:"assigneeUserIds"`
			Assignees       []RequirementAssignee `json:"assignees"`
		} `json:"items"`
		Summary struct {
			Total int `json:"total"`
		} `json:"summary"`
		WeightSummary SprintWeightSummary `json:"weightSummary"`
	}
	for _, ids := range [][]string{{"u_front", "u_back"}, {"u_back", "u_front", "u_pm"}} {
		patchPlanningRequirement(t, a, requirement.ID, jsonText(map[string]any{"assigneeUserIds": ids}))
		w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", sprint.ID), "u_admin", a.pid(), "")
		var response sprintResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatalf("sprint items: %d %s", w.Code, w.Body.String())
		}
		if len(response.Items) != 1 || response.Summary.Total != 1 || response.WeightSummary.RequirementCount != 1 || response.WeightSummary.TotalWeight != 8 {
			t.Fatalf("participant expansion duplicated work/weight: %s", w.Body.String())
		}
		item := response.Items[0]
		if item.ID != requirement.ID || !reflect.DeepEqual(item.AssigneeUserIDs, ids) || len(item.Assignees) != len(ids) {
			t.Fatalf("missing ordered assignees: %+v", item)
		}
		for index, id := range ids {
			var name string
			if err := a.db.QueryRow(`SELECT name FROM users WHERE id=? AND tenant_id=?`, id, tenantID).Scan(&name); err != nil {
				t.Fatal(err)
			}
			if item.Assignees[index].ID != id || item.Assignees[index].Name != name {
				t.Fatalf("incorrect identity snapshot: %+v", item.Assignees[index])
			}
		}
		if item.Assignee != item.Assignees[0].Name {
			t.Fatal("legacy primary assignee must remain first")
		}
	}
}

func TestSprintAssigneeHydrationKeepsHistoricalIDsAndSameNames(t *testing.T) {
	a := testApp(t)
	sprint := planningSprint(t, a, "历史多人迭代", "进行中")
	requirement := planningRequirement(t, a, jsonText(map[string]any{"title": "同名但不同身份", "sprint": sprint.Name, "assigneeUserIds": []string{"u_front", "u_back"}}))
	if _, err := a.db.Exec(`UPDATE users SET name='同名成员' WHERE tenant_id=? AND id IN ('u_front','u_back')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE tenant_id=? AND id='u_back'`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE requirements SET assignee_user_ids_json='["u_front","u_back","u_front"]' WHERE id=?`, requirement.ID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", sprint.ID), "u_admin", a.pid(), "")
	data := jsonMap(t, w)
	item := data["items"].([]any)[0].(map[string]any)
	people := item["assignees"].([]any)
	if len(people) != 2 || people[0].(map[string]any)["id"] != "u_front" || people[1].(map[string]any)["id"] != "u_back" || people[0].(map[string]any)["name"] != "同名成员" || people[1].(map[string]any)["name"] != "同名成员" {
		t.Fatalf("historical/same-name identities collapsed: %s", w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE requirements SET assignee_user_ids_json='[]',assignee_user_id='u_front' WHERE id=?`, requirement.ID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", sprint.ID), "u_admin", a.pid(), "")
	item = jsonMap(t, w)["items"].([]any)[0].(map[string]any)
	if len(item["assigneeUserIds"].([]any)) != 1 || item["assigneeUserIds"].([]any)[0] != "u_front" {
		t.Fatalf("legacy single ID fallback lost: %s", w.Body.String())
	}
}

func TestSprintDefectStillUsesOneOwnerAndMalformedAssigneesFailClosed(t *testing.T) {
	a := testApp(t)
	sprint := planningSprint(t, a, "单缺陷归属", "进行中")
	w := apiRequest(a, http.MethodPost, "/api/defects", "u_admin", a.pid(), jsonText(map[string]any{"title": "单一负责人缺陷", "sprint": sprint.Name, "assignee": "陆川"}))
	if w.Code != 201 {
		t.Fatalf("defect: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", sprint.ID), "u_admin", a.pid(), "")
	item := jsonMap(t, w)["items"].([]any)[0].(map[string]any)
	if item["objectType"] != "defect" || item["assignee"] != "陆川" || item["assigneeUserIds"] != nil {
		t.Fatalf("defect unexpectedly became multi-assignee: %s", w.Body.String())
	}
	id := insertWeightRequirement(t, a, tenantID, a.pid(), sprint.Name, "开发中", `{}`, nil)
	if _, err := a.db.Exec(`UPDATE requirements SET assignee_user_ids_json='[1]' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/sprints/%d", sprint.ID), "u_admin", a.pid(), "")
	if w.Code != 503 {
		t.Fatalf("malformed participant JSON appeared as empty data: %d %s", w.Code, w.Body.String())
	}
}
