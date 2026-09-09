package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 只走隔离测试库中的真实需求 PATCH，不启动企业微信 worker 或调用外部网络。
func handoffRequirement(t *testing.T, a *App, body map[string]any) Requirement {
	t.Helper()
	x := createPeopleRequirement(t, a, body)
	return patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "开发中"})
}

func handoffEventCount(t *testing.T, a *App, id int64, event string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND subject_type='requirement' AND subject_id=? AND event_type=?`, tenantID, projectID, id, event).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRequirementHandoffOppositeEngineersAndCompleteNotification(t *testing.T) {
	for _, direction := range []struct {
		status, event, role, recipient, second, opposite, title, english string
	}{
		{"后端已完成", "requirement.backend_completed", "frontend", "u_front", "u_front_lead", "u_back", "Backend complete — frontend handoff", "Backend work is complete. Assigned frontend engineers should coordinate integration and next steps."},
		{"前端已完成", "requirement.frontend_completed", "backend", "u_back", "u_back_lead", "u_front", "Frontend complete — backend handoff", "Frontend work is complete. Assigned backend engineers should coordinate integration and next steps."},
	} {
		t.Run(direction.status, func(t *testing.T) {
			a := wecomApp(t)
			orgRequest(t, a, "PATCH", "/api/profile/wecom-webhook", direction.recipient, map[string]any{"url": testWebhookURL, "enabled": true}, 200)
			x := handoffRequirement(t, a, map[string]any{
				"title": "交接 mixed 原标题", "description": "原始业务正文不能当作系统文案翻译", "assigneeUserIds": []string{direction.recipient, "u_pm"},
				"roleWeights": map[string]any{direction.role: map[string]any{"userIds": []string{direction.recipient, direction.second, direction.recipient}, "value": 12.5}},
			})
			patchPeopleRequirement(t, a, x.ID, map[string]any{"status": direction.status})
			if n := handoffEventCount(t, a, x.ID, direction.event); n != 2 {
				t.Fatalf("one handoff per unique bound engineer required, got %d", n)
			}
			for _, uid := range []string{direction.recipient, direction.second} {
				if peopleNoticeCount(t, a, x.ID, direction.event, uid) != 1 {
					t.Fatalf("missing engineer %s", uid)
				}
			}
			for _, uid := range []string{"u_admin", "u_pm", direction.opposite, "u_qa"} {
				if peopleNoticeCount(t, a, x.ID, direction.event, uid) != 0 {
					t.Fatalf("handoff broadcast outside opposite-role bindings: %s", uid)
				}
			}
			var notificationID int64
			var body, created, queueStatus string
			if err := a.db.QueryRow(`SELECT id,body,created_at FROM user_notifications WHERE tenant_id=? AND project_id=? AND subject_id=? AND event_type=? AND recipient_user_id=?`, tenantID, projectID, x.ID, direction.event, direction.recipient).Scan(&notificationID, &body, &created); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, x.Code+" "+x.Title) {
				t.Fatalf("handoff omitted exact code/title: %q", body)
			}
			if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
				t.Fatalf("invalid notification time: %q", created)
			}
			if err := a.db.QueryRow(`SELECT status FROM user_wecom_deliveries WHERE notification_id=?`, notificationID).Scan(&queueStatus); err != nil || queueStatus != "pending" {
				t.Fatalf("personal robot notification not atomically queued: %s %v", queueStatus, err)
			}
			var payload string
			if err := a.db.QueryRow(`SELECT payload FROM notification_outbox WHERE tenant_id=? AND project_id=? AND event_type=? AND json_extract(payload,'$.subjectId')=?`, tenantID, projectID, direction.event, x.ID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var outbox struct {
				IDs   []string `json:"mentionUserIds"`
				Field string   `json:"sourceField"`
			}
			if json.Unmarshal([]byte(payload), &outbox) != nil || !reflect.DeepEqual(outbox.IDs, []string{direction.recipient, direction.second}) || outbox.Field != "role."+direction.role+".userIds" {
				t.Fatalf("invalid handoff routing payload: %s", payload)
			}
			w := languageRequest(t, a, "GET", "/api/notifications?eventType="+direction.event, direction.recipient, projectID, "en-US", "")
			if w.Code != 200 {
				t.Fatalf("inbox: %d %s", w.Code, w.Body.String())
			}
			for _, text := range []string{direction.title, direction.english, x.Code, x.Title, created, fmt.Sprintf("/requirements?req=%d", x.ID)} {
				if !strings.Contains(w.Body.String(), text) {
					t.Fatalf("English inbox missing %q: %s", text, w.Body.String())
				}
			}
			message := wecomMessage("交接通知", body, created, "requirement", x.ID, projectID, "https://devflow.example.test")
			if !strings.Contains(message, x.Code+" "+x.Title) || !strings.Contains(message, "UTC+08:00") || !strings.Contains(message, fmt.Sprintf("https://devflow.example.test/requirements?req=%d&project=%s", x.ID, projectID)) {
				t.Fatalf("robot formatter lost code/time/project deep link: %s", message)
			}
		})
	}
}

func TestRequirementHandoffNoAssigneeLegacyAndTransitionOnly(t *testing.T) {
	a := testApp(t)
	x := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front", "value": 0}}})
	// 模拟持久化的旧单 ID 数据，确保不是创建时已转换为 userIds 才偶然通过。
	if _, err := a.db.Exec(`UPDATE requirements SET role_weights_json=? WHERE tenant_id=? AND project_id=? AND id=?`, `{"frontend":{"userId":"u_front","value":0}}`, tenantID, projectID, x.ID); err != nil {
		t.Fatal(err)
	}
	got := patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "后端已完成"})
	if len(got.AssigneeUserIDs) != 0 || peopleNoticeCount(t, a, x.ID, "requirement.backend_completed", "u_front") != 1 {
		t.Fatal("handoff incorrectly depends on an assignee or a positive numeric weight")
	}
	for _, body := range []map[string]any{{"status": "后端已完成"}, {"remarks": "完成后补充备注"}, {"title": "完成后改标题", "status": "后端已完成"}} {
		patchPeopleRequirement(t, a, x.ID, body)
	}
	if handoffEventCount(t, a, x.ID, "requirement.backend_completed") != 1 {
		t.Fatal("same status/no-op/other edits sent duplicate handoff")
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "开发中"})
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "后端已完成"})
	if handoffEventCount(t, a, x.ID, "requirement.backend_completed") != 2 {
		t.Fatal("a distinct re-entry did not send a new handoff")
	}
}

func TestRequirementHandoffUsesNewBindingsAndDoesNotGuessMissingPeople(t *testing.T) {
	a := testApp(t)
	x := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front"}}}})
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "后端已完成", "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front_lead"}}}})
	if peopleNoticeCount(t, a, x.ID, "requirement.backend_completed", "u_front") != 0 || peopleNoticeCount(t, a, x.ID, "requirement.backend_completed", "u_front_lead") != 1 {
		t.Fatal("simultaneous engineer change notified stale bindings")
	}
	for _, status := range []string{"后端已完成", "前端已完成"} {
		y := handoffRequirement(t, a, map[string]any{"assigneeUserIds": []string{"u_front", "u_back"}})
		patchPeopleRequirement(t, a, y.ID, map[string]any{"status": status})
		if handoffEventCount(t, a, y.ID, "requirement.backend_completed")+handoffEventCount(t, a, y.ID, "requirement.frontend_completed") != 0 {
			t.Fatalf("%s guessed missing engineers from assignees", status)
		}
	}
	for _, status := range []string{"后端完成 | 前端开发中", "前端完成 | 后端开发中", "开发完成"} {
		y := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front"}, "backend": map[string]any{"userId": "u_back"}}})
		patchPeopleRequirement(t, a, y.ID, map[string]any{"status": status})
		if handoffEventCount(t, a, y.ID, "requirement.backend_completed")+handoffEventCount(t, a, y.ID, "requirement.frontend_completed") != 0 {
			t.Fatalf("%s matched an inexact status despite real engineer bindings", status)
		}
	}
	// 用户修改显示名为相同文案也不能触发另一个稳定状态 key 的交接。
	if _, err := a.db.Exec(`UPDATE requirement_statuses SET name='后端实现验收完成' WHERE tenant_id=? AND project_id=? AND key='后端已完成'; UPDATE requirement_statuses SET name='后端已完成',system=0 WHERE tenant_id=? AND project_id=? AND key='规划中'`, tenantID, projectID, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "开发中"})
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "规划中"})
	if handoffEventCount(t, a, x.ID, "requirement.backend_completed") != 1 {
		t.Fatal("display label was treated as a status key")
	}
}

func TestRequirementHandoffFiltersRemovedInactiveAndForeignRecipients(t *testing.T) {
	a := testApp(t)
	x := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front", "u_back", "u_algo", "u_ui", "u_front_lead"}}}})
	for _, statement := range []string{
		`DELETE FROM project_members WHERE project_id='` + projectID + `' AND user_id='u_front'`,
		`UPDATE users SET active=0 WHERE id='u_back'`,
		`UPDATE tenant_memberships SET status='disabled' WHERE user_id='u_algo'`,
		`UPDATE users SET tenant_id='other-tenant' WHERE id='u_ui'`,
	} {
		if _, err := a.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"status": "后端已完成"})
	if handoffEventCount(t, a, x.ID, "requirement.backend_completed") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.backend_completed", "u_front_lead") != 1 {
		t.Fatal("recipient access filtering leaked or removed the remaining valid engineer")
	}
	for _, uid := range []string{"u_front", "u_back", "u_algo", "u_ui"} {
		if peopleNoticeCount(t, a, x.ID, "requirement.backend_completed", uid) != 0 {
			t.Fatalf("unauthorized handoff recipient %s", uid)
		}
	}
}

func TestRequirementHandoffNotificationFailuresRollBackStatusAndAllSideEffects(t *testing.T) {
	for _, target := range []string{"user_notifications", "notification_outbox", "audit_logs", "user_wecom_deliveries"} {
		t.Run(target, func(t *testing.T) {
			a := wecomApp(t)
			setTestWebhook(t, a)
			x := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front", "value": 8}}})
			before, err := a.get(x.ID)
			if err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, table := range []string{"user_notifications", "notification_outbox", "activities", "audit_logs", "user_wecom_deliveries"} {
				counts[table] = tableCount(t, a, table)
			}
			if _, err = a.db.Exec(`CREATE TRIGGER fail_handoff BEFORE INSERT ON ` + target + ` BEGIN SELECT RAISE(ABORT,'handoff failure'); END`); err != nil {
				t.Fatal(err)
			}
			w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"status":"后端已完成","remarks":"必须整体回滚"}`)
			if w.Code < 500 {
				t.Fatalf("injected failure succeeded: %d %s", w.Code, w.Body.String())
			}
			after, err := a.get(x.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed handoff partially persisted: %v", err)
			}
			for table, count := range counts {
				if got := tableCount(t, a, table); got != count {
					t.Fatalf("%s partially persisted: %d != %d", table, got, count)
				}
			}
		})
	}
}

func TestRequirementHandoffCannotBypassWorkflowOrProjectPermission(t *testing.T) {
	a := testApp(t)
	x := handoffRequirement(t, a, map[string]any{"roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front"}}})
	before, _ := a.get(x.ID)
	for _, request := range []struct{ user, project string }{{"u_viewer", projectID}, {"u_front", insightProjectID}, {"u_admin", insightProjectID}} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), request.user, request.project, `{"status":"后端已完成"}`)
		if w.Code != 403 && w.Code != 404 {
			t.Fatalf("unauthorized transition: %d %s", w.Code, w.Body.String())
		}
	}
	after, _ := a.get(x.ID)
	if !reflect.DeepEqual(before, after) || handoffEventCount(t, a, x.ID, "requirement.backend_completed") != 0 {
		t.Fatal("unauthorized transition changed status or sent handoff")
	}
}
