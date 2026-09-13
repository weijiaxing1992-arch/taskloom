package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) validateSprintName(name string, exceptID int64) error {
	if name == "" || name == "待规划" || utf8.RuneCountInString(name) > 120 {
		return fmt.Errorf("迭代名称须为 1–120 字，且不能使用“待规划”")
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE tenant_id=? AND project_id=? AND name=? AND id!=?`, tenantID, a.pid(), name, exceptID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("当前项目已存在同名迭代")
	}
	return nil
}

// The V1 short-name fallback is safe only when it resolves to this exact sprint.
// Two sprints named "Q3 Alpha" and "Q3 Beta" must not both count "Q3" work.
func (a *App) scopedSprintAliases(name string) (string, string) {
	_, alias := sprintAliases(name)
	resolved, err := a.resolveRequirementSprint(alias, false)
	if err != nil || resolved != name {
		alias = name
	}
	return name, alias
}

// sprintStore 让迭代校验与更新共享同一事务视图，避免并发修改覆盖。
type sprintStore interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// 保留可写业务列的完整快照，作为更新时的并发前置条件。
type sprintMutationSnapshot struct {
	sprint Sprint
}

func (a *App) readSprintMutationSnapshot(ctx context.Context, tx *sql.Tx, id int64) (sprintMutationSnapshot, error) {
	var snapshot sprintMutationSnapshot
	if err := tx.QueryRowContext(ctx, `SELECT id,code,name,goal,start_date,end_date,status,capacity,updated_at
		FROM sprints WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(
		&snapshot.sprint.ID, &snapshot.sprint.Code, &snapshot.sprint.Name, &snapshot.sprint.Goal,
		&snapshot.sprint.StartDate, &snapshot.sprint.EndDate, &snapshot.sprint.Status,
		&snapshot.sprint.Capacity, &snapshot.sprint.UpdatedAt,
	); err != nil {
		return sprintMutationSnapshot{}, err
	}
	return snapshot, nil
}

func (a *App) validateSprintNameFrom(ctx context.Context, q sprintStore, name string, exceptID int64) error {
	if name == "" || name == "待规划" || utf8.RuneCountInString(name) > 120 {
		return fmt.Errorf("迭代名称须为 1–120 字，且不能使用“待规划”")
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sprints WHERE tenant_id=? AND project_id=? AND name=? AND id!=?`, tenantID, a.pid(), name, exceptID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("当前项目已存在同名迭代")
	}
	return nil
}

// scopedSprintAliasesFrom 仅把唯一且确实解析为本迭代的旧简称纳入迁移范围。
// 事务内读取避免名称被并发修改后把工作项迁给另一个同前缀迭代。
func (a *App) scopedSprintAliasesFrom(ctx context.Context, q sprintStore, name string) (string, string, error) {
	_, alias := sprintAliases(name)
	if alias == name {
		return name, alias, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT name FROM sprints WHERE tenant_id=? AND project_id=? AND (name=? OR name LIKE ?)`, tenantID, a.pid(), alias, alias+" %")
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	matches := []string{}
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", "", err
		}
		matches = append(matches, candidate)
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if len(matches) != 1 || matches[0] != name {
		alias = name
	}
	return name, alias, nil
}

// resolveRequirementSprintFrom 与普通需求分配沿用相同的名称/简称规则，但使用
// 调用方事务读取。完成迭代时目标校验和迁移必须观察同一份项目内迭代快照。
func (a *App) resolveRequirementSprintFrom(ctx context.Context, q sprintStore, value string, assignable bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "待规划" {
		return "待规划", nil
	}
	rows, err := q.QueryContext(ctx, `SELECT name,status FROM sprints WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type match struct{ name, status string }
	exact, aliases := []match{}, []match{}
	for rows.Next() {
		var item match
		if err := rows.Scan(&item.name, &item.status); err != nil {
			return "", err
		}
		if item.name == value {
			exact = append(exact, item)
		} else if strings.HasPrefix(item.name, value+" ") {
			aliases = append(aliases, item)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	matches := exact
	if len(matches) == 0 {
		matches = aliases
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("迭代不属于当前项目或不存在，请刷新后重新选择")
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("迭代名称不唯一，请选择完整迭代名称")
	}
	if assignable && matches[0].status != "规划中" && matches[0].status != "进行中" {
		return "", fmt.Errorf("已完成或已取消的迭代不能再分配需求")
	}
	return matches[0].name, nil
}

func failSprintConflict(w http.ResponseWriter) {
	fail(w, http.StatusConflict, "sprint_conflict", "迭代已被其他成员修改，请刷新后重试")
}

func (a *App) patchSprint(w http.ResponseWriter, r *http.Request, id int64) {
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	if !a.checkIntegrationPrecondition(w, r, tx) {
		return
	}
	snapshot, err := a.readSprintMutationSnapshot(r.Context(), tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "迭代不存在")
		} else {
			fail(w, 503, "database_unavailable", "迭代暂时无法读取，请稍后重试")
		}
		return
	}
	current := snapshot.sprint
	next := current
	allowed := map[string]any{"name": &next.Name, "goal": &next.Goal, "startDate": &next.StartDate, "endDate": &next.EndDate, "status": &next.Status, "capacity": &next.Capacity}
	for key, raw := range patch {
		if target := allowed[key]; target != nil {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, target) != nil {
				fail(w, 422, "validation_error", key+" 字段格式无效")
				return
			}
		}
	}
	next.Name = strings.TrimSpace(next.Name)
	if err := a.validateSprintNameFrom(r.Context(), tx, next.Name, id); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	if !validDateRange(next.StartDate, next.EndDate) || next.Capacity < 0 {
		fail(w, 422, "validation_error", "日期范围或容量无效")
		return
	}
	if !canTransition(current.Status, next.Status, map[string][]string{"规划中": {"进行中", "已取消"}, "进行中": {"已取消"}, "已完成": {}, "已取消": {}}) {
		fail(w, 422, "validation_error", "迭代状态无效")
		return
	}
	a1, a2, err := a.scopedSprintAliasesFrom(r.Context(), tx, current.Name)
	if err != nil {
		fail(w, 503, "database_unavailable", "迭代关联关系暂时无法读取，请稍后重试")
		return
	}
	actor := a.uid()
	_ = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE id=? AND tenant_id=?`, a.uid(), tenantID).Scan(&actor)
	now := time.Now().UTC().Format(time.RFC3339)
	// 不能只比较秒级 updated_at：同秒保存仍可能覆盖。这里把事务内快照的所有
	// 可写列作为前置条件，任意并发编辑都会使本次更新返回 0 行并整体回滚。
	result, err := tx.ExecContext(r.Context(), `UPDATE sprints SET name=?,goal=?,start_date=?,end_date=?,status=?,capacity=?,updated_at=?
		WHERE id=? AND tenant_id=? AND project_id=?
			AND name=? AND goal=? AND start_date=? AND end_date=? AND status=? AND capacity=?`,
		next.Name, next.Goal, next.StartDate, next.EndDate, next.Status, next.Capacity, now,
		id, tenantID, a.pid(), current.Name, current.Goal, current.StartDate, current.EndDate, current.Status, current.Capacity)
	if err == nil {
		var changed int64
		changed, err = result.RowsAffected()
		if err == nil && changed != 1 {
			failSprintConflict(w)
			return
		}
	}
	if err == nil && next.Name != current.Name {
		err = a.recordSprintRequirementHistory(r.Context(), tx, a1, a2, next.Name, actor, now, false)
	}
	if err == nil && next.Name != current.Name {
		for _, table := range []string{"requirements", "defects", "test_plans"} {
			if _, err = tx.Exec(`UPDATE `+table+` SET sprint=?,updated_at=? WHERE tenant_id=? AND project_id=? AND sprint IN (?,?)`, next.Name, now, tenantID, a.pid(), a1, a2); err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,'sprint',?,?,'updated',?,?)`, tenantID, a.pid(), id, actor, "更新了迭代信息及关联工作项", now)
	}
	if err == nil && current.Status != next.Status {
		var notices []assignmentNotice
		notices, err = a.sprintLifecycleNotices(r.Context(), tx, id, "sprint.status_changed", "迭代状态已更新", fmt.Sprintf("迭代状态已变更为 %s", next.Status))
		if err == nil {
			err = a.writeAssignmentNotices(r.Context(), tx, notices, now)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	a.sprint(w, httptestGet(fmt.Sprintf("/api/sprints/%d", id)))
}
