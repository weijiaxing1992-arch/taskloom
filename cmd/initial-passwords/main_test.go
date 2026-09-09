package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func initialFixture(t *testing.T) (initialOptions, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "initial password fixture ?#.db")
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
CREATE TABLE tenants(id TEXT PRIMARY KEY);
CREATE TABLE users(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,active INTEGER NOT NULL,operation_disabled INTEGER NOT NULL,password_hash TEXT NOT NULL,password_changed_at TEXT NOT NULL,must_change_password INTEGER NOT NULL DEFAULT 0,email TEXT,role TEXT,department TEXT);
CREATE TABLE auth_sessions(id TEXT PRIMARY KEY,tenant_id TEXT,user_id TEXT,revoked_at TEXT);
CREATE TABLE auth_impersonations(id TEXT PRIMARY KEY,tenant_id TEXT,admin_user_id TEXT,target_user_id TEXT,ended_at TEXT);
CREATE TABLE audit_logs(id INTEGER PRIMARY KEY,tenant_id TEXT,project_id TEXT,actor_id TEXT,object_type TEXT,object_id TEXT,action TEXT,before_json TEXT,after_json TEXT,created_at TEXT);
CREATE TABLE password_initialization_batches(tenant_id TEXT NOT NULL,batch_id TEXT NOT NULL,applied_at TEXT NOT NULL,target_ids_json TEXT NOT NULL,target_count INTEGER NOT NULL,PRIMARY KEY(tenant_id,batch_id));
CREATE TABLE member_departments(tenant_id TEXT,user_id TEXT,department_id TEXT);
INSERT INTO tenants VALUES('tenant-a'),('tenant-b');
INSERT INTO users VALUES('u1','tenant-a',1,0,'old-hash-one','2026-01-01T00:00:00Z',0,'one@example.test','tenant_admin','department-a'),('u2','tenant-a',0,1,'old-hash-two','2026-01-01T00:00:00Z',0,'two@example.test','viewer','department-b'),('u3','tenant-b',1,0,'other-tenant-hash','2026-01-01T00:00:00Z',0,'three@example.test','member','department-c');
INSERT INTO auth_sessions VALUES('s1','tenant-a','u1',NULL),('s2','tenant-a','u2',NULL),('s3','tenant-a','u1','already-revoked'),('other-session','tenant-b','u1',NULL);
INSERT INTO auth_impersonations VALUES('i1','tenant-a','u1','u2',NULL),('i2','tenant-a','not-target','u1',NULL),('i3','tenant-a','u1','u2','already-ended'),('other-impersonation','tenant-b','u1','u2',NULL);
INSERT INTO member_departments VALUES('tenant-a','u1','d1'),('tenant-a','u2','d2');
`)
	if err != nil {
		t.Fatal(err)
	}
	return initialOptions{DB: path, Tenant: "tenant-a", BatchID: "fixture-batch-1"}, db
}
func execute(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}
func countRows(t *testing.T, db *sql.DB, statement string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(statement, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func fixtureInput() io.Reader { return strings.NewReader("Fixture!9\nFixture!9\n") }

type neverRead struct{}

func (neverRead) Read([]byte) (int, error) {
	panic("stdin must not be read for a preview or completed batch")
}
func snapshot(t *testing.T, db *sql.DB, tenant string) []initialTarget {
	t.Helper()
	targets, err := initialTargets(context.Background(), db, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return targets
}

func TestInitialPasswordsDryRunIsReadOnlyRepeatableAndRedacted(t *testing.T) {
	o, db := initialFixture(t)
	before := snapshot(t, db, o.Tenant)
	beforeBytes, err := os.ReadFile(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.Mode != "dry-run" || first.Changes != 0 || first.PlannedChanges != 2 || first.TargetCount != 2 {
		t.Fatalf("unexpected preview: %+v", first)
	}
	if !reflect.DeepEqual(before, snapshot(t, db, o.Tenant)) || countRows(t, db, `SELECT COUNT(*) FROM audit_logs`) != 0 || countRows(t, db, `SELECT COUNT(*) FROM password_initialization_batches`) != 0 {
		t.Fatal("dry run mutated data")
	}
	afterBytes, err := os.ReadFile(o.DB)
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("preview changed database bytes", err)
	}
	raw, _ := json.Marshal(first)
	for _, secret := range []string{"password_hash", "old-hash", "example.test", "Fixture!9", "ChangedAt"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("report contains secret or unrequested personal fields")
		}
	}
	if !reflect.DeepEqual(first.TargetIDs, []string{"u1", "u2"}) || first.Users[1].Active || !first.Users[1].OperationDisabled {
		t.Fatal("preview omitted inactive/disabled users")
	}
	ro, err := openInitialDatabase(o)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.Exec(`UPDATE users SET active=0`); err == nil {
		t.Fatal("preview connection permits writes")
	}
}

func TestInitialPasswordsApplyUniqueHashesAtomicRevocationAndPermissions(t *testing.T) {
	o, db := initialFixture(t)
	o.Apply = true
	other := snapshot(t, db, "tenant-b")
	before := snapshot(t, db, o.Tenant)
	report, err := runInitialPasswords(context.Background(), o, fixtureInput(), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changes != 2 || report.TargetCount != 2 || report.AlreadyApplied || report.AppliedAt == "" {
		t.Fatalf("bad apply receipt: %+v", report)
	}
	after := snapshot(t, db, o.Tenant)
	for i, target := range after {
		if bcrypt.CompareHashAndPassword([]byte(target.Hash), []byte("Fixture!9")) != nil || !target.User.MustChangePassword || target.ChangedAt == before[i].ChangedAt {
			t.Fatal("password or required-change flag not updated")
		}
		if target.User.Active != before[i].User.Active || target.User.OperationDisabled != before[i].User.OperationDisabled {
			t.Fatal("account state changed")
		}
		cost, err := bcrypt.Cost([]byte(target.Hash))
		if err != nil || cost != bcrypt.MinCost {
			t.Fatal("wrong hashing policy")
		}
	}
	if after[0].Hash == after[1].Hash {
		t.Fatal("accounts reused one salt/hash")
	}
	if !reflect.DeepEqual(other, snapshot(t, db, "tenant-b")) {
		t.Fatal("cross-tenant mutation")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM users WHERE id='u1' AND email='one@example.test' AND role='tenant_admin' AND department='department-a'`) != 1 || countRows(t, db, `SELECT COUNT(*) FROM member_departments`) != 2 {
		t.Fatal("identity/permissions/departments changed")
	}
	for _, query := range []string{`SELECT COUNT(*) FROM auth_sessions WHERE tenant_id='tenant-a' AND revoked_at IS NULL`, `SELECT COUNT(*) FROM auth_impersonations WHERE tenant_id='tenant-a' AND ended_at IS NULL`} {
		if countRows(t, db, query) != 0 {
			t.Fatal("related login/access was not revoked")
		}
	}
	if countRows(t, db, `SELECT COUNT(*) FROM auth_sessions WHERE id='s3' AND revoked_at='already-revoked'`) != 1 || countRows(t, db, `SELECT COUNT(*) FROM auth_impersonations WHERE id='i3' AND ended_at='already-ended'`) != 1 || countRows(t, db, `SELECT COUNT(*) FROM auth_sessions WHERE tenant_id='tenant-b' AND revoked_at IS NULL`) != 1 || countRows(t, db, `SELECT COUNT(*) FROM auth_impersonations WHERE tenant_id='tenant-b' AND ended_at IS NULL`) != 1 {
		t.Fatal("unrelated/prior revocation changed")
	}
	if countRows(t, db, `SELECT COUNT(*) FROM audit_logs WHERE tenant_id='tenant-a' AND action='user.initial_password_reset' AND actor_id='initial-passwords-cli'`) != 2 || countRows(t, db, `SELECT COUNT(*) FROM password_initialization_batches WHERE tenant_id='tenant-a' AND target_count=2`) != 1 {
		t.Fatal("missing audit or batch")
	}
	rows, err := db.Query(`SELECT before_json,after_json FROM audit_logs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var before, after string
		if err := rows.Scan(&before, &after); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(before+after, "hash") || strings.Contains(before+after, "Fixture!9") {
			t.Fatal("audit contains a credential")
		}
	}
}

func TestInitialPasswordsCompletedBatchNeverOverwritesChangedPasswordsOrNewUsers(t *testing.T) {
	o, db := initialFixture(t)
	o.Apply = true
	if _, err := runInitialPasswords(context.Background(), o, fixtureInput(), bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	execute(t, db, `UPDATE users SET password_hash='user-chosen-new-hash',password_changed_at='later',must_change_password=0 WHERE id='u1'`)
	execute(t, db, `INSERT INTO users VALUES('u4','tenant-a',1,0,'new-account-hash','later',0,'four@example.test','member','d4')`)
	before := snapshot(t, db, o.Tenant)
	for _, apply := range []bool{false, true, false} {
		o.Apply = apply
		report, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		if !report.AlreadyApplied || report.Changes != 0 || report.PlannedChanges != 0 || report.TargetCount != 2 || report.CurrentUserCount != 3 || !reflect.DeepEqual(report.TargetIDs, []string{"u1", "u2"}) {
			t.Fatalf("lost completed snapshot: %+v", report)
		}
	}
	if !reflect.DeepEqual(before, snapshot(t, db, o.Tenant)) || countRows(t, db, `SELECT COUNT(*) FROM audit_logs`) != 2 {
		t.Fatal("replay overwrote subsequent credentials or repeated audit")
	}
}

func TestInitialPasswordsFailuresRollbackWholeBatch(t *testing.T) {
	for _, entry := range []struct{ name, trigger string }{
		{"second-user-audit", `CREATE TRIGGER fail_write BEFORE INSERT ON audit_logs WHEN NEW.object_id='u2' BEGIN SELECT RAISE(ABORT,'test'); END`},
		{"batch-marker", `CREATE TRIGGER fail_write BEFORE INSERT ON password_initialization_batches BEGIN SELECT RAISE(ABORT,'test'); END`},
		{"session-revocation", `CREATE TRIGGER fail_write BEFORE UPDATE ON auth_sessions WHEN OLD.user_id='u2' BEGIN SELECT RAISE(ABORT,'test'); END`},
		{"impersonation-revocation", `CREATE TRIGGER fail_write BEFORE UPDATE ON auth_impersonations BEGIN SELECT RAISE(ABORT,'test'); END`},
	} {
		t.Run(entry.name, func(t *testing.T) {
			o, db := initialFixture(t)
			o.Apply = true
			before := snapshot(t, db, o.Tenant)
			execute(t, db, entry.trigger)
			if _, err := runInitialPasswords(context.Background(), o, fixtureInput(), bcrypt.MinCost); err == nil {
				t.Fatal("expected failure")
			}
			if !reflect.DeepEqual(before, snapshot(t, db, o.Tenant)) || countRows(t, db, `SELECT COUNT(*) FROM audit_logs`) != 0 || countRows(t, db, `SELECT COUNT(*) FROM password_initialization_batches`) != 0 || countRows(t, db, `SELECT COUNT(*) FROM auth_sessions WHERE tenant_id='tenant-a' AND revoked_at IS NULL`) != 2 || countRows(t, db, `SELECT COUNT(*) FROM auth_impersonations WHERE tenant_id='tenant-a' AND ended_at IS NULL`) != 2 {
				t.Fatal("failed transaction partially committed")
			}
		})
	}
}

func TestInitialPasswordsWriteLockRechecksEntireSnapshot(t *testing.T) {
	for _, change := range []string{
		`UPDATE users SET password_hash='concurrent-new-password' WHERE id='u1'`,
		`UPDATE users SET password_changed_at='concurrent' WHERE id='u2'`,
		`UPDATE users SET active=0 WHERE id='u1'`,
		`UPDATE users SET operation_disabled=0 WHERE id='u2'`,
		`UPDATE users SET must_change_password=1 WHERE id='u1'`,
		`INSERT INTO users VALUES('u4','tenant-a',1,0,'new-hash','now',0,'four@example.test','member','d4')`,
	} {
		t.Run(change, func(t *testing.T) {
			o, db := initialFixture(t)
			o.Apply = true
			before := snapshot(t, db, o.Tenant)
			hashes := [][]byte{}
			for range before {
				hash, err := bcrypt.GenerateFromPassword([]byte("Fixture!9"), bcrypt.MinCost)
				if err != nil {
					t.Fatal(err)
				}
				hashes = append(hashes, hash)
			}
			execute(t, db, change)
			changed := snapshot(t, db, o.Tenant)
			writer, err := openInitialDatabase(o)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err := applyInitialPasswords(context.Background(), writer, o, before, hashes); err == nil || !strings.Contains(err.Error(), "snapshot changed") {
				t.Fatal("concurrent update was not rejected", err)
			}
			if !reflect.DeepEqual(changed, snapshot(t, db, o.Tenant)) || countRows(t, db, `SELECT COUNT(*) FROM audit_logs`) != 0 {
				t.Fatal("concurrent state overwritten")
			}
		})
	}
}

func TestInitialPasswordsConcurrentCompletedBatchWinsOverStalePreparedHashes(t *testing.T) {
	o, db := initialFixture(t)
	o.Apply = true
	old := snapshot(t, db, o.Tenant)
	hashes := [][]byte{}
	for range old {
		hash, _ := bcrypt.GenerateFromPassword([]byte("Stale!99"), bcrypt.MinCost)
		hashes = append(hashes, hash)
	}
	if _, err := runInitialPasswords(context.Background(), o, fixtureInput(), bcrypt.MinCost); err != nil {
		t.Fatal(err)
	}
	current := snapshot(t, db, o.Tenant)
	writer, err := openInitialDatabase(o)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	report, err := applyInitialPasswords(context.Background(), writer, o, old, hashes)
	if err != nil || !report.AlreadyApplied || report.Changes != 0 {
		t.Fatal("concurrent replay failed", err, report)
	}
	if !reflect.DeepEqual(current, snapshot(t, db, o.Tenant)) {
		t.Fatal("prepared stale hash overwrote completed batch")
	}
}

func TestInitialPasswordsRequireExistingMigratedSchemaAndExplicitTenant(t *testing.T) {
	o, db := initialFixture(t)
	for _, patch := range []initialOptions{{DB: filepath.Join(t.TempDir(), "missing.db"), Tenant: o.Tenant}, {DB: "relative.db", Tenant: o.Tenant}, {DB: o.DB}, {DB: o.DB, Tenant: "missing"}, {DB: o.DB, Tenant: o.Tenant, Apply: true}, {DB: o.DB, Tenant: o.Tenant, BatchID: "../bad"}} {
		if _, err := runInitialPasswords(context.Background(), patch, neverRead{}, bcrypt.MinCost); err == nil {
			t.Fatalf("accepted invalid options %+v", patch)
		}
	}
	link := filepath.Join(t.TempDir(), "symlink.db")
	if err := os.Symlink(o.DB, link); err != nil {
		t.Fatal(err)
	}
	linked := o
	linked.DB = link
	if _, err := runInitialPasswords(context.Background(), linked, neverRead{}, bcrypt.MinCost); err == nil {
		t.Fatal("accepted symlink")
	}
	execute(t, db, `ALTER TABLE users DROP COLUMN must_change_password`)
	if _, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost); err == nil || !strings.Contains(err.Error(), "deploy server migrations first") {
		t.Fatal("schema absence was not explicit", err)
	}
	if countRows(t, db, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='must_change_password'`) != 0 {
		t.Fatal("CLI migrated schema")
	}
}

func TestInitialPasswordsMissingOrCorruptBatchTableFailsClosed(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		o, db := initialFixture(t)
		execute(t, db, `DROP TABLE password_initialization_batches`)
		if _, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost); err == nil {
			t.Fatal("accepted missing batch table")
		}
	})
	for _, raw := range []string{`not-json`, `["u1","u1"]`, `["u1"]`, `null`} {
		t.Run(raw, func(t *testing.T) {
			o, db := initialFixture(t)
			o.Apply = true
			execute(t, db, `INSERT INTO password_initialization_batches VALUES(?,?,'now',?,2)`, o.Tenant, o.BatchID, raw)
			if _, err := runInitialPasswords(context.Background(), o, neverRead{}, bcrypt.MinCost); err == nil {
				t.Fatal("corrupt marker allowed password reset")
			}
		})
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errors.New("test read failure") }
func TestInitialPasswordsSecureInputPolicy(t *testing.T) {
	if _, err := readInitialPassword(nil); err == nil {
		t.Fatal("missing stdin accepted")
	}
	for _, password := range []string{"tempAA", strings.Repeat("a", 72), "中文"} {
		value, err := readInitialPassword(strings.NewReader(password + "\n" + password + "\n"))
		if err != nil || string(value) != password {
			t.Fatal("valid temporary policy rejected", err)
		}
		clear(value)
	}
	for _, input := range []string{"short\nshort\n", "only-one-line\n", "first!9\nother!9\n", "valid!9\nvalid!9\nextra\n", "white space\nwhite space\n", "tab\tvalue\ntab\tvalue\n", "zero\x00value\nzero\x00value\n", "zero\u200bvalue\nzero\u200bvalue\n", strings.Repeat("a", 73) + "\n" + strings.Repeat("a", 73), "\xffabcdef\n\xffabcdef"} {
		if _, err := readInitialPassword(strings.NewReader(input)); err == nil {
			t.Fatal("invalid password input accepted")
		}
	}
	if _, err := readInitialPassword(failedReader{}); err == nil {
		t.Fatal("reader error accepted")
	}
	file, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := readInitialPassword(file); err == nil || !strings.Contains(err.Error(), "refusing terminal input") {
		t.Fatal("character device accepted", err)
	}
}
