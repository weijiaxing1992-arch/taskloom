package main

import (
	"fmt"
	"testing"
)

func TestQualityCommentsExplicitMentionsPersistAndNotifyAllTestingEntities(t *testing.T) {
	a := testApp(t)
	var name, self string
	a.db.QueryRow(`SELECT name FROM users WHERE id='u_front'`).Scan(&name)
	a.db.QueryRow(`SELECT name FROM users WHERE id='u_admin'`).Scan(&self)
	for _, entity := range []struct{ resource, kind, table string }{{"test-cases", "test_case", "test_cases"}, {"test-plans", "test_plan", "test_plans"}, {"test-executions", "test_execution", "test_executions"}, {"defects", "defect", "defects"}} {
		var id int64
		if err := a.db.QueryRow(`SELECT id FROM `+entity.table+` WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, projectID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("/api/%s/%d/comments", entity.resource, id)
		body := "请@" + name + " 帮忙确认。 @" + self + " 自提及"
		payload := jsonText(map[string]any{"body": body, "mentionUserIds": []string{"u_front", "u_front", "u_admin"}})
		for i := 0; i < 2; i++ {
			w := apiRequest(a, "POST", path, "u_admin", projectID, payload)
			if w.Code != 201 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
			c := jsonMap(t, w)
			if c["body"] != body || len(c["mentionUserIds"].([]any)) != 2 {
				t.Fatalf("incorrect comment %v", c)
			}
		}
		var count, selfCount int
		a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type=? AND subject_id=? AND recipient_user_id='u_front'`, entity.kind+".mentioned", id).Scan(&count)
		a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type=? AND subject_id=? AND recipient_user_id='u_admin'`, entity.kind+".mentioned", id).Scan(&selfCount)
		// 显式 @ 自己也是有效提醒：不能因为评论作者与收件人相同而丢弃。
		if count != 2 || selfCount != 2 {
			t.Fatalf("notices %s=%d self=%d", entity.kind, count, selfCount)
		}
		w := apiRequest(a, "GET", path, "u_front", projectID, "")
		if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 2 {
			t.Fatalf("read comments %d %s", w.Code, w.Body.String())
		}
		w = apiRequest(a, "GET", "/api/notifications?eventType="+entity.kind+".mentioned", "u_front", projectID, "")
		if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 2 || jsonMap(t, w)["items"].([]any)[0].(map[string]any)["url"] != notificationURL(entity.kind, id) {
			t.Fatalf("recipient cannot reach object %s", w.Body.String())
		}
	}
}
func TestQualityCommentValidationRejectsSpoofedOutOfScopeAndViewerWrites(t *testing.T) {
	a := testApp(t)
	path := "/api/test-cases/1/comments"
	var name string
	a.db.QueryRow(`SELECT name FROM users WHERE id='u_front'`).Scan(&name)
	for _, body := range []map[string]any{{"body": "没有真实提及", "mentionUserIds": []string{"u_front"}}, {"body": "@" + name + "辰 不是此人", "mentionUserIds": []string{"u_front"}}, {"body": "email@" + name + " 不是提及", "mentionUserIds": []string{"u_front"}}, {"body": "@不存在", "mentionUserIds": []string{"missing"}}} {
		w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(body))
		if w.Code != 422 {
			t.Fatalf("spoof accepted: %d %s", w.Code, w.Body.String())
		}
	}
	for _, pid := range []string{insightProjectID, "missing"} {
		w := apiRequest(a, "POST", path, "u_admin", pid, jsonText(map[string]any{"body": "@" + name + " 跨项目", "mentionUserIds": []string{"u_front"}}))
		if w.Code != 403 && w.Code != 404 {
			t.Fatalf("cross-project %d %s", w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "POST", path, "u_viewer", projectID, `{"body":"viewer"}`)
	if w.Code != 403 {
		t.Fatalf("viewer posted: %d", w.Code)
	}
	w = apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"body": "@" + name + " 仅自由文字"}))
	if w.Code != 201 || len(jsonMap(t, w)["mentionUserIds"].([]any)) != 0 {
		t.Fatalf("inferred recipient: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"body": "@" + name + " 已停用", "mentionUserIds": []string{"u_front"}}))
	if w.Code != 422 {
		t.Fatalf("disabled mention: %d", w.Code)
	}
}

// 业务停用的成员虽然仍保留历史身份，但不能再成为新评论提及的收件人；否则
// 通知中心会隐藏该行而企业微信投递仍可能已经发生。
func TestQualityCommentRejectsOperationDisabledMentionRecipient(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/defects/1/comments", "u_admin", projectID, jsonText(map[string]any{
		"body":           "@" + name + " 不应投递给已停用账号",
		"mentionUserIds": []string{"u_front"},
	}))
	if w.Code != 422 {
		t.Fatalf("operation-disabled mention accepted: %d %s", w.Code, w.Body.String())
	}
	if count := assignmentNoticeCount(t, a, "defect.mentioned", "defect", 1, "u_front"); count != 0 {
		t.Fatalf("operation-disabled recipient received inbox row=%d", count)
	}
}

func TestQualityCommentNotificationFailureRollsBackAllRows(t *testing.T) {
	a := testApp(t)
	tables := []string{"entity_comments", "entity_activities", "audit_logs", "user_notifications", "notification_outbox"}
	before := map[string]int{}
	for _, table := range tables {
		before[table] = tableCount(t, a, table)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER reject_quality_notice BEFORE INSERT ON user_notifications WHEN NEW.event_type='test_case.mentioned' BEGIN SELECT RAISE(ABORT,'test failure');END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/test-cases/1/comments", "u_admin", projectID, `{"body":"@沈星 不可部分保存","mentionUserIds":["u_front"]}`)
	if w.Code != 503 {
		t.Fatalf("expected rollback %d %s", w.Code, w.Body.String())
	}
	for _, table := range tables {
		if count := tableCount(t, a, table); count != before[table] {
			t.Fatalf("%s changed: %d -> %d", table, before[table], count)
		}
	}
	if _, err := a.db.Exec(`DROP TABLE entity_comments`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/test-cases/1/comments", "u_admin", projectID, "")
	if w.Code != 503 {
		t.Fatalf("query error not safe: %d %s", w.Code, w.Body.String())
	}
}
func TestRequirementLinksAreBidirectionalIdempotentAndScoped(t *testing.T) {
	a := testApp(t)
	first := planningRequirement(t, a, `{"title":"关联A"}`)
	second := planningRequirement(t, a, `{"title":"关联B"}`)
	path := fmt.Sprintf("/api/requirements/%d/links", first.ID)
	for i, status := range []int{201, 200} {
		w := apiRequest(a, "POST", path, "u_admin", projectID, fmt.Sprintf(`{"requirementId":%d}`, second.ID))
		if w.Code != status {
			t.Fatalf("link %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	reverse := fmt.Sprintf("/api/requirements/%d/links", second.ID)
	w := apiRequest(a, "GET", reverse, "u_member", projectID, "")
	if w.Code != 200 {
		t.Fatalf("reverse: %s", w.Body.String())
	}
	items := jsonMap(t, w)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != float64(first.ID) || items[0].(map[string]any)["statusName"] == nil {
		t.Fatalf("reverse missing metadata %v", items)
	}
	w = apiRequest(a, "POST", path, "u_admin", projectID, fmt.Sprintf(`{"requirementId":%d}`, first.ID))
	if w.Code != 422 {
		t.Fatalf("self linked: %d", w.Code)
	}
	b := *a
	b.project = insightProjectID
	foreign := planningRequirement(t, &b, `{"title":"他项目"}`)
	w = apiRequest(a, "POST", path, "u_admin", projectID, fmt.Sprintf(`{"requirementId":%d}`, foreign.ID))
	if w.Code != 404 {
		t.Fatalf("foreign linked %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "POST", path, "u_viewer", projectID, fmt.Sprintf(`{"requirementId":%d}`, second.ID))
	if w.Code != 403 {
		t.Fatalf("viewer linked %d", w.Code)
	}
	w = apiRequest(a, "DELETE", fmt.Sprintf("%s/%d", reverse, first.ID), "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatalf("unlink %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	if len(jsonMap(t, w)["items"].([]any)) != 0 {
		t.Fatal("one-sided unlink")
	}
	updated, _ := a.get(second.ID)
	if updated.ParentID != nil {
		t.Fatal("related link mutated parent relationship")
	}
}
func TestRequirementLinkAuditFailureRollsBack(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`CREATE TRIGGER reject_link_audit BEFORE INSERT ON audit_logs WHEN NEW.action='requirement_linked' BEGIN SELECT RAISE(ABORT,'fail');END`); err != nil {
		t.Fatal(err)
	}
	before := tableCount(t, a, "work_item_relations")
	w := apiRequest(a, "POST", "/api/requirements/1/links", "u_admin", projectID, `{"requirementId":2}`)
	if w.Code != 503 || tableCount(t, a, "work_item_relations") != before {
		t.Fatalf("partial link %d %s", w.Code, w.Body.String())
	}
}
