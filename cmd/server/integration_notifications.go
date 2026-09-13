package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const integrationNotificationDefaultPageSize = 25
const integrationNotificationMaxPageSize = 100

// 对外通知只读接口复用站内通知中心的 recipient、项目和撤权过滤条件。
// 凭据绑定的用户和项目均由服务端认证得到，查询参数不能选择其他收件人或项目。
func integrationNotificationListQuery(values url.Values) (int, int, string, string, string, string, error) {
	page, size := 1, integrationNotificationDefaultPageSize
	for key, entries := range values {
		if !validChoice(key, []string{"page", "pageSize", "limit", "read", "eventType", "group", "q"}) || len(entries) != 1 || len(entries[0]) > 800 {
			return 0, 0, "", "", "", "", errors.New("通知只支持单值分页、已读状态、事件、分类或关键词筛选")
		}
	}
	if raw := values.Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000000 {
			return 0, 0, "", "", "", "", errors.New("页码须为 1–1000000")
		}
		page = value
	}
	if values.Get("pageSize") != "" && values.Get("limit") != "" {
		return 0, 0, "", "", "", "", errors.New("pageSize 与 limit 不能同时使用")
	}
	raw := values.Get("pageSize")
	if raw == "" {
		raw = values.Get("limit")
	}
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > integrationNotificationMaxPageSize {
			return 0, 0, "", "", "", "", errors.New("每页数量须为 1–100")
		}
		size = value
	}
	read := values.Get("read")
	if read != "" && read != "read" && read != "unread" {
		return 0, 0, "", "", "", "", errors.New("通知已读状态筛选无效")
	}
	group := values.Get("group")
	if !validNotificationGroup(group) {
		return 0, 0, "", "", "", "", errors.New("通知分类无效")
	}
	event := strings.TrimSpace(values.Get("eventType"))
	query := strings.TrimSpace(values.Get("q"))
	if utf8.RuneCountInString(event) > 200 || utf8.RuneCountInString(query) > 200 {
		return 0, 0, "", "", "", "", errors.New("事件或关键词最多 200 字")
	}
	return page, size, read, event, group, query, nil
}

func integrationNotificationLike(value string) string {
	literal := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(strings.ToLower(value))
	return "%" + literal + "%"
}

func integrationNotificationValue(language string, id, subjectID int64, projectID, projectName, actorUserID, actor, eventType, subjectType, title, body string, readAt sql.NullString, createdAt, group string) map[string]any {
	title, body = localizedNotification(language, eventType, title, body)
	return map[string]any{
		"id": id, "projectId": projectID, "projectName": projectName,
		"actorUserId": actorUserID, "actor": actor, "eventType": eventType,
		"group": group, "subjectType": subjectType, "subjectId": subjectID,
		"title": title, "body": body, "read": readAt.Valid, "readAt": readAt.String,
		"createdAt": createdAt, "url": notificationURL(subjectType, subjectID),
	}
}

func (a *App) integrationNotificationWhere(read, event, group, query string) (string, []any) {
	where := ` FROM user_notifications n
JOIN projects p ON p.tenant_id=n.tenant_id AND p.id=n.project_id
LEFT JOIN users actor ON actor.tenant_id=n.tenant_id AND actor.id=n.actor_user_id
WHERE n.tenant_id=? AND n.project_id=? AND n.recipient_user_id=?` + visibleNotificationSQL
	args := []any{tenantID, a.pid(), a.uid()}
	if read == "read" {
		where += " AND n.read_at IS NOT NULL"
	} else if read == "unread" {
		where += " AND n.read_at IS NULL"
	}
	if event != "" {
		where += " AND n.event_type=?"
		args = append(args, event)
	}
	if group != "" {
		where += " AND (" + notificationGroupSQL + ")=?"
		args = append(args, group)
	}
	if query != "" {
		where += ` AND (LOWER(n.title) LIKE ? ESCAPE '\' OR LOWER(n.body) LIKE ? ESCAPE '\' OR LOWER(p.name) LIKE ? ESCAPE '\' OR LOWER(COALESCE(actor.name,'')) LIKE ? ESCAPE '\' OR LOWER(n.event_type) LIKE ? ESCAPE '\')`
		like := integrationNotificationLike(query)
		args = append(args, like, like, like, like, like)
	}
	return where, args
}

func (a *App) integrationNotifications(w http.ResponseWriter, r *http.Request, route integrationRoute) {
	language := w.Header().Get("Content-Language")
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		integrationReadError(w, err)
		return
	}
	defer tx.Rollback()
	if route.id > 0 {
		if len(r.URL.Query()) != 0 {
			fail(w, 400, "invalid_query", "通知详情不接受查询参数")
			return
		}
		where, args := a.integrationNotificationWhere("", "", "", "")
		args = append(args, route.id)
		var id, subjectID int64
		var projectID, projectName, actorUserID, actor, eventType, subjectType, title, body, createdAt, group string
		var readAt sql.NullString
		err = tx.QueryRowContext(r.Context(), `SELECT n.id,n.subject_id,n.project_id,p.name,n.actor_user_id,COALESCE(actor.name,''),n.event_type,n.subject_type,n.title,n.body,n.read_at,n.created_at,`+notificationGroupSQL+where+` AND n.id=?`, args...).Scan(&id, &subjectID, &projectID, &projectName, &actorUserID, &actor, &eventType, &subjectType, &title, &body, &readAt, &createdAt, &group)
		if err != nil {
			integrationReadError(w, err)
			return
		}
		if err = tx.Commit(); err != nil {
			integrationReadError(w, err)
			return
		}
		write(w, 200, integrationNotificationValue(language, id, subjectID, projectID, projectName, actorUserID, actor, eventType, subjectType, title, body, readAt, createdAt, group))
		return
	}
	page, size, read, event, group, query, err := integrationNotificationListQuery(r.URL.Query())
	if err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	where, args := a.integrationNotificationWhere(read, event, group, query)
	var total int
	if err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*)"+where, args...).Scan(&total); err != nil {
		integrationReadError(w, err)
		return
	}
	listArgs := append(append([]any(nil), args...), size, (page-1)*size)
	rows, err := tx.QueryContext(r.Context(), `SELECT n.id,n.subject_id,n.project_id,p.name,n.actor_user_id,COALESCE(actor.name,''),n.event_type,n.subject_type,n.title,n.body,n.read_at,n.created_at,`+notificationGroupSQL+where+` ORDER BY n.created_at DESC,n.id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		integrationReadError(w, err)
		return
	}
	items := []map[string]any{}
	for rows.Next() {
		var id, subjectID int64
		var projectID, projectName, actorUserID, actor, eventType, subjectType, title, body, createdAt, itemGroup string
		var readAt sql.NullString
		if err = rows.Scan(&id, &subjectID, &projectID, &projectName, &actorUserID, &actor, &eventType, &subjectType, &title, &body, &readAt, &createdAt, &itemGroup); err != nil {
			break
		}
		items = append(items, integrationNotificationValue(language, id, subjectID, projectID, projectName, actorUserID, actor, eventType, subjectType, title, body, readAt, createdAt, itemGroup))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		integrationReadError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		integrationReadError(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size, "contentTrust": "untrusted_business_data"})
}
