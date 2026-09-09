package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// ProjectHealth 是可解释的交付风险快照；只输出当前范围的聚合计数，
// 不携带标题、人员等工作项内容，避免本接口演变为另一份列表数据。
type ProjectHealth struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	GeneratedAt string                `json:"generatedAt"`
	Status      string                `json:"status"`
	Counts      ProjectHealthCounts   `json:"counts"`
	Reasons     []ProjectHealthReason `json:"reasons"`
}

type ProjectHealthCounts struct {
	OverdueRequirements int `json:"overdueRequirements"`
	OverdueSprints      int `json:"overdueSprints"`
	OpenFatalDefects    int `json:"openFatalDefects"`
	OpenMajorDefects    int `json:"openMajorDefects"`
}

// Key 保持稳定且不绑定展示文案；客户端负责多语言和跳转，接口可被后续场景复用。
type ProjectHealthReason struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func (a *App) projectHealthAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
		return
	}
	defer tx.Rollback()
	if err = a.requireActiveProjectHealthScope(r.Context(), tx); err != nil {
		failProjectHealthScope(w, err)
		return
	}
	health, err := a.projectHealth(r.Context(), tx, time.Now())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	write(w, http.StatusOK, health)
}

// 授权与聚合读取使用同一个只读事务，避免成员权限或项目归档状态在两者之间变化。
func (a *App) requireActiveProjectHealthScope(ctx context.Context, q stateStore) error {
	if _, err := a.requirementStateRole(ctx, q); err != nil {
		return err
	}
	var status string
	err := q.QueryRowContext(ctx, `SELECT status FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || err == nil && status != "active" {
		return stateError{"forbidden", "无权访问该项目", http.StatusForbidden}
	}
	return err
}

func failProjectHealthScope(w http.ResponseWriter, err error) {
	var state stateError
	if errors.As(err, &state) {
		fail(w, state.status, state.code, state.message)
		return
	}
	fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目仪表盘暂时无法读取，请稍后重试")
}

func (a *App) projectHealth(ctx context.Context, q stateStore, now time.Time) (ProjectHealth, error) {
	out := ProjectHealth{GeneratedAt: now.UTC().Format(time.RFC3339), Reasons: []ProjectHealthReason{}}
	out.Project.ID = a.pid()
	if err := q.QueryRowContext(ctx, `SELECT name FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&out.Project.Name); err != nil {
		return out, err
	}

	// 日期是日历字段：在产品时区确定当天，并让 SQLite 排除格式异常的历史日期，
	// 不用字符串排序把无效数据误判为逾期。
	today := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	args := []any{tenantID, a.pid(), today}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM requirements r
		LEFT JOIN requirement_statuses s ON s.tenant_id=r.tenant_id AND s.project_id=r.project_id AND s.key=r.status
		WHERE r.tenant_id=? AND r.project_id=?
		  AND r.end_date GLOB '????-??-??' AND date(r.end_date) IS NOT NULL AND date(r.end_date)<?
		  AND COALESCE(s.category,'doing') NOT IN ('done','cancelled')`, args...).Scan(&out.Counts.OverdueRequirements); err != nil {
		return out, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM sprints
		WHERE tenant_id=? AND project_id=? AND status='进行中'
		  AND end_date GLOB '????-??-??' AND date(end_date) IS NOT NULL AND date(end_date)<?`, args...).Scan(&out.Counts.OverdueSprints); err != nil {
		return out, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM defects
		WHERE tenant_id=? AND project_id=? AND status NOT IN ('已关闭','已拒绝') AND severity='致命'`, tenantID, a.pid()).Scan(&out.Counts.OpenFatalDefects); err != nil {
		return out, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM defects
		WHERE tenant_id=? AND project_id=? AND status NOT IN ('已关闭','已拒绝') AND severity='严重'`, tenantID, a.pid()).Scan(&out.Counts.OpenMajorDefects); err != nil {
		return out, err
	}

	// 未关闭致命缺陷会阻塞交付；逾期工作和严重缺陷仅标为风险，不能直接断言无法交付。
	out.Status = "healthy"
	if out.Counts.OpenFatalDefects > 0 {
		out.Status = "blocked"
	} else if out.Counts.OverdueRequirements > 0 || out.Counts.OverdueSprints > 0 || out.Counts.OpenMajorDefects > 0 {
		out.Status = "at_risk"
	}
	if out.Counts.OpenFatalDefects > 0 {
		out.Reasons = append(out.Reasons, ProjectHealthReason{Key: "open_fatal_defects", Count: out.Counts.OpenFatalDefects})
	}
	if out.Counts.OverdueSprints > 0 {
		out.Reasons = append(out.Reasons, ProjectHealthReason{Key: "overdue_sprints", Count: out.Counts.OverdueSprints})
	}
	if out.Counts.OverdueRequirements > 0 {
		out.Reasons = append(out.Reasons, ProjectHealthReason{Key: "overdue_requirements", Count: out.Counts.OverdueRequirements})
	}
	if out.Counts.OpenMajorDefects > 0 {
		out.Reasons = append(out.Reasons, ProjectHealthReason{Key: "open_major_defects", Count: out.Counts.OpenMajorDefects})
	}
	return out, nil
}
