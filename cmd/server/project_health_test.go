package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func fixedHealthNow() time.Time {
	return time.Date(2026, time.September, 10, 9, 0, 0, 0, time.UTC)
}

func readProjectHealth(t *testing.T, a *App) ProjectHealth {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/project-health", "u_viewer", a.pid(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("project health %d %s", w.Code, w.Body.String())
	}
	var result ProjectHealth
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProjectHealthEmptyAndRiskRules(t *testing.T) {
	a := dashboardFixture(t)
	now := fixedHealthNow()
	health, err := a.projectHealth(context.Background(), a.db, now)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "healthy" || health.Project.ID != a.pid() || health.Project.Name == "" || len(health.Reasons) != 0 {
		t.Fatalf("empty health = %+v", health)
	}

	insertRequirement := func(tenant, project, title, status, endDate string) {
		t.Helper()
		if _, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,status,end_date,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?)`, tenant, project, "HEALTH-REQ", title, status, endDate, "2026-09-01", "2026-09-01"); err != nil {
			t.Fatal(err)
		}
	}
	insertRequirement(tenantID, a.pid(), "late delivery", "开发中", "2026-09-09")
	insertRequirement(tenantID, a.pid(), "completed legacy", "已完成", "2026-09-01")
	insertRequirement(tenantID, a.pid(), "malformed legacy", "开发中", "2026-99-99")
	insertRequirement(tenantID, insightProjectID, "other project", "开发中", "2026-09-01")
	insertRequirement("other-tenant", a.pid(), "other tenant", "开发中", "2026-09-01")
	if _, err := a.db.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "HEALTH-SPR", "late sprint", "2026-09-01", "2026-09-09", "进行中", "2026-09-01", "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,status,severity,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?),(?,?,?,?,?,?,?,?),(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "HEALTH-MAJOR", "major", "修复中", "严重", "2026-09-01", "2026-09-01", tenantID, a.pid(), "HEALTH-CLOSED", "closed", "已关闭", "致命", "2026-09-01", "2026-09-01", tenantID, insightProjectID, "HEALTH-FOREIGN", "foreign", "修复中", "致命", "2026-09-01", "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	health, err = a.projectHealth(context.Background(), a.db, now)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "at_risk" || health.Counts.OverdueRequirements != 1 || health.Counts.OverdueSprints != 1 || health.Counts.OpenMajorDefects != 1 || health.Counts.OpenFatalDefects != 0 {
		t.Fatalf("at-risk health = %+v", health)
	}
	if got := health.Reasons; len(got) != 3 || got[0].Key != "overdue_sprints" || got[1].Key != "overdue_requirements" || got[2].Key != "open_major_defects" {
		t.Fatalf("risk reasons = %+v", got)
	}
	if _, err := a.db.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,status,severity,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "HEALTH-FATAL", "fatal", "待验证", "致命", "2026-09-01", "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	health, err = a.projectHealth(context.Background(), a.db, now)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != "blocked" || health.Counts.OpenFatalDefects != 1 || len(health.Reasons) != 4 || health.Reasons[0].Key != "open_fatal_defects" {
		t.Fatalf("blocked health = %+v", health)
	}
}

func TestProjectHealthAPIAuthorizationAndDashboardSnapshot(t *testing.T) {
	for _, scenario := range []string{"ok", "unassigned", "archived", "method"} {
		t.Run(scenario, func(t *testing.T) {
			a := dashboardFixture(t)
			switch scenario {
			case "unassigned":
				if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_viewer'`, tenantID, a.pid()); err != nil {
					t.Fatal(err)
				}
			case "archived":
				if _, err := a.db.Exec(`UPDATE projects SET status='archived' WHERE tenant_id=? AND id=?`, tenantID, a.pid()); err != nil {
					t.Fatal(err)
				}
			}
			method := http.MethodGet
			if scenario == "method" {
				method = http.MethodPost
			}
			userID := "u_viewer"
			if scenario == "method" {
				// 使用管理员请求，避免鉴权中间件先于路由方法校验返回权限错误。
				userID = "u_admin"
			}
			w := apiRequest(a, method, "/api/project-health", userID, a.pid(), "{}")
			want := http.StatusOK
			if scenario == "unassigned" || scenario == "archived" {
				want = http.StatusForbidden
			}
			if scenario == "method" {
				want = http.StatusMethodNotAllowed
			}
			if w.Code != want {
				t.Fatalf("%s => %d %s", scenario, w.Code, w.Body.String())
			}
			if scenario == "ok" {
				result := readProjectHealth(t, a)
				if result.Project.ID != a.pid() || result.Status != "healthy" || result.Reasons == nil {
					t.Fatalf("api health = %+v", result)
				}
				dashboard := readDashboard(t, a)
				if dashboard.Health.Project.ID != result.Project.ID || dashboard.Health.Status != result.Status || dashboard.Health.GeneratedAt == "" {
					t.Fatalf("dashboard health = %+v, api = %+v", dashboard.Health, result)
				}
			}
		})
	}
}
