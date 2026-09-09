package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// 个人模板保存文本及可选人员配置，不复制附件、工作流状态或通知关系。
func (a *App) migrateRequirementTemplates() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS personal_requirement_templates(
 tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',acceptance TEXT NOT NULL DEFAULT '',remarks TEXT NOT NULL DEFAULT '',
 version INTEGER NOT NULL CHECK(version>0),updated_at TEXT NOT NULL,
 PRIMARY KEY(tenant_id,user_id,id));`)
	if err != nil {
		return err
	}
	var exists int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('personal_requirement_templates') WHERE name='planning_json'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err = a.db.Exec(`ALTER TABLE personal_requirement_templates ADD COLUMN planning_json TEXT NOT NULL DEFAULT '{}'`)
	}
	return err
}

type templatePlanning struct {
	RoleWeights   map[string]RoleWeight `json:"roleWeights"`
	OwnerUserIDs  []string              `json:"ownerUserIds"`
	TesterUserIDs []string              `json:"testerUserIds"`
}

// 保存时按当前项目的有效成员和职能校验；跨项目使用时仍需重新检查候选人。
func (a *App) validateTemplatePlanning(tx *sql.Tx, p *templatePlanning) error {
	names, err := a.activeRequirementMemberNames(tx)
	if err != nil {
		return err
	}
	check := func(key string, ids []string) error {
		if len(ids) > 50 {
			return fmt.Errorf("最多选择 50 位成员")
		}
		for _, id := range ids {
			if _, ok := names[id]; !ok {
				return fmt.Errorf("人员不是当前项目的有效成员")
			}
			if err := a.validateFieldMemberRole(tx, FieldDefinition{ObjectType: "requirement", Type: "users", Key: key, Name: key}, id); err != nil {
				return err
			}
		}
		return nil
	}
	for role, weight := range p.RoleWeights {
		if !validChoice(role, requirementWeightRoles) || weight.Value != nil && (*weight.Value < 0 || *weight.Value > 1000000) {
			return fmt.Errorf("难度维度或数值无效")
		}
		if weight.UserIDs == nil && weight.UserID != "" {
			weight.UserIDs = []string{weight.UserID}
		}
		if err := check("role."+role+".userIds", weight.UserIDs); err != nil {
			return err
		}
		weight.UserID = ""
		if len(weight.UserIDs) > 0 {
			weight.UserID = weight.UserIDs[0]
		}
		p.RoleWeights[role] = weight
	}
	if err := check("ownerUserIds", p.OwnerUserIDs); err != nil {
		return err
	}
	if err := check("testers", p.TesterUserIDs); err != nil {
		return err
	}
	if len(p.TesterUserIDs) > 0 {
		d := FieldDefinition{ObjectType: "requirement", Type: "users", Key: "testers", Name: "测试负责人"}
		if err := tx.QueryRow(`SELECT department_id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key='testers' AND enabled=1`, tenantID, a.pid()).Scan(&d.DepartmentID); err != nil {
			return fmt.Errorf("测试人员字段当前不可用")
		}
		return a.validateFieldPeople(tx, d, p.TesterUserIDs, nil)
	}
	return nil
}

type requirementTemplate struct {
	templatePlanning
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Acceptance  string `json:"acceptance"`
	Remarks     string `json:"remarks"`
	Version     int64  `json:"version"`
	UpdatedAt   string `json:"updatedAt"`
}

func (a *App) requirementTemplates(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/requirement-templates/")
	if r.URL.Path == "/api/requirement-templates" {
		id = ""
	} else if !privateDraftIDPattern.MatchString(id) {
		fail(w, 404, "not_found", "模板不存在")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut && r.Method != http.MethodDelete || id == "" && r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		templatePlanning
		Name        string `json:"name"`
		Description string `json:"description"`
		Acceptance  string `json:"acceptance"`
		Remarks     string `json:"remarks"`
		BaseVersion int64  `json:"baseVersion"`
	}
	if r.Method != http.MethodGet {
		if err := decodePrivateDraftBody(w, r, &input, 256<<10); err != nil {
			failPrivateDraft(w, err)
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if input.BaseVersion < 0 || r.Method == http.MethodPut && (input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || len(input.Description) > 120000 || len(input.Acceptance) > 30000 || len(input.Remarks) > 30000 || strings.TrimSpace(input.Description+input.Acceptance+input.Remarks) == "") {
			fail(w, 422, "invalid_template", "请填写模板名称和内容，名称最多 80 字，正文最多 120 KB")
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		failPrivateDraft(w, err)
		return
	}
	defer tx.Rollback()
	if r.Method != http.MethodGet {
		if err = a.privateDraftWriteLock(r.Context(), tx); err != nil {
			failPrivateDraft(w, err)
			return
		}
	}
	if err = a.privateDraftAccess(r.Context(), tx, false); err != nil {
		failPrivateDraft(w, err)
		return
	}
	role, err := a.requirementStateRole(r.Context(), tx)
	if err != nil {
		failPrivateDraft(w, err)
		return
	}
	allowed := role == "product" || role == "tenant_admin" || role == "project_admin"
	if !allowed {
		if r.Method == http.MethodGet && id == "" {
			write(w, 200, map[string]any{"items": []any{}, "canManage": false})
			return
		}
		fail(w, 403, "product_role_required", "仅产品角色及管理员可管理个人需求模板")
		return
	}
	if r.Method == http.MethodGet && id == "" {
		rows, e := tx.QueryContext(r.Context(), `SELECT id,name,version,updated_at FROM personal_requirement_templates WHERE tenant_id=? AND user_id=? ORDER BY updated_at DESC,id`, tenantID, a.uid())
		if e != nil {
			failPrivateDraft(w, e)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var item requirementTemplate
			if e = rows.Scan(&item.ID, &item.Name, &item.Version, &item.UpdatedAt); e != nil {
				failPrivateDraft(w, e)
				return
			}
			items = append(items, map[string]any{"id": item.ID, "name": item.Name, "version": item.Version, "updatedAt": item.UpdatedAt})
		}
		if e = rows.Err(); e != nil {
			failPrivateDraft(w, e)
			return
		}
		write(w, 200, map[string]any{"items": items, "canManage": true})
		return
	}
	var item requirementTemplate
	var planning string
	err = tx.QueryRowContext(r.Context(), `SELECT id,name,description,acceptance,remarks,version,updated_at,planning_json FROM personal_requirement_templates WHERE tenant_id=? AND user_id=? AND id=?`, tenantID, a.uid(), id).Scan(&item.ID, &item.Name, &item.Description, &item.Acceptance, &item.Remarks, &item.Version, &item.UpdatedAt, &planning)
	exists := err == nil
	if exists {
		err = json.Unmarshal([]byte(planning), &item.templatePlanning)
	}
	if err != nil && err != sql.ErrNoRows {
		failPrivateDraft(w, err)
		return
	}
	if r.Method == http.MethodGet {
		if !exists {
			fail(w, 404, "not_found", "模板不存在或不属于当前账号")
			return
		}
		write(w, 200, map[string]any{"template": item})
		return
	}
	if !exists && (r.Method == http.MethodDelete || input.BaseVersion != 0) {
		fail(w, 404, "not_found", "模板不存在或已删除")
		return
	}
	if exists && input.BaseVersion != item.Version {
		fail(w, 409, "template_conflict", "模板已被其他页面修改，请刷新后重试")
		return
	}
	if r.Method == http.MethodDelete {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM personal_requirement_templates WHERE tenant_id=? AND user_id=? AND id=?`, tenantID, a.uid(), id)
	} else {
		if err = a.validateTemplatePlanning(tx, &input.templatePlanning); err != nil {
			fail(w, 422, "invalid_template", err.Error())
			return
		}
		if !exists {
			var count int
			if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM personal_requirement_templates WHERE tenant_id=? AND user_id=?`, tenantID, a.uid()).Scan(&count); err != nil {
				failPrivateDraft(w, err)
				return
			}
			if count >= 100 {
				fail(w, 422, "template_limit", "每人最多保存 100 个模板")
				return
			}
		}
		item = requirementTemplate{ID: id, Name: input.Name, Description: input.Description, Acceptance: input.Acceptance, Remarks: input.Remarks, Version: input.BaseVersion + 1, UpdatedAt: orgNow()}
		item.templatePlanning = input.templatePlanning
		_, err = tx.ExecContext(r.Context(), `INSERT INTO personal_requirement_templates(tenant_id,user_id,id,name,description,acceptance,remarks,version,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,user_id,id) DO UPDATE SET name=excluded.name,description=excluded.description,acceptance=excluded.acceptance,remarks=excluded.remarks,version=excluded.version,updated_at=excluded.updated_at`, tenantID, a.uid(), id, item.Name, item.Description, item.Acceptance, item.Remarks, item.Version, item.UpdatedAt)
	}
	if err == nil && r.Method == http.MethodPut {
		encoded, e := json.Marshal(item.templatePlanning)
		err = e
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE personal_requirement_templates SET planning_json=? WHERE tenant_id=? AND user_id=? AND id=?`, string(encoded), tenantID, a.uid(), id)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failPrivateDraft(w, err)
		return
	}
	write(w, 200, map[string]any{"template": item, "deleted": r.Method == http.MethodDelete})
}
