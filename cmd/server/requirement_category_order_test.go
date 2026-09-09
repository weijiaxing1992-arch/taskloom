package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

type requirementCategoryOrderResponse struct {
	Items        []RequirementCategory `json:"items"`
	CanManage    bool                  `json:"canManage"`
	OrderVersion string                `json:"orderVersion"`
}

func readRequirementCategoryOrder(t *testing.T, a *App, user, project string) requirementCategoryOrderResponse {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/requirement-categories", user, project, "")
	if w.Code != http.StatusOK {
		t.Fatalf("category list: %d %s", w.Code, w.Body.String())
	}
	var result requirementCategoryOrderResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) == 0 || result.OrderVersion == "" {
		t.Fatalf("missing ordered category payload: %s", w.Body.String())
	}
	return result
}

func categoryIDs(items []RequirementCategory) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func categoryByName(t *testing.T, items []RequirementCategory, name string) RequirementCategory {
	t.Helper()
	for _, item := range items {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("category %q missing from %#v", name, items)
	return RequirementCategory{}
}

func TestRequirementCategoryOrderPersistsAndRejectsStaleWrites(t *testing.T) {
	a := testApp(t)
	first := readRequirementCategoryOrder(t, a, "u_admin", projectID)
	if !first.CanManage || first.Items[0].Name != "未分类" || first.Items[0].SortOrder != 0 {
		t.Fatalf("default category order invalid: %+v", first)
	}
	for index, item := range first.Items {
		if item.SortOrder != index*requirementCategoryOrderStep {
			t.Fatalf("default sort order not normalized at %d: %+v", index, first.Items)
		}
	}

	alpha := createPlanningCategory(t, a, "排序-Alpha")
	beta := createPlanningCategory(t, a, "排序-Beta")
	before := readRequirementCategoryOrder(t, a, "u_admin", projectID)
	if alpha.SortOrder <= 0 || beta.SortOrder <= alpha.SortOrder {
		t.Fatalf("new categories were not appended: alpha=%+v beta=%+v", alpha, beta)
	}
	ordered := []int64{before.Items[0].ID, beta.ID, alpha.ID}
	for _, item := range before.Items[1:] {
		if item.ID != alpha.ID && item.ID != beta.ID {
			ordered = append(ordered, item.ID)
		}
	}
	payload := jsonText(map[string]any{"orderedIds": ordered, "orderVersion": before.OrderVersion})
	w := apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, payload)
	if w.Code != http.StatusOK {
		t.Fatalf("category reorder: %d %s", w.Code, w.Body.String())
	}
	var after requirementCategoryOrderResponse
	if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.OrderVersion == before.OrderVersion || !after.CanManage || len(after.Items) != len(ordered) {
		t.Fatalf("reorder response missing state: %+v", after)
	}
	for index, item := range after.Items {
		if item.ID != ordered[index] || item.SortOrder != index*requirementCategoryOrderStep {
			t.Fatalf("persisted category order mismatch at %d: want=%v got=%+v", index, ordered, after.Items)
		}
	}

	// 重启迁移不能把管理员已经设置的顺序还原成预设分类顺序。
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	restarted := readRequirementCategoryOrder(t, a, "u_admin", projectID)
	if got := categoryIDs(restarted.Items); jsonText(got) != jsonText(ordered) {
		t.Fatalf("restart reset category ordering: want=%v got=%v", ordered, got)
	}

	// 旧版本号可防止另一个管理者悄悄覆盖已经落库的排序。
	w = apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, payload)
	if w.Code != http.StatusConflict || jsonMap(t, w)["error"].(map[string]any)["code"] != "category_order_changed" {
		t.Fatalf("stale category order accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementCategoryOrderValidatesScopeAndReservedCategory(t *testing.T) {
	a := testApp(t)
	items := readRequirementCategoryOrder(t, a, "u_admin", projectID)
	ids := categoryIDs(items.Items)

	// 只有当前项目允许的配置角色可以改变菜单顺序。
	w := apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_front", projectID, jsonText(map[string]any{"orderedIds": ids}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer reordered categories: %d %s", w.Code, w.Body.String())
	}

	// 未分类是兜底项，必须在首位；重复、漏项和跨项目 ID 都不能落库。
	wrongFirst := append([]int64{ids[1], ids[0]}, ids[2:]...)
	w = apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, jsonText(map[string]any{"orderedIds": wrongFirst, "orderVersion": items.OrderVersion}))
	if w.Code != http.StatusUnprocessableEntity || jsonMap(t, w)["error"].(map[string]any)["code"] != "reserved_category_order" {
		t.Fatalf("reserved category moved: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, jsonText(map[string]any{"orderedIds": append(ids, ids[0])}))
	if w.Code != http.StatusUnprocessableEntity || jsonMap(t, w)["error"].(map[string]any)["code"] != "invalid_category_order" {
		t.Fatalf("duplicate category ID accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, jsonText(map[string]any{"orderedIds": ids[:len(ids)-1]}))
	if w.Code != http.StatusConflict || jsonMap(t, w)["error"].(map[string]any)["code"] != "category_order_changed" {
		t.Fatalf("partial category order accepted: %d %s", w.Code, w.Body.String())
	}

	foreign := readRequirementCategoryOrder(t, a, "u_admin", insightProjectID)
	foreignID := categoryByName(t, foreign.Items, "未分类").ID
	ids[len(ids)-1] = foreignID
	w = apiRequest(a, http.MethodPatch, "/api/requirement-categories/order", "u_admin", projectID, jsonText(map[string]any{"orderedIds": ids}))
	if w.Code != http.StatusConflict || jsonMap(t, w)["error"].(map[string]any)["code"] != "category_order_changed" {
		t.Fatalf("foreign category ID accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementCategoryOrderMigrationAddsColumnWithoutReorderingRestart(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DROP TABLE requirement_categories; CREATE TABLE requirement_categories(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,name TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,name)); DELETE FROM requirement_category_migrations WHERE tenant_id=? AND project_id=? AND version=?`, tenantID, projectID, requirementCategoryOrderVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO requirement_categories(tenant_id,project_id,name,created_at,updated_at)VALUES(?,?,?,'old','old'),(?,?,?,'old','old')`, tenantID, projectID, "未分类", tenantID, projectID, "自定义历史分类"); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementCollaboration(); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('requirement_categories') WHERE name='sort_order'`).Scan(&columns); err != nil || columns != 1 {
		t.Fatalf("sort_order migration missing: columns=%d err=%v", columns, err)
	}
	if err := a.migrateRequirementCategoryOrdering(); err != nil {
		t.Fatal(err)
	}
	items, err := a.listRequirementCategories()
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Name != "未分类" || categoryByName(t, items, "自定义历史分类").SortOrder == 0 {
		t.Fatalf("existing category order not migrated: %+v", items)
	}
}
