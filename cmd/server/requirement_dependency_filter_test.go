package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func dependencyStateList(t *testing.T, a *App, user, project, state string) []Requirement {
	return dependencyStateFilterList(t, a, user, project, "eq", state)
}

func dependencyStateFilterList(t *testing.T, a *App, user, project, operator string, value any) []Requirement {
	t.Helper()
	filters, err := json.Marshal([]workItemFilter{{Field: "dependencyState", Operator: operator, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodGet, "/api/requirements?projection=list&page=1&pageSize=100&filters="+url.QueryEscape(string(filters)), user, project, "")
	if w.Code != http.StatusOK {
		t.Fatalf("dependency state list %q: %d %s", operator, w.Code, w.Body.String())
	}
	var response struct {
		Items []Requirement `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Items
}

func includesRequirement(items []Requirement, id int64) *Requirement {
	for index := range items {
		if items[index].ID == id {
			return &items[index]
		}
	}
	return nil
}

func TestRequirementDependencyStateFilterIsCompleteAndDoesNotLeakCrossProjectBlockers(t *testing.T) {
	a := testApp(t)
	source := planningRequirement(t, a, `{"title":"当前项目的前置需求"}`)
	target := planningRequirement(t, a, `{"title":"当前项目被阻塞的需求"}`)
	clear := planningRequirement(t, a, `{"title":"当前项目无依赖需求"}`)
	createDependency(t, a, "u_admin", projectID, source.ID, map[string]any{"targetRequirementId": target.ID, "relationType": "blocks"})

	blocked := dependencyStateList(t, a, "u_admin", projectID, "blocked")
	blockedTarget := includesRequirement(blocked, target.ID)
	if blockedTarget == nil || blockedTarget.DependencyStatus == nil || blockedTarget.DependencyStatus.State != "blocked" || blockedTarget.DependencyStatus.BlockedByCount != 1 {
		t.Fatalf("blocked requirement was not returned with its visible predecessor summary: %#v", blocked)
	}
	if includesRequirement(blocked, source.ID) != nil {
		t.Fatalf("source requirement appeared in the blocked result: %#v", blocked)
	}
	empty := dependencyStateFilterList(t, a, "u_admin", projectID, "is_empty", nil)
	if includesRequirement(empty, clear.ID) == nil || includesRequirement(empty, source.ID) != nil || includesRequirement(empty, target.ID) != nil {
		t.Fatalf("empty dependency state did not consistently mean no visible dependency: %#v", empty)
	}
	nonEmpty := dependencyStateFilterList(t, a, "u_admin", projectID, "not_empty", nil)
	if includesRequirement(nonEmpty, clear.ID) != nil || includesRequirement(nonEmpty, source.ID) == nil || includesRequirement(nonEmpty, target.ID) == nil {
		t.Fatalf("non-empty dependency state did not consistently mean visible dependency: %#v", nonEmpty)
	}
	blocking := dependencyStateList(t, a, "u_admin", projectID, "blocking")
	if item := includesRequirement(blocking, source.ID); item == nil || item.DependencyStatus == nil || item.DependencyStatus.BlockingCount != 1 {
		t.Fatalf("source requirement was not returned as blocking: %#v", blocking)
	}

	// 一个前置项位于无访问权限的项目时，当前项目成员不能通过“被阻塞”
	// 筛选推断该项目或其需求存在；管理员保留完整、授权内的交付视图。
	insight := *a
	insight.project = insightProjectID
	hiddenSource := planningRequirement(t, &insight, `{"title":"不应泄露的跨项目依赖源"}`)
	localTarget := planningRequirement(t, a, `{"title":"仅管理员可见跨项目阻塞"}`)
	createDependency(t, &insight, "u_admin", insightProjectID, hiddenSource.ID, map[string]any{"targetProjectId": projectID, "targetRequirementId": localTarget.ID, "relationType": "blocks"})

	admin := dependencyStateList(t, a, "u_admin", projectID, "blocked")
	if item := includesRequirement(admin, localTarget.ID); item == nil || item.DependencyStatus == nil || item.DependencyStatus.BlockedByCount != 1 {
		t.Fatalf("administrator lost authorized cross-project blocker: %#v", admin)
	}
	front := dependencyStateList(t, a, "u_front", projectID, "blocked")
	if item := includesRequirement(front, localTarget.ID); item != nil {
		t.Fatalf("inaccessible cross-project dependency leaked through the list filter: %#v", item)
	}
}

func TestRequirementDependencyStateFilterRejectsUnknownValues(t *testing.T) {
	a := testApp(t)
	filters := url.QueryEscape(`[{"field":"dependencyState","operator":"contains","value":"blocked"}]`)
	w := apiRequest(a, http.MethodGet, "/api/requirements?filters="+filters, "u_admin", projectID, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid dependency state filter accepted: %d %s", w.Code, w.Body.String())
	}
	filters = url.QueryEscape(`[{"field":"dependencyState","operator":"eq","value":"unknown"}]`)
	w = apiRequest(a, http.MethodGet, "/api/requirements?filters="+filters, "u_admin", projectID, "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown dependency state accepted: %d %s", w.Code, w.Body.String())
	}
}
