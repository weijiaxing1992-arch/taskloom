package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func dependencyResponse(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("invalid dependency response: %v", err)
	}
	return payload
}

func createDependency(t *testing.T, a *App, user, project string, requirementID int64, payload map[string]any) map[string]any {
	t.Helper()
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/dependencies", requirementID), user, project, jsonText(payload))
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("create dependency: %d %s", w.Code, w.Body.String())
	}
	return dependencyResponse(t, w.Body.Bytes())
}

func itemID(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("missing dependency item: %#v", payload)
	}
	id, ok := item["id"].(float64)
	if !ok || id < 1 {
		t.Fatalf("invalid dependency item id: %#v", item)
	}
	return int64(id)
}

func TestRequirementDependenciesCRUDCycleAuditAndPagination(t *testing.T) {
	a := testApp(t)
	first := planningRequirement(t, a, `{"title":"依赖源","startDate":"2026-09-01","endDate":"2026-09-04"}`)
	second := planningRequirement(t, a, `{"title":"依赖目标","startDate":"2026-09-05","endDate":"2026-09-08"}`)
	third := planningRequirement(t, a, `{"title":"依赖第三项"}`)
	if err := a.migrateRequirementDependencies(); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementDependencies(); err != nil {
		t.Fatal(err)
	}

	firstCreate := createDependency(t, a, "u_admin", projectID, first.ID, map[string]any{"targetRequirementId": second.ID, "relationType": "blocks"})
	firstID := itemID(t, firstCreate)
	if item := firstCreate["item"].(map[string]any); item["relationType"] != "blocks" || item["counterpart"].(map[string]any)["title"] != second.Title {
		t.Fatalf("wrong first dependency response: %#v", item)
	}
	secondCreate := createDependency(t, a, "u_admin", projectID, first.ID, map[string]any{"targetRequirementId": third.ID, "relationType": "relates_to"})
	secondID := itemID(t, secondCreate)
	if secondID == firstID {
		t.Fatal("distinct dependency reused id")
	}

	// 先有 A->B、B->C，再尝试 C->A，必须在写入前拒绝闭环。
	createDependency(t, a, "u_admin", projectID, second.ID, map[string]any{"targetRequirementId": third.ID, "relationType": "blocks"})
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/dependencies", third.ID), "u_admin", projectID, jsonText(map[string]any{"targetRequirementId": first.ID, "relationType": "blocks"}))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "dependency_conflict") {
		t.Fatalf("blocking cycle accepted: %d %s", w.Code, w.Body.String())
	}

	// firstID 不是最新记录；PATCH 后仍应按 id 定向读取，而不是误取第二条依赖。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d/dependencies/%d", first.ID, firstID), "u_admin", projectID, `{"relationType":"blocked_by"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update older dependency: %d %s", w.Code, w.Body.String())
	}
	updated := dependencyResponse(t, w.Body.Bytes())["item"].(map[string]any)
	if int64(updated["id"].(float64)) != firstID || updated["relationType"] != "blocked_by" {
		t.Fatalf("updated wrong dependency: %#v", updated)
	}

	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/dependencies?page=1&pageSize=1", first.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list dependencies: %d %s", w.Code, w.Body.String())
	}
	listed := dependencyResponse(t, w.Body.Bytes())
	if !listed["hasMore"].(bool) || len(listed["items"].([]any)) != 1 {
		t.Fatalf("bounded dependency pagination missing: %#v", listed)
	}

	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/requirements/%d/dependencies/%d", first.ID, secondID), "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete dependency: %d %s", w.Code, w.Body.String())
	}
	var auditCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND object_type='requirement_dependency' AND action IN ('requirement_dependency_created','requirement_dependency_updated','requirement_dependency_deleted')`, tenantID).Scan(&auditCount); err != nil || auditCount < 3 {
		t.Fatalf("dependency audit missing: %d %v", auditCount, err)
	}
}

// 新依赖表上线不能悄悄迁移、覆盖或删除既有的“关联需求”数据。
// 该回归覆盖升级路径，而非只依赖两套 handler 恰好使用不同表的实现细节。
func TestRequirementDependencyMigrationPreservesLegacyRequirementLinks(t *testing.T) {
	a := testApp(t)
	first := planningRequirement(t, a, `{"title":"历史关联源"}`)
	second := planningRequirement(t, a, `{"title":"历史关联目标"}`)
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/links", first.ID), "u_admin", projectID, fmt.Sprintf(`{"requirementId":%d}`, second.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("create legacy relation: %d %s", w.Code, w.Body.String())
	}
	before := tableCount(t, a, "work_item_relations")
	if err := a.migrateRequirementDependencies(); err != nil {
		t.Fatal(err)
	}
	if after := tableCount(t, a, "work_item_relations"); after != before {
		t.Fatalf("legacy relation changed during dependency migration: %d -> %d", before, after)
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/links", first.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("legacy relation no longer readable after dependency migration: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementDependenciesCrossProjectAuthorizationCandidatesAndRoadmapVisibility(t *testing.T) {
	a := testApp(t)
	sourceSprint := planningSprint(t, a, "依赖源迭代", "规划中")
	source := planningRequirement(t, a, jsonText(map[string]any{"title": "公开源需求", "sprint": sourceSprint.Name, "startDate": "2026-09-04", "endDate": "2026-09-10"}))
	insight := *a
	insight.project = insightProjectID
	targetSprint := planningSprint(t, &insight, "依赖目标迭代", "规划中")
	target := planningRequirement(t, &insight, jsonText(map[string]any{"title": "仅洞察项目可见的依赖目标", "sprint": targetSprint.Name, "startDate": "2026-09-11", "endDate": "2026-09-18"}))
	created := createDependency(t, a, "u_admin", projectID, source.ID, map[string]any{"targetProjectId": insightProjectID, "targetRequirementId": target.ID, "relationType": "blocks"})
	if item := created["item"].(map[string]any); !item["crossProject"].(bool) || item["counterpart"].(map[string]any)["projectId"] != insightProjectID {
		t.Fatalf("cross project dependency not returned to administrator: %#v", item)
	}

	// 前端成员不属于洞察项目：写入被统一拒绝，响应不得包含目标标题。
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/dependencies", source.ID), "u_front", projectID, jsonText(map[string]any{"targetProjectId": insightProjectID, "targetRequirementId": target.ID, "relationType": "blocks"}))
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), target.Title) {
		t.Fatalf("cross project target leaked or write allowed: %d %s", w.Code, w.Body.String())
	}

	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirement-dependency-candidates?requirementId=%d&scope=all&q=%s", source.ID, "洞察"), "u_front", projectID, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), target.Title) {
		t.Fatalf("candidate leaked inaccessible target: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements/%d/dependencies", source.ID), "u_front", projectID, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), target.Title) || len(dependencyResponse(t, w.Body.Bytes())["items"].([]any)) != 0 {
		t.Fatalf("dependency read leaked revoked project: %d %s", w.Code, w.Body.String())
	}

	w = apiRequest(a, http.MethodGet, "/api/roadmap?scope=all&page=1&pageSize=100", "u_front", projectID, "")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), target.Title) {
		t.Fatalf("roadmap leaked inaccessible project: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/roadmap?scope=all&page=1&pageSize=100", "u_admin", projectID, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), target.Title) {
		t.Fatalf("administrator roadmap missing accessible target: %d %s", w.Code, w.Body.String())
	}
	roadmap := dependencyResponse(t, w.Body.Bytes())
	foundSource, foundTarget := false, false
	for _, raw := range roadmap["items"].([]any) {
		item := raw.(map[string]any)
		if int64(item["id"].(float64)) == source.ID {
			foundSource = item["dependencyStatus"].(map[string]any)["state"] == "blocking"
		}
		if int64(item["id"].(float64)) == target.ID {
			foundTarget = item["dependencyStatus"].(map[string]any)["state"] == "blocked"
		}
	}
	if !foundSource || !foundTarget {
		t.Fatalf("roadmap dependency statuses missing: %#v", roadmap)
	}
}
