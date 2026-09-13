package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
)

const operationDisabledMessage = "账号业务操作已禁用，请联系企业管理员"

// readOnlyImpersonationRequestKey is set only by scopedAPI after it has
// authenticated the administrator, resolved a current read-only delegation,
// and admitted the request through the narrow read-only route policy. It lets
// downstream read helpers avoid treating the target's first-password marker as
// a blocking login screen. It must never be synthesized by a handler.
type readOnlyImpersonationRequestKey struct{}

func isReadOnlyImpersonationRequest(ctx context.Context) bool {
	allowed, _ := ctx.Value(readOnlyImpersonationRequestKey{}).(bool)
	return allowed
}

// Business suspension is distinct from activation/security deactivation. A
// suspended, activated user may authenticate, but has no business API access.
// operation_disabled 是“可登录但禁止业务操作”；active=0 / 企业成员未激活仍不得登录，二者不能混用。
func (a *App) migrateOperationAccess() error {
	var exists int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='operation_disabled'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		_, err := a.db.Exec(`ALTER TABLE users ADD COLUMN operation_disabled INTEGER NOT NULL DEFAULT 0 CHECK(operation_disabled IN (0,1))`)
		return err
	}
	return nil
}

func operationDisabled(ctx context.Context, store stateStore, user string) (bool, error) {
	var disabled bool
	err := store.QueryRowContext(ctx, `SELECT operation_disabled FROM users WHERE tenant_id=? AND id=?`, tenantID, user).Scan(&disabled)
	return disabled, err
}

func (a *App) requireOperationAccess(ctx context.Context, store stateStore) error {
	disabled, err := operationDisabled(ctx, store, a.uid())
	if err != nil {
		return err
	}
	if disabled {
		return &organizationError{403, "account_disabled", operationDisabledMessage}
	}
	// The router only attaches this marker after rejecting every write except
	// the membership-checked project visit used to open a notification in a
	// different project. Keeping the first-password flag intact while allowing
	// these safe reads makes an explicit enterprise review usable without
	// granting a way to set credentials or mutate target data.
	if isReadOnlyImpersonationRequest(ctx) {
		return nil
	}
	return a.requirePasswordChanged(ctx, store)
}

// Run before every authenticated endpoint (including administration and
// personal preferences), then again after resolving an impersonated identity.
// This deliberately does not clear or revoke the authenticated session.
// 中央入口在认证后及代访问身份解析后检查；禁用账号仅返回最小 session 供前端显示阻断页。
// 不清除其 Cookie，管理员重新启用后可恢复；logout 在外层认证路由单独处理。
func (a *App) allowOperationRequest(w http.ResponseWriter, r *http.Request, user string) bool {
	disabled, err := operationDisabled(r.Context(), a.db, user)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 401, "unauthorized", "账号不可用")
		} else {
			fail(w, 503, "database_unavailable", "账号权限服务暂时繁忙，请稍后重试")
		}
		return false
	}
	if !disabled {
		return true
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/session" {
		a.operationDisabledSession(w, r, user)
	} else {
		fail(w, 403, "account_disabled", operationDisabledMessage)
	}
	return false
}

func (a *App) operationDisabledSession(w http.ResponseWriter, r *http.Request, user string) {
	var name, role, color, locale, timezone, tenantName string
	var mustChange bool
	err := a.db.QueryRowContext(r.Context(), `SELECT u.name,tm.role,u.avatar_color,u.locale,u.timezone,t.name,u.must_change_password FROM users u JOIN tenants t ON t.id=u.tenant_id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND tm.status='active'`, tenantID, user).Scan(&name, &role, &color, &locale, &timezone, &tenantName, &mustChange)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号权限服务暂时繁忙，请稍后重试")
		return
	}
	setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), locale))
	write(w, 200, map[string]any{
		"tenant":           map[string]string{"id": tenantID, "name": tenantName},
		"project":          map[string]string{"id": "", "name": "", "code": ""},
		"user":             map[string]any{"id": user, "name": name, "role": role, "avatarColor": color, "locale": storedLocale(locale), "timezone": timezone, "operationDisabled": true, "mustChangePassword": mustChange},
		"supportedLocales": supportedLocales, "impersonation": nil, "canImpersonate": false, "organizationPermissions": []string{},
	})
}
