package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// draftTestID 使用规范、可预测的 UUID，既覆盖路由校验，也不会把随机数带入断言。
func draftTestID(index int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
}

func privateDraftBody(kind, targetID, title string, version int64) string {
	return jsonText(map[string]any{
		"kind":        kind,
		"targetId":    targetID,
		"baseVersion": version,
		"context": map[string]any{
			"route": "/" + kind + "s/new",
		},
		"payload": map[string]any{
			"title":       title,
			"description": "仅供私人草稿恢复，尚未发布",
		},
	})
}

func privateDraftCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return jsonMap(t, w)["error"].(map[string]any)["code"].(string)
}

func privateDraftVersion(t *testing.T, w *httptest.ResponseRecorder) int64 {
	t.Helper()
	draft := jsonMap(t, w)["draft"].(map[string]any)
	return int64(draft["version"].(float64))
}

func TestPrivateDraftCRUDMetadataAndOptimisticVersion(t *testing.T) {
	a := testApp(t)
	id := draftTestID(1)
	var beforeRequirements, beforeDefects, beforeActivities, beforeNotifications int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&beforeRequirements); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM defects`).Scan(&beforeDefects); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM activities`).Scan(&beforeActivities); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&beforeNotifications); err != nil {
		t.Fatal(err)
	}

	w := apiRequest(a, http.MethodPut, "/api/drafts/"+id, "u_front", projectID, privateDraftBody("requirement", "", "  本机草稿 \u0000 标题  ", 0))
	if w.Code != http.StatusOK || privateDraftVersion(t, w) != 1 {
		t.Fatalf("create private draft: %d %s", w.Code, w.Body.String())
	}

	w = apiRequest(a, http.MethodGet, "/api/drafts", "u_front", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list private drafts: %d %s", w.Code, w.Body.String())
	}
	list := jsonMap(t, w)
	if list["limit"].(float64) != privateDraftLimit || len(list["items"].([]any)) != 1 {
		t.Fatalf("list metadata shape: %s", w.Body.String())
	}
	metadata := list["items"].([]any)[0].(map[string]any)
	if _, ok := metadata["payload"]; ok {
		t.Fatalf("payload must not be returned by draft list: %s", w.Body.String())
	}
	if _, ok := metadata["context"]; ok {
		t.Fatalf("context must not be returned by draft list: %s", w.Body.String())
	}
	if metadata["title"] == "" || strings.Contains(metadata["title"].(string), "\x00") {
		t.Fatalf("stored title was not normalized: %#v", metadata)
	}

	w = apiRequest(a, http.MethodGet, "/api/drafts/"+id, "u_front", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get private draft: %d %s", w.Code, w.Body.String())
	}
	detail := jsonMap(t, w)["draft"].(map[string]any)
	if detail["payload"].(map[string]any)["description"] != "仅供私人草稿恢复，尚未发布" || detail["context"].(map[string]any)["route"] != "/requirements/new" {
		t.Fatalf("draft detail did not retain body safely: %s", w.Body.String())
	}

	w = apiRequest(a, http.MethodPut, "/api/drafts/"+id, "u_front", projectID, privateDraftBody("requirement", "", "第二版草稿", 1))
	if w.Code != http.StatusOK || privateDraftVersion(t, w) != 2 {
		t.Fatalf("update private draft: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPut, "/api/drafts/"+id, "u_front", projectID, privateDraftBody("requirement", "", "不应覆盖", 1))
	if w.Code != http.StatusConflict || privateDraftCode(t, w) != "draft_conflict" {
		t.Fatalf("stale update did not conflict: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodDelete, "/api/drafts/"+id, "u_front", projectID, `{"version":1}`)
	if w.Code != http.StatusConflict || privateDraftCode(t, w) != "draft_conflict" {
		t.Fatalf("stale delete did not conflict: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodDelete, "/api/drafts/"+id, "u_front", projectID, `{"version":2}`)
	if w.Code != http.StatusOK || jsonMap(t, w)["deleted"] != true {
		t.Fatalf("delete private draft: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/drafts/"+id, "u_front", projectID, "")
	if w.Code != http.StatusNotFound || privateDraftCode(t, w) != "draft_not_found" {
		t.Fatalf("deleted draft remained visible: %d %s", w.Code, w.Body.String())
	}

	var afterRequirements, afterDefects, afterActivities, afterNotifications int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&afterRequirements)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM defects`).Scan(&afterDefects)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM activities`).Scan(&afterActivities)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&afterNotifications)
	if beforeRequirements != afterRequirements || beforeDefects != afterDefects || beforeActivities != afterActivities || beforeNotifications != afterNotifications {
		t.Fatalf("private draft changed published data: requirements %d/%d defects %d/%d activities %d/%d notices %d/%d", beforeRequirements, afterRequirements, beforeDefects, afterDefects, beforeActivities, afterActivities, beforeNotifications, afterNotifications)
	}
}

func TestPrivateDraftScopeRoleTargetAndRequestValidation(t *testing.T) {
	a := testApp(t)
	id := draftTestID(2)
	w := apiRequest(a, http.MethodPut, "/api/drafts/"+id, "u_front", projectID, privateDraftBody("requirement", "", "前端私有草稿", 0))
	if w.Code != http.StatusOK {
		t.Fatalf("seed draft: %d %s", w.Code, w.Body.String())
	}

	// 同一个 UUID 在不同用户空间是不同草稿；任何一方都不可读取另一方的数据。
	w = apiRequest(a, http.MethodGet, "/api/drafts/"+id, "u_pm", projectID, "")
	if w.Code != http.StatusNotFound || privateDraftCode(t, w) != "draft_not_found" {
		t.Fatalf("cross-user draft leaked: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPut, "/api/drafts/"+id, "u_pm", projectID, privateDraftBody("defect", "", "产品自己的缺陷草稿", 0))
	if w.Code != http.StatusOK {
		t.Fatalf("same UUID should be isolated by owner: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/drafts/"+id, "u_front", projectID, "")
	if w.Code != http.StatusOK || jsonMap(t, w)["draft"].(map[string]any)["kind"] != "requirement" {
		t.Fatalf("cross-user write changed original draft: %d %s", w.Code, w.Body.String())
	}

	// 只读成员能够查看自己的草稿箱，但没有任何创建、覆盖或删除能力。
	w = apiRequest(a, http.MethodGet, "/api/drafts", "u_viewer", projectID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("viewer could not read own empty drafts: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPut, "/api/drafts/"+draftTestID(3), "u_viewer", projectID, privateDraftBody("requirement", "", "只读越权", 0))
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer wrote private draft: %d %s", w.Code, w.Body.String())
	}

	var requirementID, defectID int64
	if err := a.db.QueryRow(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, projectID).Scan(&requirementID); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`SELECT id FROM defects WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, projectID).Scan(&defectID); err != nil {
		t.Fatal(err)
	}
	for index, target := range []struct {
		kind string
		id   int64
	}{{"requirement", requirementID}, {"defect", defectID}} {
		w = apiRequest(a, http.MethodPut, "/api/drafts/"+draftTestID(10+index), "u_front", projectID, privateDraftBody(target.kind, fmt.Sprint(target.id), "已关联正式工作项但尚未发布", 0))
		if w.Code != http.StatusOK {
			t.Fatalf("valid %s target was rejected: %d %s", target.kind, w.Code, w.Body.String())
		}
	}

	for _, invalid := range []struct {
		name string
		body string
		code int
	}{
		{"bad-kind", privateDraftBody("other", "", "x", 0), http.StatusUnprocessableEntity},
		{"bad-target-format", privateDraftBody("requirement", "01", "x", 0), http.StatusUnprocessableEntity},
		{"missing-target", privateDraftBody("defect", "999999999", "x", 0), http.StatusUnprocessableEntity},
		{"payload-array", `{"kind":"requirement","payload":[],"baseVersion":0}`, http.StatusUnprocessableEntity},
		{"unknown-field", `{"kind":"requirement","payload":{},"baseVersion":0,"unexpected":true}`, http.StatusBadRequest},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			w := apiRequest(a, http.MethodPut, "/api/drafts/"+draftTestID(100+len(invalid.name)), "u_front", projectID, invalid.body)
			if w.Code != invalid.code {
				t.Fatalf("%s: %d %s", invalid.name, w.Code, w.Body.String())
			}
		})
	}

	// 已归档项目在外层项目授权即被拒绝，因此无法把草稿绑定到不可恢复的工作空间。
	if _, err := a.db.Exec(`UPDATE projects SET status='archived' WHERE tenant_id=? AND id=?`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodGet, "/api/drafts", "u_front", projectID, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("archived project exposed drafts: %d %s", w.Code, w.Body.String())
	}
}

func TestPrivateDraftLimitAndImpersonationRestriction(t *testing.T) {
	a := testApp(t)
	for index := 0; index < privateDraftLimit; index++ {
		id := draftTestID(1000 + index)
		if _, err := a.db.Exec(`INSERT INTO private_workitem_drafts(tenant_id,project_id,user_id,id,kind,title,payload_json,context_json,version,created_at,updated_at)VALUES(?,?,?,?,?,'limit',?,'{}',1,'now','now')`, tenantID, projectID, "u_algo", id, "requirement", `{"title":"limit"}`); err != nil {
			t.Fatal(err)
		}
	}
	w := apiRequest(a, http.MethodPut, "/api/drafts/"+draftTestID(2000), "u_algo", projectID, privateDraftBody("requirement", "", "第 101 条", 0))
	if w.Code != http.StatusConflict || privateDraftCode(t, w) != "draft_limit_reached" {
		t.Fatalf("draft limit must fail without eviction: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM private_workitem_drafts WHERE tenant_id=? AND project_id=? AND user_id='u_algo'`, tenantID, projectID).Scan(&count); err != nil || count != privateDraftLimit {
		t.Fatalf("limit silently evicted/overflowed: %d %v", count, err)
	}

	// 直接调用已作用域化的处理器，确认“代访问”即使绕过前端也不能读取私人内容。
	r := httptest.NewRequest(http.MethodGet, "/api/drafts", nil)
	cookieWriter := httptest.NewRecorder()
	cookie, err := a.issueSession(cookieWriter, r, "u_front")
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(cookie)
	principal, err := a.authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	scoped := *a
	scoped.project, scoped.user, scoped.sessionToken = projectID, principal.UserID, principal.TokenHash
	scoped.impersonation = &impersonationContext{ID: 1, AdminID: "u_admin", TargetID: "u_front", ProjectID: projectID}
	w = httptest.NewRecorder()
	scoped.privateDrafts(w, r)
	if w.Code != http.StatusForbidden || privateDraftCode(t, w) != "draft_impersonation_forbidden" {
		t.Fatalf("impersonation accessed private drafts: %d %s", w.Code, w.Body.String())
	}
}
