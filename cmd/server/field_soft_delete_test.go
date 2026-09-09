package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func deleteFieldForTest(t *testing.T, a *App, d FieldDefinition) {
	t.Helper()
	w := apiRequest(a, "DELETE", fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, jsonText(map[string]any{"confirmKey": d.Key, "objectType": d.ObjectType}))
	if w.Code != 200 || jsonMap(t, w)["deleted"] != true {
		t.Fatalf("delete field: %d %s", w.Code, w.Body.String())
	}
}

func TestFieldSoftDeletePreservesValuesAndHidesOnlyDeletedFields(t *testing.T) {
	for _, object := range []string{"requirement", "defect", "test_case", "sprint"} {
		t.Run(object, func(t *testing.T) {
			a := testApp(t)
			d := fieldTestDefinition(t, a, fmt.Sprintf(`{"objectType":%q,"key":"retired","name":"待删字段","type":"text","defaultValue":"原默认","required":true,"searchable":true,"filterable":true,"listVisible":true}`, object))
			inactive := fieldTestDefinition(t, a, fmt.Sprintf(`{"objectType":%q,"key":"inactive_history","name":"停用但未删除","type":"text"}`, object))
			for _, def := range []FieldDefinition{d, inactive} {
				if _, err := a.db.Exec(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, projectID, object, 1, def.ID, jsonText("历史内容"), "now"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.db.Exec(`UPDATE field_definitions SET enabled=0 WHERE id=?`, inactive.ID); err != nil {
				t.Fatal(err)
			}
			deleteFieldForTest(t, a, d)
			var deletedAt, rawValue, before, after string
			var enabled, searchable, filterable, visible bool
			if err := a.db.QueryRow(`SELECT deleted_at,enabled,searchable,filterable,list_visible FROM field_definitions WHERE id=?`, d.ID).Scan(&deletedAt, &enabled, &searchable, &filterable, &visible); err != nil || deletedAt == "" || enabled || searchable || filterable || visible {
				t.Fatalf("invalid tombstone: %q %v %v", deletedAt, enabled, err)
			}
			if err := a.db.QueryRow(`SELECT value_json FROM field_values WHERE field_definition_id=?`, d.ID).Scan(&rawValue); err != nil || rawValue != jsonText("历史内容") {
				t.Fatalf("historical value removed: %q %v", rawValue, err)
			}
			if err := a.db.QueryRow(`SELECT before_json,after_json FROM audit_logs WHERE object_type='field_definition' AND object_id=? AND action='soft_delete'`, fmt.Sprint(d.ID)).Scan(&before, &after); err != nil {
				t.Fatal(err)
			}
			var prior FieldDefinition
			var result map[string]any
			if json.Unmarshal([]byte(before), &prior) != nil || prior.ID != d.ID || !prior.Enabled || !prior.Required || !prior.Searchable || prior.DefaultValue != "原默认" || json.Unmarshal([]byte(after), &result) != nil || result["preservedValueCount"] != float64(1) {
				t.Fatalf("incomplete recovery audit: %s %s", before, after)
			}
			w := apiRequest(a, "GET", "/api/field-definitions?objectType="+object, "u_viewer", projectID, "")
			if w.Code != 200 || strings.Contains(w.Body.String(), `"key":"retired"`) || !strings.Contains(w.Body.String(), `"key":"inactive_history"`) {
				t.Fatalf("catalog includes deleted / excludes disabled: %d %s", w.Code, w.Body.String())
			}
			for _, reader := range []func() (map[string]any, error){
				func() (map[string]any, error) { return a.customFieldsUsing(a.db, object, 1) },
				func() (map[string]any, error) { return a.checkedWorkCustomFields(context.Background(), object, 1) },
				func() (map[string]any, error) { return a.customFields(object, 1), nil },
			} {
				values, err := reader()
				if err != nil || values["retired"] != nil || values["inactive_history"] != "历史内容" {
					t.Fatalf("reader did not distinguish deletion and disabling: %+v %v", values, err)
				}
			}
			if object == "requirement" || object == "defect" {
				document, err := a.workPDF(object, 1)
				if err != nil {
					t.Fatal(err)
				}
				blocks := ""
				for _, block := range document.blocks {
					blocks += block.title + block.text
				}
				if strings.Contains(blocks, d.Name) || !strings.Contains(blocks, inactive.Name) {
					t.Fatalf("PDF hid disabled / exposed deleted field: %s", blocks)
				}
			}
			for _, method := range []string{"DELETE", "PATCH"} {
				body := `{"enabled":true}`
				if method == "DELETE" {
					body = jsonText(map[string]any{"confirmKey": d.Key, "objectType": object})
				}
				w = apiRequest(a, method, fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, body)
				if w.Code != 404 {
					t.Fatalf("deleted field %s accepted: %d %s", method, w.Code, w.Body.String())
				}
			}
			var count int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE object_type='field_definition' AND object_id=? AND action='soft_delete'`, fmt.Sprint(d.ID)).Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate deletion audit: %d %v", count, err)
			}
		})
	}
}

func TestFieldSoftDeleteConfirmationScopeAndLivePermissions(t *testing.T) {
	a := testApp(t)
	d := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"secure_field","name":"受保护字段","type":"text"}`)
	path := fmt.Sprintf("/api/field-definitions/%d", d.ID)
	valid := `{"confirmKey":"secure_field","objectType":"requirement"}`
	for _, tc := range []struct {
		user, project, body string
		status              int
	}{
		{"u_admin", projectID, `{}`, 422},
		{"u_admin", projectID, `{"confirmKey":"other","objectType":"requirement"}`, 409},
		{"u_admin", projectID, `{"confirmKey":"secure_field","objectType":"defect"}`, 409},
		{"u_admin", projectID, valid + ` {}`, 422},
		{"u_admin", insightProjectID, valid, 404},
		{"u_front", projectID, valid, 403},
		{"u_viewer", projectID, valid, 403},
	} {
		w := apiRequest(a, "DELETE", path, tc.user, tc.project, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	for _, systemPath := range []string{"title", "role.frontend.value"} {
		w := apiRequest(a, "DELETE", "/api/field-definitions/"+systemPath, "u_admin", projectID, jsonText(map[string]string{"confirmKey": systemPath, "objectType": "requirement"}))
		if w.Code != 400 {
			t.Fatalf("system field route accepted: %d %s", w.Code, w.Body.String())
		}
	}
	// A real field ID belonging to a different tenant must not be visible, even
	// to an administrator of this tenant and with a matching project string.
	result, err := a.db.Exec(`INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,created_at,updated_at)VALUES('foreign',?,'requirement','foreign_field','另一企业字段','text','now','now')`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	foreignID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "DELETE", fmt.Sprintf("/api/field-definitions/%d", foreignID), "u_admin", projectID, `{"confirmKey":"foreign_field","objectType":"requirement"}`)
	if w.Code != 404 {
		t.Fatalf("foreign tenant field leaked: %d %s", w.Code, w.Body.String())
	}
	// Call the handler directly: it must not depend on a previously authorized
	// middleware snapshot for destructive configuration changes.
	scoped := *a
	scoped.user = "u_admin"
	for _, mutation := range []string{
		`UPDATE users SET operation_disabled=1 WHERE id='u_admin'`,
		`UPDATE users SET operation_disabled=0 WHERE id='u_admin'; UPDATE tenant_memberships SET role='member' WHERE user_id='u_admin'; UPDATE project_members SET role='viewer' WHERE user_id='u_admin'`,
		`DELETE FROM project_members WHERE user_id='u_admin'`,
		`UPDATE tenant_memberships SET status='inactive' WHERE user_id='u_admin'`,
	} {
		if _, err := a.db.Exec(mutation); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		scoped.fieldDefinition(w, httptest.NewRequest("DELETE", path, strings.NewReader(valid)))
		if w.Code != 403 {
			t.Fatalf("live permission not checked: %d %s", w.Code, w.Body.String())
		}
	}
	var deletedAt string
	if err := a.db.QueryRow(`SELECT deleted_at FROM field_definitions WHERE id=?`, d.ID).Scan(&deletedAt); err != nil || deletedAt != "" {
		t.Fatalf("unauthorized deletion persisted: %q %v", deletedAt, err)
	}
}

func TestFieldSoftDeleteAuditFailureRollsBack(t *testing.T) {
	a := testApp(t)
	d := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"rollback","name":"不可部分删除","type":"text","searchable":true}`)
	if _, err := a.db.Exec(`CREATE TRIGGER reject_field_deletion BEFORE INSERT ON audit_logs WHEN NEW.action='soft_delete' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "DELETE", fmt.Sprintf("/api/field-definitions/%d", d.ID), "u_admin", projectID, `{"confirmKey":"rollback","objectType":"requirement"}`)
	if w.Code != 503 {
		t.Fatalf("expected unavailable: %d %s", w.Code, w.Body.String())
	}
	var deleted string
	var enabled, searchable bool
	if err := a.db.QueryRow(`SELECT deleted_at,enabled,searchable FROM field_definitions WHERE id=?`, d.ID).Scan(&deleted, &enabled, &searchable); err != nil || deleted != "" || !enabled || !searchable {
		t.Fatalf("partial deletion: %q %v %v %v", deleted, enabled, searchable, err)
	}
}

func TestFieldSoftDeleteRestartPresetsAndStaleBusinessWrites(t *testing.T) {
	a := testApp(t)
	defs, err := a.definitions("requirement", false)
	if err != nil {
		t.Fatal(err)
	}
	var testers FieldDefinition
	for _, d := range defs {
		if d.Key == "testers" {
			testers = d
		}
	}
	if testers.ID == 0 {
		t.Fatal("missing seeded testers")
	}
	x := planningRequirement(t, a, `{"title":"保留需求","customFields":{"testers":["u_qa"]}}`)
	prepared, err := a.prepareObjectFields("requirement", map[string]any{"testers": []any{"u_qa"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	deleteFieldForTest(t, a, testers)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.writeObjectFields(tx, "requirement", x.ID, prepared, "now"); err == nil {
		tx.Rollback()
		t.Fatal("stale prepared field write accepted")
	}
	tx.Rollback()
	for round := 0; round < 2; round++ {
		if err := a.migrate(); err != nil {
			t.Fatal(err)
		}
		w := apiRequest(a, "POST", "/api/field-presets/apply", "u_admin", projectID, `{"objectType":"requirement","keys":["testers"]}`)
		if w.Code != 200 || jsonMap(t, w)["createdCount"] != float64(0) || jsonMap(t, w)["skippedCount"] != float64(1) {
			t.Fatalf("preset revived: %d %s", w.Code, w.Body.String())
		}
		w = apiRequest(a, "GET", "/api/field-presets?objectType=requirement", "u_admin", projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		for _, raw := range jsonMap(t, w)["presets"].([]any) {
			p := raw.(map[string]any)
			if p["key"] == "testers" && p["installed"] != true {
				t.Fatal("deleted preset offered again")
			}
		}
	}
	w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, `{"objectType":"requirement","key":"testers","name":"测试人员","type":"users"}`)
	if w.Code != 409 {
		t.Fatalf("reserved key reused: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"title":"不应保存","customFields":{"testers":["u_qa"]}}`)
	if w.Code != 422 {
		t.Fatalf("stale browser saved retired value: %d %s", w.Code, w.Body.String())
	}
	saved, err := a.get(x.ID)
	if err != nil || saved.Title != x.Title || saved.CustomFields["testers"] != nil {
		t.Fatalf("core changed / deleted value exposed: %+v %v", saved, err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"remarks":"正常更新"}`)
	if w.Code != 200 {
		t.Fatalf("unrelated business edit blocked: %d %s", w.Code, w.Body.String())
	}
	var raw string
	if err := a.db.QueryRow(`SELECT value_json FROM field_values WHERE field_definition_id=? AND object_id=?`, testers.ID, x.ID).Scan(&raw); err != nil || raw != `["u_qa"]` {
		t.Fatalf("unrelated edit destroyed archive: %q %v", raw, err)
	}
	filters := url.QueryEscape(`[{"field":"custom.testers","operator":"neq","value":"u_qa"}]`)
	w = apiRequest(a, "GET", "/api/requirements?filters="+filters, "u_admin", projectID, "")
	if w.Code != 422 {
		t.Fatalf("retired filter silently broadened: %d %s", w.Code, w.Body.String())
	}
}
