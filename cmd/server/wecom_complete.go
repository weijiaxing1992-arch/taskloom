package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var notificationGroupNames = map[string]string{"mentions": "提及与回复", "handoffs": "分配与交接", "changes": "需求与状态变更", "activity": "其他动态"}

// 群机器人不是系统账号：链接仍需要登录并校验项目权限，不附带 Cookie 或访问令牌。
func wecomDetailURL(origin, subject string, id int64, project string) string {
	base, err := url.Parse(origin)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return ""
	}
	link := notificationURL(subject, id)
	separator := "?"
	if strings.Contains(link, "?") {
		separator = "&"
	}
	result := strings.TrimRight(origin, "/") + link + separator + "project=" + url.QueryEscape(project)
	if len(result) > 768 {
		return ""
	}
	return result
}

// 每段均带通知编号、分类、段号、时间和完整链接。仅按 UTF-8 边界分割，绝不截断正文。
// 使用 text 而非把用户内容当 Markdown/HTML 解释，保留代码、中文和换行。
func splitWecomNotice(notificationID int64, group, content, created, subject string, id int64, project, origin string) []string {
	stamp := created
	if value, err := time.Parse(time.RFC3339, created); err == nil {
		stamp = value.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05 UTC+08:00")
	}
	footer := "\n\n通知时间：" + truncateWebhookText(stamp, 100)
	if link := wecomDetailURL(origin, subject, id, project); link != "" {
		footer += "\n查看详情：" + link
	} else {
		footer += "\n详情入口未配置，请登录 TaskLoom 查看对应事项。"
	}
	prefix := fmt.Sprintf("【TaskLoom｜%s】通知 #%d", notificationGroupNames[group], notificationID)
	// 预留段号空间，保证最坏情况下也不超过官方 text 消息的 2048 字节上限。
	budget := 2048 - len(prefix) - len(footer) - 64
	pieces := []string{}
	for len(content) > 0 {
		end := min(len(content), budget)
		for end > 0 && !utf8.ValidString(content[:end]) {
			end--
		}
		if end == 0 {
			content = strings.ToValidUTF8(content, "�")
			continue
		}
		pieces = append(pieces, content[:end])
		content = content[end:]
	}
	if len(pieces) == 0 {
		pieces = append(pieces, "")
	}
	for i := range pieces {
		pieces[i] = fmt.Sprintf("%s（%d/%d）\n\n%s%s", prefix, i+1, len(pieces), pieces[i], footer)
	}
	return pieces
}

func wecomNextAction(group string) string {
	switch group {
	case "mentions":
		return "请阅读本条提及或回复，并进入事项继续讨论。"
	case "handoffs":
		return "请核对分配或交接内容，进入事项确认后续处理。"
	case "changes":
		return "请核对变更内容及其对当前工作的影响。"
	default:
		return "请查看事件详情；需要处理时进入对应事项。"
	}
}

// 首次发送前冻结消息快照，重试和后续分段不再读取变化后的业务内容，避免前后段互相矛盾。
// 所有上下文查询严格绑定通知本身的企业与项目，不依赖当前网页选择。
func (a *App) nextWecomPart(ctx context.Context, job wecomJob, title, body, created, subject string, subjectID int64, project string) (string, int, error) {
	var content string
	var index int
	err := a.db.QueryRowContext(ctx, `SELECT content,part_index FROM user_wecom_parts WHERE delivery_id=? AND status='pending' ORDER BY part_index LIMIT 1`, job.ID).Scan(&content, &index)
	if err == nil {
		return content, index, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	var group, projectName, actor, recipient, event string
	err = a.db.QueryRowContext(ctx, `SELECT `+notificationGroupSQL+`,p.name,COALESCE(actor.name,'系统'),recipient.name,n.event_type FROM user_notifications n JOIN projects p ON p.tenant_id=n.tenant_id AND p.id=n.project_id JOIN users recipient ON recipient.tenant_id=n.tenant_id AND recipient.id=n.recipient_user_id LEFT JOIN users actor ON actor.tenant_id=n.tenant_id AND actor.id=n.actor_user_id WHERE n.tenant_id=? AND n.id=? AND n.recipient_user_id=?`+visibleNotificationSQL, tenantID, job.NotificationID, job.User).Scan(&group, &projectName, &actor, &recipient, &event)
	if err != nil {
		return "", 0, err
	}
	summary, err := a.wecomSubjectSummary(ctx, subject, subjectID, project)
	if err != nil {
		return "", 0, err
	}
	full := fmt.Sprintf("%s\n项目：%s\n接收人：%s\n操作人：%s\n事件：%s\n%s\n\n通知完整内容：\n%s\n\n后续处理：%s", title, projectName, recipient, actor, event, summary, body, wecomNextAction(group))
	parts := splitWecomNotice(job.NotificationID, group, full, created, subject, subjectID, project, env("DEVFLOW_PUBLIC_URL", ""))
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM user_wecom_parts WHERE delivery_id=?`, job.ID).Scan(&exists); err != nil {
		return "", 0, err
	}
	if exists == 0 {
		for i, part := range parts {
			if _, err = tx.ExecContext(ctx, `INSERT INTO user_wecom_parts(delivery_id,part_index,content)VALUES(?,?,?)`, job.ID, i, part); err != nil {
				return "", 0, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	err = a.db.QueryRowContext(ctx, `SELECT content,part_index FROM user_wecom_parts WHERE delivery_id=? AND status='pending' ORDER BY part_index LIMIT 1`, job.ID).Scan(&content, &index)
	return content, index, err
}

func (a *App) wecomSubjectSummary(ctx context.Context, subject string, id int64, project string) (string, error) {
	query := ""
	switch subject {
	case "requirement":
		query = `SELECT code,title,status,priority,assignee,sprint FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`
	case "defect":
		query = `SELECT code,title,status,priority,assignee,sprint FROM defects WHERE tenant_id=? AND project_id=? AND id=?`
	case "test_case":
		query = `SELECT code,title,status,priority,owner,'' FROM test_cases WHERE tenant_id=? AND project_id=? AND id=?`
	case "test_plan":
		query = `SELECT code,name,status,'',owner,sprint FROM test_plans WHERE tenant_id=? AND project_id=? AND id=?`
	case "sprint":
		query = `SELECT code,name,status,'','','' FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`
	case "test_execution":
		query = `SELECT 'EXE-'||e.id,c.title,e.status,c.priority,e.executor,'' FROM test_executions e JOIN test_cases c ON c.tenant_id=e.tenant_id AND c.project_id=e.project_id AND c.id=e.case_id WHERE e.tenant_id=? AND e.project_id=? AND e.id=?`
	default:
		return fmt.Sprintf("关联对象：%s #%d", subject, id), nil
	}
	var code, title, status, priority, assignee, sprint string
	err := a.db.QueryRowContext(ctx, query, tenantID, project, id).Scan(&code, &title, &status, &priority, &assignee, &sprint)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("关联对象：%s #%d（当前记录不可用，以下保留事件原文）", subject, id), nil
	}
	if err != nil {
		return "", err
	}
	result := fmt.Sprintf("关联事项：%s %s\n发送时状态：%s", code, title, status)
	if priority != "" {
		result += "\n优先级：" + priority
	}
	if assignee != "" {
		result += "\n当前处理人：" + assignee
	}
	if sprint != "" {
		result += "\n所属迭代：" + sprint
	}
	return result, nil
}

// 一次节拍只发送一段；确认后的段不再重发，后续段单独重试。网络成功后崩溃仍可能重复当前段。
func (a *App) ackWecomPart(ctx context.Context, job wecomJob, now time.Time) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stamp := now.UTC().Format(time.RFC3339)
	result, err := tx.ExecContext(ctx, `UPDATE user_wecom_parts SET status='sent',sent_at=? WHERE delivery_id=? AND part_index=? AND status='pending' AND EXISTS(SELECT 1 FROM user_wecom_deliveries d WHERE d.id=delivery_id AND d.tenant_id=? AND d.status='sending' AND d.attempts=?)`, stamp, job.ID, job.PartIndex, tenantID, job.Attempts)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM user_wecom_parts WHERE delivery_id=? AND status='pending'`, job.ID).Scan(&remaining); err != nil {
		return err
	}
	status, sent, next := "sent", stamp, stamp
	if remaining > 0 {
		status = "pending"
		sent = ""
		next = now.Add(3 * time.Second).UTC().Format(time.RFC3339)
	}
	_, err = tx.ExecContext(ctx, `UPDATE user_wecom_deliveries SET status=?,attempts=CASE WHEN ?='pending' THEN 0 ELSE attempts END,last_error='',lease_until='',next_attempt_at=?,updated_at=?,sent_at=? WHERE id=? AND tenant_id=?`, status, status, next, stamp, sent, job.ID, tenantID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
