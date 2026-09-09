package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// 评论以平铺数组和稳定父 ID 返回，不在服务端递归构造深层树。
// 只有新评论可选择已存在的父评论，既不人为限制回复层数，也不允许改父关系制造循环。
// 作者姓名为历史快照，通知身份使用 author_user_id，不能反过来按当前姓名猜作者。
type commentReply struct {
	ReplyToID           *int64 `json:"replyToId"`
	ReplyToAuthorUserID string `json:"replyToAuthorUserId"`
	ReplyToAuthor       string `json:"replyToAuthor"`
}

func (a *App) migrateCommentThreads() error {
	// 两类评论表的加列、历史作者回填和索引创建在同一迁移事务中完成。
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"comments", "entity_comments"} {
		var authorColumn, replyColumn int
		if err = tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name='author_user_id'`, table).Scan(&authorColumn); err != nil {
			return err
		}
		if err = tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name='reply_to_id'`, table).Scan(&replyColumn); err != nil {
			return err
		}
		if authorColumn == 0 {
			if _, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN author_user_id TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
		if replyColumn == 0 {
			if _, err = tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN reply_to_id INTEGER DEFAULT NULL`); err != nil {
				return err
			}
			// 仅在新增回复列的这次迁移中，用精确 comment_created 审计的唯一 actor_id 回填。
			// 无唯一证据就保留空身份，即使现在只有一个同名用户也不能替历史评论认领作者。
			object, id := "c.object_type", "c.object_id"
			if table == "comments" {
				object, id = "'requirement'", "c.requirement_id"
			}
			evidence := ` FROM audit_logs al JOIN users u ON u.tenant_id=al.tenant_id AND u.id=al.actor_id WHERE al.tenant_id=c.tenant_id AND al.project_id=c.project_id AND al.object_type=` + object + ` AND al.object_id=CAST(` + id + ` AS TEXT) AND al.action='comment_created' AND CASE WHEN json_valid(al.after_json) THEN json_type(al.after_json,'$.commentId')='integer' AND json_extract(al.after_json,'$.commentId')=c.id ELSE 0 END`
			if _, err = tx.Exec(`UPDATE ` + table + ` AS c SET author_user_id=(SELECT min(al.actor_id)` + evidence + `) WHERE c.author_user_id='' AND (SELECT count(DISTINCT al.actor_id)` + evidence + `)=1`); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_` + table + `_reply ON ` + table + `(tenant_id,project_id,reply_to_id)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) commentReplyParent(ctx context.Context, tx *sql.Tx, object string, objectID int64, parentID *int64) (commentReply, error) {
	// 父评论必须在同租户、同项目、同工作项内；不能只凭全局递增评论 ID 查作者。
	parent := commentReply{ReplyToID: parentID}
	if parentID == nil {
		return parent, nil
	}
	if *parentID <= 0 {
		return parent, orgInvalid("回复目标必须为当前工作项的已有评论")
	}
	query := `SELECT author_user_id,author FROM entity_comments WHERE tenant_id=? AND project_id=? AND object_type=? AND object_id=? AND id=?`
	args := []any{tenantID, a.pid(), object, objectID, *parentID}
	if object == "requirement" {
		query = `SELECT author_user_id,author FROM comments WHERE tenant_id=? AND project_id=? AND requirement_id=? AND id=?`
		args = []any{tenantID, a.pid(), objectID, *parentID}
	}
	err := tx.QueryRowContext(ctx, query, args...).Scan(&parent.ReplyToAuthorUserID, &parent.ReplyToAuthor)
	if errors.Is(err, sql.ErrNoRows) {
		err = orgInvalid("回复目标必须为当前工作项的已有评论")
	}
	return parent, err
}

// 同一人既是被回复作者又被 @ 时，仅生成回复通知；仅“回复自己”不产生通知。
// 但显式 @ 自己是用户要求的待办提醒，必须写入收件箱，不能再把操作者预先排除。
// 站内通知与 outbox 跟随评论事务提交；稳定评论 ID + 收件人构成去重键。
func (a *App) notifyComment(ctx context.Context, tx *sql.Tx, object string, objectID, commentID int64, body, now string, mentions []string, parent commentReply) error {
	byEvent := map[string][]string{"mentioned": {}, "replied": {}}
	replyRecipient := parent.ReplyToAuthorUserID
	// 历史作者身份缺失时可继续回复，但不猜收件人；身份存在也须重查其项目访问资格。
	if parent.ReplyToID != nil && replyRecipient != "" && replyRecipient != a.uid() {
		var allowed int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active' AND (tm.role='tenant_admin' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=?))`, tenantID, replyRecipient, a.pid()).Scan(&allowed); err != nil {
			return err
		}
		if allowed == 1 {
			byEvent["replied"] = append(byEvent["replied"], replyRecipient)
		}
	}
	// 只用“已生成回复通知”的身份去重。若这里预先放入 a.uid()，显式 @ 自己
	// 会在落库前被吞掉，通知中心与评论中的提及状态便会不一致。
	seen := map[string]bool{}
	if len(byEvent["replied"]) != 0 {
		seen[replyRecipient] = true
	}
	for _, recipient := range mentions {
		if !seen[recipient] {
			byEvent["mentioned"] = append(byEvent["mentioned"], recipient)
			seen[recipient] = true
		}
	}
	key := fmt.Sprintf("entity-comment:%s:%s:%d:%d", a.pid(), object, objectID, commentID)
	if object == "requirement" {
		key = fmt.Sprintf("requirement-comment:%s:%d:%d", a.pid(), objectID, commentID)
	}
	for _, kind := range []string{"mentioned", "replied"} {
		recipients := byEvent[kind]
		if len(recipients) == 0 {
			continue
		}
		title, outboxKey := "你在评论中被提及", key
		if kind == "replied" {
			title, outboxKey = "你的评论收到回复", key+":reply"
		}
		for _, recipient := range recipients {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), recipient, a.uid(), object+"."+kind, object, objectID, title, body, now, key+":"+recipient); err != nil {
				return err
			}
		}
		payload := map[string]any{"subjectType": object, "subjectId": objectID, "commentId": commentID, "replyToId": parent.ReplyToID, "body": body, "recipientUserIds": recipients, "actorUserId": a.uid()}
		if kind == "mentioned" {
			payload["mentionUserIds"] = recipients // 兼容旧 outbox 消费者；回复事件统一使用 recipientUserIds。
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_outbox(tenant_id,project_id,event_type,payload,dedupe_key,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), object+"."+kind, jsonText(payload), outboxKey, now, now); err != nil {
			return err
		}
	}
	return nil
}
