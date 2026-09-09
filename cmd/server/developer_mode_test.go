package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestDeveloperSeedMigrationUsesRandomCredentialsAndPreservesExistingHashes(t *testing.T) {
	password := strings.Repeat("synthetic", 5)
	t.Setenv("DEVFLOW_DEVELOPER_MODE", "isolated")
	t.Setenv("DEVFLOW_DEVELOPER_PASSWORD", password)
	t.Setenv("DEVFLOW_ADDR", "127.0.0.1:8080")
	a := testApp(t)
	if response, cookie := loginRequest(a, "linxia@devflow.local", password); response.Code != http.StatusOK || cookie == nil {
		t.Fatalf("random developer administrator could not login: %d", response.Code)
	}
	for _, email := range []string{"linxia@devflow.local", "zhouyu@devflow.local"} {
		if response, _ := loginRequest(a, email, seedPassword); response.Code != http.StatusUnauthorized {
			t.Fatal("legacy shared demo password was accepted in the isolated database")
		}
	}
	t.Setenv("DEVFLOW_DEVELOPER_PASSWORD", strings.Repeat("different", 5))
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	if response, _ := loginRequest(a, "linxia@devflow.local", password); response.Code != http.StatusOK {
		t.Fatal("repeated migration changed an existing credential")
	}
}

func TestDeveloperSeedCredentialsAreOptInAndIndependent(t *testing.T) {
	t.Setenv("DEVFLOW_DEVELOPER_MODE", "")
	t.Setenv("DEVFLOW_DEVELOPER_PASSWORD", "")
	legacy, err := developerSeedPassword("u_admin")
	if err != nil || legacy != seedPassword {
		t.Fatal("ordinary seed behavior unexpectedly changed")
	}
	t.Setenv("DEVFLOW_DEVELOPER_MODE", "isolated")
	t.Setenv("DEVFLOW_DEVELOPER_PASSWORD", strings.Repeat("synthetic", 5))
	t.Setenv("DEVFLOW_ADDR", "127.0.0.1:8080")
	admin, err := developerSeedPassword("u_admin")
	if err != nil || admin != strings.Repeat("synthetic", 5) {
		t.Fatal("developer administrator did not receive the runtime credential")
	}
	seen := map[string]bool{admin: true, seedPassword: true}
	for i := 0; i < 8; i++ {
		password, err := developerSeedPassword("u_member")
		if err != nil || len(password) < 32 || seen[password] {
			t.Fatal("developer member credentials must be independent and unpredictable")
		}
		seen[password] = true
	}
}

func TestDeveloperSeedRejectsIncompleteOrPublicConfiguration(t *testing.T) {
	for _, tc := range []struct{ mode, password, address string }{
		{"isolated", "", "127.0.0.1:8080"},
		{"", strings.Repeat("x", 40), "127.0.0.1:8080"},
		{"isolated", "short", "127.0.0.1:8080"},
		{"isolated", strings.Repeat("x", 73), "127.0.0.1:8080"},
		{"isolated", strings.Repeat("x", 40), ":8080"},
		{"unknown", strings.Repeat("x", 40), "127.0.0.1:8080"},
	} {
		t.Setenv("DEVFLOW_DEVELOPER_MODE", tc.mode)
		t.Setenv("DEVFLOW_DEVELOPER_PASSWORD", tc.password)
		t.Setenv("DEVFLOW_ADDR", tc.address)
		if password, err := developerSeedPassword("u_admin"); err == nil || password != "" {
			t.Fatal("invalid developer configuration was accepted")
		}
	}
}
