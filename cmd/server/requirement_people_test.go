package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func peopleName(t *testing.T, a *App, id string) string {
	t.Helper()
	var name string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, id).Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
}

func createPeopleRequirement(t *testing.T, a *App, body map[string]any) Requirement {
	t.Helper()
	if body["title"] == nil {
		body["title"] = "人员协作回归需求"
	}
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(body))
	if w.Code != 201 {
		t.Fatalf("create requirement: %d %s", w.Code, w.Body.String())
	}
	var x Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func patchPeopleRequirement(t *testing.T, a *App, id int64, body map[string]any) Requirement {
	t.Helper()
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", id), "u_admin", projectID, jsonText(body))
	if w.Code != 200 {
		t.Fatalf("patch requirement: %d %s", w.Code, w.Body.String())
	}
	var x Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	return x
}

func peopleNoticeCount(t *testing.T, a *App, id int64, event, recipient string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND project_id=? AND subject_type='requirement' AND subject_id=? AND event_type=? AND recipient_user_id=?`, tenantID, projectID, id, event, recipient).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRequirementTextMentionsAndMultipleAssigneesCreate(t *testing.T) {
	a := testApp(t)
	front, pm := peopleName(t, a, "u_front"), peopleName(t, a, "u_pm")
	description, remarks := "正文需要 @"+front+" 一起确认。", "备注请 @"+pm+" 查看。"
	x := createPeopleRequirement(t, a, map[string]any{"description": description, "descriptionMentionUserIds": []string{"u_front", "u_front"}, "remarks": remarks, "remarksMentionUserIds": []string{"u_pm"}, "assigneeUserIds": []string{"u_front", "u_back", "u_front"}, "descriptionMentionNames": map[string]string{"u_front": "伪造名字"}, "assignees": []map[string]string{{"id": "u_admin", "name": "伪造"}}})
	if !reflect.DeepEqual(x.DescriptionMentionUserIDs, []string{"u_front"}) || x.DescriptionMentionNames["u_front"] != front || !reflect.DeepEqual(x.RemarksMentionUserIDs, []string{"u_pm"}) || x.RemarksMentionNames["u_pm"] != pm {
		t.Fatalf("mention IDs/snapshots not canonical: %+v", x)
	}
	if !reflect.DeepEqual(x.AssigneeUserIDs, []string{"u_front", "u_back"}) || len(x.Assignees) != 2 || x.AssigneeUserID != "u_front" || x.Assignee != front {
		t.Fatalf("multiple assignees/primary mismatch: %+v", x)
	}
	for _, expect := range []struct{ event, user string }{{"requirement.description_mentioned", "u_front"}, {"requirement.remarks_mentioned", "u_pm"}, {"requirement.assigned", "u_front"}, {"requirement.assigned", "u_back"}} {
		if n := peopleNoticeCount(t, a, x.ID, expect.event, expect.user); n != 1 {
			t.Fatalf("%s/%s notifications=%d", expect.event, expect.user, n)
		}
	}
	got, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AssigneeUserIDs, x.AssigneeUserIDs) || !reflect.DeepEqual(got.DescriptionMentionNames, x.DescriptionMentionNames) || got.Description != description || got.Remarks != remarks {
		t.Fatalf("persisted collaboration fields changed: %+v", got)
	}
	w := languageRequest(t, a, "GET", "/api/notifications?eventType=requirement.description_mentioned", "u_front", projectID, "en-US", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "You were mentioned in a requirement description") || !strings.Contains(w.Body.String(), description) || !strings.Contains(w.Body.String(), fmt.Sprintf("/requirements?req=%d", x.ID)) {
		t.Fatalf("translated/deep-linked notice: %d %s", w.Code, w.Body.String())
	}
	for _, filter := range []string{"assigneeUserId=u_back", "assignee=u_back"} {
		w := apiRequest(a, "GET", "/api/requirements?"+filter, "u_admin", projectID, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf(`"id":%d`, x.ID)) {
			t.Fatalf("secondary assignee filter missing requirement: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRequirementPeopleDiffsLegacyEditsAndReaddition(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	body := "请 @" + name + " 处理"
	x := createPeopleRequirement(t, a, map[string]any{"description": body, "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front", "u_back"}})
	for i := 0; i < 2; i++ {
		patchPeopleRequirement(t, a, x.ID, map[string]any{"description": body, "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front", "u_back"}})
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"priority": "P1"})
	legacy := patchPeopleRequirement(t, a, x.ID, map[string]any{"description": body + "，新增说明"})
	if !reflect.DeepEqual(legacy.DescriptionMentionUserIDs, []string{"u_front"}) || peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 1 {
		t.Fatalf("legacy/no-op edit lost metadata or repeated notification: %+v", legacy)
	}
	reordered := patchPeopleRequirement(t, a, x.ID, map[string]any{"assigneeUserIds": []string{"u_back", "u_front"}})
	if reordered.AssigneeUserID != "u_back" || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_back") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_front") != 1 {
		t.Fatalf("order-only save repeated assignment: %+v", reordered)
	}
	cleared := patchPeopleRequirement(t, a, x.ID, map[string]any{"description": "不再提及", "assigneeUserIds": []string{}})
	if len(cleared.DescriptionMentionUserIDs) != 0 || len(cleared.AssigneeUserIDs) != 0 || cleared.Assignee != "" || cleared.AssigneeUserID != "" {
		t.Fatalf("clear did not remove state: %+v", cleared)
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"description": body, "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front"}})
	if peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 2 || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_front") != 2 {
		t.Fatal("readding a removed person did not notify again")
	}
	legacy = patchPeopleRequirement(t, a, x.ID, map[string]any{"remarks": "只写 @" + peopleName(t, a, "u_pm") + " 未显式选择成员"})
	if len(legacy.RemarksMentionUserIDs) != 0 || peopleNoticeCount(t, a, x.ID, "requirement.remarks_mentioned", "u_pm") != 0 {
		t.Fatal("legacy free text guessed a new identity")
	}
}

func TestRequirementMentionsRequireRealTokensAndScopedIDs(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	x := createPeopleRequirement(t, a, map[string]any{})
	for _, body := range []map[string]any{
		{"description": "完全没有提及", "descriptionMentionUserIds": []string{"u_front"}},
		{"description": "email@" + name, "descriptionMentionUserIds": []string{"u_front"}},
		{"description": "@" + name + "更多字", "descriptionMentionUserIds": []string{"u_front"}},
		{"description": "@不存在的成员 ", "descriptionMentionUserIds": []string{"unknown"}},
		{"description": "@" + name, "descriptionMentionUserIds": nil},
		{"assigneeUserIds": []string{"unknown"}},
	} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(body))
		if w.Code != 422 {
			t.Fatalf("invalid selected identity/token accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{{"description": "@" + name, "descriptionMentionUserIds": []string{"u_front"}}, {"assigneeUserIds": []string{"u_front"}}} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(body))
		if w.Code != 422 {
			t.Fatalf("non-project member accepted: %d %s", w.Code, w.Body.String())
		}
	}
	got, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "" || len(got.DescriptionMentionUserIDs) != 0 || len(got.AssigneeUserIDs) != 0 {
		t.Fatalf("invalid edits partially persisted: %+v", got)
	}
}

func TestRequirementMentionsRejectOperationDisabledRecipients(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(map[string]any{
		"title":                     "停用账号不能被提及",
		"description":               "@" + name + " 不应生成新通知",
		"descriptionMentionUserIds": []string{"u_front"},
	}))
	if w.Code != 422 {
		t.Fatalf("operation-disabled requirement mention accepted: %d %s", w.Code, w.Body.String())
	}
	var notices int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND recipient_user_id='u_front' AND event_type='requirement.description_mentioned'`, tenantID).Scan(&notices); err != nil || notices != 0 {
		t.Fatalf("operation-disabled requirement recipient received inbox row=%d err=%v", notices, err)
	}
}

func TestRequirementHistoricalMentionsAndAssigneesRemainStable(t *testing.T) {
	a := testApp(t)
	oldName := peopleName(t, a, "u_front")
	body := "@" + oldName + " 负责这里"
	x := createPeopleRequirement(t, a, map[string]any{"description": body, "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front", "u_back"}})
	if _, err := a.db.Exec(`UPDATE users SET name='改名后的前端',active=0 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	got := patchPeopleRequirement(t, a, x.ID, map[string]any{"description": body + "，补充信息", "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front", "u_back"}})
	if got.DescriptionMentionNames["u_front"] != oldName || len(got.AssigneeUserIDs) != 2 || got.Assignees[0].Name != "改名后的前端" {
		t.Fatalf("historical identity lost: %+v", got)
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"priority": "P0"})
	if peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_front") != 1 {
		t.Fatal("retained historical identity notified again")
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"description": "移除提及", "descriptionMentionUserIds": []string{}, "assigneeUserIds": []string{"u_back"}})
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(map[string]any{"description": body, "descriptionMentionUserIds": []string{"u_front"}}))
	if w.Code != 422 {
		t.Fatalf("inactive historical identity was treated as a valid new mention: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementSelfMentionAndCommentMentionAreVisible(t *testing.T) {
	a := testApp(t)
	body := "@" + peopleName(t, a, "u_admin") + " 自查"
	x := createPeopleRequirement(t, a, map[string]any{"remarks": body, "remarksMentionUserIds": []string{"u_admin"}})
	if peopleNoticeCount(t, a, x.ID, "requirement.remarks_mentioned", "u_admin") != 1 {
		t.Fatal("explicit self mention missing from own inbox")
	}
	w := apiRequest(a, "POST", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, jsonText(map[string]any{"body": body, "mentionUserIds": []string{"u_admin"}}))
	if w.Code != 201 || peopleNoticeCount(t, a, x.ID, "requirement.mentioned", "u_admin") != 1 {
		t.Fatalf("explicit self comment mention missing: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementPeopleNotificationFailureRollsBackEverything(t *testing.T) {
	for _, table := range []string{"user_notifications", "notification_outbox"} {
		t.Run(table, func(t *testing.T) {
			a := testApp(t)
			x := createPeopleRequirement(t, a, map[string]any{"remarks": "原备注"})
			var beforeCount, beforeActivities int
			a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&beforeCount)
			a.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE requirement_id=?`, x.ID).Scan(&beforeActivities)
			if _, err := a.db.Exec(`CREATE TRIGGER force_people_notification_failure BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'test delivery failure'); END`); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"remarks": "@" + peopleName(t, a, "u_front") + " 新备注", "remarksMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_back"}}
			w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(body))
			if w.Code < 500 {
				t.Fatalf("failed notification returned success: %d %s", w.Code, w.Body.String())
			}
			got, err := a.get(x.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Remarks != "原备注" || len(got.RemarksMentionUserIDs) != 0 || len(got.AssigneeUserIDs) != 0 {
				t.Fatalf("failed transaction leaked business changes: %+v", got)
			}
			body["title"] = "不得创建的需求"
			w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(body))
			if w.Code < 500 {
				t.Fatalf("failed create notification returned success: %d %s", w.Code, w.Body.String())
			}
			var afterCount, afterActivities int
			a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&afterCount)
			a.db.QueryRow(`SELECT COUNT(*) FROM activities WHERE requirement_id=?`, x.ID).Scan(&afterActivities)
			if afterCount != beforeCount || afterActivities != beforeActivities || peopleNoticeCount(t, a, x.ID, "requirement.remarks_mentioned", "u_front") != 0 {
				t.Fatal("failed transaction left rows/activities/notifications")
			}
		})
	}
}

func TestConcurrentRequirementMentionSaveNotifiesOnce(t *testing.T) {
	a := fileSQLiteTestApp(t)
	cookie := fileSQLiteLogin(t, a)
	x := createPeopleRequirement(t, a, map[string]any{})
	body := jsonText(map[string]any{"description": "@" + peopleName(t, a, "u_front") + " 并发保存", "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_back"}})
	handler := a.scopedAPI()
	start := make(chan struct{})
	failures := make(chan string, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/requirements/%d", x.ID), strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-DevFlow-Project", projectID)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				failures <- fmt.Sprintf("%d %s", w.Code, w.Body.String())
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_back") != 1 {
		t.Fatal("concurrent identical updates sent duplicate notifications")
	}
}

func TestRequirementPeopleMigrationDoesNotRewriteOrRenotify(t *testing.T) {
	a := fileSQLiteTestApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"description": "@" + peopleName(t, a, "u_front"), "descriptionMentionUserIds": []string{"u_front"}, "assigneeUserIds": []string{"u_front", "u_back"}})
	before, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := a.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 1 || peopleNoticeCount(t, a, x.ID, "requirement.assigned", "u_front") != 1 {
		t.Fatal("migration rewrote people metadata or renotified")
	}
}

func TestRequirementPeopleExplicitIDsDisambiguateDuplicateNames(t *testing.T) {
	a := testApp(t)
	name := peopleName(t, a, "u_front")
	if _, err := a.db.Exec(`UPDATE users SET name=? WHERE id='u_back'`, name); err != nil {
		t.Fatal(err)
	}
	x := createPeopleRequirement(t, a, map[string]any{"description": "@" + name + " 一起协作", "descriptionMentionUserIds": []string{"u_back"}, "assigneeUserIds": []string{"u_back", "u_front"}})
	if !reflect.DeepEqual(x.AssigneeUserIDs, []string{"u_back", "u_front"}) || x.AssigneeUserID != "u_back" || peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_front") != 0 || peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", "u_back") != 1 {
		t.Fatalf("duplicate display names were conflated: %+v", x)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(map[string]any{"assignee": name}))
	if w.Code != 200 {
		t.Fatalf("unchanged legacy primary should preserve known identities: %d %s", w.Code, w.Body.String())
	}
	var unchanged Requirement
	if err := json.Unmarshal(w.Body.Bytes(), &unchanged); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged.AssigneeUserIDs, x.AssigneeUserIDs) {
		t.Fatal("unchanged ambiguous name changed known identities")
	}
	patchPeopleRequirement(t, a, x.ID, map[string]any{"assigneeUserIds": []string{"u_qa"}})
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(map[string]any{"assignee": name}))
	if w.Code != 422 {
		t.Fatalf("new ambiguous legacy name was guessed: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementPeopleRecipientLimits(t *testing.T) {
	a := testApp(t)
	ids := []string{}
	mentions := []string{}
	for i := 0; i < 51; i++ {
		id, name := fmt.Sprintf("u_limit_%02d", i), fmt.Sprintf("提及成员%02d", i)
		if _, err := a.db.Exec(`INSERT INTO users(id,tenant_id,name,email)VALUES(?,?,?,?)`, id, tenantID, name, id+"@test.invalid"); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,'member','active','','')`, tenantID, id); err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'frontend','','')`, tenantID, projectID, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		mentions = append(mentions, "@"+name)
	}
	x := createPeopleRequirement(t, a, map[string]any{"description": strings.Join(mentions[:50], " "), "descriptionMentionUserIds": ids[:50], "assigneeUserIds": ids[:50]})
	for _, body := range []map[string]any{{"description": strings.Join(mentions, " "), "descriptionMentionUserIds": ids}, {"assigneeUserIds": ids}} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, jsonText(body))
		if w.Code != 422 {
			t.Fatalf("recipient limit bypassed: %d %s", w.Code, w.Body.String())
		}
	}
	got, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AssigneeUserIDs) != 50 || len(got.DescriptionMentionUserIDs) != 50 || peopleNoticeCount(t, a, x.ID, "requirement.description_mentioned", ids[50]) != 0 {
		t.Fatal("over-limit edit partially persisted")
	}
}
