package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func bootstrapFixture(t *testing.T) (*sql.DB, bootstrapOptions, bootstrapManifest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "organization.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
CREATE TABLE tenants(id TEXT PRIMARY KEY,name TEXT); INSERT INTO tenants VALUES('tenant','Team'),('other','Other');
CREATE TABLE projects(id TEXT PRIMARY KEY,tenant_id TEXT,name TEXT,code TEXT,status TEXT);INSERT INTO projects VALUES('project','tenant','Team Project','TEAM','active'),('project2','tenant','Other Project','T2','active'),('foreign','other','Private Project','FP','active');
CREATE TABLE users(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,name TEXT NOT NULL,email TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 1,operation_disabled INTEGER NOT NULL DEFAULT 0,must_change_password INTEGER NOT NULL DEFAULT 0,department TEXT NOT NULL DEFAULT '',employee_no TEXT NOT NULL DEFAULT '',last_active TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL DEFAULT '',password_changed_at TEXT NOT NULL DEFAULT '',directory_source TEXT NOT NULL DEFAULT 'local',directory_external_id TEXT NOT NULL DEFAULT '',locale TEXT NOT NULL DEFAULT 'zh-CN',timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai');
CREATE UNIQUE INDEX idx_user_directory ON users(tenant_id,directory_source,directory_external_id) WHERE directory_external_id!='';
CREATE TABLE tenant_memberships(tenant_id TEXT,user_id TEXT,role TEXT,status TEXT,created_at TEXT,updated_at TEXT,PRIMARY KEY(tenant_id,user_id));
CREATE TABLE project_members(tenant_id TEXT,project_id TEXT,user_id TEXT,role TEXT,created_at TEXT,updated_at TEXT,PRIMARY KEY(tenant_id,project_id,user_id));
CREATE TABLE memberships(tenant_id TEXT,project_id TEXT,user_id TEXT,role TEXT,PRIMARY KEY(tenant_id,project_id,user_id));
CREATE TABLE departments(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,parent_id TEXT,name TEXT NOT NULL,code TEXT NOT NULL,source TEXT NOT NULL DEFAULT 'local',external_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'active',sort_order INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,code),UNIQUE(tenant_id,source,external_id));
CREATE TABLE department_memberships(tenant_id TEXT,department_id TEXT,user_id TEXT,is_primary INTEGER NOT NULL DEFAULT 0,title TEXT NOT NULL DEFAULT '',source TEXT NOT NULL DEFAULT 'local',external_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'active',joined_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,department_id,user_id));
CREATE TABLE audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT,project_id TEXT,actor_id TEXT,object_type TEXT,object_id TEXT,action TEXT,before_json TEXT,after_json TEXT,created_at TEXT);
CREATE TABLE auth_sessions(token_hash TEXT,tenant_id TEXT,user_id TEXT,revoked_at TEXT);INSERT INTO auth_sessions VALUES('session-1','tenant','u_admin',NULL),('session-2','tenant','u_bob',NULL),('session-3','other','u_foreign',NULL);
CREATE TABLE auth_impersonations(tenant_id TEXT,admin_user_id TEXT,target_user_id TEXT,ended_at TEXT);INSERT INTO auth_impersonations VALUES('tenant','u_admin','u_bob',NULL);
CREATE TABLE requirements(id INTEGER,title TEXT,owner_user_id TEXT);INSERT INTO requirements VALUES(1,'Untouched business data','u_admin');
CREATE TABLE user_notifications(id INTEGER,title TEXT);CREATE TABLE user_wecom_jobs(id INTEGER,payload TEXT);
INSERT INTO departments VALUES('dept_old','tenant',NULL,'研发部','RND','wecom','existing-ext','active',40,'old','old'),('dept_foreign','other',NULL,'外部部门','EXT','local','ext','active',1,'old','old');
INSERT INTO users(id,tenant_id,name,email,active,department,password_hash,password_changed_at,directory_source,directory_external_id,locale) VALUES('u_admin','tenant','林夏','admin@example.com',1,'研发部','existing-admin-hash','old-secret-time','wecom','external-admin','en-US'),('u_bob','tenant','Bob','bob@example.test',1,'研发部','existing-bob-hash','old-secret-time','local','bob','zh-CN'),('u_legacy','tenant','Legacy Admin','legacy@example.test',1,'研发部','legacy-admin-hash','old-secret-time','local','legacy','zh-CN'),('u_foreign','other','New Person','foreign@example.test',1,'外部部门','foreign-hash','old-secret-time','local','foreign','zh-CN');
INSERT INTO tenant_memberships VALUES('tenant','u_admin','tenant_admin','active','old','old'),('tenant','u_bob','member','active','old','old'),('tenant','u_legacy','tenant_admin','active','old','old'),('other','u_foreign','tenant_admin','active','old','old');
INSERT INTO project_members VALUES('tenant','project','u_admin','tenant_admin','old','old'),('tenant','project','u_bob','backend','old','old'),('tenant','project','u_legacy','project_admin','old','old'),('tenant','project2','u_bob','algorithm','old','old'),('other','foreign','u_foreign','project_admin','old','old');
INSERT INTO memberships SELECT tenant_id,project_id,user_id,role FROM project_members;
INSERT INTO department_memberships(tenant_id,department_id,user_id,is_primary,status,joined_at,updated_at) VALUES('tenant','dept_old','u_admin',1,'active','old','old'),('tenant','dept_old','u_bob',1,'active','old','old'),('tenant','dept_old','u_legacy',1,'active','old','old');`)
	if err != nil {
		t.Fatal(err)
	}
	m := bootstrapManifest{Version: 1, Administrators: []string{"admin@example.com", "alice@example.test"}, Departments: []bootstrapDepartment{{Key: "root", ExistingID: "dept_old", Name: "研发中心", SortOrder: 1}, {Key: "frontend", Name: "前端组", ParentKey: "root", SortOrder: 10}}, Members: []bootstrapMember{
		{Name: "示例管理员", Email: "admin@example.com", ExpectedName: "林夏", ExistingUserID: "u_admin", DepartmentKeys: []string{"root"}, PrimaryDepartmentKey: "root", ProjectRole: "project_admin"},
		{Name: "Alice", Email: "alice@example.test", DepartmentKeys: []string{"root"}, PrimaryDepartmentKey: "root", ProjectRole: "project_admin"},
		{Name: "Bob", Email: "bob@example.test", DepartmentKeys: []string{"frontend"}, PrimaryDepartmentKey: "frontend", ProjectRole: "frontend"},
		{Name: "New Person", Email: "new@example.test", DepartmentKeys: []string{"frontend"}, PrimaryDepartmentKey: "frontend", ProjectRole: "backend"},
	}}
	return db, bootstrapOptions{DB: path, Tenant: "tenant", Project: "project", Input: "-"}, m
}
func runManifest(t *testing.T, o bootstrapOptions, m bootstrapManifest) (bootstrapPlan, error) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return runBootstrap(context.Background(), o, strings.NewReader(string(raw)), bcrypt.MinCost)
}
func snapshotTables(t *testing.T, db *sql.DB, tables ...string) string {
	t.Helper()
	out := map[string][]any{}
	for _, table := range tables {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		values := []any{}
		for rows.Next() {
			row := make([]any, len(columns))
			scan := make([]any, len(columns))
			for i := range row {
				scan[i] = &row[i]
			}
			if err = rows.Scan(scan...); err != nil {
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		out[table] = values
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

var bootstrapTables = []string{"users", "departments", "department_memberships", "tenant_memberships", "project_members", "memberships", "audit_logs", "auth_sessions", "auth_impersonations", "requirements", "user_notifications", "user_wecom_jobs"}

func scalar(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBootstrapDefaultPreviewIsStrictlyReadOnly(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	before := snapshotTables(t, db, bootstrapTables...)
	bytesBefore, err := os.ReadFile(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	p, err := runManifest(t, o, m)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "dry-run" || p.Changes != 6 || len(p.Members) != 4 {
		t.Fatalf("incorrect preview %#v", p)
	}
	if after := snapshotTables(t, db, bootstrapTables...); after != before {
		t.Fatal("dry run changed data")
	}
	bytesAfter, _ := os.ReadFile(o.DB)
	if string(bytesBefore) != string(bytesAfter) {
		t.Fatal("dry run wrote database bytes")
	}
	raw, _ := json.Marshal(p)
	for _, secret := range []string{"existing-admin-hash", "password_hash", "token_hash", "session-1"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("preview exposed credential data")
		}
	}
}
func TestBootstrapApplyPreservesIdentityCredentialsSessionsAndOtherScopes(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	untouched := snapshotTables(t, db, "auth_sessions", "auth_impersonations", "requirements", "user_notifications", "user_wecom_jobs")
	o.Apply = true
	p, err := runManifest(t, o, m)
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "applied" || p.Changes != 6 {
		t.Fatalf("incorrect apply %#v", p)
	}
	if scalar(t, db, `SELECT name||'|'||email||'|'||password_hash||'|'||password_changed_at||'|'||active||'|'||directory_source||'|'||directory_external_id||'|'||locale FROM users WHERE id='u_admin'`) != "示例管理员|admin@example.com|existing-admin-hash|old-secret-time|1|wecom|external-admin|en-US" {
		t.Fatal("existing administrator identity, credential or preference changed unexpectedly")
	}
	if snapshotTables(t, db, "auth_sessions", "auth_impersonations", "requirements", "user_notifications", "user_wecom_jobs") != untouched {
		t.Fatal("sessions, business data or external messaging changed")
	}
	if scalar(t, db, `SELECT role FROM project_members WHERE user_id='u_bob' AND project_id='project2'`) != "algorithm" {
		t.Fatal("other project role changed")
	}
	if scalar(t, db, `SELECT role FROM tenant_memberships WHERE user_id='u_legacy'`) != "tenant_admin" {
		t.Fatal("unlisted admin downgraded")
	}
	if scalar(t, db, `SELECT password_hash||'|'||name FROM users WHERE id='u_foreign'`) != "foreign-hash|New Person" {
		t.Fatal("foreign tenant changed")
	}
	for _, email := range []string{"alice@example.test", "new@example.test"} {
		var active, mustChange bool
		var hash, role, status string
		if err := db.QueryRow(`SELECT u.active,u.password_hash,tm.role,tm.status,u.must_change_password FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE email=?`, email).Scan(&active, &hash, &role, &status, &mustChange); err != nil {
			t.Fatal(err)
		}
		if active || status != "disabled" || !mustChange {
			t.Fatal("new account was activated")
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			t.Fatal("new account lacks a bcrypt hash")
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte("TaskLoom123!")) == nil {
			t.Fatal("new account has shared password")
		}
		if (email == "alice@example.test") != (role == "tenant_admin") {
			t.Fatal("administrator allowlist not applied")
		}
	}
	if scalar(t, db, `SELECT COUNT(*) FROM audit_logs`) != "6" {
		t.Fatal("missing per-object audit")
	}
	audit := snapshotTables(t, db, "audit_logs")
	if strings.Contains(audit, "existing-admin-hash") || strings.Contains(audit, "password_hash") {
		t.Fatal("secret leaked into audit")
	}
	if scalar(t, db, `SELECT parent_id FROM departments WHERE code='BOOT-frontend'`) != "dept_old" {
		t.Fatal("department parent not saved")
	}
	if scalar(t, db, `SELECT status||'|'||is_primary FROM department_memberships WHERE user_id='u_bob' AND department_id='dept_old'`) != "inactive|0" {
		t.Fatal("old department relationship not retained as inactive")
	}
}
func TestBootstrapReplayIsIdempotentIncludingExplicitRename(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	o.Apply = true
	if _, err := runManifest(t, o, m); err != nil {
		t.Fatal(err)
	}
	before := snapshotTables(t, db, bootstrapTables...)
	p, err := runManifest(t, o, m)
	if err != nil {
		t.Fatal(err)
	}
	if p.Changes != 0 || p.Mode != "unchanged" {
		t.Fatal("replay was not a no-op")
	}
	if snapshotTables(t, db, bootstrapTables...) != before {
		t.Fatal("replay wrote timestamps, audit or credentials")
	}
}

func TestBootstrapConcurrentDirectoryChangeInvalidatesPreparedPlan(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	o.Apply = true
	if err := validateManifest(&m); err != nil {
		t.Fatal(err)
	}
	readonly, err := openBootstrapDB(o, false)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	tx, err := readonly.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planBootstrap(context.Background(), tx, o, m)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE project_members SET role='qa' WHERE user_id='u_bob' AND project_id='project'`); err != nil {
		t.Fatal(err)
	}
	before := snapshotTables(t, db, bootstrapTables...)
	_, err = applyPreparedBootstrap(context.Background(), o, m, plan, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "changed during preparation") {
		t.Fatal("stale plan was not rejected")
	}
	if snapshotTables(t, db, bootstrapTables...) != before {
		t.Fatal("stale import changed reviewed data")
	}
}
func TestBootstrapCollisionScopeAndAuditFailuresRollBackEverything(t *testing.T) {
	for _, scenario := range []string{"project-foreign", "project-archived", "duplicate-email", "duplicate-name", "wrong-user-id", "wrong-expected-name", "missing-email-account", "foreign-department", "disabled-department", "code-collision", "sibling-name", "existing-cycle", "audit-failure", "member-write-failure", "missing-schema"} {
		t.Run(scenario, func(t *testing.T) {
			db, o, m := bootstrapFixture(t)
			o.Apply = true
			switch scenario {
			case "project-foreign":
				o.Project = "foreign"
			case "project-archived":
				db.Exec(`UPDATE projects SET status='archived' WHERE id='project'`)
			case "duplicate-email":
				db.Exec(`UPDATE users SET email='WEIJIAXING@TELROBOT.TOP' WHERE id='u_bob'`)
			case "duplicate-name":
				db.Exec(`UPDATE users SET name='New Person' WHERE id='u_bob'`)
			case "wrong-user-id":
				m.Members[0].ExistingUserID = "u_bob"
			case "wrong-expected-name":
				m.Members[0].ExpectedName = "Someone else"
			case "missing-email-account":
				m.Members[0].Email = "unmatched@example.test"
				m.Administrators[0] = m.Members[0].Email
			case "foreign-department":
				m.Departments[0].ExistingID = "dept_foreign"
			case "disabled-department":
				db.Exec(`UPDATE departments SET status='inactive' WHERE id='dept_old'`)
			case "code-collision":
				db.Exec(`UPDATE departments SET code='BOOT-frontend' WHERE id='dept_old'`)
			case "sibling-name":
				m.Departments[1].ParentKey = ""
				m.Departments[1].Name = "研发中心"
			case "existing-cycle":
				db.Exec(`UPDATE departments SET parent_id=id WHERE id='dept_old'`)
				m.Departments = nil
				for i := range m.Members {
					m.Members[i].DepartmentKeys = nil
					m.Members[i].PrimaryDepartmentKey = ""
				}
			case "audit-failure":
				db.Exec(`CREATE TRIGGER deny_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT,'hidden SQL failure'); END`)
			case "member-write-failure":
				db.Exec(`CREATE TRIGGER deny_member BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT,'hidden SQL failure'); END`)
			case "missing-schema":
				db.Exec(`ALTER TABLE users RENAME COLUMN password_hash TO unsupported_hash`)
			}
			before := snapshotTables(t, db, bootstrapTables...)
			_, err := runManifest(t, o, m)
			if err == nil {
				t.Fatal("unsafe import accepted")
			}
			if strings.Contains(err.Error(), "hidden SQL failure") {
				t.Fatal("SQL details leaked")
			}
			if snapshotTables(t, db, bootstrapTables...) != before {
				t.Fatal("failed import partially changed data")
			}
		})
	}
}
func TestBootstrapExistingFlagsAndUnlistedAdminAreNeverReset(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	db.Exec(`UPDATE users SET active=0,operation_disabled=1 WHERE id='u_bob'`)
	db.Exec(`UPDATE tenant_memberships SET status='disabled',role='tenant_admin' WHERE user_id='u_bob'`)
	o.Apply = true
	p, err := runManifest(t, o, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 1 {
		t.Fatal("existing unlisted admin preservation should be visible")
	}
	if scalar(t, db, `SELECT active||'|'||operation_disabled||'|'||password_hash FROM users WHERE id='u_bob'`) != "0|1|existing-bob-hash" {
		t.Fatal("existing activation/operation/password reset")
	}
	if scalar(t, db, `SELECT role||'|'||status FROM tenant_memberships WHERE user_id='u_bob'`) != "tenant_admin|disabled" {
		t.Fatal("existing membership changed unexpectedly")
	}
}
func TestBootstrapManifestValidationAndDatabasePaths(t *testing.T) {
	db, o, m := bootstrapFixture(t)
	_ = db
	for _, scenario := range []string{"version", "third-admin", "unknown-admin", "same-admin", "duplicate-name", "duplicate-email", "unknown-role", "unauthorized-project-admin", "department-key", "case-key", "cycle", "missing-parent", "missing-primary", "duplicate-department", "unknown-field", "duplicate-json-key", "trailing-json", "too-large", "non-utf8"} {
		t.Run(scenario, func(t *testing.T) {
			raw, _ := json.Marshal(m)
			var b bootstrapManifest
			json.Unmarshal(raw, &b)
			switch scenario {
			case "version":
				b.Version = 2
			case "third-admin":
				b.Administrators = append(b.Administrators, "third@example.test")
			case "unknown-admin":
				b.Administrators[1] = "unknown@example.test"
			case "same-admin":
				b.Administrators[1] = b.Administrators[0]
			case "duplicate-name":
				b.Members[1].Name = b.Members[0].Name
			case "duplicate-email":
				b.Members[1].Email = strings.ToUpper(b.Members[0].Email)
			case "unknown-role":
				b.Members[1].ProjectRole = "billing_admin"
			case "unauthorized-project-admin":
				b.Members[2].ProjectRole = "project_admin"
			case "department-key":
				b.Departments[1].Key = "../../bad"
			case "case-key":
				b.Departments[1].Key = "ROOT"
			case "cycle":
				b.Departments[0].ParentKey = "frontend"
			case "missing-parent":
				b.Departments[1].ParentKey = "missing"
			case "missing-primary":
				b.Members[0].PrimaryDepartmentKey = "frontend"
			case "duplicate-department":
				b.Members[0].DepartmentKeys = []string{"root", "root"}
			}
			raw, _ = json.Marshal(b)
			switch scenario {
			case "unknown-field":
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"password":"never allowed"}`)
			case "duplicate-json-key":
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"version":1}`)
			case "trailing-json":
				raw = append(raw, []byte(` {}`)...)
			case "too-large":
				raw = []byte(strings.Repeat(" ", manifestLimit+1))
			case "non-utf8":
				raw = []byte{0xff}
			}
			if _, err := decodeBootstrap(strings.NewReader(string(raw))); err == nil {
				t.Fatal("invalid reviewed manifest accepted")
			}
		})
	}
	for _, path := range []string{"relative.db", filepath.Join(t.TempDir(), "missing.db")} {
		bad := o
		bad.DB = path
		if _, err := runManifest(t, bad, m); err == nil {
			t.Fatal("missing/relative database accepted")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("tool created a database")
		}
	}
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(o.DB, link); err != nil {
		t.Fatal(err)
	}
	o.DB = link
	if _, err := runManifest(t, o, m); err == nil {
		t.Fatal("symlink target accepted")
	}
}
