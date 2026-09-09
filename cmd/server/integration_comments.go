package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

// 外部评论读取使用独立分页，避免旧页面的一次性全量读取被自动化循环放大。
func (a *App) integrationComments(w http.ResponseWriter, r *http.Request, resource string, id int64) {
	for key := range r.URL.Query() {
		if key != "page" && key != "pageSize" && key != "limit" {
			fail(w, 400, "invalid_query", "评论仅支持分页参数")
			return
		}
	}
	_, _, page, size, err := integrationListQuery(resource, r.URL.Query())
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	if _, err := a.integrationEntityETag(r.Context(), a.db, integrationResources[resource], id); err != nil {
		integrationReadError(w, err)
		return
	}
	table, column, object := "entity_comments", "object_id", "defect"
	if resource == "test-cases" {
		object = "test_case"
	}
	if resource == "requirements" {
		table = "comments"
		column = "requirement_id"
	}
	where := " WHERE c.tenant_id=? AND c.project_id=? AND c." + column + "=?"
	args := []any{tenantID, a.pid(), id}
	join := " LEFT JOIN " + table + " p ON p.id=c.reply_to_id AND p.tenant_id=c.tenant_id AND p.project_id=c.project_id AND p." + column + "=c." + column
	if table == "entity_comments" {
		where += " AND c.object_type=?"
		args = append(args, object)
		join += " AND p.object_type=c.object_type"
	}
	var total int
	if err = a.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM "+table+" c"+where, args...).Scan(&total); err != nil {
		integrationReadError(w, err)
		return
	}
	extra := ""
	if resource == "requirements" {
		extra = ",c.content_doc_json"
	}
	rows, err := a.db.QueryContext(r.Context(), "SELECT c.id,c.author,c.author_user_id,c.body,c.mention_user_ids_json,c.created_at,c.reply_to_id,COALESCE(p.author_user_id,''),COALESCE(p.author,'')"+extra+" FROM "+table+" c"+join+where+" ORDER BY c.id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		integrationReadError(w, err)
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var c qualityComment
		var raw string
		var doc sql.NullString
		cols := []any{&c.ID, &c.Author, &c.AuthorUserID, &c.Body, &raw, &c.CreatedAt, &c.ReplyToID, &c.ReplyToAuthorUserID, &c.ReplyToAuthor}
		if resource == "requirements" {
			cols = append(cols, &doc)
		}
		if err = rows.Scan(cols...); err == nil {
			err = json.Unmarshal([]byte(raw), &c.MentionUserIDs)
		}
		if err != nil {
			integrationReadError(w, err)
			return
		}
		if c.MentionUserIDs == nil {
			c.MentionUserIDs = []string{}
		}
		if resource == "requirements" {
			content, err := readRichDocument(doc)
			if err != nil {
				integrationReadError(w, err)
				return
			}
			items = append(items, Comment{commentReply: c.commentReply, ID: c.ID, RequirementID: id, Author: c.Author, AuthorUserID: c.AuthorUserID, Body: c.Body, ContentDoc: content, CreatedAt: c.CreatedAt, MentionUserIDs: c.MentionUserIDs})
		} else {
			items = append(items, c)
		}
	}
	if err = rows.Err(); err != nil {
		integrationReadError(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size})
}
