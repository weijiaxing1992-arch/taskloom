package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
)

// 个人和组员工作量不能复用企业报表的授权。企业报表仍只允许 reports.view，
// 这里始终从当前有效账号、主部门、项目成员关系和显式组长角色推出最小可见范围。
type WorkloadPersonalScope struct {
	Type         string `json:"type"`
	TenantID     string `json:"tenantId"`
	TenantName   string `json:"tenantName"`
	ProjectCount int    `json:"projectCount"`
}

type WorkloadTeamAvailability struct {
	Available      bool   `json:"available"`
	DepartmentID   string `json:"departmentId,omitempty"`
	DepartmentName string `json:"departmentName,omitempty"`
	ProjectCount   int    `json:"projectCount,omitempty"`
	MemberCount    int    `json:"memberCount,omitempty"`
}

type WorkloadPersonalReport struct {
	Month            string                   `json:"month"`
	StartDate        string                   `json:"startDate"`
	EndDateExclusive string                   `json:"endDateExclusive"`
	GeneratedAt      string                   `json:"generatedAt"`
	Snapshot         bool                     `json:"snapshot"`
	Precision        int                      `json:"precision"`
	Scope            WorkloadPersonalScope    `json:"scope"`
	SprintCount      int                      `json:"sprintCount"`
	Person           WorkloadPerson           `json:"person"`
	Team             WorkloadTeamAvailability `json:"team"`
}

type WorkloadTeamScope struct {
	DepartmentID   string
	DepartmentName string
	ProjectIDs     []string
	Members        map[string]WorkloadPerson
}

type WorkloadTeamReport struct {
	Month            string                `json:"month"`
	StartDate        string                `json:"startDate"`
	EndDateExclusive string                `json:"endDateExclusive"`
	GeneratedAt      string                `json:"generatedAt"`
	Snapshot         bool                  `json:"snapshot"`
	Precision        int                   `json:"precision"`
	Scope            WorkloadPersonalScope `json:"scope"`
	SprintCount      int                   `json:"sprintCount"`
	People           []WorkloadPerson      `json:"people"`
}

func workloadRequestedMonth(r *http.Request) (string, string, string, error) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	start, end, err := workloadMonthRange(month)
	return month, start, end, err
}

func (a *App) workloadActivePerson(ctx context.Context, store stateStore) (WorkloadPerson, error) {
	person := WorkloadPerson{Roles: []WorkloadRole{}}
	err := store.QueryRowContext(ctx, `SELECT u.id,u.name FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'`, tenantID, a.uid()).Scan(&person.UserID, &person.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return person, orgForbidden()
	}
	if err != nil {
		return person, err
	}
	person.Active = true
	return person, nil
}

func workloadAccessibleActiveProjectIDs(ctx context.Context, store stateStore, user string) ([]string, error) {
	rows, err := store.QueryContext(ctx, `SELECT p.id FROM projects p WHERE p.tenant_id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active')) ORDER BY p.id`, tenantID, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func workloadPersonFromReport(report WorkloadReport, fallback WorkloadPerson) WorkloadPerson {
	for _, person := range report.People {
		if person.UserID == fallback.UserID {
			return person
		}
	}
	// 没有参与当月已授权项目时仍显式返回本人及全零指标；不能把“无数据”
	// 伪装成找不到账号，也不返回同部门或企业成员作为替代。
	return fallback
}

func workloadLeadTeamScope(ctx context.Context, store stateStore, user string) (*WorkloadTeamScope, error) {
	var scope WorkloadTeamScope
	err := store.QueryRowContext(ctx, `SELECT d.id,d.name FROM department_memberships dm JOIN departments d ON d.tenant_id=dm.tenant_id AND d.id=dm.department_id AND d.status='active' WHERE dm.tenant_id=? AND dm.user_id=? AND dm.status='active' AND dm.is_primary=1`, tenantID, user).Scan(&scope.DepartmentID, &scope.DepartmentName)
	if errors.Is(err, sql.ErrNoRows) {
		// 没有明确主部门时不猜测其团队边界。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	leadRoles := []any{tenantID, user, "frontend_lead", "backend_lead", "frontend_lead", "backend_lead"}
	rows, err := store.QueryContext(ctx, `SELECT DISTINCT pm.project_id FROM project_members pm JOIN projects p ON p.tenant_id=pm.tenant_id AND p.id=pm.project_id WHERE pm.tenant_id=? AND pm.user_id=? AND p.status='active' AND (pm.role IN (?,?) OR EXISTS(SELECT 1 FROM project_member_roles pr WHERE pr.tenant_id=pm.tenant_id AND pr.project_id=pm.project_id AND pr.user_id=pm.user_id AND pr.role IN (?,?))) ORDER BY pm.project_id`, leadRoles...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		scope.ProjectIDs = append(scope.ProjectIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(scope.ProjectIDs) == 0 {
		// 项目成员记录中的 frontend_lead/backend_lead 是唯一的组长授权来源；
		// 不能根据姓名、部门名称或普通工程师角色推断组长身份。
		return nil, nil
	}
	placeholders := make([]string, len(scope.ProjectIDs))
	args := make([]any, 0, 2+len(scope.ProjectIDs))
	args = append(args, tenantID, scope.DepartmentID)
	for i, id := range scope.ProjectIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	memberSQL := `SELECT DISTINCT u.id,u.name FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id JOIN department_memberships dm ON dm.tenant_id=u.tenant_id AND dm.user_id=u.id AND dm.status='active' AND dm.is_primary=1 WHERE u.tenant_id=? AND dm.department_id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active' AND EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id IN (` + strings.Join(placeholders, ",") + `)) ORDER BY u.id`
	rows, err = store.QueryContext(ctx, memberSQL, args...)
	if err != nil {
		return nil, err
	}
	scope.Members = map[string]WorkloadPerson{}
	for rows.Next() {
		member := WorkloadPerson{Active: true, DepartmentID: scope.DepartmentID, DepartmentName: scope.DepartmentName, Roles: []WorkloadRole{}}
		if err = rows.Scan(&member.UserID, &member.Name); err != nil {
			rows.Close()
			return nil, err
		}
		scope.Members[member.UserID] = member
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(scope.Members) == 0 {
		return nil, nil
	}
	return &scope, nil
}

func (a *App) personalWorkloadReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	person, err := a.workloadActivePerson(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	month, start, end, err := workloadRequestedMonth(r)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "invalid_month", "月份必须为有效的 YYYY-MM 格式")
		return
	}
	projects, err := workloadAccessibleActiveProjectIDs(r.Context(), tx, person.UserID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	report, err := a.monthlyWorkloadForProjects(r.Context(), tx, month, start, end, &workloadProjectScope{ProjectIDs: projects})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	team, err := workloadLeadTeamScope(r.Context(), tx, person.UserID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	out := WorkloadPersonalReport{Month: report.Month, StartDate: report.StartDate, EndDateExclusive: report.EndDateExclusive, GeneratedAt: report.GeneratedAt, Snapshot: report.Snapshot, Precision: report.Precision, SprintCount: report.SprintCount, Person: workloadPersonFromReport(report, person)}
	out.Scope = WorkloadPersonalScope{Type: "personal", TenantID: tenantID, TenantName: report.Scope.TenantName, ProjectCount: len(projects)}
	if team != nil {
		out.Team = WorkloadTeamAvailability{Available: true, DepartmentID: team.DepartmentID, DepartmentName: team.DepartmentName, ProjectCount: len(team.ProjectIDs), MemberCount: len(team.Members)}
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, http.StatusOK, out)
}

func (a *App) teamWorkloadReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	person, err := a.workloadActivePerson(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	month, start, end, err := workloadRequestedMonth(r)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "invalid_month", "月份必须为有效的 YYYY-MM 格式")
		return
	}
	team, err := workloadLeadTeamScope(r.Context(), tx, person.UserID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	if team == nil {
		fail(w, http.StatusForbidden, "team_workload_forbidden", "仅已配置主部门且拥有研发组长项目角色的成员可查看组员工作量")
		return
	}
	report, err := a.monthlyWorkloadForProjects(r.Context(), tx, month, start, end, &workloadProjectScope{ProjectIDs: team.ProjectIDs})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	people := make([]WorkloadPerson, 0, len(team.Members))
	for id, member := range team.Members {
		for _, candidate := range report.People {
			if candidate.UserID == id {
				member = candidate
				break
			}
		}
		people = append(people, member)
	}
	sort.Slice(people, func(i, j int) bool {
		if people[i].Weight != people[j].Weight {
			return people[i].Weight > people[j].Weight
		}
		return people[i].UserID < people[j].UserID
	})
	out := WorkloadTeamReport{Month: report.Month, StartDate: report.StartDate, EndDateExclusive: report.EndDateExclusive, GeneratedAt: report.GeneratedAt, Snapshot: report.Snapshot, Precision: report.Precision, SprintCount: report.SprintCount, People: people}
	out.Scope = WorkloadPersonalScope{Type: "team", TenantID: tenantID, TenantName: report.Scope.TenantName, ProjectCount: len(team.ProjectIDs)}
	w.Header().Set("Cache-Control", "no-store")
	write(w, http.StatusOK, out)
}
