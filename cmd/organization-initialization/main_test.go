package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, spec := range organizationTables {
		fields := strings.Split(spec.columns, ",")
		for i := range fields {
			fields[i] += " TEXT NOT NULL DEFAULT ''"
		}
		if spec.table == "users" {
			fields = append(fields, "active INTEGER DEFAULT 1", "password_hash TEXT DEFAULT ''", "operation_disabled INTEGER DEFAULT 0", "must_change_password INTEGER DEFAULT 0", "password_changed_at TEXT DEFAULT ''")
		}
		if _, err = db.Exec("CREATE TABLE " + spec.table + "(" + strings.Join(fields, ",") + ")"); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"INSERT INTO tenants(id,name) VALUES('tn_acme','Test')",
		"INSERT INTO users(id,tenant_id,name,email,password_hash) VALUES('admin','tn_acme','Admin','admin@example.test','old-secret'),('member','tn_acme','Member','member@example.test','old-secret'),('removed','tn_acme','Removed','removed@example.test','old-secret')",
		"INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES('tn_acme','admin','member','active'),('tn_acme','member','member','active'),('tn_acme','removed','member','removed')",
		"CREATE TABLE requirements(id INTEGER,description TEXT)",
		"INSERT INTO requirements VALUES(1,'private-business-content')",
		"CREATE TABLE auth_sessions(token TEXT)",
		"INSERT INTO auth_sessions VALUES('private-session')",
		"CREATE TABLE user_wecom_webhooks(secret TEXT)",
		"INSERT INTO user_wecom_webhooks VALUES('private-webhook')",
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestInitializePrivateOrganizationOnly(t *testing.T) {
	source := fixture(t)
	output := filepath.Join(t.TempDir(), "initial.db")
	if err := initialize(source, output, "tn_acme", "admin@example.test", "Test-only-2026"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(output)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credentials database must be private")
	}
	db, err := sql.Open("sqlite", output)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]int{"users": 2, "requirements": 0, "auth_sessions": 0, "user_wecom_webhooks": 0} {
		var count int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
	var hash string
	var role string
	if err = db.QueryRow("SELECT role FROM tenant_memberships WHERE user_id='admin'").Scan(&role); err != nil || role != "tenant_admin" {
		t.Fatal("initial administrator must use the application tenant_admin role")
	}
	var active, change int
	if err = db.QueryRow("SELECT password_hash,active,must_change_password FROM users WHERE id='admin'").Scan(&hash, &active, &change); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("Test-only-2026")) != nil || active != 1 || change != 1 {
		t.Fatal("admin initial credentials invalid")
	}
	if err = db.QueryRow("SELECT password_hash,active FROM users WHERE id='member'").Scan(&hash, &active); err != nil {
		t.Fatal(err)
	}
	if hash != "" || active != 0 {
		t.Fatal("other credentials must not be copied")
	}
	src, _ := sql.Open("sqlite", source)
	defer src.Close()
	if err = src.QueryRow("SELECT password_hash FROM users WHERE id='admin'").Scan(&hash); err != nil || hash != "old-secret" {
		t.Fatal("source changed")
	}
}

func TestInitializeRejectsOverwriteAndUnknownAdmin(t *testing.T) {
	source := fixture(t)
	output := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(output, []byte("do not overwrite"), 0600); err != nil {
		t.Fatal(err)
	}
	if initialize(source, output, "tn_acme", "admin@example.test", "Test-only-2026") == nil {
		t.Fatal("must refuse existing target")
	}
	before, _ := os.ReadFile(output)
	if string(before) != "do not overwrite" {
		t.Fatal("existing target changed")
	}
	if initialize(source, source, "tn_acme", "admin@example.test", "Test-only-2026") == nil {
		t.Fatal("must refuse source")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if initialize(source, missing, "tn_acme", "removed@example.test", "Test-only-2026") == nil {
		t.Fatal("must refuse removed admin")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("must validate before creating target")
	}
}
