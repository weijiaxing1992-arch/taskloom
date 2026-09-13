package main

import (
	"net/http"
	"strings"
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
	w = initialRequest(a, c, "GET", "/api/requirements", "", "u_admin")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "password_change_required") {
		t.Fatalf("initial password gate: %d %s", w.Code, w.Body.String())
	}
	if a.checkCommunityBind("0.0.0.0:8080") == nil || a.checkCommunityBind(":8080") == nil || a.checkCommunityBind("[::]:8080") == nil || a.checkCommunityBind("127.0.0.1:8080") != nil || a.checkCommunityBind("[::1]:8080") != nil {
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

func TestCommunityMembersHavePrivateInitialCredentials(t *testing.T) {
	t.Setenv("TASKLOOM_COMMUNITY", "1")
	t.Setenv("DEVFLOW_INITIAL_PASSWORD", "")
	a := testApp(t)
	rows, err := a.db.Query(`SELECT id,email,password_hash,must_change_password FROM users WHERE tenant_id=?`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, email, hash string
		var pending bool
		if err := rows.Scan(&id, &email, &hash, &pending); err != nil {
			t.Fatal(err)
		}
		count++
		if !strings.HasSuffix(email, "@example.com") || hash == "" || !pending {
			t.Fatalf("invalid community identity: %s", id)
		}
		if id != "u_admin" && (verifyPassword("123456", hash) || verifyPassword(seedPassword, hash)) {
			t.Fatalf("community member retained a public initial credential: %s", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 11 {
		t.Fatalf("community member count: %d", count)
	}
	if password, err := configuredInitialPassword(); err != nil || password != "" {
		t.Fatal("community must not configure a shared member password by default")
	}
}

func TestCommunityAdminAliasSharesLoginChallenge(t *testing.T) {
	t.Setenv("TASKLOOM_COMMUNITY", "1")
	a := testApp(t)
	const ip = "198.51.100.20:4000"
	challengeFixture(t, a, "Admin", ip)
	challenge := challengeFromResponse(t, guardedLogin(t, a, "admin@example.com", "123456", ip, nil))
	answer := fixedChallengeAnswer(t, a, challenge)
	w := guardedLogin(t, a, "  ADMIN  ", "123456", ip, map[string]any{"challengeId": challenge.ID, "challengeAnswer": answer})
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 {
		t.Fatalf("alias challenge login: %d %s", w.Code, w.Body.String())
	}
}
