package main

import (
	"context"
	"database/sql"
	"fmt"
)

func (a *App) verifyNewDefectPerson(tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users u JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND pm.project_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return requirementPeopleValidationError("负责人不是当前项目的有效成员，请重新选择")
	}
	return nil
}
func (a *App) verifyNewDefectReferences(tx *sql.Tx, d Defect) error {
	if _, err := tx.Exec(`UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		return err
	}
	if err := a.requireOperationAccess(context.Background(), tx); err != nil {
		return err
	}
	if err := verifyDefectSprint(tx, a.pid(), d.Sprint); err != nil {
		return err
	}
	for _, id := range []string{d.AssigneeUserID, d.VerifierUserID} {
		if err := a.verifyNewDefectPerson(tx, id); err != nil {
			return err
		}
	}
	if d.RequirementID != nil {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), *d.RequirementID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return requirementPeopleValidationError("关联需求不属于当前项目或不存在")
		}
	}
	return nil
}
func (a *App) verifyDefectPatchPeople(tx *sql.Tx, id int64, patch map[string]any) error {
	if _, err := tx.Exec(`UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		return err
	}
	if err := a.requireOperationAccess(context.Background(), tx); err != nil {
		return err
	}
	var assignee, verifier, status, sprint string
	if err := tx.QueryRow(`SELECT assignee_user_id,verifier_user_id,status,sprint FROM defects WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&assignee, &verifier, &status, &sprint); err != nil {
		return err
	}
	if next, ok := patch["status"].(string); ok && !canTransition(status, next, map[string][]string{status: defectTransitions(status)}) {
		return requirementPeopleValidationError(fmt.Sprintf("当前状态“%s”不能流转到“%v”", status, next))
	}
	if next, ok := patch["sprint"].(string); ok && next != sprint {
		if err := verifyDefectSprint(tx, a.pid(), next); err != nil {
			return err
		}
	}
	for key, old := range map[string]string{"assigneeUserId": assignee, "verifierUserId": verifier} {
		if raw, present := patch[key]; present {
			next, ok := raw.(string)
			if !ok {
				return requirementPeopleValidationError("字段格式无效")
			}
			if next != old {
				if err := a.verifyNewDefectPerson(tx, next); err != nil {
					return err
				}
			}
		}
	}
	if raw, present := patch["requirementId"]; present && raw != nil {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), raw).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return requirementPeopleValidationError("关联需求不属于当前项目或不存在")
		}
	}
	return nil
}

func verifyDefectSprint(tx *sql.Tx, project, name string) error {
	if name == "" || name == "待规划" {
		return nil
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM sprints WHERE tenant_id=? AND project_id=? AND name=? AND status IN ('规划中','进行中')`, tenantID, project, name).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return requirementPeopleValidationError("迭代不属于当前项目或不可再分配，请刷新后重新选择")
	}
	return nil
}
