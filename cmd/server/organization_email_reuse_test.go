package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedMemberEmailCanBeRecreatedWithoutInheritingIdentity(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		name := "single"
		if bulk {
			name = "bulk"
		}
		t.Run(name, func(t *testing.T) {
			a := testApp(t)
			var email, oldName string
			if err := a.db.QueryRow(`SELECT email,name FROM users WHERE id='u_front'`).Scan(&email, &oldName); err != nil {
				t.Fatal(err)
			}
			cookie, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "u_front")
			if err != nil {
				t.Fatal(err)
			}
			before := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM requirements WHERE assignee_user_id='u_front' OR owner_user_id='u_front'`)
			if bulk {
				bulkCall(t, a, "u_admin", "delete", []string{"u_front"}, nil, 200)
			} else {
				orgRequest(t, a, "DELETE", "/api/organization/members/u_front", "u_admin", nil, 200)
			}
			body := map[string]any{"name": "重新入职成员", "email": "  " + strings.ToUpper(email) + "  ", "initialPassword": "NewMemberOnly123!"}
			created := orgRequest(t, a, "POST", "/api/organization/members", "u_admin", body, 201)
			id := created["id"].(string)
			if id == "u_front" || created["email"] != email {
				t.Fatal("新账号必须使用新 ID 和规范化邮箱")
			}
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE u.id='u_front' AND tm.status='removed' AND u.active=0 AND u.operation_disabled=1 AND u.email=? AND u.name=?`, email, oldName) != 1 {
				t.Fatal("旧账号历史被修改或复活")
			}
			for _, table := range []string{"project_members", "department_memberships", "organization_group_members", "user_wecom_webhooks"} {
				if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND user_id=?`, tenantID, id) != 0 {
					t.Fatalf("新账号继承了 %s", table)
				}
			}
			if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM requirements WHERE assignee_user_id='u_front' OR owner_user_id='u_front'`) != before {
				t.Fatal("历史工作项被改绑")
			}
			r := httptest.NewRequest("GET", "/api/session", nil)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			a.scopedAPI().ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatalf("旧会话复活: %d", w.Code)
			}
			w, _ = loginRequest(a, email, seedPassword)
			if w.Code != 401 {
				t.Fatalf("旧密码仍可登录: %d", w.Code)
			}
			w, _ = loginRequest(a, email, "NewMemberOnly123!")
			if w.Code != 200 || jsonMap(t, w)["user"].(map[string]any)["id"] != id {
				t.Fatalf("新密码未登录新账号: %d %s", w.Code, w.Body.String())
			}
			orgRequest(t, a, "POST", "/api/organization/members", "u_admin", body, 409)
			orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"active": true}, 404)
		})
	}
}

func TestRemovedEmailReuseInImportAndProfile(t *testing.T) {
	a := testApp(t)
	dep := orgDepartment(t, a, "重建测试部门", "RECREATE", nil)
	id := orgCreateMember(t, a, "历史成员", "reuse-import@example.test", dep)
	orgRequest(t, a, "DELETE", "/api/organization/members/"+id, "u_admin", nil, 200)
	preview := orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email,departmentCode\n新导入成员,REUSE-IMPORT@example.test,RECREATE\n"}, 200)
	if preview["canCommit"] != true {
		t.Fatalf("已删除邮箱应通过导入预览: %v", preview)
	}
	orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 201)
	if bulkFixtureCount(t, a, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id WHERE lower(u.email)='reuse-import@example.test' AND tm.status!='removed'`) != 1 {
		t.Fatal("导入未创建独立账号")
	}
	// 即便新账号尚未激活，邮箱也不能再次分配。
	orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "重复", "email": "reuse-import@example.test", "initialPassword": "AnotherSafe123!"}, 409)
	profileID := orgCreateMember(t, a, "历史资料邮箱", "reuse-profile@example.test", dep)
	orgRequest(t, a, "DELETE", "/api/organization/members/"+profileID, "u_admin", nil, 200)
	body := `{"name":"资料测试成员","email":"reuse-profile@example.test","phone":"","jobTitle":"","bio":"","avatarColor":"#665FE8","locale":"zh-CN","timezone":"Asia/Shanghai","emailNotifications":true}`
	w := apiRequest(a, "PATCH", "/api/profile", "u_viewer", projectID, body)
	if w.Code != 200 {
		t.Fatalf("个人资料未释放已删除邮箱: %d %s", w.Code, w.Body.String())
	}
	orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "重复资料", "email": "reuse-profile@example.test", "initialPassword": "AnotherSafe123!"}, 409)
}

func TestOrganizationEmailReservationConservativeAndTenantScoped(t *testing.T) {
	a := testApp(t)
	for _, status := range []string{"active", "disabled", "removed"} {
		bulkFixtureExec(t, a, `UPDATE tenant_memberships SET status=? WHERE tenant_id=? AND user_id='u_front'`, status, tenantID)
		var email string
		if err := a.db.QueryRow(`SELECT email FROM users WHERE id='u_front'`).Scan(&email); err != nil {
			t.Fatal(err)
		}
		used, err := organizationEmailInUse(context.Background(), a.db, " "+strings.ToUpper(email)+" ", "")
		if err != nil || used != (status != "removed") {
			t.Fatalf("邮箱状态 %s: %v %v", status, used, err)
		}
		used, err = organizationEmailInUse(context.Background(), a.db, email, "u_front")
		if err != nil || used {
			t.Fatal("本人邮箱不应冲突")
		}
	}
	bulkFixtureExec(t, a, `INSERT INTO users(id,tenant_id,name,email)VALUES('email-orphan',?,'关系缺失','orphan@example.test')`, tenantID)
	used, err := organizationEmailInUse(context.Background(), a.db, "orphan@example.test", "")
	if err != nil || !used {
		t.Fatal("不能把关系缺失当成已删除")
	}
	bulkFixtureExec(t, a, `INSERT INTO tenants(id,name)VALUES('email-other','Other'); INSERT INTO users(id,tenant_id,name,email)VALUES('email-foreign','email-other','Other','foreign-email@example.test')`)
	used, err = organizationEmailInUse(context.Background(), a.db, "foreign-email@example.test", "")
	if err != nil || used {
		t.Fatal("其他企业不应占用当前企业邮箱")
	}
}
