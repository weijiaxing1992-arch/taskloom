package main

import (
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Collect personal role shares beside the canonical workload aggregation.
// No request-supplied identity, project or metric may widen the server scope.
type iterationAnalysisBucket struct {
	acc       *workloadAccumulator
	delivered *big.Rat
}
type iterationAnalysisCollector struct {
	user string
	rows map[string]map[string]map[int64]*iterationAnalysisBucket
}

func (c *iterationAnalysisCollector) add(s workloadSprint, id int64, shipped bool, roles []workloadTrendRole) {
	for _, role := range roles {
		for _, user := range role.users {
			if c.user != "" && user != c.user {
				continue
			}
			if c.rows[user] == nil {
				c.rows[user] = map[string]map[int64]*iterationAnalysisBucket{}
			}
			if c.rows[user][role.key] == nil {
				c.rows[user][role.key] = map[int64]*iterationAnalysisBucket{}
			}
			bucket := c.rows[user][role.key][s.id]
			if bucket == nil {
				bucket = &iterationAnalysisBucket{newWorkloadAccumulator(), new(big.Rat)}
				c.rows[user][role.key][s.id] = bucket
			}
			var share *big.Rat
			if role.value != nil {
				share = new(big.Rat).Quo(role.value, big.NewRat(int64(len(role.users)), 1))
			}
			bucket.acc.requirement(id, shipped)
			bucket.acc.estimate(fmt.Sprintf("%d:%s", id, role.key), share)
			if shipped && share != nil {
				bucket.delivered.Add(bucket.delivered, share)
			}
		}
	}
}

type IterationAnalysisSprint struct {
	ID          int64  `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Name        string `json:"name"`
	EndDate     string `json:"endDate"`
}
type IterationAnalysisPoint struct {
	SprintID int64 `json:"sprintId"`
	HasData  bool  `json:"hasData"`
	WorkloadMetrics
	DeliveredWeight float64 `json:"deliveredWeight"`
}
type IterationAnalysisRole struct {
	Role   string                   `json:"role"`
	Points []IterationAnalysisPoint `json:"points"`
}
type IterationAnalysisPerson struct {
	UserID         string                  `json:"userId"`
	Name           string                  `json:"name"`
	Active         bool                    `json:"active"`
	DepartmentID   string                  `json:"departmentId"`
	DepartmentName string                  `json:"departmentName"`
	Roles          []IterationAnalysisRole `json:"roles"`
}
type IterationAnalysisReport struct {
	Scope                      string                    `json:"scope"`
	TenantID                   string                    `json:"tenantId"`
	UserID                     string                    `json:"userId"`
	Month                      string                    `json:"month"`
	Count                      int                       `json:"count"`
	Project                    string                    `json:"project"`
	AsOf                       string                    `json:"asOf"`
	Snapshot                   bool                      `json:"snapshot"`
	GeneratedAt                string                    `json:"generatedAt"`
	Iterations                 []IterationAnalysisSprint `json:"iterations"`
	People                     []IterationAnalysisPerson `json:"people"`
	AmbiguousSprintItemCount   int                       `json:"ambiguousSprintItemCount"`
	UnassignedRequirementCount int                       `json:"unassignedRequirementCount"`
}

func (a *App) workloadIterationAnalysis(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	query := r.URL.Query()
	mode := query.Get("scope")
	if mode == "" {
		mode = "personal"
	}
	count := 4
	var err error
	if query.Get("count") != "" {
		count, err = strconv.Atoi(query.Get("count"))
	}
	if err != nil || (count != 2 && count != 4 && count != 6 && count != 8 && count != 12) || (mode != "personal" && mode != "organization") || len(query.Get("project")) > 200 {
		fail(w, 422, "invalid_filter", "趋势范围或筛选参数无效")
		return
	}
	month, _, end, err := workloadRequestedMonth(r)
	if err != nil {
		fail(w, 422, "invalid_month", "月份必须为有效的 YYYY-MM 格式")
		return
	}
	endTime, _ := time.Parse("2006-01-02", end)
	cutoff := endTime.AddDate(0, 0, -1).Format("2006-01-02")
	// The end date is a planned date: an iteration completed early must not be
	// omitted merely because its planned end is later than today in this month.
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	person, err := a.workloadActivePerson(r.Context(), tx)
	if err != nil {
		failOrganization(w, err)
		return
	}
	var scope *workloadProjectScope
	if mode == "organization" {
		if _, err = a.requireOrganizationPermission(r.Context(), tx, "reports.view"); err != nil {
			failOrganization(w, err)
			return
		}
	} else {
		ids, e := workloadAccessibleActiveProjectIDs(r.Context(), tx, person.UserID)
		if e != nil {
			fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
			return
		}
		scope = &workloadProjectScope{ProjectIDs: ids}
	}
	project := query.Get("project")
	clause, args := workloadProjectFilter(scope, "p.id")
	args = append([]any{tenantID, cutoff}, args...)
	if project != "" {
		clause += " AND p.id=?"
		args = append(args, project)
	}
	args = append(args, count)
	rows, err := tx.QueryContext(r.Context(), `SELECT s.id,s.project_id,p.name,s.name,s.end_date FROM sprints s JOIN projects p ON p.tenant_id=s.tenant_id AND p.id=s.project_id WHERE s.tenant_id=? AND s.status='已完成' AND s.end_date<=? AND p.status IN ('active','archived')`+clause+` ORDER BY s.end_date DESC,s.project_id DESC,s.id DESC LIMIT ?`, args...)
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	selected := []IterationAnalysisSprint{}
	for rows.Next() {
		var s IterationAnalysisSprint
		err = rows.Scan(&s.ID, &s.ProjectID, &s.ProjectName, &s.Name, &s.EndDate)
		if err != nil {
			break
		}
		if d, e := time.Parse("2006-01-02", s.EndDate); e != nil || d.Format("2006-01-02") != s.EndDate {
			err = fmt.Errorf("invalid sprint date")
			break
		}
		selected = append(selected, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].EndDate != selected[j].EndDate {
			return selected[i].EndDate < selected[j].EndDate
		}
		if selected[i].ProjectID != selected[j].ProjectID {
			return selected[i].ProjectID < selected[j].ProjectID
		}
		return selected[i].ID < selected[j].ID
	})
	startDate := cutoff
	lastDate := cutoff
	if len(selected) > 0 {
		startDate = selected[0].EndDate
		lastDate = selected[len(selected)-1].EndDate
	}
	startTime, _ := time.Parse("2006-01-02", startDate)
	lastTime, _ := time.Parse("2006-01-02", lastDate)
	collector := newWorkloadTrendCollector(time.Date(startTime.Year(), startTime.Month(), 1, 0, 0, 0, 0, time.UTC), lastTime.AddDate(0, 0, 1), WorkloadTrendFilters{Department: "*"})
	collector.selectedIDs = map[int64]bool{}
	for _, s := range selected {
		collector.selectedIDs[s.ID] = true
	}
	analysis := &iterationAnalysisCollector{rows: map[string]map[string]map[int64]*iterationAnalysisBucket{}}
	if mode == "personal" {
		analysis.user = person.UserID
	}
	collector.analysis = analysis
	report, err := a.monthlyWorkloadForProjects(r.Context(), tx, month, startDate, lastTime.AddDate(0, 0, 1).Format("2006-01-02"), scope, collector)
	// monthlyWorkloadForProjects keeps nil as organization scope after explicit authorization above.
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	out := IterationAnalysisReport{Scope: mode, TenantID: tenantID, UserID: person.UserID, Month: month, Count: count, Project: project, AsOf: cutoff, Snapshot: true, GeneratedAt: report.GeneratedAt, Iterations: selected, People: []IterationAnalysisPerson{}}
	if mode == "organization" {
		out.AmbiguousSprintItemCount = report.AmbiguousSprintItemCount
		out.UnassignedRequirementCount = report.UnassignedRequirementCount
	}
	for id, p := range collector.directory {
		if mode == "personal" && id != person.UserID {
			continue
		}
		// Include active employees with no work as no-data, not as low performers.
		if !p.Active && analysis.rows[id] == nil {
			continue
		}
		row := IterationAnalysisPerson{UserID: id, Name: p.Name, Active: p.Active, DepartmentID: p.DepartmentID, DepartmentName: p.DepartmentName, Roles: []IterationAnalysisRole{}}
		for _, role := range requirementWeightRoles {
			if analysis.rows[id][role] == nil {
				continue
			}
			data := IterationAnalysisRole{Role: role, Points: []IterationAnalysisPoint{}}
			for _, s := range selected {
				point := IterationAnalysisPoint{SprintID: s.ID}
				if bucket := analysis.rows[id][role][s.ID]; bucket != nil {
					point.HasData = true
					point.WorkloadMetrics = bucket.acc.metrics()
					point.DeliveredWeight = roundedSprintWeight(bucket.delivered)
				}
				data.Points = append(data.Points, point)
			}
			row.Roles = append(row.Roles, data)
		}
		out.People = append(out.People, row)
	}
	sort.Slice(out.People, func(i, j int) bool { return out.People[i].UserID < out.People[j].UserID })
	if err = tx.Commit(); err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, out)
}
