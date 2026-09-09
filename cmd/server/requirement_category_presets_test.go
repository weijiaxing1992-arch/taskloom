package main

import (
	"encoding/json"
	"testing"
)

func categoryNames(t *testing.T, a *App, project string) map[string]int64 {
	t.Helper()
	rows, err := a.db.Query(`SELECT name,id FROM requirement_categories WHERE tenant_id=? AND project_id=?`, tenantID, project)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err = rows.Scan(&name, &id); err != nil {
			t.Fatal(err)
		}
		result[name] = id
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func prepareLegacyCategories(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.db.Exec(`DELETE FROM requirement_category_migrations WHERE tenant_id=? AND project_id=?; DELETE FROM requirement_categories WHERE tenant_id=? AND project_id=?;UPDATE requirements SET category='研发协作/需求' WHERE tenant_id=? AND project_id=?`, tenantID, projectID, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(append([]string{}, legacyRequirementCategoryNames...), "基础能力/自建客户项目", "用户自建分类") {
		if _, err := a.db.Exec(`INSERT INTO requirement_categories(tenant_id,project_id,name,created_at,updated_at)VALUES(?,?,?,'old','old')`, tenantID, projectID, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.Exec(`UPDATE requirements SET category='基础能力/自建客户项目' WHERE tenant_id=? AND project_id=? AND id=2`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
}
func TestRequirementCategoryPresetsMigratePreciselyAndPreserveRequirements(t *testing.T) {
	a := testApp(t)
	prepareLegacyCategories(t, a)
	before, err := a.get(1)
	if err != nil {
		t.Fatal(err)
	}
	customID := categoryNames(t, a, projectID)["基础能力/自建客户项目"]
	otherBefore := categoryNames(t, a, insightProjectID)
	if err = a.migrateRequirementCategoryPresets(); err != nil {
		t.Fatal(err)
	}
	names := categoryNames(t, a, projectID)
	for _, name := range defaultRequirementCategoryNames {
		if names[name] == 0 {
			t.Fatal("missing default", name)
		}
	}
	for _, name := range legacyRequirementCategoryNames {
		if names[name] != 0 {
			t.Fatal("old category survived", name)
		}
	}
	if names["基础能力/自建客户项目"] != customID || names["用户自建分类"] == 0 {
		t.Fatal("user category changed", names)
	}
	after, err := a.get(1)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.Category != "待定" || after.CreatedAt != before.CreatedAt || after.Title != before.Title || after.Description != before.Description || after.Sprint != before.Sprint || after.Status != before.Status || after.Tags != before.Tags {
		t.Fatal("requirement was lost or rewritten", after)
	}
	custom, err := a.get(2)
	if err != nil || custom.Category != "基础能力/自建客户项目" {
		t.Fatal("prefix matching moved user data", custom, err)
	}
	otherAfter := categoryNames(t, a, insightProjectID)
	if jsonText(otherBefore) != jsonText(otherAfter) {
		t.Fatal("already migrated project changed", otherBefore, otherAfter)
	}
	var audit string
	if err = a.db.QueryRow(`SELECT before_json FROM audit_logs WHERE project_id=? AND action='category_presets_migration'`, projectID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if json.Unmarshal([]byte(audit), &rows) != nil || len(rows) != len(legacyRequirementCategoryNames) {
		t.Fatal("missing recoverable mapping", audit)
	}
}
func TestRequirementCategoryPresetsRestartDoesNotOverwriteUserChanges(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE requirement_categories SET name='客户端团队自定义' WHERE tenant_id=? AND project_id=? AND name='客户端';DELETE FROM requirement_categories WHERE tenant_id=? AND project_id=? AND name='全球化';INSERT INTO requirement_categories(tenant_id,project_id,name,created_at,updated_at)VALUES(?,?,'协作开发','new','new')`, tenantID, projectID, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	before := categoryNames(t, a, projectID)
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	after := categoryNames(t, a, projectID)
	if jsonText(before) != jsonText(after) {
		t.Fatal("restart reapplied presets", before, after)
	}
}
func TestRequirementCategoryPresetsFailureRollsBackNamesRowsAndMarker(t *testing.T) {
	a := testApp(t)
	prepareLegacyCategories(t, a)
	before := categoryNames(t, a, projectID)
	if _, err := a.db.Exec(`CREATE TRIGGER fail_category_migration BEFORE UPDATE OF category ON requirements BEGIN SELECT RAISE(ABORT,'fixture failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementCategoryPresets(); err == nil {
		t.Fatal("expected failure")
	}
	after := categoryNames(t, a, projectID)
	if jsonText(before) != jsonText(after) {
		t.Fatal("partial category writes", before, after)
	}
	var markers int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirement_category_migrations WHERE tenant_id=? AND project_id=?`, tenantID, projectID).Scan(&markers); err != nil || markers != 0 {
		t.Fatal("failed migration marked completed", markers, err)
	}
	row, err := a.get(1)
	if err != nil || row.Category != "研发协作/需求" {
		t.Fatal("failed migration moved requirement", row, err)
	}
}
func TestRequirementCategoryPresetsNewProjectReceivesDefaultsOnce(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/projects", "u_admin", projectID, `{"name":"新项目","code":"CATTEST"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	names := categoryNames(t, a, "prj_cattest")
	if len(names) != 6 {
		t.Fatal("new project missing defaults", names)
	}
	for _, name := range defaultRequirementCategoryNames {
		if names[name] == 0 {
			t.Fatal(name, names)
		}
	}
	if _, err := a.db.Exec(`UPDATE requirement_categories SET name='新名称' WHERE tenant_id=? AND project_id='prj_cattest' AND name='管理端'`, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementCategoryPresets(); err != nil {
		t.Fatal(err)
	}
	names = categoryNames(t, a, "prj_cattest")
	if names["管理端"] != 0 || names["新名称"] == 0 {
		t.Fatal("new project lacks migration marker", names)
	}
}
