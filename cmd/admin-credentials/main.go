// admin-credentials performs one explicitly addressed administrative credential
// change. It never starts the server, creates a database, migrates, or seeds data.
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
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type credentialOptions struct{ DB, Tenant, User, ExpectedEmail, Email string }

// 离线运维工具不是公开找回接口；只接受两行匹配的安全非交互 stdin，禁止在命令参数、环境变量或回显终端中输入密码。
func main() {
	var o credentialOptions
	flag.StringVar(&o.DB, "db", "", "absolute path of an existing TaskLoom database")
	flag.StringVar(&o.Tenant, "tenant", "", "exact existing tenant ID")
	flag.StringVar(&o.User, "user", "", "exact active tenant administrator ID")
	flag.StringVar(&o.ExpectedEmail, "expected-email", "", "current email; abort if it changed")
	flag.StringVar(&o.Email, "email", "", "new login email")
	flag.Parse()
	// Never read a password from argv, environment, or an echo-enabled terminal.
	// Feed exactly two matching lines through a secure non-interactive stdin.
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintln(os.Stderr, "Refusing terminal input. Provide two matching password lines through secure non-interactive stdin.")
		os.Exit(1)
	}
	password, err := readCredentialPassword(os.Stdin)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = changeCredentials(ctx, o, password, bcrypt.DefaultCost+2)
		cancel()
	}
	clear(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Credential update failed:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "Administrator credentials updated; the target account's sessions were revoked.")
}

func readCredentialPassword(input io.Reader) ([]byte, error) {
	s := bufio.NewScanner(io.LimitReader(input, 1025))
	s.Buffer(make([]byte, 128), 1025)
	lines := [][]byte{}
	defer func() {
		for _, line := range lines {
			clear(line)
		}
	}()
	for s.Scan() {
		if len(lines) >= 2 {
			return nil, errors.New("stdin must contain exactly two matching password lines")
		}
		lines = append(lines, append([]byte{}, s.Bytes()...))
	}
	if s.Err() != nil || len(lines) != 2 || subtle.ConstantTimeCompare(lines[0], lines[1]) != 1 {
		return nil, errors.New("stdin must contain exactly two matching password lines")
	}
	p := lines[0]
	letter, digit := false, false
	for _, r := range string(p) {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsNumber(r)
		if unicode.IsControl(r) {
			return nil, errors.New("password contains a control character")
		}
	}
	if utf8.RuneCount(p) < 8 || len(p) > 72 || !utf8.Valid(p) || !letter || !digit {
		return nil, errors.New("password must contain letters and numbers, at least 8 characters, and at most 72 UTF-8 bytes")
	}
	return append([]byte{}, p...), nil
}

func validCredentialEmail(raw string) bool {
	value, err := mail.ParseAddress(raw)
	return err == nil && value.Address == raw && len(raw) <= 254 && strings.Contains(raw, "@") && !strings.ContainsAny(raw, "\r\n\x00 ")
}

// 必须显式指定现有库、企业、用户及预期旧邮箱，并确认目标仍为有效管理员；只改此账号，不建库、不迁移、不种子初始化。
func changeCredentials(ctx context.Context, o credentialOptions, password []byte, cost int) error {
	if o.Tenant == "" || o.User == "" || o.ExpectedEmail == "" || o.Email == "" || !filepath.IsAbs(o.DB) || !validCredentialEmail(o.ExpectedEmail) || !validCredentialEmail(o.Email) {
		return errors.New("explicit database, tenant, user, expected email and valid new email are required")
	}
	info, err := os.Lstat(o.DB)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("database must be an existing non-empty regular file, not a symbolic link")
	}
	// Validate in the helper too, so callers cannot bypass stdin policy checks.
	validated, err := readCredentialPassword(strings.NewReader(string(password) + "\n" + string(password) + "\n"))
	if err != nil {
		return err
	}
	clear(validated)
	hash, err := bcrypt.GenerateFromPassword(password, cost)
	if err != nil {
		return errors.New("could not hash the password")
	}
	defer clear(hash)
	params := url.Values{"mode": []string{"rw"}, "_pragma": []string{"busy_timeout(5000)", "foreign_keys(ON)", "synchronous(FULL)"}, "_txlock": []string{"immediate"}}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(o.DB), RawQuery: params.Encode()}).String())
	if err != nil {
		return errors.New("could not open database")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("could not reserve database writer")
	}
	defer tx.Rollback()
	var disabledColumn int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('users') WHERE name='operation_disabled'`).Scan(&disabledColumn); err != nil {
		return errors.New("could not inspect account schema")
	}
	query := `SELECT u.email,u.active,tm.role,tm.status FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=?`
	if disabledColumn > 0 {
		query += ` AND u.operation_disabled=0`
	}
	var email, role, status string
	var active bool
	if err = tx.QueryRowContext(ctx, query, o.Tenant, o.User).Scan(&email, &active, &role, &status); err != nil || email != o.ExpectedEmail || !active || role != "tenant_admin" || status != "active" {
		return errors.New("target is not the expected active administrator; no changes made")
	}
	var duplicate int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE tenant_id=? AND id!=? AND lower(email)=lower(?)`, o.Tenant, o.User, o.Email).Scan(&duplicate); err != nil || duplicate != 0 {
		return errors.New("new email conflicts with another account or cannot be verified")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `UPDATE users SET email=?,password_hash=?,password_changed_at=? WHERE tenant_id=? AND id=? AND email=? AND active=1`, strings.ToLower(o.Email), string(hash), now, o.Tenant, o.User, o.ExpectedEmail)
	if err != nil {
		return errors.New("could not update the target account")
	}
	affected, err := res.RowsAffected()
	if err != nil || affected != 1 {
		return errors.New("target account changed; no changes made")
	}
	// 凭据变更、原账号会话/代访问撤销及脱敏审计一起提交；任一失败全部回滚，不能跳过审计“强制成功”。
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND revoked_at IS NULL`, now, o.Tenant, o.User); err != nil {
		return errors.New("could not revoke target sessions")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_impersonations SET ended_at=? WHERE tenant_id=? AND (admin_user_id=? OR target_user_id=?) AND ended_at IS NULL`, now, o.Tenant, o.User, o.User); err != nil {
		return errors.New("could not revoke target impersonations")
	}
	before, _ := json.Marshal(map[string]any{"email": email})
	after, _ := json.Marshal(map[string]any{"email": strings.ToLower(o.Email), "passwordChanged": true, "sessionsRevoked": true})
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,'','admin-credentials-cli','user',?,'admin.credentials_changed',?,?,?)`, o.Tenant, o.User, string(before), string(after), now); err != nil {
		return errors.New("could not record credential audit")
	}
	if err = tx.Commit(); err != nil {
		return errors.New("could not commit credentials transaction")
	}
	return nil
}
