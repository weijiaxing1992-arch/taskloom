package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	// 子测试名可能包含 #、?、换行等 URI 保留字符。直接拼到 SQLite
	// DSN 会让 query 参数或库名被截断，造成不同子测试复用错误连接甚至只读库。
	// 固定长度哈希既保留每个测试的隔离性，也不把测试名称当作 URI 语法解释。
	nameHash := sha256.Sum256([]byte(t.Name()))
	db, e := sql.Open("sqlite", fmt.Sprintf("file:test-%x?mode=memory&cache=shared", nameHash))
	if e != nil {
		t.Fatal(e)
	}
	a := &App{db: db, passwordCost: 4, signingKey: []byte("test-session-signing-key-at-least-32-bytes")}
	if e = a.migrate(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return a
}
func TestSeedAndTenantScopedList(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("GET", "/api/requirements", nil)
	w := httptest.NewRecorder()
	a.requirements(w, r)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("REQ-0001")) {
		t.Fatal("seed requirement missing")
	}
}
func TestCreateValidationAndActivity(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.create(w, httptest.NewRequest("POST", "/api/requirements", bytes.NewBufferString(`{"title":""}`)))
	if w.Code != 422 {
		t.Fatalf("expected 422, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	a.create(w, httptest.NewRequest("POST", "/api/requirements", bytes.NewBufferString(`{"title":"验证租户隔离","assignee":"周屿"}`)))
	if w.Code != 201 {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE tenant_id=? AND project_id=?`, tenantID, projectID).Scan(&n)
	if n != 1 {
		t.Fatalf("expected activity, got %d", n)
	}
}
func TestCannotDisableDemoAdmin(t *testing.T) {
	a := testApp(t)
	scoped := administrationSessionFixture(t, a, "u_admin")
	w := httptest.NewRecorder()
	scoped.members(w, httptest.NewRequest("PATCH", "/api/members", bytes.NewBufferString(`{"id":"u_admin","active":false}`)))
	if w.Code != 409 {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestStaticAssetsKeepBrowserMIMEType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=app></div>"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('ok')"), 0644); err != nil {
		t.Fatal(err)
	}
	h := withJSON(spa(dir))
	for _, tc := range []struct {
		path string
		want string
	}{{"/", "text/html"}, {"/app.js", "text/javascript"}, {"/requirements", "text/html"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: status %d", tc.path, w.Code)
		}
		if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.want) {
			t.Fatalf("%s: content type %q, want prefix %q", tc.path, got, tc.want)
		}
	}
}

func TestBrandingConsistency(t *testing.T) {
	a := testApp(t)
	var gotTenant, gotProject string
	if err := a.db.QueryRow(`SELECT name FROM tenants WHERE id=?`, tenantID).Scan(&gotTenant); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT name FROM projects WHERE id=? AND tenant_id=?`, projectID, tenantID).Scan(&gotProject); err != nil {
		t.Fatal(err)
	}
	if gotTenant != tenantName || gotProject != projectName {
		t.Fatalf("branding mismatch: %q / %q", gotTenant, gotProject)
	}
	w := httptest.NewRecorder()
	a.session(w, httptest.NewRequest("GET", "/api/session", nil))
	if !bytes.Contains(w.Body.Bytes(), []byte(tenantName)) || !bytes.Contains(w.Body.Bytes(), []byte(projectName)) {
		t.Fatalf("session branding mismatch: %s", w.Body.String())
	}
}
