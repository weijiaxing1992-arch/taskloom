package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// A missing estimate is distinct from a deliberately entered zero.
type RoleWeight struct {
	UserID  string   `json:"userId"`
	UserIDs []string `json:"userIds"`
	Value   *float64 `json:"value"`
}

var requirementWeightRoles = []string{"frontend", "backend", "algorithm", "ui", "product"}
var tagColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

const requirementSelectColumns = `id,code,title,type,description,acceptance,parent_id,category,sprint,status,priority,owner,assignee,tags,start_date,end_date,discipline,progress,estimated_hours,actual_hours,sensitive,auth_impact,updated_at,created_at,role_weights_json,tag_colors_json,remarks,assignee_user_id,owner_user_id,text_mentions_json,assignee_user_ids_json,owner_user_ids_json,description_doc_json,iteration_delay_count`

func scanRequirement(row interface{ Scan(...any) error }, x *Requirement) error {
	var weights, colors, mentions, assignees, owners string
	var descriptionDoc sql.NullString
	if err := row.Scan(&x.ID, &x.Code, &x.Title, &x.Type, &x.Description, &x.Acceptance, &x.ParentID, &x.Category, &x.Sprint, &x.Status, &x.Priority, &x.Owner, &x.Assignee, &x.Tags, &x.StartDate, &x.EndDate, &x.Discipline, &x.Progress, &x.EstimatedHours, &x.ActualHours, &x.Sensitive, &x.AuthImpact, &x.UpdatedAt, &x.CreatedAt, &weights, &colors, &x.Remarks, &x.AssigneeUserID, &x.OwnerUserID, &mentions, &assignees, &owners, &descriptionDoc, &x.IterationDelayCount); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(weights), &x.RoleWeights); err != nil {
		return fmt.Errorf("需求权重数据格式无效: %w", err)
	}
	if err := json.Unmarshal([]byte(colors), &x.TagColors); err != nil {
		return fmt.Errorf("需求标签颜色数据格式无效: %w", err)
	}
	if err := readRequirementMentionState(mentions, x); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(assignees), &x.AssigneeUserIDs); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(owners), &x.OwnerUserIDs); err != nil {
		return err
	}
	var err error
	x.DescriptionDoc, err = readRichDocument(descriptionDoc)
	if err != nil {
		return err
	}
	normalizeRequirementWeights(x)
	if x.TagColors == nil {
		x.TagColors = map[string]string{}
	}
	// Old rows retain their stored REQ-* value for audit safety, while every
	// read surface receives the new concise serial number.
	normalizeRequirementCode(x)
	return nil
}

func normalizeRequirementWeights(x *Requirement) {
	if x.RoleWeights == nil {
		x.RoleWeights = map[string]RoleWeight{}
	}
	x.WeightTotal = 0
	for _, role := range requirementWeightRoles {
		weight := x.RoleWeights[role]
		if weight.UserIDs == nil {
			weight.UserIDs = []string{}
			if weight.UserID != "" {
				weight.UserIDs = append(weight.UserIDs, weight.UserID)
			}
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, id := range weight.UserIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		weight.UserIDs = ids
		weight.UserID = ""
		if len(ids) > 0 {
			weight.UserID = ids[0]
		}
		x.RoleWeights[role] = weight
		if weight.Value != nil {
			x.WeightTotal += *weight.Value
		}
	}
	// Avoid exposing binary floating-point artifacts such as 0.1 + 0.2.
	x.WeightTotal = math.Round(x.WeightTotal*1e6) / 1e6
}

// Old browser bundles only know userId and send every role on assessment saves.
// An unchanged non-empty primary ID is not an instruction to discard secondary
// members. Explicit userIds (including []) and changed/empty legacy IDs remain
// authoritative. Repeat against the latest record inside the writer transaction.
func preserveLegacyRequirementWeightMembers(x, previous *Requirement, patch map[string]json.RawMessage) {
	var rawWeights map[string]json.RawMessage
	if !hasPatch(patch, "roleWeights") || json.Unmarshal(patch["roleWeights"], &rawWeights) != nil {
		return
	}
	for role, raw := range rawWeights {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || hasPatch(fields, "userIds") {
			continue
		}
		weight := x.RoleWeights[role]
		old := previous.RoleWeights[role]
		legacyID := ""
		if rawID, ok := fields["userId"]; ok && json.Unmarshal(rawID, &legacyID) != nil {
			continue
		}
		weight.UserID = legacyID
		weight.UserIDs = []string{}
		if legacyID != "" {
			weight.UserIDs = []string{legacyID}
			if legacyID == old.UserID && len(old.UserIDs) > 0 {
				weight.UserIDs = append([]string{}, old.UserIDs...)
			}
		}
		x.RoleWeights[role] = weight
	}
}

func (a *App) migrateRequirementPlanning() error {
	for column, definition := range map[string]string{
		"role_weights_json": `TEXT NOT NULL DEFAULT '{}'`,
		"tag_colors_json":   `TEXT NOT NULL DEFAULT '{}'`,
		"remarks":           `TEXT NOT NULL DEFAULT ''`,
	} {
		var exists int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('requirements') WHERE name=?`, column).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := a.db.Exec(`ALTER TABLE requirements ADD COLUMN ` + column + ` ` + definition); err != nil {
				return err
			}
		}
	}
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS user_view_preferences(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,view_key TEXT NOT NULL,columns_json TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id,view_key))`); err != nil {
		return err
	}
	// Historic V1 used names such as "V1.0". Canonicalize only unambiguous
	// project-local references; never guess when older names collide.
	for _, table := range []string{"requirements", "defects", "test_plans"} {
		rows, err := a.db.Query(`SELECT DISTINCT tenant_id,project_id,sprint FROM ` + table + ` WHERE sprint NOT IN ('','待规划')`)
		if err != nil {
			return err
		}
		type reference struct{ tenant, project, name string }
		refs := []reference{}
		for rows.Next() {
			var ref reference
			if err := rows.Scan(&ref.tenant, &ref.project, &ref.name); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, ref)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, ref := range refs {
			name, err := resolveSprintName(a.db, ref.tenant, ref.project, ref.name, false)
			if err != nil || name == ref.name {
				continue
			}
			if _, err := a.db.Exec(`UPDATE `+table+` SET sprint=? WHERE tenant_id=? AND project_id=? AND sprint=?`, name, ref.tenant, ref.project, ref.name); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveSprintName(db *sql.DB, tenant, project, value string, assignable bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "待规划" {
		return "待规划", nil
	}
	rows, err := db.Query(`SELECT name,status FROM sprints WHERE tenant_id=? AND project_id=?`, tenant, project)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type match struct{ name, status string }
	exact, aliases := []match{}, []match{}
	for rows.Next() {
		var s match
		if err := rows.Scan(&s.name, &s.status); err != nil {
			return "", err
		}
		if s.name == value {
			exact = append(exact, s)
		} else if strings.HasPrefix(s.name, value+" ") {
			aliases = append(aliases, s)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	matches := exact
	if len(matches) == 0 {
		matches = aliases
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("迭代不属于当前项目或不存在，请刷新后重新选择")
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("迭代名称不唯一，请选择完整迭代名称")
	}
	if assignable && matches[0].status != "规划中" && matches[0].status != "进行中" {
		return "", fmt.Errorf("已完成或已取消的迭代不能再分配需求")
	}
	return matches[0].name, nil
}

func (a *App) resolveRequirementSprint(value string, assignable bool) (string, error) {
	return resolveSprintName(a.db, tenantID, a.pid(), value, assignable)
}

func (a *App) validateRequirement(x *Requirement, previous *Requirement) error {
	x.Title = strings.TrimSpace(x.Title)
	if x.Title == "" {
		return fmt.Errorf("标题不能为空")
	}
	if err := a.validateRequirementCategory(x.Category); err != nil {
		return err
	}
	if x.Progress < 0 || x.Progress > 100 || math.IsNaN(x.EstimatedHours) || math.IsInf(x.EstimatedHours, 0) || x.EstimatedHours < 0 || math.IsNaN(x.ActualHours) || math.IsInf(x.ActualHours, 0) || x.ActualHours < 0 {
		return fmt.Errorf("进度须为 0–100，工时必须为非负有限数值")
	}
	if previous == nil || x.Sprint != previous.Sprint {
		name, err := a.resolveRequirementSprint(x.Sprint, true)
		if err != nil {
			return err
		}
		x.Sprint = name
	}
	if err := a.validateRequirementParent(x.ID, x.ParentID); err != nil {
		return err
	}
	normalizeRequirementWeights(x)
	for role, weight := range x.RoleWeights {
		if !validChoice(role, requirementWeightRoles) {
			return fmt.Errorf("未知权重维度 %s", role)
		}
		if weight.Value != nil && (math.IsNaN(*weight.Value) || math.IsInf(*weight.Value, 0) || *weight.Value < 0 || *weight.Value > 1_000_000) {
			return fmt.Errorf("%s 难度必须为 0–1000000 的有限数值，未评估请留空", role)
		}
		if len(weight.UserIDs) > 50 {
			return fmt.Errorf("每个权重维度最多选择 50 位成员")
		}
		for _, userID := range weight.UserIDs {
			var member int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM project_members pm JOIN users u ON u.id=pm.user_id AND u.tenant_id=pm.tenant_id JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE pm.tenant_id=? AND pm.project_id=? AND pm.user_id=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), userID).Scan(&member); err != nil {
				return err
			}
			if member == 0 {
				// Preserve historic assignment when editing other fields after a
				// colleague leaves; newly assigned inactive members are rejected.
				historical := false
				if previous != nil {
					for _, oldID := range previous.RoleWeights[role].UserIDs {
						if oldID == userID {
							historical = true
						}
					}
					if previous.RoleWeights[role].UserID == userID && userID != "" {
						historical = true
					}
				}
				if !historical {
					return fmt.Errorf("%s 绑定人员不是当前项目的有效成员", role)
				}
			}
		}
	}
	normalizeRequirementWeights(x)
	if x.TagColors == nil {
		x.TagColors = map[string]string{}
	}
	if len(x.TagColors) > 100 {
		return fmt.Errorf("最多配置 100 个标签颜色")
	}
	for tag, color := range x.TagColors {
		if strings.TrimSpace(tag) == "" || utf8.RuneCountInString(tag) > 64 || !tagColorPattern.MatchString(color) {
			return fmt.Errorf("标签名称不能超过 64 字，颜色须为 #RRGGBB 格式")
		}
		x.TagColors[tag] = strings.ToUpper(color)
	}
	if utf8.RuneCountInString(x.Remarks) > 20000 {
		return fmt.Errorf("备注不能超过 20000 字")
	}
	return nil
}

func (a *App) validateRequirementParent(id int64, parent *int64) error {
	seen := map[int64]bool{}
	if id != 0 {
		seen[id] = true
	}
	for parent != nil {
		if *parent <= 0 || seen[*parent] {
			return fmt.Errorf("父需求关系无效：不能选择自身或形成循环")
		}
		seen[*parent] = true
		var next *int64
		if err := a.db.QueryRow(`SELECT parent_id FROM requirements WHERE id=? AND tenant_id=? AND project_id=?`, *parent, tenantID, a.pid()).Scan(&next); err != nil {
			return fmt.Errorf("父需求不属于当前项目或不存在")
		}
		parent = next
	}
	return nil
}

type requirementFieldWrite struct {
	definitionID int64
	value        any
}

func (a *App) prepareRequirementCustomFields(values map[string]any, creating bool) ([]requirementFieldWrite, error) {
	defs, err := a.definitions("requirement", true)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]any{}
	}
	byKey := map[string]FieldDefinition{}
	for _, d := range defs {
		byKey[d.Key] = d
		if creating {
			if _, supplied := values[d.Key]; !supplied && d.DefaultValue != nil {
				values[d.Key] = d.DefaultValue
			}
			if err := validateFieldValue(d, values[d.Key]); err != nil {
				return nil, err
			}
		}
	}
	writes := []requirementFieldWrite{}
	for key, value := range values {
		d, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("自定义字段 %s 不存在或已停用", key)
		}
		if err := validateFieldValue(d, value); err != nil {
			return nil, err
		}
		writes = append(writes, requirementFieldWrite{d.ID, value})
	}
	return writes, nil
}

func (a *App) writeRequirementCustomFields(tx *sql.Tx, id int64, writes []requirementFieldWrite, now string) error {
	return a.writeObjectFields(tx, "requirement", id, writes, now)
}

func (a *App) createRequirement(w http.ResponseWriter, r *http.Request) {
	var x Requirement
	if err := json.NewDecoder(r.Body).Decode(&x); err != nil {
		fail(w, 400, "invalid_json", "请求字段格式不正确")
		return
	}
	x.ID = 0
	importSource := x.TapdImport
	if importSource != nil {
		if err := importSource.validate(); err != nil {
			fail(w, 422, "tapd_import_invalid", err.Error())
			return
		}
		// 完整源字段同时进入可见备注，后续 Markdown/JSON 导出也能读取，不依赖客户端缓存。
		x.Remarks += "\n\n[TAPD 来源 " + importSource.WorkspaceID + "/" + importSource.SourceID + "]\n"
		for _, field := range importSource.Fields {
			x.Remarks += field.Label + "：" + field.Value + "\n"
		}
		x.Remarks += "原始 PDF 与完整字段对照 JSON 见附件。来源创建信息仅留档，不替代本系统审计信息。"
	}
	requestedStatus := x.Status
	defaults(&x)
	x.Status = requestedStatus // Blank means the project's configured initial state.
	doc, docErr := parseRichDocument(x.DescriptionDoc)
	if docErr != nil {
		failRichDocument(w, docErr)
		return
	}
	if doc != nil {
		x.Description = doc.plainText()
		x.DescriptionMentionUserIDs = doc.mentionIDs()
	}
	if err := a.validateRequirement(&x, nil); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	fields, err := a.prepareRequirementCustomFields(x.CustomFields, true)
	if err != nil {
		fail(w, 422, "custom_field_invalid", err.Error())
		return
	}
	x.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	x.UpdatedAt = x.CreatedAt
	var actor string
	_ = a.db.QueryRow(`SELECT name FROM users WHERE id=? AND tenant_id=?`, a.uid(), tenantID).Scan(&actor)
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	if importSource != nil {
		if err := a.requireStateManager(r.Context(), tx); err != nil {
			failState(w, err)
			return
		}
		id, same, err := a.existingTapdImport(tx, importSource)
		if err != nil {
			fail(w, 503, "database_unavailable", "暂时无法检查导入记录，请重试")
			return
		}
		if id > 0 {
			if !same {
				fail(w, 409, "tapd_source_exists", fmt.Sprintf("此 TAPD 需求已导入为 %s，源文件已变化，请在已有需求中核对更新", requirementDisplayCode(id, "")))
				return
			}
			write(w, 200, map[string]any{"id": id, "code": requirementDisplayCode(id, ""), "alreadyImported": true})
			return
		}
	}
	if err := a.validateRequirementImportState(r.Context(), tx, &x); err != nil {
		failState(w, err)
		return
	}
	if err := a.prepareRichRequirement(tx, &x, nil, true, doc); err != nil {
		failRichDocument(w, err)
		return
	}
	peopleNotices, err := a.prepareRequirementPeople(tx, &x, nil, true)
	if err != nil {
		failRequirementPeople(w, err)
		return
	}
	if err := a.validateRequirementCategoryUsing(tx, x.Category); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	res, err := tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,type,description,acceptance,parent_id,category,sprint,status,priority,owner,assignee,tags,start_date,end_date,discipline,progress,estimated_hours,actual_hours,sensitive,auth_impact,created_at,updated_at,role_weights_json,tag_colors_json,remarks,assignee_user_id,owner_user_id,created_by)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", x.Title, x.Type, x.Description, x.Acceptance, x.ParentID, x.Category, x.Sprint, x.Status, x.Priority, x.Owner, x.Assignee, x.Tags, x.StartDate, x.EndDate, x.Discipline, x.Progress, x.EstimatedHours, x.ActualHours, x.Sensitive, x.AuthImpact, x.CreatedAt, x.UpdatedAt, jsonText(x.RoleWeights), jsonText(x.TagColors), x.Remarks, x.AssigneeUserID, x.OwnerUserID, a.uid())
	if err == nil {
		x.ID, err = res.LastInsertId()
	}
	if err == nil && doc != nil {
		x.DescriptionDoc, err = a.persistRichDocument(tx, doc, x.ID, x.CreatedAt)
		x.Description = doc.plainText()
	}
	if err == nil {
		x.Code, err = requirementSerialCode(x.ID)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE requirements SET code=?,text_mentions_json=?,assignee_user_ids_json=?,owner_user_ids_json=?,description_doc_json=?,description=? WHERE id=? AND tenant_id=? AND project_id=?`, x.Code, requirementMentionJSON(&x), jsonText(x.AssigneeUserIDs), jsonText(x.OwnerUserIDs), richSQL(x.DescriptionDoc), x.Description, x.ID, tenantID, a.pid())
	}
	if err == nil && doc != nil {
		err = a.auditRichDocument(tx, x.ID, 0, x.DescriptionDoc, x.CreatedAt)
	}
	if err == nil {
		err = a.writeRequirementCustomFields(tx, x.ID, fields, x.CreatedAt)
	}
	if err == nil && importSource != nil {
		err = a.persistTapdImport(tx, &x, importSource)
	}
	if err == nil {
		var activity sql.Result
		activity, err = tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), x.ID, actor, "created", "创建了需求", x.CreatedAt)
		if err == nil {
			var activityID int64
			activityID, err = activity.LastInsertId()
			if err == nil {
				var after map[string]any
				_, after, err = a.requirementSnapshot(tx, x.ID)
				if err == nil {
					err = a.recordRequirementHistory(tx, activityID, x.ID, map[string]any{}, after, false)
				}
			}
		}
	}
	if err == nil {
		for i := range peopleNotices {
			if peopleNotices[i].Field == "description" {
				peopleNotices[i].Body = x.Description
			}
		}
		err = a.writeRequirementPeopleNotices(tx, &x, peopleNotices, x.CreatedAt)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if errors.Is(err, errRequirementCodeExhausted) {
			fail(w, http.StatusConflict, "requirement_code_exhausted", err.Error())
			return
		}
		if failCustomFieldValidation(w, err) {
			return
		}
		var invalid richValidationError
		if errors.As(err, &invalid) {
			failRichDocument(w, err)
		} else {
			fail(w, 500, "db_error", err.Error())
		}
		return
	}
	x.CustomFields = a.customFields("requirement", x.ID)
	x.IterationDelayCount = 0 // Derived value cannot be supplied by a creating client.
	x.TapdImport = nil        // 不把 PDF Base64 回传到列表、日志或通知中。
	if err := a.hydrateRequirementState(r.Context(), &x); err != nil {
		failState(w, err)
		return
	}
	write(w, 201, x)
}

func (a *App) patchRequirement(w http.ResponseWriter, r *http.Request, id int64) {
	previous, err := a.get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "需求不存在")
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		}
		return
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	var doc *richDocument
	if raw, ok := patch["descriptionDoc"]; ok {
		var docErr error
		doc, docErr = parseRichDocument(raw)
		if docErr != nil {
			failRichDocument(w, docErr)
			return
		}
		if doc != nil {
			patch["description"] = richRaw(doc.plainText())
			patch["descriptionMentionUserIds"] = richRaw(doc.mentionIDs())
		}
	}
	x := previous
	x.DescriptionDoc = append(json.RawMessage(nil), previous.DescriptionDoc...)
	x.CustomFields = nil
	// Collection fields are replaced when present; omitted fields remain intact.
	allowed := map[string]any{"title": &x.Title, "type": &x.Type, "description": &x.Description, "acceptance": &x.Acceptance, "parentId": &x.ParentID, "category": &x.Category, "sprint": &x.Sprint, "status": &x.Status, "priority": &x.Priority, "owner": &x.Owner, "assignee": &x.Assignee, "tags": &x.Tags, "startDate": &x.StartDate, "endDate": &x.EndDate, "discipline": &x.Discipline, "progress": &x.Progress, "estimatedHours": &x.EstimatedHours, "actualHours": &x.ActualHours, "sensitive": &x.Sensitive, "authImpact": &x.AuthImpact, "remarks": &x.Remarks, "roleWeights": &x.RoleWeights, "tagColors": &x.TagColors, "customFields": &x.CustomFields}
	allowed["descriptionMentionUserIds"] = &x.DescriptionMentionUserIDs
	allowed["remarksMentionUserIds"] = &x.RemarksMentionUserIDs
	allowed["assigneeUserIds"] = &x.AssigneeUserIDs
	allowed["ownerUserIds"] = &x.OwnerUserIDs
	allowed["descriptionDoc"] = &x.DescriptionDoc
	changed := []string{}
	for key, raw := range patch {
		target, ok := allowed[key]
		if !ok {
			continue // id, code, createdAt and weightTotal are server-owned.
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && key != "parentId" && key != "roleWeights" && key != "tagColors" && key != "customFields" && key != "descriptionDoc" {
			fail(w, 422, "validation_error", key+" 字段不能为空值")
			return
		}
		if key == "roleWeights" {
			x.RoleWeights = nil
		}
		if key == "tagColors" {
			x.TagColors = nil
		}
		if err := json.Unmarshal(raw, target); err != nil {
			fail(w, 422, "validation_error", key+" 字段格式不正确")
			return
		}
		changed = append(changed, key)
	}
	preserveLegacyRequirementWeightMembers(&x, &previous, patch)
	if err := a.validateRequirement(&x, &previous); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	fields, err := a.prepareRequirementCustomFields(x.CustomFields, false)
	if err != nil {
		fail(w, 422, "custom_field_invalid", err.Error())
		return
	}
	if len(changed) == 0 && !hasIntegrationPrecondition(r) {
		write(w, 200, previous)
		return
	}
	sort.Strings(changed)
	x.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	var actor string
	_ = a.db.QueryRow(`SELECT name FROM users WHERE id=? AND tenant_id=?`, a.uid(), tenantID).Scan(&actor)
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	if !a.checkIntegrationPrecondition(w, r, tx) {
		return
	}
	if _, err := tx.Exec(`UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	if err := a.requireOperationAccess(r.Context(), tx); err != nil {
		failOrganization(w, err)
		return
	}
	currentStatus, stateErr := a.validateRequirementStateWrite(r.Context(), tx, &x, false, hasPatch(patch, "status"))
	if stateErr != nil {
		failState(w, stateErr)
		return
	}
	previous.Status = currentStatus
	_, beforeValues, err := a.requirementSnapshot(tx, id)
	if err != nil {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	if err := a.prepareRichRequirement(tx, &x, patch, false, doc); err != nil {
		failRichDocument(w, err)
		return
	}
	peopleNotices, err := a.prepareRequirementPeople(tx, &x, patch, false)
	if err != nil {
		failRequirementPeople(w, err)
		return
	}
	if doc != nil {
		x.DescriptionDoc, err = a.persistRichDocument(tx, doc, id, x.UpdatedAt)
		if err != nil {
			failRichDocument(w, err)
			return
		}
		x.Description = doc.plainText()
	}
	// Recheck inside the same transaction as the write so a concurrent category
	// rename/delete cannot turn this request's old category into an orphan.
	if err := a.validateRequirementCategoryUsing(tx, x.Category); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	type columnValue struct {
		column string
		value  any
	}
	updates := map[string]columnValue{
		"title": {"title", x.Title}, "type": {"type", x.Type}, "description": {"description", x.Description}, "acceptance": {"acceptance", x.Acceptance}, "parentId": {"parent_id", x.ParentID}, "category": {"category", x.Category}, "sprint": {"sprint", x.Sprint}, "status": {"status", x.Status}, "priority": {"priority", x.Priority}, "owner": {"owner", x.Owner}, "assignee": {"assignee", x.Assignee}, "tags": {"tags", x.Tags}, "startDate": {"start_date", x.StartDate}, "endDate": {"end_date", x.EndDate}, "discipline": {"discipline", x.Discipline}, "progress": {"progress", x.Progress}, "estimatedHours": {"estimated_hours", x.EstimatedHours}, "actualHours": {"actual_hours", x.ActualHours}, "sensitive": {"sensitive", x.Sensitive}, "authImpact": {"auth_impact", x.AuthImpact}, "roleWeights": {"role_weights_json", jsonText(x.RoleWeights)}, "tagColors": {"tag_colors_json", jsonText(x.TagColors)}, "remarks": {"remarks", x.Remarks},
	}
	// Write supplied fields only; a remarks-only PATCH must never rewrite a
	// category, assignment or estimate from an earlier snapshot.
	sets, values := []string{"updated_at=?"}, []any{x.UpdatedAt}
	for _, key := range changed {
		if update, ok := updates[key]; ok {
			sets = append(sets, update.column+"=?")
			values = append(values, update.value)
		}
		if key == "assignee" {
			sets = append(sets, "assignee_user_id=?")
			values = append(values, x.AssigneeUserID)
		}
		if key == "owner" {
			sets = append(sets, "owner_user_id=?")
			values = append(values, x.OwnerUserID)
		}
	}
	if hasPatch(patch, "assigneeUserIds") || hasPatch(patch, "assignee") {
		sets = append(sets, "assignee_user_ids_json=?")
		values = append(values, jsonText(x.AssigneeUserIDs))
		if !hasPatch(patch, "assignee") {
			sets = append(sets, "assignee=?", "assignee_user_id=?")
			values = append(values, x.Assignee, x.AssigneeUserID)
		}
	}
	if hasPatch(patch, "ownerUserIds") || hasPatch(patch, "owner") {
		sets = append(sets, "owner_user_ids_json=?")
		values = append(values, jsonText(x.OwnerUserIDs))
		if !hasPatch(patch, "owner") {
			sets = append(sets, "owner=?", "owner_user_id=?")
			values = append(values, x.Owner, x.OwnerUserID)
		}
	}
	if hasPatch(patch, "description") || hasPatch(patch, "descriptionMentionUserIds") || hasPatch(patch, "remarks") || hasPatch(patch, "remarksMentionUserIds") {
		sets = append(sets, "text_mentions_json=?")
		values = append(values, requirementMentionJSON(&x))
	}
	if hasPatch(patch, "description") || hasPatch(patch, "descriptionDoc") {
		sets = append(sets, "description_doc_json=?")
		values = append(values, richSQL(x.DescriptionDoc))
	}
	values = append(values, id, tenantID, a.pid())
	_, err = tx.Exec(`UPDATE requirements SET `+strings.Join(sets, ",")+` WHERE id=? AND tenant_id=? AND project_id=?`, values...)
	if err == nil && (hasPatch(patch, "descriptionDoc") || hasPatch(patch, "description") && !richIsNull(previous.DescriptionDoc)) {
		err = a.auditRichDocument(tx, id, 0, x.DescriptionDoc, x.UpdatedAt)
	}
	if err == nil {
		err = a.writeRequirementCustomFields(tx, id, fields, x.UpdatedAt)
	}
	var saved Requirement
	var afterValues map[string]any
	if err == nil {
		saved, afterValues, err = a.requirementSnapshot(tx, id)
		if err == nil {
			changed = requirementActualChanges(beforeValues, afterValues)
			if len(changed) == 0 {
				if err = tx.Rollback(); err != nil {
					fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
					return
				}
				current, readErr := a.get(id)
				if readErr != nil {
					fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
					return
				}
				write(w, 200, current)
				return
			}
		}
	}
	var changeActivityID int64
	if err == nil {
		// 活动主键是此次需求变更的稳定事件锚点。自动化重入时复用它，避免
		// 同一状态变更因网络重试或事务包装而生成新的通知事件。
		var activity sql.Result
		activity, err = tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), id, actor, "updated", "更新了需求字段："+strings.Join(changed, "、"), x.UpdatedAt)
		if err == nil {
			changeActivityID, err = activity.LastInsertId()
		}
	}
	if err == nil {
		err = a.recordRequirementHistory(tx, changeActivityID, id, beforeValues, afterValues, true)
	}
	if err == nil {
		for i := range peopleNotices {
			if peopleNotices[i].Field == "description" {
				peopleNotices[i].Body = x.Description
			}
		}
		err = a.writeRequirementPeopleNotices(tx, &x, peopleNotices, x.UpdatedAt)
	}
	if err == nil {
		err = a.writeRequirementChangeNotice(r.Context(), tx, &saved, previous.Status, changed, x.UpdatedAt)
	}
	if err == nil {
		// 自动化事件与需求状态写入同一事务，任一规则通知失败时整个状态变更回滚，
		// 避免“页面已显示新状态但自动化没有可重试事件”的不一致。
		err = a.executeRequirementStatusAutomationRules(r.Context(), tx, &saved, previous.Status, changeActivityID, x.UpdatedAt)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	updated, err := a.get(id)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 200, updated)
}
