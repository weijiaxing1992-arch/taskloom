package main

// This predicate refers only to the notification row (alias n), never the
// selected project header. Revoked projects must disappear from all inbox
// reads, unread badges and read-status mutations immediately.
// 按每条通知自身的项目重新鉴权，不能用当前页所选项目替代；退项目后列表、未读数、标记已读同步失效。
// 企业管理员可读自己的跨项目通知，但仍需有效账号，不能借此读取他人的收件箱。
const visibleNotificationSQL = ` AND EXISTS (
SELECT 1 FROM users nu JOIN tenant_memberships ntm ON ntm.tenant_id=nu.tenant_id AND ntm.user_id=nu.id
JOIN projects np ON np.tenant_id=nu.tenant_id AND np.id=n.project_id
WHERE nu.tenant_id=n.tenant_id AND nu.id=n.recipient_user_id AND nu.active=1 AND nu.operation_disabled=0 AND ntm.status='active' AND np.status='active'
AND (ntm.role='tenant_admin' OR EXISTS (SELECT 1 FROM project_members npm WHERE npm.tenant_id=nu.tenant_id AND npm.project_id=np.id AND npm.user_id=nu.id)))`
