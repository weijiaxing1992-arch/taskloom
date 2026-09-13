package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 企业工作量报表按“所属迭代结束日期”归属月份，读取当前状态/人员/部门快照，
// 不是历史某天实际完成或上线的事件统计。Snapshot=true 必须随接口保留，避免图表误导。
type WorkloadMetrics struct {
	Weight                  float64 `json:"weight"`
	RequirementCount        int     `json:"requirementCount"`
	ShippedRequirementCount int     `json:"shippedRequirementCount"`
	DefectCount             int     `json:"defectCount"`
	EstimatedRoleCount      int     `json:"estimatedRoleCount"`
	UnestimatedRoleCount    int     `json:"unestimatedRoleCount"`
}
type WorkloadRole struct {
	Key string `json:"key"`
	WorkloadMetrics
}
type WorkloadPerson struct {
	UserID         string `json:"userId"`
	Name           string `json:"name"`
	Active         bool   `json:"active"`
	DepartmentID   string `json:"departmentId"`
	DepartmentName string `json:"departmentName"`
	WorkloadMetrics
	Roles []WorkloadRole `json:"roles"`
}
type WorkloadDepartment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"memberCount"`
	WorkloadMetrics
}
type WorkloadReport struct {
	Month            string `json:"month"`
	StartDate        string `json:"startDate"`
	EndDateExclusive string `json:"endDateExclusive"`
	GeneratedAt      string `json:"generatedAt"`
	Snapshot         bool   `json:"snapshot"`
	Precision        int    `json:"precision"`
	Scope            struct {
		Type         string `json:"type"`
		TenantID     string `json:"tenantId"`
		TenantName   string `json:"tenantName"`
		ProjectCount int    `json:"projectCount"`
	} `json:"scope"`
	SprintCount                 int                  `json:"sprintCount"`
	Totals                      WorkloadMetrics      `json:"totals"`
	People                      []WorkloadPerson     `json:"people"`
	Departments                 []WorkloadDepartment `json:"departments"`
	Roles                       []WorkloadRole       `json:"roles"`
	UnassignedWeight            float64              `json:"unassignedWeight"`
	UnassignedRequirementCount  int                  `json:"unassignedRequirementCount"`
	UnassignedDefectCount       int                  `json:"unassignedDefectCount"`
	UnestimatedRequirementCount int                  `json:"unestimatedRequirementCount"`
	AmbiguousSprintItemCount    int                  `json:"ambiguousSprintItemCount"`
}
type workloadAccumulator struct {
	// 权重用有理数累加，输出时统一舍入；工作项与角色计数使用集合去重，
	// 同一个人跨职能参与、同部门多人协作不能导致需求/缺陷数量重复累计。
	weight                         *big.Rat
	requirements, shipped, defects map[int64]bool
	estimated, unestimated         map[string]bool
}

func newWorkloadAccumulator() *workloadAccumulator {
	return &workloadAccumulator{new(big.Rat), map[int64]bool{}, map[int64]bool{}, map[int64]bool{}, map[string]bool{}, map[string]bool{}}
}
func (a *workloadAccumulator) requirement(id int64, shipped bool) {
	a.requirements[id] = true
	if shipped {
		a.shipped[id] = true
	}
}
func (a *workloadAccumulator) estimate(key string, value *big.Rat) {
	if value == nil {
		a.unestimated[key] = true
	} else {
		a.estimated[key] = true
		a.weight.Add(a.weight, value)
	}
}
func (a *workloadAccumulator) metrics() WorkloadMetrics {
	return WorkloadMetrics{roundedSprintWeight(a.weight), len(a.requirements), len(a.shipped), len(a.defects), len(a.estimated), len(a.unestimated)}
}

type workloadPersonState struct {
	row    WorkloadPerson
	totals *workloadAccumulator
	roles  map[string]*workloadAccumulator
}
type workloadDepartmentState struct {
	row     WorkloadDepartment
	totals  *workloadAccumulator
	members map[string]bool
}
type workloadSprint struct {
	id                 int64
	project, name, end string
	projectName        string
	selected           bool
}

// workloadProjectScope is an explicit, server-derived project allow-list for
// personal and team reports. A nil scope is reserved for the existing
// organization-wide report, which is separately protected by reports.view.
// An empty non-nil scope deliberately matches no projects instead of falling
// back to the organization, so a revoked membership can never widen a report.
type workloadProjectScope struct {
	ProjectIDs []string
}

var workloadMonthPattern = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

func workloadMonthRange(month string) (string, string, error) {
	if !workloadMonthPattern.MatchString(month) {
		return "", "", fmt.Errorf("invalid month")
	}
	start, err := time.Parse("2006-01", month)
	if err != nil || start.Year() < 1 || start.Year() > 9998 {
		return "", "", fmt.Errorf("invalid month")
	}
	return start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02"), nil
}

func normalizedWorkloadProjectScope(scope *workloadProjectScope) *workloadProjectScope {
	if scope == nil {
		return nil
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(scope.ProjectIDs))
	for _, id := range scope.ProjectIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return &workloadProjectScope{ProjectIDs: ids}
}

func workloadProjectFilter(scope *workloadProjectScope, column string) (string, []any) {
	if scope == nil {
		return "", nil
	}
	if len(scope.ProjectIDs) == 0 {
		return " AND 1=0", nil
	}
	placeholders := make([]string, len(scope.ProjectIDs))
	args := make([]any, len(scope.ProjectIDs))
	for i, id := range scope.ProjectIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	return " AND " + column + " IN (" + strings.Join(placeholders, ",") + ")", args
}

// 此接口有意忽略当前项目选择，属于企业级统计，必须先验证真实 reports.view 企业授权。
// 不可改成“任意项目成员即可查看企业全量”；只读事务同时保证授权和聚合读取一致。
func (a *App) workloadReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	if _, err = a.requireOrganizationPermission(r.Context(), tx, "reports.view"); err != nil {
		failOrganization(w, err)
		return
	}
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	start, end, err := workloadMonthRange(month)
	if err != nil {
		fail(w, 422, "invalid_month", "月份必须为有效的 YYYY-MM 格式")
		return
	}
	report, err := a.monthlyWorkload(r.Context(), tx, month, start, end)
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, report)
}

func (a *App) monthlyWorkload(ctx context.Context, q stateStore, month, start, end string, collectors ...*workloadTrendCollector) (WorkloadReport, error) {
	return a.monthlyWorkloadScoped(ctx, q, month, start, end, nil, collectors...)
}

func (a *App) monthlyWorkloadForProjects(ctx context.Context, q stateStore, month, start, end string, scope *workloadProjectScope, collectors ...*workloadTrendCollector) (WorkloadReport, error) {
	return a.monthlyWorkloadScoped(ctx, q, month, start, end, normalizedWorkloadProjectScope(scope), collectors...)
}

func (a *App) monthlyWorkloadScoped(ctx context.Context, q stateStore, month, start, end string, scope *workloadProjectScope, collectors ...*workloadTrendCollector) (WorkloadReport, error) {
	// 月表与趋势共用一次聚合读取；collector 只旁路收集相同口径，不能另建一套分摊规则。
	var trend *workloadTrendCollector
	if len(collectors) > 0 {
		trend = collectors[0]
	}
	out := WorkloadReport{Month: month, StartDate: start, EndDateExclusive: end, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Snapshot: true, Precision: 6, People: []WorkloadPerson{}, Departments: []WorkloadDepartment{}, Roles: []WorkloadRole{}}
	out.Scope.Type = "organization"
	out.Scope.TenantID = tenantID
	if err := q.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id=?`, tenantID).Scan(&out.Scope.TenantName); err != nil {
		return out, err
	}
	// 同项目完整迭代名称优先，其次唯一旧前缀别名；跨月份也参与歧义判断。
	// 跨项目同名不合并，待规划不计入月份，歧义条目单独计数而非猜测归属。
	sprints := map[string][]workloadSprint{}
	aliases := map[string][]workloadSprint{}
	projects := map[string]bool{}
	projectClause, projectArgs := workloadProjectFilter(scope, "p.id")
	rows, err := q.QueryContext(ctx, `SELECT s.id,s.project_id,s.name,s.end_date,p.name FROM sprints s JOIN projects p ON p.tenant_id=s.tenant_id AND p.id=s.project_id WHERE s.tenant_id=? AND p.status IN ('active','archived')`+projectClause, append([]any{tenantID}, projectArgs...)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s workloadSprint
		if err = rows.Scan(&s.id, &s.project, &s.name, &s.end, &s.projectName); err != nil {
			rows.Close()
			return out, err
		}
		if s.end >= start && s.end < end && (trend == nil || trend.selectedIDs == nil || trend.selectedIDs[s.id]) {
			date, e := time.Parse("2006-01-02", s.end)
			if e != nil || date.Format("2006-01-02") != s.end {
				rows.Close()
				return out, fmt.Errorf("invalid sprint end date")
			}
			s.selected = true
			out.SprintCount++
			projects[s.project] = true
			if trend != nil {
				trend.addSprint(s)
			}
		}
		key := s.project + "\x00" + s.name
		sprints[key] = append(sprints[key], s)
		_, alias := sprintAliases(s.name)
		aliases[s.project+"\x00"+alias] = append(aliases[s.project+"\x00"+alias], s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Scope.ProjectCount = len(projects)
	matchSprint := func(project, name string) (bool, bool, *workloadSprint) {
		if name == "" || name == "待规划" {
			return false, false, nil
		}
		matches := sprints[project+"\x00"+name]
		if len(matches) == 0 {
			matches = aliases[project+"\x00"+name]
		}
		if len(matches) == 1 {
			return matches[0].selected, false, &matches[0]
		}
		for _, s := range matches {
			if s.selected {
				return false, true, nil
			}
		}
		return false, false, nil
	}

	// 目录展示当前姓名/部门，不伪造历史组织快照；活动标志只作展示，不丢弃历史贡献。
	directory := map[string]WorkloadPerson{}
	rows, err = q.QueryContext(ctx, `SELECT u.id,u.name,u.active,COALESCE(tm.status,'inactive') FROM users u LEFT JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=?`, tenantID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p WorkloadPerson
		var active bool
		var status string
		if err = rows.Scan(&p.UserID, &p.Name, &active, &status); err != nil {
			rows.Close()
			return out, err
		}
		p.Active = active && status == "active"
		p.Roles = []WorkloadRole{}
		directory[p.UserID] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = q.QueryContext(ctx, `SELECT dm.user_id,d.id,d.name FROM department_memberships dm JOIN departments d ON d.tenant_id=dm.tenant_id AND d.id=dm.department_id WHERE dm.tenant_id=? AND dm.status='active' ORDER BY dm.is_primary DESC,d.sort_order,d.id`, tenantID)
	// 多部门人员只归属首个主部门（同优先级按排序/ID兜底），避免企业总权重重复。
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, department, name string
		if err = rows.Scan(&id, &department, &name); err != nil {
			rows.Close()
			return out, err
		}
		if p, ok := directory[id]; ok && p.DepartmentID == "" {
			p.DepartmentID = department
			p.DepartmentName = name
			directory[id] = p
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// 部门筛选必须展示企业目录的完整集合，而不能只展示本月恰好有工作量的人。
	// 这里保留停用/历史部门，既能让统计筛选与企业组织页一致，也不会让已有
	// 历史贡献在部门被调整后突然消失；无工作量部门自然以零指标返回。
	departments := map[string]*workloadDepartmentState{}
	rows, err = q.QueryContext(ctx, `SELECT id,name FROM departments WHERE tenant_id=? ORDER BY sort_order,name,id`, tenantID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return out, err
		}
		departments[id] = &workloadDepartmentState{row: WorkloadDepartment{ID: id, Name: name}, totals: newWorkloadAccumulator(), members: map[string]bool{}}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// 成员数是当前主部门的人数，不能因为本月未参与工作项而被错误地归零。
	for id, person := range directory {
		if department := departments[person.DepartmentID]; department != nil {
			department.members[id] = true
		}
	}
	ends := map[string]map[string]bool{}
	if trend != nil {
		trend.directory = directory
	}
	for project := range projects {
		flow, e := loadRequirementWorkflow(ctx, q, tenantID, project)
		if e != nil {
			return out, e
		}
		ends[project] = map[string]bool{}
		for _, key := range flow.EndStatuses {
			ends[project][key] = true
		}
	}
	totals := newWorkloadAccumulator()
	unassigned := new(big.Rat)
	people := map[string]*workloadPersonState{}
	roles := map[string]*workloadAccumulator{}
	for _, role := range requirementWeightRoles {
		roles[role] = newWorkloadAccumulator()
	}
	personFor := func(id string) (*workloadPersonState, error) {
		if p := people[id]; p != nil {
			return p, nil
		}
		row, ok := directory[id]
		if !ok {
			return nil, fmt.Errorf("unknown tenant member in workload binding")
		}
		p := &workloadPersonState{row, newWorkloadAccumulator(), map[string]*workloadAccumulator{}}
		people[id] = p
		if departments[row.DepartmentID] == nil {
			// 无主部门或已被彻底移除的历史部门仍需保留贡献；真实目录部门已在上方预载。
			departments[row.DepartmentID] = &workloadDepartmentState{row: WorkloadDepartment{ID: row.DepartmentID, Name: row.DepartmentName}, totals: newWorkloadAccumulator(), members: map[string]bool{}}
		}
		departments[row.DepartmentID].members[id] = true
		return p, nil
	}
	roleFor := func(p *workloadPersonState, key string) *workloadAccumulator {
		if p.roles[key] == nil {
			p.roles[key] = newWorkloadAccumulator()
		}
		if roles[key] == nil {
			roles[key] = newWorkloadAccumulator()
		}
		return p.roles[key]
	}
	rows, err = q.QueryContext(ctx, `SELECT r.id,r.project_id,r.sprint,r.status,r.role_weights_json,COALESCE(rs.category,'') FROM requirements r JOIN projects p ON p.tenant_id=r.tenant_id AND p.id=r.project_id LEFT JOIN requirement_statuses rs ON rs.tenant_id=r.tenant_id AND rs.project_id=r.project_id AND rs.key=r.status WHERE r.tenant_id=? AND p.status IN ('active','archived')`+projectClause, append([]any{tenantID}, projectArgs...)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		var project, sprint, status, raw, category string
		if err = rows.Scan(&id, &project, &sprint, &status, &raw, &category); err != nil {
			rows.Close()
			return out, err
		}
		selected, ambiguous, matchedSprint := matchSprint(project, sprint)
		if ambiguous {
			out.AmbiguousSprintItemCount++
		}
		if !selected {
			continue
		}
		if category == "" {
			rows.Close()
			return out, fmt.Errorf("missing requirement status")
		}
		shipped := category == "done" && ends[project][status]
		// “上线数”沿用已完成类别且为项目工作流终态的口径；取消/拒绝即便是终态也不算。
		totals.requirement(id, shipped)
		var weights map[string]struct {
			UserID  string          `json:"userId"`
			UserIDs []string        `json:"userIds"`
			Value   json.RawMessage `json:"value"`
		}
		if err = json.Unmarshal([]byte(raw), &weights); err != nil {
			rows.Close()
			return out, err
		}
		for key := range weights {
			if !validChoice(key, requirementWeightRoles) {
				rows.Close()
				return out, fmt.Errorf("invalid weight role")
			}
		}
		participants := map[string]bool{}
		estimated := false
		trendRoles := []workloadTrendRole{}
		for _, role := range requirementWeightRoles {
			// 五职能难度独立取值，不混入 estimatedHours；显式 0 是已估算，null 是未估算。
			entry := weights[role]
			var value *big.Rat
			if len(entry.Value) > 0 && strings.TrimSpace(string(entry.Value)) != "null" {
				var ok bool
				value, ok = new(big.Rat).SetString(strings.TrimSpace(string(entry.Value)))
				if !ok || value.Sign() < 0 || value.Cmp(big.NewRat(1_000_000, 1)) > 0 {
					rows.Close()
					return out, fmt.Errorf("invalid weight")
				}
			}
			key := fmt.Sprintf("%d:%s", id, role)
			if value != nil {
				estimated = true
			}
			ids := entry.UserIDs
			if ids == nil && entry.UserID != "" {
				ids = []string{entry.UserID}
			}
			unique := []string{}
			seen := map[string]bool{}
			for _, uid := range ids {
				if uid == "" || strings.TrimSpace(uid) != uid {
					rows.Close()
					return out, fmt.Errorf("invalid weight member")
				}
				if !seen[uid] {
					seen[uid] = true
					unique = append(unique, uid)
				}
			}
			if value == nil && len(unique) == 0 {
				continue
			}
			if trend != nil {
				trendRoles = append(trendRoles, workloadTrendRole{role, value, unique})
			}
			totals.estimate(key, value)
			roles[role].requirement(id, shipped)
			roles[role].estimate(key, value)
			if len(unique) == 0 {
				if value != nil {
					unassigned.Add(unassigned, value)
				}
				continue
			}
			var share *big.Rat
			// 同职能多人均分该职能权重，不按人数放大总权重；无人员权重单列 unassigned。
			if value != nil {
				share = new(big.Rat).Quo(value, big.NewRat(int64(len(unique)), 1))
			}
			for _, uid := range unique {
				p, e := personFor(uid)
				if e != nil {
					rows.Close()
					return out, e
				}
				participants[uid] = true
				p.totals.requirement(id, shipped)
				p.totals.estimate(key, share)
				r := roleFor(p, role)
				r.requirement(id, shipped)
				r.estimate(key, share)
				d := departments[p.row.DepartmentID]
				d.totals.requirement(id, shipped)
				d.totals.estimate(key, share)
			}
		}
		if len(participants) == 0 {
			out.UnassignedRequirementCount++
		}
		if !estimated {
			out.UnestimatedRequirementCount++
		}
		if trend != nil {
			trend.addRequirement(*matchedSprint, id, shipped, trendRoles)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = q.QueryContext(ctx, `SELECT d.id,d.project_id,d.sprint,d.assignee_user_id,d.discipline FROM defects d JOIN projects p ON p.tenant_id=d.tenant_id AND p.id=d.project_id WHERE d.tenant_id=? AND p.status IN ('active','archived')`+projectClause, append([]any{tenantID}, projectArgs...)...)
	// 缺陷按处理人稳定 ID 和职能计数，不计需求难度；未知职能进入 other，未分配单列。
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		var project, sprint, uid, role string
		if err = rows.Scan(&id, &project, &sprint, &uid, &role); err != nil {
			rows.Close()
			return out, err
		}
		selected, ambiguous, matchedSprint := matchSprint(project, sprint)
		if ambiguous {
			out.AmbiguousSprintItemCount++
		}
		if !selected {
			continue
		}
		totals.defects[id] = true
		if !validChoice(role, requirementWeightRoles) {
			role = "other"
		}
		if trend != nil {
			trend.addDefect(*matchedSprint, id, uid, role)
		}
		if roles[role] == nil {
			roles[role] = newWorkloadAccumulator()
		}
		roles[role].defects[id] = true
		if uid == "" {
			out.UnassignedDefectCount++
			continue
		}
		p, e := personFor(uid)
		if e != nil {
			rows.Close()
			return out, e
		}
		p.totals.defects[id] = true
		roleFor(p, role).defects[id] = true
		departments[p.row.DepartmentID].totals.defects[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, p := range people {
		p.row.WorkloadMetrics = p.totals.metrics()
		for key, acc := range p.roles {
			p.row.Roles = append(p.row.Roles, WorkloadRole{key, acc.metrics()})
		}
		sort.Slice(p.row.Roles, func(i, j int) bool { return p.row.Roles[i].Key < p.row.Roles[j].Key })
		out.People = append(out.People, p.row)
	}
	for _, d := range departments {
		d.row.MemberCount = len(d.members)
		d.row.WorkloadMetrics = d.totals.metrics()
		out.Departments = append(out.Departments, d.row)
	}
	for key, acc := range roles {
		out.Roles = append(out.Roles, WorkloadRole{key, acc.metrics()})
	}
	sort.Slice(out.People, func(i, j int) bool {
		if out.People[i].Weight != out.People[j].Weight {
			return out.People[i].Weight > out.People[j].Weight
		}
		return out.People[i].UserID < out.People[j].UserID
	})
	sort.Slice(out.Departments, func(i, j int) bool {
		if out.Departments[i].Weight != out.Departments[j].Weight {
			return out.Departments[i].Weight > out.Departments[j].Weight
		}
		return out.Departments[i].ID < out.Departments[j].ID
	})
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i].Key < out.Roles[j].Key })
	out.Totals = totals.metrics()
	out.UnassignedWeight = roundedSprintWeight(unassigned)
	return out, nil
}
