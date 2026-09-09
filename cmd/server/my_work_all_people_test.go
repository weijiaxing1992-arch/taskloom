package main

import (
	"fmt"
	"net/url"
	"testing"
)

func TestMyWorkAnyPeopleFieldRoleAndMention(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"全部参与关系验收","assigneeUserIds":[],"ownerUserIds":[]}`)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(user string, want int) {
		t.Helper()
		w := apiRequest(a, "GET", "/api/my-work?category=&q="+url.QueryEscape(x.Title), user, projectID, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if got := len(jsonMap(t, w)["items"].([]any)); got != want {
			t.Fatalf("user %s got %d want %d: %s", user, got, want, w.Body.String())
		}
	}
	check("u_front", 0)
	field := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"arbitrary_collaborator","name":"专项研发人员","type":"users"}`)
	exec(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,'requirement',?,?,'["u_back","u_front"]','x')`, tenantID, projectID, x.ID, field.ID)
	check("u_front", 1)
	exec(`UPDATE field_values SET value_json='"u_front"' WHERE field_definition_id=?`, field.ID)
	exec(`UPDATE field_definitions SET type='user' WHERE id=?`, field.ID)
	check("u_front", 1)
	exec(`UPDATE field_definitions SET enabled=0 WHERE id=?`, field.ID)
	check("u_front", 0)
	exec(`UPDATE field_definitions SET enabled=1,type='text' WHERE id=?`, field.ID)
	check("u_front", 0)
	exec(`UPDATE field_definitions SET type='users',deleted_at='removed' WHERE id=?`, field.ID)
	check("u_front", 0)
	// 各职能角色中的第二位成员同样属于我的工作，零权重不影响归属。
	for _, role := range []string{"frontend", "backend", "ui", "algorithm", "product"} {
		exec(`UPDATE requirements SET role_weights_json=json_object(?,json_object('userIds',json_array('u_back','u_front'),'value',0)) WHERE id=?`, role, x.ID)
		check("u_front", 1)
	}
	exec(`UPDATE requirements SET role_weights_json='{}',text_mentions_json='{"description":{"u_front":"旧姓名"}}' WHERE id=?`, x.ID)
	check("u_front", 1)
	exec(`UPDATE requirements SET text_mentions_json='{"remarks":{"u_front":"旧姓名"}}' WHERE id=?`, x.ID)
	check("u_front", 1)
	exec(`UPDATE requirements SET text_mentions_json='{}' WHERE id=?`, x.ID)
	check("u_front", 0)
	// 使用真实评论入口保存明确提及；读通知不会从我的工作中移除。
	var name string
	if err := a.db.QueryRow(`SELECT name FROM users WHERE id='u_front'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, jsonText(map[string]any{"body": "@" + name + " 请关注此需求", "mentionUserIds": []string{"u_front"}}))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	check("u_front", 1)
	w = apiRequest(a, "POST", "/api/notifications/read-all", "u_front", projectID, `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	check("u_front", 1)
	exec(`UPDATE users SET name=? WHERE id='u_ui'`, name)
	check("u_ui", 0)
	// 多种关系同时出现，只产生一个工作项；收回项目权限后不能继续返回。
	exec(`UPDATE requirements SET assignee_user_ids_json='["u_front"]',owner_user_ids_json='["u_front"]' WHERE id=?`, x.ID)
	check("u_front", 1)
	exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID)
	w = apiRequest(a, "GET", "/api/my-work?project=all&q="+url.QueryEscape(x.Title), "u_front", projectID, "")
	if w.Code == 200 {
		if len(jsonMap(t, w)["items"].([]any)) != 0 {
			t.Fatal("revoked work leaked")
		}
	} else if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
