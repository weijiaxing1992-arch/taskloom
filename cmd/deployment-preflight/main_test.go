package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "original.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE tenants(id TEXT PRIMARY KEY);INSERT INTO tenants VALUES('tn_acme');
CREATE TABLE projects(id TEXT PRIMARY KEY,tenant_id TEXT);INSERT INTO projects VALUES('prj_orbit','tn_acme');
CREATE TABLE users(id TEXT PRIMARY KEY,tenant_id TEXT,email TEXT,active INTEGER,password_hash TEXT,password_changed_at TEXT);
CREATE TABLE tenant_memberships(tenant_id TEXT,user_id TEXT,role TEXT,status TEXT);
CREATE TABLE memberships(user_id TEXT,role TEXT);CREATE TABLE project_members(user_id TEXT,role TEXT);
CREATE TABLE requirements(id INTEGER PRIMARY KEY,title TEXT);INSERT INTO requirements VALUES(42,'不能改动的真实需求');
CREATE TABLE sprints(id INTEGER PRIMARY KEY,name TEXT);INSERT INTO sprints VALUES(22,'123');
CREATE TABLE defects(id INTEGER PRIMARY KEY);CREATE TABLE test_cases(id INTEGER PRIMARY KEY);
CREATE TABLE auth_sessions(tenant_id TEXT,revoked_at TEXT);INSERT INTO auth_sessions VALUES('tn_acme',NULL);
CREATE TABLE auth_impersonations(tenant_id TEXT,ended_at TEXT);INSERT INTO auth_impersonations VALUES('tn_acme',NULL);
CREATE TABLE audit_logs(tenant_id TEXT,project_id TEXT,actor_id TEXT,object_type TEXT,object_id TEXT,action TEXT,before_json TEXT,after_json TEXT,created_at TEXT);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct {
		id, password, role string
		active             int
	}{{"u_admin", knownDevelopmentPassword, "tenant_admin", 1}, {"u_member", knownDevelopmentPassword, "member", 1}, {"u_real", "ExistingPrivate2026!", "member", 1}, {"u_disabled", knownDevelopmentPassword, "member", 0}} {
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO users VALUES(?,'tn_acme',?,?,?,'unchanged')`, u.id, u.id+"@example.test", u.active, string(hash)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO tenant_memberships VALUES('tn_acme',?,?,'active')`, u.id, u.role); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO memberships VALUES(?,'product');INSERT INTO project_members VALUES(?,'product')`, u.id, u.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(dir, "deployment.db")
	if err := os.WriteFile(copy, content, 0600); err != nil {
		t.Fatal(err)
	}
	return source, copy
}
func provisionOptions(t *testing.T, source, copy string) options {
	t.Helper()
	return options{db: copy, source: source, provision: true, offline: true, admin: "u_admin", credentials: filepath.Join(t.TempDir(), "admin.json")}
}

func TestReadOnlyAuditDetectsDefaultsWithoutWriting(t *testing.T) {
	_, copy := fixture(t)
	before, _ := os.ReadFile(copy)
	r, err := run(context.Background(), options{db: copy})
	if err != nil || r.Ready || r.ActiveAccounts != 3 || r.ActiveAdmins != 1 || r.DefaultPasswords != 2 {
		t.Fatalf("incorrect audit: %+v, %v", r, err)
	}
	after, _ := os.ReadFile(copy)
	if !bytes.Equal(before, after) {
		t.Fatal("read-only audit changed database bytes")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err := run(context.Background(), options{db: missing}); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("audit created a new database")
	}
}

func TestOfflineProvisionPreservesBusinessAndMemberships(t *testing.T) {
	source, copy := fixture(t)
	before, _ := os.ReadFile(source)
	o := provisionOptions(t, source, copy)
	r, err := run(context.Background(), o)
	if err != nil || !r.Ready || !r.Provisioned || r.RotatedAccounts != 3 || r.DefaultPasswords != 0 {
		t.Fatalf("provisioning failed: %+v %v", r, err)
	}
	after, _ := os.ReadFile(source)
	if !bytes.Equal(before, after) {
		t.Fatal("original database changed")
	}
	info, err := os.Stat(o.credentials)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials file is not private")
	}
	raw, err := os.ReadFile(o.credentials)
	if err != nil {
		t.Fatal(err)
	}
	var secret map[string]string
	if json.Unmarshal(raw, &secret) != nil || len(secret["adminPassword"]) < 32 {
		t.Fatal("missing administrator credential")
	}
	db, err := sql.Open("sqlite", copy)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash, title, sprint, role string
	var active, count int
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin'`).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret["adminPassword"])) != nil {
		t.Fatal("private administrator credential does not match saved hash")
	}
	if err := db.QueryRow(`SELECT password_hash FROM users WHERE id='u_real'`).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte("ExistingPrivate2026!")) != nil {
		t.Fatal("non-default user password was changed")
	}
	if err := db.QueryRow(`SELECT active FROM users WHERE id='u_disabled'`).Scan(&active); err != nil || active != 0 {
		t.Fatal("disabled member was activated")
	}
	if err := db.QueryRow(`SELECT role FROM tenant_memberships WHERE user_id='u_admin'`).Scan(&role); err != nil || role != "tenant_admin" {
		t.Fatal("administrator role changed")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM project_members WHERE role='product'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("project memberships changed")
	}
	if err := db.QueryRow(`SELECT title FROM requirements WHERE id=42`).Scan(&title); err != nil || title != "不能改动的真实需求" {
		t.Fatal("business requirement changed")
	}
	if err := db.QueryRow(`SELECT name FROM sprints WHERE id=22`).Scan(&sprint); err != nil || sprint != "123" {
		t.Fatal("sprint changed")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE revoked_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatal("copied sessions still active")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_impersonations WHERE ended_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatal("copied impersonation still active")
	}
	var audit string
	if err := db.QueryRow(`SELECT after_json FROM audit_logs WHERE action='production.provisioned'`).Scan(&audit); err != nil || strings.Contains(audit, secret["adminPassword"]) || strings.Contains(audit, "password_hash") {
		t.Fatal("credentials leaked into audit")
	}
}

func TestProvisionRejectsOriginalSymlinksAndHardlinks(t *testing.T) {
	source, copy := fixture(t)
	soft := filepath.Join(t.TempDir(), "symlink.db")
	if err := os.Symlink(source, soft); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(filepath.Dir(source), "hardlink.db")
	if err := os.Link(source, hard); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, soft, hard} {
		o := provisionOptions(t, source, path)
		if _, err := run(context.Background(), o); err == nil {
			t.Fatal("original database alias accepted")
		}
		if _, err := os.Stat(o.credentials); !os.IsNotExist(err) {
			t.Fatal("created credentials despite original database guard")
		}
	}
	o := provisionOptions(t, source, copy)
	o.offline = false
	if _, err := run(context.Background(), o); err == nil {
		t.Fatal("implicit provisioning accepted")
	}
}

func TestProvisionRollbackWhenCredentialsOrCommitFails(t *testing.T) {
	for _, kind := range []string{"existing-output", "commit-failure"} {
		t.Run(kind, func(t *testing.T) {
			source, copy := fixture(t)
			o := provisionOptions(t, source, copy)
			db, err := sql.Open("sqlite", copy)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "existing-output" {
				if err := os.WriteFile(o.credentials, []byte("preserve existing private file"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.Exec(`CREATE TABLE deferred_failure(user_id TEXT REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER fail_commit AFTER INSERT ON audit_logs BEGIN INSERT INTO deferred_failure VALUES('no-such-user');END`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			if _, err := run(context.Background(), o); err == nil {
				t.Fatal("expected provisioning failure")
			}
			db, err = sql.Open("sqlite", copy)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var hash string
			var count int
			if err := db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin'`).Scan(&hash); err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(knownDevelopmentPassword)) != nil {
				t.Fatal("failed provisioning changed administrator password")
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE revoked_at IS NULL`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed provisioning revoked sessions")
			}
			if kind == "commit-failure" {
				if _, err := os.Stat(o.credentials); !os.IsNotExist(err) {
					t.Fatal("failed commit left a credential file")
				}
			} else {
				raw, _ := os.ReadFile(o.credentials)
				if string(raw) != "preserve existing private file" {
					t.Fatal("existing credential file overwritten")
				}
			}
		})
	}
}

func TestProductionEnvironmentAndSQLiteVersionGuards(t *testing.T) {
	t.Setenv("DEVFLOW_ADDR", "127.0.0.1:19080")
	t.Setenv("DEVFLOW_SESSION_SECRET", strings.Repeat("aZ9_", 12))
	t.Setenv("DEVFLOW_COOKIE_SECURE", "true")
	t.Setenv("DEVFLOW_SQLITE_SYNCHRONOUS", "FULL")
	if err := productionEnvironment(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ key, value string }{{"DEVFLOW_ADDR", ":19080"}, {"DEVFLOW_ADDR", "0.0.0.0:8080"}, {"DEVFLOW_COOKIE_SECURE", "false"}, {"DEVFLOW_SESSION_SECRET", "replace-with-at-least-32-random-bytes"}, {"DEVFLOW_SQLITE_SYNCHRONOUS", "NORMAL"}} {
		t.Run(test.key+test.value, func(t *testing.T) {
			t.Setenv(test.key, test.value)
			if productionEnvironment() == nil {
				t.Fatal("unsafe production environment accepted")
			}
		})
	}
	for _, version := range []string{"3.51.3", "3.53.4", "3.50.7", "3.44.6"} {
		if !walFixVersion(version) {
			t.Fatalf("fixed version rejected: %s", version)
		}
	}
	for _, version := range []string{"3.50.4", "3.51.2", "3.44.5", "unknown"} {
		if walFixVersion(version) {
			t.Fatalf("unfixed version accepted: %s", version)
		}
	}
}
