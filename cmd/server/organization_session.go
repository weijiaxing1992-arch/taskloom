package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// 中间件认证与获取写锁之间可能发生注销、改密撤销或开启代访问。
// 管理写入必须在同一写事务中复核原会话，不能只依赖已复制的用户 ID 和角色。
func (a *App) requireAdministrationSession(ctx context.Context, store stateStore) error {
	invalid := &organizationError{http.StatusUnauthorized, "unauthorized", "登录已失效，请重新登录"}
	if a.sessionToken == "" {
		return invalid
	}
	var rawExpiry string
	var impersonating bool
	err := store.QueryRowContext(ctx, `SELECT s.expires_at,EXISTS(SELECT 1 FROM auth_impersonations i WHERE i.tenant_id=s.tenant_id AND i.session_hash=s.token_hash AND i.ended_at IS NULL)
FROM auth_sessions s WHERE s.tenant_id=? AND s.user_id=? AND s.token_hash=? AND s.revoked_at IS NULL`, tenantID, a.uid(), a.sessionToken).Scan(&rawExpiry, &impersonating)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid
	}
	if err != nil {
		return err
	}
	expires, err := time.Parse(time.RFC3339, rawExpiry)
	if err != nil || !time.Now().Before(expires) {
		return invalid
	}
	if impersonating {
		return &organizationError{http.StatusForbidden, "impersonation_restricted", "代访问期间不能修改账号安全或成员权限"}
	}
	return nil
}
