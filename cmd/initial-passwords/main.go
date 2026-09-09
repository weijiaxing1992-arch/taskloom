// initial-passwords 是显式批次的离线凭据初始化工具：默认只读预检，
// 不启动服务、不创建/迁移数据库、不导入账号，也不发送通知。
package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const initialPasswordCost = 12

type initialOptions struct {
	DB, Tenant, BatchID string
	Apply               bool
}
type initialUser struct {
	ID                 string `json:"id"`
	Active             bool   `json:"active"`
	OperationDisabled  bool   `json:"operationDisabled"`
	MustChangePassword bool   `json:"mustChangePassword"`
}
type initialTarget struct {
	User            initialUser
	Hash, ChangedAt string // 只在内存中用于并发比较，绝不作为报告/审计内容。
}
type initialBatch struct {
	TargetIDs []string
	AppliedAt string
	Count     int
}
type initialReport struct {
	Mode             string        `json:"mode"`
	TenantID         string        `json:"tenantId"`
	BatchID          string        `json:"batchId,omitempty"`
	AlreadyApplied   bool          `json:"alreadyApplied"`
	AppliedAt        string        `json:"appliedAt,omitempty"`
	TargetCount      int           `json:"targetCount"`
	CurrentUserCount int           `json:"currentUserCount"`
	TargetIDs        []string      `json:"targetIds"`
	Users            []initialUser `json:"users"`
	PlannedChanges   int           `json:"plannedChanges"`
	Changes          int           `json:"changes"`
}
type initialReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func main() {
	var o initialOptions
	flag.StringVar(&o.DB, "db", "", "absolute path to an existing migrated TaskLoom SQLite database")
	flag.StringVar(&o.Tenant, "tenant", "", "exact existing tenant ID")
	flag.StringVar(&o.BatchID, "batch-id", "", "stable unique operation batch ID (required with --apply)")
	flag.BoolVar(&o.Apply, "apply", false, "explicitly initialize all tenant passwords; default is read-only preview")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Unexpected positional arguments")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	report, err := runInitialPasswords(ctx, o, os.Stdin, initialPasswordCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Initial password operation rejected:", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot output operation result; inspect this batch ID before retrying")
		os.Exit(1)
	}
}

var initialBatchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)

func validateInitialOptions(o initialOptions) error {
	if !filepath.IsAbs(o.DB) || o.Tenant == "" || strings.TrimSpace(o.Tenant) != o.Tenant || !utf8.ValidString(o.Tenant) || len(o.Tenant) > 128 || strings.IndexFunc(o.Tenant, unicode.IsControl) >= 0 {
		return errors.New("explicit absolute database path and exact tenant ID are required")
	}
	if (o.Apply && o.BatchID == "") || (o.BatchID != "" && !initialBatchPattern.MatchString(o.BatchID)) {
		return errors.New("batch ID must be 1-80 ASCII letters, digits, periods, underscores or hyphens")
	}
	info, err := os.Lstat(o.DB)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("database must be an existing non-empty regular file, not a symbolic link")
	}
	return nil
}

func openInitialDatabase(o initialOptions) (*sql.DB, error) {
	if err := validateInitialOptions(o); err != nil {
		return nil, err
	}
	mode := "ro"
	pragmas := []string{"busy_timeout(5000)", "query_only(ON)"}
	if o.Apply {
		mode = "rw"
		pragmas = []string{"busy_timeout(5000)", "foreign_keys(ON)", "synchronous(FULL)"}
	}
	params := url.Values{"mode": {mode}, "_pragma": pragmas, "_txlock": {"immediate"}}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(o.DB), RawQuery: params.Encode()}).String())
	if err != nil {
		return nil, errors.New("could not open existing database")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// 先验证服务端已部署所需 schema，缺表/缺列即失败；CLI 不“顺便修库”。
func inspectInitialSchema(ctx context.Context, q initialReader) error {
	required := []struct {
		table   string
		columns []string
	}{
		{"tenants", []string{"id"}},
		{"users", []string{"id", "tenant_id", "active", "operation_disabled", "password_hash", "password_changed_at", "must_change_password"}},
		{"auth_sessions", []string{"tenant_id", "user_id", "revoked_at"}},
		{"auth_impersonations", []string{"tenant_id", "admin_user_id", "target_user_id", "ended_at"}},
		{"audit_logs", []string{"tenant_id", "project_id", "actor_id", "object_type", "object_id", "action", "before_json", "after_json", "created_at"}},
		{"password_initialization_batches", []string{"tenant_id", "batch_id", "applied_at", "target_ids_json", "target_count"}},
	}
	for _, table := range required {
		rows, err := q.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table.table)
		if err != nil {
			return errors.New("could not inspect required schema")
		}
		columns := map[string]bool{}
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return errors.New("could not inspect required schema")
			}
			columns[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return errors.New("could not inspect required schema")
		}
		for _, column := range table.columns {
			if !columns[column] {
				return fmt.Errorf("required schema missing: %s.%s; deploy server migrations first", table.table, column)
			}
		}
	}
	return nil
}

func initialTargets(ctx context.Context, q initialReader, tenant string) ([]initialTarget, error) {
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE id=?`, tenant).Scan(&exists); err != nil || exists != 1 {
		return nil, errors.New("exact tenant does not exist or cannot be verified")
	}
	rows, err := q.QueryContext(ctx, `SELECT id,active,operation_disabled,must_change_password,password_hash,password_changed_at FROM users WHERE tenant_id=? ORDER BY id`, tenant)
	if err != nil {
		return nil, errors.New("could not read tenant account snapshot")
	}
	defer rows.Close()
	targets := []initialTarget{}
	for rows.Next() {
		var target initialTarget
		if err = rows.Scan(&target.User.ID, &target.User.Active, &target.User.OperationDisabled, &target.User.MustChangePassword, &target.Hash, &target.ChangedAt); err != nil {
			return nil, errors.New("invalid account snapshot")
		}
		targets = append(targets, target)
	}
	if rows.Err() != nil {
		return nil, errors.New("could not complete account snapshot")
	}
	return targets, nil
}

func readInitialBatch(ctx context.Context, q initialReader, tenant, batchID string) (*initialBatch, error) {
	if batchID == "" {
		return nil, nil
	}
	var raw string
	batch := &initialBatch{}
	err := q.QueryRowContext(ctx, `SELECT target_ids_json,target_count,applied_at FROM password_initialization_batches WHERE tenant_id=? AND batch_id=?`, tenant, batchID).Scan(&raw, &batch.Count, &batch.AppliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("could not verify batch state; refusing to reset credentials")
	}
	if json.Unmarshal([]byte(raw), &batch.TargetIDs) != nil || batch.TargetIDs == nil || batch.Count < 0 || len(batch.TargetIDs) != batch.Count || batch.AppliedAt == "" {
		return nil, errors.New("invalid completed batch metadata; refusing to reset credentials")
	}
	seen := map[string]bool{}
	for _, id := range batch.TargetIDs {
		if id == "" || seen[id] {
			return nil, errors.New("invalid completed batch target IDs")
		}
		seen[id] = true
	}
	return batch, nil
}

func initialResult(o initialOptions, targets []initialTarget, batch *initialBatch) initialReport {
	r := initialReport{Mode: "dry-run", TenantID: o.Tenant, BatchID: o.BatchID, TargetCount: len(targets), CurrentUserCount: len(targets), TargetIDs: []string{}, Users: []initialUser{}, PlannedChanges: len(targets)}
	if o.Apply {
		r.Mode = "apply"
	}
	for _, target := range targets {
		r.TargetIDs = append(r.TargetIDs, target.User.ID)
		r.Users = append(r.Users, target.User)
	}
	if batch != nil {
		r.AlreadyApplied = true
		r.AppliedAt = batch.AppliedAt
		r.TargetCount = batch.Count
		r.TargetIDs = append([]string{}, batch.TargetIDs...)
		r.PlannedChanges = 0
	}
	return r
}

// 临时密码采用独立策略：6–72 UTF-8 字节、不含空白/控制/不可见格式字符。
// 不复用正常改密的强度规则；强制首次改密标记才是此操作的必要配套。
func validateInitialPassword(password []byte) error {
	if len(password) < 6 || len(password) > 72 || !utf8.Valid(password) {
		return errors.New("temporary password must contain 6-72 UTF-8 bytes")
	}
	for _, r := range string(password) {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("temporary password cannot contain whitespace or control characters")
		}
	}
	return nil
}
func readInitialPassword(input io.Reader) ([]byte, error) {
	if input == nil {
		return nil, errors.New("secure non-interactive stdin is required")
	}
	if file, ok := input.(*os.File); ok {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice != 0 {
			return nil, errors.New("refusing terminal input; use secure non-interactive stdin with two matching lines")
		}
	}
	scanner := bufio.NewScanner(io.LimitReader(input, 1025))
	scanner.Buffer(make([]byte, 128), 1025)
	lines := [][]byte{}
	defer func() {
		for _, line := range lines {
			clear(line)
		}
	}()
	for scanner.Scan() {
		if len(lines) >= 2 {
			return nil, errors.New("stdin must contain exactly two matching password lines")
		}
		lines = append(lines, append([]byte{}, scanner.Bytes()...))
	}
	if scanner.Err() != nil || len(lines) != 2 || subtle.ConstantTimeCompare(lines[0], lines[1]) != 1 {
		return nil, errors.New("stdin must contain exactly two matching password lines")
	}
	if err := validateInitialPassword(lines[0]); err != nil {
		return nil, err
	}
	return append([]byte{}, lines[0]...), nil
}

func runInitialPasswords(ctx context.Context, o initialOptions, input io.Reader, cost int) (initialReport, error) {
	db, err := openInitialDatabase(o)
	if err != nil {
		return initialReport{}, err
	}
	defer db.Close()
	if err := inspectInitialSchema(ctx, db); err != nil {
		return initialReport{}, err
	}
	targets, err := initialTargets(ctx, db, o.Tenant)
	if err != nil {
		return initialReport{}, err
	}
	batch, err := readInitialBatch(ctx, db, o.Tenant, o.BatchID)
	if err != nil {
		return initialReport{}, err
	}
	report := initialResult(o, targets, batch)
	// 已完成批次优先于密码输入：以后同批重跑不会碰用户自行更改的新密码。
	if !o.Apply || batch != nil {
		return report, nil
	}
	password, err := readInitialPassword(input)
	if err != nil {
		return initialReport{}, err
	}
	defer clear(password)
	if cost < bcrypt.MinCost || cost > initialPasswordCost {
		return initialReport{}, errors.New("invalid password hashing cost")
	}
	hashes := make([][]byte, len(targets))
	defer func() {
		for _, hash := range hashes {
			clear(hash)
		}
	}()
	// 逐人独立盐。耗时 bcrypt 在写锁外完成，避免把整批计算时间变成数据库锁等待。
	for i := range targets {
		if ctx.Err() != nil {
			return initialReport{}, errors.New("operation cancelled before writing")
		}
		hashes[i], err = bcrypt.GenerateFromPassword(password, cost)
		if err != nil {
			return initialReport{}, errors.New("could not hash temporary password")
		}
	}
	return applyInitialPasswords(ctx, db, o, targets, hashes)
}

func applyInitialPasswords(ctx context.Context, db *sql.DB, o initialOptions, expected []initialTarget, hashes [][]byte) (initialReport, error) {
	if !o.Apply || o.BatchID == "" || len(expected) != len(hashes) {
		return initialReport{}, errors.New("invalid explicit batch write")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return initialReport{}, errors.New("could not reserve database writer")
	}
	defer tx.Rollback()
	// BEGIN IMMEDIATE 后重读批次与全部目标。新账号、并发改密或停用状态
	// 改变均使本次快照失效；同一批被另一进程先完成则返回已完成，不覆盖。
	batch, err := readInitialBatch(ctx, tx, o.Tenant, o.BatchID)
	if err != nil {
		return initialReport{}, err
	}
	current, err := initialTargets(ctx, tx, o.Tenant)
	if err != nil {
		return initialReport{}, err
	}
	if batch != nil {
		return initialResult(o, current, batch), nil
	}
	if !reflect.DeepEqual(expected, current) {
		return initialReport{}, errors.New("tenant account snapshot changed; no passwords were changed; preview again")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, target := range current {
		if _, err := bcrypt.Cost(hashes[i]); err != nil {
			return initialReport{}, errors.New("invalid prepared password hash")
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_changed_at=?,must_change_password=1 WHERE tenant_id=? AND id=?`, string(hashes[i]), now, o.Tenant, target.User.ID)
		if err != nil {
			return initialReport{}, errors.New("could not initialize account password")
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return initialReport{}, errors.New("account changed; batch rolled back")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND revoked_at IS NULL`, now, o.Tenant, target.User.ID); err != nil {
			return initialReport{}, errors.New("could not revoke account sessions")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (admin_user_id=? OR target_user_id=?) AND ended_at IS NULL`, now, o.Tenant, target.User.ID, target.User.ID); err != nil {
			return initialReport{}, errors.New("could not revoke related impersonations")
		}
		before, _ := json.Marshal(target.User)
		after, _ := json.Marshal(map[string]any{"batchId": o.BatchID, "passwordChanged": true, "mustChangePassword": true, "sessionsRevoked": true})
		if _, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,'','initial-passwords-cli','user',?,'user.initial_password_reset',?,?,?)`, o.Tenant, target.User.ID, string(before), string(after), now); err != nil {
			return initialReport{}, errors.New("could not record account audit; batch rolled back")
		}
	}
	report := initialResult(o, current, nil)
	rawIDs, _ := json.Marshal(report.TargetIDs)
	if _, err = tx.ExecContext(ctx, `INSERT INTO password_initialization_batches(tenant_id,batch_id,applied_at,target_ids_json,target_count)VALUES(?,?,?,?,?)`, o.Tenant, o.BatchID, now, string(rawIDs), len(current)); err != nil {
		return initialReport{}, errors.New("could not record completed batch; batch rolled back")
	}
	if err := tx.Commit(); err != nil {
		return initialReport{}, errors.New("could not confirm batch commit; inspect this batch ID before retrying")
	}
	report.Changes = len(current)
	report.AppliedAt = now
	for i := range report.Users {
		report.Users[i].MustChangePassword = true
	}
	return report, nil
}
