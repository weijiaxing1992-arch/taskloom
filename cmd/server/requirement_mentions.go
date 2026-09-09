package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// 需求评论入口：普通文本兼容旧客户端，富文档使用经过校验的 mention 节点。
// 评论、附件落库、活动、审计和通知队列必须一起提交；任一步失败都不发布半条评论。
func (a *App) commentMentionRecipients(body string, requested *[]string) ([]string, error) {
	return a.commentMentionRecipientsUsing(a.db, body, requested)
}
func (a *App) commentMentionRecipientsUsing(query requirementPeopleQuery, body string, requested *[]string) ([]string, error) {
	// 使用调用方的事务/查询句柄读取当前项目活动成员，姓名只是显示值，ID 才是身份。
	// requested=nil 是旧版文本推断分支；显式空数组表示明确不提及任何人，不能混为一谈。
	// operation_disabled 的账号不能访问业务或查看通知；新提及也不能给它写入
	// 收件箱/企业微信投递，避免产生本人无法打开的外部提醒。
	rows, err := query.Query(`SELECT u.id,u.name FROM users u JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND pm.project_id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	byName := map[string][]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, err
		}
		byID[id] = name
		byName[name] = append(byName[name], id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	recipients := []string{}
	if requested != nil {
		seen := map[string]bool{}
		for _, id := range *requested {
			if _, ok := byID[id]; !ok {
				return nil, requirementPeopleValidationError("提及成员不是当前项目的有效成员，请重新选择")
			}
			if !seen[id] {
				recipients = append(recipients, id)
				seen[id] = true
			}
		}
	} else {
		// 旧版纯文本按完整姓名和词边界匹配；同名不猜人，须改用显式用户 ID。
		for name, ids := range byName {
			if name != "" && len(ids) == 1 && hasLegacyNameMention(body, name) {
				recipients = append(recipients, ids[0])
			}
		}
		sort.Strings(recipients)
	}
	if len(recipients) > 50 {
		return nil, requirementPeopleValidationError("一条评论最多提及 50 位成员")
	}
	return recipients, nil
}

func mentionWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func hasLegacyNameMention(body, name string) bool {
	needle := "@" + name
	for offset := 0; offset < len(body); {
		found := strings.Index(body[offset:], needle)
		if found < 0 {
			return false
		}
		start := offset + found
		end := start + len(needle)
		validStart, validEnd := true, true
		if start > 0 {
			before, _ := utf8.DecodeLastRuneInString(body[:start])
			validStart = !mentionWordRune(before)
		}
		if end < len(body) {
			after, _ := utf8.DecodeRuneInString(body[end:])
			validEnd = !mentionWordRune(after)
		}
		if validStart && validEnd {
			return true
		}
		offset = end
	}
	return false
}

func (a *App) requirementComments(w http.ResponseWriter, r *http.Request, requirementID int64) {
	// 所有查询都限定 tenant/project/requirement，父评论关联也不能跨工作项。
	// 外层路由认证并不替代 POST 在写事务内重查资源和当前操作权限。
	var exists int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE id=? AND tenant_id=? AND project_id=?`, requirementID, tenantID, a.pid()).Scan(&exists); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if exists == 0 {
		fail(w, 404, "not_found", "需求不存在")
		return
	}
	if r.Method == http.MethodGet {
		rows, err := a.db.QueryContext(r.Context(), `SELECT c.id,c.requirement_id,c.author,c.body,c.created_at,c.mention_user_ids_json,c.content_doc_json,c.author_user_id,c.reply_to_id,COALESCE(p.author_user_id,''),COALESCE(p.author,'') FROM comments c LEFT JOIN comments p ON p.id=c.reply_to_id AND p.tenant_id=c.tenant_id AND p.project_id=c.project_id AND p.requirement_id=c.requirement_id WHERE c.tenant_id=? AND c.project_id=? AND c.requirement_id=? ORDER BY c.id DESC`, tenantID, a.pid(), requirementID)
		if err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
		defer rows.Close()
		items := []Comment{}
		for rows.Next() {
			var item Comment
			var rawMentions string
			var rawDoc sql.NullString
			if err := rows.Scan(&item.ID, &item.RequirementID, &item.Author, &item.Body, &item.CreatedAt, &rawMentions, &rawDoc, &item.AuthorUserID, &item.ReplyToID, &item.ReplyToAuthorUserID, &item.ReplyToAuthor); err != nil {
				fail(w, 500, "db_error", err.Error())
				return
			}
			item.ContentDoc, err = readRichDocument(rawDoc)
			if err != nil {
				fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
				return
			}
			if err := json.Unmarshal([]byte(rawMentions), &item.MentionUserIDs); err != nil {
				fail(w, 500, "db_error", "评论提及数据格式无效")
				return
			}
			if item.MentionUserIDs == nil {
				item.MentionUserIDs = []string{}
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body struct {
		Body           string          `json:"body"`
		MentionUserIDs *[]string       `json:"mentionUserIds"`
		ContentDoc     json.RawMessage `json:"contentDoc"`
		ReplyToID      *int64          `json:"replyToId"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	doc, docErr := parseRichDocument(body.ContentDoc)
	if docErr != nil {
		failRichDocument(w, docErr)
		return
	}
	if doc != nil {
		body.Body = doc.plainText()
	}
	if doc != nil && strings.TrimSpace(body.Body) == "" {
		fail(w, 422, "validation_error", "评论内容不能为空")
		return
	}
	if doc == nil && (strings.TrimSpace(body.Body) == "" || utf8.RuneCountInString(body.Body) > 20000) {
		fail(w, 422, "validation_error", "评论须为 1–20000 字")
		return
	}
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	defer tx.Rollback()
	if err := a.verifyRequirementResourceParent(tx, requirementID); err != nil {
		failRichDocument(w, err)
		return
	}
	parent, err := a.commentReplyParent(r.Context(), tx, "requirement", requirementID, body.ReplyToID)
	if err != nil {
		failCollaboration(w, err)
		return
	}
	var actor string
	if err = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor); err != nil {
		failCollaboration(w, err)
		return
	}
	comment := Comment{commentReply: parent, RequirementID: requirementID, Author: actor, AuthorUserID: a.uid(), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if doc != nil {
		if err := a.validateRichMentions(tx, doc, nil); err != nil {
			failRichDocument(w, err)
			return
		}
		ids := doc.mentionIDs()
		body.MentionUserIDs = &ids
		comment.ContentDoc, err = a.persistRichDocument(tx, doc, requirementID, comment.CreatedAt)
		if err != nil {
			failRichDocument(w, err)
			return
		}
		body.Body = doc.plainText()
	}
	recipients, err := a.commentMentionRecipientsUsing(tx, body.Body, body.MentionUserIDs)
	if err != nil {
		var invalid requirementPeopleValidationError
		if errors.As(err, &invalid) {
			fail(w, 422, "invalid_mentions", err.Error())
		} else {
			failRichDocument(w, err)
		}
		return
	}
	// 富文档节点已绑定校验过的 ID 与可见姓名；旧版显式 ID 也必须出现在正文 @姓名 中，
	// 不能只提交任意收件人列表就触发通知。
	if doc == nil && body.MentionUserIDs != nil {
		for _, recipient := range recipients {
			var name string
			if err := tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, recipient).Scan(&name); err != nil {
				failRichDocument(w, err)
				return
			}
			if !hasSelectedCommentMention(body.Body, name) {
				fail(w, 422, "invalid_mentions", "提及成员必须在评论正文中以 @姓名 出现")
				return
			}
		}
	}
	comment.Body = body.Body
	// 仅在确定评论 ID 后生成回复/提及通知去重键；Commit 前不向外部通知服务发送请求。
	comment.MentionUserIDs = recipients
	res, err := tx.ExecContext(r.Context(), `INSERT INTO comments(tenant_id,project_id,requirement_id,author,body,created_at,mention_user_ids_json,content_doc_json,author_user_id,reply_to_id)VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), requirementID, comment.Author, comment.Body, comment.CreatedAt, jsonText(comment.MentionUserIDs), richSQL(comment.ContentDoc), comment.AuthorUserID, comment.ReplyToID)
	if err == nil {
		comment.ID, err = res.LastInsertId()
	}
	if err == nil && doc != nil {
		err = a.auditRichDocument(tx, requirementID, comment.ID, comment.ContentDoc, comment.CreatedAt)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,'commented','添加了评论',?)`, tenantID, a.pid(), requirementID, comment.Author, comment.CreatedAt)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,'requirement',?,'comment_created',?,?)`, tenantID, a.pid(), a.uid(), requirementID, jsonText(map[string]any{"commentId": comment.ID, "replyToId": comment.ReplyToID, "mentionUserIds": recipients}), comment.CreatedAt)
	}
	if err == nil {
		err = a.notifyComment(r.Context(), tx, "requirement", requirementID, comment.ID, comment.Body, comment.CreatedAt, recipients, parent)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 201, comment)
}
