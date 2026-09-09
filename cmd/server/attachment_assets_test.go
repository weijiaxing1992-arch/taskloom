package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestAttachmentAssetClassification(t *testing.T) {
	for _, x := range []struct{ name, category, language string }{{"Component.vue", "code", "vue"}, {"api.go", "code", "go"}, {"service.php", "code", "php"}, {"parser.cpp", "code", "cpp"}, {"openapi.yaml", "api", "yaml"}, {"bug截图.png", "bug", ""}, {"界面.fig", "design", ""}, {"文档.pdf", "other", ""}} {
		if attachmentCategory(x.name, "auto") != x.category || attachmentLanguage(x.name) != x.language {
			t.Fatalf("classification: %+v", x)
		}
	}
	if category, language := resolveAttachmentAsset("内容.txt", "auto", []byte("package main\nfunc main() {}")); category != "code" || language != "go" {
		t.Fatal("content inference failed")
	}
	a := fileSQLiteTestApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	raw := []byte("<?php\n\t// 中文\n" + strings.Repeat("echo 'safe';\n", 10000))
	w := resourceUpload(t, a, x.ID, "u_admin", projectID, "service.php", raw, false)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	meta := jsonMap(t, w)
	path := meta["downloadUrl"].(string)
	if meta["category"] != "code" || meta["language"] != "php" {
		t.Fatal(meta)
	}
	for _, category := range []string{"bug", "api", "design", "code", "other", "auto"} {
		w = apiRequest(a, "PATCH", path, "u_admin", projectID, fmt.Sprintf(`{"category":%q}`, category))
		if w.Code != 200 {
			t.Fatalf("patch %d %s", w.Code, w.Body.String())
		}
	}
	if w = apiRequest(a, "PATCH", path, "u_viewer", projectID, `{"category":"bug"}`); w.Code != 403 {
		t.Fatalf("viewer write %d", w.Code)
	}
	if w = apiRequest(a, "PATCH", path, "u_admin", insightProjectID, `{"category":"bug"}`); w.Code != 404 {
		t.Fatalf("cross project %d", w.Code)
	}
	if w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"category":"evil"}`); w.Code != 422 {
		t.Fatalf("invalid %d", w.Code)
	}
	w = apiRequest(a, "GET", path+"?metadata=1", "u_admin", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["language"] != "php" || strings.Contains(w.Body.String(), "echo") {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatal("code bytes lost")
	}
	if err := a.migrateRequirementResources(); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", path+"?metadata=1", "u_admin", projectID, "")
	if jsonMap(t, w)["category"] != "code" {
		t.Fatal("migration changed classification")
	}
	manual := resourceUpload(t, a, x.ID, "u_admin", projectID, "screen.txt", []byte("repro"), false, "bug")
	if manual.Code != 201 || jsonMap(t, manual)["category"] != "bug" {
		t.Fatalf("explicit upload category: %d %s", manual.Code, manual.Body.String())
	}
	invalid := resourceUpload(t, a, x.ID, "u_admin", projectID, "bad.txt", []byte("no"), false, "invalid")
	if invalid.Code != 422 {
		t.Fatal("invalid upload category accepted")
	}
	node := rtPending("attachment", "handler.go", []byte("package main\nfunc main() {}\n"))
	node["attrs"].(map[string]any)["category"] = "api"
	w = apiRequest(a, "POST", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, jsonText(map[string]any{"contentDoc": rtDoc(node)}))
	if w.Code != 201 {
		t.Fatalf("comment %d %s", w.Code, w.Body.String())
	}
	var category string
	if err := a.db.QueryRow(`SELECT category FROM requirement_attachments WHERE requirement_id=? AND name='handler.go'`, x.ID).Scan(&category); err != nil || category != "api" {
		t.Fatalf("comment category %s %v", category, err)
	}
	node["attrs"].(map[string]any)["category"] = "invalid"
	if _, err := parseRichDocument(richRaw(rtDoc(node))); err == nil {
		t.Fatal("invalid rich category accepted")
	}
}
