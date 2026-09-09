package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// 需求多人绑定与正文/备注提及的兼容层：稳定 ID 数组为主，旧单人字段只保留首位兼容值。
// 新增绑定必须是当前项目有效成员；历史未变绑定允许保留，不能静默清掉离职/停用人员。
type RequirementAssignee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type requirementMentionState struct {
	Description map[string]string `json:"description"`
	Remarks     map[string]string `json:"remarks"`
}

type requirementPeopleNotice struct {
	Event      string
	Field      string
	Title      string
	Body       string
	Recipients []string
	// Assignment notices use the shared scoped writer so owner and role
	// assignments follow the same stable-ID/access policy as defects and tests.
	Assignment bool
}

type requirementPeopleValidationError string

func (e requirementPeopleValidationError) Error() string { return string(e) }

func failRequirementPeople(w http.ResponseWriter, err error) {
	var validation requirementPeopleValidationError
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "not_found", "需求不存在")
	} else if errors.As(err, &validation) {
		fail(w, http.StatusUnprocessableEntity, "invalid_requirement_people", err.Error())
	} else {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
	}
}

func (a *App) migrateRequirementPeople() error {
	for _, column := range []struct{ name, definition string }{
		{"text_mentions_json", `TEXT NOT NULL DEFAULT '{}'`},
		{"assignee_user_ids_json", `TEXT NOT NULL DEFAULT '[]'`},
		{"owner_user_ids_json", `TEXT NOT NULL DEFAULT '[]'`},
	} {
		var exists int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('requirements') WHERE name=?`, column.name).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			continue
		}
		if _, err := a.db.Exec(`ALTER TABLE requirements ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
			return err
		}
		// 只在首次加列时用既有稳定单 ID 回填数组；不改旧正文/姓名，也不补发历史通知。
		if column.name == "assignee_user_ids_json" {
			if _, err := a.db.Exec(`UPDATE requirements SET assignee_user_ids_json=json_array(assignee_user_id) WHERE assignee_user_id!=''`); err != nil {
				return err
			}
		}
		if column.name == "owner_user_ids_json" {
			if _, err := a.db.Exec(`UPDATE requirements SET owner_user_ids_json=json_array(owner_user_id) WHERE owner_user_id!=''`); err != nil {
				return err
			}
		}
	}
	return nil
}

func readRequirementMentionState(raw string, x *Requirement) error {
	var state requirementMentionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	x.DescriptionMentionNames = state.Description
	x.RemarksMentionNames = state.Remarks
	syncRequirementMentionIDs(x)
	return nil
}

func mentionIDs(names map[string]string) []string {
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func syncRequirementMentionIDs(x *Requirement) {
	if x.DescriptionMentionNames == nil {
		x.DescriptionMentionNames = map[string]string{}
	}
	if x.RemarksMentionNames == nil {
		x.RemarksMentionNames = map[string]string{}
	}
	x.DescriptionMentionUserIDs = mentionIDs(x.DescriptionMentionNames)
	x.RemarksMentionUserIDs = mentionIDs(x.RemarksMentionNames)
}

func requirementMentionJSON(x *Requirement) string {
	return jsonText(requirementMentionState{Description: x.DescriptionMentionNames, Remarks: x.RemarksMentionNames})
}

type requirementPeopleQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func (a *App) loadRequirementAssignees(query requirementPeopleQuery, x *Requirement) error {
	var err error
	x.AssigneeUserIDs, x.Assignees, err = a.requirementPeopleNames(query, x.AssigneeUserIDs, x.AssigneeUserID)
	if err != nil {
		return err
	}
	if len(x.Assignees) > 0 {
		x.AssigneeUserID, x.Assignee = x.Assignees[0].ID, x.Assignees[0].Name
	}
	x.OwnerUserIDs, x.Owners, err = a.requirementPeopleNames(query, x.OwnerUserIDs, x.OwnerUserID)
	if err != nil {
		return err
	}
	if len(x.Owners) > 0 {
		x.OwnerUserID, x.Owner = x.Owners[0].ID, x.Owners[0].Name
	}
	return nil
}

func (a *App) requirementPeopleNames(query requirementPeopleQuery, ids []string, primary string) ([]string, []RequirementAssignee, error) {
	// 按已保存 ID 顺序取姓名；历史停用成员也需可展示，缺姓名回退 ID 而非同名猜测。
	// 该函数只用于读取展示，不代表这些人员还能被新分配。
	if len(ids) == 0 && primary != "" {
		ids = []string{primary}
	}
	if ids == nil {
		ids = []string{}
	}
	people := []RequirementAssignee{}
	if len(ids) == 0 {
		return ids, people, nil
	}
	args := []any{tenantID}
	marks := make([]string, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := query.Query(`SELECT id,name FROM users WHERE tenant_id=? AND id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		name := names[id]
		if name == "" {
			name = id
		}
		people = append(people, RequirementAssignee{ID: id, Name: name})
	}
	return ids, people, nil
}

func (a *App) activeRequirementMemberNames(query requirementPeopleQuery) (map[string]string, error) {
	// 此成员集同时校验正文、备注和富文本提及；与通知可见性一致排除
	// operation_disabled，避免从任一编辑入口绕过已停用账号的投递边界。
	rows, err := query.Query(`SELECT u.id,u.name FROM users u JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE u.tenant_id=? AND pm.project_id=? AND u.active=1 AND u.operation_disabled=0 AND tm.status='active'`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

func hasPatch(patch map[string]json.RawMessage, key string) bool { _, ok := patch[key]; return ok }

// 必须在取得写事务后调用：重新解码最新人员/提及快照，防并发同内容保存重复通知，
// 也防事务外旧对象的 slice/map 浅拷贝污染“原来已绑定”的权限判断。
func (a *App) prepareRequirementPeople(tx *sql.Tx, x *Requirement, patch map[string]json.RawMessage, creating bool) ([]requirementPeopleNotice, error) {
	previous := Requirement{}
	if !creating {
		var mentions, assignees, owners, weights string
		if err := tx.QueryRow(`SELECT description,remarks,text_mentions_json,assignee_user_ids_json,assignee_user_id,assignee,owner_user_ids_json,owner_user_id,owner,role_weights_json FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), x.ID).Scan(&previous.Description, &previous.Remarks, &mentions, &assignees, &previous.AssigneeUserID, &previous.Assignee, &owners, &previous.OwnerUserID, &previous.Owner, &weights); err != nil {
			return nil, err
		}
		if err := readRequirementMentionState(mentions, &previous); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(assignees), &previous.AssigneeUserIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(owners), &previous.OwnerUserIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(weights), &previous.RoleWeights); err != nil {
			return nil, err
		}
		normalizeRequirementWeights(&previous)
		if err := a.loadRequirementAssignees(tx, &previous); err != nil {
			return nil, err
		}
		preserveLegacyRequirementWeightMembers(x, &previous, patch)
	}
	active, err := a.activeRequirementMemberNames(tx)
	if err != nil {
		return nil, err
	}
	// 事务中重查新增职能绑定：请求开始后已移出项目的成员不能靠旧目录被新加入。
	if creating || hasPatch(patch, "roleWeights") {
		for role, weight := range x.RoleWeights {
			old := map[string]bool{}
			for _, id := range previous.RoleWeights[role].UserIDs {
				old[id] = true
			}
			for _, id := range weight.UserIDs {
				if _, valid := active[id]; !valid && !old[id] {
					return nil, requirementPeopleValidationError("权重绑定人员不是当前项目的有效成员，请重新选择")
				}
			}
		}
	}
	notices := []requirementPeopleNotice{}
	for _, field := range []struct {
		key, label, event string
		text              *string
		ids               *[]string
		names             *map[string]string
		previousText      string
		previousNames     map[string]string
	}{
		{"description", "你在需求正文中被提及", "requirement.description_mentioned", &x.Description, &x.DescriptionMentionUserIDs, &x.DescriptionMentionNames, previous.Description, previous.DescriptionMentionNames},
		{"remarks", "你在需求备注中被提及", "requirement.remarks_mentioned", &x.Remarks, &x.RemarksMentionUserIDs, &x.RemarksMentionNames, previous.Remarks, previous.RemarksMentionNames},
	} {
		idsProvided := creating || hasPatch(patch, field.key+"MentionUserIds")
		// 缺省字段、显式空数组与删除正文 token 含义不同；仅通知这次真正新增的 ID。
		textProvided := creating || hasPatch(patch, field.key)
		if !textProvided {
			*field.text = field.previousText
		}
		if !idsProvided && !textProvided {
			*field.names = field.previousNames
			continue
		}
		requested := *field.ids
		if !idsProvided {
			requested = mentionIDs(field.previousNames)
		}
		seen := map[string]bool{}
		retained := map[string]string{}
		added := []string{}
		for _, id := range requested {
			if seen[id] {
				continue
			}
			seen[id] = true
			if len(seen) > 50 {
				return nil, requirementPeopleValidationError("需求正文或备注每处最多提及 50 位成员")
			}
			oldName, wasMentioned := field.previousNames[id]
			if wasMentioned && oldName != "" && hasLegacyNameMention(*field.text, oldName) {
				retained[id] = oldName
				continue
			}
			currentName, validMember := active[id]
			if validMember && currentName != "" && hasLegacyNameMention(*field.text, currentName) {
				retained[id] = currentName
				if !wasMentioned {
					added = append(added, id)
				}
				continue
			}
			if !idsProvided {
				continue
			} // Legacy text edit removes only tokens actually deleted.
			if !validMember && !wasMentioned {
				return nil, requirementPeopleValidationError("提及成员不是当前项目的有效成员，请重新选择")
			}
			return nil, requirementPeopleValidationError("正文或备注中必须包含所选成员的完整 @姓名")
		}
		*field.names = retained
		if len(added) > 0 {
			sort.Strings(added)
			notices = append(notices, requirementPeopleNotice{Event: field.event, Field: field.key, Title: field.label, Body: *field.text, Recipients: added})
		}
	}
	syncRequirementMentionIDs(x)
	for _, field := range []struct {
		key                 string
		ids                 *[]string
		people              *[]RequirementAssignee
		primary, name       *string
		oldIDs              []string
		oldPeople           []RequirementAssignee
		oldPrimary, oldName string
	}{
		{"assignee", &x.AssigneeUserIDs, &x.Assignees, &x.AssigneeUserID, &x.Assignee, previous.AssigneeUserIDs, previous.Assignees, previous.AssigneeUserID, previous.Assignee},
		{"owner", &x.OwnerUserIDs, &x.Owners, &x.OwnerUserID, &x.Owner, previous.OwnerUserIDs, previous.Owners, previous.OwnerUserID, previous.Owner},
	} {
		assignmentProvided := creating || hasPatch(patch, field.key+"UserIds") || hasPatch(patch, field.key)
		if !assignmentProvided {
			*field.ids, *field.people, *field.primary, *field.name = field.oldIDs, field.oldPeople, field.oldPrimary, field.oldName
			continue
		}
		// 旧编辑器常在每次保存带上未修改的单人姓名；此时保留完整多人关系。
		// 只有真正改了单人值或显式提交数组才替换；旧字段明确空字符串仍表示清空。
		if !creating && !hasPatch(patch, field.key+"UserIds") && strings.TrimSpace(*field.name) != "" && strings.TrimSpace(*field.name) == field.oldName {
			*field.ids, *field.people, *field.primary, *field.name = field.oldIDs, field.oldPeople, field.oldPrimary, field.oldName
			continue
		}
		requested := *field.ids
		if (creating && requested == nil) || (!creating && !hasPatch(patch, field.key+"UserIds") && hasPatch(patch, field.key)) {
			requested = []string{}
			if name := strings.TrimSpace(*field.name); name != "" {
				for id, currentName := range active {
					if name == currentName {
						requested = append(requested, id)
					}
				}
				if len(requested) != 1 {
					if field.key == "owner" {
						return nil, requirementPeopleValidationError("负责人必须是当前项目唯一有效成员，请改用成员选择器")
					}
					return nil, requirementPeopleValidationError("处理人必须是当前项目唯一有效成员，请改用成员选择器")
				}
			}
		}
		oldIDs := map[string]bool{}
		oldNames := map[string]string{}
		for _, person := range field.oldPeople {
			oldIDs[person.ID] = true
			oldNames[person.ID] = person.Name
		}
		seen := map[string]bool{}
		*field.ids = []string{}
		*field.people = []RequirementAssignee{}
		added := []string{}
		for _, id := range requested {
			if seen[id] {
				continue
			}
			seen[id] = true
			if len(seen) > 50 {
				if field.key == "owner" {
					return nil, requirementPeopleValidationError("一个需求最多选择 50 位负责人")
				}
				return nil, requirementPeopleValidationError("一个需求最多选择 50 位处理人")
			}
			name, valid := active[id]
			if !valid && !oldIDs[id] {
				if field.key == "owner" {
					return nil, requirementPeopleValidationError("负责人不是当前项目的有效成员，请重新选择")
				}
				return nil, requirementPeopleValidationError("处理人不是当前项目的有效成员，请重新选择")
			}
			if !valid {
				name = oldNames[id]
			}
			*field.ids = append(*field.ids, id)
			*field.people = append(*field.people, RequirementAssignee{ID: id, Name: name})
			if !oldIDs[id] {
				added = append(added, id)
			}
		}
		*field.primary, *field.name = "", ""
		if len(*field.people) > 0 {
			*field.primary, *field.name = (*field.people)[0].ID, (*field.people)[0].Name
		}
		if len(added) > 0 {
			switch field.key {
			case "assignee":
				notices = append(notices, requirementPeopleNotice{Event: "requirement.assigned", Field: "assigneeUserIds", Title: "有新的工作项分配给你", Body: x.Title, Recipients: added, Assignment: true})
			case "owner":
				notices = append(notices, requirementPeopleNotice{Event: "requirement.owner_assigned", Field: "ownerUserIds", Title: "你被指定为需求负责人", Body: x.Title, Recipients: added, Assignment: true})
			}
		}
	}
	if creating || hasPatch(patch, "roleWeights") {
		for _, role := range requirementWeightRoles {
			old := map[string]bool{}
			for _, id := range previous.RoleWeights[role].UserIDs {
				old[id] = true
			}
			added := []string{}
			for _, id := range x.RoleWeights[role].UserIDs {
				if id != "" && !old[id] {
					added = append(added, id)
				}
			}
			if len(added) == 0 {
				continue
			}
			sort.Strings(added)
			notices = append(notices, requirementPeopleNotice{
				Event:      "requirement.role_assigned",
				Field:      "roleWeights." + role + ".userIds",
				Title:      "你被分配为" + requirementRoleLabel(role),
				Body:       x.Title,
				Recipients: added,
				Assignment: true,
			})
		}
	}
	return notices, nil
}

func requirementRoleLabel(role string) string {
	return map[string]string{
		"frontend":  "前端工程师",
		"backend":   "后端工程师",
		"algorithm": "算法工程师",
		"ui":        "UI 工程师",
		"product":   "产品经理",
	}[role]
}

func (a *App) writeRequirementPeopleNotices(tx *sql.Tx, x *Requirement, notices []requirementPeopleNotice, now string) error {
	// 只写事务内站内记录与 outbox，不进行企业微信 HTTP 请求。
	// 随机事件键区分不同真实变更，同事件每人一条；是否需要发由前置差异比较决定。
	for _, notice := range notices {
		if notice.Assignment {
			if err := a.writeAssignmentNotices(context.Background(), tx, []assignmentNotice{{
				Event:      notice.Event,
				Subject:    "requirement",
				SubjectID:  x.ID,
				Field:      notice.Field,
				Title:      notice.Title,
				Body:       notice.Body,
				Recipients: notice.Recipients,
			}}, now); err != nil {
				return err
			}
			continue
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		key := fmt.Sprintf("requirement-people:%s:%d:%s:%s", a.pid(), x.ID, notice.Event, hex.EncodeToString(nonce[:]))
		for _, recipient := range notice.Recipients {
			// 正文/备注明确自提及保留自己的站内副本，不套用评论回复的排除自己规则。
			if _, err := tx.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,'requirement',?,?,?,?,?)`, tenantID, a.pid(), recipient, a.uid(), notice.Event, x.ID, notice.Title, notice.Body, now, key+":"+recipient); err != nil {
				return err
			}
		}
		payload := jsonText(map[string]any{"subjectType": "requirement", "subjectId": x.ID, "sourceField": notice.Field, "title": notice.Title, "body": notice.Body, "mentionUserIds": notice.Recipients, "actorUserId": a.uid()})
		if _, err := tx.Exec(`INSERT INTO notification_outbox(tenant_id,project_id,event_type,payload,dedupe_key,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), notice.Event, payload, key, now, now); err != nil {
			return err
		}
	}
	return nil
}
