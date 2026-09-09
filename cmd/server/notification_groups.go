package main

// 分类只表达消息用途，不把“已读”解释为业务已完成。
// SQL 同时用于筛选和未读分组统计，避免前端仅分类当前页而漏掉旧消息。
const notificationGroupSQL = `CASE
 WHEN n.event_type LIKE '%.mentioned' OR n.event_type LIKE '%_mentioned' OR n.event_type LIKE '%.replied' THEN 'mentions'
 WHEN n.event_type LIKE '%.assigned' OR n.event_type LIKE '%_assigned' OR n.event_type LIKE '%_changed' AND (n.event_type LIKE '%assignee%' OR n.event_type LIKE '%owner%' OR n.event_type LIKE '%verifier%' OR n.event_type LIKE '%executor%') OR n.event_type IN ('requirement.backend_completed','requirement.frontend_completed','defect.verification','test.failed') THEN 'handoffs'
 WHEN n.event_type IN ('requirement.updated','requirement.status_changed','defect.status_changed','sprint.status_changed','sprint.completed','user.password_changed') THEN 'changes'
 ELSE 'activity' END`

func validNotificationGroup(group string) bool {
	return group == "" || group == "mentions" || group == "handoffs" || group == "changes" || group == "activity"
}
