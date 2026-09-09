package main

import (
	"fmt"
	"net/url"
	"testing"
)

func TestMyWorkSprintFilterScopeCountsAndFavorites(t *testing.T) {
	a := testApp(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := a.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sprints(id,tenant_id,project_id,code,name,start_date,end_date,status,created_at,updated_at) VALUES(98001,?,?,'SPR-98001','同名验收迭代','2026-09-01','2026-10-01','进行中','x','x'),(98002,?,?,'SPR-98002','同名验收迭代','2026-09-01','2026-10-01','进行中','x','x')`, tenantID, projectID, tenantID, insightProjectID)
	x := createPeopleRequirement(t, a, map[string]any{"title": "迭代筛选验收A", "sprint": "同名验收迭代", "assigneeUserIds": []string{"u_front"}})
	y := createPeopleRequirement(t, a, map[string]any{"title": "迭代筛选验收B", "assigneeUserIds": []string{"u_front"}})
	exec(`UPDATE requirements SET sprint='待规划' WHERE id=?`, y.ID)
	exec(`INSERT INTO test_cases(tenant_id,project_id,code,title,owner_user_id,requirement_id,created_at,updated_at)VALUES(?,?,'CASE-SCOPE','关联用例','u_front',?,'x','x')`, tenantID, projectID, x.ID)
	path := "/api/my-work?project=all&category=&sprintId=98001"
	w := apiRequest(a, "GET", path, "u_front", projectID, "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	data := jsonMap(t, w)
	foundReq, foundCase := false, false
	for _, raw := range data["items"].([]any) {
		item := raw.(map[string]any)
		if item["projectId"] != projectID || item["sprint"] != "同名验收迭代" {
			t.Fatal(item)
		}
		if item["type"] == "需求" && int64(item["id"].(float64)) == x.ID {
			foundReq = true
		}
		if item["type"] == "测试用例" {
			foundCase = true
		}
	}
	if !foundReq || !foundCase {
		t.Fatal("missing assigned requirement or related test case")
	}
	if data["counts"].(map[string]any)["all"].(float64) != float64(len(data["items"].([]any))) {
		t.Fatal("counts ignore sprint")
	}
	w = apiRequest(a, "GET", "/api/my-work?project="+url.QueryEscape(projectID)+"&sprintId=98002", "u_admin", projectID, "")
	if w.Code != 404 {
		t.Fatalf("cross-project selection %d", w.Code)
	}
	for _, id := range []string{"bad", "-1", "0", "1.5"} {
		w = apiRequest(a, "GET", "/api/my-work?sprintId="+id, "u_admin", projectID, "")
		if w.Code != 422 {
			t.Fatalf("invalid id %q %d", id, w.Code)
		}
	}
	for _, id := range []int64{x.ID, y.ID} {
		w = apiRequest(a, "PUT", fmt.Sprintf("/api/requirements/%d/favorite", id), "u_front", projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w = apiRequest(a, "GET", path+"&view=favorites", "u_front", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f := jsonMap(t, w)
	if len(f["items"].([]any)) != 1 || f["items"].([]any)[0].(map[string]any)["id"].(float64) != float64(x.ID) {
		t.Fatal("favorites not filtered")
	}
	w = apiRequest(a, "GET", "/api/my-work?project=all&category=active&sprintId=98001&view=favorites", "u_front", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 1 || jsonMap(t, w)["counts"].(map[string]any)["active"] != float64(1) {
		t.Fatal("active favorites count/filter mismatch")
	}
	// 筛选元数据也不能暴露项目访问权限外的迭代。
	exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, insightProjectID)
	w = apiRequest(a, "GET", "/api/my-work?project=all", "u_front", projectID, "")
	for _, raw := range jsonMap(t, w)["sprints"].([]any) {
		if raw.(map[string]any)["projectId"] == insightProjectID {
			t.Fatal("inaccessible sprint metadata leaked")
		}
	}
}
