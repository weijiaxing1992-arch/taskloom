package main

import "strings"

// 仅由内部固定表名/别名调用；搜索词始终作为参数，不拼入 SQL。
// 姓名取稳定账号的当前名称，部门只按账号关系匹配，绝不通过同名猜测部门。
// 聚合子查询不展开主表行，保证多人、多部门不会重复结果或影响分页总数。
func workItemPeopleSearchSQL(object, alias string) string {
	ids := `SELECT @.assignee_user_id UNION ALL SELECT @.verifier_user_id`
	legacy := `@.assignee||' '||@.verifier`
	if object == "requirement" {
		legacy = `@.assignee||' '||@.owner`
		ids = `SELECT @.assignee_user_id UNION ALL SELECT @.owner_user_id
 UNION ALL SELECT value FROM json_each(CASE WHEN json_valid(@.assignee_user_ids_json) THEN @.assignee_user_ids_json ELSE '[]' END)
 UNION ALL SELECT value FROM json_each(CASE WHEN json_valid(@.owner_user_ids_json) THEN @.owner_user_ids_json ELSE '[]' END)
 UNION ALL SELECT json_extract(CASE WHEN role.type='object' THEN role.value ELSE '{}' END,'$.userId')
 FROM json_each(CASE WHEN json_valid(@.role_weights_json) THEN @.role_weights_json ELSE '{}' END) role
 UNION ALL SELECT member.value FROM json_each(CASE WHEN json_valid(@.role_weights_json) THEN @.role_weights_json ELSE '{}' END) role,
 json_each(json_extract(CASE WHEN role.type='object' THEN role.value ELSE '{}' END,'$.userIds')) member`
	}
	// 仅启用且未删除的人员字段参与检索；普通文本中的账号 ID 不当作人员关系。
	ids += ` UNION ALL SELECT member.value FROM field_values fv JOIN field_definitions fd
 ON fd.id=fv.field_definition_id AND fd.tenant_id=fv.tenant_id AND fd.project_id=fv.project_id AND fd.object_type=fv.object_type
 JOIN json_each(CASE WHEN json_valid(fv.value_json) THEN fv.value_json ELSE '[]' END) member
 WHERE fv.tenant_id=@.tenant_id AND fv.project_id=@.project_id AND fv.object_type='` + object + `' AND fv.object_id=@.id
 AND fd.enabled=1 AND fd.deleted_at='' AND fd.type IN ('user','users')`
	return strings.ReplaceAll(`(`+legacy+`||' '||COALESCE((SELECT group_concat(person.name||' '||COALESCE((
 SELECT group_concat(dep.name,' ') FROM department_memberships dm JOIN departments dep
 ON dep.tenant_id=dm.tenant_id AND dep.id=dm.department_id
 WHERE dm.tenant_id=person.tenant_id AND dm.user_id=person.id AND dm.status='active' AND dep.status='active'
 ),''),' ') FROM users person WHERE person.tenant_id=@.tenant_id AND person.id IN (`+ids+`)),''))`, "@", alias)
}
