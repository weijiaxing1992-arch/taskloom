package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMarkdownTableDocumentPersistsAndRejectsMalformedRows(t *testing.T) {
	a := testApp(t)
	doc := `{"type":"doc","content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"字段"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"名称"}]}]}]}]}]}`
	x := createPeopleRequirement(t,a,map[string]any{"title":"Markdown table","descriptionDoc":json.RawMessage(doc)})
	if !strings.Contains(x.Description,"名称") { t.Fatal("table text lost",x.Description) }
	w := apiRequest(a,"GET",fmt.Sprintf("/api/requirements/%d",x.ID),"u_admin",projectID,"")
	if w.Code!=200 || !strings.Contains(w.Body.String(),"tableHeader") { t.Fatal(w.Body.String()) }
	bad := strings.Replace(doc,`"tableCell"`,`"text"`,1)
	if _, err := parseRichDocument(json.RawMessage(bad)); err==nil { t.Fatal("malformed table accepted") }
	bad = strings.Replace(doc,`"tableCell"`,`"tableCell","attrs":{"onclick":"evil"}`,1)
	if _, err := parseRichDocument(json.RawMessage(bad)); err==nil { t.Fatal("unsafe attributes accepted") }
}

func TestNotificationGroupsFilterBeforePagingAndKeepUnreadScope(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DELETE FROM user_notifications`); err != nil { t.Fatal(err) }
	for i := 0; i < 46; i++ {
		event := "requirement.updated"
		if i == 0 { event = "requirement.mentioned" }
		if _, err := a.db.Exec(`INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,'u_front','u_admin',?,'requirement',1,'通知','内容',?,?)`, tenantID, projectID, event, fmt.Sprintf("2026-09-08T00:00:%02dZ", i), fmt.Sprint(i)); err != nil { t.Fatal(err) }
	}
	w := apiRequest(a, "GET", "/api/notifications?group=mentions", "u_front", projectID, "")
	if w.Code != 200 { t.Fatal(w.Body.String()) }
	v := jsonMap(t, w)
	if len(v["items"].([]any)) != 1 || v["total"] != float64(1) || v["unread"] != float64(46) || v["groupUnread"].(map[string]any)["changes"] != float64(45) { t.Fatal(w.Body.String()) }
	w = apiRequest(a, "GET", "/api/notifications?group=changes&offset=40", "u_front", projectID, "")
	v = jsonMap(t, w)
	if len(v["items"].([]any)) != 5 || v["hasMore"] != false || v["total"] != float64(45) { t.Fatal(w.Body.String()) }
	w = apiRequest(a, "GET", "/api/notifications?group=mentions", "u_back", projectID, "")
	if w.Code != 200 || jsonMap(t,w)["unread"] != float64(0) { t.Fatal("other account leaked", w.Body.String()) }
	w = apiRequest(a, "GET", "/api/notifications?group=unknown", "u_front", projectID, "")
	if w.Code != 400 { t.Fatal("invalid group accepted", w.Body.String()) }
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil { t.Fatal(err) }
	// 撤销项目权限后，列表和分类数量都不得泄露原有消息。
	w = apiRequest(a, "GET", "/api/notifications?group=mentions", "u_front", projectID, "")
	if w.Code == 200 {
		v = jsonMap(t,w)
		if v["total"] != float64(0) || v["unread"] != float64(0) { t.Fatal(w.Body.String()) }
	} else if w.Code != 403 { t.Fatal(w.Body.String()) }
}

func TestWorkAndChangeNoticesIncludeBoundCollaborators(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"title":"协作人员覆盖", "assigneeUserIds":[]string{}, "ownerUserIds":[]string{}, "roleWeights": map[string]any{"frontend":map[string]any{"userIds":[]string{"u_front"},"value":1}}})
	// 通过字段存储构造测试人员绑定，验证预置字段与权重字段共同参与通知。
	var fieldID int64
	if err := a.db.QueryRow(`SELECT id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key='testers'`, tenantID, projectID).Scan(&fieldID); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`UPDATE field_definitions SET enabled=1,type='users',deleted_at='' WHERE id=?`, fieldID); err != nil { t.Fatal(err) }
	if _, err := a.db.Exec(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,'requirement',?,?,?,?) ON CONFLICT(tenant_id,project_id,object_type,object_id,field_definition_id) DO UPDATE SET value_json=excluded.value_json`, tenantID, projectID, x.ID, fieldID, `["u_qa","u_front"]`, orgNow()); err != nil { t.Fatal(err) }
	for _, uid := range []string{"u_front", "u_qa"} {
		w := apiRequest(a,"GET","/api/my-work?category=&q=协作人员覆盖",uid,projectID,"")
		if w.Code != 200 || !strings.Contains(w.Body.String(),"协作人员覆盖") { t.Fatal("missing collaboration",uid,w.Body.String()) }
	}
	w := apiRequest(a,"PATCH",fmt.Sprintf("/api/requirements/%d",x.ID),"u_admin",projectID,`{"acceptance":"新的验收标准"}`)
	if w.Code != 200 { t.Fatal(w.Body.String()) }
	for _, uid := range []string{"u_front","u_qa"} { if n := peopleNoticeCount(t,a,x.ID,"requirement.updated",uid); n != 1 { t.Fatalf("%s wants one notice, got %d",uid,n) } }
	if _, err := a.db.Exec(`UPDATE users SET name=(SELECT name FROM users WHERE id='u_qa') WHERE id='u_ui'`); err != nil { t.Fatal(err) }
	w = apiRequest(a,"GET","/api/my-work?q=协作人员覆盖","u_ui",projectID,"")
	if strings.Contains(w.Body.String(),"协作人员覆盖") { t.Fatal("same-name user inherited work",w.Body.String()) }
	if _, err := a.db.Exec(`UPDATE field_definitions SET enabled=0 WHERE id=?`,fieldID); err != nil { t.Fatal(err) }
	w = apiRequest(a,"GET","/api/my-work?q=协作人员覆盖","u_qa",projectID,"")
	if strings.Contains(w.Body.String(),"协作人员覆盖") { t.Fatal("disabled field still assigns work",w.Body.String()) }
}
