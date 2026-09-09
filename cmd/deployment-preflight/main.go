// deployment-preflight audits a migrated database without starting TaskLoom.
// Its optional provisioning mode is restricted to an explicitly distinct,
// offline deployment copy and never migrates or seeds a database.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
	_ "modernc.org/sqlite"
)

const knownDevelopmentPassword = "TaskLoom2026!"
const deploymentTenant = "tn_acme"

type options struct {
	db, web, source, credentials, admin, backup string
	provision, offline, checkEnv                bool
}
type report struct {
	SQLiteVersion     string `json:"sqliteVersion"`
	ActiveAccounts    int    `json:"activeAccounts"`
	ActiveAdmins      int    `json:"activeAdmins"`
	DefaultPasswords  int    `json:"activeDefaultPasswords"`
	BlankPasswords    int    `json:"activeBlankPasswords"`
	UnsupportedHashes int    `json:"activeUnsupportedPasswordHashes"`
	RotatedAccounts   int    `json:"rotatedAccounts,omitempty"`
	Provisioned       bool   `json:"provisioned"`
	Ready             bool   `json:"ready"`
	BackedUp          bool   `json:"backedUp,omitempty"`
	BackupPath        string `json:"backupPath,omitempty"`
}

func main() {
	var o options
	flag.StringVar(&o.db, "db", os.Getenv("DEVFLOW_DB"), "existing migrated database; never created")
	flag.StringVar(&o.web, "web-dir", os.Getenv("DEVFLOW_WEB_DIR"), "optional built web directory to verify")
	flag.BoolVar(&o.checkEnv, "check-env", false, "require production loopback, Secure cookie and non-example signing secret")
	flag.BoolVar(&o.provision, "provision-copy", false, "rotate development credentials on an offline deployment copy")
	flag.BoolVar(&o.offline, "confirm-offline-copy", false, "confirm no application is using the deployment copy")
	flag.StringVar(&o.source, "source-db", "", "protected original database path; required for provisioning")
	flag.StringVar(&o.credentials, "credentials-out", "", "new private JSON file for administrator credentials; required for provisioning")
	flag.StringVar(&o.admin, "admin-id", "u_admin", "existing active enterprise administrator to initialize")
	flag.StringVar(&o.backup, "backup-out", "", "new absolute private output for an online backup using the embedded SQLite engine")
	flag.Parse()
	r, err := run(context.Background(), o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Deployment preflight failed:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	if !r.Ready {
		os.Exit(2)
	}
}

func run(ctx context.Context, o options) (report, error) {
	var result report
	if o.backup != "" {
		if o.provision || o.offline || o.source != "" || o.credentials != "" {
			return result, errors.New("backup and offline credential provisioning must be separate operations")
		}
		return backupDatabase(ctx, o)
	}
	if o.checkEnv {
		if err := productionEnvironment(); err != nil {
			return result, err
		}
	}
	abs, info, err := existingDatabase(o.db)
	if err != nil {
		return result, err
	}
	if o.web != "" {
		if err := checkWeb(o.web); err != nil {
			return result, err
		}
	}
	if o.provision {
		if !o.offline || o.source == "" || o.credentials == "" {
			return result, errors.New("provisioning requires --confirm-offline-copy, --source-db and --credentials-out")
		}
		source, original, err := existingDatabase(o.source)
		if err != nil {
			return result, errors.New("the protected source database must exist")
		}
		if source == abs || os.SameFile(info, original) {
			return result, errors.New("refusing to provision the original database or one of its links")
		}
		if !filepath.IsAbs(o.credentials) {
			return result, errors.New("the credentials output must be an absolute private path")
		}
		if o.web != "" {
			if rel, err := filepath.Rel(o.web, o.credentials); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return result, errors.New("credentials must not be written inside the web directory")
			}
		}
	}
	params := url.Values{"mode": []string{"ro"}, "_pragma": []string{"busy_timeout(5000)", "query_only(ON)"}}
	if o.provision {
		params = url.Values{"mode": []string{"rw"}, "_pragma": []string{"busy_timeout(5000)", "foreign_keys(ON)"}, "_txlock": []string{"exclusive"}}
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: params.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return result, errors.New("unable to open the existing database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return result, errors.New("unable to read the existing database")
	}
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&result.SQLiteVersion); err != nil {
		return result, errors.New("unable to identify the embedded SQLite version")
	}
	if !walFixVersion(result.SQLiteVersion) {
		return result, errors.New("embedded SQLite is missing the WAL-reset fix; upgrade before production")
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return result, errors.New("database integrity_check failed")
	}
	for _, table := range []string{"tenants", "projects", "users", "tenant_memberships", "memberships", "project_members", "auth_sessions", "auth_impersonations", "audit_logs", "requirements", "sprints", "defects", "test_cases"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			return result, errors.New("the database is not a fully migrated TaskLoom database")
		}
	}
	var tenant, project int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id=?`, deploymentTenant).Scan(&tenant); err != nil || tenant != 1 {
		return result, errors.New("the expected organization is missing")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE tenant_id=? AND id='prj_orbit'`, deploymentTenant).Scan(&project); err != nil || project != 1 {
		return result, errors.New("the default project is missing")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return result, errors.New("unable to check foreign keys")
	}
	hasViolations := rows.Next()
	err = rows.Err()
	rows.Close()
	if hasViolations || err != nil {
		return result, errors.New("database foreign_key_check failed")
	}
	if o.provision {
		result.RotatedAccounts, err = provisionCopy(ctx, db, o)
		if err != nil {
			return result, err
		}
		result.Provisioned = true
	}
	rows, err = db.QueryContext(ctx, `SELECT u.password_hash,tm.role FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.active=1 AND tm.status='active'`, deploymentTenant)
	if err != nil {
		return result, errors.New("unable to audit active accounts")
	}
	type credential struct{ hash, role string }
	accounts := []credential{}
	for rows.Next() {
		var c credential
		if err := rows.Scan(&c.hash, &c.role); err != nil {
			rows.Close()
			return result, errors.New("unable to audit active accounts")
		}
		accounts = append(accounts, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, errors.New("unable to audit active accounts")
	}
	for _, c := range accounts {
		result.ActiveAccounts++
		if c.role == "tenant_admin" {
			result.ActiveAdmins++
		}
		if c.hash == "" {
			result.BlankPasswords++
			continue
		}
		matches, err := matchesDevelopmentPassword(c.hash)
		if err != nil {
			result.UnsupportedHashes++
			continue
		}
		if matches {
			result.DefaultPasswords++
		}
	}
	result.Ready = result.ActiveAdmins > 0 && result.DefaultPasswords == 0 && result.BlankPasswords == 0 && result.UnsupportedHashes == 0
	return result, nil
}

func existingDatabase(path string) (string, os.FileInfo, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", nil, errors.New("provide an absolute path to an existing database")
	}
	abs, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, errors.New("database does not exist; refusing automatic seed initialization")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", nil, errors.New("database must be an existing non-empty regular file")
	}
	return abs, info, nil
}
func checkWeb(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("the built web directory must be absolute")
	}
	info, err := os.Stat(filepath.Join(path, "index.html"))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("built web/index.html is missing")
	}
	info, err = os.Stat(filepath.Join(path, "assets"))
	if err != nil || !info.IsDir() {
		return errors.New("built web/assets directory is missing")
	}
	return nil
}
func productionEnvironment() error {
	if os.Getenv("DEVFLOW_SQLITE_SYNCHRONOUS") != "FULL" {
		return errors.New("DEVFLOW_SQLITE_SYNCHRONOUS must be FULL for production durability")
	}
	secret := os.Getenv("DEVFLOW_SESSION_SECRET")
	if len(secret) < 32 || strings.Contains(secret, "replace-with") || strings.Contains(secret, "change-in-production") || strings.Contains(strings.ToLower(secret), "example") {
		return errors.New("configure a unique random session secret of at least 32 bytes")
	}
	if os.Getenv("DEVFLOW_COOKIE_SECURE") != "true" {
		return errors.New("DEVFLOW_COOKIE_SECURE must be true behind the HTTPS proxy")
	}
	host, port, err := net.SplitHostPort(os.Getenv("DEVFLOW_ADDR"))
	ip := net.ParseIP(host)
	n, numberErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || numberErr != nil || n < 1024 || n > 65535 {
		return errors.New("DEVFLOW_ADDR must use a loopback IP and an unprivileged port")
	}
	return nil
}
func walFixVersion(version string) bool {
	var major, minor, patch int
	if n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); n != 3 {
		return false
	}
	return major > 3 || major == 3 && (minor > 51 || minor == 51 && patch >= 3 || minor == 50 && patch >= 7 || minor == 44 && patch >= 6)
}
func matchesDevelopmentPassword(hash string) (bool, error) {
	if strings.HasPrefix(hash, "$2") {
		cost, err := bcrypt.Cost([]byte(hash))
		if err != nil || cost > 16 {
			return false, errors.New("unsupported password hash")
		}
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(knownDevelopmentPassword))
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return false, nil
		}
		return err == nil, err
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false, errors.New("unsupported password hash")
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 1000000 {
		return false, errors.New("unsupported password hash")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) > 1024 {
		return false, errors.New("unsupported password hash")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) < 1 || len(expected) > 128 {
		return false, errors.New("unsupported password hash")
	}
	actual := pbkdf2.Key([]byte(knownDevelopmentPassword), salt, iterations, len(expected), sha256.New)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
func randomPassword() (string, error) {
	var data [32]byte
	_, err := rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:]), err
}

func provisionCopy(ctx context.Context, db *sql.DB, o options) (count int, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("unable to exclusively begin offline provisioning")
	}
	defer tx.Rollback()
	var email string
	if err := tx.QueryRowContext(ctx, `SELECT u.email FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active' AND tm.role='tenant_admin'`, deploymentTenant, o.admin).Scan(&email); err != nil {
		return 0, errors.New("the selected administrator is not an existing active enterprise administrator")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,password_hash FROM users WHERE tenant_id=?`, deploymentTenant)
	if err != nil {
		return 0, errors.New("unable to read existing credentials")
	}
	type candidate struct{ id, hash string }
	users := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			rows.Close()
			return 0, errors.New("unable to read existing credentials")
		}
		users = append(users, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, errors.New("unable to read existing credentials")
	}
	adminPassword, err := randomPassword()
	if err != nil {
		return 0, errors.New("secure randomness unavailable")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, u := range users {
		match := false
		if u.hash != "" {
			match, err = matchesDevelopmentPassword(u.hash)
			if err != nil {
				return 0, errors.New("unsupported existing password hash; provision nothing until reviewed")
			}
		}
		if !match && u.id != o.admin {
			continue
		}
		password := adminPassword
		if u.id != o.admin {
			password, err = randomPassword()
			if err != nil {
				return 0, errors.New("secure randomness unavailable")
			}
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return 0, errors.New("password hashing failed")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_changed_at=? WHERE tenant_id=? AND id=?`, string(hash), now, deploymentTenant, u.id); err != nil {
			return 0, errors.New("unable to initialize credentials")
		}
		count++
	}
	for _, query := range []string{`UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND revoked_at IS NULL`, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND ended_at IS NULL`} {
		if _, err = tx.ExecContext(ctx, query, now, deploymentTenant); err != nil {
			return 0, errors.New("unable to revoke copied sessions")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,NULL,'system:deployment','deployment','offline-copy','production.provisioned','{}',?,?)`, deploymentTenant, fmt.Sprintf(`{"rotatedAccounts":%d,"copiedSessionsRevoked":true}`, count), now); err != nil {
		return 0, errors.New("unable to record provisioning audit")
	}
	// O_EXCL refuses existing files/symlinks. Secret contents never reach logs or
	// stdout. Abort the SQL transaction if the private file cannot be persisted.
	file, err := os.OpenFile(o.credentials, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, errors.New("credentials output must be a new writable private file")
	}
	committed := false
	defer func() {
		file.Close()
		if !committed {
			_ = os.Remove(o.credentials)
		}
	}()
	if err = json.NewEncoder(file).Encode(map[string]string{"adminId": o.admin, "adminEmail": email, "adminPassword": adminPassword, "createdAt": now, "notice": "Private initial administrator credentials. Other development passwords were replaced without changing membership. Verify successful provisioning before using this file."}); err != nil {
		return 0, errors.New("unable to write the private credentials file")
	}
	if err = file.Sync(); err != nil {
		return 0, errors.New("unable to sync the private credentials file")
	}
	if err = file.Close(); err != nil {
		return 0, errors.New("unable to close the private credentials file")
	}
	if err = tx.Commit(); err != nil {
		return 0, errors.New("unable to commit offline provisioning")
	}
	committed = true
	return count, nil
}
