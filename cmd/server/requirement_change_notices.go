package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// 本模块负责“实际内容发生改变”后的处理人动态通知，与新增提及/分配通知区分。
// 前后快照必须来自同一写事务：旧浏览器可能总提交未改字段，JSON 对象顺序也不代表变化；
// 使用事务内最新值比较，不能只看 PATCH 携带了哪些 key 或请求开始前的旧快照。
func (a *App) requirementSnapshot(tx *sql.Tx, id int64) (Requirement, map[string]any, error) {
	var x Requirement
	if err := scanRequirement(tx.QueryRow(`SELECT `+requirementSelectColumns+` FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id), &x); err != nil {
		return x, nil, err
	}
	var err error
	x.CustomFields, err = a.customFieldsUsing(tx, "requirement", id)
	if err != nil {
		return x, nil, err
	}
	if len(x.AssigneeUserIDs) == 0 && x.AssigneeUserID != "" {
		x.AssigneeUserIDs = []string{x.AssigneeUserID}
	}
	if len(x.OwnerUserIDs) == 0 && x.OwnerUserID != "" {
		x.OwnerUserIDs = []string{x.OwnerUserID}
	}
	encoded, err := json.Marshal(x)
	if err != nil {
		return x, nil, err
	}
	var values map[string]any
	if err = json.Unmarshal(encoded, &values); err != nil {
		return x, nil, err
	}
	for _, key := range []string{"updatedAt", "createdAt", "statusName", "statusColor", "statusCategory", "statusSystem", "isEnd", "assignees", "owners", "weightTotal"} {
		// 排除时间戳、目录展示元数据及派生总权重，避免同内容保存或人员改名产生假变化。
		delete(values, key)
	}
	return x, values, nil
}

func requirementActualChanges(before, after map[string]any) []string {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		if !reflect.DeepEqual(before[key], after[key]) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func (a *App) requirementChangeRecipients(ctx context.Context, tx *sql.Tx, x *Requirement) ([]string, error) {
	// 内容变更需要同步给所有仍在当前项目可见范围内的协作人，而不只是
	// "处理人"：负责人和每个职能权重中的成员同样可能以自己的身份承担工作。
	// 将稳定 ID 交给统一的收件人校验器，既避免同一人重复通知，也不会把
	// 已停用、业务禁用或已退出项目的账号重新暴露到通知中心。
	candidates := append([]string{}, x.AssigneeUserIDs...)
	candidates = append(candidates, x.OwnerUserIDs...)
	for _, role := range requirementWeightRoles {
		candidates = append(candidates, x.RoleWeights[role].UserIDs...)
	}
	fieldPeople, err := a.requirementFieldParticipants(ctx, tx, x.ID)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, fieldPeople...)
	return a.activeAssignmentRecipients(ctx, tx, candidates)
}

func (a *App) writeRequirementChangeNotice(ctx context.Context, tx *sql.Tx, x *Requirement, from string, changed []string, now string) error {
	// 与需求保存共用事务：无真实变化不发通知，状态变化单独记录稳定 from/to 审计。
	// 这里只写站内通知和 outbox，不能在事务内直接请求企业微信。
	if len(changed) == 0 {
		return nil
	}
	event, header := "requirement.updated", "需求已更新："+strings.Join(changed, ", ")
	if from != x.Status {
		event = "requirement.status_changed"
		var name string
		var system bool
		if err := tx.QueryRowContext(ctx, `SELECT name,system FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND key=?`, tenantID, a.pid(), x.Status).Scan(&name, &system); err != nil {
			return err
		}
		header = "需求状态已变更为「" + name + "」"
		if system && name == x.Status {
			header = "需求状态已变更为 " + name
		}
		if err := a.auditRequirementState(ctx, tx, "requirement", "status_changed", x.ID, map[string]string{"from": from, "to": x.Status}); err != nil {
			return err
		}
		// 交接对象与处理人是两种绑定：即使处理人为空，也要通知对口前/后端工程师。
		// 仍处于同一事务内，通知或外发排队失败时整个状态变更回滚。
		if err := a.writeRequirementHandoffNotice(ctx, tx, x, from, now); err != nil {
			return err
		}
	}
	people, err := a.requirementChangeRecipients(ctx, tx, x)
	if err != nil {
		return err
	}
	if len(people) == 0 {
		return nil
	}
	body := header + "\n" + x.Code + " " + x.Title
	// 保留已保存的纯文本原文；机器人按字节分段，不在源头丢失通知内容。
	// 不附带富文档 base64、附件二进制或成员凭据。
	if strings.TrimSpace(x.Description) != "" {
		body += "\n" + x.Description
	}
	return a.writeRequirementPeopleNotices(tx, x, []requirementPeopleNotice{{Event: event, Field: strings.Join(changed, ","), Title: "工作项有新动态", Body: body, Recipients: people}}, now)
}
