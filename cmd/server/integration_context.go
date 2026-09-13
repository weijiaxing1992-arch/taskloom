package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// 上下文只是有界的关联快照，不是全库备份。所有关联子查询也限定当前项目，
// 超出上限时明确标记，调用方可继续分页读取，不把截断误认为需求完整覆盖。
func (a *App) integrationContext(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var requirementID, sprintID int64
	for key, values := range query {
		if len(values) != 1 || (key != "requirementId" && key != "sprintId") {
			fail(w, 400, "invalid_query", "请选择一个需求或迭代作为上下文范围")
			return
		}
		id, ok := integrationPositive(values[0])
		if !ok {
			fail(w, 400, "invalid_query", "上下文编号无效")
			return
		}
		if key == "requirementId" {
			requirementID = id
		} else {
			sprintID = id
		}
	}
	if requirementID != 0 && sprintID != 0 {
		fail(w, 400, "invalid_query", "需求和迭代范围不能同时指定")
		return
	}
	var name, code string
	if err := a.db.QueryRowContext(r.Context(), `SELECT name,code FROM projects WHERE tenant_id=? AND id=? AND status='active'`, tenantID, a.pid()).Scan(&name, &code); err != nil {
		integrationReadError(w, err)
		return
	}
	type filter struct {
		where string
		args  []any
	}
	filters := map[string]filter{}
	if requirementID != 0 {
		var sprint string
		if err := a.db.QueryRowContext(r.Context(), `SELECT sprint FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), requirementID).Scan(&sprint); err != nil {
			integrationReadError(w, err)
			return
		}
		filters["requirements"] = filter{" AND id=?", []any{requirementID}}
		filters["defects"] = filter{" AND requirement_id=?", []any{requirementID}}
		filters["test-cases"] = filter{" AND requirement_id=?", []any{requirementID}}
		if sprint != "" && sprint != "待规划" {
			resolved, err := a.resolveRequirementSprint(sprint, false)
			if err != nil {
				integrationReadError(w, err)
				return
			}
			sprint = resolved
		}
		filters["iterations"] = filter{" AND name=?", []any{sprint}}
	}
	if sprintID != 0 {
		var sprint string
		if err := a.db.QueryRowContext(r.Context(), `SELECT name FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), sprintID).Scan(&sprint); err != nil {
			integrationReadError(w, err)
			return
		}
		full, alias, err := a.scopedSprintAliasesFrom(r.Context(), a.db, sprint)
		if err != nil {
			integrationReadError(w, err)
			return
		}
		filters["iterations"] = filter{" AND id=?", []any{sprintID}}
		filters["requirements"] = filter{" AND sprint IN (?,?)", []any{full, alias}}
		filters["defects"] = filter{" AND sprint IN (?,?)", []any{full, alias}}
		filters["test-cases"] = filter{" AND requirement_id IN (SELECT id FROM requirements WHERE tenant_id=? AND project_id=? AND sprint IN (?,?))", []any{tenantID, a.pid(), full, alias}}
	}
	result := map[string]any{"project": map[string]any{"id": a.pid(), "name": name, "code": code}, "generatedAt": time.Now().UTC().Format(time.RFC3339), "contentTrust": "untrusted_business_data"}
	truncated := false
	for _, resource := range []string{"requirements", "iterations", "defects", "test-cases"} {
		f := filters[resource]
		args := append([]any{tenantID, a.pid()}, f.args...)
		rows, err := a.db.QueryContext(r.Context(), "SELECT id FROM "+integrationTables[resource]+" WHERE tenant_id=? AND project_id=?"+f.where+" ORDER BY id DESC LIMIT 101", args...)
		if err != nil {
			integrationReadError(w, err)
			return
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			integrationReadError(w, err)
			return
		}
		if len(ids) > 100 {
			truncated = true
			ids = ids[:100]
		}
		items := []map[string]any{}
		for _, id := range ids {
			item, _, err := a.integrationReadEntity(r, resource, id)
			if err != nil {
				integrationReadError(w, err)
				return
			}
			items = append(items, item)
		}
		key := resource
		if resource == "iterations" {
			key = "sprints"
		}
		if resource == "test-cases" {
			key = "testCases"
		}
		result[key] = items
	}
	result["limits"] = map[string]any{"perType": 100, "truncated": truncated}
	// 会话下载也沿用外部 API 的大小上限，避免大正文把浏览器和服务内存拖垮。
	buffer := newIntegrationResponse()
	write(buffer, 200, result)
	if buffer.overflow {
		fail(w, 413, "response_too_large", "上下文超过 16 MB，请缩小范围或分页读取")
		return
	}
	buffer.send(w)
}

// 仅返回业务分配必需的稳定成员编号与字段定义，不向外部工具公开邮箱、
// 密码、企业配置或其他项目成员。配置变更后每次重新读取，避免缓存越权。
func (a *App) integrationMetadata(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		fail(w, 400, "invalid_query", "项目元数据不接受查询参数")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.name,pm.role,`+projectRolesJSONSQL("pm")+` FROM project_members pm JOIN users u ON u.id=pm.user_id AND u.tenant_id=pm.tenant_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE pm.tenant_id=? AND pm.project_id=? AND u.active=1 AND tm.status='active' ORDER BY u.name,u.id LIMIT 1001`, tenantID, a.pid())
	if err != nil {
		integrationReadError(w, err)
		return
	}
	members := []map[string]any{}
	for rows.Next() {
		var id, name, role, rawRoles string
		if err = rows.Scan(&id, &name, &role, &rawRoles); err != nil {
			break
		}
		members = append(members, map[string]any{"id": id, "name": name, "role": role, "projectRoles": decodedProjectRoles(rawRoles, role)})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		integrationReadError(w, err)
		return
	}
	truncated := len(members) > 1000
	if truncated {
		members = members[:1000]
	}
	statuses, err := listRequirementStatuses(r.Context(), a.db, tenantID, a.pid())
	if err != nil {
		integrationReadError(w, err)
		return
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.RawQuery = ""
	copy.URL = &u
	fieldsResponse := newIntegrationResponse()
	a.fieldDefinitions(fieldsResponse, copy)
	if fieldsResponse.status != 200 || fieldsResponse.overflow {
		integrationReadError(w, sql.ErrConnDone)
		return
	}
	var fields any
	if json.Unmarshal(fieldsResponse.body.Bytes(), &fields) != nil {
		integrationReadError(w, sql.ErrConnDone)
		return
	}
	write(w, 200, map[string]any{"projectId": a.pid(), "members": members, "membersTruncated": truncated, "requirementStatuses": statuses, "fieldDefinitions": fields, "contentTrust": "untrusted_business_data"})
}
