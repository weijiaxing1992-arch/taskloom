package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func testingJSONID(t *testing.T, wBody map[string]any) int64 {
	t.Helper()
	value, ok := wBody["id"].(float64)
	if !ok || value < 1 {
		t.Fatalf("missing id in %#v", wBody)
	}
	return int64(value)
}

func createWorkspaceCase(t *testing.T, a *App, title string) TestCase {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, fmt.Sprintf(`{"title":%q,"steps":"执行","expected":"通过","metadata":{"description":"覆盖主路径","testData":"账号 A","estimatedMinutes":8}}`, title))
	if w.Code != http.StatusCreated {
		t.Fatalf("create case: %d %s", w.Code, w.Body.String())
	}
	var item TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestTestingWorkspaceNewProjectDefaultsKeepListCompact(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/testing/settings", "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("read default testing settings: %d %s", w.Code, w.Body.String())
	}
	fields, ok := jsonMap(t, w)["fields"].([]any)
	if !ok || len(fields) != len(testingFieldDefaults) {
		t.Fatalf("default fields missing: %s", w.Body.String())
	}
	visible := map[string]bool{}
	for _, raw := range fields {
		field, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("invalid default field: %#v", raw)
		}
		key, keyOK := field["key"].(string)
		listVisible, visibleOK := field["listVisible"].(bool)
		if !keyOK || !visibleOK {
			t.Fatalf("invalid default field shape: %#v", field)
		}
		visible[key] = listVisible
	}
	for key, want := range map[string]bool{
		"description": false, "testData": false, "preconditions": false,
		"estimatedMinutes": false, "requirementId": true,
	} {
		if got, exists := visible[key]; !exists || got != want {
			t.Fatalf("default list visibility for %s = %t, want %t", key, got, want)
		}
	}
}

func TestTestingWorkspaceMigrationKeepsSavedColumnPreferences(t *testing.T) {
	a := testApp(t)
	// 管理员已保存的列偏好属于项目数据；重新部署时的幂等迁移只能补缺，不能
	// 用新版本的紧凑默认值覆盖它。
	w := apiRequest(a, http.MethodGet, "/api/testing/settings", "u_admin", projectID, "")
	settings := jsonMap(t, w)
	fields := settings["fields"].([]any)
	for _, raw := range fields {
		field := raw.(map[string]any)
		switch field["key"] {
		case "description":
			field["listVisible"] = true
		case "requirementId":
			field["listVisible"] = false
		}
	}
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, "/api/testing/settings", "u_admin", projectID, string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("save custom column preference: %d %s", w.Code, w.Body.String())
	}
	if err := a.migrateTestingWorkspace(); err != nil {
		t.Fatalf("repeat workspace migration: %v", err)
	}
	w = apiRequest(a, http.MethodGet, "/api/testing/settings", "u_admin", projectID, "")
	visible := map[string]bool{}
	for _, raw := range jsonMap(t, w)["fields"].([]any) {
		field := raw.(map[string]any)
		visible[field["key"].(string)] = field["listVisible"].(bool)
	}
	if !visible["description"] || visible["requirementId"] {
		t.Fatalf("migration overwrote saved column preference: %#v", visible)
	}
}

func TestTestingWorkspaceLibraryFolderMoveAndPagination(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/testing/workspace", "u_admin", projectID, "")
	workspace := jsonMap(t, w)
	if w.Code != http.StatusOK || len(workspace["libraries"].([]any)) == 0 || workspace["canManage"] != true {
		t.Fatalf("workspace bootstrap: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/testing/libraries", "u_admin", projectID, `{"name":"Web 回归库"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("library: %d %s", w.Code, w.Body.String())
	}
	libraryID := testingJSONID(t, jsonMap(t, w))
	w = apiRequest(a, http.MethodPost, "/api/testing/folders", "u_admin", projectID, fmt.Sprintf(`{"libraryId":%d,"name":"登录","sortOrder":10}`, libraryID))
	if w.Code != http.StatusCreated {
		t.Fatalf("folder: %d %s", w.Code, w.Body.String())
	}
	folderID := testingJSONID(t, jsonMap(t, w))
	w = apiRequest(a, http.MethodPost, "/api/testing/folders", "u_admin", projectID, fmt.Sprintf(`{"libraryId":%d,"parentId":%d,"name":"异常","sortOrder":20}`, libraryID, folderID))
	if w.Code != http.StatusCreated {
		t.Fatalf("child folder: %d %s", w.Code, w.Body.String())
	}
	childID := testingJSONID(t, jsonMap(t, w))
	// 目录不能移动到自己的子树；该限制避免筛选 descendants 时递归循环。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/testing/folders/%d", folderID), "u_admin", projectID, fmt.Sprintf(`{"libraryId":%d,"parentId":%d,"name":"登录","sortOrder":10}`, libraryID, childID))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("folder cycle accepted: %d %s", w.Code, w.Body.String())
	}
	// 旧页面编辑目录时未传 sortOrder；后端应保留人工排好的顺序，而不是回退到 0。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/testing/folders/%d", folderID), "u_admin", projectID, fmt.Sprintf(`{"libraryId":%d,"name":"登录入口"}`, libraryID))
	if w.Code != http.StatusOK || jsonMap(t, w)["sortOrder"] != float64(10) {
		t.Fatalf("folder sort order lost on legacy patch: %d %s", w.Code, w.Body.String())
	}

	item := createWorkspaceCase(t, a, "目录树筛选")
	w = apiRequest(a, http.MethodPost, "/api/testing/cases/move", "u_admin", projectID, fmt.Sprintf(`{"caseIds":[%d],"libraryId":%d,"folderId":%d}`, item.ID, libraryID, childID))
	if w.Code != http.StatusOK {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/test-cases?folderId=%d&page=1&pageSize=20", folderID), "u_admin", projectID, "")
	payload := jsonMap(t, w)
	if w.Code != http.StatusOK || payload["total"].(float64) < 1 || len(payload["items"].([]any)) == 0 {
		t.Fatalf("descendant page missing moved case: %d %s", w.Code, w.Body.String())
	}
	first := payload["items"].([]any)[0].(map[string]any)
	metadata := first["metadata"].(map[string]any)
	if metadata["libraryId"] != float64(libraryID) || metadata["folderId"] != float64(childID) {
		t.Fatalf("location not returned safely: %#v", metadata)
	}
	// 接口本身仍以项目作用域确认 caseId，不能把 ORBIT 用例移动到洞察项目。
	w = apiRequest(a, http.MethodPost, "/api/testing/cases/move", "u_admin", insightProjectID, fmt.Sprintf(`{"caseIds":[%d],"libraryId":1}`, item.ID))
	if w.Code != http.StatusNotFound && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-project move accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, "/api/testing/libraries", "u_viewer", projectID, `{"name":"越权库"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer created library: %d %s", w.Code, w.Body.String())
	}
}

func TestTestingSettingsMetadataReviewHistoryAndBlockedGuard(t *testing.T) {
	a := testApp(t)
	settingsBody := `{"version":1,"blockedEnabled":false,"fields":[{"key":"description","name":"用例描述","description":"","required":true,"listVisible":true,"defaultValue":"","enabled":true},{"key":"testData","name":"测试数据","description":"","required":false,"listVisible":true,"defaultValue":"","enabled":true},{"key":"preconditions","name":"前置条件","description":"","required":false,"listVisible":true,"defaultValue":"","enabled":true},{"key":"estimatedMinutes","name":"预计时长","description":"","required":false,"listVisible":true,"defaultValue":0,"enabled":true},{"key":"requirementId","name":"关联需求","description":"","required":false,"listVisible":false,"defaultValue":null,"enabled":true}]}`
	w := apiRequest(a, http.MethodPatch, "/api/testing/settings", "u_admin", projectID, settingsBody)
	if w.Code != http.StatusOK {
		t.Fatalf("settings update: %d %s", w.Code, w.Body.String())
	}
	// 设置页会把 GET/PATCH 的完整对象存回编辑草稿；只读 canManage 必须可安全
	// 忽略，不能让第二次保存因为回显字段被拒绝。
	var roundTrip map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &roundTrip); err != nil {
		t.Fatal(err)
	}
	rawRoundTrip, err := json.Marshal(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, "/api/testing/settings", "u_admin", projectID, string(rawRoundTrip))
	if w.Code != http.StatusOK {
		t.Fatalf("settings round trip rejected: %d %s", w.Code, w.Body.String())
	}
	before := tableCount(t, a, "test_cases")
	w = apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, `{"title":"遗漏描述","steps":"执行","expected":"通过"}`)
	if w.Code != http.StatusUnprocessableEntity || tableCount(t, a, "test_cases") != before {
		t.Fatalf("required metadata bypassed: %d %s", w.Code, w.Body.String())
	}
	item := createWorkspaceCase(t, a, "审核用例")
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/testing/cases/%d/history", item.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("creation history missing: %d %s", w.Code, w.Body.String())
	}
	// 顶层旧字段和 metadata 是同一业务值的双入口。只有 metadata 不带别名时，
	// 顶层更新才能安全写入；冲突（包括顶层明确 null）必须失败而不能由旧值覆盖。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", item.ID), "u_admin", projectID, `{"preconditions":"已具备测试账号","metadata":{"description":"覆盖主路径并检查异常"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("top-level preconditions update: %d %s", w.Code, w.Body.String())
	}
	var patched TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil || patched.Preconditions != "已具备测试账号" || patched.Metadata == nil || patched.Metadata.Preconditions != "已具备测试账号" {
		t.Fatalf("metadata alias overwrote preconditions: %#v err=%v", patched, err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", item.ID), "u_admin", projectID, `{"requirementId":null,"metadata":{"requirementId":1}}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("null top-level requirement was silently overwritten: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/testing/cases/%d/metadata", item.ID), "u_admin", projectID, `{"metadata":{"testData":"账号 B","preconditions":"已登录","requirementId":1,"estimatedMinutes":12}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("metadata patch: %d %s", w.Code, w.Body.String())
	}
	updated := jsonMap(t, w)
	if updated["testData"] != "账号 B" || updated["requirementId"] != float64(1) {
		t.Fatalf("metadata not persisted: %#v", updated)
	}
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/testing/cases/%d/reviews", item.ID), "u_front", projectID, `{"decision":"submit","comment":"请测试验收"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("submit review: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/testing/cases/%d/reviews", item.ID), "u_qa", projectID, `{"decision":"approve","comment":"步骤完整"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("approve review: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/testing/cases/%d/history", item.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK || len(jsonMap(t, w)["items"].([]any)) < 2 {
		t.Fatalf("history missing: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/testing/cases/%d/reviews", item.ID), "u_admin", projectID, "")
	if w.Code != http.StatusOK || len(jsonMap(t, w)["items"].([]any)) != 2 {
		t.Fatalf("reviews missing: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/test-executions/1", "u_qa", projectID, `{"status":"阻塞","note":"外部依赖"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled blocked status accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestTestingDesignsRemainScopedAndAtomic(t *testing.T) {
	a := testApp(t)
	item := createWorkspaceCase(t, a, "设计关联用例")
	w := apiRequest(a, http.MethodPost, "/api/testing/designs", "u_admin", projectID, fmt.Sprintf(`{"name":"登录方案","requirementId":1,"description":"覆盖核心分支","tags":["登录"],"points":[{"title":"成功路径","category":"主流程","priority":"P1","caseIds":[%d]}]}`, item.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("design create: %d %s", w.Code, w.Body.String())
	}
	designID := testingJSONID(t, jsonMap(t, w))
	// 失败 PATCH 不能先删除原设计点；这里尝试把当前项目设计关联到另一个项目的需求。
	var foreignID int64
	if err := a.db.QueryRow(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, insightProjectID).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/testing/designs/%d", designID), "u_admin", projectID, fmt.Sprintf(`{"name":"越权设计","requirementId":%d,"points":[]}`, foreignID))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign design accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, fmt.Sprintf("/api/testing/designs/%d", designID), "u_admin", projectID, "")
	loaded := jsonMap(t, w)
	if w.Code != http.StatusOK || len(loaded["points"].([]any)) != 1 {
		t.Fatalf("failed patch lost points: %d %s", w.Code, w.Body.String())
	}
	pointID := int64(loaded["points"].([]any)[0].(map[string]any)["id"].(float64))
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/testing/designs/%d/points/%d/case", designID, pointID), "u_admin", insightProjectID, fmt.Sprintf(`{"caseId":%d}`, item.ID))
	if w.Code != http.StatusNotFound && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign point link accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestTestingDesignTagsAcceptOptionalBlankString(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  json.RawMessage
		want []string
	}{
		{name: "empty", raw: json.RawMessage(`""`), want: []string{}},
		{name: "whitespace", raw: json.RawMessage(`" \t "`), want: []string{}},
		{name: "comma separated", raw: json.RawMessage(`"回归, 性能, 冒烟"`), want: []string{"回归", "性能", "冒烟"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseTestingDesignTags(test.raw)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parse tags=%#v, %v; want %#v", got, err, test.want)
			}
		})
	}

	// 这条 API 回归覆盖前端默认 tags:""：名称、测试点与关联用例均合法时，
	// 可选标签不应使整个设计保存失败。
	a := testApp(t)
	item := createWorkspaceCase(t, a, "空标签设计关联用例")
	w := apiRequest(a, http.MethodPost, "/api/testing/designs", "u_admin", projectID, fmt.Sprintf(`{"name":"空标签设计","tags":"","points":[{"title":"主路径","category":"功能","priority":"P1","caseIds":[%d]}]}`, item.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("blank tags design create: %d %s", w.Code, w.Body.String())
	}
}
