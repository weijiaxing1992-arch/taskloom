package main

import (
	"context"
	"database/sql"
)

// 按稳定状态 key 定义前后端交接，不匹配翻译文本或自定义显示名称。
// 只给这条需求绑定的对口工程师发消息；未绑定时不广播整个部门，不猜测同名成员。
func (a *App) writeRequirementHandoffNotice(ctx context.Context, tx *sql.Tx, x *Requirement, from, now string) error {
	if from == x.Status {
		return nil
	}
	var role, event, title, instruction string
	switch x.Status {
	case "后端已完成":
		role, event = "frontend", "requirement.backend_completed"
		title, instruction = "后端已完成，待前端协作", "该需求后端已完成，请对口前端工程师确认联调与后续开发。"
	case "前端已完成":
		role, event = "backend", "requirement.frontend_completed"
		title, instruction = "前端已完成，待后端协作", "该需求前端已完成，请对口后端工程师确认联调与后续开发。"
	default:
		return nil
	}
	// 使用事务中已保存的新人员绑定（包括本次同时调整的工程师）；旧单 ID 保留兼容。
	weight := x.RoleWeights[role]
	ids := requirementPeopleIDs(weight.UserIDs, weight.UserID)
	if len(ids) == 0 {
		return nil
	}
	// 沿用通知的当前租户/项目权限过滤及去重规则。离职、停用或移出项目的人不新收通知。
	people, err := a.requirementChangeRecipients(ctx, tx, &Requirement{AssigneeUserIDs: ids})
	if err != nil || len(people) == 0 {
		return err
	}
	return a.writeRequirementPeopleNotices(tx, x, []requirementPeopleNotice{{
		Event: event, Field: "role." + role + ".userIds", Title: title,
		Body: instruction + "\n" + x.Code + " " + x.Title, Recipients: people,
	}}, now)
}
