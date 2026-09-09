package main

import (
	"net/http"
	"testing"
)

func TestCommunityBootstrap(t *testing.T) {
	t.Setenv("TASKLOOM_COMMUNITY", "1")
	a := testApp(t)
	w, c := loginRequest(a, "Admin", "123456")
	if w.Code != http.StatusOK || c == nil {
		t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	var required int
	var hash string
	a.db.QueryRow(`SELECT must_change_password,password_hash FROM users WHERE id='u_admin'`).Scan(&required, &hash)
	if required != 1 || !verifyPassword("123456", hash) {
		t.Fatal("bootstrap invalid")
	}
	if a.checkCommunityBind("0.0.0.0:8080") == nil || a.checkCommunityBind("127.0.0.1:8080") != nil {
		t.Fatal("unsafe binding")
	}
	changed, _ := a.encodePassword("Changed2026!")
	a.db.Exec(`UPDATE users SET password_hash=?,must_change_password=0 WHERE id='u_admin'`, changed)
	if e := a.migrate(); e != nil {
		t.Fatal(e)
	}
	a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin'`).Scan(&hash)
	if hash != changed {
		t.Fatal("restart reset credential")
	}
	if a.checkCommunityBind("0.0.0.0:8080") != nil {
		t.Fatal("changed account blocked")
	}
}
