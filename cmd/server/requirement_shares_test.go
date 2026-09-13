package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func publicShareRequest(a *App, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func TestRequirementShareSnapshotRevocationAndPermissions(t *testing.T) {
	a := testApp(t)
	req := planningRequirement(t, a, `{"title":"对外分享标题","description":"原始正文","acceptance":"验收标准"}`)
	endpoint := fmt.Sprintf("/api/requirement-shares?requirementId=%d", req.ID)
	for _, tc := range []struct {
		user, body string
		status     int
	}{{"u_viewer", `{"confirmed":true}`, 403}, {"u_admin", `{"confirmed":false}`, 400}} {
		w := apiRequest(a, "POST", endpoint, tc.user, projectID, tc.body)
		if w.Code != tc.status {
			t.Fatalf("permission %d %s", w.Code, w.Body.String())
		}
	}
	create := apiRequest(a, "POST", endpoint, "u_admin", projectID, `{"confirmed":true}`)
	if create.Code != 201 {
		t.Fatal(create.Code, create.Body.String())
	}
	var share struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &share); err != nil {
		t.Fatal(err)
	}
	path := "/api/public/requirement-shares/" + strings.TrimPrefix(share.Path, "/share/requirements/")
	// Never store the bearer link in the database.
	var hash string
	if err := a.db.QueryRow(`SELECT token_hash FROM requirement_shares WHERE id=?`, share.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, hash) {
		t.Fatal("raw token persisted")
	}
	if _, err := a.db.Exec(`UPDATE requirements SET description='内部后续修改' WHERE id=?`, req.ID); err != nil {
		t.Fatal(err)
	}
	w := publicShareRequest(a, path)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "原始正文") || strings.Contains(w.Body.String(), "内部后续修改") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("public cache enabled")
	}
	body := jsonMap(t, w)
	if len(body) != 5 {
		t.Fatal("unexpected public fields", body)
	}
	// Another member cannot revoke the creator's link.
	apiRequest(a, "DELETE", endpoint+fmt.Sprintf("&shareId=%d", share.ID), "u_back", projectID, "")
	if publicShareRequest(a, path).Code != 200 {
		t.Fatal("foreign revoke succeeded")
	}
	revoked := apiRequest(a, "DELETE", endpoint+fmt.Sprintf("&shareId=%d", share.ID), "u_admin", projectID, "")
	if revoked.Code != 200 {
		t.Fatal(revoked.Body.String())
	}
	if publicShareRequest(a, path).Code != 404 {
		t.Fatal("revoked link still accessible")
	}
	if publicShareRequest(a, path+"x").Code != 404 {
		t.Fatal("invalid token accepted")
	}
}
func TestRequirementShareExpiredAndCreatorDisabled(t *testing.T) {
	a := testApp(t)
	req := planningRequirement(t, a, `{"title":"分享失效"}`)
	endpoint := fmt.Sprintf("/api/requirement-shares?requirementId=%d", req.ID)
	for _, reason := range []string{"expired", "disabled"} {
		w := apiRequest(a, "POST", endpoint, "u_admin", projectID, `{"confirmed":true}`)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		share := jsonMap(t, w)
		path := "/api/public/requirement-shares/" + strings.TrimPrefix(share["path"].(string), "/share/requirements/")
		var err error
		if reason == "expired" {
			_, err = a.db.Exec(`UPDATE requirement_shares SET expires_at='2000-01-01T00:00:00Z'`)
		} else {
			_, err = a.db.Exec(`UPDATE users SET active=0 WHERE id='u_admin'`)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := publicShareRequest(a, path); got.Code != 404 {
			t.Fatal(reason, got.Code, got.Body.String())
		}
	}
}
