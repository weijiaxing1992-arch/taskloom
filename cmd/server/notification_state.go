package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const notificationBatchLimit = 100

type notificationStateResult struct {
	Updated int64 `json:"updated"`
	Unread  int   `json:"unread"`
}

func failNotificationState(w http.ResponseWriter, err error) {
	var problem *organizationError
	if errors.As(err, &problem) {
		fail(w, problem.Status, problem.Code, problem.Message)
		return
	}
	fail(w, 503, "database_unavailable", "通知记录暂时无法保存，请稍后重试")
}

func decodeNotificationState(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func (a *App) notificationBatchState(w http.ResponseWriter, r *http.Request, all bool) {
	var body struct {
		IDs  []int64 `json:"ids"`
		Read *bool   `json:"read"`
	}
	read := true
	if !all {
		if decodeNotificationState(w, r, &body) != nil || body.Read == nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		read = *body.Read
	}
	result, err := a.setNotificationState(r.Context(), body.IDs, read, all)
	if err != nil {
		failNotificationState(w, err)
		return
	}
	write(w, 200, result)
}

func (a *App) setNotificationState(ctx context.Context, ids []int64, read, all bool) (notificationStateResult, error) {
	result := notificationStateResult{}
	if !all {
		if len(ids) == 0 || len(ids) > notificationBatchLimit {
			return result, &organizationError{400, "invalid_notification_ids", "每次请选择 1–100 条通知"}
		}
		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return result, &organizationError{400, "invalid_notification_ids", "通知编号无效或重复"}
			}
			seen[id] = true
		}
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// 先获取 SQLite 写锁，再复核原会话、账号及每条通知的项目权限。
	// 与成员撤权和注销串行化，拒绝使用等待写锁之前的认证快照。
	if _, err := tx.ExecContext(ctx, `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		return result, err
	}
	if a.impersonation != nil {
		return result, &organizationError{403, "impersonation_restricted", "代访问期间不能修改他人的通知状态"}
	}
	if err := a.requireAdministrationSession(ctx, tx); err != nil {
		return result, err
	}
	if err := a.requireOperationAccess(ctx, tx); err != nil {
		return result, err
	}
	var active int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, a.uid()).Scan(&active)
	if err != nil {
		return result, err
	}
	if active != 1 {
		return result, &organizationError{401, "unauthorized", "账号不可用"}
	}
	where := ` WHERE n.tenant_id=? AND n.recipient_user_id=?` + visibleNotificationSQL
	args := []any{tenantID, a.uid()}
	if !all {
		where += " AND n.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
		var visible int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_notifications n"+where, args...).Scan(&visible); err != nil {
			return result, err
		}
		// Mixed batches are rejected atomically, without revealing which ID was foreign or revoked.
		if visible != len(ids) {
			return result, &organizationError{404, "not_found", "通知不存在或已无访问权限，请刷新后重试"}
		}
	}
	var at any
	if read {
		at = orgNow()
		where += " AND n.read_at IS NULL"
	} else {
		where += " AND n.read_at IS NOT NULL"
	}
	updated, err := tx.ExecContext(ctx, "UPDATE user_notifications AS n SET read_at=?"+where, append([]any{at}, args...)...)
	if err != nil {
		return result, err
	}
	result.Updated, err = updated.RowsAffected()
	if err != nil {
		return result, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_notifications n WHERE n.tenant_id=? AND n.recipient_user_id=? AND n.read_at IS NULL`+visibleNotificationSQL, tenantID, a.uid()).Scan(&result.Unread); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return notificationStateResult{}, err
	}
	return result, nil
}
