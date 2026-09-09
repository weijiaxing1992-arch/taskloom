package main

import (
	"fmt"
	"net/http"
	"testing"
)

// 字段退役后，旧客户端传入的数据不能恢复旧分配或出现在任何业务响应中。
// 旧数据库列保留作存档，避免升级时删除历史信息；新业务路径不依赖这些列。
func TestSprintRetiredFieldsDoNotAffectLifecycleOrExports(t *testing.T) {
	a := testApp(t)
	for _, statement := range []string{
		`ALTER TABLE sprints ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sprints ADD COLUMN owner_user_ids_json TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	w := apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", projectID, `{"name":"简化迭代","startDate":"2026-09-04","endDate":"2026-09-10","owner":"ignored","ownerUserIds":["u_front"]}`)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	assertNoRetiredSprintFields(t, jsonMap(t, w))
	// 即使遗留列损坏，编辑和完成也不应再解析它们。
	if _, err := a.db.Exec(`UPDATE sprints SET owner='archived',owner_user_ids_json='broken-json' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/sprints/%d", id), "u_admin", projectID, `{"status":"进行中","ownerUserIds":["u_back"]}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNoRetiredSprintFields(t, jsonMap(t, w)["sprint"].(map[string]any))
	w = apiRequest(a, http.MethodGet, "/api/sprints", "u_admin", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, item := range jsonMap(t, w)["items"].([]any) {
		assertNoRetiredSprintFields(t, item.(map[string]any))
	}
	key := integrationAPIToken(t, a)
	w = securityIntegrationRequest(t, a, key, http.MethodGet, fmt.Sprintf("/api/open/v1/iterations/%d", id), "", nil)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	assertNoRetiredSprintFields(t, jsonMap(t, w))
	for _, create := range []bool{true, false} {
		assertNoRetiredSprintFields(t, integrationOpenAPIWriteSchema("iterations", create)["properties"].(map[string]any))
	}
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/sprints/%d/complete", id), "u_admin", projectID, `{"targetSprint":"待规划"}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var archived string
	if err := a.db.QueryRow(`SELECT owner_user_ids_json FROM sprints WHERE id=?`, id).Scan(&archived); err != nil || archived != "broken-json" {
		t.Fatalf("archive changed: %q %v", archived, err)
	}
}

func assertNoRetiredSprintFields(t *testing.T, fields map[string]any) {
	t.Helper()
	for _, key := range []string{"owner", "ownerUserIds", "owners"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("retired field exposed: %s", key)
		}
	}
}
