package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Each detailed history record extends the existing activity anchor. The delay
// counter is derived from those anchors, never from status, dates or retries.
func (a *App) migrateRequirementHistory() error {
	var exists int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('requirements') WHERE name='iteration_delay_count'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := a.db.Exec(`ALTER TABLE requirements ADD COLUMN iteration_delay_count INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_activity_history(activity_id INTEGER PRIMARY KEY,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,actor_id TEXT NOT NULL DEFAULT '',before_json TEXT NOT NULL,after_json TEXT NOT NULL,iteration_delay INTEGER NOT NULL DEFAULT 0,source_audit_id INTEGER UNIQUE);
		CREATE INDEX IF NOT EXISTS idx_requirement_history_scope ON requirement_activity_history(tenant_id,project_id,requirement_id,activity_id)`); err != nil {
		return err
	}
	return a.backfillRequirementHistory()
}

func requirementIterationMoved(before, after map[string]any) bool {
	from, hasFrom := before["sprint"].(string)
	to, hasTo := after["sprint"].(string)
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	return hasFrom && hasTo && from != "" && to != "" && from != "待规划" && to != "待规划" && from != to
}

func (a *App) recordRequirementHistory(tx *sql.Tx, activityID, requirementID int64, before, after map[string]any, transfer bool) error {
	delay := transfer && requirementIterationMoved(before, after)
	if _, err := tx.Exec(`INSERT INTO requirement_activity_history(activity_id,tenant_id,project_id,requirement_id,actor_id,before_json,after_json,iteration_delay)VALUES(?,?,?,?,?,?,?,?)`, activityID, tenantID, a.pid(), requirementID, a.uid(), jsonText(before), jsonText(after), delay); err != nil {
		return err
	}
	if delay {
		_, err := tx.Exec(`UPDATE requirements SET iteration_delay_count=iteration_delay_count+1 WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), requirementID)
		return err
	}
	return nil
}

// Record bulk migrations before the matching UPDATE, using exactly the same
// predicate and transaction. A rename is preserved but never counts as a move.
func (a *App) recordSprintRequirementHistory(ctx context.Context, tx *sql.Tx, from, alias, to, actor, now string, transfer bool) error {
	where := `tenant_id=? AND project_id=? AND sprint IN (?,?)`
	if transfer {
		where += ` AND NOT EXISTS(SELECT 1 FROM requirement_statuses rs WHERE rs.tenant_id=requirements.tenant_id AND rs.project_id=requirements.project_id AND rs.key=requirements.status AND rs.category IN ('done','cancelled'))`
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,sprint,status FROM requirements WHERE `+where+` ORDER BY id`, tenantID, a.pid(), from, alias)
	if err != nil {
		return err
	}
	type item struct {
		id             int64
		sprint, status string
	}
	items := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.sprint, &x.status); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range items {
		event, detail := "sprint_renamed", "所属迭代更名"
		if transfer {
			event, detail = "sprint_transferred", "完成迭代后迁移需求"
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), x.id, actor, event, detail, now)
		if err != nil {
			return err
		}
		activityID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		before := map[string]any{"sprint": x.sprint, "status": x.status}
		after := map[string]any{"sprint": to, "status": x.status}
		if err = a.recordRequirementHistory(tx, activityID, x.id, before, after, transfer); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) recordRequirementCategoryHistory(ctx context.Context, tx *sql.Tx, from, to, now string) error {
	if from == to {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,sprint,status FROM requirements WHERE tenant_id=? AND project_id=? AND category=? ORDER BY id`, tenantID, a.pid(), from)
	if err != nil {
		return err
	}
	type item struct {
		id             int64
		sprint, status string
	}
	items := []item{}
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.sprint, &x.status); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	actor := a.uid()
	if err = tx.QueryRowContext(ctx, `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		return err
	}
	for _, x := range items {
		activity, err := tx.ExecContext(ctx, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), x.id, actor, "category_changed", "分类调整后更新需求归属", now)
		if err != nil {
			return err
		}
		activityID, err := activity.LastInsertId()
		if err != nil {
			return err
		}
		before := map[string]any{"category": from, "sprint": x.sprint, "status": x.status}
		after := map[string]any{"category": to, "sprint": x.sprint, "status": x.status}
		if err = a.recordRequirementHistory(tx, activityID, x.id, before, after, false); err != nil {
			return err
		}
	}
	return nil
}

// Recover only explicit requirement snapshots. Aggregate sprint completion logs
// and prose activities cannot prove which requirement moved from which sprint.
// Existing audit rows remain untouched. Source IDs make this migration idempotent.
func (a *App) backfillRequirementHistory() error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT al.id,al.tenant_id,al.project_id,al.object_id,al.actor_id,COALESCE(u.name,al.actor_id),al.action,al.before_json,al.after_json,al.created_at
		FROM audit_logs al JOIN requirements q ON q.tenant_id=al.tenant_id AND q.project_id=al.project_id AND CAST(q.id AS TEXT)=al.object_id
		LEFT JOIN users u ON u.tenant_id=al.tenant_id AND u.id=al.actor_id
		WHERE al.object_type='requirement' AND al.action IN ('update','updated','patch','requirement.updated','create','created','requirement.created')
		AND NOT EXISTS(SELECT 1 FROM requirement_activity_history h WHERE h.source_audit_id=al.id) ORDER BY al.created_at,al.id`)
	if err != nil {
		return err
	}
	type audit struct {
		id                                                                 int64
		tenant, project, object, actorID, actor, action, before, after, at string
	}
	logs := []audit{}
	for rows.Next() {
		var x audit
		if err = rows.Scan(&x.id, &x.tenant, &x.project, &x.object, &x.actorID, &x.actor, &x.action, &x.before, &x.after, &x.at); err != nil {
			rows.Close()
			return err
		}
		logs = append(logs, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, x := range logs {
		var before, after map[string]any
		if json.Unmarshal([]byte(x.before), &before) != nil || json.Unmarshal([]byte(x.after), &after) != nil {
			continue
		}
		before, after = requirementHistorySnapshot(before), requirementHistorySnapshot(after)
		creating := validChoice(x.action, []string{"create", "created", "requirement.created"})
		if len(after) == 0 || (!creating && len(before) == 0) || reflect.DeepEqual(before, after) {
			continue
		}
		id, err := strconv.ParseInt(x.object, 10, 64)
		if err != nil {
			continue
		}
		event, detail := "updated", "从审计快照恢复需求变更"
		if creating {
			event, detail = "created", "从审计快照恢复需求创建"
		}
		// A current activity may already contain this exact evidence, or an old
		// audit exporter may have written the same snapshot more than once. Do
		// not manufacture another move merely because its audit row ID differs.
		candidates, err := tx.Query(`SELECT h.before_json,h.after_json FROM requirement_activity_history h JOIN activities a ON a.id=h.activity_id AND a.tenant_id=h.tenant_id AND a.project_id=h.project_id AND a.requirement_id=h.requirement_id WHERE h.tenant_id=? AND h.project_id=? AND h.requirement_id=? AND h.actor_id=? AND a.created_at=? AND a.event=?`, x.tenant, x.project, id, x.actorID, x.at, event)
		if err != nil {
			return err
		}
		existing := false
		for candidates.Next() {
			var leftRaw, rightRaw string
			if err = candidates.Scan(&leftRaw, &rightRaw); err != nil {
				break
			}
			var left, right map[string]any
			if json.Unmarshal([]byte(leftRaw), &left) == nil && json.Unmarshal([]byte(rightRaw), &right) == nil && reflect.DeepEqual(requirementHistorySnapshot(left), before) && reflect.DeepEqual(requirementHistorySnapshot(right), after) {
				existing = true
			}
		}
		if err == nil {
			err = candidates.Err()
		}
		candidates.Close()
		if err != nil {
			return err
		}
		if existing {
			continue
		}
		// Link only an unambiguous existing activity with the exact original actor
		// and timestamp. Never claim a prose-only record has a known old value.
		var count int
		var activityID sql.NullInt64
		if err = tx.QueryRow(`SELECT COUNT(*),MIN(a.id) FROM activities a WHERE a.tenant_id=? AND a.project_id=? AND a.requirement_id=? AND a.actor=? AND a.created_at=? AND a.event=? AND NOT EXISTS(SELECT 1 FROM requirement_activity_history h WHERE h.activity_id=a.id)`, x.tenant, x.project, id, x.actor, x.at, event).Scan(&count, &activityID); err != nil {
			return err
		}
		if count != 1 {
			res, err := tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, x.tenant, x.project, id, x.actor, event, detail, x.at)
			if err != nil {
				return err
			}
			activityID.Int64, err = res.LastInsertId()
			if err != nil {
				return err
			}
		}
		delay := !creating && requirementIterationMoved(before, after)
		if _, err = tx.Exec(`INSERT INTO requirement_activity_history(activity_id,tenant_id,project_id,requirement_id,actor_id,before_json,after_json,iteration_delay,source_audit_id)VALUES(?,?,?,?,?,?,?,?,?)`, activityID.Int64, x.tenant, x.project, id, x.actorID, jsonText(before), jsonText(after), delay, x.id); err != nil {
			return err
		}
		if delay {
			if _, err = tx.Exec(`UPDATE requirements SET iteration_delay_count=iteration_delay_count+1 WHERE tenant_id=? AND project_id=? AND id=?`, x.tenant, x.project, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Restrict audit recovery to requirement fields; privileged audit metadata and
// unrelated payloads do not become visible to ordinary project members.
func requirementHistorySnapshot(values map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"title", "type", "description", "acceptance", "parentId", "category", "sprint", "status", "priority", "owner", "assignee", "assigneeUserIds", "ownerUserIds", "tags", "tagColors", "roleWeights", "remarks", "startDate", "endDate", "discipline", "progress", "estimatedHours", "actualHours", "sensitive", "authImpact", "customFields", "descriptionDoc", "descriptionMentionUserIds", "remarksMentionUserIds", "checklist", "attachment", "designLink", "comment", "relatedRequirement"} {
		if value, ok := values[key]; ok {
			result[key] = value
		}
	}
	return result
}

type requirementHistoryChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type requirementHistoryItem struct {
	ID                  int64                      `json:"id"`
	Actor               string                     `json:"actor"`
	ActorID             string                     `json:"actorId"`
	ActorName           string                     `json:"actorName"`
	Event               string                     `json:"event"`
	Detail              string                     `json:"detail"`
	CreatedAt           string                     `json:"createdAt"`
	Sprint              *string                    `json:"sprint"`
	Status              *string                    `json:"status"`
	Changes             []requirementHistoryChange `json:"changes"`
	SnapshotAvailable   bool                       `json:"snapshotAvailable"`
	IterationDelay      bool                       `json:"iterationDelay"`
	IterationDelayCount int                        `json:"iterationDelayCount"`
	Recovered           bool                       `json:"recovered"`
}

func requirementHistoryChanges(before, after map[string]any) []requirementHistoryChange {
	changes := []requirementHistoryChange{}
	for _, key := range requirementActualChanges(before, after) {
		// Keep rich structure too: formatting, images and links can change while
		// plain text stays identical. The UI displays these values as escaped text.
		if key == "customFields" || key == "roleWeights" {
			left, _ := before[key].(map[string]any)
			right, _ := after[key].(map[string]any)
			for _, field := range requirementActualChanges(left, right) {
				changes = append(changes, requirementHistoryChange{key + "." + field, left[field], right[field]})
			}
			continue
		}
		changes = append(changes, requirementHistoryChange{key, before[key], after[key]})
	}
	return changes
}

func populateRequirementHistory(x *requirementHistoryItem, before, after map[string]any) {
	x.Changes = []requirementHistoryChange{}
	if !x.SnapshotAvailable {
		return
	}
	x.Changes = requirementHistoryChanges(requirementHistorySnapshot(before), requirementHistorySnapshot(after))
	if x.Recovered && x.Event != "created" {
		confirmed := x.Changes[:0]
		for _, change := range x.Changes {
			key := strings.SplitN(change.Field, ".", 2)[0]
			_, hasBefore := before[key]
			_, hasAfter := after[key]
			if hasBefore && hasAfter {
				confirmed = append(confirmed, change)
			}
		}
		x.Changes = confirmed
	}
	if value, ok := after["sprint"].(string); ok {
		x.Sprint = &value
	}
	if value, ok := after["status"].(string); ok {
		x.Status = &value
	}
}

// RFC3339 timestamps can mix seconds, fractions and offsets. String sorting
// incorrectly orders "00.5Z" before "00Z", so sort instants before counting.
func orderRequirementHistory(items []requirementHistoryItem) int {
	sort.SliceStable(items, func(i, j int) bool {
		left, le := time.Parse(time.RFC3339Nano, items[i].CreatedAt)
		right, re := time.Parse(time.RFC3339Nano, items[j].CreatedAt)
		if le == nil && re == nil {
			if !left.Equal(right) {
				return left.Before(right)
			}
		} else if (le == nil) != (re == nil) {
			return le != nil // Unparseable legacy dates precede known instants.
		} else if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt < items[j].CreatedAt
		}
		return items[i].ID < items[j].ID
	})
	count := 0
	for i := range items {
		if items[i].IterationDelay {
			count++
		}
		items[i].IterationDelayCount = count
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return count
}

func (a *App) recordRequirementRelatedHistory(tx *sql.Tx, activityID, requirementID int64, key string, before, after any) error {
	var sprint, status string
	if err := tx.QueryRow(`SELECT sprint,status FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), requirementID).Scan(&sprint, &status); err != nil {
		return err
	}
	return a.recordRequirementHistory(tx, activityID, requirementID, map[string]any{"sprint": sprint, "status": status, key: before}, map[string]any{"sprint": sprint, "status": status, key: after}, false)
}

func (a *App) recordRequirementChecklistHistory(tx *sql.Tx, requirementID int64, before, after any) error {
	return a.recordRequirementRelatedChange(tx, requirementID, "checklist", "checklist.updated", "检查项", before, after)
}

func (a *App) recordRequirementRelatedChange(tx *sql.Tx, requirementID int64, key, event, detail string, before, after any) error {
	actor := a.uid()
	if err := tx.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, actor, event, detail, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	return a.recordRequirementRelatedHistory(tx, id, requirementID, key, before, after)
}

func (a *App) requirementHistory(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.requireEntity(w, "requirement", id) {
		return
	}
	// 旧活动可能只存了 u_xxx 一类账号标识；新记录同时有 actor_id。
	// 读取时在同一租户内补齐姓名，让详情页无需把内部账号直接展示给成员。
	rows, err := a.db.QueryContext(r.Context(), `SELECT a.id,a.actor,a.event,a.detail,a.created_at,COALESCE(h.actor_id,''),COALESCE(u.name,''),h.before_json,h.after_json,COALESCE(h.iteration_delay,0),h.source_audit_id FROM activities a LEFT JOIN requirement_activity_history h ON h.activity_id=a.id AND h.tenant_id=a.tenant_id AND h.project_id=a.project_id AND h.requirement_id=a.requirement_id LEFT JOIN users u ON u.tenant_id=a.tenant_id AND u.id=CASE WHEN COALESCE(h.actor_id,'')<>'' THEN h.actor_id ELSE a.actor END WHERE a.tenant_id=? AND a.project_id=? AND a.requirement_id=? ORDER BY a.created_at,a.id`, tenantID, a.pid(), id)
	if err != nil {
		fail(w, 503, "database_unavailable", "变更记录暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := []requirementHistoryItem{}
	for rows.Next() {
		var x requirementHistoryItem
		var beforeRaw, afterRaw sql.NullString
		var auditID sql.NullInt64
		if err = rows.Scan(&x.ID, &x.Actor, &x.Event, &x.Detail, &x.CreatedAt, &x.ActorID, &x.ActorName, &beforeRaw, &afterRaw, &x.IterationDelay, &auditID); err != nil {
			break
		}
		x.SnapshotAvailable = beforeRaw.Valid && afterRaw.Valid
		x.Recovered = auditID.Valid
		var before, after map[string]any
		if x.SnapshotAvailable {
			before, _ = redactAuditJSON(beforeRaw.String).(map[string]any)
			after, _ = redactAuditJSON(afterRaw.String).(map[string]any)
		}
		populateRequirementHistory(&x, before, after)
		items = append(items, x)
	}
	if err != nil || rows.Err() != nil {
		fail(w, 503, "database_unavailable", "变更记录暂时无法读取，请稍后重试")
		return
	}
	count := orderRequirementHistory(items)
	write(w, 200, map[string]any{"items": items, "iterationDelayCount": count})
}
