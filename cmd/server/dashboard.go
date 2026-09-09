package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type DashboardStatus struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	System bool   `json:"system"`
	Color  string `json:"color"`
	Count  int    `json:"count"`
}
type DashboardSprint struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	StartDate   string  `json:"startDate"`
	EndDate     string  `json:"endDate"`
	Total       int     `json:"total"`
	Done        int     `json:"done"`
	WeightTotal float64 `json:"weightTotal"`
}
type DashboardDay struct {
	Date                string `json:"date"`
	RequirementsCreated int    `json:"requirementsCreated"`
	DefectsCreated      int    `json:"defectsCreated"`
}
type DashboardMember struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Active           bool   `json:"active"`
	RequirementCount int    `json:"requirementCount"`
	DefectCount      int    `json:"defectCount"`
	ExecutionCount   int    `json:"executionCount"`
}
type ProjectDashboard struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	GeneratedAt string `json:"generatedAt"`
	Days        int    `json:"days"`
	Timezone    string `json:"timezone"`
	Totals      struct {
		Requirements struct {
			Total     int `json:"total"`
			Done      int `json:"done"`
			Cancelled int `json:"cancelled"`
		} `json:"requirements"`
		Defects struct {
			Total    int `json:"total"`
			Closed   int `json:"closed"`
			Open     int `json:"open"`
			Critical int `json:"critical"`
		} `json:"defects"`
		Sprints struct {
			Total     int `json:"total"`
			Ongoing   int `json:"ongoing"`
			Planned   int `json:"planned"`
			Completed int `json:"completed"`
			Cancelled int `json:"cancelled"`
		} `json:"sprints"`
		TestCases struct {
			Total   int `json:"total"`
			Enabled int `json:"enabled"`
		} `json:"testCases"`
		TestPlans struct {
			Total int `json:"total"`
		} `json:"testPlans"`
		Executions struct {
			Total   int `json:"total"`
			Passed  int `json:"passed"`
			Failed  int `json:"failed"`
			Blocked int `json:"blocked"`
			NotRun  int `json:"notRun"`
			Skipped int `json:"skipped"`
		} `json:"executions"`
	} `json:"totals"`
	Statuses struct {
		Requirements []DashboardStatus `json:"requirements"`
		Defects      []DashboardStatus `json:"defects"`
	} `json:"statuses"`
	CurrentSprints       []DashboardSprint `json:"currentSprints"`
	Trend                []DashboardDay    `json:"trend"`
	TrendAvailable       bool              `json:"trendAvailable"`
	MissingCreationDates int               `json:"missingCreationDates"`
	Members              []DashboardMember `json:"members"`
	Health               ProjectHealth     `json:"health"`
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	days := 30
	if value := r.URL.Query().Get("days"); value != "" {
		var err error
		days, err = strconv.Atoi(value)
		if err != nil || days != 7 && days != 30 && days != 90 {
			fail(w, 422, "invalid_dashboard_range", "趋势范围仅支持 7、30 或 90 天")
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, 503, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	// 在与统计数据相同的快照内复核项目与账户，避免已归档项目或已撤销成员关系泄露报表。
	if _, err = a.requirementStateRole(r.Context(), tx); err != nil {
		failState(w, err)
		return
	}
	var status string
	err = tx.QueryRowContext(r.Context(), `SELECT status FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "active" {
		fail(w, 403, "project_forbidden", "无权访问该项目")
		return
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
		return
	}
	result, err := a.projectDashboard(r.Context(), tx, days, time.Now())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 503, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	write(w, 200, result)
}

func dashboardRows(ctx context.Context, q stateStore, query string, args []any, consume func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err = consume(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func dashboardCreatedDate(raw string, location *time.Location) string {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return parsed.In(location).Format("2006-01-02")
		}
	}
	return ""
}

func (a *App) projectDashboard(ctx context.Context, q stateStore, days int, now time.Time) (ProjectDashboard, error) {
	out := ProjectDashboard{Days: days, Timezone: "Asia/Shanghai", GeneratedAt: now.UTC().Format(time.RFC3339), CurrentSprints: []DashboardSprint{}, Trend: []DashboardDay{}, Members: []DashboardMember{}}
	out.Project.ID = a.pid()
	out.Statuses.Requirements, out.Statuses.Defects = []DashboardStatus{}, []DashboardStatus{}
	if err := q.QueryRowContext(ctx, `SELECT name FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&out.Project.Name); err != nil {
		return out, err
	}
	args := []any{tenantID, a.pid()}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	today := now.In(location)
	dayIndexes := map[string]int{}
	for i := days - 1; i >= 0; i-- {
		date := today.AddDate(0, 0, -i).Format("2006-01-02")
		dayIndexes[date] = len(out.Trend)
		out.Trend = append(out.Trend, DashboardDay{Date: date})
	}
	addTrend := func(raw string, requirement bool) {
		date := dashboardCreatedDate(raw, location)
		if date == "" {
			out.MissingCreationDates++
			return
		}
		out.TrendAvailable = true
		if index, ok := dayIndexes[date]; ok {
			if requirement {
				out.Trend[index].RequirementsCreated++
			} else {
				out.Trend[index].DefectsCreated++
			}
		}
	}
	statuses, err := listRequirementStatuses(ctx, q, tenantID, a.pid())
	if err != nil {
		return out, err
	}
	statusIndex, category := map[string]int{}, map[string]string{}
	for _, status := range statuses {
		statusIndex[status.Key] = len(out.Statuses.Requirements)
		category[status.Key] = status.Category
		out.Statuses.Requirements = append(out.Statuses.Requirements, DashboardStatus{Key: status.Key, Name: status.Name, System: status.System, Color: status.Color})
	}
	defectIndex := map[string]int{}
	for _, status := range []string{"新建", "已确认", "修复中", "已解决", "待验证", "已关闭", "重新打开", "已拒绝"} {
		defectIndex[status] = len(out.Statuses.Defects)
		out.Statuses.Defects = append(out.Statuses.Defects, DashboardStatus{Key: status, Name: status, System: true})
	}
	people := map[string]*DashboardMember{}
	err = dashboardRows(ctx, q, `SELECT u.id,u.name,u.active,tm.status FROM project_members pm JOIN users u ON u.tenant_id=pm.tenant_id AND u.id=pm.user_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE pm.tenant_id=? AND pm.project_id=?`, args, func(rows *sql.Rows) error {
		var person DashboardMember
		var membership string
		if err := rows.Scan(&person.ID, &person.Name, &person.Active, &membership); err != nil {
			return err
		}
		person.Active = person.Active && membership == "active"
		people[person.ID] = &person
		return nil
	})
	if err != nil {
		return out, err
	}
	sprints := []DashboardSprint{}
	exact, aliases := map[string][]int{}, map[string][]int{}
	weights := map[int]*big.Rat{}
	err = dashboardRows(ctx, q, `SELECT id,name,status,start_date,end_date FROM sprints WHERE tenant_id=? AND project_id=? ORDER BY start_date,id`, args, func(rows *sql.Rows) error {
		var sprint DashboardSprint
		if err := rows.Scan(&sprint.ID, &sprint.Name, &sprint.Status, &sprint.StartDate, &sprint.EndDate); err != nil {
			return err
		}
		index := len(sprints)
		sprints = append(sprints, sprint)
		exact[sprint.Name] = append(exact[sprint.Name], index)
		_, alias := sprintAliases(sprint.Name)
		aliases[alias] = append(aliases[alias], index)
		weights[index] = new(big.Rat)
		out.Totals.Sprints.Total++
		switch sprint.Status {
		case "进行中":
			out.Totals.Sprints.Ongoing++
		case "规划中":
			out.Totals.Sprints.Planned++
		case "已完成":
			out.Totals.Sprints.Completed++
		case "已取消":
			out.Totals.Sprints.Cancelled++
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	matchSprint := func(name string) int {
		if name == "" || name == "待规划" {
			return -1
		}
		items := exact[name]
		if len(items) == 0 {
			items = aliases[name]
		}
		if len(items) != 1 {
			return -1
		}
		return items[0]
	}
	// 每条范围内需求仅统计一次；人员关联和父子关系展开都不能放大总量，也不受列表分页影响。
	err = dashboardRows(ctx, q, `SELECT status,sprint,created_at,role_weights_json,assignee_user_ids_json,owner_user_ids_json,assignee_user_id,owner_user_id FROM requirements WHERE tenant_id=? AND project_id=?`, args, func(rows *sql.Rows) error {
		var status, sprint, created, raw, assignees, owners, assignee, owner string
		if err := rows.Scan(&status, &sprint, &created, &raw, &assignees, &owners, &assignee, &owner); err != nil {
			return err
		}
		out.Totals.Requirements.Total++
		done := category[status] == "done"
		if done {
			out.Totals.Requirements.Done++
		}
		if category[status] == "cancelled" {
			out.Totals.Requirements.Cancelled++
		}
		index, ok := statusIndex[status]
		if !ok {
			index = len(out.Statuses.Requirements)
			statusIndex[status] = index
			out.Statuses.Requirements = append(out.Statuses.Requirements, DashboardStatus{Key: status, Name: status})
		}
		out.Statuses.Requirements[index].Count++
		addTrend(created, true)
		var roleWeights map[string]struct {
			Value   *json.Number `json:"value"`
			UserID  string       `json:"userId"`
			UserIDs []string     `json:"userIds"`
		}
		var assigneeIDs, ownerIDs []string
		if err := json.Unmarshal([]byte(raw), &roleWeights); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(assignees), &assigneeIDs); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(owners), &ownerIDs); err != nil {
			return err
		}
		ids := map[string]bool{assignee: true, owner: true}
		for _, id := range append(assigneeIDs, ownerIDs...) {
			ids[id] = true
		}
		total := new(big.Rat)
		for role, weight := range roleWeights {
			if !validChoice(role, requirementWeightRoles) {
				return fmt.Errorf("unknown requirement weight dimension")
			}
			ids[weight.UserID] = true
			for _, id := range weight.UserIDs {
				ids[id] = true
			}
			if weight.Value != nil {
				value, ok := new(big.Rat).SetString(weight.Value.String())
				if !ok || value.Sign() < 0 || value.Cmp(big.NewRat(1_000_000, 1)) > 0 {
					return fmt.Errorf("invalid requirement weight")
				}
				total.Add(total, value)
			}
		}
		for id := range ids {
			if person := people[id]; person != nil {
				person.RequirementCount++
			}
		}
		if index := matchSprint(sprint); index >= 0 {
			sprints[index].Total++
			if done {
				sprints[index].Done++
			}
			weights[index].Add(weights[index], total)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	err = dashboardRows(ctx, q, `SELECT status,severity,sprint,created_at,assignee_user_id,verifier_user_id FROM defects WHERE tenant_id=? AND project_id=?`, args, func(rows *sql.Rows) error {
		var status, severity, sprint, created, assignee, verifier string
		if err := rows.Scan(&status, &severity, &sprint, &created, &assignee, &verifier); err != nil {
			return err
		}
		out.Totals.Defects.Total++
		if status == "已关闭" {
			out.Totals.Defects.Closed++
		}
		if status != "已关闭" && status != "已拒绝" {
			out.Totals.Defects.Open++
			if severity == "致命" || severity == "严重" {
				out.Totals.Defects.Critical++
			}
		}
		index, ok := defectIndex[status]
		if !ok {
			index = len(out.Statuses.Defects)
			defectIndex[status] = index
			out.Statuses.Defects = append(out.Statuses.Defects, DashboardStatus{Key: status, Name: status})
		}
		out.Statuses.Defects[index].Count++
		addTrend(created, false)
		for id := range map[string]bool{assignee: true, verifier: true} {
			if person := people[id]; person != nil {
				person.DefectCount++
			}
		}
		if index := matchSprint(sprint); index >= 0 {
			sprints[index].Total++
			if status == "已关闭" {
				sprints[index].Done++
			}
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for index, sprint := range sprints {
		if sprint.Status == "进行中" {
			sprint.WeightTotal = roundedSprintWeight(weights[index])
			out.CurrentSprints = append(out.CurrentSprints, sprint)
		}
	}
	err = dashboardRows(ctx, q, `SELECT enabled FROM test_cases WHERE tenant_id=? AND project_id=?`, args, func(rows *sql.Rows) error {
		var enabled bool
		if err := rows.Scan(&enabled); err != nil {
			return err
		}
		out.Totals.TestCases.Total++
		if enabled {
			out.Totals.TestCases.Enabled++
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_plans WHERE tenant_id=? AND project_id=?`, args...).Scan(&out.Totals.TestPlans.Total); err != nil {
		return out, err
	}
	err = dashboardRows(ctx, q, `SELECT e.status,e.executor_user_id FROM test_executions e JOIN test_cases c ON c.id=e.case_id AND c.tenant_id=e.tenant_id AND c.project_id=e.project_id JOIN test_plans p ON p.id=e.plan_id AND p.tenant_id=e.tenant_id AND p.project_id=e.project_id WHERE e.tenant_id=? AND e.project_id=?`, args, func(rows *sql.Rows) error {
		var status, executor string
		if err := rows.Scan(&status, &executor); err != nil {
			return err
		}
		out.Totals.Executions.Total++
		switch status {
		case "通过":
			out.Totals.Executions.Passed++
		case "失败":
			out.Totals.Executions.Failed++
		case "阻塞":
			out.Totals.Executions.Blocked++
		case "未执行":
			out.Totals.Executions.NotRun++
		case "跳过":
			out.Totals.Executions.Skipped++
		default:
			return fmt.Errorf("invalid execution status")
		}
		if person := people[executor]; person != nil {
			person.ExecutionCount++
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	for _, person := range people {
		if person.Active || person.RequirementCount+person.DefectCount+person.ExecutionCount > 0 {
			out.Members = append(out.Members, *person)
		}
	}
	sort.Slice(out.Members, func(i, j int) bool {
		a, b := out.Members[i], out.Members[j]
		left, right := a.RequirementCount+a.DefectCount+a.ExecutionCount, b.RequirementCount+b.DefectCount+b.ExecutionCount
		if left != right {
			return left > right
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	if out.Health, err = a.projectHealth(ctx, q, now); err != nil {
		return out, err
	}
	return out, nil
}
