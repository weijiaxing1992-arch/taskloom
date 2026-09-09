package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

const testPassword = "StrongPrivatePassword2026!"

func credentialFixture(t *testing.T) (*sql.DB, credentialOptions) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,tenant_id TEXT,email TEXT,active INTEGER,operation_disabled INTEGER DEFAULT 0,password_hash TEXT,password_changed_at TEXT);
CREATE TABLE tenant_memberships(tenant_id TEXT,user_id TEXT,role TEXT,status TEXT);
CREATE TABLE auth_sessions(tenant_id TEXT,user_id TEXT,revoked_at TEXT);
CREATE TABLE auth_impersonations(tenant_id TEXT,admin_user_id TEXT,target_user_id TEXT,ended_at TEXT);
CREATE TABLE audit_logs(tenant_id TEXT,project_id TEXT,actor_id TEXT,object_type TEXT,object_id TEXT,action TEXT,before_json TEXT,after_json TEXT,created_at TEXT);
CREATE TABLE requirements(id INTEGER,title TEXT);INSERT INTO requirements VALUES(1,'business data');
INSERT INTO users VALUES('admin','tenant','admin@example.test',1,0,'old-secret-hash','old-time'),('member','tenant','member@example.test',1,0,'member-hash','old-time'),('foreign','other','foreign@example.test',1,0,'foreign-hash','old-time');
INSERT INTO tenant_memberships VALUES('tenant','admin','tenant_admin','active'),('tenant','member','member','active'),('other','foreign','tenant_admin','active');
INSERT INTO auth_sessions VALUES('tenant','admin',NULL),('tenant','member',NULL),('other','foreign',NULL);
INSERT INTO auth_impersonations VALUES('tenant','admin','member',NULL),('tenant','member','admin',NULL),('other','foreign','foreign',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, credentialOptions{DB: path, Tenant: "tenant", User: "admin", ExpectedEmail: "admin@example.test", Email: "new@example.test"}
}
func TestCredentialChangeIsExactAtomicAndRedacted(t *testing.T) {
	db, o := credentialFixture(t)
	if err := changeCredentials(context.Background(), o, []byte(testPassword), bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	var email, hash, changed string
	db.QueryRow(`SELECT email,password_hash,password_changed_at FROM users WHERE id='admin'`).Scan(&email, &hash, &changed)
	if email != o.Email || bcrypt.CompareHashAndPassword([]byte(hash), []byte(testPassword)) != nil || changed == "old-time" {
		t.Fatal("target credentials not saved")
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{`SELECT count(*) FROM auth_sessions WHERE user_id='admin' AND revoked_at IS NULL`, 0}, {`SELECT count(*) FROM auth_sessions WHERE user_id!='admin' AND revoked_at IS NULL`, 2}, {`SELECT count(*) FROM auth_impersonations WHERE tenant_id='tenant' AND ended_at IS NULL`, 0}, {`SELECT count(*) FROM auth_impersonations WHERE tenant_id='other' AND ended_at IS NULL`, 1}, {`SELECT count(*) FROM requirements WHERE title='business data'`, 1}, {`SELECT count(*) FROM users WHERE id='member' AND password_hash='member-hash'`, 1}} {
		var n int
		if err := db.QueryRow(tc.query).Scan(&n); err != nil || n != tc.want {
			t.Fatalf("scope leak %d %v", n, err)
		}
	}
	var audit string
	if err := db.QueryRow(`SELECT before_json||after_json FROM audit_logs`).Scan(&audit); err != nil || strings.Contains(audit, testPassword) || strings.Contains(audit, hash) || strings.Contains(audit, "password_hash") {
		t.Fatal("secret in audit")
	}
	if err := changeCredentials(context.Background(), o, []byte(testPassword), bcrypt.MinCost); err == nil {
		t.Fatal("stale expected-email accepted")
	}
}
func TestCredentialChangeFailuresNeverModifyAccounts(t *testing.T) {
	for _, scenario := range []string{"wrong-user", "wrong-tenant", "wrong-email", "duplicate-email", "disabled", "inactive", "not-admin", "audit-failure", "session-failure"} {
		t.Run(scenario, func(t *testing.T) {
			db, o := credentialFixture(t)
			switch scenario {
			case "wrong-user":
				o.User = "missing"
			case "wrong-tenant":
				o.Tenant = "other"
			case "wrong-email":
				o.ExpectedEmail = "old@example.test"
			case "duplicate-email":
				o.Email = "MEMBER@example.test"
			case "disabled":
				db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='admin'`)
			case "inactive":
				db.Exec(`UPDATE users SET active=0 WHERE id='admin'`)
			case "not-admin":
				db.Exec(`UPDATE tenant_memberships SET role='member' WHERE user_id='admin'`)
			case "audit-failure":
				db.Exec(`CREATE TRIGGER deny_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "session-failure":
				db.Exec(`CREATE TRIGGER deny_sessions BEFORE UPDATE ON auth_sessions BEGIN SELECT RAISE(ABORT,'injected'); END`)
			}
			if err := changeCredentials(context.Background(), o, []byte(testPassword), bcrypt.MinCost); err == nil {
				t.Fatal("unsafe change accepted")
			}
			var n int
			db.QueryRow(`SELECT count(*) FROM users WHERE id='admin' AND email='admin@example.test' AND password_hash='old-secret-hash'`).Scan(&n)
			if n != 1 {
				t.Fatal("partial credential change")
			}
			db.QueryRow(`SELECT count(*) FROM auth_sessions WHERE revoked_at IS NULL`).Scan(&n)
			if n != 3 {
				t.Fatal("partial session revocation")
			}
			db.QueryRow(`SELECT count(*) FROM audit_logs`).Scan(&n)
			if n != 0 {
				t.Fatal("partial audit")
			}
		})
	}
}
func TestCredentialInputAndMissingDatabaseAreSafe(t *testing.T) {
	for _, input := range []string{"", testPassword + "\n", testPassword + "\nwrong-password\n", testPassword + "\n" + testPassword + "\nextra", strings.Repeat("x", 1100), "onlyletters\nonlyletters", "123456789012\n123456789012"} {
		if _, err := readCredentialPassword(strings.NewReader(input)); err == nil {
			t.Fatal("invalid secret input accepted")
		}
	}
	p, err := readCredentialPassword(strings.NewReader(testPassword + "\n" + testPassword + "\n"))
	if err != nil || string(p) != testPassword {
		t.Fatal("valid pipe input failed")
	}
	clear(p)
	_, o := credentialFixture(t)
	o.DB = filepath.Join(t.TempDir(), "missing.db")
	if err := changeCredentials(context.Background(), o, []byte(testPassword), bcrypt.MinCost); err == nil {
		t.Fatal("created missing db")
	}
	if _, err := os.Stat(o.DB); !os.IsNotExist(err) {
		t.Fatal("missing file created")
	}
}
func TestCredentialToolSupportsPreBusinessSuspensionSchema(t *testing.T) {
	db, o := credentialFixture(t)
	if _, err := db.Exec(`ALTER TABLE users DROP COLUMN operation_disabled`); err != nil {
		t.Fatal(err)
	}
	if err := changeCredentials(context.Background(), o, []byte(testPassword), bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
}
