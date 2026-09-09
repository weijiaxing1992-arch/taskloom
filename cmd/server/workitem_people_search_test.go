package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestWorkItemPeopleSearchNamesDepartmentsAndIsolation(t *testing.T) {
	a := testApp(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE requirements SET owner='',assignee='',owner_user_id='',assignee_user_id='',owner_user_ids_json='[]',assignee_user_ids_json='[]',role_weights_json='{}'`)
	exec(`UPDATE defects SET assignee='',verifier='',assignee_user_id='',verifier_user_id=''`)
	exec(`UPDATE users SET name='甲前端工程师' WHERE id='u_front'`)
	var backend string
	if err := a.db.QueryRow(`SELECT id FROM users WHERE id!='u_front' AND id!='u_pm' AND id!='u_admin' ORDER BY id LIMIT 1`).Scan(&backend); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE users SET name='乙后端工程师' WHERE id=?`, backend)
	exec(`UPDATE users SET name='丙需求负责人' WHERE id='u_pm'`)
	exec(`UPDATE users SET name='丁字段工程师' WHERE id='u_admin'`)
	exec(`INSERT INTO departments(id,tenant_id,name,code,external_id,created_at,updated_at)VALUES('search-dept',?,'研发搜索专项组','search-dept','search-dept','x','x')`, tenantID)
	exec(`INSERT INTO department_memberships(tenant_id,department_id,user_id,joined_at,updated_at)VALUES(?,'search-dept','u_front','x','x')`, tenantID)
	exec(`UPDATE requirements SET assignee_user_ids_json=json_array('u_pm','u_front'),owner_user_ids_json=json_array('u_pm'),role_weights_json=json_object('backend',json_object('userIds',json_array(?))) WHERE id=1`, backend)
	exec(`UPDATE defects SET assignee_user_id='u_front',verifier_user_id='u_pm' WHERE id=1`)
	field := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"search_engineer","name":"专项工程师","type":"users"}`)
	exec(`INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,'requirement',1,?,'["u_admin"]','x')`, tenantID, projectID, field.ID)
	search := func(path, term string) map[string]any {
		t.Helper()
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		w := apiRequest(a, "GET", path+sep+"q="+url.QueryEscape(term), "u_front", projectID, "")
		if w.Code != 200 {
			t.Fatalf("%s %s: %d %s", path, term, w.Code, w.Body.String())
		}
		return jsonMap(t, w)
	}
	for _, term := range []string{"甲前端", "乙后端", "丙需求负责人", "丁字段工程师", "研发搜索专项组"} {
		for _, path := range []string{"/api/search?type=需求", "/api/requirements"} {
			result := search(path, term)
			items := result["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"].(float64) != 1 {
				t.Fatalf("%s %s wrong results: %v", path, term, result)
			}
			if path == "/api/search?type=需求" && !strings.Contains(items[0].(map[string]any)["snippet"].(string), term) {
				t.Fatalf("missing match explanation: %v", items)
			}
		}
	}
	for _, term := range []string{"甲前端", "丙需求负责人", "研发搜索专项组"} {
		for _, path := range []string{"/api/search?type=缺陷", "/api/defects"} {
			result := search(path, term)
			if len(result["items"].([]any)) != 1 {
				t.Fatalf("%s %s: %v", path, term, result)
			}
		}
	}
	exec(`UPDATE field_definitions SET enabled=0 WHERE id=?`, field.ID)
	if len(search("/api/search?type=需求", "丁字段工程师")["items"].([]any)) != 0 {
		t.Fatal("disabled person field matched")
	}
	exec(`UPDATE department_memberships SET status='removed' WHERE department_id='search-dept'`)
	if len(search("/api/search?type=需求", "研发搜索专项组")["items"].([]any)) != 0 {
		t.Fatal("outdated department matched")
	}
	exec(`UPDATE requirements SET assignee_user_id='u_front' WHERE project_id=?`, insightProjectID)
	if len(search("/api/search?type=需求&project="+insightProjectID, "甲前端")["items"].([]any)) != 0 {
		t.Fatal("private project leaked")
	}
	if len(search("/api/search", "%' OR 1=1 --")["items"].([]any)) != 0 {
		t.Fatal("query escaped parameter boundary")
	}
	// 历史人员字段可存单 ID 字符串；文本字段即使含相同 ID 也不能推断身份。
	exec(`UPDATE field_definitions SET enabled=1 WHERE id=?`, field.ID)
	exec(`UPDATE field_values SET value_json='"u_admin"' WHERE field_definition_id=?`, field.ID)
	if len(search("/api/search?type=需求", "丁字段工程师")["items"].([]any)) != 1 {
		t.Fatal("single person field missed")
	}
	exec(`UPDATE field_definitions SET type='text' WHERE id=?`, field.ID)
	if len(search("/api/search?type=需求", "丁字段工程师")["items"].([]any)) != 0 {
		t.Fatal("text field treated as person")
	}
	// 只剩旧姓名的记录可按姓名查找，但不能据此关联同名成员的部门。
	exec(`UPDATE requirements SET assignee='甲前端工程师',assignee_user_id='',owner_user_id='',assignee_user_ids_json='[]',owner_user_ids_json='[]',role_weights_json='{}' WHERE id=1`)
	exec(`UPDATE department_memberships SET status='active' WHERE department_id='search-dept'`)
	if len(search("/api/search?type=需求&project="+projectID, "研发搜索专项组")["items"].([]any)) != 0 {
		t.Fatal("legacy name inferred department")
	}
	if len(search("/api/search?type=需求&project="+projectID, "甲前端")["items"].([]any)) != 1 {
		t.Fatal("legacy name lost")
	}
}
