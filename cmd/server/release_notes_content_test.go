package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func releaseContentSprint(t *testing.T, a *App, name string) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,status,created_at,updated_at)VALUES(?,?,'REL',?,'2026-08-01','2026-08-08','已完成','2026-09-10T08:00:00Z','2026-09-10T08:00:00Z')`, tenantID, projectID, name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func releaseContentRequirement(t *testing.T, a *App, sprint, status, kind, category string) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,type,category,sprint,status,description,acceptance,created_at,updated_at)VALUES(?,?,'REL-REQ','支持客户筛选',?,?,?,?,'新增客户负责人筛选。','筛选后保留分页条件。','2026-09-10T08:00:00Z','2026-09-10T08:00:00Z')`, tenantID, projectID, kind, category, sprint, status)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func releaseContentImage(t *testing.T, a *App, id int64, name, category string, content []byte) int64 {
	t.Helper()
	result, err := a.db.Exec(`INSERT INTO requirement_attachments(tenant_id,project_id,requirement_id,name,size_bytes,content_type,sha256,content,created_by,created_at,category)VALUES(?,?,?,?,?,'image/png','untrusted-db-digest',?,'u_admin','2026-09-10T08:00:00Z',?)`, tenantID, projectID, id, name, len(content), content, category)
	if err != nil {
		t.Fatal(err)
	}
	aid, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return aid
}
func releaseContentPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{50, 80, 230, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func readReleaseContent(t *testing.T, a *App, id int64) (releaseNoteSource, string) {
	t.Helper()
	tx, err := a.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	source, hash, err := a.releaseNotesSource(context.Background(), tx, projectID, id)
	if err != nil {
		t.Fatal(err)
	}
	return source, hash
}
func releaseContentEntries(source releaseNoteSource) []releaseNoteEntry {
	out := []releaseNoteEntry{}
	for _, item := range source.Requirements {
		out = append(out, releaseNoteEntry{Category: "其他", Title: item.Title, Description: "新增客户负责人筛选，筛选后保留分页条件。", RequirementIDs: []int64{item.ID}, ImageIDs: []int64{}})
	}
	return out
}

func TestReleaseNotesContentOnlyCompletedNonDefectsAndRealOwnedImages(t *testing.T) {
	a := testApp(t)
	sprint := releaseContentSprint(t, a, "Release content")
	wanted := releaseContentRequirement(t, a, "Release content", "已完成", "产品需求", "Debug工具")
	for _, fixture := range [][3]string{{"已上线", "Bug", "未分类"}, {"已完成", "产品需求", "缺陷修复"}, {"已完成", "产品需求", "Bug修复"}, {"已完成", "产品需求", "CRM Defects"}, {"已完成", "bugfix", "维护"}, {"已完成", "产品需求", "defectfixes"}, {"规划中", "产品需求", "未分类"}, {"已取消", "产品需求", "未分类"}, {"待上线", "产品需求", "未分类"}} {
		releaseContentRequirement(t, a, "Release content", fixture[0], fixture[1], fixture[2])
	}
	foreign := releaseContentRequirement(t, a, "Release content", "已完成", "产品需求", "未分类")
	bulkFixtureExec(t, a, `UPDATE requirements SET project_id=? WHERE id=?`, insightProjectID, foreign)
	outside := releaseContentRequirement(t, a, "Another iteration", "已完成", "产品需求", "未分类")
	good := releaseContentImage(t, a, wanted, "功能筛选.png", "other", releaseContentPNG(t))
	releaseContentImage(t, a, wanted, "错误复现.png", "auto", releaseContentPNG(t))
	releaseContentImage(t, a, wanted, "bug.png", "bug", releaseContentPNG(t))
	releaseContentImage(t, a, wanted, "fake.png", "other", []byte(`<svg onload="alert(1)"></svg>`))
	releaseContentImage(t, a, wanted, "truncated.png", "other", releaseContentPNG(t)[:32])
	releaseContentImage(t, a, outside, "outside.png", "other", releaseContentPNG(t))
	bulkFixtureExec(t, a, `INSERT INTO defects(tenant_id,project_id,code,title,sprint,status,created_at,updated_at)VALUES(?,?,'REL-BUG','DO NOT INCLUDE DEFECT BODY','Release content','已关闭','now','now')`, tenantID, projectID)
	bulkFixtureExec(t, a, `INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'sprint',?,'actor','completed','not source content','2026-09-09T18:00:00Z')`, tenantID, projectID, sprint)
	source, hash := readReleaseContent(t, a, sprint)
	if len(source.Requirements) != 1 || source.Requirements[0].ID != wanted || source.ReleaseDate != "2026-09-09T18:00:00Z" || len(hash) != 64 {
		t.Fatal("incorrect completed source")
	}
	images := source.Requirements[0].Images
	if len(images) != 1 || images[0].ID != good || images[0].SHA256 == "untrusted-db-digest" || images[0].ContentType != "image/png" || images[0].URL != fmt.Sprintf("/api/requirements/%d/attachments/%d", wanted, good) {
		t.Fatal("unsafe or missing owned image")
	}
	if strings.Contains(jsonText(source), "DO NOT INCLUDE") || strings.Contains(jsonText(source), "not source content") {
		t.Fatal("out-of-scope source disclosure")
	}
	_, same := readReleaseContent(t, a, sprint)
	if hash != same {
		t.Fatal("nondeterministic source fingerprint")
	}
	bulkFixtureExec(t, a, `UPDATE requirements SET description='changed source' WHERE id=?`, wanted)
	_, changed := readReleaseContent(t, a, sprint)
	if hash == changed {
		t.Fatal("source text not bound to fingerprint")
	}
	bulkFixtureExec(t, a, `UPDATE requirement_attachments SET category='bug' WHERE id=?`, good)
	next, changedAgain := readReleaseContent(t, a, sprint)
	if changed == changedAgain || len(next.Requirements[0].Images) != 0 {
		t.Fatal("image eligibility not bound to source fingerprint")
	}
}

func TestReleaseNotesContentSnapshotAndAmbiguousLegacySprintAliases(t *testing.T) {
	a := fileSQLiteTestApp(t)
	sprint := releaseContentSprint(t, a, "Release Alpha")
	id := releaseContentRequirement(t, a, "Release Alpha", "已完成", "产品需求", "未分类")
	short := releaseContentRequirement(t, a, "Release", "已完成", "产品需求", "未分类")
	source, _ := readReleaseContent(t, a, sprint)
	if len(source.Requirements) != 2 {
		t.Fatal("safe legacy alias dropped")
	}
	releaseContentSprint(t, a, "Release Beta")
	source, _ = readReleaseContent(t, a, sprint)
	if len(source.Requirements) != 1 || source.Requirements[0].ID == short {
		t.Fatal("ambiguous alias included")
	}
	tx, err := a.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, hash, err := a.releaseNotesSource(context.Background(), tx, projectID, sprint)
	if err != nil {
		t.Fatal(err)
	}
	bulkFixtureExec(t, a, `UPDATE requirements SET description='concurrently changed' WHERE id=?`, id)
	after, again, err := a.releaseNotesSource(context.Background(), tx, projectID, sprint)
	if err != nil || hash != again || before.Requirements[0].Description != after.Requirements[0].Description {
		t.Fatal("caller snapshot was not preserved", err)
	}
	tx.Rollback()
	_, fresh := readReleaseContent(t, a, sprint)
	if fresh == hash {
		t.Fatal("fresh source missed committed change")
	}
}

func TestReleaseNotesContentFailsExplicitlyWithoutPartialSource(t *testing.T) {
	for _, scenario := range []string{"empty", "not-completed", "too-many", "too-large", "too-many-images", "bad-time"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			sprint := releaseContentSprint(t, a, "Limits content")
			id := releaseContentRequirement(t, a, "Limits content", "已完成", "产品需求", "未分类")
			switch scenario {
			case "empty":
				bulkFixtureExec(t, a, `UPDATE requirements SET status='已取消' WHERE id=?`, id)
			case "not-completed":
				bulkFixtureExec(t, a, `UPDATE sprints SET status='进行中' WHERE id=?`, sprint)
			case "bad-time":
				bulkFixtureExec(t, a, `UPDATE sprints SET updated_at='not-a-date' WHERE id=?`, sprint)
			case "too-large":
				bulkFixtureExec(t, a, `UPDATE requirements SET description=? WHERE id=?`, strings.Repeat("x", releaseNotesMaxSourceBytes+1), id)
			case "too-many":
				for i := 0; i < releaseNotesMaxRequirements; i++ {
					releaseContentRequirement(t, a, "Limits content", "已完成", "产品需求", "未分类")
				}
			case "too-many-images":
				content := releaseContentPNG(t)
				for i := 0; i < 257; i++ {
					releaseContentImage(t, a, id, fmt.Sprintf("screen-%d.png", i), "other", content)
				}
			}
			_, hash, err := a.releaseNotesSource(context.Background(), a.db, projectID, sprint)
			var typed *organizationError
			if !errors.As(err, &typed) || hash != "" {
				t.Fatal("partial or invalid source silently accepted", err)
			}
			if (scenario == "too-large" || scenario == "too-many" || scenario == "too-many-images") && typed.Code != "release_notes_source_too_large" {
				t.Fatal("source limit not explicit", typed.Code)
			}
		})
	}
}

func TestReleaseNotesEntriesCoverageImageOwnershipCaptionsAndFixedRenderer(t *testing.T) {
	source := releaseNoteSource{VersionName: "V2 <script>", ReleaseDate: "2026-09-10T08:00:00Z", Requirements: []releaseNoteRequirement{{ID: 1, Code: "REQ-1", Title: "增加筛选", Images: []releaseNoteImage{{ID: 11, Name: "真实图.png", URL: "https://untrusted.invalid/ignored"}}}, {ID: 2, Code: "REQ-2", Title: "增加分组", Images: []releaseNoteImage{{ID: 22, Name: "另一需求.png"}}}}}
	entries := releaseContentEntries(source)
	entries[0].Category = "大模型类型"
	entries[0].ImageIDs = []int64{11}
	entries[0].ImageCaptions = map[string]string{"11": "负责人 *筛选* 结果"}
	if err := validateReleaseNotesEntries(source, entries); err != nil {
		t.Fatal(err)
	}
	markdown := renderReleaseNotesMarkdown(source, entries)
	if strings.Contains(markdown, "<script>") || strings.Contains(markdown, "untrusted.invalid") || !strings.Contains(markdown, "/api/requirements/1/attachments/11") || !strings.Contains(markdown, "负责人 \\*筛选\\* 结果") || !strings.Contains(markdown, "待补图") {
		t.Fatal("renderer escaped structure or image boundary")
	}
	previous := -1
	for _, category := range releaseNoteCategories {
		position := strings.Index(markdown, "\n## "+category+"\n")
		if position <= previous {
			t.Fatal("categories missing/out-of-order")
		}
		previous = position
	}
	for _, scenario := range []string{"missing", "duplicate", "unknown", "cross-image", "duplicate-image", "null-images", "html", "remote-link", "unknown-category", "unselected-caption", "oversized-caption"} {
		t.Run(scenario, func(t *testing.T) {
			copy := releaseContentEntries(source)
			switch scenario {
			case "missing":
				copy = copy[:1]
			case "duplicate":
				copy[1].RequirementIDs = []int64{1}
			case "unknown":
				copy[1].RequirementIDs = []int64{99}
			case "cross-image":
				copy[0].ImageIDs = []int64{22}
			case "duplicate-image":
				copy[0].ImageIDs = []int64{11, 11}
			case "null-images":
				copy[0].ImageIDs = nil
			case "html":
				copy[0].Description = "<img src=x>"
			case "remote-link":
				copy[0].Description = "图片 https://untrusted.invalid/a.png"
			case "unknown-category":
				copy[0].Category = "自定义"
			case "unselected-caption":
				copy[0].ImageCaptions = map[string]string{"11": "caption"}
			case "oversized-caption":
				copy[0].ImageIDs = []int64{11}
				copy[0].ImageCaptions = map[string]string{"11": strings.Repeat("图", 501)}
			}
			if validateReleaseNotesEntries(source, copy) == nil || renderReleaseNotesMarkdown(source, copy) != "" {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
}
