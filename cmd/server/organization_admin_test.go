package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func orgRequest(t *testing.T, a *App, method, path, user string, body any, status int) map[string]any {
	t.Helper()
	text := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = string(raw)
	}
	w := apiRequest(a, method, path, user, "unrelated-project", text)
	if w.Code != status {
		t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, w.Code, status, w.Body.String())
	}
	return jsonMap(t, w)
}
func orgGroup(t *testing.T, a *App, name string, permissions, members []string) string {
	t.Helper()
	return orgRequest(t, a, "POST", "/api/organization/groups", "u_admin", map[string]any{"name": name, "permissions": permissions, "memberIds": members}, 201)["id"].(string)
}
func orgDepartment(t *testing.T, a *App, name, code string, parent any) string {
	t.Helper()
	return orgRequest(t, a, "POST", "/api/organization/departments", "u_admin", map[string]any{"name": name, "code": code, "parentId": parent}, 201)["id"].(string)
}
func orgCreateMember(t *testing.T, a *App, name, email, dep string) string {
	t.Helper()
	return orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": name, "email": email, "initialPassword": "SafeInitial123!", "departmentIds": []string{dep}, "primaryDepartmentId": dep, "projectMemberships": []any{map[string]string{"projectId": projectID, "role": "frontend"}}}, 201)["id"].(string)
}
func orgPublic(t *testing.T, a *App, method, token string, body any, status int) map[string]any {
	t.Helper()
	value := ""
	if body != nil {
		raw, _ := json.Marshal(body)
		value = string(raw)
	}
	r := httptest.NewRequest(method, "/api/public/organization-invitations/"+token, strings.NewReader(value))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("public %s: got %d want %d: %s", method, w.Code, status, w.Body.String())
	}
	return jsonMap(t, w)
}
func orgInvitation(t *testing.T, a *App, dep string, maxUses int) map[string]any {
	t.Helper()
	return orgRequest(t, a, "POST", "/api/organization/invitations", "u_admin", map[string]any{"name": "团队加入", "expiresInHours": 24, "maxUses": maxUses, "departmentIds": []string{dep}, "allowedRoles": []string{"frontend", "qa"}}, 201)
}

func TestOrganizationPermissionsIgnoreSelectedProjectAndApplyImmediately(t *testing.T) {
	a := testApp(t)
	orgRequest(t, a, "GET", "/api/organization/admin", "u_admin", nil, 200)
	orgRequest(t, a, "GET", "/api/organization/admin", "u_viewer", nil, 403)
	id := orgGroup(t, a, "目录维护", []string{"departments.manage"}, []string{"u_viewer"})
	orgRequest(t, a, "GET", "/api/organization/admin", "u_viewer", nil, 200)
	orgRequest(t, a, "POST", "/api/organization/departments", "u_viewer", map[string]any{"name": "授权部门", "code": "DELEGATED"}, 201)
	orgRequest(t, a, "GET", "/api/organization/members", "u_viewer", nil, 200)
	orgRequest(t, a, "POST", "/api/organization/groups", "u_viewer", map[string]any{"name": "越权", "permissions": []string{"members.manage"}, "memberIds": []string{"u_viewer"}}, 403)
	orgRequest(t, a, "POST", "/api/organization/members", "u_viewer", map[string]any{"name": "越权", "email": "bad@example.com", "initialPassword": "Password123"}, 403)
	orgRequest(t, a, "PATCH", "/api/organization/groups/"+id, "u_admin", map[string]any{"memberIds": []string{}}, 200)
	orgRequest(t, a, "POST", "/api/organization/departments", "u_viewer", map[string]any{"name": "权限已撤回", "code": "REVOKED"}, 403)
	orgRequest(t, a, "POST", "/api/organization/groups", "u_admin", map[string]any{"name": "不可委派", "permissions": []string{"groups.manage"}, "memberIds": []string{"u_viewer"}}, 422)
	orgRequest(t, a, "POST", "/api/organization/groups", "u_admin", map[string]any{"name": "未知权限", "permissions": []string{"billing.admin"}, "memberIds": []string{}}, 422)
}
func TestOrganizationDepartmentsHierarchyAndAssignmentsAreCanonical(t *testing.T) {
	a := testApp(t)
	parent := orgDepartment(t, a, "新中心", "NEWCENTER", nil)
	child := orgDepartment(t, a, "研发分部", "SUB", parent)
	orgRequest(t, a, "PATCH", "/api/organization/departments/"+parent, "u_admin", map[string]any{"parentId": child}, 422)
	orgRequest(t, a, "DELETE", "/api/organization/departments/"+parent, "u_admin", nil, 409)
	uid := orgCreateMember(t, a, "新成员", "new-department@example.com", child)
	orgRequest(t, a, "DELETE", "/api/organization/departments/"+child, "u_admin", nil, 409)
	orgRequest(t, a, "PATCH", "/api/organization/departments/"+child, "u_admin", map[string]any{"name": "已更名分部"}, 200)
	var snapshot string
	if err := a.db.QueryRow(`SELECT department FROM users WHERE id=?`, uid).Scan(&snapshot); err != nil || snapshot != "已更名分部" {
		t.Fatalf("snapshot %q err=%v", snapshot, err)
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/"+uid, "u_admin", map[string]any{"departmentIds": []string{}, "primaryDepartmentId": ""}, 200)
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM department_memberships WHERE user_id=?`, uid).Scan(&count)
	a.db.QueryRow(`SELECT department FROM users WHERE id=?`, uid).Scan(&snapshot)
	if count != 0 || snapshot != "" {
		t.Fatalf("stale department: %d %q", count, snapshot)
	}
	orgRequest(t, a, "DELETE", "/api/organization/departments/"+child, "u_admin", nil, 200)
	orgRequest(t, a, "DELETE", "/api/organization/departments/"+parent, "u_admin", nil, 200)
}
func TestOrganizationMemberSecurityAndLegacyEscalation(t *testing.T) {
	a := testApp(t)
	var storedRole string
	if err := a.db.QueryRow(`SELECT role FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_admin'`, tenantID, projectID).Scan(&storedRole); err != nil {
		t.Fatal(err)
	}
	adminMembers, err := organizationMemberList(t.Context(), a.db, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_admin", map[string]any{"projectMemberships": adminMembers[0].ProjectMemberships}, 200)
	var preservedRole string
	a.db.QueryRow(`SELECT role FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_admin'`, tenantID, projectID).Scan(&preservedRole)
	if preservedRole != storedRole {
		t.Fatalf("legacy admin project role was rewritten: %s -> %s", storedRole, preservedRole)
	}
	orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "非法项目管理员角色", "email": "bad-project-role@example.com", "active": false, "projectMemberships": []any{map[string]string{"projectId": projectID, "role": "tenant_admin"}}}, 422)
	if _, err := a.db.Exec(`UPDATE memberships SET role='project_admin' WHERE user_id='u_pm'; UPDATE project_members SET role='project_admin' WHERE user_id='u_pm'`); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"id":"u_front","tenantRole":"tenant_admin"}`, `{"id":"u_admin","active":false}`, `{"id":"u_front","initialPassword":"Attack123!"}`} {
		w := apiRequest(a, "PATCH", "/api/members", "u_pm", projectID, body)
		if w.Code != 403 {
			t.Fatalf("legacy escalation: %d %s", w.Code, w.Body.String())
		}
	}
	w := apiRequest(a, "POST", "/api/members", "u_pm", projectID, `{"name":"越权管理员","email":"escalate@example.com","tenantRole":"tenant_admin","initialPassword":"Attack123!"}`)
	if w.Code != 403 {
		t.Fatalf("legacy create: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", "/api/members", "u_pm", projectID, `{"id":"u_front","projectRole":"backend"}`)
	if w.Code != 200 {
		t.Fatalf("project role edit: %d %s", w.Code, w.Body.String())
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_admin", map[string]any{"active": false}, 409)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_admin", map[string]any{"tenantRole": "member"}, 409)
	orgGroup(t, a, "成员维护", []string{"members.manage"}, []string{"u_viewer"})
	orgRequest(t, a, "PATCH", "/api/organization/members/u_admin", "u_viewer", map[string]any{"email": "takeover@example.com"}, 403)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_viewer", map[string]any{"tenantRole": "tenant_admin"}, 403)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_viewer", map[string]any{"projectMemberships": []any{map[string]string{"projectId": projectID, "role": "project_admin"}}}, 403)
	orgGroup(t, a, "导出员", []string{"members.export"}, []string{"u_back"})
	orgRequest(t, a, "PATCH", "/api/organization/members/u_back", "u_viewer", map[string]any{"initialPassword": "Takeover123"}, 403)
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_viewer", map[string]any{"name": "授权修改"}, 200)
}

func TestOrganizationMemberDeletionIsAdminOnlyAndSafelyRevokesMembership(t *testing.T) {
	a := testApp(t)
	// A delegated member manager must not obtain the irreversible deletion capability.
	orgGroup(t, a, "成员维护", []string{"members.manage"}, []string{"u_back"})
	orgRequest(t, a, "DELETE", "/api/organization/members/u_front", "u_back", nil, 403)
	var membershipStatus string
	if err := a.db.QueryRow(`SELECT status FROM tenant_memberships WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&membershipStatus); err != nil || membershipStatus != "active" {
		t.Fatalf("delegated deletion changed membership: %q %v", membershipStatus, err)
	}
	self := orgRequest(t, a, "DELETE", "/api/organization/members/u_admin", "u_admin", nil, 409)
	if self["error"].(map[string]any)["code"] != "cannot_delete_self" {
		t.Fatalf("self deletion returned wrong error: %v", self)
	}

	// Seed every live relationship that removal is required to revoke. The user
	// record itself must remain for historical work-item and audit references.
	if _, err := a.db.Exec(`INSERT INTO user_wecom_webhooks(tenant_id,user_id,encrypted_url,enabled,version,updated_at)VALUES(?,?,X'01',1,1,?)`, tenantID, "u_front", orgNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), "u_front"); err != nil {
		t.Fatal(err)
	}
	var email string
	if err := a.db.QueryRow(`SELECT email FROM users WHERE tenant_id=? AND id='u_front'`, tenantID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	result := orgRequest(t, a, "DELETE", "/api/organization/members/u_front", "u_admin", nil, 200)
	if result["deleted"] != true {
		t.Fatalf("unexpected deletion result: %v", result)
	}
	var active, operationDisabled bool
	if err := a.db.QueryRow(`SELECT active,operation_disabled FROM users WHERE tenant_id=? AND id='u_front'`, tenantID).Scan(&active, &operationDisabled); err != nil || active || !operationDisabled {
		t.Fatalf("removed account state active=%v disabled=%v err=%v", active, operationDisabled, err)
	}
	if err := a.db.QueryRow(`SELECT status FROM tenant_memberships WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&membershipStatus); err != nil || membershipStatus != "removed" {
		t.Fatalf("member relation was not softly removed: %q %v", membershipStatus, err)
	}
	for _, table := range []string{"department_memberships", "organization_group_members", "memberships", "project_members", "user_wecom_webhooks"} {
		var count int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("live %s relation survived removal: %d %v", table, count, err)
		}
	}
	var sessions, audits, retainedUsers int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE tenant_id=? AND user_id='u_front' AND revoked_at IS NULL`, tenantID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("member sessions were not revoked: %d %v", sessions, err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE tenant_id=? AND id='u_front'`, tenantID).Scan(&retainedUsers); err != nil || retainedUsers != 1 {
		t.Fatalf("historical user was hard deleted: %d %v", retainedUsers, err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND object_id='u_front' AND action='organization_member_removed'`, tenantID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("member deletion audit missing: %d %v", audits, err)
	}
	items, err := organizationMemberList(t.Context(), a.db, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "u_front" {
			t.Fatal("removed account remained in organization member list")
		}
	}
	overview := orgRequest(t, a, "GET", "/api/organization/admin", "u_admin", nil, 200)
	if overview["counts"].(map[string]any)["members"].(float64) != float64(len(items)) {
		t.Fatalf("overview member count includes removed accounts: %v", overview["counts"])
	}
	if login, cookie := loginRequest(a, email, seedPassword); login.Code != http.StatusUnauthorized || cookie != nil {
		t.Fatalf("removed member can still sign in: %d %s", login.Code, login.Body.String())
	}
}

func TestOrganizationMemberDeletionRollsBackWhenAuditFails(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`CREATE TRIGGER block_member_remove_audit BEFORE INSERT ON audit_logs WHEN NEW.action='organization_member_removed' BEGIN SELECT RAISE(ABORT,'test audit failure');END`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "DELETE", "/api/organization/members/u_front", "u_admin", nil, 503)
	var status string
	var active bool
	if err := a.db.QueryRow(`SELECT tm.status,u.active FROM tenant_memberships tm JOIN users u ON u.tenant_id=tm.tenant_id AND u.id=tm.user_id WHERE tm.tenant_id=? AND tm.user_id='u_front'`, tenantID).Scan(&status, &active); err != nil || status != "active" || !active {
		t.Fatalf("failed deletion left a partial account mutation: status=%q active=%v err=%v", status, active, err)
	}
	var memberships int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM project_members WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&memberships); err != nil || memberships == 0 {
		t.Fatalf("failed deletion removed live project membership: %d %v", memberships, err)
	}
}
func TestOrganizationWritesAreScopedAndAuditFailuresRollback(t *testing.T) {
	a := testApp(t)
	now := orgNow()
	_, err := a.db.Exec(`INSERT INTO tenants(id,name)VALUES('foreign','Other');INSERT INTO departments(id,tenant_id,name,code,source,external_id,status,created_at,updated_at)VALUES('foreign-dept','foreign','Foreign','FOREIGN','local','foreign','active',?,?);INSERT INTO users(id,tenant_id,name,email)VALUES('foreign-user','foreign','Foreign','foreign@example.com');INSERT INTO tenant_memberships VALUES('foreign','foreign-user','member','active',?,?)`, now, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "PATCH", "/api/organization/departments/foreign-dept", "u_admin", map[string]any{"name": "跨企业"}, 404)
	orgRequest(t, a, "POST", "/api/organization/departments", "u_admin", map[string]any{"name": "跨企业父级", "parentId": "foreign-dept"}, 422)
	orgRequest(t, a, "POST", "/api/organization/groups", "u_admin", map[string]any{"name": "跨企业成员", "memberIds": []string{"foreign-user"}, "permissions": []string{"organization.read"}}, 422)
	orgRequest(t, a, "PATCH", "/api/organization/members/foreign-user", "u_admin", map[string]any{"name": "跨企业"}, 404)
	if _, err = a.db.Exec(`CREATE TRIGGER block_org_audit BEFORE INSERT ON audit_logs WHEN NEW.action='organization_member_saved' BEGIN SELECT RAISE(ABORT,'test audit failure');END`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "POST", "/api/organization/members", "u_admin", map[string]any{"name": "回滚", "email": "rollback@example.com", "initialPassword": "Password123", "departmentIds": []string{"dept_frontend"}}, 503)
	var count int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email='rollback@example.com'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial member inserted: %d %v", count, err)
	}
}
func TestOrganizationInvitationPublicMinimalApplicationAndApproval(t *testing.T) {
	a := testApp(t)
	invite := orgInvitation(t, a, "dept_frontend", 10)
	token := invite["token"].(string)
	public := orgPublic(t, a, "GET", token, nil, 200)
	if len(public) != 3 || public["organizationName"] == nil {
		t.Fatalf("public overexposure: %v", public)
	}
	var stored string
	a.db.QueryRow(`SELECT token_hash FROM organization_invitations WHERE id=?`, invite["id"]).Scan(&stored)
	if stored == token || stored != organizationTokenHash(token) {
		t.Fatal("raw token stored")
	}
	private := orgRequest(t, a, "GET", "/api/organization/invitations", "u_admin", nil, 200)
	if strings.Contains(jsonText(private), token) || strings.Contains(jsonText(private), stored) {
		t.Fatal("list exposed token/hash")
	}
	var before int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&before)
	payload := map[string]any{"name": "待审批同事", "departmentId": "dept_frontend", "requestedRole": "frontend"}
	orgPublic(t, a, "POST", token, payload, 202)
	orgPublic(t, a, "POST", token, payload, 202)
	var users, applications, uses int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users)
	a.db.QueryRow(`SELECT COUNT(*) FROM organization_applications`).Scan(&applications)
	a.db.QueryRow(`SELECT uses FROM organization_invitations WHERE id=?`, invite["id"]).Scan(&uses)
	if users != before || applications != 1 || uses != 1 {
		t.Fatalf("submission improperly activated/duplicated: users=%d apps=%d uses=%d", users, applications, uses)
	}
	app := orgRequest(t, a, "GET", "/api/organization/applications", "u_admin", nil, 200)["items"].([]any)[0].(map[string]any)
	orgRequest(t, a, "POST", "/api/organization/applications/"+app["id"].(string)+"/approve", "u_admin", map[string]any{"email": "approved@example.com", "initialPassword": "Approved123"}, 422)
	result := orgRequest(t, a, "POST", "/api/organization/applications/"+app["id"].(string)+"/approve", "u_admin", map[string]any{"email": "approved@example.com", "initialPassword": "Approved123", "projectMemberships": []any{map[string]string{"projectId": projectID, "role": "qa"}}}, 201)
	uid := result["userId"].(string)
	var role, hash string
	var active bool
	a.db.QueryRow(`SELECT active,password_hash FROM users WHERE id=?`, uid).Scan(&active, &hash)
	a.db.QueryRow(`SELECT role FROM project_members WHERE user_id=? AND project_id=?`, uid, projectID).Scan(&role)
	if !active || !verifyPassword("Approved123", hash) || role != "qa" {
		t.Fatal("approval did not explicitly configure account/role")
	}
	orgRequest(t, a, "POST", "/api/organization/applications/"+app["id"].(string)+"/reject", "u_admin", map[string]any{"reason": "重复审批"}, 409)
	var leaked int
	a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE after_json LIKE '%Approved123%' OR before_json LIKE '%Approved123%' OR after_json LIKE ?`, "%"+token+"%").Scan(&leaked)
	if leaked != 0 {
		t.Fatal("audit leaked credentials")
	}
}
func TestOrganizationInvitationRevokeExpiryCapsRateLimitAndRejection(t *testing.T) {
	a := testApp(t)
	invite := orgInvitation(t, a, "dept_frontend", 1)
	token := invite["token"].(string)
	orgPublic(t, a, "POST", token, map[string]any{"name": "普通申请", "departmentId": "dept_frontend", "requestedRole": "project_admin"}, 422)
	orgPublic(t, a, "POST", token, map[string]any{"name": "普通申请", "departmentId": "dept_backend", "requestedRole": "frontend"}, 422)
	orgPublic(t, a, "POST", token, map[string]any{"name": "普通申请", "departmentId": "dept_frontend", "requestedRole": "frontend", "password": "Disallowed123"}, 422)
	orgPublic(t, a, "POST", token, map[string]any{"name": "普通申请", "departmentId": "dept_frontend", "requestedRole": "frontend"}, 202)
	orgPublic(t, a, "GET", token, nil, 410)
	apps := orgRequest(t, a, "GET", "/api/organization/applications", "u_admin", nil, 200)["items"].([]any)
	id := apps[0].(map[string]any)["id"].(string)
	orgRequest(t, a, "POST", "/api/organization/applications/"+id+"/reject", "u_admin", map[string]any{"reason": "信息需重新确认"}, 200)
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE name='普通申请'`).Scan(&count)
	if count != 0 {
		t.Fatal("rejected application created account")
	}
	inv2 := orgInvitation(t, a, "dept_frontend", 100)
	token2 := inv2["token"].(string)
	for i := 0; i < 10; i++ {
		orgPublic(t, a, "POST", token2, map[string]any{"name": "重复请求", "departmentId": "dept_frontend", "requestedRole": "frontend"}, 202)
	}
	orgPublic(t, a, "POST", token2, map[string]any{"name": "重复请求", "departmentId": "dept_frontend", "requestedRole": "frontend"}, 429)
	orgRequest(t, a, "POST", "/api/organization/invitations/"+inv2["id"].(string)+"/revoke", "u_admin", map[string]any{}, 200)
	orgPublic(t, a, "GET", token2, nil, 410)
	inv3 := orgInvitation(t, a, "dept_frontend", 10)
	a.db.Exec(`UPDATE organization_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), inv3["id"])
	orgPublic(t, a, "GET", inv3["token"].(string), nil, 410)
}

func organizationWriteLockRevision(t *testing.T, a *App) int {
	t.Helper()
	var revision int
	if err := a.db.QueryRow(`SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestPublicInvitationPreflightAvoidsInvalidAndKnownLimitedWriteLocks(t *testing.T) {
	a := testApp(t)
	input := map[string]any{"name": "公开申请", "departmentId": "dept_frontend", "requestedRole": "frontend"}

	// 有效格式但不存在的 token 必须在只读预检被拒绝，不能推进组织写锁或
	// 生成任何限流记录。这里覆盖了匿名流量最容易被滥用的路径。
	before := organizationWriteLockRevision(t, a)
	orgPublic(t, a, "POST", strings.Repeat("0", 64), input, http.StatusNotFound)
	if after := organizationWriteLockRevision(t, a); after != before {
		t.Fatalf("invalid public token acquired organization write lock: before=%d after=%d", before, after)
	}
	var rateRows int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM organization_public_rate_limits`).Scan(&rateRows); err != nil || rateRows != 0 {
		t.Fatalf("invalid token should not create rate-limit rows: rows=%d err=%v", rateRows, err)
	}

	invite := orgInvitation(t, a, "dept_frontend", 10)
	token := invite["token"].(string)
	r := httptest.NewRequest(http.MethodPost, "/api/public/organization-invitations/"+token, strings.NewReader(`{"name":"受限申请","departmentId":"dept_frontend","requestedRole":"frontend"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.41:4567"
	window := organizationPublicRateLimitWindows(r, token, time.Now().Unix())[0]
	if _, err := a.db.Exec(`INSERT INTO organization_public_rate_limits(key_hash,window_start,count)VALUES(?,?,?)`, organizationTokenHash(window.key), window.start, window.max); err != nil {
		t.Fatal(err)
	}
	before = organizationWriteLockRevision(t, a)
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("known limited public request: %d %s headers=%v", w.Code, w.Body.String(), w.Header())
	}
	if after := organizationWriteLockRevision(t, a); after != before {
		t.Fatalf("preflight-limited request acquired organization write lock: before=%d after=%d", before, after)
	}
}

func TestPublicInvitationRechecksAndConditionallyConsumesAfterWriteLock(t *testing.T) {
	a := testApp(t)
	invite := orgInvitation(t, a, "dept_frontend", 1)
	token := invite["token"].(string)
	// 模拟预检完成后另一个事务正好耗尽额度。触发器在本请求获得写锁时
	// 改变 uses，验证事务内重读和条件更新会让申请插入整体回滚。
	// tenantID 是测试固定常量；触发器 DDL 不支持占位参数，不能把旧演示租户
	// 名称写死，否则触发器虽创建成功却没有耗尽当前测试邀请。
	if _, err := a.db.Exec(fmt.Sprintf(`CREATE TRIGGER test_public_invitation_exhaust_after_lock AFTER UPDATE ON organization_write_locks BEGIN UPDATE organization_invitations SET uses=max_uses WHERE tenant_id=%q; END`, tenantID)); err != nil {
		t.Fatal(err)
	}
	orgPublic(t, a, "POST", token, map[string]any{"name": "并发额度申请", "departmentId": "dept_frontend", "requestedRole": "frontend"}, http.StatusGone)
	var applications int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM organization_applications WHERE tenant_id=? AND invitation_id=?`, tenantID, invite["id"].(string)).Scan(&applications); err != nil || applications != 0 {
		t.Fatalf("exhausted invitation created an application: count=%d err=%v", applications, err)
	}
}

func TestOrganizationCSVPreviewCommitRevalidationAndExportSafety(t *testing.T) {
	a := testApp(t)
	dep := orgDepartment(t, a, "导入部门", "IMPORT", nil)
	csvText := "name,email,employeeNo,departmentCode,projectCode,projectRole\n=HYPERLINK(\"\"x\"\"),first-import@example.com,E1,IMPORT,ORBIT,frontend\n"
	// Standard CSV quoting is required: construct a valid cell containing a formula.
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Write([]string{"name", "email", "employeeNo", "departmentCode", "projectCode", "projectRole"})
	writer.Write([]string{"=SUM(1+2)", "first-import@example.com", "E1", "IMPORT", "ORBIT", "frontend"})
	writer.Flush()
	csvText = buf.String()
	preview := orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": csvText}, 200)
	if preview["canCommit"] != true {
		t.Fatalf("preview %v", preview)
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE email='first-import@example.com'`).Scan(&count)
	if count != 0 {
		t.Fatal("preview mutated members")
	}
	orgGroup(t, a, "导入员", []string{"members.import"}, []string{"u_viewer"})
	orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_viewer", map[string]any{"previewId": preview["previewId"]}, 404)
	result := orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 201)
	if result["imported"] != float64(1) {
		t.Fatalf("import %v", result)
	}
	var active bool
	var password, primary string
	a.db.QueryRow(`SELECT active,password_hash FROM users WHERE email='first-import@example.com'`).Scan(&active, &password)
	a.db.QueryRow(`SELECT dm.department_id FROM department_memberships dm JOIN users u ON u.id=dm.user_id WHERE u.email='first-import@example.com' AND dm.is_primary=1`).Scan(&primary)
	if active || password != "" || primary != dep {
		t.Fatal("import must create inactive passwordless canonical department member")
	}
	orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 409)
	w := apiRequest(a, "GET", "/api/organization/members/export", "u_admin", "missing", "")
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "\ufeff") || !strings.Contains(w.Body.String(), "'=SUM(1+2)") || strings.Contains(w.Body.String(), "password_hash") {
		t.Fatalf("unsafe export: %d %s", w.Code, w.Body.String())
	}
	preview = orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email,departmentCode\n第二人,second-import@example.com,IMPORT\n"}, 200)
	orgRequest(t, a, "PATCH", "/api/organization/departments/"+dep, "u_admin", map[string]any{"status": "inactive"}, 200)
	orgRequest(t, a, "POST", "/api/organization/members/import/commit", "u_admin", map[string]any{"previewId": preview["previewId"]}, 422)
	bad := orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email,projectRole\n重复,a@example.com,tenant_admin\n重复,a@example.com,viewer\n"}, 200)
	if bad["canCommit"] != false || len(bad["errors"].([]any)) < 2 {
		t.Fatalf("missing row errors %v", bad)
	}
	orgRequest(t, a, "POST", "/api/organization/members/import/preview", "u_admin", map[string]any{"csv": "name,email,password\nX,x@example.com,Password123\n"}, 422)
}
func TestOrganizationImpersonationAndFailuresFailClosed(t *testing.T) {
	a := testApp(t)
	scoped := *a
	scoped.impersonation = &impersonationContext{AdminID: "u_admin", TargetID: "u_viewer"}
	r := httptest.NewRequest(http.MethodPost, "/api/organization/departments", strings.NewReader(`{"name":"bad","code":"BAD"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	scoped.organizationAdmin(w, r)
	if w.Code != 403 {
		t.Fatalf("impersonation write %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`DROP TABLE organization_group_permissions`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "GET", "/api/organization/admin", "u_viewer", nil, 503)
	if _, err := a.db.Exec(`DROP TABLE departments`); err != nil {
		t.Fatal(err)
	}
	orgRequest(t, a, "GET", "/api/organization/departments", "u_admin", nil, 503)
}
