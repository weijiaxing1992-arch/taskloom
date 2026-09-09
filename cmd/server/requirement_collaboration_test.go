package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func createPlanningCategory(t *testing.T, a *App, name string) RequirementCategory {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/requirement-categories", "u_admin", a.pid(), jsonText(map[string]string{"name": name}))
	if w.Code != 201 {
		t.Fatalf("category create: %d %s", w.Code, w.Body.String())
	}
	var item RequirementCategory
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestRequirementCategoryCRUDKeepsRequirementsAndCounts(t *testing.T) {
	a := testApp(t)
	category := createPlanningCategory(t, a, " 发布平台/验收 ")
	if category.Name != "发布平台/验收" {
		t.Fatal("category name was not trimmed")
	}
	x := planningRequirement(t, a, `{"title":"分类流转需求","category":"发布平台/验收","remarks":"保留内容"}`)
	w := apiRequest(a, http.MethodGet, "/api/requirement-categories", "u_admin", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["canManage"] != true {
		t.Fatalf("category list failed: %d %s", w.Code, w.Body.String())
	}
	items, err := a.listRequirementCategories()
	if err != nil {
		t.Fatal(err)
	}
	var reservedID int64
	for _, item := range items {
		if item.ID == category.ID && item.Count != 1 {
			t.Fatalf("category count: %+v", item)
		}
		if item.Name == "未分类" {
			reservedID = item.ID
		}
	}
	if reservedID == 0 {
		t.Fatal("reserved category missing")
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirement-categories/%d", category.ID), "u_admin", projectID, `{"name":"发布平台/已验收"}`)
	if w.Code != 200 || jsonMap(t, w)["count"].(float64) != 1 {
		t.Fatalf("category rename failed: %d %s", w.Code, w.Body.String())
	}
	stored, _ := a.get(x.ID)
	if stored.Category != "发布平台/已验收" || stored.CreatedAt != x.CreatedAt || stored.Remarks != x.Remarks {
		t.Fatalf("rename lost content or relation: %+v", stored)
	}
	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/requirement-categories/%d", category.ID), "u_admin", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["movedRequirements"].(float64) != 1 {
		t.Fatalf("category deletion failed: %d %s", w.Code, w.Body.String())
	}
	stored, err = a.get(x.ID)
	if err != nil || stored.Category != "未分类" || stored.Title != x.Title || stored.CreatedAt != x.CreatedAt {
		t.Fatalf("category deletion deleted or changed requirement: %+v %v", stored, err)
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		w = apiRequest(a, method, fmt.Sprintf("/api/requirement-categories/%d", reservedID), "u_admin", projectID, `{"name":"不可改"}`)
		if w.Code != 409 {
			t.Fatalf("reserved category mutated: %d %s", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, http.MethodGet, "/api/meta", "u_admin", projectID, "")
	if strings.Contains(w.Body.String(), "发布平台/已验收") || !strings.Contains(w.Body.String(), "待定") || !strings.Contains(w.Body.String(), "客户端") {
		t.Fatalf("meta categories stale: %s", w.Body.String())
	}
}

func TestRequirementCategoriesScopedGovernedAndValidated(t *testing.T) {
	a := testApp(t)
	category := createPlanningCategory(t, a, "项目独有分类")
	createPlanningCategory(t, a, "已有分类")
	w := apiRequest(a, http.MethodPost, "/api/requirement-categories", "u_admin", projectID, `{"name":"项目独有分类"}`)
	if w.Code != 409 {
		t.Fatalf("duplicate accepted: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"", " ", "未分类", "全部", "/空层级", "路径//空层级", "换\n行", strings.Repeat("长", 121)} {
		w = apiRequest(a, http.MethodPost, "/api/requirement-categories", "u_admin", projectID, jsonText(map[string]string{"name": name}))
		if w.Code != 422 {
			t.Fatalf("invalid category accepted: %q %d %s", name, w.Code, w.Body.String())
		}
	}
	for _, user := range []string{"u_viewer", "u_qa", "u_front"} {
		w = apiRequest(a, http.MethodPost, "/api/requirement-categories", user, projectID, `{"name":"越权分类"}`)
		if w.Code != 403 {
			t.Fatalf("%s managed category: %d %s", user, w.Code, w.Body.String())
		}
	}
	for _, user := range []string{"u_pm", "u_front_lead", "u_back_lead"} {
		w = apiRequest(a, http.MethodPost, "/api/requirement-categories", user, projectID, jsonText(map[string]string{"name": "分类-" + user}))
		if w.Code != 201 {
			t.Fatalf("%s cannot manage category: %d %s", user, w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, http.MethodPost, "/api/requirements", "u_admin", insightProjectID, `{"title":"跨项目分类","category":"项目独有分类"}`)
	if w.Code != 422 {
		t.Fatalf("foreign project category accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodDelete, fmt.Sprintf("/api/requirement-categories/%d", category.ID), "u_admin", insightProjectID, "")
	if w.Code != 404 {
		t.Fatalf("cross-project category deleted: %d %s", w.Code, w.Body.String())
	}
	x := planningRequirement(t, a, `{"title":"分类事务","category":"项目独有分类"}`)
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirement-categories/%d", category.ID), "u_admin", projectID, `{"name":"已有分类"}`)
	if w.Code != 409 {
		t.Fatalf("duplicate category rename accepted: %d %s", w.Code, w.Body.String())
	}
	stored, _ := a.get(x.ID)
	if stored.Category != "项目独有分类" {
		t.Fatal("failed rename partially persisted")
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"category":"尚未注册","remarks":"不应保存"}`)
	if w.Code != 422 {
		t.Fatalf("unregistered category accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementCategoriesBackfillAndNewProject(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"旧分类迁移"}`)
	if _, err := a.db.Exec(`UPDATE requirements SET category='历史目录/旧分类' WHERE id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementCollaboration(); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementCollaboration(); err != nil {
		t.Fatal(err)
	}
	stored, _ := a.get(x.ID)
	if stored.Category != "历史目录/旧分类" || stored.CreatedAt != x.CreatedAt {
		t.Fatal("category backfill changed legacy requirement")
	}
	if err := a.validateRequirementCategory("历史目录/旧分类"); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPost, "/api/projects", "u_admin", projectID, `{"name":"分类新项目","code":"CTEST"}`)
	if w.Code != 201 {
		t.Fatalf("project create failed: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/requirement-categories", "u_admin", "prj_ctest", "")
	items := jsonMap(t, w)["items"].([]any)
	if w.Code != 200 || len(items) != len(defaultRequirementCategoryNames) || items[0].(map[string]any)["name"] != "未分类" {
		t.Fatalf("new project default category missing: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementMentionsUseExactIDsAndPersistWithOutbox(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"精确提及"}`)
	if _, err := a.db.Exec(`UPDATE users SET name='同名工程师' WHERE id IN ('u_front','u_back')`); err != nil {
		t.Fatal(err)
	}
	body := "  请 @同名工程师 审核，@周屿 只是正文。  "
	payload := jsonText(map[string]any{"body": body, "mentionUserIds": []string{"u_front", "u_front"}})
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	for i := 0; i < 2; i++ {
		w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, payload)
		if w.Code != 201 {
			t.Fatalf("comment failed: %d %s", w.Code, w.Body.String())
		}
		var comment Comment
		if err := json.Unmarshal(w.Body.Bytes(), &comment); err != nil {
			t.Fatal(err)
		}
		if comment.Body != body || len(comment.MentionUserIDs) != 1 || comment.MentionUserIDs[0] != "u_front" {
			t.Fatalf("explicit IDs/body lost: %+v", comment)
		}
	}
	var notifications, outbox, wrongRecipient int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type='requirement.mentioned' AND subject_id=? AND recipient_user_id='u_front'`, x.ID).Scan(&notifications)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type='requirement.mentioned' AND subject_id=? AND recipient_user_id!='u_front'`, x.ID).Scan(&wrongRecipient)
	a.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE event_type='requirement.mentioned'`).Scan(&outbox)
	if notifications != 2 || outbox != 2 || wrongRecipient != 0 {
		t.Fatalf("incorrect mentions notifications=%d outbox=%d wrong=%d", notifications, outbox, wrongRecipient)
	}
	var raw string
	a.db.QueryRow(`SELECT payload FROM notification_outbox WHERE event_type='requirement.mentioned' LIMIT 1`).Scan(&raw)
	var external struct {
		MentionUserIDs []string `json:"mentionUserIds"`
	}
	if err := json.Unmarshal([]byte(raw), &external); err != nil || len(external.MentionUserIDs) != 1 || external.MentionUserIDs[0] != "u_front" {
		t.Fatalf("external notification not explicitly addressed: %s", raw)
	}
	w := apiRequest(a, http.MethodGet, path, "u_admin", projectID, "")
	var list struct {
		Items []Comment `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 2 || len(list.Items[0].MentionUserIDs) != 1 {
		t.Fatalf("stored mention IDs unavailable: %s", w.Body.String())
	}
}

func TestRequirementMentionsRejectForeignInactiveAndMissingMembers(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"提及校验"}`)
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"u_missing", "u_front"} {
		w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]any{"body": "不可写入", "mentionUserIds": []string{uid}}))
		if w.Code != 422 {
			t.Fatalf("invalid mention accepted: %d %s", w.Code, w.Body.String())
		}
	}
	other := *a
	other.project = insightProjectID
	y := planningRequirement(t, &other, `{"title":"另一项目"}`)
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/comments", y.ID), "u_admin", insightProjectID, `{"body":"无项目权限成员","mentionUserIds":["u_back"]}`)
	if w.Code != 422 {
		t.Fatalf("foreign member accepted: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, path, "u_admin", insightProjectID, `{"body":"跨项目需求评论","mentionUserIds":[]}`)
	if w.Code != 404 {
		t.Fatalf("foreign requirement accepted comment: %d %s", w.Code, w.Body.String())
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE requirement_id IN (?,?)`, x.ID, y.ID).Scan(&count)
	if count != 0 {
		t.Fatal("invalid mention comment persisted")
	}
}

func TestRequirementLegacyMentionsRespectNamesBoundariesAndProject(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"旧版提及解析"}`)
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	if _, err := a.db.Exec(`UPDATE users SET name='周' WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"请 @周屿，确认。", "这不是提及：@周屿团队", "邮箱 account@周屿"} {
		w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]string{"body": body}))
		if w.Code != 201 {
			t.Fatalf("legacy comment failed: %d %s", w.Code, w.Body.String())
		}
	}
	var correct, substring int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type='requirement.mentioned' AND subject_id=? AND recipient_user_id='u_member'`, x.ID).Scan(&correct)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE event_type='requirement.mentioned' AND subject_id=? AND recipient_user_id='u_front'`, x.ID).Scan(&substring)
	if correct != 1 || substring != 0 {
		t.Fatalf("substring/email falsely mentioned: correct=%d shorter=%d", correct, substring)
	}
	if _, err := a.db.Exec(`UPDATE users SET name='周屿' WHERE id='u_back'`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, `{"body":"请 @周屿 确认，但名字重复"}`)
	if w.Code != 201 || len(jsonMap(t, w)["mentionUserIds"].([]any)) != 0 {
		t.Fatalf("duplicate display name should not infer IDs: %d %s", w.Code, w.Body.String())
	}
	other := *a
	other.project = insightProjectID
	y := planningRequirement(t, &other, `{"title":"外项目正文提及"}`)
	w = apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/comments", y.ID), "u_admin", insightProjectID, `{"body":"@周屿 不属于这个项目"}`)
	if w.Code != 201 || len(jsonMap(t, w)["mentionUserIds"].([]any)) != 0 {
		t.Fatalf("legacy mention crossed project: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPost, path, "u_admin", projectID, `{"body":"@周屿 显式不提及","mentionUserIds":[]}`)
	if w.Code != 201 || len(jsonMap(t, w)["mentionUserIds"].([]any)) != 0 {
		t.Fatal("explicit empty mention list did not suppress legacy parsing")
	}
}

func TestRequirementCommentAndMentionNotificationRollbackTogether(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"通知事务验证"}`)
	if _, err := a.db.Exec(`CREATE TRIGGER fail_mention_outbox BEFORE INSERT ON notification_outbox WHEN NEW.event_type='requirement.mentioned' BEGIN SELECT RAISE(ABORT,'test outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, `{"body":"@周屿 不能部分发送","mentionUserIds":["u_member"]}`)
	if w.Code != 500 {
		t.Fatalf("expected injected storage failure: %d %s", w.Code, w.Body.String())
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM comments WHERE requirement_id=?`,
		`SELECT COUNT(*) FROM user_notifications WHERE event_type='requirement.mentioned' AND subject_id=?`,
		`SELECT COUNT(*) FROM activities WHERE event='commented' AND requirement_id=?`,
	} {
		var count int
		if err := a.db.QueryRow(query, x.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial mention transaction persisted: %s count=%d err=%v", query, count, err)
		}
	}
}

func TestRequirementPartialPatchCannotRestoreStaleCategory(t *testing.T) {
	a := testApp(t)
	old := createPlanningCategory(t, a, "切换前分类")
	createPlanningCategory(t, a, "切换后分类")
	x := planningRequirement(t, a, `{"title":"并发分类保护","category":"切换前分类","priority":"P2"}`)
	// Simulate another writer changing an unrelated field between reading the
	// requirement snapshot and applying this PATCH. Only supplied columns may
	// be written back; otherwise the old full-row UPDATE restores an orphan.
	trigger := fmt.Sprintf(`CREATE TRIGGER change_category_during_patch BEFORE UPDATE OF remarks ON requirements WHEN OLD.id=%d BEGIN UPDATE requirements SET category='切换后分类',priority='P0' WHERE id=OLD.id; DELETE FROM requirement_categories WHERE id=%d; END`, x.ID, old.ID)
	if _, err := a.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	updated := patchPlanningRequirement(t, a, x.ID, `{"remarks":"只修改备注"}`)
	if updated.Category != "切换后分类" || updated.Priority != "P0" || updated.Remarks != "只修改备注" {
		t.Fatalf("partial PATCH restored stale unrelated fields: %+v", updated)
	}
	if err := a.validateRequirementCategory(updated.Category); err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := a.validateRequirementCategoryUsing(tx, "切换前分类"); err == nil {
		t.Fatal("transaction accepted removed category")
	}
}

func TestRequirementMentionLimitAppliesToExplicitAndLegacy(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"提及人数上限"}`)
	ids, names := []string{}, []string{}
	for index := 0; index < 51; index++ {
		id, name := fmt.Sprintf("u_limit_%02d", index), fmt.Sprintf("限额成员%02d", index)
		if _, err := a.db.Exec(`INSERT INTO users(id,tenant_id,name,email)VALUES(?,?,?,?)`, id, tenantID, name, id+"@local.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'member','test','test')`, tenantID, projectID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,'member','active','test','test')`, tenantID, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		names = append(names, "@"+name)
	}
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	for _, payload := range []map[string]any{
		{"body": strings.Join(names, " "), "mentionUserIds": ids},
		{"body": strings.Join(names, " ")},
	} {
		w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(payload))
		if w.Code != 422 {
			t.Fatalf("mention limit bypassed: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE requirement_id=?`, x.ID).Scan(&count)
	if count != 0 {
		t.Fatal("over-limit comment persisted")
	}
	w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]any{"body": strings.Join(names[:50], " "), "mentionUserIds": ids[:50]}))
	if w.Code != 201 || len(jsonMap(t, w)["mentionUserIds"].([]any)) != 50 {
		t.Fatalf("valid recipient limit rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementPlainCommentMentionsMustMatchBody(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"评论提及正文绑定"}`)
	path := fmt.Sprintf("/api/requirements/%d/comments", x.ID)
	front, back := peopleName(t, a, "u_front"), peopleName(t, a, "u_back")
	counts := map[string]int{}
	for _, table := range []string{"comments", "activities", "audit_logs", "user_notifications", "notification_outbox", "requirement_attachments"} {
		counts[table] = tableCount(t, a, table)
	}
	for _, test := range []struct {
		body string
		ids  []string
	}{
		{"正文未提及任何人", []string{"u_front"}},
		{"@" + front + "扩展姓名 不是同一人", []string{"u_front"}},
		{"email@" + front + " 不是提及", []string{"u_front"}},
		{"只提及 @" + back + " 请查看", []string{"u_front"}},
		{"只提及 @" + front + " 请查看", []string{"u_front", "u_back"}},
	} {
		w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]any{"body": test.body, "mentionUserIds": test.ids}))
		if w.Code != 422 || jsonMap(t, w)["error"].(map[string]any)["code"] != "invalid_mentions" {
			t.Fatalf("mismatched mention accepted: %q %v %d %s", test.body, test.ids, w.Code, w.Body.String())
		}
		for table, before := range counts {
			if after := tableCount(t, a, table); after != before {
				t.Fatalf("rejected mention changed %s: %d -> %d", table, before, after)
			}
		}
	}
	// A selected mention can follow Chinese prose, and a repeated explicit ID
	// still sends one notice. Preserve all original body whitespace verbatim.
	body := "  请@" + front + "，检查这项内容。  "
	w := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]any{"body": body, "mentionUserIds": []string{"u_front", "u_front"}}))
	if w.Code != 201 || jsonMap(t, w)["body"] != body || peopleNoticeCount(t, a, x.ID, "requirement.mentioned", "u_front") != 1 {
		t.Fatalf("valid selected mention rejected or rewritten: %d %s", w.Code, w.Body.String())
	}
}
