package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWatermarkPeerIP(t *testing.T) {
	for _, test := range []struct{ remote, want string }{
		{"127.0.0.1:19086", "127.0.0.1"}, {"198.51.100.42:443", "198.51.100.42"},
		{"[2001:db8::1]:5555", "2001:db8::1"}, {"::1", "::1"},
		{"[fe80::1%en0]:4567", "fe80::1"}, {"::ffff:192.0.2.1", "192.0.2.1"},
		{"forged.example:80", ""}, {"garbage<script>", ""}, {"", ""},
	} {
		if got := watermarkPeerIP(test.remote); got != test.want {
			t.Errorf("%q = %q, want %q", test.remote, got, test.want)
		}
	}
}

func TestWatermarkAuthenticationIsolationAndReadOnly(t *testing.T) {
	a := testApp(t)
	_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if cookie == nil {
		t.Fatal("fixture login failed")
	}
	request := func(method string, authenticated bool, expected string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/watermark?accountName=forged", nil)
		r.RemoteAddr = "[2001:db8::123]:4137"
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		r.Header.Set("X-Real-IP", "203.0.113.10")
		r.Header.Set("X-TaskLoom-User", "u_front")
		r.Header.Set("X-TaskLoom-Project", "deleted-project")
		r.Header.Set("X-TaskLoom-Expected-User", expected)
		if authenticated {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.scopedAPI().ServeHTTP(w, r)
		return w
	}
	if w := request("GET", false, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("POST", true, "u_admin"); w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", true, "u_front"); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	var before, after int
	if err := a.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := request("GET", true, "u_admin")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("identity response is cacheable")
	}
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["userId"] != "u_admin" || data["accountName"] != "林夏" || data["ipAddress"] != "2001:db8::123" || data["ipSource"] != "connection" {
		t.Fatal(data)
	}
	stamp, err := time.Parse(time.RFC3339, data["serverTime"].(string))
	if err != nil || time.Since(stamp) > 3*time.Second {
		t.Fatal("invalid server time", data)
	}
	if err := a.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("watermark read mutated database", before, after)
	}
	if _, err := a.db.Exec(`UPDATE auth_sessions SET revoked_at=?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if w = request(http.MethodGet, true, "u_admin"); w.Code != 401 {
		t.Fatal("revoked session still sees watermark", w.Code)
	}
}
