package main

import (
	"database/sql"
	"fmt"
)

// These are candidate constraints, not authorization grants. They are derived
// from stable field keys; renaming a field cannot change its meaning, and the
// request body cannot broaden a preset's member roles.
func memberRolesForKey(key string) []string {
	switch key {
	case "ownerUserIds", "role.product.userIds":
		return []string{"product"}
	case "role.frontend.userIds":
		return []string{"frontend", "frontend_lead"}
	case "role.backend.userIds":
		return []string{"backend", "backend_lead"}
	case "role.algorithm.userIds":
		return []string{"algorithm"}
	case "role.ui.userIds":
		return []string{"ui"}
	case "frontend_leads":
		return []string{"frontend_lead"}
	case "backend_leads":
		return []string{"backend_lead"}
	case "managers":
		return []string{"tenant_admin", "project_admin"}
	case "testers":
		return []string{"qa"}
	}
	return []string{}
}

func fieldMemberRoles(d FieldDefinition) []string {
	if d.ObjectType == "requirement" && (d.Type == "user" || d.Type == "users") {
		return memberRolesForKey(d.Key)
	}
	return []string{}
}

func (a *App) validateFieldMemberRole(q fieldQueryer, d FieldDefinition, id string) error {
	roles := fieldMemberRoles(d)
	if len(roles) == 0 {
		return nil
	}
	var role, tenantRole, rawRoles string
	if err := q.QueryRow(`SELECT pm.role,tm.role,`+projectRolesJSONSQL("pm")+` FROM project_members pm JOIN tenant_memberships tm ON tm.tenant_id=pm.tenant_id AND tm.user_id=pm.user_id WHERE pm.tenant_id=? AND pm.project_id=? AND pm.user_id=?`, tenantID, a.pid(), id).Scan(&role, &tenantRole, &rawRoles); err != nil {
		return err
	}
	if rolesOverlap(decodedProjectRoles(rawRoles, role), roles) || (tenantRole == "tenant_admin" && validChoice("tenant_admin", roles)) {
		return nil
	}
	return customFieldValidationError{fmt.Sprintf("%s 的新增成员必须符合字段限定角色", d.Name)}
}

func (a *App) migrateDefaultTestersField() error {
	return seedDefaultTestersField(a.db, "")
}

func seedDefaultTestersField(q interface {
	Exec(string, ...any) (sql.Result, error)
}, project string) error {
	// Add the missing optional field only; never enable/relabel/replace an
	// existing definition or touch its saved values or department constraint.
	_, err := q.Exec(`INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id,created_at,updated_at)
SELECT p.tenant_id,p.id,'requirement','testers','测试人员','users','可绑定多位测试项目成员；可按真实部门限定候选范围',0,0,1,1,1,140,'null','[]','',?,?
FROM projects p WHERE (?='' OR p.id=?) AND NOT EXISTS(SELECT 1 FROM field_definitions f WHERE f.tenant_id=p.tenant_id AND f.project_id=p.id AND f.object_type='requirement' AND (f.key='testers' OR trim(f.name)='测试人员'))`, orgNow(), orgNow(), project, project)
	return err
}
