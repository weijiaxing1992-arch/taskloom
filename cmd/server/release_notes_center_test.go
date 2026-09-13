package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func releaseNotesCenterFixture(t *testing.T, a *App) (int64, int64, int64) {
	t.Helper()
	sprint := releaseContentSprint(t, a, "V10.3 升级日志")
	requirement := releaseContentRequirement(t, a, "V10.3 升级日志", "已完成", "产品需求", "智能体")
	image := releaseContentImage(t, a, requirement, "智能体工作流.png", "design", releaseContentPNG(t))
	// 必须证明“缺陷不进入日志”，因此将同一迭代中的可完成缺陷写成醒目内容。
	defect := releaseContentRequirement(t, a, "V10.3 升级日志", "已完成", "缺陷", "缺陷修复")
	if _, err := a.db.Exec(`UPDATE requirements SET title='DEFECT MUST NOT APPEAR',description='DEFECT BODY MUST NOT APPEAR',acceptance='DEFECT ACCEPTANCE MUST NOT APPEAR' WHERE id=?`, defect); err != nil {
		t.Fatal(err)
	}
	source, hash := readReleaseContent(t, a, sprint)
	entries := releaseContentEntries(source)
	if len(entries) != 1 || entries[0].RequirementIDs[0] != requirement {
		t.Fatalf("source must contain only completed non-defect requirement: %#v", source.Requirements)
	}
	entries[0].Category = "智能体类型"
	entries[0].ImageIDs = []int64{image}
	entries[0].ImageCaptions = map[string]string{fmt.Sprint(image): "智能体配置的真实界面截图"}
	now := "2026-09-10T09:00:00Z"
	if _, err := a.db.Exec(`INSERT INTO release_note_jobs(tenant_id,project_id,sprint_id,actor_id,session_hash,automatic,state,revision,settings_version,source_json,source_hash,created_at,updated_at)VALUES(?,?,?,?,?,0,'draft',4,1,?,?,?,?)`, tenantID, projectID, sprint, "u_admin", "", jsonText(source), hash, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO release_note_drafts(tenant_id,project_id,sprint_id,entries_json,source_json,source_hash,created_by,edited_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, projectID, sprint, jsonText(entries), jsonText(source), hash, "u_admin", "u_admin", now, now); err != nil {
		t.Fatal(err)
	}
	return sprint, requirement, image
}

func releaseNotesCenterGrantReports(t *testing.T, a *App, user string) {
	t.Helper()
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO organization_groups(id,tenant_id,name,description,created_at,updated_at)VALUES('release-note-reporters',?,'Release note reporters','',?,?)`, tenantID, "2026-09-10T09:00:00Z", "2026-09-10T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO organization_group_permissions(tenant_id,group_id,permission)VALUES(?,'release-note-reporters','reports.view')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO organization_group_members(tenant_id,group_id,user_id)VALUES(?,'release-note-reporters',?)`, tenantID, user); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseNotesCenterPersistsFullProjectScopedSnapshot(t *testing.T) {
	a := aiApp(t)
	sprint, requirement, image := releaseNotesCenterFixture(t, a)
	list := apiRequest(a, http.MethodGet, "/api/reports/release-notes?pageSize=20", "u_admin", projectID, "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), aiTestSecret) || strings.Contains(list.Body.String(), "新增客户负责人筛选。") || strings.Contains(list.Body.String(), "DEFECT MUST NOT APPEAR") {
		t.Fatalf("unsafe or incomplete list: %d %s", list.Code, list.Body.String())
	}
	data := jsonMap(t, list)
	items := data["items"].([]any)
	if len(items) != 1 || int64(items[0].(map[string]any)["sprintId"].(float64)) != sprint || items[0].(map[string]any)["requirementCount"] != float64(1) || items[0].(map[string]any)["selectedImageCount"] != float64(1) {
		t.Fatalf("unexpected persistent list: %#v", data)
	}
	detail := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/reports/release-notes/%d", sprint), "u_admin", projectID, "")
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), aiTestSecret) || strings.Contains(detail.Body.String(), "DEFECT MUST NOT APPEAR") {
		t.Fatalf("unsafe detail: %d %s", detail.Code, detail.Body.String())
	}
	body := jsonMap(t, detail)
	if body["versionName"] != "V10.3 升级日志" || body["releaseDate"] == "" || body["snapshot"] != true || len(body["categories"].([]any)) != len(releaseNoteCategories) || !strings.Contains(detail.Body.String(), "新增客户负责人筛选。") || !strings.Contains(detail.Body.String(), "筛选后保留分页条件。") || !strings.Contains(detail.Body.String(), fmt.Sprintf("/api/requirements/%d/attachments/%d", requirement, image)) || !strings.Contains(detail.Body.String(), "智能体配置的真实界面截图") {
		t.Fatalf("detail must retain full requirement and screenshot facts: %s", detail.Body.String())
	}
	for index, category := range releaseNoteCategories {
		if body["categories"].([]any)[index] != category {
			t.Fatalf("category order changed: %#v", body["categories"])
		}
	}
	if bad := apiRequest(a, http.MethodGet, "/api/reports/release-notes?pageSize=20&limit=20", "u_admin", projectID, ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous pagination accepted: %d %s", bad.Code, bad.Body.String())
	}
	matched := apiRequest(a, http.MethodGet, "/api/reports/release-notes?q="+url.QueryEscape("新增客户负责人筛选"), "u_admin", projectID, "")
	if matched.Code != http.StatusOK || jsonMap(t, matched)["total"] != float64(1) {
		t.Fatalf("saved requirement text was not searchable: %d %s", matched.Code, matched.Body.String())
	}
	if literal := apiRequest(a, http.MethodGet, "/api/reports/release-notes?q=%25", "u_admin", projectID, ""); literal.Code != http.StatusOK || jsonMap(t, literal)["total"] != float64(0) {
		t.Fatalf("SQL wildcard must be treated as literal: %d %s", literal.Code, literal.Body.String())
	}
	if bad := apiRequest(a, http.MethodGet, "/api/reports/release-notes?q=a&q=b", "u_admin", projectID, ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("repeated release-note query accepted: %d %s", bad.Code, bad.Body.String())
	}
	if bad := apiRequest(a, http.MethodGet, "/api/reports/release-notes?q="+url.QueryEscape(strings.Repeat("需", 101)), "u_admin", projectID, ""); bad.Code != http.StatusBadRequest {
		t.Fatalf("oversized release-note query accepted: %d %s", bad.Code, bad.Body.String())
	}
}

func TestReleaseNotesCenterRequiresReportsPermissionAndProjectBoundary(t *testing.T) {
	a := testApp(t)
	sprint, _, _ := releaseNotesCenterFixture(t, a)
	if response := apiRequest(a, http.MethodGet, "/api/reports/release-notes", "u_pm", projectID, ""); response.Code != http.StatusForbidden {
		t.Fatalf("project member bypassed reports permission: %d %s", response.Code, response.Body.String())
	}
	releaseNotesCenterGrantReports(t, a, "u_pm")
	if response := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/reports/release-notes/%d", sprint), "u_pm", projectID, ""); response.Code != http.StatusOK {
		t.Fatalf("granted project member cannot read report: %d %s", response.Code, response.Body.String())
	}
	// u_front is deliberately not an insight-project member. A report grant by
	// itself must never turn the current-project header into a cross-project read.
	releaseNotesCenterGrantReports(t, a, "u_front")
	if response := apiRequest(a, http.MethodGet, "/api/reports/release-notes", "u_front", insightProjectID, ""); response.Code != http.StatusForbidden {
		t.Fatalf("foreign project report leaked: %d %s", response.Code, response.Body.String())
	}
}

func TestIntegrationReleaseNotesReadUsesScopeAndSanitizedImages(t *testing.T) {
	a := aiApp(t)
	sprint, _, _ := releaseNotesCenterFixture(t, a)
	withoutScope := integrationAPIToken(t, a)
	if response := integrationAPIRequest(a, withoutScope, http.MethodGet, "/api/open/v1/release-notes", "", "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("release notes scope bypassed: %d %s", response.Code, response.Body.String())
	}
	withScope := integrationAPIToken(t, a, "release-notes:read")
	list := integrationAPIRequest(a, withScope, http.MethodGet, "/api/open/v1/release-notes?page=1&pageSize=20", "", "", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), aiTestSecret) {
		t.Fatalf("external list failed or leaked secret: %d %s", list.Code, list.Body.String())
	}
	if response := integrationAPIRequest(a, withScope, http.MethodGet, "/api/open/v1/release-notes?q="+url.QueryEscape("新增客户负责人筛选"), "", "", ""); response.Code != http.StatusOK || jsonMap(t, response)["total"] != float64(1) {
		t.Fatalf("external snapshot search failed: %d %s", response.Code, response.Body.String())
	}
	detail := integrationAPIRequest(a, withScope, http.MethodGet, fmt.Sprintf("/api/open/v1/release-notes/%d", sprint), "", "", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "新增客户负责人筛选。") || !strings.Contains(detail.Body.String(), "筛选后保留分页条件。") || strings.Contains(detail.Body.String(), "/api/requirements/") || strings.Contains(detail.Body.String(), aiTestSecret) {
		t.Fatalf("external detail lacks safe snapshot boundary: %d %s", detail.Code, detail.Body.String())
	}
	if response := integrationAPIRequest(a, withScope, http.MethodGet, "/api/open/v1/release-notes/0001", "", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("noncanonical sprint ID accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestIntegrationReleaseNotesScopeRequiresLiveTenantAdministrator(t *testing.T) {
	a := testApp(t)
	sprint, _, _ := releaseNotesCenterFixture(t, a)
	releaseNotesCenterGrantReports(t, a, "u_pm")

	available := apiRequest(a, http.MethodGet, "/api/integrations", "u_pm", projectID, "")
	if available.Code != http.StatusOK {
		t.Fatalf("member integration settings: %d %s", available.Code, available.Body.String())
	}
	foundNotification, foundRelease := false, false
	for _, raw := range jsonMap(t, available)["scopes"].([]any) {
		key := raw.(map[string]any)["key"]
		foundNotification = foundNotification || key == "notifications:read"
		foundRelease = foundRelease || key == "release-notes:read"
	}
	if !foundNotification || foundRelease {
		t.Fatalf("member scope catalogue exposed the wrong scopes: %s", available.Body.String())
	}
	scopes := append(append([]string{}, integrationReadScopes...), "release-notes:read")
	denied := apiRequest(a, http.MethodPost, "/api/integrations/tokens", "u_pm", projectID, jsonText(map[string]any{"name": "Denied release export", "scopes": scopes, "expiresInDays": 7}))
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), "scope_forbidden") {
		t.Fatalf("member minted release-note scope: %d %s", denied.Code, denied.Body.String())
	}

	if _, err := a.db.Exec(`UPDATE tenant_memberships SET role='tenant_admin' WHERE tenant_id=? AND user_id='u_pm'`, tenantID); err != nil {
		t.Fatal(err)
	}
	created := apiRequest(a, http.MethodPost, "/api/integrations/tokens", "u_pm", projectID, jsonText(map[string]any{"name": "Administrator release export", "scopes": scopes, "expiresInDays": 7}))
	if created.Code != http.StatusCreated {
		t.Fatalf("administrator could not mint release-note scope: %d %s", created.Code, created.Body.String())
	}
	token := jsonMap(t, created)["token"].(string)
	if response := integrationAPIRequest(a, token, http.MethodGet, fmt.Sprintf("/api/open/v1/release-notes/%d", sprint), "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("administrator release-note token failed: %d %s", response.Code, response.Body.String())
	}

	// Demotion does not have to discover and revoke every token synchronously:
	// the release endpoint rechecks the live enterprise role on each request.
	if _, err := a.db.Exec(`UPDATE tenant_memberships SET role='member' WHERE tenant_id=? AND user_id='u_pm'`, tenantID); err != nil {
		t.Fatal(err)
	}
	demoted := integrationAPIRequest(a, token, http.MethodGet, "/api/open/v1/release-notes", "", "", "")
	if demoted.Code != http.StatusForbidden || !strings.Contains(demoted.Body.String(), "admin_required") {
		t.Fatalf("demoted administrator retained release-note access: %d %s", demoted.Code, demoted.Body.String())
	}
}
