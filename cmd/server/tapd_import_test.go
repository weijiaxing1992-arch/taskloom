package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTapdImportPreservesStructuredBodyAndIndependentImages(t *testing.T) {
	a := fileSQLiteTestApp(t)
	payload := tapdTestPayload()
	image := rtPNG(t)
	payload["descriptionDoc"] = rtDoc(
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{rtText("导入规则")}},
		rtParagraph(rtText("折行已恢复为完整段落。")),
		rtPending("image", "TAPD-image-1-1.png", image),
		map[string]any{"type": "orderedList", "attrs": map[string]any{"start": 3}, "content": []any{
			map[string]any{"type": "listItem", "content": []any{rtParagraph(rtText("第三条规则的折行仍在同一项。"))}},
			map[string]any{"type": "listItem", "content": []any{rtParagraph(rtText("第四条规则。"))}},
		}},
	)
	payload["tapdImport"].(map[string]any)["warnings"] = []string{"第 2 页含表格或分栏，请核对原始 PDF。"}
	body, _ := json.Marshal(payload)
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
	if w.Code != 201 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var result Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	stored, err := a.get(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Content []map[string]any `json:"content"`
	}
	if err = json.Unmarshal(stored.DescriptionDoc, &doc); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"heading", "paragraph", "image", "orderedList"} {
		if len(doc.Content) != 4 || doc.Content[i]["type"] != kind {
			t.Fatalf("structured body lost: %s", stored.DescriptionDoc)
		}
	}
	if doc.Content[3]["attrs"].(map[string]any)["start"] != float64(3) || len(doc.Content[3]["content"].([]any)) != 2 {
		t.Fatalf("list numbering/items changed: %s", stored.DescriptionDoc)
	}
	if bytes.Contains(stored.DescriptionDoc, []byte(`"data"`)) || bytes.Contains(stored.DescriptionDoc, []byte("TAPD-page-")) {
		t.Fatal("pending binary or page screenshot retained in body")
	}
	var actual []byte
	if err = a.db.QueryRow(`SELECT content FROM requirement_attachments WHERE requirement_id=? AND name='TAPD-image-1-1.png'`, stored.ID).Scan(&actual); err != nil || !bytes.Equal(actual, image) {
		t.Fatalf("independent original image changed: %v", err)
	}
	if rtCount(t, a, "requirement_attachments") != 3 {
		t.Fatal("expected original image, source PDF, and mapping only")
	}
	var archive []byte
	if err = a.db.QueryRow(`SELECT content FROM requirement_attachments WHERE requirement_id=? AND content_type='application/json'`, stored.ID).Scan(&archive); err != nil || !bytes.Contains(archive, []byte("表格或分栏")) {
		t.Fatalf("layout warning not archived: %v", err)
	}
}

func tapdTestPayload() map[string]any {
	return map[string]any{"title": "TAPD 迁移测试", "description": "完整正文", "tapdImport": map[string]any{"workspaceId": "32131908", "sourceId": "1132131908001008228", "fileName": "需求.pdf", "pdfBase64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nfixture\n%%EOF\n")), "pageCount": 2, "fields": []map[string]string{{"label": "处理人", "value": "原始姓名"}, {"label": "未知字段", "value": "原值不能丢失"}, {"label": "创建人", "value": "来源创建人"}}, "rawText": "原文", "warnings": []string{"请核对"}, "reviewed": true}}
}
func TestTapdImportAtomicProvenanceAndIdempotency(t *testing.T) {
	a := fileSQLiteTestApp(t)
	payload := tapdTestPayload()
	payload["descriptionDoc"] = rtDoc(rtParagraph(map[string]any{"type": "text", "text": "完整正文"}), rtPending("image", "TAPD-page-1.png", rtPNG(t)))
	body, _ := json.Marshal(payload)
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
	if w.Code != 201 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	result := jsonMap(t, w)
	id := int64(result["id"].(float64))
	if result["tapdImport"] != nil || !strings.Contains(result["remarks"].(string), "未知字段：原值不能丢失") {
		t.Fatal("missing provenance or leaked binary")
	}
	var attachments, imports int
	a.db.QueryRow(`SELECT COUNT(*) FROM requirement_attachments WHERE requirement_id=?`, id).Scan(&attachments)
	a.db.QueryRow(`SELECT COUNT(*) FROM requirement_tapd_imports WHERE requirement_id=?`, id).Scan(&imports)
	if attachments != 3 || imports != 1 {
		t.Fatalf("attachments %d imports %d", attachments, imports)
	}
	var snapshot []byte
	if err := a.db.QueryRow(`SELECT content FROM requirement_attachments WHERE requirement_id=? AND content_type='application/json'`, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var archive map[string]any
	if err := json.Unmarshal(snapshot, &archive); err != nil {
		t.Fatal(err)
	}
	if archive["importedBy"] != "u_admin" || strings.Contains(string(snapshot), "pdfBase64") || !strings.Contains(string(snapshot), "attachmentId") {
		t.Fatal("incorrect audit identity or binary in provenance")
	}
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
	if w.Code != 200 || jsonMap(t, w)["alreadyImported"] != true {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	payload["tapdImport"].(map[string]any)["pdfBase64"] = base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\nchanged\n%%EOF\n"))
	body, _ = json.Marshal(payload)
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
	if w.Code != 409 {
		t.Fatalf("changed source %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/attachments", id), "u_admin", insightProjectID, "")
	if w.Code != 404 {
		t.Fatalf("cross-project %d", w.Code)
	}
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
}

func TestTapdImportGuardsAndRollback(t *testing.T) {
	a := fileSQLiteTestApp(t)
	for _, user := range []string{"u_viewer", "u_pm"} {
		body, _ := json.Marshal(tapdTestPayload())
		w := apiRequest(a, "POST", "/api/requirements", user, projectID, string(body))
		if w.Code != 403 {
			t.Fatalf("permission %s %d %s", user, w.Code, w.Body.String())
		}
	}
	for _, change := range []func(map[string]any){func(in map[string]any) { in["reviewed"] = false }, func(in map[string]any) { in["pdfBase64"] = "not pdf" }, func(in map[string]any) { in["fileName"] = "../x.pdf" }, func(in map[string]any) { in["sourceId"] = "../1" }, func(in map[string]any) { in["pageCount"] = 31 }} {
		p := tapdTestPayload()
		change(p["tapdImport"].(map[string]any))
		body, _ := json.Marshal(p)
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
		if w.Code != 422 {
			t.Fatalf("invalid import %d %s", w.Code, w.Body.String())
		}
	}
	var before int
	a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&before)
	if _, err := a.db.Exec(`CREATE TRIGGER fail_tapd_snapshot BEFORE INSERT ON requirement_attachments WHEN NEW.content_type='application/json' BEGIN SELECT RAISE(ABORT,'injected snapshot failure'); END`); err != nil {
		t.Fatal(err)
	}
	payload := tapdTestPayload()
	payload["descriptionDoc"] = rtDoc(rtPending("image", "TAPD-page-1.png", rtPNG(t)))
	body, _ := json.Marshal(payload)
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, string(body))
	if w.Code != 500 {
		t.Fatalf("injected failure %d %s", w.Code, w.Body.String())
	}
	var after, files, imports int
	a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&after)
	a.db.QueryRow(`SELECT COUNT(*) FROM requirement_attachments`).Scan(&files)
	a.db.QueryRow(`SELECT COUNT(*) FROM requirement_tapd_imports`).Scan(&imports)
	if before != after || files != 0 || imports != 0 {
		t.Fatalf("partial import retained: %d %d %d %d", before, after, files, imports)
	}
}
