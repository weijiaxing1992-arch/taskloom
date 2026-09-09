package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

func rtDoc(nodes ...any) map[string]any { return map[string]any{"type": "doc", "content": nodes} }
func rtParagraph(nodes ...any) map[string]any {
	return map[string]any{"type": "paragraph", "content": nodes}
}
func rtText(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
func rtMention(id, name string) map[string]any {
	return map[string]any{"type": "mention", "attrs": map[string]any{"id": id, "label": name}}
}
func rtPending(kind, name string, content []byte) map[string]any {
	return map[string]any{"type": kind, "attrs": map[string]any{"attachmentId": nil, "name": name, "data": base64.StdEncoding.EncodeToString(content)}}
}
func rtExisting(kind, name string, id int64) map[string]any {
	return map[string]any{"type": kind, "attrs": map[string]any{"attachmentId": id, "name": name}}
}
func rtPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 4, 3))
	im.Set(1, 1, color.RGBA{R: 60, G: 130, B: 210, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func rtGIF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	palette := []color.Color{color.Black, color.White}
	im := image.NewPaletted(image.Rect(0, 0, 3, 2), palette)
	im.SetColorIndex(1, 1, 1)
	if err := gif.EncodeAll(&b, &gif.GIF{Image: []*image.Paletted{im, im}, Delay: []int{5, 5}}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func rtParse(t *testing.T, raw json.RawMessage) *richDocument {
	t.Helper()
	d, err := parseRichDocument(raw)
	if err != nil || d == nil {
		t.Fatalf("parse normalized document: %v: %s", err, raw)
	}
	return d
}
func rtCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRichRequirementCreatePersistsAtomicMediaMentionsAndParent(t *testing.T) {
	a := fileSQLiteTestApp(t)
	parent := createPeopleRequirement(t, a, map[string]any{"title": "父需求"})
	front, admin := peopleName(t, a, "u_front"), peopleName(t, a, "u_admin")
	pngBytes := rtPNG(t)
	fileBytes := []byte("<html><script>do not execute</script></html>")
	doc := rtDoc(rtParagraph(rtText("请"), rtMention("u_front", front), rtText("确认"), rtMention("u_admin", admin)), rtPending("image", "截图.png", pngBytes), rtPending("attachment", "方案.html", fileBytes))
	x := createPeopleRequirement(t, a, map[string]any{"title": "截图子需求", "parentId": parent.ID, "descriptionDoc": doc, "description": "客户端伪造正文", "descriptionMentionUserIds": []string{"u_back"}})
	if x.ParentID == nil || *x.ParentID != parent.ID || x.Description != "请 @"+front+" 确认 @"+admin+"\n截图.png\n方案.html" {
		t.Fatalf("parent/plain text mismatch: %+v", x)
	}
	if !reflect.DeepEqual(x.DescriptionMentionUserIDs, []string{"u_admin", "u_front"}) || x.DescriptionMentionNames["u_front"] != front {
		t.Fatalf("non-authoritative rich mention IDs: %+v", x)
	}
	d := rtParse(t, x.DescriptionDoc)
	if len(d.files) != 2 || d.files[0].pending || d.files[1].pending || bytes.Contains(x.DescriptionDoc, []byte(`"data"`)) {
		t.Fatalf("pending file leaked: %s", x.DescriptionDoc)
	}
	for index, expected := range [][]byte{pngBytes, fileBytes} {
		path := fmt.Sprintf("/api/requirements/%d/attachments/%d", x.ID, d.files[index].id)
		w := apiRequest(a, "GET", path, "u_viewer", projectID, "")
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), expected) || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatalf("download mismatch: %d %v", w.Code, w.Header())
		}
		if w = apiRequest(a, "GET", path, "u_admin", insightProjectID, ""); w.Code != 404 {
			t.Fatalf("cross-project media leaked: %d", w.Code)
		}
		if w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/attachments/%d", parent.ID, d.files[index].id), "u_admin", projectID, ""); w.Code != 404 {
			t.Fatalf("cross-parent media leaked: %d", w.Code)
		}
	}
	for _, id := range []string{"u_admin", "u_front"} {
		if count := peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", id); count != 1 {
			t.Fatalf("mention %s count %d", id, count)
		}
	}
	var notification, payload, audit string
	if err := a.db.QueryRow(`SELECT body FROM user_notifications WHERE subject_id=? AND event_type='requirement.description_mentioned' LIMIT 1`, x.ID).Scan(&notification); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT payload FROM notification_outbox WHERE event_type='requirement.description_mentioned' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT after_json FROM audit_logs WHERE object_id=? AND action='requirement.description_saved'`, fmt.Sprint(x.ID)).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	if notification != x.Description || strings.Contains(payload, encoded) || strings.Contains(audit, encoded) || strings.Contains(audit, `"data"`) {
		t.Fatal("binary content leaked into notification/audit")
	}
	for i := 0; i < 2; i++ {
		if err := a.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.get(x.ID)
	if err != nil || !bytes.Equal(got.DescriptionDoc, x.DescriptionDoc) || got.Description != x.Description || rtCount(t, a, "requirement_attachments") != 2 {
		t.Fatalf("restart migration changed rich content: %v", err)
	}
}

func TestRichRequirementLegacyCompatibilityAndMentionReentry(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	x := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtParagraph(rtMention("u_front", name)), rtPending("image", "image.png", rtPNG(t)))})
	original := append(json.RawMessage(nil), x.DescriptionDoc...)
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"title": "不应改变正文", "description": x.Description, "descriptionMentionUserIds": []string{}})
	if !bytes.Equal(original, x.DescriptionDoc) || !reflect.DeepEqual(x.DescriptionMentionUserIDs, []string{"u_front"}) {
		t.Fatalf("unchanged legacy save lost rich state: %+v", x)
	}
	for i := 0; i < 2; i++ {
		x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": json.RawMessage(original), "description": "ignored", "descriptionMentionUserIds": []string{"u_back"}})
	}
	if count := peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front"); count != 1 {
		t.Fatalf("unchanged rich save renotified %d times", count)
	}
	if rtCount(t, a, "requirement_attachments") != 1 {
		t.Fatal("existing media reference reuploaded")
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"description": "旧页面显式修改的纯文本"})
	if !richIsNull(x.DescriptionDoc) || len(x.DescriptionMentionUserIDs) != 0 || x.Description != "旧页面显式修改的纯文本" {
		t.Fatalf("legacy plain edit not authoritative: %+v", x)
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": json.RawMessage(original)})
	if count := peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front"); count != 2 {
		t.Fatalf("reentered mention did not notify: %d", count)
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": nil})
	if !richIsNull(x.DescriptionDoc) || !strings.Contains(x.Description, "@"+name) || len(x.DescriptionMentionUserIDs) != 1 {
		t.Fatalf("doc:null did not retain text fallback: %+v", x)
	}
}

func TestRichRequirementMembersNamesAndInactiveHistory(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	doc := rtDoc(rtParagraph(rtMention("u_front", name)))
	x := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": doc})
	if _, err := a.db.Exec(`UPDATE users SET name='改名成员' WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": doc})
	if x.Description != "@改名成员" || x.DescriptionMentionNames["u_front"] != "改名成员" {
		t.Fatalf("rename snapshot not canonical: %+v", x)
	}
	if count := peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front"); count != 1 {
		t.Fatal("renaming a member notified again")
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": x.DescriptionDoc})
	if x.Description != "@改名成员" {
		t.Fatal("inactive historical mention was lost")
	}
	for _, invalid := range []any{rtDoc(rtParagraph(rtMention("u_front", "改名成员"))), rtDoc(rtParagraph(rtMention("u_back", "不是成员姓名"))), rtDoc(rtParagraph(rtMention("not-a-member", "任意姓名")))} {
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(map[string]any{"title": "非法提及", "descriptionDoc": invalid}))
		if w.Code != 422 {
			t.Fatalf("invalid new mention accepted: %d %s", w.Code, w.Body.String())
		}
	}
	foreign := rtDoc(rtParagraph(rtMention("u_back", peopleName(t, a, "u_back"))))
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", insightProjectID, jsonText(map[string]any{"title": "跨项目提及", "descriptionDoc": foreign}))
	if w.Code != 422 {
		t.Fatalf("foreign project member accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRichCommentsImageGIFEmojiAndLegacyText(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	name, self := peopleName(t, a, "u_front"), peopleName(t, a, "u_admin")
	bodies := []map[string]any{
		{"contentDoc": rtDoc(rtPending("image", "动画表情.gif", rtGIF(t)))},
		{"contentDoc": rtDoc(rtParagraph(rtText("🎉👍")))},
		{"contentDoc": rtDoc(rtParagraph(rtMention("u_front", name), rtText("请检查"), rtMention("u_admin", self)), rtPending("image", "截图.png", rtPNG(t))), "body": "伪造正文", "mentionUserIds": []string{"u_back"}},
		{"body": "保留旧版纯文本评论 👏"},
	}
	for index, body := range bodies {
		w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(body))
		if w.Code != 201 {
			t.Fatalf("comment %d: %d %s", index, w.Code, w.Body.String())
		}
		var comment Comment
		if err := json.Unmarshal(w.Body.Bytes(), &comment); err != nil {
			t.Fatal(err)
		}
		if index < 3 && (richIsNull(comment.ContentDoc) || strings.Contains(string(comment.ContentDoc), `"data"`) || comment.Body == "伪造正文") {
			t.Fatalf("rich comment not canonical: %+v", comment)
		}
		if index == 0 && comment.Body != "动画表情.gif" {
			t.Fatalf("image-only comment invalid plain text: %s", comment.Body)
		}
		if index == 1 && comment.Body != "🎉👍" {
			t.Fatal("emoji comment changed")
		}
		if index == 3 && !richIsNull(comment.ContentDoc) {
			t.Fatal("legacy comment fabricated a rich document")
		}
	}
	if peopleNoticeCount(t, a, x.ID, "requirement.mentioned", "u_front") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.mentioned", "u_admin") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.mentioned", "u_back") != 0 {
		t.Fatal("comment mention routing wrong; rich-text self mention must enter inbox")
	}
	w := apiRequest(a, "GET", path, "u_viewer", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != len(bodies) || strings.Contains(w.Body.String(), `"data"`) {
		t.Fatalf("rich comments reload: %d %s", w.Code, w.Body.String())
	}
	for _, invalid := range []any{rtDoc(), rtDoc(rtParagraph()), rtDoc(map[string]any{"type": "horizontalRule"}), rtDoc(rtParagraph(rtMention("u_front", "伪造")))} {
		w = apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"contentDoc": invalid}))
		if w.Code != 422 {
			t.Fatalf("empty/invalid rich comment accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRichDocumentSafeSchemaAndLinks(t *testing.T) {
	valid := rtDoc(
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 3}, "content": []any{rtText("标题")}},
		rtParagraph(map[string]any{"type": "text", "text": "链接", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "https://example.org/path?q=1", "target": "_blank", "rel": "noopener noreferrer", "class": "external"}}}}, map[string]any{"type": "hardBreak"}, rtText("内容")),
		map[string]any{"type": "orderedList", "attrs": map[string]any{"start": 1}, "content": []any{map[string]any{"type": "listItem", "content": []any{rtParagraph(rtText("列表")), map[string]any{"type": "bulletList", "content": []any{map[string]any{"type": "listItem", "content": []any{rtParagraph(rtText("子项"))}}}}}}}},
		map[string]any{"type": "blockquote", "content": []any{rtParagraph(rtText("引用"))}},
		map[string]any{"type": "codeBlock", "attrs": map[string]any{"language": nil}, "content": []any{rtText("<script>not executed</script>")}},
		map[string]any{"type": "horizontalRule"},
	)
	d := rtParse(t, richRaw(valid))
	raw := richRaw(d.root)
	if bytes.Contains(raw, []byte(`"target"`)) || bytes.Contains(raw, []byte(`"rel"`)) || bytes.Contains(raw, []byte(`"class"`)) {
		t.Fatalf("display-only link attributes persisted: %s", raw)
	}
	for _, name := range []string{"bold", "italic", "underline", "strike", "code"} {
		rtParse(t, richRaw(rtDoc(rtParagraph(map[string]any{"type": "text", "text": "style", "marks": []any{map[string]any{"type": name}}}))))
	}
	for _, href := range []string{"https://example.com/", "http://example.com/?q=a%20b", "mailto:help@example.com?subject=Hello"} {
		if !safeRichLink(href) {
			t.Fatalf("valid link rejected: %q", href)
		}
	}
	for _, href := range []string{"javascript:alert(1)", "data:text/html,a", "file:///tmp/a", "//evil.test", "https://user:pass@example.com", "https://u@example.com", "https:\\evil.test", "http:evil.test", "https:///path", " mailto:a@b.test", "mailto://a@b.test", "https://examp\u200ble.com", "https://example.com/\n"} {
		if safeRichLink(href) {
			t.Fatalf("unsafe link accepted: %q", href)
		}
	}
	invalid := []any{
		map[string]any{"type": "doc", "html": "<script/>"},
		rtDoc(map[string]any{"type": "script"}),
		rtDoc(rtText("block required")),
		rtDoc(map[string]any{"type": "heading", "attrs": map[string]any{"level": 7}}),
		rtDoc(map[string]any{"type": "heading", "attrs": map[string]any{"level": 2, "onclick": "alert()"}}),
		rtDoc(map[string]any{"type": "paragraph", "attrs": map[string]any{"style": "color:red"}}),
		rtDoc(map[string]any{"type": "image", "attrs": map[string]any{"src": "https://evil.test/image.png", "name": "a.png", "attachmentId": 1}}),
		rtDoc(rtParagraph(map[string]any{"type": "image", "attrs": map[string]any{"name": "a.png", "attachmentId": 1}})),
		rtDoc(map[string]any{"type": "bulletList"}),
		rtDoc(map[string]any{"type": "orderedList", "attrs": map[string]any{"start": -1}, "content": []any{map[string]any{"type": "listItem", "content": []any{rtParagraph(rtText("x"))}}}}),
		rtDoc(map[string]any{"type": "codeBlock", "attrs": map[string]any{"language": "js onclick=evil"}}),
		rtDoc(rtParagraph(map[string]any{"type": "text", "text": "a", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "javascript:alert(1)"}}}})),
		rtDoc(rtParagraph(map[string]any{"type": "text", "text": "a", "marks": []any{map[string]any{"type": "bold"}, map[string]any{"type": "bold"}}})),
	}
	for index, input := range invalid {
		if _, err := parseRichDocument(richRaw(input)); err == nil {
			t.Fatalf("unsafe document %d accepted: %s", index, jsonText(input))
		}
	}
	for _, raw := range []string{`"<p>HTML is not JSON</p>"`, `{"type":"doc"} {"type":"doc"}`, `{"type":"doc","content":[null]}`} {
		if _, err := parseRichDocument(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed raw document accepted: %s", raw)
		}
	}
}

func TestRichImageValidationRejectsFakeTruncatedAndOversizedImages(t *testing.T) {
	pngBytes, gifBytes := rtPNG(t), rtGIF(t)
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{pngBytes, gifBytes, jpegBytes.Bytes()} {
		if err := validateRichImage(data); err != nil {
			t.Fatalf("valid image: %v", err)
		}
	}
	oversized := append([]byte(nil), gifBytes...)
	binary.LittleEndian.PutUint16(oversized[6:8], 12001)
	tooManyPixels := append([]byte(nil), gifBytes...)
	binary.LittleEndian.PutUint16(tooManyPixels[6:8], 10000)
	binary.LittleEndian.PutUint16(tooManyPixels[8:10], 10000)
	for index, data := range [][]byte{[]byte(`<svg onload="alert(1)"/>`), []byte("GIF89afake"), []byte("<html>image.png</html>"), pngBytes[:len(pngBytes)-12], gifBytes[:len(gifBytes)-2], jpegBytes.Bytes()[:len(jpegBytes.Bytes())/2], oversized, tooManyPixels} {
		if err := validateRichImage(data); err == nil {
			t.Fatalf("unsafe image %d accepted", index)
		}
	}
}

func TestRichAttachmentReferencesScopeAndImageType(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	y := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtPending("image", "other.png", rtPNG(t)))})
	foreignFile := rtParse(t, y.DescriptionDoc).files[0].id
	upload := resourceUpload(t, a, x.ID, "u_admin", projectID, "unsafe.svg", []byte(`<svg onload="alert(1)"/>`), false)
	if upload.Code != 201 {
		t.Fatal(upload.Body.String())
	}
	svgID := int64(jsonMap(t, upload)["id"].(float64))
	path := fmt.Sprintf("/api/requirements/%d", x.ID)
	for _, doc := range []any{rtDoc(rtExisting("image", "other.png", foreignFile)), rtDoc(rtExisting("image", "missing.png", 999999)), rtDoc(rtExisting("image", "unsafe.svg", svgID))} {
		w := apiRequest(a, "PATCH", path, "u_admin", projectID, jsonText(map[string]any{"descriptionDoc": doc}))
		if w.Code != 422 {
			t.Fatalf("unsafe existing image reference accepted: %d %s", w.Code, w.Body.String())
		}
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": rtDoc(rtExisting("attachment", "pretend.txt", svgID))})
	if x.Description != "unsafe.svg" || !strings.Contains(string(x.DescriptionDoc), "unsafe.svg") {
		t.Fatal("stored attachment filename must be canonical")
	}
	for _, node := range []any{rtPending("attachment", "../escape", []byte("no")), map[string]any{"type": "attachment", "attrs": map[string]any{"attachmentId": svgID, "name": "x", "data": "eA=="}}, map[string]any{"type": "attachment", "attrs": map[string]any{"name": "x", "data": "data:text/plain;base64,eA=="}}, map[string]any{"type": "attachment", "attrs": map[string]any{"name": "x", "data": "eA==\n"}}, map[string]any{"type": "attachment", "attrs": map[string]any{"name": "x", "data": nil}}} {
		if _, err := parseRichDocument(richRaw(rtDoc(node))); err == nil {
			t.Fatalf("unsafe attachment node accepted: %s", jsonText(node))
		}
	}
}
