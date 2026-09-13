package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// 只使用调用方的只读事务：鉴权、树遍历、评论、人员和关联资源观察同一数据库快照。
func (a *App) readRequirementExport(ctx context.Context, store stateStore, root int64, limits requirementExportLimits) (requirementExportDocument, error) {
	out := requirementExportDocument{SchemaVersion: "devflow.requirement-export.v1", ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), ContentTrust: requirementExportTrust}
	if err := a.requireOperationAccess(ctx, store); err != nil {
		return out, err
	}
	if err := a.dependencyProjectAllowed(ctx, store, a.pid()); err != nil {
		return out, err
	}
	b := &requirementExportReader{ctx: ctx, store: store, project: a.pid(), limits: limits, data: map[string][]map[string]any{}}
	var projectName, projectCode, projectRole, tenantRole string
	if err := store.QueryRowContext(ctx, `SELECT p.name,p.code,COALESCE(pm.role,''),tm.role FROM projects p JOIN tenant_memberships tm ON tm.tenant_id=p.tenant_id AND tm.user_id=? LEFT JOIN project_members pm ON pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=tm.user_id WHERE p.tenant_id=? AND p.id=?`, a.uid(), tenantID, a.pid()).Scan(&projectName, &projectCode, &projectRole, &tenantRole); err != nil {
		return out, err
	}
	auditAllowed := tenantRole == "tenant_admin" || projectRole == "project_admin"
	var err error
	load := func(section, table, where string, args ...any) {
		if err == nil {
			err = b.table(section, table, where, args...)
		}
	}
	query := func(section, statement string, args ...any) {
		if err == nil {
			err = b.query(section, statement, args...)
		}
	}
	// UNION 对已访问 ID 去重防止脏循环无限递归；随后单独验证层级，绝不伪装成正常树。
	load("requirements", "requirements", `t.id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM requirements WHERE tenant_id=? AND project_id=? AND id=? UNION SELECT c.id FROM requirements c JOIN tree p ON c.parent_id=p.id WHERE c.tenant_id=? AND c.project_id=?) SELECT id FROM tree LIMIT ?)`, tenantID, a.pid(), root, tenantID, a.pid(), limits.Requirements+1)
	if err != nil {
		return out, err
	}
	if len(b.data["requirements"]) == 0 {
		return out, sql.ErrNoRows
	}
	if len(b.data["requirements"]) > limits.Requirements {
		return out, errRequirementExportBudget
	}
	if err = validateRequirementExportTree(b.data["requirements"]); err != nil {
		return out, err
	}
	requirementIDs := exportIDs(b.data["requirements"], "id")
	load("requirementComments", "comments", "t.requirement_id IN (SELECT value FROM json_each(?))", requirementIDs)
	load("checklists", "checklist_items", "t.requirement_id IN (SELECT value FROM json_each(?))", requirementIDs)
	query("requirementActivities", `SELECT a.id,a.requirement_id,a.actor,a.event,a.detail,a.created_at,COALESCE(h.actor_id,'') AS actor_id,COALESCE(u.name,'') AS actor_name,h.before_json,h.after_json,COALESCE(h.iteration_delay,0) AS iteration_delay,h.source_audit_id FROM activities a LEFT JOIN requirement_activity_history h ON h.activity_id=a.id AND h.tenant_id=a.tenant_id AND h.project_id=a.project_id AND h.requirement_id=a.requirement_id LEFT JOIN users u ON u.tenant_id=a.tenant_id AND u.id=CASE WHEN COALESCE(h.actor_id,'')<>'' THEN h.actor_id ELSE a.actor END WHERE a.tenant_id=? AND a.project_id=? AND a.requirement_id IN (SELECT value FROM json_each(?)) ORDER BY a.id`, tenantID, a.pid(), requirementIDs)
	if err == nil {
		err = enrichRequirementExportHistory(b)
	}
	load("requirementAttachments", "requirement_attachments", "t.requirement_id IN (SELECT value FROM json_each(?))", requirementIDs)
	load("designLinks", "requirement_design_links", "t.requirement_id IN (SELECT value FROM json_each(?))", requirementIDs)
	load("testingDesigns", "testing_designs", "t.requirement_id IN (SELECT value FROM json_each(?))", requirementIDs)
	designIDs := exportIDs(b.data["testingDesigns"], "id")
	load("testingDesignPoints", "testing_design_points", "t.design_id IN (SELECT value FROM json_each(?))", designIDs)
	pointIDs := exportIDs(b.data["testingDesignPoints"], "id")
	load("testingDesignPointCases", "testing_design_point_cases", `t.point_id IN (SELECT value FROM json_each(?)) AND EXISTS(SELECT 1 FROM test_cases c WHERE c.tenant_id=t.tenant_id AND c.project_id=t.project_id AND c.id=t.case_id)`, pointIDs)
	pointCaseIDs := exportIDs(b.data["testingDesignPointCases"], "caseId")
	// 设计点挂接是独立关联，不要求用例的直接 requirement_id 指向这棵需求树。
	load("testCases", "test_cases", "t.requirement_id IN (SELECT value FROM json_each(?)) OR t.id IN (SELECT value FROM json_each(?))", requirementIDs, pointCaseIDs)
	caseIDs := exportIDs(b.data["testCases"], "id")
	load("caseMetadata", "testing_case_metadata", "t.case_id IN (SELECT value FROM json_each(?))", caseIDs)
	load("caseLocations", "testing_case_locations", `t.case_id IN (SELECT value FROM json_each(?)) AND EXISTS(SELECT 1 FROM testing_libraries l WHERE l.tenant_id=t.tenant_id AND l.project_id=t.project_id AND l.id=t.library_id)`, caseIDs)
	load("caseHistory", "testing_case_history", "t.case_id IN (SELECT value FROM json_each(?))", caseIDs)
	load("caseReviews", "testing_case_reviews", "t.case_id IN (SELECT value FROM json_each(?))", caseIDs)
	libraryIDs := exportIDs(b.data["caseLocations"], "libraryId")
	folderIDs := exportIDs(b.data["caseLocations"], "folderId")
	load("caseLibraries", "testing_libraries", "t.id IN (SELECT value FROM json_each(?))", libraryIDs)
	load("caseFolders", "testing_folders", `t.id IN (WITH RECURSIVE folders(id) AS (SELECT id FROM testing_folders WHERE tenant_id=? AND project_id=? AND id IN (SELECT value FROM json_each(?)) AND library_id IN (SELECT value FROM json_each(?)) UNION SELECT p.id FROM testing_folders p JOIN testing_folders c ON c.parent_id=p.id AND c.library_id=p.library_id JOIN folders f ON f.id=c.id WHERE p.tenant_id=? AND p.project_id=? AND c.tenant_id=p.tenant_id AND c.project_id=p.project_id) SELECT id FROM folders)`, tenantID, a.pid(), folderIDs, libraryIDs, tenantID, a.pid())
	load("testExecutions", "test_executions", `t.case_id IN (SELECT value FROM json_each(?)) AND EXISTS(SELECT 1 FROM test_plans p WHERE p.tenant_id=t.tenant_id AND p.project_id=t.project_id AND p.id=t.plan_id)`, caseIDs)
	executionIDs := exportIDs(b.data["testExecutions"], "id")
	load("executionHistory", "test_execution_history", "t.execution_id IN (SELECT value FROM json_each(?))", executionIDs)
	load("testPlanCases", "test_plan_cases", `t.case_id IN (SELECT value FROM json_each(?)) AND EXISTS(SELECT 1 FROM test_plans p WHERE p.tenant_id=t.tenant_id AND p.project_id=t.project_id AND p.id=t.plan_id)`, caseIDs)
	planIDs := exportIDs(b.data["testPlanCases"], "planId")
	executionPlanIDs := exportIDs(b.data["testExecutions"], "planId")
	load("testPlans", "test_plans", "t.id IN (SELECT value FROM json_each(?)) OR t.id IN (SELECT value FROM json_each(?))", planIDs, executionPlanIDs)
	planIDs = exportIDs(b.data["testPlans"], "id")
	load("defects", "defects", "t.requirement_id IN (SELECT value FROM json_each(?)) OR t.source_execution_id IN (SELECT value FROM json_each(?)) OR t.id IN (SELECT value FROM json_each(?))", requirementIDs, executionIDs, exportIDs(b.data["testExecutions"], "defectId"))
	defectIDs := exportIDs(b.data["defects"], "id")
	load("sprints", "sprints", `EXISTS(SELECT 1 FROM requirements q WHERE q.tenant_id=t.tenant_id AND q.project_id=t.project_id AND q.id IN (SELECT value FROM json_each(?)) AND q.sprint=t.name)`, requirementIDs)
	sprintIDs := exportIDs(b.data["sprints"], "id")
	objectFilter := `(t.object_type='requirement' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?))) OR (t.object_type='test_case' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?))) OR (t.object_type='test_plan' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?))) OR (t.object_type='test_execution' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?))) OR (t.object_type='defect' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?))) OR (t.object_type='sprint' AND CAST(t.object_id AS INTEGER) IN (SELECT value FROM json_each(?)))`
	objects := []any{requirementIDs, caseIDs, planIDs, executionIDs, defectIDs, sprintIDs}
	load("entityComments", "entity_comments", objectFilter, objects...)
	load("entityActivities", "entity_activities", objectFilter, objects...)
	b.data["auditHistory"] = []map[string]any{}
	if auditAllowed {
		load("auditHistory", "audit_logs", objectFilter, objects...)
	}
	load("legacyAttachmentMetadata", "attachments", "t.deleted_at IS NULL AND ("+objectFilter+")", objects...)
	load("customFieldValues", "field_values", "("+objectFilter+") AND EXISTS(SELECT 1 FROM field_definitions d WHERE d.tenant_id=t.tenant_id AND d.project_id=t.project_id AND d.object_type=t.object_type AND d.id=t.field_definition_id AND d.deleted_at='')", objects...)
	definitionIDs := exportIDs(b.data["customFieldValues"], "fieldDefinitionId")
	load("customFieldDefinitions", "field_definitions", "t.deleted_at='' AND t.id IN (SELECT value FROM json_each(?))", definitionIDs)
	load("requirementStatuses", "requirement_statuses", "t.key IN (SELECT DISTINCT status FROM requirements WHERE tenant_id=? AND project_id=? AND id IN (SELECT value FROM json_each(?)))", tenantID, a.pid(), requirementIDs)
	load("relations", "work_item_relations", `t.source_type='requirement' AND t.target_type='requirement' AND (t.source_id IN (SELECT value FROM json_each(?)) OR t.target_id IN (SELECT value FROM json_each(?))) AND EXISTS(SELECT 1 FROM requirements q WHERE q.tenant_id=t.tenant_id AND q.project_id=t.project_id AND q.id=t.source_id) AND EXISTS(SELECT 1 FROM requirements q WHERE q.tenant_id=t.tenant_id AND q.project_id=t.project_id AND q.id=t.target_id)`, requirementIDs, requirementIDs)
	// 依赖双方必须同企业、项目活动且当前身份可读；不输出被拒绝端的 ID、标题或数量。
	query("dependencies", `SELECT d.id,d.source_tenant_id,d.source_project_id,d.source_requirement_id,d.target_tenant_id,d.target_project_id,d.target_requirement_id,d.relation_type,d.created_by,d.created_at,d.updated_at,sr.code AS source_code,sr.title AS source_title,tr.code AS target_code,tr.title AS target_title
 FROM requirement_dependencies d JOIN requirements sr ON sr.tenant_id=d.source_tenant_id AND sr.project_id=d.source_project_id AND sr.id=d.source_requirement_id JOIN requirements tr ON tr.tenant_id=d.target_tenant_id AND tr.project_id=d.target_project_id AND tr.id=d.target_requirement_id
 JOIN projects sp ON sp.tenant_id=d.source_tenant_id AND sp.id=d.source_project_id AND sp.status='active' JOIN projects tp ON tp.tenant_id=d.target_tenant_id AND tp.id=d.target_project_id AND tp.status='active'
 WHERE d.source_tenant_id=? AND d.target_tenant_id=? AND ((d.source_project_id=? AND d.source_requirement_id IN (SELECT value FROM json_each(?))) OR (d.target_project_id=? AND d.target_requirement_id IN (SELECT value FROM json_each(?))))
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.source_tenant_id AND pm.project_id=d.source_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.source_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))
 AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=d.target_tenant_id AND pm.project_id=d.target_project_id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=d.target_tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active')) ORDER BY d.id`, tenantID, tenantID, a.pid(), requirementIDs, a.pid(), requirementIDs, a.uid(), a.uid(), a.uid(), a.uid())
	// 关联、父需求和设计用例可能指向树外需求，只附可见摘要，不递归扩展其正文和讨论。
	query("relatedRequirementSummaries", `SELECT t.id,t.code,t.title,t.status,t.project_id FROM requirements t WHERE t.tenant_id=? AND t.project_id=? AND t.id NOT IN (SELECT value FROM json_each(?)) AND (t.id IN (SELECT parent_id FROM requirements WHERE tenant_id=? AND project_id=? AND id IN (SELECT value FROM json_each(?))) OR t.id IN (SELECT requirement_id FROM test_cases WHERE tenant_id=? AND project_id=? AND id IN (SELECT value FROM json_each(?))) OR t.id IN (SELECT value FROM json_each(?)) OR t.id IN (SELECT value FROM json_each(?))) ORDER BY t.id`, tenantID, a.pid(), requirementIDs, tenantID, a.pid(), requirementIDs, tenantID, a.pid(), caseIDs, exportIDs(b.data["relations"], "sourceId"), exportIDs(b.data["relations"], "targetId"))
	if err != nil {
		return out, err
	}
	if err = b.sanitizeReferences(); err != nil {
		return out, err
	}
	for _, row := range b.data["requirementAttachments"] {
		var sample []byte
		if err := store.QueryRowContext(ctx, `SELECT substr(content,1,8192) FROM requirement_attachments WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), row["id"]).Scan(&sample); err != nil {
			return out, err
		}
		row["category"], row["language"] = resolveAttachmentAsset(fmt.Sprint(row["name"]), fmt.Sprint(row["category"]), sample)
		row["downloadUrl"] = fmt.Sprintf("/api/requirements/%d/attachments/%d", row["requirementId"], row["id"])
		row["downloadHeaders"] = map[string]string{"X-TaskLoom-Project": a.pid()}
	}
	for _, row := range b.data["legacyAttachmentMetadata"] {
		row["downloadUrl"] = nil
		row["downloadUnavailableReason"] = "legacy_metadata_only"
	}
	enrichRequirementExportComments(b.data["requirementComments"], false)
	enrichRequirementExportComments(b.data["entityComments"], true)
	if err = a.enrichRequirementExport(ctx, store, b); err != nil {
		return out, err
	}
	normalizeRequirementExportCodes(b.data)
	counts := map[string]int{}
	for section, items := range b.data {
		counts[section] = len(items)
	}
	out.Source = map[string]any{"tenantId": tenantID, "projectId": a.pid(), "projectName": projectName, "projectCode": projectCode, "rootRequirementId": root, "path": fmt.Sprintf("/api/requirements/%d", root), "snapshot": "single_read_transaction", "fieldNaming": "camelCase; JSON columns are decoded; this document is not a write request"}
	out.Completeness = map[string]any{"status": "complete", "truncated": false, "scope": "authorized_requirement_tree_and_linked_test_records", "auditIncluded": auditAllowed, "counts": counts, "limits": map[string]int{"requirements": limits.Requirements, "rows": limits.Rows, "bytes": limits.Bytes}, "excludedCategories": []string{"unauthorized or cross-tenant records", "private drafts and private AI review caches", "credentials and robot configuration", "attachment binary contents", "unrelated cases/executions from shared test plans"}, "notes": []string{"所有纳入需求、用例、计划、执行、缺陷的公开评论和回复完整导出，未使用展示分页。", "测试计划本体和讨论完整；testPlanCases/testExecutions 仅包含所选用例，不代表计划全部测试范围。", "树外相关需求只提供摘要；跨项目依赖只提供双方有权读取的关系摘要。", "审计仅在现有项目审计权限允许时导出，敏感字段仍会脱敏。", "历史附件表没有受支持的下载接口时只输出元数据，不伪造下载地址。"}}
	out.Data = b.data
	return out, nil
}

// Export the same public history as the detail endpoint, in the caller's single
// read snapshot. A batch join avoids an extra query for every exported child.
func enrichRequirementExportHistory(b *requirementExportReader) error {
	groups := map[int64][]requirementHistoryItem{}
	rowsByID := map[int64]map[string]any{}
	for _, row := range b.data["requirementActivities"] {
		id := row["id"].(int64)
		before, beforeOK := row["before"].(map[string]any)
		after, afterOK := row["after"].(map[string]any)
		x := requirementHistoryItem{ID: id, Actor: fmt.Sprint(row["actor"]), ActorID: fmt.Sprint(row["actorId"]), ActorName: fmt.Sprint(row["actorName"]), Event: fmt.Sprint(row["event"]), Detail: fmt.Sprint(row["detail"]), CreatedAt: fmt.Sprint(row["createdAt"]), SnapshotAvailable: beforeOK && afterOK, Recovered: row["sourceAuditId"] != nil, IterationDelay: row["iterationDelay"] == int64(1)}
		populateRequirementHistory(&x, before, after)
		parent := row["requirementId"].(int64)
		groups[parent] = append(groups[parent], x)
		rowsByID[id] = row
	}
	for _, items := range groups {
		orderRequirementHistory(items)
		for _, item := range items {
			row := rowsByID[item.ID]
			previous, _ := json.Marshal(row)
			delete(row, "before")
			delete(row, "after")
			delete(row, "sourceAuditId")
			row["sprint"], row["status"], row["changes"] = item.Sprint, item.Status, item.Changes
			row["snapshotAvailable"], row["recovered"] = item.SnapshotAvailable, item.Recovered
			row["iterationDelay"], row["iterationDelayCount"] = item.IterationDelay, item.IterationDelayCount
			encoded, err := json.Marshal(row)
			if err != nil {
				return err
			}
			b.bytes += len(encoded) - len(previous)
			if b.bytes > b.limits.Bytes {
				return errRequirementExportBudget
			}
		}
	}
	return nil
}

func validateRequirementExportTree(items []map[string]any) error {
	parents := map[int64]int64{}
	children := map[int64][]int64{}
	for _, row := range items {
		id := row["id"].(int64)
		parent, _ := row["parentId"].(int64)
		parents[id] = parent
		children[parent] = append(children[parent], id)
	}
	for _, row := range items {
		id := row["id"].(int64)
		seen := map[int64]bool{}
		for current := id; current != 0; current = parents[current] {
			if seen[current] {
				return errRequirementExportHierarchy
			}
			seen[current] = true
		}
		row["childRequirementIds"] = children[id]
		if children[id] == nil {
			row["childRequirementIds"] = []int64{}
		}
	}
	return nil
}

func enrichRequirementExportComments(items []map[string]any, entity bool) {
	key := func(row map[string]any, id any) string {
		if entity {
			return fmt.Sprint(row["objectType"], "/", row["objectId"], "/", id)
		}
		return fmt.Sprint(row["requirementId"], "/", id)
	}
	byID := map[string]map[string]any{}
	for _, row := range items {
		byID[key(row, row["id"])] = row
	}
	for _, row := range items {
		row["replyToAuthor"] = ""
		row["replyToAuthorUserId"] = ""
		if parent := byID[key(row, row["replyToId"])]; parent != nil {
			row["replyToAuthor"] = parent["author"]
			row["replyToAuthorUserId"] = parent["authorUserId"]
		} else {
			row["replyToId"] = nil
		}
	}
}

func (a *App) enrichRequirementExport(ctx context.Context, store stateStore, b *requirementExportReader) error {
	// 仅按业务记录中保存的稳定 ID 查姓名；移除/停用账号仍可解释历史，不导出邮箱或账号目录。
	ids := map[string]bool{}
	var visit func(any, string)
	visit = func(value any, key string) {
		switch v := value.(type) {
		case map[string]any:
			for k, nested := range v {
				visit(nested, k)
			}
		case []any:
			for _, nested := range v {
				visit(nested, key)
			}
		case string:
			if strings.HasSuffix(key, "UserId") || strings.HasSuffix(key, "UserIds") || key == "userId" || key == "userIds" || key == "createdBy" || key == "actor" || key == "actorId" {
				if v != "" {
					ids[v] = true
				}
			}
		}
	}
	for section, items := range b.data {
		// 自定义值的结构可由用户输入，不能把普通 JSON 中名为 userIds 的键当成人员授权。
		if section == "customFieldValues" || section == "customFieldDefinitions" {
			continue
		}
		for _, row := range items {
			visit(row, "")
		}
	}
	memberFields := map[int64]bool{}
	for _, definition := range b.data["customFieldDefinitions"] {
		memberFields[definition["id"].(int64)] = definition["type"] == "user" || definition["type"] == "users"
	}
	for _, value := range b.data["customFieldValues"] {
		if !memberFields[value["fieldDefinitionId"].(int64)] {
			continue
		}
		switch users := value["value"].(type) {
		case string:
			if users != "" {
				ids[users] = true
			}
		case []any:
			for _, user := range users {
				if id, ok := user.(string); ok && id != "" {
					ids[id] = true
				}
			}
		}
	}
	peopleIDs := []string{}
	for id := range ids {
		peopleIDs = append(peopleIDs, id)
	}
	sort.Strings(peopleIDs)
	if err := b.query("people", `SELECT id,name FROM users WHERE tenant_id=? AND id IN (SELECT value FROM json_each(?)) ORDER BY id`, tenantID, jsonText(peopleIDs)); err != nil {
		return err
	}
	states := map[string]map[string]any{}
	for _, state := range b.data["requirementStatuses"] {
		states[fmt.Sprint(state["key"])] = state
	}
	for _, row := range b.data["requirements"] {
		row["statusName"] = row["status"]
		if state := states[fmt.Sprint(row["status"])]; state != nil {
			row["statusName"] = state["name"]
			row["statusColor"] = state["color"]
			row["statusCategory"] = state["category"]
			row["statusSystem"] = state["system"]
			row["isEnd"] = terminalCategory(fmt.Sprint(state["category"]))
		}
		weights, err := json.Marshal(row["roleWeights"])
		if err != nil {
			return err
		}
		var requirement Requirement
		if err = json.Unmarshal(weights, &requirement.RoleWeights); err != nil {
			return err
		}
		normalizeRequirementWeights(&requirement)
		row["weightTotal"] = requirement.WeightTotal
	}
	definitions := map[int64]string{}
	for _, definition := range b.data["customFieldDefinitions"] {
		definitions[definition["id"].(int64)] = fmt.Sprint(definition["key"])
	}
	values := map[string]map[string]any{}
	for _, value := range b.data["customFieldValues"] {
		key := fmt.Sprint(value["objectType"], "/", value["objectId"])
		if values[key] == nil {
			values[key] = map[string]any{}
		}
		values[key][definitions[value["fieldDefinitionId"].(int64)]] = value["value"]
	}
	for section, object := range map[string]string{"requirements": "requirement", "testCases": "test_case", "defects": "defect", "sprints": "sprint"} {
		for _, row := range b.data[section] {
			fields := values[fmt.Sprint(object, "/", row["id"])]
			if fields == nil {
				fields = map[string]any{}
			}
			row["customFields"] = fields
		}
	}
	return nil
}

// 沿用普通详情的关联隔离规则：异常外键不因根对象可读而获得跨项目可见性。
// 每个目标表来自固定清单；同项目合法的树外关联保留，外部或已不存在的引用清空。
func (b *requirementExportReader) sanitizeReferences() error {
	for _, reference := range []struct{ section, field, table string }{
		{"requirements", "parentId", "requirements"},
		{"testCases", "requirementId", "requirements"},
		{"defects", "requirementId", "requirements"},
		{"defects", "sourceExecutionId", "test_executions"},
		{"testExecutions", "defectId", "defects"},
	} {
		items := b.data[reference.section]
		rows, err := b.store.QueryContext(b.ctx, "SELECT id FROM "+reference.table+" WHERE tenant_id=? AND project_id=? AND id IN (SELECT value FROM json_each(?))", tenantID, b.project, exportIDs(items, reference.field))
		if err != nil {
			return err
		}
		allowed := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			allowed[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, item := range items {
			if id, ok := item[reference.field].(int64); ok && !allowed[id] {
				item[reference.field] = nil
			}
		}
	}
	// 用例目录和父目录还须属于相同的用例库，不能仅凭目录 ID 或项目相同就接受。
	folders := map[int64]map[string]any{}
	for _, folder := range b.data["caseFolders"] {
		folders[folder["id"].(int64)] = folder
	}
	for section, field := range map[string]string{"caseLocations": "folderId", "caseFolders": "parentId"} {
		for _, item := range b.data[section] {
			id, _ := item[field].(int64)
			folder := folders[id]
			if id != 0 && (folder == nil || folder["libraryId"] != item["libraryId"]) {
				item[field] = int64(0)
			}
		}
	}
	return nil
}
