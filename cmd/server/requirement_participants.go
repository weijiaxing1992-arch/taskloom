package main

import (
	"context"
	"database/sql"
	"encoding/json"
)

// 使用稳定账号标识识别所有职能协作人与人员字段；普通文本字段不推断身份。
// 停用或删除的字段不再定义参与关系，历史值仍保留在需求中。
const requirementCollaboratorSQL = `EXISTS (
 SELECT 1 FROM json_each(r.role_weights_json) role
 WHERE EXISTS(SELECT 1 FROM json_each(json_extract(role.value,'$.userIds')) ids WHERE ids.value=?)
 OR (json_type(role.value,'$.userIds') IS NULL AND json_extract(role.value,'$.userId')=?)
) OR EXISTS (
 SELECT 1 FROM field_values fv JOIN field_definitions fd ON fd.id=fv.field_definition_id
 AND fd.tenant_id=fv.tenant_id AND fd.project_id=fv.project_id AND fd.object_type=fv.object_type
 WHERE fv.tenant_id=r.tenant_id AND fv.project_id=r.project_id AND fv.object_type='requirement' AND fv.object_id=r.id
 AND fd.enabled=1 AND fd.deleted_at='' AND fd.type IN ('user','users')
 AND EXISTS(SELECT 1 FROM json_each(fv.value_json) ids WHERE ids.value=?)
)`

// 我的工作按当前正文/备注提及及已保存评论中的明确账号 ID 归属。
// 已读状态不影响参与关系，移除正文提及后不使用旧通知重新把需求加回来。
const requirementMentionedWorkSQL = `EXISTS (
 SELECT 1 FROM json_each(json_extract(r.text_mentions_json,'$.description')) person WHERE person.key=?
) OR EXISTS (
 SELECT 1 FROM json_each(json_extract(r.text_mentions_json,'$.remarks')) person WHERE person.key=?
) OR EXISTS (
 SELECT 1 FROM comments c,json_each(c.mention_user_ids_json) person
 WHERE c.tenant_id=r.tenant_id AND c.project_id=r.project_id AND c.requirement_id=r.id AND person.value=?
)`

func (a *App) requirementFieldParticipants(ctx context.Context, tx *sql.Tx, id int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT fv.value_json FROM field_values fv JOIN field_definitions fd ON fd.id=fv.field_definition_id AND fd.tenant_id=fv.tenant_id AND fd.project_id=fv.project_id AND fd.object_type=fv.object_type WHERE fv.tenant_id=? AND fv.project_id=? AND fv.object_type='requirement' AND fv.object_id=? AND fd.enabled=1 AND fd.deleted_at='' AND fd.type IN ('user','users') AND fd.key IN ('testers','operations_engineers','frontend_leads','backend_leads','managers')`, tenantID, a.pid(), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		switch value := value.(type) {
		case string:
			ids = append(ids, value)
		case []any:
			for _, item := range value {
				if id, ok := item.(string); ok {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids, rows.Err()
}
