package main

import (
	"context"
	"fmt"
	"net/url"
	"testing"
)

func TestRelatedQuickFiltersShareAllParticipants(t *testing.T) {
	a := testApp(t)
	req := planningRequirement(t, a, `{"title":"related-unified-acceptance","assigneeUserIds":[],"ownerUserIds":[]}`)
	run := func(q string, args ...any) {
		t.Helper()
		if _, err := a.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	run(`UPDATE requirements SET sprint='待规划' WHERE id=?`, req.ID)
	run(`INSERT INTO defects(tenant_id,project_id,code,title,sprint,created_at,updated_at)VALUES(?,?,'RELATED-BUG','related-unified-acceptance','待规划','now','now')`, tenantID, projectID)
	var bug int64
	if err := a.db.QueryRow(`SELECT id FROM defects WHERE code='RELATED-BUG'`).Scan(&bug); err != nil {
		t.Fatal(err)
	}
	rf := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"related_staff","name":"专项工程师","type":"users"}`)
	df := fieldTestDefinition(t, a, `{"objectType":"defect","key":"related_staff","name":"专项工程师","type":"users"}`)
	check := func(user string, wantReq, wantBug bool) {
		t.Helper()
		for _, item := range []struct {
			path string
			want bool
		}{{"requirements", wantReq}, {"defects", wantBug}} {
			w := apiRequest(a, "GET", "/api/"+item.path+"?mine=1&q="+url.QueryEscape(req.Title), user, projectID, "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			got := len(jsonMap(t, w)["items"].([]any))
			want := 0
			if item.want {
				want = 1
			}
			if got != want {
				t.Fatalf("%s %s want %d: %s", user, item.path, want, w.Body.String())
			}
		}
		scoped := *a
		scoped.user = user
		items, err := scoped.sprintWorkItems(context.Background(), "待规划")
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, item := range items {
			if item["title"] == req.Title {
				found++
				want := wantReq
				if item["objectType"] == "defect" {
					want = wantBug
				}
				if item["relatedToMe"] != want {
					t.Fatalf("sprint relation %#v want %v", item, want)
				}
			}
		}
		if found != 2 {
			t.Fatal("missing sprint records", found)
		}
	}
	check("u_front", false, false)
	for _, item := range []struct {
		object    string
		id, field int64
	}{{"requirement", req.ID, rf.ID}, {"defect", bug, df.ID}} {
		run(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,?,?,?,'["u_back","u_front"]','now')`, tenantID, projectID, item.object, item.id, item.field)
	}
	check("u_front", true, true)
	run(`UPDATE field_definitions SET enabled=0 WHERE id IN (?,?)`, rf.ID, df.ID)
	check("u_front", false, false)
	run(`UPDATE requirements SET role_weights_json='{"backend":{"userIds":["u_back","u_front"],"value":0}}' WHERE id=?`, req.ID)
	check("u_front", true, false)
	run(`UPDATE requirements SET role_weights_json='{}',text_mentions_json='{"description":{"u_front":"旧姓名"}}' WHERE id=?`, req.ID)
	check("u_front", true, false)
	run(`UPDATE requirements SET text_mentions_json='{}' WHERE id=?`, req.ID)
	var name string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE id='u_front'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fmt.Sprintf("requirements/%d", req.ID), fmt.Sprintf("defects/%d", bug)} {
		w := apiRequest(a, "POST", "/api/"+path+"/comments", "u_back", projectID, jsonText(map[string]any{"body": "@" + name + " 请检查代码", "mentionUserIds": []string{"u_front"}}))
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	check("u_front", true, true)
	check("u_back", true, true)
	run(`UPDATE users SET name=? WHERE id='u_ui'`, name)
	check("u_ui", false, false)
	// 同名、已读和旧通知均不能改变稳定账号判定；已有权限检查仍包围全部查询。
	w := apiRequest(a, "POST", "/api/notifications/read-all", "u_front", projectID, `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	check("u_front", true, true)
	invalid := apiRequest(a, "GET", "/api/requirements?mine=u_front", "u_admin", projectID, "")
	if invalid.Code != 422 {
		t.Fatal(invalid.Code)
	}
	run(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID)
	denied := apiRequest(a, "GET", "/api/requirements?mine=1", "u_front", projectID, "")
	if denied.Code != 403 {
		t.Fatal("revoked access", denied.Code, denied.Body.String())
	}
}
