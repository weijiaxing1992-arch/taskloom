package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSPAEntryNeverReusesStaleHTML(t *testing.T) {
	dir := t.TempDir()
	const html = `<!doctype html><script src="/assets/new-hash.js"></script>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0600); err != nil {
		t.Fatal(err)
	}
	h := withJSON(spa(dir))
	for _, url := range []string{"/", "/index.html", "/requirements?req=123", "/requirements/123/edit", "/iterations", "/members"} {
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, url, nil)
			r.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			r.Header.Set("If-None-Match", "*")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s %s: stale HTML validator returned %d", method, url, w.Code)
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" || w.Header().Get("Expires") != "0" || w.Header().Get("Last-Modified") != "" {
				t.Fatalf("%s missing entry cache protection: %v", url, w.Header())
			}
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("%s HTML MIME wrong: %s", url, w.Header().Get("Content-Type"))
			}
			if method == "GET" && w.Body.String() != html {
				t.Fatalf("%s wrong entry body", url)
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatalf("HEAD %s has response body", url)
			}
			if r.Header.Get("If-None-Match") != "*" {
				t.Fatal("SPA modified incoming request headers")
			}
		}
	}
}

func TestSPAAssetsKeepMIMEAndMissingAssetsAreNotHTML(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ name, body string }{{"index.html", "<main>application entry</main>"}, {"assets/current.js", "console.log('current')"}, {"assets/current.css", "body{color:blue}"}, {"assets/logo.svg", "<svg xmlns='http://www.w3.org/2000/svg'/>"}} {
		if err := os.WriteFile(filepath.Join(dir, file.name), []byte(file.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := withJSON(spa(dir))
	for _, file := range []struct{ path, mime string }{{"/assets/current.js", "text/javascript"}, {"/assets/current.css", "text/css"}, {"/assets/logo.svg", "image/svg+xml"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", file.path, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), file.mime) || w.Header().Get("Cache-Control") != "" {
			t.Fatalf("asset %s policy/MIME changed %d %v", file.path, w.Code, w.Header())
		}
		modified := w.Header().Get("Last-Modified")
		if modified == "" {
			t.Fatalf("asset %s validators removed", file.path)
		}
		r := httptest.NewRequest("GET", file.path, nil)
		r.Header.Set("If-Modified-Since", modified)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 304 {
			t.Fatalf("asset %s conditional GET stopped working: %d", file.path, w.Code)
		}
	}
	for _, path := range []string{"/assets/old-hash.js", "/assets/old.css", "/assets/no-extension", "/assets/", "/missing.js", "/missing.css", "/favicon.ico", "/manifest.json", "/unknown.html"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 || strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || strings.Contains(w.Body.String(), "application entry") {
			t.Fatalf("missing asset %s masqueraded as HTML: %d %v %s", path, w.Code, w.Header(), w.Body.String())
		}
	}
}
