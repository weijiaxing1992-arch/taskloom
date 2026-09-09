package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

type projectMemberCandidate struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Email      string  `json:"email"`
	Active     bool    `json:"active"`
	TenantRole string  `json:"tenantRole"`
	Role       *string `json:"role"`
}

func activeMemberProject(ctx context.Context, store stateStore, id string) error {
	var status string
	err := store.QueryRowContext(ctx, `SELECT status FROM projects WHERE tenant_id=? AND id=?`, tenantID, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status == "deleted" {
		return orgNotFound()
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return &organizationError{409, "project_inactive", "归档项目仅支持恢复或删除"}
	}
	return nil
}

func listProjectMemberCandidates(ctx context.Context, store stateStore, project string) ([]projectMemberCandidate, error) {
	rows, err := store.QueryContext(ctx, `SELECT u.id,u.name,u.email,(u.active=1 AND tm.status='active'),tm.role,pm.role FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=? WHERE u.tenant_id=? AND tm.status!='removed' ORDER BY u.name,u.id`, project, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []projectMemberCandidate{}
	for rows.Next() {
		var item projectMemberCandidate
		if err = rows.Scan(&item.ID, &item.Name, &item.Email, &item.Active, &item.TenantRole, &item.Role); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) manageProjectMembers(w http.ResponseWriter, r *http.Request, project string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	// 此处返回企业候选目录而非普通项目名单，仅有项目管理员角色不能访问。
	if a.impersonation != nil {
		failOrganization(w, &organizationError{403, "impersonation_restricted", "代访问期间不能修改账号安全或成员权限"})
		return
	}
	if r.Method == http.MethodGet {
		if values := r.URL.Query()["candidates"]; len(values) != 1 || values[0] != "1" {
			failOrganization(w, orgInvalid("请求格式不正确"))
			return
		}
		tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			failOrganization(w, err)
			return
		}
		defer tx.Rollback()
		admin, err := a.requireOrganizationPermission(r.Context(), tx, "members.manage")
		if err == nil {
			err = activeMemberProject(r.Context(), tx, project)
		}
		var items []projectMemberCandidate
		if err == nil {
			items, err = listProjectMemberCandidates(r.Context(), tx, project)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items, "canManage": true, "currentUserId": a.uid(), "isTenantAdmin": admin})
		return
	}
	var input struct {
		AddUserIDs    []string `json:"addUserIds"`
		RemoveUserIDs []string `json:"removeUserIds"`
		Role          string   `json:"role"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failOrganization(w, err)
		return
	}
	add, err := bulkMemberIDs(input.AddUserIDs)
	var remove []string
	if err == nil {
		remove, err = bulkMemberIDs(input.RemoveUserIDs)
	}
	if err == nil && (len(input.AddUserIDs)+len(input.RemoveUserIDs) > 200 || (input.Role != "" && input.Role != "viewer")) {
		err = orgInvalid("项目成员操作仅支持最多 200 名成员及只读角色")
	}
	seen := map[string]bool{}
	for _, id := range add {
		seen[id] = true
	}
	for _, id := range remove {
		if seen[id] {
			err = orgInvalid("同一成员不能同时添加和移除")
		}
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "members.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	if err = activeMemberProject(r.Context(), tx, project); err != nil {
		failOrganization(w, err)
		return
	}
	before, err := listProjectMemberCandidates(r.Context(), tx, project)
	if err != nil {
		failOrganization(w, err)
		return
	}
	candidates := map[string]projectMemberCandidate{}
	for _, item := range before {
		candidates[item.ID] = item
	}
	for _, id := range append(append([]string{}, add...), remove...) {
		if _, exists := candidates[id]; !exists {
			failOrganization(w, orgNotFound())
			return
		}
	}
	for _, id := range remove {
		item := candidates[id]
		if id == a.uid() || item.TenantRole == "tenant_admin" {
			failOrganization(w, &organizationError{409, "protected_member", "不能移除当前账号或企业管理员的项目权限"})
			return
		}
		// 沿用单人编辑的委派权限边界，禁止借移除成员降权项目管理员。
		if !admin && item.Role != nil && (*item.Role == "project_admin" || *item.Role == "tenant_admin") {
			failOrganization(w, orgForbidden())
			return
		}
		if !admin {
			var protected int
			if err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM organization_group_members WHERE tenant_id=? AND user_id=?) + (SELECT COUNT(*) FROM memberships WHERE tenant_id=? AND project_id=? AND user_id=? AND role IN ('project_admin','tenant_admin'))`, tenantID, id, tenantID, project, id).Scan(&protected); err != nil {
				failOrganization(w, err)
				return
			}
			if protected > 0 {
				failOrganization(w, orgForbidden())
				return
			}
		}
	}
	now := orgNow()
	for _, id := range add {
		// 以 project_members 为准；仅存在旧授权时也保留原角色，不自动降为只读。
		role := "viewer"
		if candidates[id].Role != nil {
			role = *candidates[id].Role
		} else {
			err = tx.QueryRowContext(r.Context(), `SELECT role FROM memberships WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, project, id).Scan(&role)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				failOrganization(w, err)
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				role = "viewer"
			}
		}
		// 委派成员管理员不能恢复旧表中的管理员授权。
		if !admin && candidates[id].Role == nil && (role == "project_admin" || role == "tenant_admin") {
			failOrganization(w, orgForbidden())
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id) DO NOTHING`, tenantID, project, id, role, now, now)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id) DO UPDATE SET role=excluded.role`, tenantID, project, id, role)
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
	}
	for _, id := range remove {
		for _, table := range []string{"memberships", "project_members"} {
			if _, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, project, id); err != nil {
				failOrganization(w, err)
				return
			}
		}
	}
	after, err := listProjectMemberCandidates(r.Context(), tx, project)
	if err == nil && (len(add) > 0 || len(remove) > 0) {
		// 审计只记录成员 ID 和角色变化，不记录企业邮箱目录。
		beforeRoles := map[string]*string{}
		afterRoles := map[string]*string{}
		for _, id := range append(append([]string{}, add...), remove...) {
			beforeRoles[id] = candidates[id].Role
		}
		for _, item := range after {
			if _, changed := beforeRoles[item.ID]; changed {
				afterRoles[item.ID] = item.Role
			}
		}
		err = a.organizationAudit(r.Context(), tx, "project", project, "project_members_updated", beforeRoles, map[string]any{"addUserIds": add, "removeUserIds": remove, "roles": afterRoles})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	// 会话仍可用于其他项目；项目请求和外部凭据均实时复核成员授权，移除后立即失权。
	write(w, 200, map[string]any{"items": after, "canManage": true, "currentUserId": a.uid(), "isTenantAdmin": admin})
}
