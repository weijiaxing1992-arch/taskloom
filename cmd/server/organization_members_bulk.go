package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// 按提交数量限制上限；批量 ID 去重后保留首次出现顺序，便于核对结果。
func bulkMemberIDs(values []string) ([]string, error) {
	if len(values) > 200 {
		return nil, orgInvalid("选项数量超过限制")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range values {
		if id == "" || id != strings.TrimSpace(id) || !validOrgText(id, 1, 100) {
			return nil, orgInvalid("成员 ID 无效")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *App) organizationMembersBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		Action  string   `json:"action"`
		UserIDs []string `json:"userIds"`
		Webhook *struct {
			Enabled *bool   `json:"enabled"`
			URL     *string `json:"url"`
		} `json:"webhook"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failOrganization(w, err)
		return
	}
	ids, err := bulkMemberIDs(input.UserIDs)
	if err == nil && (len(ids) == 0 || !validChoice(input.Action, []string{"activate", "deactivate", "delete", "wecom-config"})) {
		err = orgInvalid("请选择成员及有效的批量操作")
	}
	if err == nil && ((input.Action == "wecom-config" && (input.Webhook == nil || input.Webhook.Enabled == nil && input.Webhook.URL == nil)) || (input.Action != "wecom-config" && input.Webhook != nil)) {
		err = orgInvalid("批量机器人配置参数无效")
	}
	if err != nil {
		failOrganization(w, err)
		return
	}
	tx, admin, err := a.beginOrganizationWrite(r, "members.manage")
	if err != nil {
		failOrganization(w, err)
		return
	}
	defer tx.Rollback()
	if input.Action == "delete" && !admin {
		failOrganization(w, orgForbidden())
		return
	}
	// 持有写锁时先校验全部目标；后续业务校验或审计失败均整批回滚。
	for _, id := range ids {
		members, e := organizationMemberList(r.Context(), tx, id)
		if e != nil {
			failOrganization(w, e)
			return
		}
		if len(members) != 1 {
			failOrganization(w, orgNotFound())
			return
		}
		member := members[0]
		if (input.Action == "deactivate" || input.Action == "delete") && (id == a.uid() || member.TenantRole == "tenant_admin") {
			failOrganization(w, &organizationError{409, "protected_member", "批量停用或删除不能包含当前账号或企业管理员"})
			return
		}
		if input.Action == "wecom-config" {
			err = a.checkWebhookAccess(r.Context(), tx, id)
		} else if !admin && (member.TenantRole == "tenant_admin" || len(member.GroupIDs) > 0) {
			err = orgForbidden()
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
	}
	for _, id := range ids {
		switch input.Action {
		case "activate", "deactivate":
			active, disabled := input.Action == "activate", false
			patch := organizationMemberPatch{Active: &active}
			if active {
				patch.OperationDisabled = &disabled
			}
			_, err = a.saveOrganizationMember(r.Context(), tx, admin, id, patch)
		case "delete":
			err = a.removeOrganizationMember(r.Context(), tx, id)
		case "wecom-config":
			err = a.saveUserWecomConfiguration(r.Context(), tx, id, input.Webhook.URL, input.Webhook.Enabled, false)
		}
		if err != nil {
			failOrganization(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		failOrganization(w, err)
		return
	}
	write(w, 200, map[string]any{"affected": len(ids), "userIds": ids})
}

// 单人和批量配置共用；调用方须持有组织写锁并完成鉴权，不外发消息或记录明文地址。
func (a *App) saveUserWecomConfiguration(ctx context.Context, tx *sql.Tx, user string, rawURL *string, newEnabled *bool, clear bool) error {
	var encrypted []byte
	var enabled bool
	var version int
	err := tx.QueryRowContext(ctx, `SELECT encrypted_url,enabled,version FROM user_wecom_webhooks WHERE tenant_id=? AND user_id=?`, tenantID, user).Scan(&encrypted, &enabled, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if clear {
		encrypted, enabled = []byte{}, false
	} else {
		if rawURL != nil {
			raw, err := validateWecomURL(*rawURL)
			if err != nil {
				return err
			}
			encrypted, err = encryptWebhook(a.wecomKey, tenantID, user, raw)
			if err != nil {
				return &organizationError{503, "webhook_key_unavailable", "机器人加密配置不可用，请联系管理员"}
			}
		}
		if newEnabled != nil {
			enabled = *newEnabled
		}
		if enabled && len(encrypted) == 0 {
			return orgInvalid("请先填写机器人地址")
		}
	}
	if encrypted == nil {
		encrypted = []byte{}
	}
	// 清空配置也递增版本，防止已领取的旧投递任务误用新机器人地址。
	_, err = tx.ExecContext(ctx, `INSERT INTO user_wecom_webhooks(tenant_id,user_id,encrypted_url,enabled,version,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,user_id)DO UPDATE SET encrypted_url=excluded.encrypted_url,enabled=excluded.enabled,version=excluded.version,updated_at=excluded.updated_at`, tenantID, user, encrypted, enabled, version+1, orgNow())
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE user_wecom_deliveries SET status='skipped',last_error='configuration_changed',updated_at=? WHERE tenant_id=? AND user_id=? AND status IN ('pending','retry')`, orgNow(), tenantID, user)
	}
	if err == nil {
		err = a.organizationAudit(ctx, tx, "user_wecom", user, "configure", nil, map[string]any{"configured": len(encrypted) > 0, "enabled": enabled})
	}
	return err
}
