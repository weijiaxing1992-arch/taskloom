package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// assignmentNotice 表示新增的稳定身份分配。分配给自己与评论中显式 @ 自己
// 都是用户可见的待办提醒；两类通知保持独立，避免职责变更被评论去重规则吞掉。
type assignmentNotice struct {
	Event      string
	Subject    string
	SubjectID  int64
	Field      string
	Title      string
	Body       string
	Recipients []string
}

// activeAssignmentRecipients 与通知读取共用项目可见性边界，只接收已保存的
// 用户 ID，不按显示名猜测。停用、业务禁用或退出项目的成员不新收无法访问的通知。
func (a *App) activeAssignmentRecipients(ctx context.Context, tx *sql.Tx, candidates []string) ([]string, error) {
	seen := map[string]bool{}
	recipients := []string{}
	for _, rawID := range candidates {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var allowed int
		err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u
			JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id
			WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'
			AND (tm.role='tenant_admin' OR EXISTS(
				SELECT 1 FROM project_members pm
				WHERE pm.tenant_id=u.tenant_id AND pm.project_id=? AND pm.user_id=u.id
			))`, tenantID, id, a.pid()).Scan(&allowed)
		if err != nil {
			return nil, err
		}
		if allowed == 1 {
			recipients = append(recipients, id)
		}
	}
	// 输入可能来自职能权重等 JSON 映射；稳定写入顺序，使外发载荷与回归结果可复现。
	sort.Strings(recipients)
	return recipients, nil
}

// firstActiveAssignmentRecipient 保留调用方给出的职责优先级：例如测试失败
// 优先通知计划负责人，计划负责人无效时才通知用例负责人。不能把候选整体排序后
// 再取第一个，否则会把“主负责人优先”的业务规则悄悄变成按 ID 排序。
func (a *App) firstActiveAssignmentRecipient(ctx context.Context, tx *sql.Tx, candidates []string) (string, error) {
	seen := map[string]bool{}
	for _, rawID := range candidates {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		allowed, err := a.activeAssignmentRecipients(ctx, tx, []string{id})
		if err != nil {
			return "", err
		}
		if len(allowed) == 1 {
			return allowed[0], nil
		}
	}
	return "", nil
}

// writeAssignmentNotices 在调用方业务事务中同时写入站内通知和持久化外发队列。
// 随机事件键区分先前分配与后续重新分配，同一事件内按收件人去重；任一写入失败
// 均交由调用方回滚，不在提交前向外部服务发送消息。
func (a *App) writeAssignmentNotices(ctx context.Context, tx *sql.Tx, notices []assignmentNotice, now string) error {
	for _, notice := range notices {
		if notice.Event == "" || notice.Subject == "" || notice.SubjectID <= 0 {
			return fmt.Errorf("assignment notification is missing its event or subject")
		}
		recipients, err := a.activeAssignmentRecipients(ctx, tx, notice.Recipients)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			continue
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return err
		}
		key := fmt.Sprintf("assignment:%s:%s:%d:%s:%s", a.pid(), notice.Subject, notice.SubjectID, notice.Event, hex.EncodeToString(nonce[:]))
		for _, recipient := range recipients {
			if _, err = tx.ExecContext(ctx, `INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), recipient, a.uid(), notice.Event, notice.Subject, notice.SubjectID, notice.Title, notice.Body, now, key+":"+recipient); err != nil {
				return err
			}
		}
		payload := jsonText(map[string]any{
			"subjectType":      notice.Subject,
			"subjectId":        notice.SubjectID,
			"sourceField":      notice.Field,
			"title":            notice.Title,
			"body":             notice.Body,
			"recipientUserIds": recipients,
			"actorUserId":      a.uid(),
		})
		if _, err = tx.ExecContext(ctx, `INSERT INTO notification_outbox(tenant_id,project_id,event_type,payload,dedupe_key,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), notice.Event, payload, key, now, now); err != nil {
			return err
		}
	}
	return nil
}

// defectAssignmentNotices 仅为新增稳定 ID 生成分配事件。处理人和验证人是不同
// 职责，同一人同时承担二者时分别收到明确提醒，而非含义不清的通用更新。
func defectAssignmentNotices(id int64, code, title, assigneeID, verifierID, previousAssigneeID, previousVerifierID string) []assignmentNotice {
	body := strings.TrimSpace(strings.TrimSpace(code) + " " + strings.TrimSpace(title))
	if body == "" {
		body = fmt.Sprintf("BUG-%04d", id)
	}
	notices := []assignmentNotice{}
	if assigneeID != "" && assigneeID != previousAssigneeID {
		event := "defect.assigned"
		if previousAssigneeID != "" {
			event = "defect.assignee_changed"
		}
		notices = append(notices, assignmentNotice{Event: event, Subject: "defect", SubjectID: id, Field: "assigneeUserId", Title: "有新的工作项分配给你", Body: body, Recipients: []string{assigneeID}})
	}
	if verifierID != "" && verifierID != previousVerifierID {
		event := "defect.verifier_assigned"
		if previousVerifierID != "" {
			event = "defect.verifier_changed"
		}
		notices = append(notices, assignmentNotice{Event: event, Subject: "defect", SubjectID: id, Field: "verifierUserId", Title: "你被指定为缺陷验证人", Body: body, Recipients: []string{verifierID}})
	}
	return notices
}

// sprintLifecycleNotices builds the status/completion notice before the
// surrounding sprint transaction commits.  The write path validates each
// recipient again, writes the inbox and outbox together, and makes a failed
// notification a failed state transition instead of a silent post-commit loss.
func (a *App) sprintLifecycleNotices(ctx context.Context, tx *sql.Tx, id int64, event, title, body string) ([]assignmentNotice, error) {
	// 迭代沿用项目管理归属；发送前仍由 writeAssignmentNotices 校验账号及项目权限。
	var recipient string
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM projects WHERE tenant_id=? AND id=?`, tenantID, a.pid()).Scan(&recipient); err != nil {
		return nil, err
	}
	if recipient == "" {
		return nil, nil
	}
	recipients := []string{recipient}
	return []assignmentNotice{{
		Event:      event,
		Subject:    "sprint",
		SubjectID:  id,
		Field:      "status",
		Title:      title,
		Body:       body,
		Recipients: recipients,
	}}, nil
}
