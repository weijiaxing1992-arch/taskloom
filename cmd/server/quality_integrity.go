package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func sameNullableID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func (a *App) validateDefectData(d *Defect, previous *Defect) error {
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" || utf8.RuneCountInString(d.Title) > 200 {
		return fmt.Errorf("缺陷标题须为 1–200 字")
	}
	if d.Status == "" {
		d.Status = "新建"
	}
	if d.Priority == "" {
		d.Priority = "P2"
	}
	if d.Severity == "" {
		d.Severity = "一般"
	}
	if !validChoice(d.Status, []string{"新建", "已确认", "修复中", "已解决", "待验证", "已关闭", "重新打开", "已拒绝"}) || !validChoice(d.Priority, []string{"P0", "P1", "P2", "P3"}) || !validChoice(d.Severity, []string{"致命", "严重", "一般", "轻微"}) {
		return fmt.Errorf("缺陷状态、优先级或严重程度无效")
	}
	if d.Progress < 0 || d.Progress > 100 || d.EstimatedHours < 0 || d.ActualHours < 0 || math.IsNaN(d.EstimatedHours) || math.IsInf(d.EstimatedHours, 0) || math.IsNaN(d.ActualHours) || math.IsInf(d.ActualHours, 0) {
		return fmt.Errorf("进度须为 0–100，工时不能为负数")
	}
	if previous == nil || d.Sprint != previous.Sprint {
		s, err := a.resolveRequirementSprint(d.Sprint, true)
		if err != nil {
			return err
		}
		d.Sprint = s
	}
	if previous == nil || d.Assignee != previous.Assignee || d.AssigneeUserID != previous.AssigneeUserID {
		if err := a.normalizeDefectPerson(&d.Assignee, &d.AssigneeUserID); err != nil {
			return err
		}
	}
	if previous == nil || d.Verifier != previous.Verifier || d.VerifierUserID != previous.VerifierUserID {
		if err := a.normalizeDefectPerson(&d.Verifier, &d.VerifierUserID); err != nil {
			return err
		}
	}
	if d.RequirementID != nil && (previous == nil || !sameNullableID(d.RequirementID, previous.RequirementID)) {
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), *d.RequirementID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("关联需求不属于当前项目或不存在")
		}
	}
	return nil
}

func (a *App) prepareDefectPatch(id int64, p map[string]any) error {
	if _, supplied := p["sourceExecutionId"]; supplied {
		return fmt.Errorf("测试执行来源只能通过失败执行生成缺陷，不能手动设置")
	}
	for key := range p {
		if !validChoice(key, []string{"title", "description", "steps", "actual", "expected", "environment", "foundVersion", "fixVersion", "severity", "priority", "status", "assignee", "assigneeUserId", "verifier", "verifierUserId", "sprint", "discipline", "progress", "estimatedHours", "actualHours", "requirementId", "tags", "customFields"}) {
			return fmt.Errorf("缺陷编辑包含不支持或只读字段")
		}
	}
	current, err := a.getDefect(id)
	if err != nil {
		return err
	}
	next := current
	// Unmarshal into the typed model before the SQL layer ever sees these values.
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("字段格式无效")
	}
	if err = json.Unmarshal(raw, &next); err != nil {
		return fmt.Errorf("字段格式无效")
	}
	// Explicit IDs are validated and canonicalize names. Legacy name-only
	// changes may resolve by an unambiguous name, but cannot override an ID
	// supplied by a modern member picker.
	if _, ok := p["assigneeUserId"]; ok {
		if next.AssigneeUserID == "" {
			next.Assignee = ""
		}
	} else if next.Assignee != current.Assignee {
		next.AssigneeUserID = ""
	}
	if _, ok := p["verifierUserId"]; ok {
		if next.VerifierUserID == "" {
			next.Verifier = ""
		}
	} else if next.Verifier != current.Verifier {
		next.VerifierUserID = ""
	}
	for key, value := range p {
		if value == nil && key != "requirementId" && key != "customFields" {
			return fmt.Errorf("字段格式无效")
		}
	}
	if err = a.validateDefectData(&next, &current); err != nil {
		return err
	}
	if value, ok := p["customFields"]; ok && value != nil {
		if _, valid := value.(map[string]any); !valid {
			return fmt.Errorf("字段格式无效")
		}
	}
	// Normalize names and sprint aliases without rewriting untouched fields.
	if _, ok := p["title"]; ok {
		p["title"] = next.Title
	}
	if _, ok := p["sprint"]; ok {
		p["sprint"] = next.Sprint
	}
	if _, ok := p["status"]; ok {
		p["status"] = next.Status
	}
	if _, ok := p["priority"]; ok {
		p["priority"] = next.Priority
	}
	if _, ok := p["severity"]; ok {
		p["severity"] = next.Severity
	}
	_, assigneeSupplied := p["assignee"]
	_, assigneeIDProvided := p["assigneeUserId"]
	if assigneeSupplied || assigneeIDProvided {
		p["assignee"] = next.Assignee
		p["assigneeUserId"] = next.AssigneeUserID
	}
	_, verifierSupplied := p["verifier"]
	_, verifierIDProvided := p["verifierUserId"]
	if verifierSupplied || verifierIDProvided {
		p["verifier"] = next.Verifier
		p["verifierUserId"] = next.VerifierUserID
	}
	return nil
}

func (a *App) normalizeDefectPerson(name, id *string) error {
	if *id == "" {
		uid, err := a.projectPerson(*name)
		if err != nil {
			return err
		}
		*id = uid
		return nil
	}
	var canonical string
	err := a.db.QueryRow(`SELECT u.name FROM users u JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND pm.project_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), *id).Scan(&canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("负责人不是当前项目的有效成员，请重新选择")
	}
	if err != nil {
		return err
	}
	*name = canonical
	return nil
}

func (a *App) createDefectAtomic(w http.ResponseWriter, r *http.Request, d Defect) {
	if d.SourceExecutionID != nil {
		fail(w, 422, "validation_error", "测试执行来源只能通过失败执行生成缺陷，不能手动设置")
		return
	}
	if err := a.validateDefectData(&d, nil); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	fields, err := a.prepareObjectFields("defect", d.CustomFields, true)
	if err != nil {
		fail(w, 422, "custom_field_invalid", err.Error())
		return
	}
	actor := a.actorName()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	if err := a.verifyNewDefectReferences(tx, d); err != nil {
		failCollaboration(w, err)
		return
	}
	id, err := a.insertDefectWith(tx, d)
	if err == nil {
		d.Code = fmt.Sprintf("BUG-%04d", id)
	}
	if err == nil {
		err = a.writeObjectFields(tx, "defect", id, fields, now)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'defect',?,?,'created',?,?)`, tenantID, a.pid(), id, actor, "创建了缺陷", now)
	}
	if err == nil {
		// Assignment delivery must commit with the defect.  The former
		// post-commit best-effort notify call could silently lose an inbox row.
		err = a.writeAssignmentNotices(r.Context(), tx, defectAssignmentNotices(id, d.Code, d.Title, d.AssigneeUserID, d.VerifierUserID, "", ""), now)
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
	created, err := a.getDefect(id)
	if err != nil {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 201, created)
}

func (a *App) requireEntity(w http.ResponseWriter, object string, id int64) bool {
	table := map[string]string{"requirement": "requirements", "defect": "defects", "test_case": "test_cases", "sprint": "sprints", "test_plan": "test_plans"}[object]
	if table == "" {
		fail(w, 404, "not_found", "资源不存在")
		return false
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&count); err != nil {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return false
	}
	if count != 1 {
		fail(w, 404, "not_found", "资源不存在")
		return false
	}
	return true
}

// Validate everything before writing. Field values, business rows and audit
// records must commit together, including when an optional field fails.
func (a *App) prepareObjectFields(object string, values map[string]any, creating bool) ([]requirementFieldWrite, error) {
	defs, err := a.definitions(object, true)
	if err != nil {
		return nil, err
	}
	merged := map[string]any{}
	for k, v := range values {
		merged[k] = v
	}
	byKey := map[string]FieldDefinition{}
	for _, d := range defs {
		byKey[d.Key] = d
		if creating {
			if _, ok := merged[d.Key]; !ok && d.DefaultValue != nil {
				merged[d.Key] = d.DefaultValue
			}
			if err := validateFieldValue(d, merged[d.Key]); err != nil {
				return nil, err
			}
		}
	}
	writes := []requirementFieldWrite{}
	keys := []string{}
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		d, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("自定义字段 %s 不存在或已停用", key)
		}
		if err := validateFieldValue(d, merged[key]); err != nil {
			return nil, err
		}
		writes = append(writes, requirementFieldWrite{d.ID, merged[key]})
	}
	return writes, nil
}

func (a *App) writeObjectFields(tx *sql.Tx, object string, id int64, writes []requirementFieldWrite, now string) error {
	for _, field := range writes {
		if err := a.validateObjectFieldWrite(tx, object, id, field); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,object_type,object_id,field_definition_id) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at`, tenantID, a.pid(), object, id, field.definitionID, jsonText(field.value), now); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) projectPerson(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	rows, err := a.db.Query(`SELECT DISTINCT u.id FROM users u JOIN project_members pm ON pm.user_id=u.id AND pm.tenant_id=u.tenant_id JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE pm.tenant_id=? AND pm.project_id=? AND u.name=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("负责人不是当前项目唯一有效成员，请重新选择")
	}
	return ids[0], nil
}

func (a *App) validateCase(c *TestCase, checkOwner, checkRequirement bool) error {
	c.Title = strings.TrimSpace(c.Title)
	if c.Priority == "" {
		c.Priority = "P2"
	}
	if c.Status == "" {
		c.Status = "草稿"
	}
	if c.CaseType == "" {
		c.CaseType = "功能测试"
	}
	if len(c.StepsDetail) > 200 {
		return fmt.Errorf("测试步骤不能超过 200 条")
	}
	if len(c.StepsDetail) > 0 {
		actions, expected := []string{}, []string{}
		for i := range c.StepsDetail {
			s := &c.StepsDetail[i]
			s.Order = i + 1
			if strings.TrimSpace(s.Action) == "" || strings.TrimSpace(s.Expected) == "" {
				return fmt.Errorf("每个测试步骤都需要操作和预期结果")
			}
			actions = append(actions, fmt.Sprintf("%d. %s", i+1, s.Action))
			expected = append(expected, fmt.Sprintf("%d. %s", i+1, s.Expected))
		}
		c.Steps = strings.Join(actions, "\n")
		c.Expected = strings.Join(expected, "\n")
	}
	if c.Title == "" || utf8.RuneCountInString(c.Title) > 200 || strings.TrimSpace(c.Steps) == "" || strings.TrimSpace(c.Expected) == "" {
		return fmt.Errorf("标题、步骤和预期不能为空，标题不能超过 200 字")
	}
	if !validChoice(c.Status, []string{"草稿", "待评审", "已通过", "已废弃"}) || !validChoice(c.Priority, []string{"P0", "P1", "P2", "P3"}) || !validChoice(c.CaseType, []string{"功能测试", "接口测试", "兼容性测试", "安全测试", "性能测试", "自动化测试"}) {
		return fmt.Errorf("用例状态、优先级或类型无效")
	}
	if checkRequirement && c.RequirementID != nil && *c.RequirementID <= 0 {
		return fmt.Errorf("关联需求不属于当前项目或不存在")
	}
	if checkOwner {
		uid, err := a.projectPerson(c.Owner)
		if err != nil {
			return err
		}
		c.OwnerUserID = uid
	}
	return nil
}

// normalizeCasePatchOwner 在测试用例 PATCH 的写事务内收敛负责人“显示名 +
// 稳定 ID”这一对字段。现代成员选择器发送 ownerUserId；ID 发生变化时便
// 只以它为准，不允许同名显示名反向改绑。旧客户端仅发送 owner 时仍可兼容，
// 但名称必须在当前项目唯一匹配，避免把同名成员的通知发错人。
//
// 显式空 ownerUserId 是清空负责人。无论按 ID 还是旧名称解析，都同时要求项目
// 成员、租户成员、active 和 operation_disabled=0；校验与写入/通知共用一个事务，
// 因此账号刚被停用或退出项目不能留下半条负责人更新。
func (a *App) normalizeCasePatchOwner(ctx context.Context, tx *sql.Tx, owner, ownerUserID *string, explicitID, legacyNameChanged bool) error {
	if explicitID {
		*ownerUserID = strings.TrimSpace(*ownerUserID)
		if *ownerUserID == "" {
			*owner = ""
			return nil
		}
		var canonical string
		err := tx.QueryRowContext(ctx, `SELECT u.name FROM users u
			JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id
			JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
			WHERE u.tenant_id=? AND pm.project_id=? AND u.id=?
				AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'`, tenantID, a.pid(), *ownerUserID).Scan(&canonical)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("负责人不是当前项目的有效成员，请重新选择")
		}
		if err != nil {
			return err
		}
		*owner = canonical
		return nil
	}
	if !legacyNameChanged {
		return nil
	}
	*owner = strings.TrimSpace(*owner)
	if *owner == "" {
		*ownerUserID = ""
		return nil
	}
	// 旧 name-only 请求最多读取两位候选成员：一位可安全回填，第二位即可判定
	// 歧义。这样既兼容历史客户端，也不会在大型项目中做无界同名扫描。
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT u.id,u.name FROM users u
		JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id
		JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
		WHERE u.tenant_id=? AND pm.project_id=? AND u.name=?
			AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'
		LIMIT 2`, tenantID, a.pid(), *owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids, names := []string{}, []string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		ids, names = append(ids, id), append(names, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) != 1 {
		return fmt.Errorf("负责人不是当前项目唯一有效成员，请重新选择")
	}
	*ownerUserID, *owner = ids[0], names[0]
	return nil
}

func (a *App) insertCaseAtomic(w http.ResponseWriter, r *http.Request, c TestCase, event string) {
	if c.Metadata != nil {
		if c.Metadata.Preconditions != "" {
			if c.Preconditions != "" && c.Preconditions != c.Metadata.Preconditions {
				fail(w, 422, "validation_error", "metadata 与顶层前置条件不一致")
				return
			}
			c.Preconditions = c.Metadata.Preconditions
		}
		if c.Metadata.RequirementID != nil {
			if c.RequirementID != nil && !sameNullableID(c.RequirementID, c.Metadata.RequirementID) {
				fail(w, 422, "validation_error", "metadata 与顶层关联需求不一致")
				return
			}
			c.RequirementID = c.Metadata.RequirementID
		}
	}
	// 人员由写事务内的稳定 ID 解析；预检不能按旧显示名覆盖客户端选择的 ID。
	if err := a.validateCase(&c, false, true); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	fields, err := a.prepareObjectFields("test_case", c.CustomFields, true)
	if err != nil {
		fail(w, 422, "custom_field_invalid", err.Error())
		return
	}
	actor := a.actorName()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	// 与更新路径复用同一成员边界：显式 ID 优先，同名不影响绑定；旧 name-only
	// 客户端继续要求项目内唯一有效匹配。成员校验、保存和分配通知一起提交。
	if err = a.normalizeCasePatchOwner(r.Context(), tx, &c.Owner, &c.OwnerUserID, c.OwnerUserID != "", true); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	metadata := TestingCaseMetadata{}
	if c.Metadata != nil {
		metadata = *c.Metadata
	}
	// 创建用例与 AI 导入均在插入前读取同一事务内的设置快照。默认值、必填项、
	// 关联需求和目录位置后续一次提交，避免“用例已创建但配置字段丢失”。
	metadata, err = a.normalizeTestingCaseMetadata(r.Context(), tx, &c, metadata, map[string]bool{})
	if err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	c.Metadata = &metadata
	// 关联需求的有效性必须在本次写事务中确认，避免 preflight 后目标需求发生变化。
	if err = a.validateTestCaseRequirement(r.Context(), tx, c.RequirementID); err != nil {
		var invalid *organizationError
		if errors.As(err, &invalid) {
			fail(w, invalid.Status, invalid.Code, invalid.Message)
		} else {
			fail(w, 500, "db_error", err.Error())
		}
		return
	}
	if c.RequirementID != nil {
		reference, referenceErr := a.requirementTestCaseReference(r.Context(), tx, *c.RequirementID)
		if referenceErr != nil {
			fail(w, 500, "db_error", referenceErr.Error())
			return
		}
		c.Requirement = &reference
	}
	c.Enabled = true
	res, err := tx.Exec(`INSERT INTO test_cases(tenant_id,project_id,code,category,title,preconditions,steps,expected,priority,status,owner,requirement_id,owner_user_id,type,tags,enabled,steps_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", c.Category, c.Title, c.Preconditions, c.Steps, c.Expected, c.Priority, c.Status, c.Owner, c.RequirementID, c.OwnerUserID, c.CaseType, c.Tags, true, jsonText(c.StepsDetail), now, now)
	if err == nil {
		c.ID, err = res.LastInsertId()
	}
	c.Code = fmt.Sprintf("TC-%04d", c.ID)
	if err == nil {
		_, err = tx.Exec(`UPDATE test_cases SET code=? WHERE id=? AND tenant_id=? AND project_id=?`, c.Code, c.ID, tenantID, a.pid())
	}
	if err == nil {
		err = a.writeTestingCaseMetadata(r.Context(), tx, c.ID, metadata, now)
	}
	if err == nil {
		// 返回与库定位同事务中的实际结果（包括自动分配的默认库），避免客户端
		// 刚创建完又因空 libraryId 额外查询或错误地显示“未归属”。
		var stored TestingCaseMetadata
		stored, err = a.testingCaseMetadata(r.Context(), tx, c.ID)
		if err == nil {
			stored.Preconditions = c.Preconditions
			stored.RequirementID = c.RequirementID
			c.Metadata = &stored
		}
	}
	if err == nil {
		err = a.writeObjectFields(tx, "test_case", c.ID, fields, now)
	}
	if err == nil {
		c.UpdatedAt = now
		err = a.recordTestCaseTrace(r.Context(), tx, c, actor, event, "保存了测试用例")
	}
	if err == nil && c.OwnerUserID != "" {
		err = a.writeAssignmentNotices(r.Context(), tx, []assignmentNotice{{
			Event:      "test_case.owner_assigned",
			Subject:    "test_case",
			SubjectID:  c.ID,
			Field:      "owner",
			Title:      "你被指定为测试用例负责人",
			Body:       strings.TrimSpace(c.Code + " " + c.Title),
			Recipients: []string{c.OwnerUserID},
		}}, now)
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
	// 摘要已在写事务内获得，提交后不再追加一次可能失败的读请求；否则业务已经
	// 成功却回 5xx 会诱导客户端重试，进而造成重复创建。
	c.CustomFields = a.customFields("test_case", c.ID)
	write(w, 201, c)
}

func (a *App) patchCaseAtomic(w http.ResponseWriter, r *http.Request, id int64) {
	current, err := a.getTestCase(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "用例不存在")
		} else {
			fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		}
		return
	}
	var patch map[string]json.RawMessage
	if err := decodeJSON(r, &patch); err != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	// 可选关联前置条件：显式 null 表示只允许关联尚未归属需求的用例。
	// 不携带该字段的旧客户端保持兼容；携带时必须在写事务内再次比较。
	var expectedRequirementID *int64
	_, checkRequirementLink := patch["expectedRequirementId"]
	if checkRequirementLink {
		expectedRequirementID, err = testingDecodeOptionalID(patch["expectedRequirementId"])
		if err != nil {
			fail(w, 422, "validation_error", "原关联需求编号无效")
			return
		}
	}
	next := current
	next.CustomFields = nil
	metadata := TestingCaseMetadata{}
	if current.Metadata != nil {
		metadata = *current.Metadata
	}
	var metadataPatch testingMetadataPatch
	metadataChanged := false
	if raw, ok := patch["metadata"]; ok {
		metadataPatch, err = parseTestingMetadataPatch(raw, metadata)
		if err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		metadata = metadataPatch.Metadata
		metadataChanged = true
		if metadataPatch.PreconditionsSet {
			if rawTop, topSet := patch["preconditions"]; topSet {
				var top string
				if json.Unmarshal(rawTop, &top) != nil || top != metadata.Preconditions {
					fail(w, 422, "validation_error", "metadata 与顶层前置条件不一致")
					return
				}
			}
			next.Preconditions = metadata.Preconditions
		}
		if metadataPatch.RequirementSet {
			if rawTop, topSet := patch["requirementId"]; topSet {
				// 显式 null 也是用户的有效清空意图，必须和 metadata 的别名值
				// 一致；若跳过 null 比较，旧嵌套值会悄悄覆盖用户清空关联的更新。
				linkedID, parseErr := testingDecodeOptionalID(rawTop)
				if parseErr != nil || !sameNullableID(linkedID, metadata.RequirementID) {
					fail(w, 422, "validation_error", "metadata 与顶层关联需求不一致")
					return
				}
			}
			next.RequirementID = metadata.RequirementID
		}
	}
	allowed := map[string]string{"category": "category", "title": "title", "preconditions": "preconditions", "steps": "steps", "expected": "expected", "priority": "priority", "status": "status", "owner": "owner", "caseType": "type", "tags": "tags", "enabled": "enabled", "requirementId": "requirement_id"}
	targets := map[string]any{"category": &next.Category, "title": &next.Title, "preconditions": &next.Preconditions, "steps": &next.Steps, "expected": &next.Expected, "priority": &next.Priority, "status": &next.Status, "owner": &next.Owner, "ownerUserId": &next.OwnerUserID, "caseType": &next.CaseType, "tags": &next.Tags, "enabled": &next.Enabled, "requirementId": &next.RequirementID, "stepsDetail": &next.StepsDetail, "customFields": &next.CustomFields}
	for key, raw := range patch {
		if target := targets[key]; target != nil {
			// ownerUserId 的 null 与空字符串均表示显式清空。其他字符串字段
			// 保持既有的非空 JSON 类型约束，避免 null 被悄悄转换为零值。
			if key == "ownerUserId" && string(raw) == "null" {
				next.OwnerUserID = ""
				continue
			}
			if (string(raw) == "null" && key != "requirementId" && key != "customFields") || json.Unmarshal(raw, target) != nil {
				fail(w, 422, "validation_error", "字段格式无效")
				return
			}
		}
	}
	if metadataPatch.PreconditionsSet {
		next.Preconditions = metadata.Preconditions
	}
	if metadataPatch.RequirementSet {
		next.RequirementID = metadata.RequirementID
	}
	// metadata 内嵌字段在上方已经合并到 next；循环中保留它只作“已提供”标识，
	// 不会被拼接进 SQL 列名，避免客户端控制列名。
	if metadataChanged {
		metadata.Preconditions = next.Preconditions
		metadata.RequirementID = next.RequirementID
	}
	if _, ok := patch["stepsDetail"]; ok && len(next.StepsDetail) == 0 {
		fail(w, 422, "validation_error", "至少保留一个测试步骤")
		return
	}
	if _, ok := patch["steps"]; ok {
		if _, detail := patch["stepsDetail"]; !detail {
			next.StepsDetail = nil
		}
	}
	if _, ok := patch["expected"]; ok {
		if _, detail := patch["stepsDetail"]; !detail {
			next.StepsDetail = nil
		}
	}
	_, ownerIDSupplied := patch["ownerUserId"]
	_, ownerNameSupplied := patch["owner"]
	if err := a.validateCase(&next, false, !sameNullableID(next.RequirementID, current.RequirementID)); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	fields, err := a.prepareObjectFields("test_case", next.CustomFields, false)
	if err != nil {
		fail(w, 422, "custom_field_invalid", err.Error())
		return
	}
	if _, ok := patch["stepsDetail"]; ok {
		patch["steps"] = nil
		patch["expected"] = nil
	}
	actor := a.actorName()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	if !a.checkIntegrationPrecondition(w, r, tx) {
		return
	}
	if checkRequirementLink {
		var linkedID *int64
		if err = tx.QueryRowContext(r.Context(), `SELECT requirement_id FROM test_cases WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&linkedID); err != nil {
			fail(w, 503, "database_unavailable", "用例关联暂时无法读取，请重试")
			return
		}
		if !sameNullableID(linkedID, expectedRequirementID) {
			fail(w, 409, "requirement_link_conflict", "用例的需求关联已变化，请刷新后重试；本次修改未保存")
			return
		}
	}
	// 初始 getTestCase 只用于读取完整编辑模型。负责人是否真的变化必须在写
	// 事务获得锁后重读：两个请求同时选择同一 ID 时，后进入者应看见前者已
	// 写入的负责人，不能按过期快照再投递一次 owner_changed 通知。
	var currentOwner, currentOwnerUserID string
	if err = tx.QueryRowContext(r.Context(), `SELECT owner,owner_user_id FROM test_cases WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&currentOwner, &currentOwnerUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "用例不存在")
		} else {
			fail(w, 500, "db_error", err.Error())
		}
		return
	}
	ownerIDChanged := ownerIDSupplied && next.OwnerUserID != currentOwnerUserID
	// 老数据可能只有 owner 显示名而没有 owner_user_id。新版整包表单回传这
	// 对原值时应视为未改动；否则空 ID 仍表示用户明确清空负责人。
	ownerPairUnchanged := ownerIDSupplied && ownerNameSupplied && next.OwnerUserID == currentOwnerUserID && next.Owner == currentOwner
	ownerIDCleared := ownerIDSupplied && strings.TrimSpace(next.OwnerUserID) == "" && !ownerPairUnchanged
	legacyOwnerChanged := !ownerIDSupplied && ownerNameSupplied && next.Owner != currentOwner
	if ownerIDChanged || ownerIDCleared || legacyOwnerChanged {
		if err = a.normalizeCasePatchOwner(r.Context(), tx, &next.Owner, &next.OwnerUserID, ownerIDChanged || ownerIDCleared, legacyOwnerChanged); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
	} else if ownerIDSupplied {
		// 新版表单每次保存会回传 owner 与 ownerUserId。稳定 ID 未变时，保持
		// 数据库中的历史显示名和 ID：这允许已停用的历史负责人继续编辑其他
		// 字段，也防止前端缓存的旧姓名在改名后反向覆盖正确绑定。
		next.Owner, next.OwnerUserID = currentOwner, currentOwnerUserID
	} else {
		// 本次 PATCH 没有任何负责人字段时，next 仍来自事务外的完整模型。必须
		// 回填写事务刚读到的二元组，否则并发请求会把已经更新的负责人回写成
		// 旧快照，甚至误判为一次新的 owner_changed。
		next.Owner, next.OwnerUserID = currentOwner, currentOwnerUserID
	}
	ownerChanged := next.Owner != currentOwner || next.OwnerUserID != currentOwnerUserID
	// 负责人经过事务内校验和规范化之后再序列化。这样 owner/ownerUserId
	// 永远作为一对原子字段持久化，显示名也不会落入客户端传来的伪造值。
	payload := map[string]any{}
	encoded, _ := json.Marshal(next)
	_ = json.Unmarshal(encoded, &payload)
	if metadataChanged {
		// 编辑不暗中填充新默认值，但配置为必填后必须在任何后续保存时满足。
		allSupplied := map[string]bool{"description": true, "testData": true, "preconditions": true, "estimatedMinutes": true, "requirementId": true}
		metadata, err = a.normalizeTestingCaseMetadata(r.Context(), tx, &next, metadata, allSupplied)
		if err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
	}
	if _, supplied := patch["requirementId"]; supplied || metadataPatch.RequirementSet {
		// PATCH 未携带 requirementId 时允许历史脏关联继续保留，避免无关编辑被
		// 阻断；一旦客户端试图设置关联，便必须以当前事务内的作用域校验为准。
		if err = a.validateTestCaseRequirement(r.Context(), tx, next.RequirementID); err != nil {
			var invalid *organizationError
			if errors.As(err, &invalid) {
				fail(w, invalid.Status, invalid.Code, invalid.Message)
			} else {
				fail(w, 500, "db_error", err.Error())
			}
			return
		}
	}
	for key := range patch {
		if key == "owner" || key == "ownerUserId" {
			continue
		}
		if col := allowed[key]; col != "" {
			_, err = tx.Exec(`UPDATE test_cases SET `+col+`=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, payload[key], now, id, tenantID, a.pid())
			if err != nil {
				break
			}
		}
	}
	if err == nil && (metadataPatch.PreconditionsSet || metadataPatch.RequirementSet) {
		_, err = tx.Exec(`UPDATE test_cases SET preconditions=?,requirement_id=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, next.Preconditions, next.RequirementID, now, id, tenantID, a.pid())
	}
	if err == nil && ownerChanged {
		_, err = tx.Exec(`UPDATE test_cases SET owner=?,owner_user_id=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, next.Owner, next.OwnerUserID, now, id, tenantID, a.pid())
	}
	_, detail := patch["stepsDetail"]
	_, steps := patch["steps"]
	_, expected := patch["expected"]
	if err == nil && (detail || steps || expected) {
		_, err = tx.Exec(`UPDATE test_cases SET steps_json=? WHERE id=? AND tenant_id=? AND project_id=?`, jsonText(next.StepsDetail), id, tenantID, a.pid())
	}
	if err == nil {
		err = a.writeObjectFields(tx, "test_case", id, fields, now)
	}
	if err == nil && metadataChanged {
		err = a.writeTestingCaseMetadata(r.Context(), tx, id, metadata, now)
	}
	if err == nil {
		next.ID = id
		next.Code = current.Code
		next.UpdatedAt = now
		err = a.recordTestCaseTrace(r.Context(), tx, next, actor, "updated", "更新了测试用例")
	}
	if err == nil && ownerChanged && next.OwnerUserID != "" {
		err = a.writeAssignmentNotices(r.Context(), tx, []assignmentNotice{{
			Event:      "test_case.owner_changed",
			Subject:    "test_case",
			SubjectID:  id,
			Field:      "owner",
			Title:      "你被指定为测试用例负责人",
			Body:       strings.TrimSpace(current.Code + " " + next.Title),
			Recipients: []string{next.OwnerUserID},
		}}, now)
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
	saved, err := a.getTestCase(id)
	if err != nil {
		fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, saved)
}

func (a *App) validatePlan(p *TestPlan, oldSprint string, checkOwner, checkExecutor bool) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 200 {
		return fmt.Errorf("计划名称须为 1–200 字")
	}
	if p.Status == "" {
		p.Status = "规划中"
	}
	if !validChoice(p.Status, []string{"规划中", "执行中", "已完成", "已取消"}) {
		return fmt.Errorf("测试计划状态无效")
	}
	if (p.StartDate != "" || p.EndDate != "") && !validDateRange(p.StartDate, p.EndDate) {
		return fmt.Errorf("测试计划日期范围无效")
	}
	if checkOwner {
		if err := a.normalizePlanOwner(p); err != nil {
			return err
		}
	}
	if checkExecutor && p.ExecutorUserID != "" {
		var n int
		err := a.db.QueryRow(`SELECT COUNT(*) FROM project_members pm JOIN users u ON u.id=pm.user_id AND u.tenant_id=pm.tenant_id JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE pm.tenant_id=? AND pm.project_id=? AND pm.user_id=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), p.ExecutorUserID).Scan(&n)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("执行人不是当前项目的有效成员")
		}
	}
	sprint, err := a.resolveRequirementSprint(p.Sprint, p.Sprint != oldSprint)
	if err != nil {
		return err
	}
	p.Sprint = sprint
	seen := map[int64]bool{}
	unique := []int64{}
	if len(p.CaseIDs) > 1000 {
		return fmt.Errorf("一个测试计划最多关联 1000 个用例")
	}
	for _, id := range p.CaseIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM test_cases WHERE tenant_id=? AND project_id=? AND id=? AND enabled=1 AND status!='已废弃'`, tenantID, a.pid(), id).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("关联用例不属于当前项目、已停用或不存在")
		}
		unique = append(unique, id)
	}
	p.CaseIDs = unique
	return nil
}

// normalizePlanOwner accepts legacy owner names for compatibility, but turns
// every new or changed plan owner into the stable project-member ID used by
// notifications.  When a client supplies an ID, its canonical name wins so a
// stale or ambiguous display name cannot redirect a notification.
func (a *App) normalizePlanOwner(p *TestPlan) error {
	if p.OwnerUserID == "" {
		uid, err := a.projectPerson(p.Owner)
		if err != nil {
			return err
		}
		p.OwnerUserID = uid
		return nil
	}
	var canonical string
	err := a.db.QueryRow(`SELECT u.name FROM users u
		JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id
		JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
		WHERE u.tenant_id=? AND pm.project_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), p.OwnerUserID).Scan(&canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("负责人不是当前项目的有效成员，请重新选择")
	}
	if err != nil {
		return err
	}
	p.Owner = canonical
	return nil
}

func (a *App) createPlanAtomic(w http.ResponseWriter, r *http.Request) {
	var p TestPlan
	if decodeJSON(r, &p) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if err := a.validatePlan(&p, "", true, true); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	if p.Environment == "" {
		p.Environment = "测试环境"
	}
	actor := a.actorName()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO test_plans(tenant_id,project_id,code,name,sprint,version,scope,owner,owner_user_id,start_date,end_date,status,environment,executor_user_id,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", p.Name, p.Sprint, p.Version, p.Scope, p.Owner, p.OwnerUserID, p.StartDate, p.EndDate, p.Status, p.Environment, p.ExecutorUserID, now, now)
	if err == nil {
		p.ID, err = res.LastInsertId()
	}
	p.Code = fmt.Sprintf("TP-%03d", p.ID)
	if err == nil {
		_, err = tx.Exec(`UPDATE test_plans SET code=? WHERE id=? AND tenant_id=? AND project_id=?`, p.Code, p.ID, tenantID, a.pid())
	}
	for _, cid := range p.CaseIDs {
		if err != nil {
			break
		}
		var n int
		err = tx.QueryRow(`SELECT COUNT(*) FROM test_cases WHERE tenant_id=? AND project_id=? AND id=? AND enabled=1 AND status!='已废弃'`, tenantID, a.pid(), cid).Scan(&n)
		if err == nil && n != 1 {
			err = fmt.Errorf("关联用例不属于当前项目、已停用或不存在")
		}
		if err != nil {
			break
		}
		_, err = tx.Exec(`INSERT INTO test_plan_cases VALUES(?,?,?,?)`, tenantID, a.pid(), p.ID, cid)
		if err == nil {
			_, err = tx.Exec(`INSERT INTO test_executions(tenant_id,project_id,plan_id,case_id,executor_user_id,updated_at)VALUES(?,?,?,?,?,?)`, tenantID, a.pid(), p.ID, cid, p.ExecutorUserID, now)
		}
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'test_plan',?,?,'created',?,?)`, tenantID, a.pid(), p.ID, actor, "创建了测试计划", now)
	}
	if err == nil {
		notices := []assignmentNotice{}
		if p.OwnerUserID != "" {
			notices = append(notices, assignmentNotice{
				Event:      "test_plan.owner_assigned",
				Subject:    "test_plan",
				SubjectID:  p.ID,
				Field:      "ownerUserId",
				Title:      "你被指定为测试计划负责人",
				Body:       strings.TrimSpace(p.Code + " " + p.Name),
				Recipients: []string{p.OwnerUserID},
			})
		}
		if p.ExecutorUserID != "" {
			notices = append(notices, assignmentNotice{
				Event:      "test_plan.executor_assigned",
				Subject:    "test_plan",
				SubjectID:  p.ID,
				Field:      "executorUserId",
				Title:      "你被指定为测试计划执行人",
				Body:       strings.TrimSpace(p.Code + " " + p.Name),
				Recipients: []string{p.ExecutorUserID},
			})
		}
		err = a.writeAssignmentNotices(r.Context(), tx, notices, now)
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
	p.UpdatedAt = now
	write(w, 201, p)
}

func (a *App) patchPlanAtomic(w http.ResponseWriter, r *http.Request, id int64) {
	var current TestPlan
	err := a.db.QueryRow(`SELECT name,sprint,version,scope,owner,owner_user_id,start_date,end_date,status,environment,executor_user_id FROM test_plans WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&current.Name, &current.Sprint, &current.Version, &current.Scope, &current.Owner, &current.OwnerUserID, &current.StartDate, &current.EndDate, &current.Status, &current.Environment, &current.ExecutorUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "计划不存在")
		} else {
			fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
		}
		return
	}
	var patch map[string]json.RawMessage
	if decodeJSON(r, &patch) != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	next := current
	targets := map[string]*string{"name": &next.Name, "sprint": &next.Sprint, "version": &next.Version, "scope": &next.Scope, "owner": &next.Owner, "ownerUserId": &next.OwnerUserID, "startDate": &next.StartDate, "endDate": &next.EndDate, "status": &next.Status, "environment": &next.Environment, "executorUserId": &next.ExecutorUserID}
	for key, raw := range patch {
		if target := targets[key]; target != nil {
			if string(raw) == "null" || json.Unmarshal(raw, target) != nil {
				fail(w, 422, "validation_error", "字段格式无效")
				return
			}
		}
	}
	_, ownerNameSupplied := patch["owner"]
	_, ownerIDSupplied := patch["ownerUserId"]
	// A name-only patch only resolves when it changes the displayed owner.  If a
	// historical owner has since been disabled, an unrelated edit may still send
	// the unchanged legacy name and must retain its stable ID rather than fail or
	// accidentally bind a same-name active member. Explicit ownerUserId remains
	// an intentional current assignment and is always validated.
	ownerNeedsValidation := ownerIDSupplied || (ownerNameSupplied && next.Owner != current.Owner)
	if ownerNeedsValidation && ownerNameSupplied && !ownerIDSupplied {
		next.OwnerUserID = ""
	}
	if err := a.validatePlan(&next, current.Sprint, ownerNeedsValidation, next.ExecutorUserID != current.ExecutorUserID); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	ownerChanged := next.Owner != current.Owner || next.OwnerUserID != current.OwnerUserID
	allowed := map[string]string{"name": "name", "sprint": "sprint", "version": "version", "scope": "scope", "owner": "owner", "ownerUserId": "owner_user_id", "startDate": "start_date", "endDate": "end_date", "status": "status", "environment": "environment", "executorUserId": "executor_user_id"}
	actor := a.actorName()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		if failCustomFieldValidation(w, err) {
			return
		}
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	// Validate against the same snapshot we replace, not a seconds-resolution
	// timestamp. This also prevents two individually valid date patches from
	// combining into an invalid range after one writer commits first.
	keys := []string{}
	for key := range patch {
		if allowed[key] != "" {
			keys = append(keys, key)
		}
	}
	if ownerChanged {
		// Name and ID form a single identity pair; update both atomically even
		// when the caller supplied only one of the backward-compatible fields.
		if !ownerNameSupplied {
			keys = append(keys, "owner")
		}
		if !ownerIDSupplied {
			keys = append(keys, "ownerUserId")
		}
	}
	sort.Strings(keys)
	sets := []string{"updated_at=?"}
	args := []any{now}
	for _, key := range keys {
		sets = append(sets, allowed[key]+"=?")
		args = append(args, *targets[key])
	}
	args = append(args, id, tenantID, a.pid(), current.Name, current.Sprint, current.Version, current.Scope, current.Owner, current.OwnerUserID, current.StartDate, current.EndDate, current.Status, current.Environment, current.ExecutorUserID)
	result, err := tx.Exec(`UPDATE test_plans SET `+strings.Join(sets, ",")+` WHERE id=? AND tenant_id=? AND project_id=? AND name=? AND sprint=? AND version=? AND scope=? AND owner=? AND owner_user_id=? AND start_date=? AND end_date=? AND status=? AND environment=? AND executor_user_id=?`, args...)
	if err == nil {
		var affected int64
		affected, err = result.RowsAffected()
		if err == nil && affected != 1 {
			fail(w, 409, "edit_conflict", "测试计划已被其他操作修改，请刷新后重试")
			return
		}
	}
	if _, changed := patch["executorUserId"]; changed && err == nil {
		_, err = tx.Exec(`UPDATE test_executions SET executor_user_id=?,updated_at=? WHERE tenant_id=? AND project_id=? AND plan_id=? AND status='未执行'`, next.ExecutorUserID, now, tenantID, a.pid(), id)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'test_plan',?,?,'updated',?,?)`, tenantID, a.pid(), id, actor, "更新了测试计划", now)
	}
	if err == nil {
		notices := []assignmentNotice{}
		if ownerChanged && next.OwnerUserID != "" {
			notices = append(notices, assignmentNotice{
				Event:      "test_plan.owner_changed",
				Subject:    "test_plan",
				SubjectID:  id,
				Field:      "ownerUserId",
				Title:      "你被指定为测试计划负责人",
				Body:       strings.TrimSpace(fmt.Sprintf("TP-%03d %s", id, next.Name)),
				Recipients: []string{next.OwnerUserID},
			})
		}
		if _, executorChanged := patch["executorUserId"]; executorChanged && next.ExecutorUserID != "" && next.ExecutorUserID != current.ExecutorUserID {
			notices = append(notices, assignmentNotice{
				Event:      "test_plan.executor_changed",
				Subject:    "test_plan",
				SubjectID:  id,
				Field:      "executorUserId",
				Title:      "你被指定为测试计划执行人",
				Body:       strings.TrimSpace(fmt.Sprintf("TP-%03d %s", id, next.Name)),
				Recipients: []string{next.ExecutorUserID},
			})
		}
		err = a.writeAssignmentNotices(r.Context(), tx, notices, now)
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
	a.testPlan(w, httptestGet(fmt.Sprintf("/api/test-plans/%d", id)))
}
