package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportPDFScopedDownload(t *testing.T) {
	a := testApp(t)
	for _, kind := range []string{"requirement", "defect"} {
		doc, err := a.workPDF(kind, 1)
		if err != nil {
			t.Fatal(err)
		}
		if doc.code == "" {
			t.Fatalf("%s code omitted", kind)
		}
		if _, err = renderWorkPDF(doc, false); err != nil {
			t.Fatalf("render %s: %v", kind, err)
		}
		w := apiRequest(a, "GET", "/api/exports/"+kind+"/1.pdf", "u_admin", projectID, "")
		if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) || w.Header().Get("Content-Type") != "application/pdf" {
			t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatal("not a download")
		}
		if dir := os.Getenv("DEVFLOW_PDF_QA_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, kind+".pdf"), w.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
		w = apiRequest(a, "GET", "/api/exports/"+kind+"/1.pdf", "u_admin", insightProjectID, "")
		if w.Code != 404 {
			t.Fatalf("cross-project %s %d", kind, w.Code)
		}
		w = apiRequest(a, "POST", "/api/exports/"+kind+"/1.pdf", "u_admin", projectID, "{}")
		if w.Code != 405 {
			t.Fatalf("method %d", w.Code)
		}
	}
	for _, path := range []string{"/api/exports/requirement/no.pdf", "/api/exports/no/1.pdf", "/api/exports/requirement/999999.pdf"} {
		if w := apiRequest(a, "GET", path, "u_admin", projectID, ""); w.Code != 404 {
			t.Fatalf("invalid %s %d", path, w.Code)
		}
	}
}

func TestExportPDFLegacyDefectCodeAndClosedStore(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE defects SET code='' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	d, err := a.getDefect(1)
	if err != nil || d.Code != "BUG-0001" {
		t.Fatalf("missing legacy code: %+v %v", d, err)
	}
	doc, err := a.workPDF("defect", 1)
	if err != nil || doc.code != "BUG-0001" {
		t.Fatal("PDF code absent")
	}
	var stored string
	a.db.QueryRow(`SELECT code FROM defects WHERE id=1`).Scan(&stored)
	if stored != "" {
		t.Fatal("read mutated legacy database")
	}
	a.db.Close()
	w := httptest.NewRecorder()
	a.exportPDF(w, httptest.NewRequest("GET", "/api/exports/defect/1.pdf", nil))
	if w.Code != 503 {
		t.Fatal("database failure hidden")
	}
}

func pdfQAImage(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 960, 540))
	draw.Draw(im, im.Bounds(), image.NewUniform(color.RGBA{242, 246, 251, 255}), image.Point{}, draw.Src)
	draw.Draw(im, image.Rect(0, 0, 960, 66), image.NewUniform(color.RGBA{27, 58, 102, 255}), image.Point{}, draw.Src)
	for i := 0; i < 4; i++ {
		draw.Draw(im, image.Rect(36, 104+i*100, 922, 172+i*100), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
		draw.Draw(im, image.Rect(55, 120+i*100, 250+i*100, 155+i*100), image.NewUniform(color.RGBA{38, uint8(130 + i*20), 200, 255}), image.Point{}, draw.Src)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, im); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestExportPDFRichImagesPaginationAndScopedReferences(t *testing.T) {
	a := testApp(t)
	nodes := []any{rtParagraph(rtText(strings.Repeat("富文本分页：下面的截图必须完整显示，文字不能覆盖图片。\n", 30))), rtPending("image", "桌面测试截图.png", pdfQAImage(t)), rtParagraph(rtText("图片说明：蓝色标题栏、四行测试条目完整可见。")), rtPending("image", "动画表情.gif", rtGIF(t)), rtParagraph(rtText(strings.Repeat("图文混排的后续说明与验收，不能丢失下一节。\n", 34))), rtPending("image", "第二张截图.png", pdfQAImage(t)), rtParagraph(rtText("图文需求最终验收：图片与所有说明均可查看。"))}
	x := createPeopleRequirement(t, a, map[string]any{"title": "图文需求导出分页回归", "descriptionDoc": rtDoc(nodes...)})
	doc, err := a.workPDF("requirement", x.ID)
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, b := range doc.blocks {
		if len(b.image) > 0 {
			images++
		}
	}
	if images != 3 {
		t.Fatalf("missing images %d", images)
	}
	data, err := renderWorkPDF(doc, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("/Type /Page")) < 4 || !bytes.Contains(data, []byte("/Subtype /Image")) {
		t.Fatal("image pagination missing")
	}
	if dir := os.Getenv("DEVFLOW_PDF_QA_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "rich-images.pdf"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Even a damaged document cannot export another requirement's attachment.
	var attachmentID int64
	a.db.QueryRow(`SELECT id FROM requirement_attachments WHERE requirement_id=? LIMIT 1`, x.ID).Scan(&attachmentID)
	other := createPeopleRequirement(t, a, map[string]any{"title": "other"})
	raw := jsonText(rtDoc(rtExisting("image", "hidden.png", attachmentID)))
	if _, err := a.db.Exec(`UPDATE requirements SET description_doc_json=? WHERE id=?`, raw, other.ID); err != nil {
		t.Fatal(err)
	}
	doc, err = a.workPDF("requirement", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.blocks {
		if len(b.image) > 0 {
			t.Fatal("cross-requirement attachment exported")
		}
	}
	// An external URL cannot become a server-side image fetch.
	var malicious map[string]any
	json.Unmarshal([]byte(raw), &malicious)
	malicious["content"].([]any)[0].(map[string]any)["attrs"].(map[string]any)["src"] = "https://attacker.invalid/image.png"
	if _, err := a.db.Exec(`UPDATE requirements SET description_doc_json=? WHERE id=?`, jsonText(malicious), other.ID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", fmt.Sprintf("/api/exports/requirement/%d.pdf", other.ID), "u_admin", projectID, "")
	if w.Code != 503 {
		t.Fatalf("unsafe stored doc accepted %d", w.Code)
	}
}

func TestExportPDFResourceLimits(t *testing.T) {
	for _, doc := range []workPDF{{title: strings.Repeat("x", (1<<20)+1)}, {blocks: make([]pdfBlock, 5001)}, {blocks: []pdfBlock{{image: []byte(`<svg><script/></svg>`)}}}} {
		if _, err := renderWorkPDF(doc, false); err == nil {
			t.Fatal("unbounded/unsafe document accepted")
		}
	}
	imageData := rtPNG(t)
	doc := workPDF{}
	for i := 0; i < maxPDFImages+1; i++ {
		doc.blocks = append(doc.blocks, pdfBlock{image: imageData})
	}
	if _, err := renderWorkPDF(doc, false); err == nil {
		t.Fatal("image count unlimited")
	}
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtPending("image", "image.png", imageData))})
	if _, err := a.db.Exec(`UPDATE requirement_attachments SET content=zeroblob(?),size_bytes=? WHERE requirement_id=?`, 10<<20, 10<<20, x.ID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", fmt.Sprintf("/api/exports/requirement/%d.pdf", x.ID), "u_admin", projectID, "")
	if w.Code != 422 {
		t.Fatalf("oversized DB blob %d %s", w.Code, w.Body.String())
	}
	pdfExportSlots <- struct{}{}
	pdfExportSlots <- struct{}{}
	defer func() { <-pdfExportSlots; <-pdfExportSlots }()
	w = apiRequest(a, "GET", "/api/exports/defect/1.pdf", "u_admin", projectID, "")
	if w.Code != 503 {
		t.Fatal("unbounded parallel render")
	}
}
func TestExportPDFLongChinesePagination(t *testing.T) {
	doc := workPDF{code: "REQ-9000", title: "中文长需求导出：验证分页与英文 Mixed English", project: "星河示例企业", blocks: []pdfBlock{{title: "需求描述 / Description", text: strings.Repeat("中文分页验收：记录正确，不丢失长段落。Mixed English 0123456789\n", 150)}, {title: "最终验收 / Final acceptance", text: "最后一行：可以交付。"}}}
	data, err := renderWorkPDF(doc, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("/Type /Page")) < 3 {
		t.Fatal("expected multiple pages")
	}
	if dir := os.Getenv("DEVFLOW_PDF_QA_DIR"); dir != "" {
		if err = os.WriteFile(filepath.Join(dir, "pagination.pdf"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
