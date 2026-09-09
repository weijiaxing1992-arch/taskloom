package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type organizationImportRow struct {
	Row            int    `json:"row"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	EmployeeNo     string `json:"employeeNo"`
	DepartmentCode string `json:"departmentCode"`
	ProjectCode    string `json:"projectCode"`
	ProjectRole    string `json:"projectRole"`
}
type organizationImportError struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// 仅支持白名单 UTF-8 CSV（1 MiB / 500 人），不接受密码列；active 列用于导出兼容，不赋予导入激活能力。
func parseOrganizationCSV(input string) ([]organizationImportRow, []organizationImportError, error) {
	if len(input) > 1<<20 || !utf8.ValidString(input) {
		return nil, nil, orgInvalid("CSV 必须为 UTF-8 且不超过 1 MiB")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(input, "\ufeff")))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, nil, orgInvalid("CSV 缺少有效表头")
	}
	allowed := map[string]bool{"name": true, "email": true, "employeeNo": true, "departmentCode": true, "projectCode": true, "projectRole": true, "active": true}
	columns := map[string]int{}
	for i, key := range header {
		key = strings.TrimSpace(key)
		if !allowed[key] {
			return nil, nil, orgInvalid("CSV 包含不支持的列")
		}
		if _, exists := columns[key]; exists {
			return nil, nil, orgInvalid("CSV 表头不能重复")
		}
		columns[key] = i
	}
	if _, ok := columns["name"]; !ok {
		return nil, nil, orgInvalid("CSV 必须包含 name 和 email 列")
	}
	if _, ok := columns["email"]; !ok {
		return nil, nil, orgInvalid("CSV 必须包含 name 和 email 列")
	}
	rows := []organizationImportRow{}
	problems := []organizationImportError{}
	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if line > 501 {
			return nil, nil, orgInvalid("CSV 最多允许 500 行成员")
		}
		if err != nil {
			return nil, nil, orgInvalid("CSV 行格式无效")
		}
		if len(record) != len(header) {
			problems = append(problems, organizationImportError{line, "row", "列数与表头不一致"})
			continue
		}
		field := func(key string) string {
			if i, ok := columns[key]; ok {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		role := field("projectRole")
		if role == "" {
			role = "viewer"
		}
		row := organizationImportRow{line, field("name"), field("email"), field("employeeNo"), field("departmentCode"), field("projectCode"), role}
		rows = append(rows, row)
	}
	if len(rows) == 0 && len(problems) == 0 {
		return nil, nil, orgInvalid("CSV 没有可导入的成员")
	}
	return rows, problems, nil
}

// 导入只创建未激活普通成员；项目和部门编码必须在当前企业内解析，不能从职称或姓名推导管理员权限。
func resolveOrganizationImportRow(ctx context.Context, store stateStore, row organizationImportRow) (organizationMemberPatch, []organizationImportError, error) {
	problems := []organizationImportError{}
	add := func(field, message string) {
		problems = append(problems, organizationImportError{row.Row, field, message})
	}
	if !validOrgText(row.Name, 1, 80) {
		add("name", "姓名须为 1–80 字")
	}
	if !validOrgText(row.EmployeeNo, 0, 64) {
		add("employeeNo", "工号长度不能超过 64 字")
	}
	email, err := normalizedOrganizationEmail(row.Email)
	if err != nil {
		add("email", err.Error())
	} else {
		var n int
		if err = store.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE tenant_id=? AND lower(email)=?`, tenantID, email).Scan(&n); err != nil {
			return organizationMemberPatch{}, nil, err
		}
		if n > 0 {
			add("email", "邮箱或成员已存在")
		}
	}
	deps := []string{}
	primary := ""
	if row.DepartmentCode != "" {
		var dep string
		err = store.QueryRowContext(ctx, `SELECT id FROM departments WHERE tenant_id=? AND lower(code)=lower(?) AND status='active'`, tenantID, row.DepartmentCode).Scan(&dep)
		if errors.Is(err, sql.ErrNoRows) {
			add("departmentCode", "部门编码不存在或部门已停用")
		} else if err != nil {
			return organizationMemberPatch{}, nil, err
		} else {
			deps = append(deps, dep)
			primary = dep
		}
	}
	projects := []organizationProjectMembership{}
	if row.ProjectCode != "" {
		var id string
		err = store.QueryRowContext(ctx, `SELECT id FROM projects WHERE tenant_id=? AND lower(code)=lower(?) AND status='active'`, tenantID, row.ProjectCode).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			add("projectCode", "项目编码不存在或项目已归档")
		} else if err != nil {
			return organizationMemberPatch{}, nil, err
		} else {
			projects = append(projects, organizationProjectMembership{id, row.ProjectRole})
		}
	} else if row.ProjectRole != "viewer" {
		add("projectCode", "指定项目角色时必须提供项目编码")
	}
	if !validProjectRole(row.ProjectRole) || row.ProjectRole == "project_admin" {
		add("projectRole", "CSV 仅可分配普通项目角色")
	}
	active := false
	role := "member"
	return organizationMemberPatch{Name: &row.Name, Email: &email, EmployeeNo: &row.EmployeeNo, Active: &active, TenantRole: &role, DepartmentIDs: &deps, PrimaryDepartmentID: &primary, ProjectMemberships: &projects}, problems, nil
}

// preview 会保存短期、绑定企业和操作者的预览记录，但不创建成员；commit 复查目录变化并一次性原子导入。
func (a *App) organizationImport(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 1 || r.Method != http.MethodPost || !validChoice(parts[0], []string{"preview", "commit"}) {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if parts[0] == "preview" {
		var b struct {
			CSV string `json:"csv"`
		}
		if err := decodeOrganizationJSON(w, r, &b); err != nil {
			failOrganization(w, err)
			return
		}
		tx, _, err := a.beginOrganizationWrite(r, "members.import")
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer tx.Rollback()
		rows, problems, err := parseOrganizationCSV(b.CSV)
		if err != nil {
			failOrganization(w, err)
			return
		}
		seen := map[string]bool{}
		for _, row := range rows {
			_, rowErrors, rowErr := resolveOrganizationImportRow(r.Context(), tx, row)
			if rowErr != nil {
				failOrganization(w, rowErr)
				return
			}
			problems = append(problems, rowErrors...)
			email := strings.ToLower(strings.TrimSpace(row.Email))
			if seen[email] {
				problems = append(problems, organizationImportError{row.Row, "email", "CSV 中的邮箱重复"})
			}
			seen[email] = true
		}
		id := ""
		expires := ""
		if len(problems) == 0 {
			id, err = organizationID("import_")
			if err == nil {
				expires = time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)
				_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_import_previews(id,tenant_id,actor_id,rows_json,created_at,expires_at)VALUES(?,?,?,?,?,?)`, id, tenantID, a.uid(), jsonText(rows), orgNow(), expires)
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM organization_import_previews WHERE tenant_id=? AND expires_at<?`, tenantID, orgNow())
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"previewId": id, "expiresAt": expires, "rows": rows, "errors": localizedOrganizationImportErrors(w, problems), "canCommit": len(problems) == 0})
		return
	}
	var b struct {
		PreviewID string `json:"previewId"`
	}
	if err := decodeOrganizationJSON(w, r, &b); err != nil {
		failOrganization(w, err)
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "members.import")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	var raw, expires string
	var consumed *string
	err = tx.QueryRowContext(r.Context(), `SELECT rows_json,expires_at,consumed_at FROM organization_import_previews WHERE tenant_id=? AND actor_id=? AND id=?`, tenantID, a.uid(), b.PreviewID).Scan(&raw, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		err = orgNotFound()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	expiration, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		failOrganization(w, err)
		return
	}
	if consumed != nil || !expiration.After(time.Now()) {
		failOrganization(w, orgConflict("导入预览已过期或已使用，请重新预览"))
		return
	}
	var rows []organizationImportRow
	if err = json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) == 0 || len(rows) > 500 {
		failOrganization(w, orgInvalid("导入预览内容无效"))
		return
	}
	patches := []organizationMemberPatch{}
	problems := []organizationImportError{}
	for _, row := range rows {
		patch, rowProblems, rowErr := resolveOrganizationImportRow(r.Context(), tx, row)
		if rowErr != nil {
			failOrganization(w, rowErr)
			return
		}
		patches = append(patches, patch)
		problems = append(problems, rowProblems...)
	}
	if len(problems) > 0 {
		write(w, 422, map[string]any{"error": map[string]string{"code": "import_validation_failed", "message": localizedError(w.Header().Get("Content-Language"), 422, "import_validation_failed", "目录已变化，请重新预览后导入")}, "errors": localizedOrganizationImportErrors(w, problems)})
		return
	}
	ids := []string{}
	for _, patch := range patches {
		member, saveErr := a.saveOrganizationMember(r.Context(), tx, admin, "", patch)
		if saveErr != nil {
			failOrganization(w, saveErr)
			return
		}
		ids = append(ids, member.ID)
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE organization_import_previews SET consumed_at=? WHERE tenant_id=? AND actor_id=? AND id=? AND consumed_at IS NULL`, orgNow(), tenantID, a.uid(), b.PreviewID)
	if err == nil {
		err = a.organizationAudit(r.Context(), tx, "organization_import", b.PreviewID, "members_imported", nil, map[string]any{"memberIds": ids, "count": len(ids), "active": false})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 201, map[string]any{"imported": len(ids), "memberIds": ids, "active": false})
}
func localizedOrganizationImportErrors(w http.ResponseWriter, values []organizationImportError) []organizationImportError {
	out := append([]organizationImportError{}, values...)
	for i := range out {
		out[i].Message = localizedError(w.Header().Get("Content-Language"), 422, "validation_error", out[i].Message)
	}
	return out
}

// 防表格公式注入：即使有前导空格/控制字符，也不能让导出的姓名等业务文本成为可执行公式。
func organizationCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if strings.ContainsAny(value, "\t\r\n") || strings.HasPrefix(value, "\x00") || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}

// CSV 只导出目录白名单字段并先写脱敏审计；不会导出密码、令牌或机器人地址，不能当作完整数据库备份。
func (a *App) organizationExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	tx, _, err := a.beginOrganizationWrite(r, "members.export")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	members, err := organizationMemberList(r.Context(), tx, "")
	if err != nil {
		failOrganization(w, err)
		return
	}
	departments, err := organizationDepartmentList(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	codes := map[string]string{}
	for _, d := range departments {
		codes[d.ID] = d.Code
	}
	projectCodes := map[string]string{}
	rows, err := tx.QueryContext(r.Context(), `SELECT id,code FROM projects WHERE tenant_id=? AND status!='deleted'`, tenantID)
	if err != nil {
		failOrganization(w, err)
		return
	}
	for rows.Next() {
		var id, code string
		if err = rows.Scan(&id, &code); err != nil {
			rows.Close()
			failOrganization(w, err)
			return
		}
		projectCodes[id] = code
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		failOrganization(w, err)
		return
	}
	var buffer bytes.Buffer
	buffer.WriteString("\ufeff")
	writer := csv.NewWriter(&buffer)
	if err = writer.Write([]string{"name", "email", "employeeNo", "departmentCode", "projectCode", "projectRole", "active"}); err != nil {
		failOrganization(w, err)
		return
	}
	for _, m := range members {
		projects := m.ProjectMemberships
		if len(projects) == 0 {
			projects = []organizationProjectMembership{{}}
		}
		active := "false"
		if m.Active {
			active = "true"
		}
		for _, p := range projects {
			record := []string{m.Name, m.Email, m.EmployeeNo, codes[m.PrimaryDepartmentID], projectCodes[p.ProjectID], p.Role, active}
			for i := range record {
				record[i] = organizationCSVCell(record[i])
			}
			if err = writer.Write(record); err != nil {
				failOrganization(w, err)
				return
			}
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		failOrganization(w, err)
		return
	}
	if err = a.organizationAudit(r.Context(), tx, "organization", tenantID, "members_exported", nil, map[string]int{"count": len(members)}); err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="organization-members.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(buffer.Bytes())
}
