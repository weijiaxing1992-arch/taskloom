package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var phonePattern = regexp.MustCompile(`^[0-9+()\- ]*$`)

type profileMembership struct {
	ProjectID    string   `json:"projectId"`
	ProjectName  string   `json:"projectName"`
	ProjectCode  string   `json:"projectCode"`
	Role         string   `json:"role"`
	ProjectRoles []string `json:"projectRoles"`
	Status       string   `json:"status"`
}

type profileData struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Email              string              `json:"email"`
	Phone              string              `json:"phone"`
	JobTitle           string              `json:"jobTitle"`
	Bio                string              `json:"bio"`
	AvatarColor        string              `json:"avatarColor"`
	Locale             string              `json:"locale"`
	Timezone           string              `json:"timezone"`
	EmailNotifications bool                `json:"emailNotifications"`
	EmployeeNo         string              `json:"employeeNo"`
	Department         string              `json:"department"`
	TenantRole         string              `json:"tenantRole"`
	CurrentProjectRole string              `json:"currentProjectRole"`
	LastActive         string              `json:"lastActive"`
	Active             bool                `json:"active"`
	PasswordConfigured bool                `json:"passwordConfigured"`
	PasswordChangedAt  string              `json:"passwordChangedAt"`
	Memberships        []profileMembership `json:"memberships"`
}

type profileUpdate struct {
	Name               string `json:"name"`
	Email              string `json:"email"`
	Phone              string `json:"phone"`
	JobTitle           string `json:"jobTitle"`
	Bio                string `json:"bio"`
	AvatarColor        string `json:"avatarColor"`
	Locale             string `json:"locale"`
	Timezone           string `json:"timezone"`
	EmailNotifications *bool  `json:"emailNotifications"`
}

// 只读取当前认证账号资料，密码摘要仅用于判断是否已配置，不进入响应。
func (a *App) getProfile() (profileData, error) {
	var p profileData
	var passwordHash string
	err := a.db.QueryRow(`SELECT u.id,u.name,u.email,u.phone,u.job_title,u.bio,u.avatar_color,u.locale,u.timezone,u.email_notifications,u.employee_no,u.department,u.last_active,u.active,u.password_hash,u.password_changed_at,tm.role,COALESCE(pm.role,tm.role) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.project_id=? AND pm.user_id=u.id WHERE u.tenant_id=? AND u.id=?`, a.pid(), tenantID, a.uid()).Scan(&p.ID, &p.Name, &p.Email, &p.Phone, &p.JobTitle, &p.Bio, &p.AvatarColor, &p.Locale, &p.Timezone, &p.EmailNotifications, &p.EmployeeNo, &p.Department, &p.LastActive, &p.Active, &passwordHash, &p.PasswordChangedAt, &p.TenantRole, &p.CurrentProjectRole)
	if err != nil {
		return p, err
	}
	p.PasswordConfigured = passwordHash != ""
	p.Memberships = []profileMembership{}
	rows, err := a.db.Query(`SELECT p.id,p.name,p.code,pm.role,p.status,`+projectRolesJSONSQL("pm")+` FROM project_members pm JOIN projects p ON p.tenant_id=pm.tenant_id AND p.id=pm.project_id WHERE pm.tenant_id=? AND pm.user_id=? AND p.status='active' ORDER BY p.status,p.name`, tenantID, a.uid())
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var membership profileMembership
		var rawRoles string
		if err = rows.Scan(&membership.ProjectID, &membership.ProjectName, &membership.ProjectCode, &membership.Role, &membership.Status, &rawRoles); err != nil {
			return p, err
		}
		membership.ProjectRoles = decodedProjectRoles(rawRoles, membership.Role)
		p.Memberships = append(p.Memberships, membership)
	}
	return p, rows.Err()
}

func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/profile/password" {
		a.changePassword(w, r)
		return
	}
	if r.Method == http.MethodGet {
		p, err := a.getProfile()
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, 404, "profile_not_found", "个人信息不存在")
			} else {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法读取，请稍后重试")
			}
			return
		}
		write(w, 200, p)
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var update profileUpdate
	if decodeJSON(r, &update) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	update.Name = strings.TrimSpace(update.Name)
	update.Email = strings.ToLower(strings.TrimSpace(update.Email))
	update.Phone = strings.TrimSpace(update.Phone)
	update.JobTitle = strings.TrimSpace(update.JobTitle)
	update.Bio = strings.TrimSpace(update.Bio)
	if update.Name == "" || len([]rune(update.Name)) > 40 {
		fail(w, 422, "validation_error", "姓名不能为空且不能超过 40 个字符")
		return
	}
	address, err := mail.ParseAddress(update.Email)
	if err != nil || address.Address != update.Email || len(update.Email) > 120 {
		fail(w, 422, "validation_error", "请输入有效的邮箱地址")
		return
	}
	if len(update.Phone) > 30 || !phonePattern.MatchString(update.Phone) {
		fail(w, 422, "validation_error", "手机号格式不正确")
		return
	}
	if len([]rune(update.JobTitle)) > 60 || len([]rune(update.Bio)) > 200 {
		fail(w, 422, "validation_error", "职位不能超过 60 个字符，个人简介不能超过 200 个字符")
		return
	}
	if !validChoice(update.AvatarColor, []string{"#665FE8", "#3478F6", "#12A594", "#E17B2D", "#D84C6F", "#7357B8"}) {
		fail(w, 422, "validation_error", "头像主题色无效")
		return
	}
	if !validChoice(update.Locale, []string{"zh-CN", "en-US"}) || !validChoice(update.Timezone, []string{"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Singapore", "UTC"}) {
		fail(w, 422, "validation_error", "语言或时区设置无效")
		return
	}
	if update.EmailNotifications == nil {
		fail(w, 422, "validation_error", "通知偏好不能为空")
		return
	}
	var oldName string
	if err = a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&oldName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "profile_not_found", "个人信息不存在")
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法读取，请稍后重试")
		}
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	// 与员工创建共用邮箱占用规则，并先取得写锁，避免校验后另一请求抢占邮箱。
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法保存，请稍后重试")
		return
	}
	conflict, err := organizationEmailInUse(r.Context(), tx, update.Email, a.uid())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法读取，请稍后重试")
		return
	}
	if conflict {
		fail(w, 409, "email_exists", "该邮箱已被其他成员使用")
		return
	}
	if _, err = tx.Exec(`UPDATE users SET name=?,email=?,phone=?,job_title=?,bio=?,avatar_color=?,locale=?,timezone=?,email_notifications=? WHERE tenant_id=? AND id=?`, update.Name, update.Email, update.Phone, update.JobTitle, update.Bio, update.AvatarColor, update.Locale, update.Timezone, *update.EmailNotifications, tenantID, a.uid()); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if update.Name != oldName {
		for _, statement := range []string{
			`UPDATE requirements SET owner=? WHERE tenant_id=? AND owner_user_id=?`,
			`UPDATE requirements SET assignee=? WHERE tenant_id=? AND assignee_user_id=?`,
			`UPDATE defects SET assignee=? WHERE tenant_id=? AND assignee_user_id=?`,
			`UPDATE defects SET verifier=? WHERE tenant_id=? AND verifier_user_id=?`,
			`UPDATE test_cases SET owner=? WHERE tenant_id=? AND owner_user_id=?`,
			`UPDATE test_executions SET executor=? WHERE tenant_id=? AND executor_user_id=?`,
		} {
			if _, err = tx.Exec(statement, update.Name, tenantID, a.uid()); err != nil {
				fail(w, 500, "db_error", err.Error())
				return
			}
		}
		if _, err = tx.Exec(`UPDATE test_plans SET owner=? WHERE tenant_id=? AND owner=?`, update.Name, tenantID, oldName); err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
	}
	before := jsonText(map[string]any{"name": oldName})
	after := jsonText(map[string]any{"name": update.Name, "email": update.Email, "phone": update.Phone, "jobTitle": update.JobTitle, "avatarColor": update.AvatarColor, "locale": update.Locale, "timezone": update.Timezone, "emailNotifications": *update.EmailNotifications})
	if _, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "user", a.uid(), "profile_updated", before, after, now); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	p, err := a.getProfile()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 200, p)
}

// 这是已登录用户校验当前密码后的改密接口，不是邮件找回或免验证重置；新密码、审计和安全通知在同一事务保存。
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	if err := a.requirePasswordChanged(r.Context(), a.db); err != nil {
		failOrganization(w, err)
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if decodeJSON(r, &body) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if body.NewPassword != body.ConfirmPassword {
		fail(w, 422, "password_mismatch", "两次输入的新密码不一致")
		return
	}
	if err := validatePassword(body.NewPassword); err != nil {
		fail(w, 422, "weak_password", err.Error())
		return
	}
	if configured := os.Getenv("DEVFLOW_INITIAL_PASSWORD"); configured != "" && body.NewPassword == configured {
		fail(w, 422, "password_unchanged", "新密码不能使用系统临时密码")
		return
	}
	var existing string
	if err := a.db.QueryRow(`SELECT password_hash FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "profile_not_found", "个人信息不存在")
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法读取，请稍后重试")
		}
		return
	}
	if existing != "" && !verifyPassword(body.CurrentPassword, existing) {
		fail(w, 422, "current_password_invalid", "当前密码不正确")
		return
	}
	if existing != "" && verifyPassword(body.NewPassword, existing) {
		fail(w, 422, "password_unchanged", "新密码不能与当前密码相同")
		return
	}
	encoded, err := a.encodePassword(body.NewPassword)
	if err != nil {
		fail(w, 500, "password_error", "密码加密失败，请稍后重试")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err = a.commitProfilePasswordChange(r.Context(), existing, encoded, now); err != nil {
		var problem *organizationError
		if errors.As(err, &problem) {
			failOrganization(w, err)
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "个人资料暂时无法读取，请稍后重试")
		}
		return
	}
	write(w, 200, map[string]any{"changed": true, "passwordConfigured": true, "passwordChangedAt": now})
}

func (a *App) commitProfilePasswordChange(ctx context.Context, existing, encoded, now string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 外层认证和 bcrypt 之后可能已被管理员重置/禁用；提交时重新检查业务位、旧摘要和真实会话。
	if err = a.requireOperationAccess(ctx, tx); err != nil {
		return err
	}
	var allowed int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id JOIN auth_sessions s ON s.tenant_id=u.tenant_id AND s.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active' AND u.must_change_password=0 AND u.password_hash=? AND s.token_hash=? AND s.revoked_at IS NULL AND s.expires_at>? AND NOT EXISTS(SELECT 1 FROM auth_impersonations i WHERE i.tenant_id=s.tenant_id AND i.session_hash=s.token_hash AND i.ended_at IS NULL)`, tenantID, a.uid(), existing, a.sessionToken, now).Scan(&allowed); err != nil {
		return err
	}
	if allowed != 1 {
		return &organizationError{409, "credentials_changed", "账号凭据已变化，请重新登录后重试"}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=?,password_changed_at=? WHERE tenant_id=? AND id=?`, encoded, now, tenantID, a.uid()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,?,?,'password_changed','{}','{}',?)`, tenantID, a.pid(), a.uid(), "user", a.uid(), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "", "user.password_changed", "user", 0, "登录密码已更新", "如果这不是你的操作，请立即联系企业管理员。", now, fmt.Sprintf("password-changed:%s:%d", a.uid(), time.Now().UnixNano())); err != nil {
		return err
	}
	// 仅保留在本事务中重新确认的当前会话，其他会话统一撤销。
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND user_id=? AND token_hash<>? AND revoked_at IS NULL`, now, tenantID, a.uid(), a.sessionToken); err != nil {
		return err
	}
	return tx.Commit()
}

func validatePassword(value string) error {
	if len([]rune(value)) < 8 || len(value) > 72 {
		return fmt.Errorf("新密码须为 8–72 个字符")
	}
	var hasLetter, hasNumber bool
	for _, char := range value {
		hasLetter = hasLetter || unicode.IsLetter(char)
		hasNumber = hasNumber || unicode.IsNumber(char)
	}
	if !hasLetter || !hasNumber {
		return fmt.Errorf("新密码须同时包含字母和数字")
	}
	return nil
}

// 兼容读取历史 PBKDF2 摘要；新密码使用 bcrypt，成功登录的旧摘要升级由 auth.go 处理，不向外暴露格式细节。
func verifyPassword(password, encoded string) bool {
	if strings.HasPrefix(encoded, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := derivePasswordKey([]byte(password), salt, iterations, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func derivePasswordKey(password, salt []byte, iterations, length int) []byte {
	result := make([]byte, 0, length)
	for block := uint32(1); len(result) < length; block++ {
		counter := make([]byte, 4)
		binary.BigEndian.PutUint32(counter, block)
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(counter)
		u := mac.Sum(nil)
		t := append([]byte{}, u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:length]
}

var _ statementExecutor = (*sql.Tx)(nil)
