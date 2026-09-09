package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func lifecycleRequest(t *testing.T, a *App, method, path, user, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := apiRequest(a, method, path, user, "missing-selected-project", body)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func lifecycleExec(t *testing.T, a *App, query string, args ...any) {
	t.Helper()
	if _, err := a.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func lifecycleStatus(t *testing.T, a *App, id string) string {
	t.Helper()
	var status string
	if err := a.db.QueryRow(`SELECT status FROM projects WHERE tenant_id=? AND id=?`, tenantID, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}
func lifecycleCode(t *testing.T, a *App, id string) string {
	t.Helper()
	var code string
	if err := a.db.QueryRow(`SELECT code FROM projects WHERE tenant_id=? AND id=?`, tenantID, id).Scan(&code); err != nil {
		t.Fatal(err)
	}
	return code
}
func lifecycleDelete(t *testing.T, a *App, id, user string, status int) *httptest.ResponseRecorder {
	return lifecycleRequest(t, a, "DELETE", "/api/projects/"+id, user, jsonText(map[string]string{"confirmCode": lifecycleCode(t, a, id)}), status)
}

func TestProjectLifecycleVisibilityAndScopedManagement(t *testing.T) {
	a := testApp(t)
	lifecycleExec(t, a, `UPDATE project_members SET role='project_admin' WHERE tenant_id=? AND project_id=? AND user_id='u_pm'`, tenantID, projectID)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/archive", "u_front", "{}", 403)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/archive", "u_pm", "{}", 200)
	for _, user := range []string{"u_admin", "u_pm", "u_front", "u_viewer"} {
		w := lifecycleRequest(t, a, "GET", "/api/projects?status=archived", user, "", 200)
		out := jsonMap(t, w)
		if out["canViewArchived"] != (user == "u_admin") {
			t.Fatal("incorrect archived capability")
		}
		items := out["items"].([]any)
		if user != "u_admin" {
			if len(items) != 0 {
				t.Fatal("ordinary member can see archived project")
			}
			continue
		}
		if len(items) != 1 {
			t.Fatalf("archived directory: %s", w.Body.String())
		}
		item := items[0].(map[string]any)
		if item["canManage"] != false || item["canRestore"] != true || item["canDelete"] != true {
			t.Fatal("bad lifecycle controls")
		}
	}
	for _, action := range []string{"restore", "archive"} {
		lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/"+action, "u_front", "{}", 403)
	}
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/restore", "u_pm", "{}", 403)
	lifecycleDelete(t, a, projectID, "u_pm", 403)
	lifecycleRequest(t, a, "GET", "/api/projects/"+projectID, "u_pm", "", 403)
	lifecycleRequest(t, a, "PATCH", "/api/projects/"+projectID, "u_admin", `{"status":"active","name":"must not change"}`, 409)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/visit", "u_admin", "{}", 409)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/restore", "u_admin", "{}", 200)
	if lifecycleStatus(t, a, projectID) != "active" {
		t.Fatal("restore did not activate")
	}
	w := apiRequest(a, "GET", "/api/requirements", "u_front", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestProjectLifecycleDeleteConfirmationTombstoneAndHistory(t *testing.T) {
	a := testApp(t)
	id := projectID
	path := "/api/projects/" + id
	counts := map[string]int{}
	for _, table := range []string{"requirements", "defects", "sprints", "test_cases", "test_plans", "project_members", "memberships", "attachments", "comments"} {
		var n int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=?`, tenantID, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[table] = n
	}
	lifecycleDelete(t, a, id, "u_admin", 409)
	lifecycleRequest(t, a, "POST", path+"/archive", "u_admin", "{}", 200)
	code := lifecycleCode(t, a, id)
	for _, body := range []string{`{}`, `{"confirmCode":"wrong"}`, jsonText(map[string]string{"confirmCode": " " + code}), jsonText(map[string]string{"confirmCode": strings.ToLower(code)}), `{"confirmCode":4}`} {
		lifecycleRequest(t, a, "DELETE", path, "u_admin", body, 422)
	}
	w := lifecycleDelete(t, a, id, "u_admin", 200)
	if jsonMap(t, w)["deleted"] != true {
		t.Fatal("missing deletion response")
	}
	w = lifecycleDelete(t, a, id, "u_admin", 200)
	if jsonMap(t, w)["alreadyDeleted"] != true {
		t.Fatal("same deletion must be safe to retry")
	}
	lifecycleRequest(t, a, "POST", path+"/restore", "u_admin", "{}", 409)
	lifecycleRequest(t, a, "POST", path+"/archive", "u_admin", "{}", 409)
	lifecycleRequest(t, a, "PATCH", path, "u_admin", `{"status":"active"}`, 409)
	lifecycleRequest(t, a, "GET", path, "u_admin", "", 404)
	lifecycleRequest(t, a, "GET", path+"/summary", "u_admin", "", 404)
	lifecycleRequest(t, a, "POST", "/api/projects", "u_admin", jsonText(map[string]string{"name": "Do not resurrect", "code": code}), 409)
	for table, before := range counts {
		var after int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=?`, tenantID, id).Scan(&after); err != nil || before != after {
			t.Fatalf("history removed in %s: %d -> %d, %v", table, before, after, err)
		}
	}
	var audits int
	lifecycleExec(t, a, `UPDATE projects SET name=name WHERE id=?`, id)
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND project_id=? AND action='project.delete'`, tenantID, id).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("delete audit = %d %v", audits, err)
	}
}

func TestProjectLifecycleUnavailableBusinessAndCrossTenant(t *testing.T) {
	a := testApp(t)
	lifecycleExec(t, a, `INSERT INTO tenants(id,name)VALUES('life_foreign','Foreign');INSERT INTO projects(id,tenant_id,name,code,status)VALUES('life_foreign_project','life_foreign','Foreign','FOREIGN','archived')`)
	for _, action := range []string{"restore", "archive"} {
		lifecycleRequest(t, a, "POST", "/api/projects/life_foreign_project/"+action, "u_admin", "{}", 404)
	}
	lifecycleRequest(t, a, "DELETE", "/api/projects/life_foreign_project", "u_admin", `{"confirmCode":"FOREIGN"}`, 404)
	lifecycleRequest(t, a, "GET", "/api/projects/life_foreign_project", "u_admin", "", 404)
	lifecycleExec(t, a, `UPDATE users SET operation_disabled=1 WHERE id='u_admin'`)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/archive", "u_admin", "{}", 403)
	if lifecycleStatus(t, a, projectID) != "active" {
		t.Fatal("disabled account changed project")
	}
	lifecycleExec(t, a, `UPDATE users SET operation_disabled=0,must_change_password=1 WHERE id='u_admin'`)
	lifecycleRequest(t, a, "GET", "/api/projects", "u_admin", "", 403)
}

func TestProjectLifecycleAuditAndPermissionRecheckRollback(t *testing.T) {
	for _, action := range []string{"archive", "restore", "delete", "update", "create"} {
		t.Run(action, func(t *testing.T) {
			a := testApp(t)
			if action == "restore" || action == "delete" {
				lifecycleExec(t, a, `UPDATE projects SET status='archived',archived_at='prior' WHERE id=?`, projectID)
			}
			before := lifecycleStatus(t, a, projectID)
			name := ""
			a.db.QueryRow(`SELECT name FROM projects WHERE id=?`, projectID).Scan(&name)
			lifecycleExec(t, a, `CREATE TRIGGER fail_lifecycle_audit BEFORE INSERT ON audit_logs WHEN NEW.object_type='project' BEGIN SELECT RAISE(ABORT,'private storage failure');END`)
			switch action {
			case "delete":
				lifecycleDelete(t, a, projectID, "u_admin", 503)
			case "update":
				lifecycleRequest(t, a, "PATCH", "/api/projects/"+projectID, "u_admin", `{"name":"Never persisted"}`, 503)
			case "create":
				lifecycleRequest(t, a, "POST", "/api/projects", "u_admin", `{"name":"Never created","code":"ROLL"}`, 503)
			default:
				lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/"+action, "u_admin", "{}", 503)
			}
			if lifecycleStatus(t, a, projectID) != before {
				t.Fatal("status changed despite audit failure")
			}
			var count int
			a.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE code='ROLL'`).Scan(&count)
			if count != 0 {
				t.Fatal("partial create")
			}
			var after string
			a.db.QueryRow(`SELECT name FROM projects WHERE id=?`, projectID).Scan(&after)
			if after != name {
				t.Fatal("partial edit")
			}
		})
	}
	t.Run("role revoked after acquiring lock", func(t *testing.T) {
		a := testApp(t)
		lifecycleExec(t, a, `UPDATE project_members SET role='project_admin' WHERE user_id='u_pm' AND project_id=?`, projectID)
		lifecycleExec(t, a, `CREATE TRIGGER revoke_lifecycle_role AFTER UPDATE ON organization_write_locks BEGIN UPDATE project_members SET role='viewer' WHERE user_id='u_pm';END`)
		lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/archive", "u_pm", "{}", 403)
		if lifecycleStatus(t, a, projectID) != "active" {
			t.Fatal("stale project-admin grant")
		}
	})
}

func TestProjectLifecycleConcurrentRestoreDeleteHasOneWinner(t *testing.T) {
	a := testApp(t)
	authenticated := administrationSessionFixture(t, a, "u_admin")
	a.db.SetMaxOpenConns(1)
	lifecycleExec(t, a, `UPDATE projects SET status='archived' WHERE id=?`, projectID)
	code := lifecycleCode(t, a, projectID)
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"restore", "delete"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			<-start
			scoped := authenticated
			method, path, body := "POST", "/api/projects/"+projectID+"/restore", "{}"
			if action == "delete" {
				method, path, body = "DELETE", "/api/projects/"+projectID, jsonText(map[string]string{"confirmCode": code})
			}
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			scoped.projectResource(w, r)
			results <- w.Code
		}(action)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for status := range results {
		if status == 200 {
			ok++
		} else if status == 409 {
			conflict++
		} else {
			t.Fatalf("unexpected concurrent status %d", status)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("both transitions succeeded")
	}
	var audits int
	a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE object_type='project' AND action IN ('project.restore','project.delete')`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit count %d", audits)
	}
}

func TestProjectLifecycleNoActiveProjectAdminSessionAndRecovery(t *testing.T) {
	a := testApp(t)
	lifecycleExec(t, a, `UPDATE projects SET status='archived' WHERE tenant_id=?`, tenantID)
	w := lifecycleRequest(t, a, "GET", "/api/session", "u_admin", "", 200)
	out := jsonMap(t, w)
	if out["project"].(map[string]any)["id"] != "" || out["user"].(map[string]any)["mustChangePassword"] != false || len(out["organizationPermissions"].([]any)) == 0 {
		t.Fatal("empty-project session lost identity/capability")
	}
	lifecycleRequest(t, a, "GET", "/api/session", "u_front", "", 403)
	lifecycleRequest(t, a, "GET", "/api/projects?status=archived", "u_admin", "", 200)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/restore", "u_admin", "{}", 200)
	w = apiRequest(a, "GET", "/api/session", "u_admin", projectID, "")
	if w.Code != 200 || jsonMap(t, w)["project"].(map[string]any)["id"] != projectID {
		t.Fatal("restored project session unavailable")
	}
}

func TestProjectLifecycleReadOnlyImpersonationAndStorageFailures(t *testing.T) {
	a := testApp(t)
	for _, method := range []string{"POST", "DELETE", "PATCH"} {
		path, body := "/api/projects/"+projectID, `{"name":"Denied"}`
		if method == "POST" {
			path += "/archive"
			body = `{}`
		}
		if method == "DELETE" {
			body = jsonText(map[string]string{"confirmCode": lifecycleCode(t, a, projectID)})
		}
		lifecycleRequest(t, a, method, path, "u_viewer", body, 403)
	}
	scoped := *a
	scoped.user, scoped.impersonation = "u_admin", &impersonationContext{TargetID: "u_admin", AdminID: "another-admin"}
	r := httptest.NewRequest("POST", "/api/projects/"+projectID+"/archive", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	scoped.projectResource(w, r)
	if w.Code != 403 || lifecycleStatus(t, a, projectID) != "active" {
		t.Fatal("impersonation changed lifecycle")
	}
	// 故障不能返回成功或零摘要，也不能把异常写到审计后留下部分创建。
	lifecycleExec(t, a, `CREATE TRIGGER fail_lifecycle_member BEFORE INSERT ON project_members BEGIN SELECT RAISE(ABORT,'private storage detail');END`)
	w = lifecycleRequest(t, a, "POST", "/api/projects", "u_admin", `{"name":"Rollback memberships","code":"MEMFAIL"}`, 503)
	if bytes.Contains(w.Body.Bytes(), []byte("private storage detail")) {
		t.Fatal("storage error leaked")
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE code='MEMFAIL'`).Scan(&count)
	if count != 0 {
		t.Fatal("membership failure left project")
	}
	lifecycleExec(t, a, `DROP TABLE requirements`)
	lifecycleRequest(t, a, "GET", "/api/projects", "u_admin", "", 503)
}

func TestProjectLifecycleDeletedProjectExcludedFromBusinessPaths(t *testing.T) {
	a := testApp(t)
	var req int64
	if err := a.db.QueryRow(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? LIMIT 1`, tenantID, projectID).Scan(&req); err != nil {
		t.Fatal(err)
	}
	lifecycleExec(t, a, `UPDATE requirements SET title='Lifecycle sentinel requirement',assignee_user_id='u_admin',assignee_user_ids_json='["u_admin"]' WHERE id=?`, req)
	lifecycleExec(t, a, `INSERT INTO requirement_favorites(tenant_id,project_id,user_id,requirement_id,created_at)VALUES(?,?,'u_admin',?,'now')`, tenantID, projectID, req)
	lifecycleExec(t, a, `INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at)VALUES(?,?,'u_admin','u_front','requirement.mentioned','requirement',?,'Lifecycle sentinel notification','secret','now')`, tenantID, projectID, req)
	lifecycleRequest(t, a, "POST", "/api/projects/"+projectID+"/archive", "u_admin", "{}", 200)
	lifecycleDelete(t, a, projectID, "u_admin", 200)
	for _, path := range []string{"/api/requirements", "/api/requirements/" + fmt.Sprint(req), "/api/sprints", "/api/defects", "/api/test-cases", "/api/meta", "/api/notifications/outbox"} {
		w := apiRequest(a, "GET", path, "u_admin", projectID, "")
		if w.Code != 403 {
			t.Fatalf("deleted project served %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/search?q=Lifecycle", "/api/my-work?project=all", "/api/my-work?project=all&view=favorites", "/api/notifications?project=" + projectID, "/api/projects", "/api/organization/admin"} {
		w := apiRequest(a, "GET", path, "u_admin", insightProjectID, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("Lifecycle sentinel")) {
			t.Fatalf("leaked deleted work via %s", path)
		}
		if path == "/api/projects" || path == "/api/organization/admin" {
			if bytes.Contains(w.Body.Bytes(), []byte(`"id":"`+projectID+`"`)) {
				t.Fatalf("leaked project directory: %s", path)
			}
		}
	}
	w := apiRequest(a, "POST", "/api/notifications/read-all", "u_admin", insightProjectID, `{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var unread int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE project_id=? AND title='Lifecycle sentinel notification' AND read_at IS NULL`, projectID).Scan(&unread)
	if unread != 1 {
		t.Fatal("hidden notification mutated")
	}
}

func TestProjectLifecycleDeletedReportsAndPreservedMemberships(t *testing.T) {
	a := testApp(t)
	workloadSprintFixture(t, a, projectID, "Lifecycle report", "2051-02-28")
	workloadRequirementFixture(t, a, projectID, "Lifecycle report", "已上线", `{"frontend":{"userId":"u_front","value":50}}`)
	before, w := workloadRead(t, a, "2051-02")
	if w.Code != 200 || before.Totals.Weight != 50 {
		t.Fatal("bad report fixture")
	}
	lifecycleExec(t, a, `UPDATE projects SET status='archived' WHERE id=?`, projectID)
	archived, w := workloadRead(t, a, "2051-02")
	if w.Code != 200 || archived.Totals.Weight != 50 {
		t.Fatal("archived company history should remain in reports")
	}
	orgGroup(t, a, "Lifecycle directory only", []string{"organization.read"}, []string{"u_front"})
	directory := orgRequest(t, a, "GET", "/api/organization/members", "u_front", nil, 200)
	directoryJSON, _ := json.Marshal(directory)
	if bytes.Contains(directoryJSON, []byte(`"projectId":"`+projectID+`"`)) {
		t.Fatal("ordinary directory reader sees archived membership")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"employeeNo": "keeps-archived"}, 200)
	var retained int
	a.db.QueryRow(`SELECT COUNT(*) FROM project_members WHERE project_id=? AND user_id='u_front'`, projectID).Scan(&retained)
	if retained != 1 {
		t.Fatal("archived membership erased by unrelated edit")
	}
	lifecycleDelete(t, a, projectID, "u_admin", 200)
	after, w := workloadRead(t, a, "2051-02")
	if w.Code != 200 || after.Totals.Weight != 0 || after.SprintCount != 0 {
		t.Fatal("deleted project in report")
	}
	trends, tw := readWorkloadTrends(t, a, "month=2051-02")
	if tw.Code != 200 || len(trends.Iterations) != 0 {
		t.Fatal("deleted project in trends")
	}
	out := orgRequest(t, a, "GET", "/api/organization/members", "u_admin", nil, 200)
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte(`"projectId":"`+projectID+`"`)) {
		t.Fatal("deleted membership in directory")
	}
	orgRequest(t, a, "PATCH", "/api/organization/members/u_front", "u_admin", map[string]any{"name": "Preserved historical membership"}, 200)
	for _, table := range []string{"project_members", "memberships"} {
		var count int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("hidden relation destroyed: %s %d %v", table, count, err)
		}
	}
}

func TestProjectLifecycleWecomDoesNotDeliverOrExposeDeletedWork(t *testing.T) {
	a := wecomApp(t)
	setTestWebhook(t, a)
	id := insertWecomNotice(t, a)
	lifecycleExec(t, a, `UPDATE projects SET status='deleted' WHERE id=?`, projectID)
	t.Setenv("DEVFLOW_WECOM_MODE", "live")
	calls := 0
	a.wecomHTTP = &http.Client{Transport: wecomRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("must not send") })}
	if err := a.processUserWecom(context.Background(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || deliveryStatus(t, a, id) != "skipped" {
		t.Fatal("deleted project notification delivered")
	}
	out := orgRequest(t, a, "GET", "/api/profile/wecom-webhook", "u_front", nil, 200)
	if len(out["deliveries"].([]any)) != 0 {
		t.Fatal("deleted notification exposed in delivery history")
	}
}
