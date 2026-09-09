package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRichDocumentTransactionsRollBackEverySideEffect(t *testing.T) {
	for _, target := range []string{"requirement_attachments", "audit_logs", "activities", "user_notifications", "notification_outbox"} {
		for _, operation := range []string{"create", "patch", "comment"} {
			t.Run(target+"/"+operation, func(t *testing.T) {
				a := testApp(t)
				x := createPeopleRequirement(t, a, map[string]any{"description": "原来的正文", "tags": "保留标签"})
				before := map[string]int{}
				for _, table := range []string{"requirements", "comments", "requirement_attachments", "audit_logs", "activities", "user_notifications", "notification_outbox"} {
					before[table] = rtCount(t, a, table)
				}
				if _, err := a.db.Exec(`CREATE TRIGGER reject_rich_write BEFORE INSERT ON ` + target + ` BEGIN SELECT RAISE(ABORT,'private disk path secret'); END`); err != nil {
					t.Fatal(err)
				}
				doc := rtDoc(rtParagraph(rtMention("u_front", peopleName(t, a, "u_front"))), rtPending("image", "rollback.png", rtPNG(t)))
				method, path, body := "POST", "/api/requirements", map[string]any{"title": "未保存的新需求", "descriptionDoc": doc}
				if operation == "patch" {
					method, path, body = "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), map[string]any{"descriptionDoc": doc, "tags": "不能保存"}
				}
				if operation == "comment" {
					path, body = fmt.Sprintf("/api/requirements/%d/comments", x.ID), map[string]any{"contentDoc": doc}
				}
				w := apiRequest(a, method, path, "u_admin", projectID, jsonText(body))
				if w.Code < 500 || strings.Contains(w.Body.String(), "private disk") || strings.Contains(w.Body.String(), "SQL") {
					t.Fatalf("must fail safely: %d %s", w.Code, w.Body.String())
				}
				for table, count := range before {
					if after := rtCount(t, a, table); after != count {
						t.Errorf("%s leaked transaction rows: before=%d after=%d", table, count, after)
					}
				}
				got, err := a.get(x.ID)
				if err != nil || got.Description != x.Description || !richIsNull(got.DescriptionDoc) || got.Tags != x.Tags || len(got.DescriptionMentionUserIDs) != 0 {
					t.Fatalf("rollback changed requirement: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestRichAttachmentDeletionPreservesLiveReferences(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtPending("image", "referenced.png", rtPNG(t)))})
	id := rtParse(t, x.DescriptionDoc).files[0].id
	path := fmt.Sprintf("/api/requirements/%d/attachments/%d", x.ID, id)
	w := languageRequest(t, a, "DELETE", path, "u_admin", projectID, "en-US", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Remove its references first") {
		t.Fatalf("deleted active description image: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, jsonText(map[string]any{"contentDoc": rtDoc(rtExisting("image", "referenced.png", id))}))
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	x = patchPeopleRequirement(t, a, x.ID, map[string]any{"descriptionDoc": rtDoc(rtParagraph(rtText("正文不再引用")))})
	w = apiRequest(a, "DELETE", path, "u_admin", projectID, "")
	if w.Code != 409 {
		t.Fatalf("deleted comment image: %d %s", w.Code, w.Body.String())
	}
	if w = apiRequest(a, "GET", path, "u_viewer", projectID, ""); w.Code != 200 {
		t.Fatal("protected media unreadable")
	}
	other := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtPending("image", "removable.png", rtPNG(t)))})
	otherID := rtParse(t, other.DescriptionDoc).files[0].id
	patchPeopleRequirement(t, a, other.ID, map[string]any{"descriptionDoc": nil, "description": "已去掉图片"})
	w = apiRequest(a, "DELETE", fmt.Sprintf("/api/requirements/%d/attachments/%d", other.ID, otherID), "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatalf("unreferenced image must be removable: %d %s", w.Code, w.Body.String())
	}
}

func TestRichDocumentLimits(t *testing.T) {
	if _, err := parseRichDocument(richRaw(rtDoc(rtParagraph(rtText(strings.Repeat("中", richMaxText)))))); err != nil {
		t.Fatalf("text boundary rejected: %v", err)
	}
	if _, err := parseRichDocument(richRaw(rtDoc(rtParagraph(rtText(strings.Repeat("中", richMaxText+1)))))); err == nil {
		t.Fatal("text limit ignored")
	}
	nodes := make([]any, richMaxNodes-1)
	for i := range nodes {
		nodes[i] = rtParagraph()
	}
	if _, err := parseRichDocument(richRaw(rtDoc(nodes...))); err != nil {
		t.Fatalf("node boundary rejected: %v", err)
	}
	nodes = append(nodes, rtParagraph())
	if _, err := parseRichDocument(richRaw(rtDoc(nodes...))); err == nil {
		t.Fatal("node limit ignored")
	}
	var nested any = rtParagraph()
	for i := 1; i < richMaxDepth; i++ {
		nested = map[string]any{"type": "blockquote", "content": []any{nested}}
	}
	if _, err := parseRichDocument(richRaw(rtDoc(nested))); err != nil {
		t.Fatalf("depth boundary rejected: %v", err)
	}
	nested = map[string]any{"type": "blockquote", "content": []any{nested}}
	if _, err := parseRichDocument(richRaw(rtDoc(nested))); err == nil {
		t.Fatal("depth limit ignored")
	}
	data := bytes.Repeat([]byte{3}, int(requirementAttachmentMaxSize))
	if _, err := parseRichDocument(richRaw(rtDoc(rtPending("attachment", "max-one.bin", data), rtPending("attachment", "max-two.bin", data)))); err != nil {
		t.Fatalf("20MiB aggregate boundary rejected: %v", err)
	}
	if _, err := parseRichDocument(richRaw(rtDoc(rtPending("attachment", "a.bin", data), rtPending("attachment", "b.bin", data), rtPending("attachment", "over.bin", []byte{1})))); err == nil {
		t.Fatal("aggregate binary limit ignored")
	}
	if _, err := parseRichDocument(richRaw(rtDoc(rtPending("attachment", "oversize.bin", append(data, 3))))); err == nil {
		t.Fatal("individual binary limit ignored")
	}
}

func TestRichDocumentRequestLimitsAreScopedAndEnforced(t *testing.T) {
	for _, test := range []struct {
		method, path string
		rich         bool
	}{
		{"POST", "/api/requirements", true}, {"PATCH", "/api/requirements/12", true}, {"POST", "/api/requirements/12/comments", true},
		{"POST", "/api/sprints", false}, {"PATCH", "/api/preferences/display", false}, {"PATCH", "/api/requirements/12/comments", false}, {"POST", "/api/requirements/-1/comments", false}, {"POST", "/api/requirements/12/design-links", false}, {"GET", "/api/requirements/12", false},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, strings.NewReader("{}"))
			r.ContentLength = 3 << 20
			called := false
			w := httptest.NewRecorder()
			withJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) })).ServeHTTP(w, r)
			if called != test.rich || test.rich && w.Code != 200 || !test.rich && w.Code != 413 {
				t.Fatalf("limit exception: called=%v status=%d", called, w.Code)
			}
			r = httptest.NewRequest(test.method, test.path, strings.NewReader("{}"))
			r.ContentLength = richRequestMaxSize + 1
			w = httptest.NewRecorder()
			withJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("oversized body reached handler") })).ServeHTTP(w, r)
			if w.Code != 413 {
				t.Fatalf("request too large: %d", w.Code)
			}
		})
	}
	// Unknown Content-Length (chunked transfer) still stops at the middleware cap.
	r := httptest.NewRequest("POST", "/api/requirements", io.LimitReader(richZeroReader{}, richRequestMaxSize+1))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	withJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if n != richRequestMaxSize || err == nil {
			t.Fatalf("chunked limit ignored: %d %v", n, err)
		}
		w.WriteHeader(413)
	})).ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatal("chunked oversized rich request was accepted")
	}
	// Exercise a real JSON API save above the old 2MiB cap with a valid attachment.
	a := fileSQLiteTestApp(t)
	body := jsonText(map[string]any{"title": "大附件 JSON 保存", "descriptionDoc": rtDoc(rtPending("attachment", "large.bin", bytes.Repeat([]byte("media"), 500000)))})
	w = languageRequest(t, a, "POST", "/api/requirements", "u_admin", projectID, "en-US", body)
	if w.Code != 201 {
		t.Fatalf("large rich API save: %d %s", w.Code, w.Body.String())
	}
}

type richZeroReader struct{}

func (richZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRichConcurrentSavesDoNotRepeatMentions(t *testing.T) {
	a := fileSQLiteTestApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"descriptionDoc": rtDoc(rtPending("image", "existing.png", rtPNG(t)))})
	id := rtParse(t, x.DescriptionDoc).files[0].id
	doc := rtDoc(rtParagraph(rtMention("u_front", peopleName(t, a, "u_front"))), rtExisting("image", "existing.png", id))
	body := jsonText(map[string]any{"descriptionDoc": doc})
	path := fmt.Sprintf("/api/requirements/%d", x.ID)
	const workers = 12
	wg := sync.WaitGroup{}
	results := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := apiRequest(a, "PATCH", path, "u_admin", projectID, body)
			if w.Code != 200 {
				results <- fmt.Sprintf("%d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		t.Error(result)
	}
	if count := peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front"); count != 1 {
		t.Fatalf("concurrent mention count %d", count)
	}
	if rtCount(t, a, "requirement_attachments") != 1 {
		t.Fatal("concurrent saves duplicated binary")
	}
	got, err := a.get(x.ID)
	if err != nil || !reflect.DeepEqual(got.DescriptionMentionUserIDs, []string{"u_front"}) {
		t.Fatalf("missing final rich state: %v %+v", err, got)
	}
}

func TestRichReadRejectsCorruptPendingBinary(t *testing.T) {
	pending := jsonText(rtDoc(rtPending("attachment", "secret.txt", []byte("private binary content"))))
	if _, err := readRichDocument(sql.NullString{String: pending, Valid: true}); err == nil {
		t.Fatal("read permitted persisted pending binary")
	}
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	if _, err := a.db.Exec(`UPDATE requirements SET description_doc_json=? WHERE id=?`, pending, x.ID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, "")
	if w.Code < 500 || strings.Contains(w.Body.String(), "private binary") || strings.Contains(w.Body.String(), "cHJpdmF0Z") {
		t.Fatalf("corrupt document leaked: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE requirements SET description_doc_json=NULL WHERE id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO comments(tenant_id,project_id,requirement_id,author,body,created_at,content_doc_json)VALUES(?,?,?,?,?,?,?)`, tenantID, projectID, x.ID, "author", "safe body", "2026-09-03", pending); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), `"data"`) {
		t.Fatalf("corrupt comment leaked: %d %s", w.Code, w.Body.String())
	}
}

func TestRichValidationIsLocalizedWithoutTranslatingBusinessContent(t *testing.T) {
	a := testApp(t)
	w := languageRequest(t, a, "POST", "/api/requirements", "u_admin", projectID, "en-US", jsonText(map[string]any{"title": "标题仍中文", "descriptionDoc": rtDoc(map[string]any{"type": "iframe"})}))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "rich text document") || w.Header().Get("Content-Language") != "en-US" {
		t.Fatalf("unlocalized rich validation: %d %s", w.Code, w.Body.String())
	}
	doc := rtDoc(rtParagraph(rtText("富文本格式无效")))
	w = languageRequest(t, a, "POST", "/api/requirements", "u_admin", projectID, "en-US", jsonText(map[string]any{"title": "标题仍中文", "descriptionDoc": doc}))
	var x Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil || w.Code != 201 || x.Title != "标题仍中文" || x.Description != "富文本格式无效" {
		t.Fatalf("translated user content: %d %s %v", w.Code, w.Body.String(), err)
	}
}
