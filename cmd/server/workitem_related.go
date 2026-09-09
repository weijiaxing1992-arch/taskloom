package main

import (
	"regexp"
	"strings"
)

// 所有入口使用同一参与关系；账号由会话给出，展示姓名只兼容没有账号 ID 的历史数据。
// alias 和 object 仅由下面的白名单决定，不能使用请求参数拼接 SQL。
func workItemRelatedSQL(object, alias, uid string) (string, []any) {
	if object != "requirement" && object != "defect" {
		panic("unsupported related object")
	}
	if alias != "r" && alias != "d" && alias != "requirements" && alias != "defects" {
		panic("unsupported related alias")
	}
	name := `(SELECT name FROM users WHERE tenant_id=` + alias + `.tenant_id AND id=?)`
	var predicate string
	var args []any
	if object == "requirement" {
		predicate = `EXISTS(SELECT 1 FROM json_each(r.assignee_user_ids_json) ids WHERE ids.value=?) OR r.assignee_user_id=? OR (r.assignee_user_id='' AND json_array_length(r.assignee_user_ids_json)=0 AND r.assignee=` + name + `) OR EXISTS(SELECT 1 FROM json_each(r.owner_user_ids_json) ids WHERE ids.value=?) OR r.owner_user_id=? OR (r.owner_user_id='' AND json_array_length(r.owner_user_ids_json)=0 AND r.owner=` + name + `) OR ` + requirementCollaboratorSQL + ` OR ` + requirementMentionedWorkSQL + ` OR r.created_by=? OR EXISTS(SELECT 1 FROM comments c WHERE c.tenant_id=r.tenant_id AND c.project_id=r.project_id AND c.requirement_id=r.id AND c.author_user_id=?)`
		predicate = strings.ReplaceAll(predicate, "r.", alias+".")
		for i := 0; i < 14; i++ {
			args = append(args, uid)
		}
	} else {
		predicate = `d.assignee_user_id=? OR d.verifier_user_id=? OR (d.assignee_user_id='' AND d.assignee=` + name + `) OR (d.verifier_user_id='' AND d.verifier=` + name + `) OR d.created_by=? OR EXISTS(SELECT 1 FROM field_values fv JOIN field_definitions fd ON fd.id=fv.field_definition_id AND fd.tenant_id=fv.tenant_id AND fd.project_id=fv.project_id AND fd.object_type=fv.object_type WHERE fv.tenant_id=d.tenant_id AND fv.project_id=d.project_id AND fv.object_type='defect' AND fv.object_id=d.id AND fd.enabled=1 AND fd.deleted_at='' AND fd.type IN ('user','users') AND EXISTS(SELECT 1 FROM json_each(fv.value_json) ids WHERE ids.value=?)) OR EXISTS(SELECT 1 FROM entity_comments c WHERE c.tenant_id=d.tenant_id AND c.project_id=d.project_id AND c.object_type='defect' AND c.object_id=d.id AND (c.author_user_id=? OR EXISTS(SELECT 1 FROM json_each(c.mention_user_ids_json) ids WHERE ids.value=?)))`
		predicate = regexp.MustCompile(`\bd\.`).ReplaceAllString(predicate, alias+".")
		for i := 0; i < 8; i++ {
			args = append(args, uid)
		}
	}
	return "(" + predicate + ")", args
}
