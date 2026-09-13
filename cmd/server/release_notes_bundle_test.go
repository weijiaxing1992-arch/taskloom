package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestReleaseNotesBundleContainsOfflineOwnedImagesAndNoInternalSource(t *testing.T) {
	a := testApp(t)
	sprint := releaseContentSprint(t, a, "Bundle release")
	id := releaseContentRequirement(t, a, "Bundle release", "已完成", "产品需求", "其他")
	png := releaseContentPNG(t)
	imageID := releaseContentImage(t, a, id, "正式截图.png", "other", png)
	bulkFixtureExec(t, a, `UPDATE requirements SET description='PRIVATE SOURCE NOT FOR EXPORT' WHERE id=?`, id)
	source, _ := readReleaseContent(t, a, sprint)
	entries := releaseContentEntries(source)
	entries[0].ImageIDs = []int64{imageID}
	entries[0].ImageCaptions = map[string]string{fmt.Sprint(imageID): "新功能截图"}
	payload, err := buildReleaseNotesBundle(context.Background(), a.db, source, entries, 3)
	if err != nil {
		t.Fatal(err)
	}
	again, err := buildReleaseNotesBundle(context.Background(), a.db, source, entries, 3)
	if err != nil || !bytes.Equal(payload, again) {
		t.Fatal("bundle not stable")
	}
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		if strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "..") {
			t.Fatal("unsafe zip name")
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = data
		if bytes.Contains(data, []byte("PRIVATE SOURCE NOT FOR EXPORT")) {
			t.Fatal("private source included")
		}
	}
	path := fmt.Sprintf("images/requirement-%d-image-%d.png", id, imageID)
	if len(files) != 4 || !bytes.Equal(files[path], png) {
		t.Fatal("missing real screenshot")
	}
	md := string(files["upgrade-notes.md"])
	if !strings.Contains(md, "]("+path+")") || strings.Contains(md, "/api/requirements/") {
		t.Fatal("markdown still depends on authenticated image URL")
	}
	if !strings.Contains(string(files["upgrade-notes.json"]), path) || len(files["README.md"]) == 0 {
		t.Fatal("missing portable metadata")
	}
	// 删除或替换截图后，旧草稿不能导出错误图片。
	bulkFixtureExec(t, a, `UPDATE requirement_attachments SET content=?,size_bytes=? WHERE id=?`, []byte("changed"), len("changed"), imageID)
	if _, err = buildReleaseNotesBundle(context.Background(), a.db, source, entries, 3); err == nil {
		t.Fatal("changed image exported")
	}
}

func TestReleaseNotesBundleRejectsForeignImagesAndRetainsMissingImageNotice(t *testing.T) {
	a := testApp(t)
	sprint := releaseContentSprint(t, a, "Bundle no images")
	id := releaseContentRequirement(t, a, "Bundle no images", "已完成", "产品需求", "其他")
	source, _ := readReleaseContent(t, a, sprint)
	entries := releaseContentEntries(source)
	if _, err := buildReleaseNotesBundle(context.Background(), a.db, source, entries, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renderReleaseNotesMarkdown(source, entries), "待补图") {
		t.Fatal("missing screenshot represented as ready")
	}
	foreign := releaseContentRequirement(t, a, "Outside", "已完成", "产品需求", "其他")
	imageID := releaseContentImage(t, a, foreign, "图片.png", "other", releaseContentPNG(t))
	entries[0].ImageIDs = []int64{imageID}
	if _, err := buildReleaseNotesBundle(context.Background(), a.db, source, entries, 1); err == nil {
		t.Fatalf("foreign image from %d included in %d", foreign, id)
	}
}

func TestReleaseNotesBundleJSONUsesCanonicalCategoryOrderAfterManualEdit(t *testing.T) {
	a := testApp(t)
	source := releaseNoteSource{ProjectID: projectID, SprintID: 1, VersionName: "V2", ReleaseDate: "2026-09-10T08:00:00Z", Requirements: []releaseNoteRequirement{
		{ID: 1, Code: "REQ-1", Title: "接口能力"},
		{ID: 2, Code: "REQ-2", Title: "模型能力"},
	}}
	entries := releaseContentEntries(source)
	entries[0].Category = "API接口"
	entries[1].Category = "大模型类型"
	payload, err := buildReleaseNotesBundle(context.Background(), a.db, source, entries, 4)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Entries []releaseNoteEntry `json:"entries"`
	}
	for _, file := range reader.File {
		if file.Name != "upgrade-notes.json" {
			continue
		}
		input, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		decodeErr := json.NewDecoder(input).Decode(&document)
		input.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
	}
	if len(document.Entries) != 2 || document.Entries[0].RequirementIDs[0] != 2 || document.Entries[0].Category != "大模型类型" || document.Entries[1].Category != "API接口" {
		t.Fatalf("portable JSON did not preserve the seven-category order: %#v", document.Entries)
	}
}
