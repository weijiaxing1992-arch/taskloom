package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

// The canonical membership and its legacy single role remain authoritative.
// Additional roles never create membership by themselves. Legacy role updates
// and deletions revoke additional roles, including writes by older clients.
func (a *App) migrateProjectMemberRoles() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS project_member_roles(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,role TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id,role));
CREATE TRIGGER IF NOT EXISTS project_member_roles_deleted AFTER DELETE ON project_members BEGIN DELETE FROM project_member_roles WHERE tenant_id=OLD.tenant_id AND project_id=OLD.project_id AND user_id=OLD.user_id; END;
CREATE TRIGGER IF NOT EXISTS project_member_roles_changed AFTER UPDATE OF role ON project_members BEGIN DELETE FROM project_member_roles WHERE tenant_id=OLD.tenant_id AND project_id=OLD.project_id AND user_id=OLD.user_id; END;`)
	return err
}

// Stable ordering chooses only an actually granted role for old clients. All
// role-specific permissions and candidate checks must use the complete set.
var projectRoleOrder = []string{"tenant_admin", "project_admin", "product", "frontend_lead", "backend_lead", "qa", "frontend", "backend", "algorithm", "ui", "viewer"}

func normalizedProjectRoles(roles []string, fallback string) ([]string, error) {
	if roles == nil {
		roles = []string{fallback}
	}
	if len(roles) == 0 || len(roles) > len(projectRoleOrder) {
		return nil, orgInvalid("至少选择一个有效的项目角色")
	}
	result := []string{}
	for _, role := range roles {
		if !validChoice(role, projectRoleOrder) {
			return nil, orgInvalid("项目或项目角色无效")
		}
		if !validChoice(role, result) {
			result = append(result, role)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		for _, role := range projectRoleOrder {
			if role == result[i] {
				return true
			}
			if role == result[j] {
				return false
			}
		}
		return false
	})
	return result, nil
}

func projectRolesJSONSQL(alias string) string {
	return `(SELECT json_group_array(role) FROM (SELECT ` + alias + `.role AS role UNION SELECT pr.role FROM project_member_roles pr WHERE pr.tenant_id=` + alias + `.tenant_id AND pr.project_id=` + alias + `.project_id AND pr.user_id=` + alias + `.user_id))`
}

func decodedProjectRoles(raw, fallback string) []string {
	var roles []string
	if json.Unmarshal([]byte(raw), &roles) != nil {
		roles = nil
	}
	result, err := normalizedProjectRoles(roles, fallback)
	if err != nil {
		return []string{}
	}
	return result
}

func memberProjectRoles(ctx context.Context, q stateStore, project, user string) ([]string, error) {
	var raw, role string
	err := q.QueryRowContext(ctx, `SELECT pm.role,`+projectRolesJSONSQL("pm")+` FROM project_members pm WHERE pm.tenant_id=? AND pm.project_id=? AND pm.user_id=?`, tenantID, project, user).Scan(&role, &raw)
	if err == sql.ErrNoRows {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return decodedProjectRoles(raw, role), nil
}

func rolesOverlap(roles, allowed []string) bool {
	for _, role := range roles {
		if validChoice(role, allowed) {
			return true
		}
	}
	return false
}

func (a *App) requirementStateRoles(ctx context.Context, q stateStore) ([]string, error) {
	role, err := a.requirementStateRole(ctx, q)
	if err != nil {
		return nil, err
	}
	roles, err := memberProjectRoles(ctx, q, a.pid(), a.uid())
	if err != nil {
		return nil, err
	}
	if role == "tenant_admin" && !validChoice(role, roles) {
		roles = append([]string{role}, roles...)
	}
	return roles, nil
}

func replaceProjectMemberRoles(ctx context.Context, tx *sql.Tx, project, user string, roles []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_member_roles WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, project, user); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_member_roles(tenant_id,project_id,user_id,role) VALUES(?,?,?,?)`, tenantID, project, user, role); err != nil {
			return err
		}
	}
	return nil
}

// Existing DF001-style employee numbers are monotonically allocated under the
// organization's write lock; removed accounts keep their original number.
func nextOrganizationEmployeeNo(ctx context.Context, tx *sql.Tx) (string, error) {
	var maximum int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(substr(employee_no,3) AS INTEGER)),0) FROM users WHERE tenant_id=? AND employee_no GLOB 'DF[0-9]*' AND substr(employee_no,3) NOT GLOB '*[^0-9]*'`, tenantID).Scan(&maximum)
	if err != nil {
		return "", err
	}
	if maximum == 9223372036854775807 {
		return "", orgConflict("工号序列已用尽")
	}
	return fmt.Sprintf("DF%03d", maximum+1), nil
}
