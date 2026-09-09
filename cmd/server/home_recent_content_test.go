package main

import (
	"net/http"
	"strings"
	"testing"
)

func recentContentItems(t *testing.T, a *App, user, project, query string, status int) []any {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/home/recent-content"+query, user, project, "")
	if w.Code != status {
		t.Fatalf("recent content: got %d want %d: %s", w.Code, status, w.Body.String())
	}
	if status != http.StatusOK {
		return nil
	}
	return jsonMap(t, w)["items"].([]any)
}

func TestHomeRecentContentUsesAccessibleActiveProjectScope(t *testing.T) {
	a := testApp(t)
	// 以稳定且晚于演示数据的时间验证排序，不依赖测试运行时钟或种子顺序。
	if _, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,type,status,created_at,updated_at)VALUES(?,?,?,?,?,'规划中',?,?)`, tenantID, projectID, "REQ-HOME", "首页可见需求", "产品需求", "2031-01-01T00:00:00Z", "2031-01-04T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, projectID, "BUG-HOME", "首页可见缺陷", "修复中", "2031-01-01T00:00:00Z", "2031-01-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?, ?,?)`, tenantID, insightProjectID, "SPR-HOME", "管理员可见迭代", "2031-01-01", "2031-01-07", "进行中", "2031-01-01T00:00:00Z", "2031-01-05T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO tenants(id,name)VALUES('home_foreign','Foreign'); INSERT INTO projects(id,tenant_id,name,code,status)VALUES('home_foreign_project','home_foreign','Foreign','FOR','active'); INSERT INTO requirements(tenant_id,project_id,code,title,type,status,created_at,updated_at)VALUES('home_foreign','home_foreign_project','REQ-FOREIGN','不可泄漏','产品需求','规划中','2032-01-01T00:00:00Z','2032-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	memberItems := recentContentItems(t, a, "u_front", projectID, "?limit=20", http.StatusOK)
	if len(memberItems) == 0 {
		t.Fatal("member recent content unexpectedly empty")
	}
	for _, raw := range memberItems {
		item := raw.(map[string]any)
		if item["projectId"] != projectID || item["title"] == "管理员可见迭代" || item["title"] == "不可泄漏" {
			t.Fatalf("member received an inaccessible item: %#v", item)
		}
		if !strings.HasPrefix(item["url"].(string), "/") || item["event"] == "" {
			t.Fatalf("recent item is not safely navigable: %#v", item)
		}
	}

	adminItems := recentContentItems(t, a, "u_admin", projectID, "?limit=20", http.StatusOK)
	foundInsight := false
	for _, raw := range adminItems {
		item := raw.(map[string]any)
		if item["title"] == "管理员可见迭代" {
			foundInsight = item["projectId"] == insightProjectID && item["objectType"] == "sprint"
		}
		if item["title"] == "不可泄漏" {
			t.Fatalf("foreign tenant content leaked to administrator: %#v", item)
		}
	}
	if !foundInsight {
		t.Fatalf("tenant administrator did not receive accessible cross-project content: %#v", adminItems)
	}
	if got := recentContentItems(t, a, "u_front", projectID, "?limit=1", http.StatusOK); len(got) != 1 || got[0].(map[string]any)["title"] != "首页可见需求" {
		t.Fatalf("recent content sort or limit invalid: %#v", got)
	}
}

func TestHomeRecentContentValidatesLimitAndDoesNotMaskStoreFailure(t *testing.T) {
	a := testApp(t)
	for _, query := range []string{"?limit=0", "?limit=21", "?limit=bad"} {
		recentContentItems(t, a, "u_admin", projectID, query, http.StatusUnprocessableEntity)
	}
	if _, err := a.db.Exec(`ALTER TABLE defects RENAME TO home_unavailable_defects`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodGet, "/api/home/recent-content", "u_admin", projectID, "")
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "home_unavailable_defects") {
		t.Fatalf("database failure leaked or returned partial result: %d %s", w.Code, w.Body.String())
	}
}
