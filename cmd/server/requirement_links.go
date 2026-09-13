package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

func (a *App) requirementLinks(w http.ResponseWriter, r *http.Request, id int64, parts []string) {
	if len(parts) == 0 && r.Method == http.MethodGet {
		if err := a.collaborationEntityExists(r.Context(), a.db, "requirement", id); err != nil {
			failCollaboration(w, err)
			return
		}
		states, err := listRequirementStatuses(r.Context(), a.db, tenantID, a.pid())
		if err != nil {
			failCollaboration(w, err)
			return
		}
		catalog := requirementCategoryMap(states)
		rows, err := a.db.QueryContext(r.Context(), `SELECT DISTINCT q.id,q.code,q.title,q.status FROM work_item_relations l JOIN requirements q ON q.tenant_id=l.tenant_id AND q.project_id=l.project_id AND q.id=CASE WHEN l.source_id=? THEN l.target_id ELSE l.source_id END WHERE l.tenant_id=? AND l.project_id=? AND l.source_type='requirement' AND l.target_type='requirement' AND l.relation_type='relates_to' AND (l.source_id=? OR l.target_id=?) ORDER BY q.id DESC`, id, tenantID, a.pid(), id, id)
		if err != nil {
			failCollaboration(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var linked int64
			var code, title, status string
			if err = rows.Scan(&linked, &code, &title, &status); err != nil {
				failCollaboration(w, err)
				return
			}
			state := catalog[status]
			items = append(items, map[string]any{"id": linked, "code": requirementDisplayCode(linked, code), "title": title, "status": status, "statusName": state.Name, "statusColor": state.Color, "statusCategory": state.Category, "statusSystem": state.System})
		}
		if err = rows.Err(); err != nil {
			failCollaboration(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	var target int64
	if len(parts) == 0 && r.Method == http.MethodPost {
		var body struct {
			RequirementID int64 `json:"requirementId"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		d.DisallowUnknownFields()
		if d.Decode(&body) != nil || d.Decode(&struct{}{}) != io.EOF {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		target = body.RequirementID
	} else if len(parts) == 1 && r.Method == http.MethodDelete {
		var err error
		target, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			fail(w, 422, "invalid_relation", "关联需求 ID 无效")
			return
		}
	} else {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if target <= 0 || target == id {
		fail(w, 422, "invalid_relation", "不能关联自身或无效的需求")
		return
	}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	defer tx.Rollback()
	for _, required := range []int64{id, target} {
		if err = a.collaborationEntityExists(r.Context(), tx, "requirement", required); err != nil {
			failCollaboration(w, err)
			return
		}
	}
	var count int
	err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM work_item_relations WHERE tenant_id=? AND project_id=? AND source_type='requirement' AND target_type='requirement' AND relation_type='relates_to' AND ((source_id=? AND target_id=?) OR (source_id=? AND target_id=?))`, tenantID, a.pid(), id, target, target, id).Scan(&count)
	changed := false
	if err == nil && r.Method == http.MethodPost && count == 0 {
		left, right := id, target
		if left > right {
			left, right = right, left
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO work_item_relations(tenant_id,project_id,source_type,source_id,target_type,target_id,relation_type,created_by,created_at)VALUES(?,?,'requirement',?,'requirement',?,'relates_to',?,?)`, tenantID, a.pid(), left, right, a.uid(), orgNow())
		changed = true
	}
	if err == nil && r.Method == http.MethodDelete && count > 0 {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM work_item_relations WHERE tenant_id=? AND project_id=? AND source_type='requirement' AND target_type='requirement' AND relation_type='relates_to' AND ((source_id=? AND target_id=?) OR (source_id=? AND target_id=?))`, tenantID, a.pid(), id, target, target, id)
		changed = true
	}
	if err == nil && changed {
		action := "requirement_linked"
		if r.Method == http.MethodDelete {
			action = "requirement_unlinked"
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,'requirement',?,?,?,?)`, tenantID, a.pid(), a.uid(), fmt.Sprint(id), action, jsonText(map[string]any{"requirementId": target, "relationType": "relates_to"}), orgNow())
		if err == nil {
			for _, pair := range [][2]int64{{id, target}, {target, id}} {
				var code, title string
				err = tx.QueryRowContext(r.Context(), `SELECT code,title FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), pair[1]).Scan(&code, &title)
				if err != nil {
					break
				}
				value := map[string]any{"id": pair[1], "code": requirementDisplayCode(pair[1], code), "title": title, "relationType": "relates_to"}
				var before, after any
				if r.Method == http.MethodDelete {
					before = value
				} else {
					after = value
				}
				if err = a.recordRequirementRelatedChange(tx, pair[0], "relatedRequirement", action, "关联需求", before, after); err != nil {
					break
				}
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failCollaboration(w, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost && changed {
		status = 201
	}
	write(w, status, map[string]any{"requirementId": target, "linked": r.Method == http.MethodPost})
}
