package main

import (
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"
)

// 月度/迭代趋势是月报当前快照的再分桶，不是由历史完成时间重建的燃尽曲线。
// 月份取迭代结束月；每个迭代点以项目 ID + 迭代 ID 区分，避免同名版本合并。
type WorkloadTrendFilters struct {
	Department string `json:"department"`
	User       string `json:"user"`
	Role       string `json:"role"`
}
type WorkloadTrendPoint struct {
	Key         string          `json:"key"`
	Month       string          `json:"month"`
	SprintCount int             `json:"sprintCount"`
	SprintID    int64           `json:"sprintId,omitempty"`
	Name        string          `json:"name,omitempty"`
	ProjectID   string          `json:"projectId,omitempty"`
	ProjectName string          `json:"projectName,omitempty"`
	EndDate     string          `json:"endDate,omitempty"`
	HasData     bool            `json:"hasData"`
	Metrics     WorkloadMetrics `json:"metrics"`
}
type WorkloadTrends struct {
	Month       string               `json:"month"`
	Months      int                  `json:"months"`
	FromMonth   string               `json:"fromMonth"`
	GeneratedAt string               `json:"generatedAt"`
	Snapshot    bool                 `json:"snapshot"`
	Precision   int                  `json:"precision"`
	Scope       any                  `json:"scope"`
	Filters     WorkloadTrendFilters `json:"filters"`
	Monthly     []WorkloadTrendPoint `json:"monthly"`
	Iterations  []WorkloadTrendPoint `json:"iterations"`
	Options     struct {
		People      []WorkloadPerson     `json:"people"`
		Departments []WorkloadDepartment `json:"departments"`
	} `json:"options"`
	AmbiguousSprintItemCount int `json:"ambiguousSprintItemCount"`
}
type workloadTrendRole struct {
	key   string
	value *big.Rat
	users []string
}
type workloadTrendBucket struct {
	point WorkloadTrendPoint
	acc   *workloadAccumulator
}
type workloadTrendCollector struct {
	filters    WorkloadTrendFilters
	directory  map[string]WorkloadPerson
	monthly    map[string]*workloadTrendBucket
	iterations map[string]*workloadTrendBucket
}

func newWorkloadTrendCollector(start, end time.Time, filters WorkloadTrendFilters) *workloadTrendCollector {
	// 预置连续月份以展示没有工作项的月份；HasData 区分“无数据”与已有条目权重恰为 0。
	c := &workloadTrendCollector{filters: filters, monthly: map[string]*workloadTrendBucket{}, iterations: map[string]*workloadTrendBucket{}}
	for date := start; date.Before(end); date = date.AddDate(0, 1, 0) {
		month := date.Format("2006-01")
		c.monthly[month] = &workloadTrendBucket{WorkloadTrendPoint{Key: month, Month: month}, newWorkloadAccumulator()}
	}
	return c
}
func (c *workloadTrendCollector) addSprint(s workloadSprint) {
	month := s.end[:7]
	key := fmt.Sprintf("%s:%d", s.project, s.id)
	c.iterations[key] = &workloadTrendBucket{WorkloadTrendPoint{Key: key, Month: month, SprintCount: 1, SprintID: s.id, Name: s.name, ProjectID: s.project, ProjectName: s.projectName, EndDate: s.end}, newWorkloadAccumulator()}
	c.monthly[month].point.SprintCount++
}
func (c *workloadTrendCollector) targets(s workloadSprint) []*workloadAccumulator {
	return []*workloadAccumulator{c.monthly[s.end[:7]].acc, c.iterations[fmt.Sprintf("%s:%d", s.project, s.id)].acc}
}
func (c *workloadTrendCollector) personMatches(id string) bool {
	// 部门 * 表示全部，空字符串表示未归属部门；历史成员可筛选，不等于允许新业务绑定。
	p, ok := c.directory[id]
	return ok && (c.filters.User == "" || id == c.filters.User) && (c.filters.Department == "*" || p.DepartmentID == c.filters.Department)
}
func (c *workloadTrendCollector) addRequirement(s workloadSprint, id int64, shipped bool, roles []workloadTrendRole) {
	// 职能/成员/部门共同过滤；仅选择部分参与人员时仍以原完整人数作分母，
	// 不能把被过滤人员的份额重新分给可见人员，否则趋势与个人/企业月表不一致。
	personFilter := c.filters.User != "" || c.filters.Department != "*"
	matched := !personFilter && c.filters.Role == ""
	for _, role := range roles {
		if c.filters.Role != "" && c.filters.Role != role.key {
			continue
		}
		value := role.value
		if personFilter {
			selected := 0
			for _, uid := range role.users {
				if c.personMatches(uid) {
					selected++
				}
			}
			if selected == 0 {
				continue
			}
			if value != nil {
				value = new(big.Rat).Mul(value, big.NewRat(int64(selected), int64(len(role.users))))
			}
		}
		matched = true
		for _, acc := range c.targets(s) {
			acc.estimate(fmt.Sprintf("%d:%s", id, role.key), value)
		}
	}
	if matched {
		for _, acc := range c.targets(s) {
			acc.requirement(id, shipped)
		}
	}
}
func (c *workloadTrendCollector) addDefect(s workloadSprint, id int64, user, role string) {
	if c.filters.Role != "" && role != c.filters.Role {
		return
	}
	if (c.filters.User != "" || c.filters.Department != "*") && !c.personMatches(user) {
		return
	}
	for _, acc := range c.targets(s) {
		acc.defects[id] = true
	}
}
func trendPoints(buckets map[string]*workloadTrendBucket, iterations bool) []WorkloadTrendPoint {
	// 时间正序并以项目/迭代稳定 ID 兜底，界面可直接绘图；无数据月份不伪造历史事件。
	points := make([]WorkloadTrendPoint, 0, len(buckets))
	for _, bucket := range buckets {
		point := bucket.point
		point.Metrics = bucket.acc.metrics()
		point.HasData = point.Metrics.RequirementCount > 0 || point.Metrics.DefectCount > 0
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool {
		if !iterations {
			return points[i].Month < points[j].Month
		}
		if points[i].EndDate != points[j].EndDate {
			return points[i].EndDate < points[j].EndDate
		}
		if points[i].ProjectID != points[j].ProjectID {
			return points[i].ProjectID < points[j].ProjectID
		}
		return points[i].SprintID < points[j].SprintID
	})
	return points
}

func (a *App) workloadTrends(w http.ResponseWriter, r *http.Request) {
	// 与企业月表使用相同 reports.view 权限；仅支持截止所选月的 6/12 月窗口。
	// 整个范围调用一次 monthlyWorkload，避免逐月重复读取和跨次快照不一致。
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
	query := r.URL.Query()
	month := query.Get("month")
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	_, endDate, err := workloadMonthRange(month)
	if err != nil {
		fail(w, 422, "invalid_month", "月份必须为有效的 YYYY-MM 格式")
		return
	}
	months := 6
	switch query.Get("months") {
	case "", "6":
	case "12":
		months = 12
	default:
		fail(w, 422, "invalid_filter", "趋势范围或筛选参数无效")
		return
	}
	filters := WorkloadTrendFilters{Department: "*", User: query.Get("user"), Role: query.Get("role")}
	if query.Has("department") {
		filters.Department = query.Get("department")
	}
	if len(filters.User) > 200 || len(filters.Department) > 200 || strings.TrimSpace(filters.User) != filters.User || strings.TrimSpace(filters.Department) != filters.Department || (filters.Role != "" && !validChoice(filters.Role, append(append([]string{}, requirementWeightRoles...), "other"))) {
		fail(w, 422, "invalid_filter", "趋势范围或筛选参数无效")
		return
	}
	end, _ := time.Parse("2006-01-02", endDate)
	start := end.AddDate(0, -months, 0)
	if start.Year() < 1 {
		start = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	collector := newWorkloadTrendCollector(start, end, filters)
	report, err := a.monthlyWorkload(r.Context(), tx, month, start.Format("2006-01-02"), endDate, collector)
	if err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "database_unavailable", "月度工作量暂时无法读取，请稍后重试")
		return
	}
	out := WorkloadTrends{Month: month, Months: months, FromMonth: start.Format("2006-01"), GeneratedAt: report.GeneratedAt, Snapshot: true, Precision: 6, Scope: report.Scope, Filters: filters, Monthly: trendPoints(collector.monthly, false), Iterations: trendPoints(collector.iterations, true), AmbiguousSprintItemCount: report.AmbiguousSprintItemCount}
	out.Options.People = report.People
	out.Options.Departments = report.Departments
	w.Header().Set("Cache-Control", "no-store")
	write(w, 200, out)
}
