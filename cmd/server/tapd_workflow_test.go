package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestTapdImportHistoricalStateAuthorizationAndScope(t *testing.T) {
	a := fileSQLiteTestApp(t)
	// Deliberately remove every normal transition: an import records a historical
	// state, but must not leave a workflow bypass for later edits.
	if _, err := a.db.Exec(`UPDATE requirement_workflows SET transitions_json='[]' WHERE tenant_id=? AND project_id=?`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	p := tapdTestPayload()
	p["status"] = "后端已完成"
	p["tapdImport"].(map[string]any)["fields"] = []map[string]string{{"label": "状态", "value": "后端已完成"}}
	for _, user := range []string{"u_pm", "u_front", "u_viewer"} {
		w := apiRequest(a, "POST", "/api/requirements", user, projectID, jsonText(p))
		if w.Code != 403 {
			t.Fatalf("nonadmin historical import %s: %d %s", user, w.Code, w.Body.String())
		}
	}
	for _, user := range []string{"u_admin", "u_pm"} {
		w := apiRequest(a, "POST", "/api/requirements", user, projectID, `{"title":"ordinary create","status":"后端已完成"}`)
		if w.Code != 422 {
			t.Fatalf("ordinary create bypass: %s %d %s", user, w.Code, w.Body.String())
		}
	}
	for _, test := range []struct{ key, scope string }{{"tapd_foreign_project", insightProjectID}, {"tapd_foreign_tenant", projectID}, {"tapd_disabled", projectID}} {
		tenant, enabled := tenantID, 1
		if test.key == "tapd_foreign_tenant" {
			tenant = "other-tenant"
		}
		if test.key == "tapd_disabled" {
			enabled = 0
		}
		if _, err := a.db.Exec(`INSERT INTO requirement_statuses(tenant_id,project_id,key,name,color,category,enabled,created_at,updated_at)VALUES(?,?,?,?,'#123456','doing',?,'now','now')`, tenant, test.scope, test.key, test.key, enabled); err != nil {
			t.Fatal(err)
		}
		p["status"] = test.key
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
		if w.Code != 422 {
			t.Fatalf("invalid scoped historical state %s: %d %s", test.key, w.Code, w.Body.String())
		}
	}
	if rtCount(t, a, "requirement_tapd_imports") != 0 || rtCount(t, a, "requirement_attachments") != 0 {
		t.Fatal("rejected import left data")
	}
	p["status"] = "后端已完成"
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
	if w.Code != 201 {
		t.Fatalf("historical state import: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	var state, actor, audit string
	if err := a.db.QueryRow(`SELECT status,created_by FROM requirements WHERE id=?`, id).Scan(&state, &actor); err != nil || state != "后端已完成" || actor != "u_admin" {
		t.Fatalf("historical state/identity lost: %s %s %v", state, actor, err)
	}
	if err := a.db.QueryRow(`SELECT after_json FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_id=? AND action='tapd_import' AND actor_id='u_admin'`, tenantID, projectID, fmt.Sprint(id)).Scan(&audit); err != nil || !strings.Contains(audit, "historical_state_import") || !strings.Contains(audit, "后端已完成") {
		t.Fatalf("historical import audit missing: %s %v", audit, err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", id), "u_admin", projectID, `{"status":"已上线"}`)
	if w.Code != 403 {
		t.Fatalf("import left a normal edit bypass: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
	if w.Code != 200 || jsonMap(t, w)["alreadyImported"] != true {
		t.Fatalf("historical retry: %d %s", w.Code, w.Body.String())
	}
	var audits int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='tapd_import'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("retry duplicated import audit")
	}
	// A project administrator has the same explicitly scoped migration right,
	// without being promoted to enterprise administrator.
	if _, err := a.db.Exec(`UPDATE memberships SET role='project_admin' WHERE project_id=? AND user_id='u_front'; UPDATE project_members SET role='project_admin' WHERE project_id=? AND user_id='u_front'`, projectID, projectID); err != nil {
		t.Fatal(err)
	}
	p["tapdImport"].(map[string]any)["sourceId"] = "123456"
	p["tapdImport"].(map[string]any)["pdfBase64"] = "JVBERi0xLjYKcHJvamVjdCBhZG1pbgolJUVPRgo="
	w = apiRequest(a, "POST", "/api/requirements", "u_front", projectID, jsonText(p))
	if w.Code != 201 {
		t.Fatalf("project admin import: %d %s", w.Code, w.Body.String())
	}
}

func TestTapdImportAuditFailureRollsBackAndCanRetry(t *testing.T) {
	a := fileSQLiteTestApp(t)
	before := rtCount(t, a, "requirements")
	if _, err := a.db.Exec(`CREATE TRIGGER fail_tapd_audit BEFORE INSERT ON audit_logs WHEN NEW.action='tapd_import' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	p := tapdTestPayload()
	p["status"] = "后端已完成"
	p["descriptionDoc"] = rtDoc(rtParagraph(rtText("用户修订的正文")), rtPending("image", "TAPD-image-1-1.png", rtPNG(t)))
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
	if w.Code != 500 {
		t.Fatalf("audit fault: %d %s", w.Code, w.Body.String())
	}
	if rtCount(t, a, "requirements") != before || rtCount(t, a, "requirement_attachments") != 0 || rtCount(t, a, "requirement_tapd_imports") != 0 {
		t.Fatal("audit failure left partial import")
	}
	if _, err := a.db.Exec(`DROP TRIGGER fail_tapd_audit`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
	if w.Code != 201 || rtCount(t, a, "requirements") != before+1 || rtCount(t, a, "requirement_attachments") != 3 {
		t.Fatalf("retry after rollback: %d %s", w.Code, w.Body.String())
	}
}

// Optional local fixture generated by scripts/test-tapd-pdf.mjs from the user's
// read-only PDF. The document is never committed or sent to a business database.
func TestTapdImportRealSampleLocalOnly(t *testing.T) {
	path := os.Getenv("TAPD_QA_OUTPUT")
	if path == "" {
		t.Skip("set TAPD_QA_OUTPUT to the local parser output for the optional real-sample round trip")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	source["reviewed"] = true
	p := map[string]any{"title": "真实样本本地验证", "status": "后端已完成", "descriptionDoc": source["descriptionDoc"], "tapdImport": source}
	a := fileSQLiteTestApp(t)
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(p))
	if w.Code != 201 {
		t.Fatalf("real-sample local import: %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	x, err := a.get(id)
	if err != nil || x.Status != "后端已完成" || !strings.Contains(x.Description, "替换规则") || !bytes.Contains(x.DescriptionDoc, []byte("orderedList")) {
		t.Fatalf("real-sample document/state lost: %v", err)
	}
	for i, height := range []int{31, 338} {
		var content []byte
		if err := a.db.QueryRow(`SELECT content FROM requirement_attachments WHERE requirement_id=? AND name=?`, id, fmt.Sprintf("TAPD-image-%d-1.png", i+1)).Scan(&content); err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(content))
		if err != nil || cfg.Width != 700 || cfg.Height != height {
			t.Fatalf("real original raster changed: %+v %v", cfg, err)
		}
	}
	if rtCount(t, a, "requirement_attachments") != 4 {
		t.Fatal("expected two independent images, source PDF and mapping")
	}
	t.Log("Read-only real sample round trip passed: historical state, five-item list, two original-size images and four archived attachments")
}
