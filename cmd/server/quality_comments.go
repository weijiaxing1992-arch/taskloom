package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 缺陷、测试用例、测试计划和执行记录共用的纯文本讨论接口，数据存 entity_comments。
// 需求富文本评论在 requirement_mentions.go；两者复用回复父级和通知去重规则。
func (a *App) migrateQualityCollaboration() error {
	for _, column := range []struct{ name, definition string }{{"author_user_id", `TEXT NOT NULL DEFAULT ''`}, {"mention_user_ids_json", `TEXT NOT NULL DEFAULT '[]'`}} {
		exists, err := a.databaseHasColumns("entity_comments", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err = a.db.Exec(`ALTER TABLE entity_comments ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return err
			}
		}
	}
	// 关联筛选按项目/对象读取评论，避免每个缺陷扫描整张评论表。
	_, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_entity_comments_object_scope ON entity_comments(tenant_id,project_id,object_type,object_id)`)
	return err
}
func failCollaboration(w http.ResponseWriter, err error) {
	var invalid *organizationError
	if errors.As(err, &invalid) {
		fail(w, invalid.Status, invalid.Code, invalid.Message)
		return
	}
	var mention requirementPeopleValidationError
	if errors.As(err, &mention) {
		fail(w, 422, "invalid_mentions", mention.Error())
		return
	}
	fail(w, 503, "collaboration_unavailable", "协作服务暂时不可用，请稍后重试")
}
func (a *App) beginCollaborationWrite(r *http.Request) (*sql.Tx, error) {
	// 先取得租户写锁，再检查账号业务禁用、活动成员及项目角色，防目录撤权竞态。
	// 返回事务由调用方统一提交/回滚，不能在权限检查之后另开一个未受保护的写事务。
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err = a.requireOperationAccess(r.Context(), tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	var role string
	err = tx.QueryRowContext(r.Context(), `SELECT CASE WHEN tm.role='tenant_admin' THEN 'tenant_admin' ELSE COALESCE(pm.role,'') END FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id AND pm.project_id=? WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, a.pid(), tenantID, a.uid()).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (role == "" || role == "viewer") {
		err = &organizationError{403, "forbidden", "当前角色仅可查看"}
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func (a *App) collaborationEntityExists(ctx context.Context, store stateStore, object string, id int64) error {
	// 表名只能来自本地白名单，不能把用户 object 字符串直接拼 SQL；对象须同租户/项目。
	table := map[string]string{"requirement": "requirements", "defect": "defects", "test_case": "test_cases", "test_plan": "test_plans", "test_execution": "test_executions"}[object]
	if table == "" || id <= 0 {
		return orgNotFound()
	}
	var count int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return orgNotFound()
	}
	return nil
}

type qualityComment struct {
	commentReply
	ID             int64    `json:"id"`
	Author         string   `json:"author"`
	AuthorUserID   string   `json:"authorUserId"`
	Body           string   `json:"body"`
	MentionUserIDs []string `json:"mentionUserIds"`
	CreatedAt      string   `json:"createdAt"`
}

func hasSelectedCommentMention(body, name string) bool {
	needle := "@" + name
	for offset := 0; offset < len(body); {
		index := strings.Index(body[offset:], needle)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(needle)
		beforeOK := true
		if start > 0 {
			before := body[start-1]
			beforeOK = !(before >= 'a' && before <= 'z' || before >= 'A' && before <= 'Z' || before >= '0' && before <= '9' || strings.ContainsRune("._%+-", rune(before)))
		}
		afterOK := end == len(body)
		if !afterOK {
			after, _ := utf8.DecodeRuneInString(body[end:])
			afterOK = unicode.IsSpace(after) || strings.ContainsRune("@,，。！？!?;；:：()[]{}", after)
		}
		if beforeOK && afterOK {
			return true
		}
		offset = end
	}
	return false
}

func (a *App) qualityComments(w http.ResponseWriter, r *http.Request, object string, id int64) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if err := a.collaborationEntityExists(r.Context(), a.db, object, id); err != nil {
		failCollaboration(w, err)
		return
	}
	if r.Method == http.MethodGet {
		rows, err := a.db.QueryContext(r.Context(), `SELECT c.id,c.author,c.author_user_id,c.body,c.mention_user_ids_json,c.created_at,c.reply_to_id,COALESCE(p.author_user_id,''),COALESCE(p.author,'') FROM entity_comments c LEFT JOIN entity_comments p ON p.id=c.reply_to_id AND p.tenant_id=c.tenant_id AND p.project_id=c.project_id AND p.object_type=c.object_type AND p.object_id=c.object_id WHERE c.tenant_id=? AND c.project_id=? AND c.object_type=? AND c.object_id=? ORDER BY c.id DESC`, tenantID, a.pid(), object, id)
		if err != nil {
			failCollaboration(w, err)
			return
		}
		defer rows.Close()
		items := []qualityComment{}
		for rows.Next() {
			var c qualityComment
			var raw string
			if err = rows.Scan(&c.ID, &c.Author, &c.AuthorUserID, &c.Body, &raw, &c.CreatedAt, &c.ReplyToID, &c.ReplyToAuthorUserID, &c.ReplyToAuthor); err == nil {
				err = json.Unmarshal([]byte(raw), &c.MentionUserIDs)
			}
			if err != nil {
				failCollaboration(w, err)
				return
			}
			if c.MentionUserIDs == nil {
				c.MentionUserIDs = []string{}
			}
			items = append(items, c)
		}
		if err = rows.Err(); err != nil {
			failCollaboration(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	var b struct {
		Body           string   `json:"body"`
		MentionUserIDs []string `json:"mentionUserIds"`
		ReplyToID      *int64   `json:"replyToId"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&b) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if strings.TrimSpace(b.Body) == "" || utf8.RuneCountInString(b.Body) > 20000 {
		fail(w, 422, "validation_error", "评论须为 1–20000 字")
		return
	}
	if len(b.MentionUserIDs) > 100 {
		fail(w, 422, "invalid_mentions", "一条评论最多提及 50 位成员")
		return
	}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.collaborationEntityExists(r.Context(), tx, object, id); err != nil {
		failCollaboration(w, err)
		return
	}
	parent, err := a.commentReplyParent(r.Context(), tx, object, id, b.ReplyToID)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	// 这里仅显式 ID 决定收件人，且每位必须实际出现在正文；手输自由文本 @姓名不发通知。
	// nil 规范化为空数组，刻意不进入需求旧版文本推断兼容分支。
	if b.MentionUserIDs == nil {
		b.MentionUserIDs = []string{}
	}
	recipients, err := a.commentMentionRecipientsUsing(tx, b.Body, &b.MentionUserIDs)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	for _, recipient := range recipients {
		var name string
		if err = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, recipient).Scan(&name); err != nil {
			failCollaboration(w, err)
			return
		}
		if !hasSelectedCommentMention(b.Body, name) {
			failCollaboration(w, orgInvalid("提及成员必须在评论正文中以 @姓名 出现"))
			return
		}
	}
	var actor string
	if err = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		failCollaboration(w, err)
		return
	}
	c := qualityComment{commentReply: parent, Author: actor, AuthorUserID: a.uid(), Body: b.Body, MentionUserIDs: recipients, CreatedAt: orgNow()}
	// 评论、活动、审计、收件箱和 outbox 原子提交；不能发生“保存失败但通知已发”。
	result, err := tx.ExecContext(r.Context(), `INSERT INTO entity_comments(tenant_id,project_id,object_type,object_id,author,author_user_id,body,mention_user_ids_json,created_at,reply_to_id)VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), object, id, c.Author, c.AuthorUserID, c.Body, jsonText(c.MentionUserIDs), c.CreatedAt, c.ReplyToID)
	if err == nil {
		c.ID, err = result.LastInsertId()
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,'commented','添加了评论',?)`, tenantID, a.pid(), object, id, c.Author, c.CreatedAt)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,?,?,'comment_created',?,?)`, tenantID, a.pid(), a.uid(), object, fmt.Sprint(id), jsonText(map[string]any{"commentId": c.ID, "replyToId": c.ReplyToID, "mentionUserIds": recipients}), c.CreatedAt)
	}
	if err == nil {
		err = a.notifyComment(r.Context(), tx, object, id, c.ID, c.Body, c.CreatedAt, recipients, parent)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failCollaboration(w, err)
		return
	}
	write(w, 201, c)
}
