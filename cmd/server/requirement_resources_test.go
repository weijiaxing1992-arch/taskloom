package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
)

func resourceUpload(t *testing.T, a *App, id int64, user, project, name string, content []byte, extra bool, category ...string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	h.Set("Content-Type", "text/html") // Claims from clients must not control download execution.
	part, err := writer.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if extra {
		if err = writer.WriteField("extra", "ignored?"); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/requirements/%d/attachments", id)
	if len(category) > 0 {
		path += "?category=" + url.QueryEscape(category[0])
	}
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-DevFlow-Project", project)
	cookie, err := a.issueSession(httptest.NewRecorder(), r, user)
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	withJSON(a.scopedAPI()).ServeHTTP(w, r)
	return w
}

func TestRequirementAttachmentsPersistenceDownloadAndScope(t *testing.T) {
	a := fileSQLiteTestApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	data := append([]byte("<script>alert('never execute')</script>\n"), bytes.Repeat([]byte("content"), 400000)...)
	w := resourceUpload(t, a, x.ID, "u_admin", projectID, "需求方案.html", data, false)
	if w.Code != 201 {
		t.Fatalf("upload >2MiB: %d %s", w.Code, w.Body.String())
	}
	meta := jsonMap(t, w)
	id := int64(meta["id"].(float64))
	path := meta["downloadUrl"].(string)
	digest := sha256.Sum256(data)
	if meta["sha256"] != hex.EncodeToString(digest[:]) || int(meta["size"].(float64)) != len(data) {
		t.Fatal("incorrect stored metadata")
	}
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	typ, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) || err != nil || typ != "attachment" || params["filename"] != "需求方案.html" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("unsafe/incorrect download %d %v", w.Code, w.Header())
	}
	for _, badPath := range []string{fmt.Sprintf("/api/requirements/1/attachments/%d", id), fmt.Sprintf("/api/requirements/%d/attachments/%d", x.ID, id+10000)} {
		w = apiRequest(a, "GET", badPath, "u_admin", projectID, "")
		if w.Code != 404 {
			t.Fatalf("wrong requirement attachment access: %d", w.Code)
		}
	}
	w = apiRequest(a, "GET", path, "u_admin", insightProjectID, "")
	if w.Code != 404 {
		t.Fatalf("foreign scope %d", w.Code)
	}
	w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/attachments", x.ID), "u_admin", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	w = resourceUpload(t, a, x.ID, "u_viewer", projectID, "blocked.txt", []byte("x"), false)
	if w.Code != 403 {
		t.Fatalf("viewer upload %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "DELETE", path, "u_viewer", projectID, "")
	if w.Code != 403 {
		t.Fatalf("viewer delete %d", w.Code)
	}
	w = apiRequest(a, "DELETE", path, "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	if w.Code != 404 {
		t.Fatalf("deleted file still readable %d", w.Code)
	}
}

func TestRequirementAttachmentLimitsAndUnsafeNames(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	for _, name := range []string{"../private.txt", "/tmp/private.txt", `C:\private.txt`, `folder\file.txt`, "..", "a\u202Egnp.exe", "a\n.txt", " ", strings.Repeat("文", 256)} {
		w := resourceUpload(t, a, x.ID, "u_admin", projectID, name, []byte("x"), false)
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("name %q accepted: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := resourceUpload(t, a, x.ID, "u_admin", projectID, "big.bin", make([]byte, requirementAttachmentMaxSize+1), false)
	if w.Code != 413 {
		t.Fatalf("oversized upload: %d %s", w.Code, w.Body.String())
	}
	w = resourceUpload(t, a, x.ID, "u_admin", projectID, "extra.txt", []byte("x"), true)
	if w.Code != 400 {
		t.Fatalf("extra multipart field %d", w.Code)
	}
	w = resourceUpload(t, a, x.ID, "u_admin", projectID, "empty.txt", nil, false)
	if w.Code != 201 {
		t.Fatalf("empty file %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementFigmaLinksValidationAndScope(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	path := fmt.Sprintf("/api/requirements/%d/design-links", x.ID)
	for _, url := range []string{"http://figma.com/design/abc", "https://figma.com.evil.test/design/abc", "https://figma.com@evil.test/design/abc", "https://u:p@figma.com/design/abc", "https://figma.com:443/design/abc", "https://figma.com/community/abc", "https://figma.com/design/", "https://figma.com/file/../private", "javascript:alert(1)", "https://127.0.0.1/design/abc"} {
		w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]string{"url": url}))
		if w.Code != 422 {
			t.Fatalf("URL %q accepted %d %s", url, w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "POST", path, "u_admin", projectID, `{"title":"登录设计 User content","url":"https://www.figma.com/design/ABC/Login?node-id=1-2"}`)
	if w.Code != 201 {
		t.Fatalf("valid design %d %s", w.Code, w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	w = apiRequest(a, "POST", path, "u_admin", projectID, `{"url":"https://www.figma.com/design/ABC/Login?node-id=1-2"}`)
	if w.Code != 409 {
		t.Fatalf("duplicate %d", w.Code)
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("links list %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "DELETE", fmt.Sprintf("/api/requirements/1/design-links/%d", id), "u_admin", projectID, "")
	if w.Code != 404 {
		t.Fatalf("wrong parent delete %d", w.Code)
	}
	w = apiRequest(a, "DELETE", fmt.Sprintf("%s/%d", path, id), "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatalf("link delete %d", w.Code)
	}
}

func TestRequirementResourcesAuditFailureRollsBack(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	path := fmt.Sprintf("/api/requirements/%d/design-links", x.ID)
	w := resourceUpload(t, a, x.ID, "u_admin", projectID, "existing.txt", []byte("preserve"), false)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	download := jsonMap(t, w)["downloadUrl"].(string)
	w = apiRequest(a, "POST", path, "u_admin", projectID, `{"url":"https://figma.com/file/keep"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	link := fmt.Sprintf("%s/%d", path, int64(jsonMap(t, w)["id"].(float64)))
	if _, err := a.db.Exec(`CREATE TRIGGER reject_resource_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'disk unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	for _, w := range []*httptest.ResponseRecorder{
		resourceUpload(t, a, x.ID, "u_admin", projectID, "new.txt", []byte("rollback"), false),
		apiRequest(a, "DELETE", download, "u_admin", projectID, ""),
		apiRequest(a, "POST", path, "u_admin", projectID, `{"url":"https://figma.com/file/rollback"}`),
		apiRequest(a, "DELETE", link, "u_admin", projectID, ""),
	} {
		if w.Code != 503 || strings.Contains(w.Body.String(), "disk unavailable") {
			t.Fatalf("audit failure %d %s", w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"requirement_attachments", "requirement_design_links"} {
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE requirement_id=?`, x.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s rollback count=%d err=%v", table, n, err)
		}
	}
	for _, resource := range []struct{ table, route string }{{"requirement_attachments", "attachments"}, {"requirement_design_links", "design-links"}} {
		if _, err := a.db.Exec(`DROP TABLE ` + resource.table); err != nil {
			t.Fatal(err)
		}
		w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/%s", x.ID, resource.route), "u_admin", projectID, "")
		if w.Code != 503 {
			t.Fatalf("unavailable table should fail closed: %d %s", w.Code, w.Body.String())
		}
	}
}
