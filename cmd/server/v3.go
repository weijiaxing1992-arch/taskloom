package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const insightProjectID = "prj_insight"

func (a *App) apiMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/exports/", a.exportPDF)
	mux.HandleFunc("/api/dashboard", a.dashboard)
	mux.HandleFunc("/api/project-health", a.projectHealthAPI)
	mux.HandleFunc("/api/reports/release-notes", a.releaseNotesCenter)
	mux.HandleFunc("/api/reports/release-notes/", a.releaseNotesCenter)
	mux.HandleFunc("/api/drafts", a.privateDrafts)
	mux.HandleFunc("/api/drafts/", a.privateDrafts)
	mux.HandleFunc("/api/requirement-templates", a.requirementTemplates)
	mux.HandleFunc("/api/requirement-templates/", a.requirementTemplates)
	mux.HandleFunc("/api/ai/requirement-title", a.requirementAITitle)
	mux.HandleFunc("/api/ai/requirement-refine", a.requirementAIRefine)
	mux.HandleFunc("/api/ai/defect-refine", a.requirementAIRefine)
	mux.HandleFunc("/api/requirement-shares", a.requirementShares)
	mux.HandleFunc("/api/health", a.health)
	mux.HandleFunc("/api/session", a.session)
	mux.HandleFunc("/api/organization/directory", a.organizationDirectory)
	mux.HandleFunc("/api/meta", a.meta)
	mux.HandleFunc("/api/projects", a.projects)
	mux.HandleFunc("/api/projects/", a.projectResource)
	mux.HandleFunc("/api/home/recent-content", a.homeRecentContent)
	mux.HandleFunc("/api/profile", a.profile)
	mux.HandleFunc("/api/profile/password", a.profile)
	mux.HandleFunc("/api/preferences/requirement-list", a.requirementListPreferences)
	mux.HandleFunc("/api/preferences/requirement-detail", a.requirementDetailPreferences)
	mux.HandleFunc("/api/requirement-views", a.requirementViews)
	mux.HandleFunc("/api/requirement-views/", a.requirementViews)
	mux.HandleFunc("/api/preferences/display", a.displayPreferences)
	mux.HandleFunc("/api/preferences/locale", a.localePreferences)
	mux.HandleFunc("/api/my-work", a.myWork)
	mux.HandleFunc("/api/search", a.search)
	mux.HandleFunc("/api/audit-logs", a.auditHistory)
	mux.HandleFunc("/api/notifications", a.notifications)
	mux.HandleFunc("/api/notifications/", a.notification)
	mux.HandleFunc("/api/requirements", a.requirements)
	mux.HandleFunc("/api/requirements/", a.requirement)
	mux.HandleFunc("/api/requirement-dependency-candidates", a.requirementDependencyCandidates)
	mux.HandleFunc("/api/roadmap", a.roadmap)
	mux.HandleFunc("/api/requirement-categories", a.requirementCategories)
	mux.HandleFunc("/api/requirement-categories/order", a.requirementCategoryOrder)
	mux.HandleFunc("/api/requirement-categories/", a.requirementCategory)
	mux.HandleFunc("/api/requirement-tags", a.requirementTags)
	mux.HandleFunc("/api/requirement-statuses", a.requirementStatuses)
	mux.HandleFunc("/api/requirement-statuses/", a.requirementStatus)
	mux.HandleFunc("/api/requirement-workflow", a.requirementWorkflow)
	mux.HandleFunc("/api/automation-rules", a.automationRules)
	mux.HandleFunc("/api/automation-rules/", a.automationRules)
	mux.HandleFunc("/api/members", a.members)
	mux.HandleFunc("/api/field-definitions", a.fieldDefinitions)
	mux.HandleFunc("/api/field-definitions/", a.fieldDefinition)
	mux.HandleFunc("/api/field-presets", a.fieldPresets)
	mux.HandleFunc("/api/field-presets/apply", a.applyFieldPresets)
	mux.HandleFunc("/api/departments", a.fieldDepartments)
	mux.HandleFunc("/api/sprints", a.sprints)
	mux.HandleFunc("/api/sprints/", a.sprint)
	mux.HandleFunc("/api/defects", a.defects)
	mux.HandleFunc("/api/defects/", a.defect)
	mux.HandleFunc("/api/test-cases", a.testCases)
	mux.HandleFunc("/api/test-cases/", a.testCase)
	mux.HandleFunc("/api/testing", a.testingWorkspace)
	mux.HandleFunc("/api/testing/", a.testingWorkspace)
	mux.HandleFunc("/api/test-plans", a.testPlans)
	mux.HandleFunc("/api/test-plans/", a.testPlan)
	mux.HandleFunc("/api/test-executions", a.testExecutions)
	mux.HandleFunc("/api/test-executions/", a.testExecution)
	mux.HandleFunc("/api/notifications/outbox", a.outbox)
	mux.HandleFunc("/api/notifications/outbox/", a.outbox)
	return mux
}

func (a *App) scopedAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), ""))
		w.Header().Set("Cache-Control", "private, no-store")
		if a.apiSlots != nil {
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case a.apiSlots <- struct{}{}:
				defer func() { <-a.apiSlots }()
			case <-r.Context().Done():
				return
			case <-timer.C:
				fail(w, http.StatusServiceUnavailable, "server_busy", "服务繁忙，请稍后重试")
				return
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/health" {
			a.health(w, r)
			return
		}
		if r.URL.Path == "/api/auth/login" {
			a.login(w, r)
			return
		}
		// 微信回调以一次性 state 和浏览器 Cookie 鉴权；不能直接复用未认证用户上下文。
		switch r.URL.Path {
		case "/api/auth/wechat/status":
			a.wechatStatus(w, r)
			return
		case "/api/auth/wechat/start":
			a.startWechat(w, r, "login")
			return
		case "/api/auth/wechat/callback":
			a.wechatCallback(w, r)
			return
		case "/api/auth/wecom/callback":
			a.wecomAppCallback(w, r)
			return
		}
		if r.URL.Path == "/api/auth/logout" {
			a.logout(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/public/organization-invitations/") {
			a.publicOrganizationInvitation(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/public/requirement-shares/") {
			a.publicRequirementShare(w, r)
			return
		}
		// 外部协作凭证走独立认证边界，不将 Bearer 扩展为管理后台 Cookie。
		if strings.HasPrefix(r.URL.Path, "/api/open/") {
			a.integrationExternal(w, r)
			return
		}
		principal, err := a.authenticate(r)
		if err != nil {
			if errors.Is(err, errSessionStoreUnavailable) {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "登录服务暂时繁忙，请稍后重试")
				return
			}
			fail(w, http.StatusUnauthorized, "unauthorized", "登录已失效，请重新登录")
			return
		}
		if !a.allowOperationRequest(w, r, principal.UserID) {
			return
		}
		if !a.allowInitialPasswordRequest(w, r, principal, nil) {
			return
		}
		if r.URL.Path == "/api/auth/impersonation" || r.URL.Path == "/api/auth/impersonation/stop" {
			a.impersonationAPI(w, r, principal)
			return
		}
		impersonation, ok := a.resolveImpersonation(w, r, &principal)
		if !ok {
			return
		}
		if impersonation != nil && !a.allowOperationRequest(w, r, principal.UserID) {
			return
		}
		if impersonation != nil && !a.allowInitialPasswordRequest(w, r, principal, impersonation) {
			return
		}
		// Only requests that have passed both the read-only delegation policy and
		// the first-password gate reach this marker. Downstream read helpers use
		// it to preserve the target's credential boundary without replacing the
		// authorised review with the first-password screen.
		if impersonation != nil && impersonation.ReadOnly {
			r = r.WithContext(context.WithValue(r.Context(), readOnlyImpersonationRequestKey{}, true))
		}
		if expected := r.Header.Get("X-TaskLoom-Expected-User"); expected != "" && expected != principal.UserID {
			fail(w, 409, "identity_changed", "账号身份已在其他页面切换，请刷新后继续")
			return
		}
		if acceptedLocale(r.Header.Get("Accept-Language")) == "" {
			var preference string
			if err := a.db.QueryRowContext(r.Context(), `SELECT locale FROM users WHERE tenant_id=? AND id=? AND active=1`, principal.TenantID, principal.UserID).Scan(&preference); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					fail(w, http.StatusUnauthorized, "unauthorized", "账号不可用")
				} else {
					fail(w, http.StatusServiceUnavailable, "database_unavailable", "语言设置暂时无法读取，请稍后重试")
				}
				return
			}
			setResponseLocale(w, preference)
		}
		// 水印在身份、停用及代访问校验后读取，但不受过期项目选择影响。
		if r.URL.Path == "/api/watermark" {
			scoped := *a
			scoped.user, scoped.sessionToken, scoped.impersonation = principal.UserID, principal.TokenHash, impersonation
			scoped.serveImpersonated(http.HandlerFunc(scoped.watermark), w, r)
			return
		}
		// Personal language settings must remain usable even when a previously
		// selected project is no longer accessible. Authentication already checks
		// active tenant membership; no project write permission is involved.
		if r.URL.Path == "/api/preferences/locale" || r.URL.Path == "/api/preferences/display" {
			scoped := *a
			scoped.user = principal.UserID
			scoped.impersonation = impersonation
			handler := scoped.localePreferences
			if r.URL.Path == "/api/preferences/display" {
				handler = scoped.displayPreferences
			}
			scoped.serveImpersonated(http.HandlerFunc(handler), w, r)
			return
		}
		// 企业能力（包含目录）不能依赖另一个浏览器标签碰巧选中的项目；目录本身
		// 会再校验 organization.read，避免普通项目成员借项目上下文枚举全员信息。
		if strings.HasPrefix(r.URL.Path, "/api/organization/") || strings.HasPrefix(r.URL.Path, "/api/profile/wechat") || strings.HasPrefix(r.URL.Path, "/api/profile/wecom-app") || r.URL.Path == "/api/profile/wecom-webhook" || r.URL.Path == "/api/reports/workload" || r.URL.Path == "/api/reports/workload/trends" || r.URL.Path == "/api/reports/workload/personal" || r.URL.Path == "/api/reports/workload/team" || r.URL.Path == "/api/reports/workload/iterations" {
			scoped := *a
			scoped.user, scoped.sessionToken, scoped.impersonation = principal.UserID, principal.TokenHash, impersonation
			handler := scoped.organizationAdmin
			if r.URL.Path == "/api/organization/wechat-login" {
				handler = scoped.organizationWechat
			}
			if r.URL.Path == "/api/organization/wecom-app" {
				handler = scoped.organizationWecomApp
			}
			if strings.HasPrefix(r.URL.Path, "/api/profile/wechat") {
				handler = scoped.profileWechat
			}
			if strings.HasPrefix(r.URL.Path, "/api/profile/wecom-app") {
				handler = scoped.profileWecomApp
			}
			if r.URL.Path == "/api/organization/directory" {
				handler = scoped.organizationDirectory
			}
			if r.URL.Path == "/api/organization/ai-settings" {
				handler = scoped.organizationAISettings
			}
			if r.URL.Path == "/api/profile/wecom-webhook" {
				handler = func(w http.ResponseWriter, r *http.Request) { scoped.userWecomWebhook(w, r, scoped.uid()) }
			}
			if r.URL.Path == "/api/reports/workload" {
				handler = scoped.workloadReport
			}
			if r.URL.Path == "/api/reports/workload/trends" {
				handler = scoped.workloadTrends
			}
			if r.URL.Path == "/api/reports/workload/iterations" {
				handler = scoped.workloadIterationAnalysis
			}
			if r.URL.Path == "/api/reports/workload/personal" {
				handler = scoped.personalWorkloadReport
			}
			if r.URL.Path == "/api/reports/workload/team" {
				handler = scoped.teamWorkloadReport
			}
			scoped.serveImpersonated(http.HandlerFunc(handler), w, r)
			return
		}
		// 项目目录/生命周期使用请求目标自身鉴权，不能被已经归档或删除的旧选择器阻断。
		if r.URL.Path == "/api/projects" || strings.HasPrefix(r.URL.Path, "/api/projects/") {
			scoped := *a
			scoped.user, scoped.sessionToken, scoped.impersonation = principal.UserID, principal.TokenHash, impersonation
			handler := scoped.projects
			if r.URL.Path != "/api/projects" {
				handler = scoped.projectResource
			}
			scoped.serveImpersonated(http.HandlerFunc(handler), w, r)
			return
		}
		// 个人收件箱按每条通知的项目鉴权，不依赖浏览器中可能已撤权的项目选择。
		// 外发队列仍走下方项目/管理员鉴权，不能包含在此个人能力分支中。
		if r.URL.Path == "/api/notifications" || strings.HasPrefix(r.URL.Path, "/api/notifications/") && !strings.HasPrefix(r.URL.Path, "/api/notifications/outbox") {
			scoped := *a
			scoped.user, scoped.sessionToken, scoped.impersonation = principal.UserID, principal.TokenHash, impersonation
			handler := scoped.notification
			if r.URL.Path == "/api/notifications" {
				handler = scoped.notifications
			}
			scoped.serveImpersonated(http.HandlerFunc(handler), w, r)
			return
		}
		pid := r.Header.Get("X-TaskLoom-Project")
		if pid == "" {
			pid = projectID
		}
		var allowed int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM projects p WHERE p.id=? AND p.tenant_id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`, pid, tenantID, principal.UserID, principal.UserID).Scan(&allowed); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目权限服务暂时繁忙，请稍后重试")
			return
		}
		if allowed == 0 {
			if r.Method == http.MethodGet && r.URL.Path == "/api/session" && impersonation == nil {
				scoped := *a
				scoped.user, scoped.sessionToken = principal.UserID, principal.TokenHash
				if scoped.projectlessAdminSession(w, r) {
					return
				}
			}
			fail(w, 403, "project_forbidden", "无权访问该项目")
			return
		}
		scoped := *a
		scoped.project = pid
		scoped.user = principal.UserID
		scoped.sessionToken = principal.TokenHash
		scoped.impersonation = impersonation
		if r.URL.Path == "/api/integrations" || strings.HasPrefix(r.URL.Path, "/api/integrations/") {
			// 个人只读凭证允许只读成员管理；处理器仍独立校验写权限与代访问限制。
			scoped.integrations(w, r)
			return
		}
		scoped.serveImpersonated(scoped.authorize(scoped.apiMux()), w, r)
	})
}

func (a *App) migrateV3() error {
	needsBackfill := map[string]bool{}
	for table, columns := range map[string][]string{
		"projects":        {"description", "owner_user_id", "created_at", "updated_at"},
		"requirements":    {"assignee_user_id", "owner_user_id", "created_by"},
		"defects":         {"assignee_user_id", "verifier_user_id", "created_by"},
		"test_cases":      {"owner_user_id"},
		"test_plans":      {"owner_user_id"},
		"test_executions": {"executor_user_id", "updated_at"},
	} {
		exists, err := a.databaseHasColumns(table, columns...)
		if err != nil {
			return err
		}
		needsBackfill[table] = !exists
	}
	for _, column := range []migrationColumn{
		{"users", "phone", `ALTER TABLE users ADD COLUMN phone TEXT NOT NULL DEFAULT ''`},
		{"users", "job_title", `ALTER TABLE users ADD COLUMN job_title TEXT NOT NULL DEFAULT ''`},
		{"users", "bio", `ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT ''`},
		{"users", "avatar_color", `ALTER TABLE users ADD COLUMN avatar_color TEXT NOT NULL DEFAULT '#665FE8'`},
		{"users", "locale", `ALTER TABLE users ADD COLUMN locale TEXT NOT NULL DEFAULT 'zh-CN'`},
		{"users", "timezone", `ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai'`},
		{"users", "email_notifications", `ALTER TABLE users ADD COLUMN email_notifications INTEGER NOT NULL DEFAULT 1`},
		{"users", "password_hash", `ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`},
		{"users", "password_changed_at", `ALTER TABLE users ADD COLUMN password_changed_at TEXT NOT NULL DEFAULT ''`},
		{"projects", "description", `ALTER TABLE projects ADD COLUMN description TEXT NOT NULL DEFAULT ''`},
		{"projects", "status", `ALTER TABLE projects ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`},
		{"projects", "owner_user_id", `ALTER TABLE projects ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT ''`},
		{"projects", "icon", `ALTER TABLE projects ADD COLUMN icon TEXT NOT NULL DEFAULT '项'`},
		{"projects", "color", `ALTER TABLE projects ADD COLUMN color TEXT NOT NULL DEFAULT '#5B5BD6'`},
		{"projects", "created_at", `ALTER TABLE projects ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`},
		{"projects", "updated_at", `ALTER TABLE projects ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`},
		{"projects", "archived_at", `ALTER TABLE projects ADD COLUMN archived_at TEXT`},
		{"requirements", "assignee_user_id", `ALTER TABLE requirements ADD COLUMN assignee_user_id TEXT NOT NULL DEFAULT ''`},
		{"requirements", "owner_user_id", `ALTER TABLE requirements ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT ''`},
		{"requirements", "created_by", `ALTER TABLE requirements ADD COLUMN created_by TEXT NOT NULL DEFAULT ''`},
		{"defects", "assignee_user_id", `ALTER TABLE defects ADD COLUMN assignee_user_id TEXT NOT NULL DEFAULT ''`},
		{"defects", "verifier_user_id", `ALTER TABLE defects ADD COLUMN verifier_user_id TEXT NOT NULL DEFAULT ''`},
		{"defects", "created_by", `ALTER TABLE defects ADD COLUMN created_by TEXT NOT NULL DEFAULT ''`},
		{"test_cases", "owner_user_id", `ALTER TABLE test_cases ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT ''`},
		{"test_cases", "type", `ALTER TABLE test_cases ADD COLUMN type TEXT NOT NULL DEFAULT '功能测试'`},
		{"test_cases", "tags", `ALTER TABLE test_cases ADD COLUMN tags TEXT NOT NULL DEFAULT ''`},
		{"test_cases", "enabled", `ALTER TABLE test_cases ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`},
		{"test_cases", "steps_json", `ALTER TABLE test_cases ADD COLUMN steps_json TEXT NOT NULL DEFAULT '[]'`},
		{"test_plans", "owner_user_id", `ALTER TABLE test_plans ADD COLUMN owner_user_id TEXT NOT NULL DEFAULT ''`},
		{"test_plans", "environment", `ALTER TABLE test_plans ADD COLUMN environment TEXT NOT NULL DEFAULT '测试环境'`},
		{"test_plans", "executor_user_id", `ALTER TABLE test_plans ADD COLUMN executor_user_id TEXT NOT NULL DEFAULT ''`},
		{"test_executions", "executor_user_id", `ALTER TABLE test_executions ADD COLUMN executor_user_id TEXT NOT NULL DEFAULT ''`},
		{"test_executions", "actual_result", `ALTER TABLE test_executions ADD COLUMN actual_result TEXT NOT NULL DEFAULT ''`},
		{"test_executions", "updated_at", `ALTER TABLE test_executions ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`},
		{"notification_outbox", "updated_at", `ALTER TABLE notification_outbox ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`},
	} {
		if _, err := addMigrationColumn(a.db, column); err != nil {
			return err
		}
	}
	schema := `
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_tenant_code ON projects(tenant_id,code);
CREATE INDEX IF NOT EXISTS idx_projects_tenant_status ON projects(tenant_id,status,updated_at);
CREATE INDEX IF NOT EXISTS idx_requirements_assignee ON requirements(tenant_id,project_id,assignee_user_id,status);
CREATE INDEX IF NOT EXISTS idx_defects_assignee ON defects(tenant_id,project_id,assignee_user_id,status);
CREATE INDEX IF NOT EXISTS idx_defects_verifier ON defects(tenant_id,project_id,verifier_user_id,status);
CREATE INDEX IF NOT EXISTS idx_test_cases_owner ON test_cases(tenant_id,project_id,owner_user_id,status);
CREATE INDEX IF NOT EXISTS idx_test_plans_owner ON test_plans(tenant_id,project_id,owner_user_id,status);
CREATE TABLE IF NOT EXISTS project_visits(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,visited_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id));
CREATE INDEX IF NOT EXISTS idx_project_visits_user ON project_visits(tenant_id,user_id,visited_at);
CREATE TABLE IF NOT EXISTS user_notifications(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,recipient_user_id TEXT NOT NULL,actor_user_id TEXT NOT NULL DEFAULT '',event_type TEXT NOT NULL,subject_type TEXT NOT NULL,subject_id INTEGER NOT NULL,title TEXT NOT NULL,body TEXT NOT NULL DEFAULT '',read_at TEXT,created_at TEXT NOT NULL,dedupe_key TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_notifications_dedupe ON user_notifications(tenant_id,dedupe_key) WHERE dedupe_key!='';
CREATE INDEX IF NOT EXISTS idx_user_notifications_recipient ON user_notifications(tenant_id,recipient_user_id,read_at,created_at);
CREATE INDEX IF NOT EXISTS idx_user_notifications_scope ON user_notifications(tenant_id,project_id,created_at);
CREATE TABLE IF NOT EXISTS test_execution_history(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,execution_id INTEGER NOT NULL,status TEXT NOT NULL,executor_user_id TEXT NOT NULL,actual_result TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_test_execution_history ON test_execution_history(tenant_id,project_id,execution_id,created_at);
`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// 每个回填都依赖同一批刚新增的稳定 ID 列；放在一个事务中可以避免中断后
	// 只写入负责人、未写入创建人的半状态。下一次启动会按列存在性安全跳过 DDL。
	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("启动 V3 回填事务: %w", err)
	}
	defer tx.Rollback()
	execBackfill := func(name, statement string, args ...any) error {
		if _, err := tx.Exec(statement, args...); err != nil {
			return fmt.Errorf("V3 回填%s: %w", name, err)
		}
		return nil
	}
	if needsBackfill["projects"] {
		if err := execBackfill("项目资料", `UPDATE projects SET description=CASE WHEN id=? AND description='' THEN '星河示例企业核心研发协作、需求与质量交付空间' ELSE description END,owner_user_id=CASE WHEN owner_user_id='' THEN 'u_admin' ELSE owner_user_id END,icon=CASE WHEN icon='' THEN '研' ELSE icon END,created_at=CASE WHEN created_at='' THEN ? ELSE created_at END,updated_at=CASE WHEN updated_at='' THEN ? ELSE updated_at END WHERE tenant_id=?`, projectID, now, now, tenantID); err != nil {
			return err
		}
	}
	if needsBackfill["requirements"] {
		if err := execBackfill("需求稳定人员 ID", `UPDATE requirements SET assignee_user_id=CASE WHEN assignee_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=requirements.tenant_id AND users.name=requirements.assignee LIMIT 1),'') ELSE assignee_user_id END,owner_user_id=CASE WHEN owner_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=requirements.tenant_id AND users.name=requirements.owner LIMIT 1),'') ELSE owner_user_id END,created_by=CASE WHEN created_by='' THEN 'u_admin' ELSE created_by END`); err != nil {
			return err
		}
	}
	if needsBackfill["defects"] {
		if err := execBackfill("缺陷稳定人员 ID", `UPDATE defects SET assignee_user_id=CASE WHEN assignee_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=defects.tenant_id AND users.name=defects.assignee LIMIT 1),'') ELSE assignee_user_id END,verifier_user_id=CASE WHEN verifier_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=defects.tenant_id AND users.name=defects.verifier LIMIT 1),'') ELSE verifier_user_id END,created_by=CASE WHEN created_by='' THEN 'u_admin' ELSE created_by END`); err != nil {
			return err
		}
	}
	if needsBackfill["test_cases"] {
		if err := execBackfill("用例负责人 ID", `UPDATE test_cases SET owner_user_id=CASE WHEN owner_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=test_cases.tenant_id AND users.name=test_cases.owner LIMIT 1),'') ELSE owner_user_id END`); err != nil {
			return err
		}
	}
	if needsBackfill["test_plans"] {
		// Older plans stored only an owner display name.  Backfill the stable ID
		// only when a member can be resolved in the same tenant; future writes
		// always persist owner_user_id before creating notifications.
		if err := execBackfill("计划负责人 ID", `UPDATE test_plans SET owner_user_id=CASE WHEN owner_user_id='' THEN COALESCE((SELECT u.id FROM users u JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.user_id=u.id WHERE u.tenant_id=test_plans.tenant_id AND pm.project_id=test_plans.project_id AND u.name=test_plans.owner LIMIT 1),'') ELSE owner_user_id END`); err != nil {
			return err
		}
	}
	if needsBackfill["test_executions"] {
		if err := execBackfill("执行人 ID", `UPDATE test_executions SET executor_user_id=CASE WHEN executor_user_id='' THEN COALESCE((SELECT id FROM users WHERE users.tenant_id=test_executions.tenant_id AND users.name=test_executions.executor LIMIT 1),'') ELSE executor_user_id END,updated_at=CASE WHEN updated_at='' THEN COALESCE(NULLIF(executed_at,''),?) ELSE updated_at END`, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 V3 回填事务: %w", err)
	}
	if err := a.seedV3(now); err != nil {
		return err
	}
	if _, err := a.db.Exec(`PRAGMA optimize`); err != nil {
		return fmt.Errorf("优化 V3 数据库: %w", err)
	}
	if err := a.migrateSavedViews(); err != nil {
		return err
	}
	return a.migrateAuth()
}

func (a *App) seedV3(now string) error {
	if !a.seedDemo {
		return nil
	}
	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("启动 V3 演示数据事务: %w", err)
	}
	defer tx.Rollback()
	execSeed := func(name, statement string, args ...any) error {
		if _, err := tx.Exec(statement, args...); err != nil {
			return fmt.Errorf("写入 V3 演示%s: %w", name, err)
		}
		return nil
	}
	if err := execSeed("洞察项目", `INSERT OR IGNORE INTO projects(id,tenant_id,name,code,description,status,owner_user_id,icon,color,created_at,updated_at)VALUES(?,?,?,?,?,'active',?,'智','#0F766E',?,?)`, insightProjectID, tenantID, "星河示例分析", "AIP", "算法洞察、模型评测与数据产品协作空间", "u_algo", now, now); err != nil {
		return err
	}
	for _, m := range []struct{ id, role string }{{"u_admin", "project_admin"}, {"u_pm", "product"}, {"u_algo", "algorithm"}, {"u_qa", "qa"}, {"u_viewer", "viewer"}} {
		if err := execSeed("项目成员", `INSERT OR IGNORE INTO memberships(tenant_id,project_id,user_id,role)VALUES(?,?,?,?)`, tenantID, insightProjectID, m.id, m.role); err != nil {
			return err
		}
		if err := execSeed("项目成员权限", `INSERT OR IGNORE INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,?,?,?)`, tenantID, insightProjectID, m.id, m.role, now, now); err != nil {
			return err
		}
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=?`, tenantID, insightProjectID).Scan(&n); err != nil {
		return fmt.Errorf("统计洞察项目需求: %w", err)
	}
	if n == 0 {
		for _, x := range []Requirement{{Title: "洞察报告支持多模型对比", Type: "产品需求", Description: "在同一评测集比较不同模型效果并输出差异摘要。", Category: "算法产品", Sprint: "AIP 1.0", Status: "开发中", Priority: "P0", Owner: "唐果", Assignee: "唐果", Discipline: "algorithm", Progress: 60, EstimatedHours: 24, ActualHours: 13}, {Title: "评测数据集权限分级", Type: "产品需求", Description: "按项目成员角色限制敏感评测集。", Category: "数据治理", Sprint: "待规划", Status: "评审中", Priority: "P1", Owner: "陈澄", Assignee: "陈澄", Discipline: "product", Progress: 20, EstimatedHours: 12}} {
			x.CustomFields = map[string]any{}
			defaults(&x)
			assigneeID, err := migrationUserIDByName(tx, x.Assignee)
			if err != nil {
				return fmt.Errorf("解析洞察需求处理人: %w", err)
			}
			ownerID, err := migrationUserIDByName(tx, x.Owner)
			if err != nil {
				return fmt.Errorf("解析洞察需求负责人: %w", err)
			}
			res, err := tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,type,description,acceptance,category,sprint,status,priority,owner,assignee,discipline,progress,estimated_hours,actual_hours,assignee_user_id,owner_user_id,created_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, insightProjectID, "", x.Title, x.Type, x.Description, "关键链路可用并通过评测", x.Category, x.Sprint, x.Status, x.Priority, x.Owner, x.Assignee, x.Discipline, x.Progress, x.EstimatedHours, x.ActualHours, assigneeID, ownerID, "u_admin", now, now)
			if err != nil {
				return fmt.Errorf("写入洞察项目需求: %w", err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("读取洞察需求编号: %w", err)
			}
			code, err := requirementSerialCode(id)
			if err != nil {
				return err
			}
			if err := execSeed("洞察需求编号", `UPDATE requirements SET code=? WHERE tenant_id=? AND project_id=? AND id=?`, code, tenantID, insightProjectID, id); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,goal,start_date,end_date,status,capacity,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, insightProjectID, "", "AIP 1.0 模型评测", "交付多模型评测与洞察报告", "2026-09-01", "2026-09-20", "进行中", 64, now, now)
		if err != nil {
			return fmt.Errorf("写入洞察项目迭代: %w", err)
		}
		sid, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取洞察迭代编号: %w", err)
		}
		if err := execSeed("洞察迭代编号", `UPDATE sprints SET code=? WHERE tenant_id=? AND project_id=? AND id=?`, fmt.Sprintf("SPR-%03d", sid), tenantID, insightProjectID, sid); err != nil {
			return err
		}
		res, err = tx.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,description,severity,priority,status,assignee,verifier,sprint,discipline,progress,estimated_hours,actual_hours,assignee_user_id,verifier_user_id,created_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, insightProjectID, "", "评测结果图表在空数据时溢出", "空评测集未显示引导状态", "一般", "P1", "待验证", "唐果", "苏禾", "AIP 1.0", "algorithm", 85, 4, 3, "u_algo", "u_qa", "u_qa", now, now)
		if err != nil {
			return fmt.Errorf("写入洞察项目缺陷: %w", err)
		}
		did, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取洞察缺陷编号: %w", err)
		}
		if err := execSeed("洞察缺陷编号", `UPDATE defects SET code=? WHERE tenant_id=? AND project_id=? AND id=?`, fmt.Sprintf("BUG-%04d", did), tenantID, insightProjectID, did); err != nil {
			return err
		}
	}
	if err := execSeed("洞察项目字段", `INSERT OR IGNORE INTO field_definitions(tenant_id,project_id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,created_at,updated_at) SELECT tenant_id,?,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,?,? FROM field_definitions WHERE tenant_id=? AND project_id=?`, insightProjectID, now, now, tenantID, projectID); err != nil {
		return err
	}
	var orbitRequirementID, orbitDefectID, insightSprintID int64
	if err := tx.QueryRow(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, projectID).Scan(&orbitRequirementID); err != nil {
		return fmt.Errorf("读取核心需求通知对象: %w", err)
	}
	if err := tx.QueryRow(`SELECT id FROM defects WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1 OFFSET 1`, tenantID, projectID).Scan(&orbitDefectID); err != nil {
		return fmt.Errorf("读取核心缺陷通知对象: %w", err)
	}
	if err := tx.QueryRow(`SELECT id FROM sprints WHERE tenant_id=? AND project_id=? ORDER BY id LIMIT 1`, tenantID, insightProjectID).Scan(&insightSprintID); err != nil {
		return fmt.Errorf("读取洞察迭代通知对象: %w", err)
	}
	for _, seed := range []struct {
		pid, recipient, event, typ string
		sid                        int64
		title, body, key           string
	}{
		{projectID, "u_pm", "requirement.assigned", "requirement", orbitRequirementID, "你有新的高优先级需求", "000001 已分配给你，请确认范围与迭代。", "seed:pm:req1"},
		{projectID, "u_qa", "defect.verification", "defect", orbitDefectID, "缺陷等待回归验证", "筛选刷新问题已修复，等待测试确认。", "seed:qa:bug2"},
		{insightProjectID, "u_algo", "sprint.started", "sprint", insightSprintID, "洞察引擎迭代已开始", "AIP 1.0 已进入执行阶段。", "seed:algo:sprint"},
	} {
		if err := execSeed("演示通知", `INSERT OR IGNORE INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, seed.pid, seed.recipient, "u_admin", seed.event, seed.typ, seed.sid, seed.title, seed.body, now, seed.key); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 V3 演示数据事务: %w", err)
	}
	return nil
}

func userIDByName(db *sql.DB, name string) string {
	id, _ := migrationUserIDByName(db, name)
	return id
}

func (a *App) isTenantAdmin(uid string) bool {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM tenant_memberships WHERE tenant_id=? AND user_id=? AND role='tenant_admin' AND status='active'`, tenantID, uid).Scan(&n)
	return n > 0
}

func (a *App) canManageProject(uid, project string) bool {
	if a.isTenantAdmin(uid) {
		return true
	}
	var role string
	_ = a.db.QueryRow(`SELECT role FROM project_members WHERE tenant_id=? AND project_id=? AND user_id=?`, tenantID, project, uid).Scan(&role)
	return role == "project_admin"
}

func (a *App) projectSummary(ctx context.Context, project string) (map[string]int, error) {
	counts := map[string]int{}
	for key, table := range map[string]string{"members": "project_members", "requirements": "requirements", "defects": "defects", "sprints": "sprints", "testCases": "test_cases", "testPlans": "test_plans"} {
		var n int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE tenant_id=? AND project_id=?`, tenantID, project).Scan(&n); err != nil {
			return nil, err
		}
		counts[key] = n
	}
	return counts, nil
}

func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	a.listLifecycleProjects(w, r)
}

func (a *App) projectResource(w http.ResponseWriter, r *http.Request) {
	a.projectLifecycleResource(w, r)
}

func (a *App) projectMembers(w http.ResponseWriter, r *http.Request, id string) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.name,u.email,pm.role,u.active,`+projectRolesJSONSQL("pm")+` FROM project_members pm JOIN users u ON u.id=pm.user_id AND u.tenant_id=pm.tenant_id WHERE pm.tenant_id=? AND pm.project_id=? ORDER BY u.name`, tenantID, id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var uid, name, email, role, rawRoles string
		var active bool
		if err := rows.Scan(&uid, &name, &email, &role, &active, &rawRoles); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		items = append(items, map[string]any{"id": uid, "name": name, "email": email, "role": role, "projectRoles": decodedProjectRoles(rawRoles, role), "active": active})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": items})
}

type workItem struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Type           string `json:"type"`
	ProjectID      string `json:"projectId"`
	ProjectName    string `json:"projectName"`
	Status         string `json:"status"`
	StatusName     string `json:"statusName,omitempty"`
	StatusColor    string `json:"statusColor,omitempty"`
	StatusCategory string `json:"statusCategory,omitempty"`
	StatusSystem   bool   `json:"statusSystem"`
	IsEnd          bool   `json:"isEnd"`
	Priority       string `json:"priority"`
	Assignee       string `json:"assignee"`
	Role           string `json:"role"`
	Sprint         string `json:"sprint"`
	DueDate        string `json:"dueDate"`
	UpdatedAt      string `json:"updatedAt"`
	Category       string `json:"category"`
	URL            string `json:"url"`
}

func (a *App) accessibleProjectIDs(uid string) map[string]bool {
	projects, _ := a.accessibleProjectIDsChecked(context.Background(), uid)
	return projects // Best-effort background notification delivery fails closed.
}

func (a *App) accessibleProjectIDsChecked(ctx context.Context, uid string) (map[string]bool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT p.id FROM projects p WHERE p.tenant_id=? AND p.status='active' AND (EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=p.tenant_id AND pm.project_id=p.id AND pm.user_id=?) OR EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=p.tenant_id AND tm.user_id=? AND tm.role='tenant_admin' AND tm.status='active'))`, tenantID, uid, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func workCategory(typ, status, due string) string {
	if (typ == "缺陷" && validChoice(status, []string{"已关闭", "已拒绝"})) || (typ == "迭代" && validChoice(status, []string{"已完成", "已取消"})) || (typ == "测试执行" && validChoice(status, []string{"通过", "跳过"})) || (typ == "测试用例" && status == "已废弃") {
		return "completed"
	}
	if due != "" {
		if d, e := time.Parse("2006-01-02", due); e == nil {
			today := time.Now().In(time.Local).Truncate(24 * time.Hour)
			if d.Before(today) {
				return "overdue"
			}
			if d.Before(today.AddDate(0, 0, 4)) {
				return "due"
			}
		}
	}
	if validChoice(status, []string{"开发中", "测试中", "修复中", "已解决", "待验证", "执行中", "进行中"}) {
		return "doing"
	}
	return "todo"
}

func (a *App) myWork(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("view") == "favorites" {
		a.favoriteWork(w, r)
		return
	}
	uid, name, _, active, userErr := a.currentUserState(r)
	if userErr != nil && !errors.Is(userErr, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "账号权限服务暂时繁忙，请稍后重试")
		return
	}
	if !active {
		fail(w, 403, "account_disabled", "账号已停用")
		return
	}
	access, err := a.accessibleProjectIDsChecked(r.Context(), uid)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目权限服务暂时繁忙，请稍后重试")
		return
	}
	scope := r.URL.Query().Get("project")
	if scope == "" {
		scope = a.pid()
	}
	sprintChoices, selectedSprint, validSprint := a.myWorkSprintSelection(w, r, access, scope)
	if !validSprint {
		return
	}
	items := []workItem{}
	catalog, err := a.stateCatalogByProject(r.Context())
	if err != nil {
		failState(w, err)
		return
	}
	related, relatedArgs := workItemRelatedSQL("requirement", "r", uid)
	rows, err := a.db.QueryContext(r.Context(), `SELECT r.id,r.code,r.title,r.project_id,p.name,r.status,r.priority,r.assignee,r.discipline,r.sprint,r.end_date,r.updated_at FROM requirements r JOIN projects p ON p.id=r.project_id AND p.tenant_id=r.tenant_id WHERE r.tenant_id=? AND `+related, append([]any{tenantID}, relatedArgs...)...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var x workItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Title, &x.ProjectID, &x.ProjectName, &x.Status, &x.Priority, &x.Assignee, &x.Role, &x.Sprint, &x.DueDate, &x.UpdatedAt); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		x.Type = "需求"
		x.Code = requirementDisplayCode(x.ID, x.Code)
		x.URL = "/requirements?req=" + fmt.Sprint(x.ID)
		applyWorkStatus(&x, catalog[x.ProjectID])
		if access[x.ProjectID] && (scope == "all" || scope == x.ProjectID) {
			items = append(items, x)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT s.id,s.code,s.name,s.project_id,p.name,s.status,'P2','','项目负责人',s.name,s.end_date,s.updated_at FROM sprints s JOIN projects p ON p.id=s.project_id AND p.tenant_id=s.tenant_id WHERE s.tenant_id=? AND p.owner_user_id=?`, tenantID, uid)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var x workItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Title, &x.ProjectID, &x.ProjectName, &x.Status, &x.Priority, &x.Assignee, &x.Role, &x.Sprint, &x.DueDate, &x.UpdatedAt); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		x.Type = "迭代"
		x.URL = "/iterations?sprint=" + fmt.Sprint(x.ID)
		x.Category = workCategory(x.Type, x.Status, x.DueDate)
		if access[x.ProjectID] && (scope == "all" || scope == x.ProjectID) {
			items = append(items, x)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	related, relatedArgs = workItemRelatedSQL("defect", "d", uid)
	rows, err = a.db.QueryContext(r.Context(), `SELECT d.id,d.code,d.title,d.project_id,p.name,d.status,d.priority,d.assignee,d.discipline,d.sprint,d.updated_at FROM defects d JOIN projects p ON p.id=d.project_id AND p.tenant_id=d.tenant_id WHERE d.tenant_id=? AND `+related, append([]any{tenantID}, relatedArgs...)...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var x workItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Title, &x.ProjectID, &x.ProjectName, &x.Status, &x.Priority, &x.Assignee, &x.Role, &x.Sprint, &x.UpdatedAt); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		x.Type = "缺陷"
		x.URL = "/defects?bug=" + fmt.Sprint(x.ID)
		x.Category = workCategory(x.Type, x.Status, "")
		if access[x.ProjectID] && (scope == "all" || scope == x.ProjectID) {
			items = append(items, x)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT c.id,c.code,c.title,c.project_id,p.name,c.status,c.priority,c.owner,c.type,c.updated_at,COALESCE(r.sprint,'') FROM test_cases c JOIN projects p ON p.id=c.project_id AND p.tenant_id=c.tenant_id LEFT JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.id=c.requirement_id WHERE c.tenant_id=? AND (c.owner_user_id=? OR (c.owner_user_id='' AND c.owner=?))`, tenantID, uid, name)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var x workItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Title, &x.ProjectID, &x.ProjectName, &x.Status, &x.Priority, &x.Assignee, &x.Role, &x.UpdatedAt, &x.Sprint); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		x.Type = "测试用例"
		x.URL = "/tests?tab=cases&case=" + fmt.Sprint(x.ID)
		x.Category = workCategory(x.Type, x.Status, "")
		if access[x.ProjectID] && (scope == "all" || scope == x.ProjectID) {
			items = append(items, x)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT e.id,'EXE-'||printf('%04d',e.id),c.title,e.project_id,p.name,e.status,c.priority,e.executor,'测试执行',pl.sprint,pl.end_date,e.updated_at FROM test_executions e JOIN test_cases c ON c.id=e.case_id AND c.project_id=e.project_id JOIN test_plans pl ON pl.id=e.plan_id AND pl.project_id=e.project_id JOIN projects p ON p.id=e.project_id WHERE e.tenant_id=? AND (e.executor_user_id=? OR (e.executor_user_id='' AND e.executor=?))`, tenantID, uid, name)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var x workItem
		if err := rows.Scan(&x.ID, &x.Code, &x.Title, &x.ProjectID, &x.ProjectName, &x.Status, &x.Priority, &x.Assignee, &x.Role, &x.Sprint, &x.DueDate, &x.UpdatedAt); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		x.Type = "测试执行"
		x.URL = "/tests?tab=executions&execution=" + fmt.Sprint(x.ID)
		x.Category = workCategory(x.Type, x.Status, x.DueDate)
		if access[x.ProjectID] && (scope == "all" || scope == x.ProjectID) {
			items = append(items, x)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	queryRequirementID := requirementCodeQueryID(q)
	typ := r.URL.Query().Get("type")
	status := r.URL.Query().Get("status")
	category := r.URL.Query().Get("category")
	base := []workItem{}
	for _, x := range items {
		if !matchesWorkSprint(x, selectedSprint) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(x.Title+" "+x.Code+" "+x.ProjectName), q) && !(x.Type == "需求" && queryRequirementID == x.ID) {
			continue
		}
		if typ != "" && x.Type != typ {
			continue
		}
		if status != "" && x.Status != status {
			continue
		}
		base = append(base, x)
	}
	counts := map[string]int{"all": 0, "active": 0, "todo": 0, "doing": 0, "due": 0, "overdue": 0, "completed": 0, "cancelled": 0}
	for _, x := range base {
		counts[x.Category]++
		counts["all"]++
		if x.Category != "completed" && x.Category != "cancelled" {
			counts["active"]++
		}
	}
	filtered := []workItem{}
	for _, x := range base {
		if category == "" || x.Category == category || category == "active" && x.Category != "completed" && x.Category != "cancelled" {
			filtered = append(filtered, x)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].UpdatedAt > filtered[j].UpdatedAt })
	write(w, 200, map[string]any{"items": filtered, "total": len(filtered), "counts": counts, "sprints": sprintChoices, "requirementStatuses": statusDefinitionsForScope(catalog, access, scope)})
}

type searchItem struct {
	ID          any    `json:"id"`
	Type        string `json:"type"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Status      string `json:"status"`
	Snippet     string `json:"snippet"`
	UpdatedAt   string `json:"updatedAt"`
	URL         string `json:"url"`
}

func (a *App) search(w http.ResponseWriter, r *http.Request) {
	uid := a.uid()
	access, err := a.accessibleProjectIDsChecked(r.Context(), uid)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目权限服务暂时繁忙，请稍后重试")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	queryRequirementID := requirementCodeQueryID(q)
	typ := r.URL.Query().Get("type")
	pid := r.URL.Query().Get("project")
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	items := []searchItem{}
	match := func(s string) bool { return q == "" || strings.Contains(strings.ToLower(s), q) }
	allow := func(t, p, st string) bool {
		return access[p] && (pid == "" || pid == "all" || pid == p) && (typ == "" || typ == t) && (status == "" || status == st)
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,code,name,description,status,updated_at FROM projects WHERE tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id, code, title, desc, st, up string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		if allow("项目", id, st) && match(code+" "+title+" "+desc) {
			items = append(items, searchItem{id, "项目", code, title, id, title, st, excerpt(desc, q), up, "/projects?project=" + id})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT r.id,r.code,r.title,r.description,r.status,r.updated_at,r.project_id,p.name,`+workItemPeopleSearchSQL("requirement", "r")+` FROM requirements r JOIN projects p ON p.id=r.project_id AND p.tenant_id=r.tenant_id WHERE r.tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id int64
		var code, title, desc, st, up, p, n, people string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up, &p, &n, &people); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		displayCode := requirementDisplayCode(id, code)
		if allow("需求", p, st) && (match(displayCode+" "+code+" "+title+" "+desc+" "+people) || queryRequirementID == id) {
			snippet := excerpt(desc, q)
			if q != "" && !match(displayCode+" "+code+" "+title+" "+desc) {
				snippet = "参与人员与部门：" + excerpt(people, q)
			}
			items = append(items, searchItem{id, "需求", displayCode, title, p, n, st, snippet, up, "/requirements?req=" + fmt.Sprint(id)})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT d.id,d.code,d.title,d.description,d.status,d.updated_at,d.project_id,p.name,`+workItemPeopleSearchSQL("defect", "d")+` FROM defects d JOIN projects p ON p.id=d.project_id AND p.tenant_id=d.tenant_id WHERE d.tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id int64
		var code, title, desc, st, up, p, n, people string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up, &p, &n, &people); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		if allow("缺陷", p, st) && match(code+" "+title+" "+desc+" "+people) {
			snippet := excerpt(desc, q)
			if q != "" && !match(code+" "+title+" "+desc) {
				snippet = "参与人员与部门：" + excerpt(people, q)
			}
			items = append(items, searchItem{id, "缺陷", code, title, p, n, st, snippet, up, "/defects?bug=" + fmt.Sprint(id)})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT s.id,s.code,s.name,s.goal,s.status,s.updated_at,s.project_id,p.name FROM sprints s JOIN projects p ON p.id=s.project_id AND p.tenant_id=s.tenant_id WHERE s.tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id int64
		var code, title, desc, st, up, p, n string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up, &p, &n); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		if allow("迭代", p, st) && match(code+" "+title+" "+desc) {
			items = append(items, searchItem{id, "迭代", code, title, p, n, st, excerpt(desc, q), up, "/iterations?sprint=" + fmt.Sprint(id)})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT c.id,c.code,c.title,c.preconditions,c.status,c.updated_at,c.project_id,p.name FROM test_cases c JOIN projects p ON p.id=c.project_id AND p.tenant_id=c.tenant_id WHERE c.tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id int64
		var code, title, desc, st, up, p, n string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up, &p, &n); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		if allow("测试用例", p, st) && match(code+" "+title+" "+desc) {
			items = append(items, searchItem{id, "测试用例", code, title, p, n, st, excerpt(desc, q), up, "/tests?tab=cases&case=" + fmt.Sprint(id)})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	rows, err = a.db.QueryContext(r.Context(), `SELECT t.id,t.code,t.name,t.scope,t.status,t.updated_at,t.project_id,p.name FROM test_plans t JOIN projects p ON p.id=t.project_id AND p.tenant_id=t.tenant_id WHERE t.tenant_id=?`, tenantID)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	for rows.Next() {
		var id int64
		var code, title, desc, st, up, p, n string
		if err := rows.Scan(&id, &code, &title, &desc, &st, &up, &p, &n); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		if allow("测试计划", p, st) && match(code+" "+title+" "+desc) {
			items = append(items, searchItem{id, "测试计划", code, title, p, n, st, excerpt(desc, q), up, "/tests?tab=plans&plan=" + fmt.Sprint(id)})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt > items[j].UpdatedAt })
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	catalog, err := a.stateCatalogByProject(r.Context())
	if err != nil {
		failState(w, err)
		return
	}
	output := []map[string]any{}
	for _, item := range items[offset:end] {
		record, err := workRecord(item)
		if err != nil {
			failState(w, err)
			return
		}
		if item.Type == "需求" {
			state, ok := catalog[item.ProjectID][item.Status]
			if !ok {
				state = RequirementStatus{Name: item.Status, Category: "todo"}
			}
			record["statusName"], record["statusColor"], record["statusCategory"], record["isEnd"], record["statusSystem"] = state.Name, state.Color, state.Category, terminalCategory(state.Category), state.System
		}
		output = append(output, record)
	}
	write(w, 200, map[string]any{"items": output, "total": total, "limit": limit, "offset": offset, "query": q})
}

func excerpt(s, q string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > 96 {
		return string(r[:96]) + "…"
	}
	return s
}

func (a *App) stationNotify(project, recipient, actor, event, subject string, subjectID int64, title, body, dedupe string) {
	if recipient == "" || recipient == actor {
		return
	}
	if !a.accessibleProjectIDs(recipient)[project] {
		return
	}
	_, _ = a.db.Exec(`INSERT OR IGNORE INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, project, recipient, actor, event, subject, subjectID, title, body, time.Now().UTC().Format(time.RFC3339), dedupe)
}

func (a *App) actorName() string {
	var name string
	_ = a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&name)
	if name == "" {
		return a.uid()
	}
	return name
}

func (a *App) recordWorkflowEvent(exec statementExecutor, recipient, event, subject string, subjectID int64, title, body, dedupe string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	payload := jsonText(map[string]any{"subjectType": subject, "subjectId": subjectID, "title": title, "body": body})
	if _, err := exec.Exec(`INSERT OR IGNORE INTO notification_outbox(tenant_id,project_id,event_type,payload,dedupe_key,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), event, payload, dedupe, now, now); err != nil {
		return err
	}
	if recipient == "" {
		return nil
	}
	_, err := exec.Exec(`INSERT OR IGNORE INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), recipient, a.uid(), event, subject, subjectID, title, body, now, dedupe+":"+recipient)
	return err
}

func notificationURL(typ string, id int64) string {
	switch typ {
	case "requirement":
		return "/requirements?req=" + fmt.Sprint(id)
	case "defect":
		return "/defects?bug=" + fmt.Sprint(id)
	case "sprint":
		return "/iterations?sprint=" + fmt.Sprint(id)
	case "test_case":
		return "/tests?tab=cases&case=" + fmt.Sprint(id)
	case "test_plan":
		return "/tests?tab=plans&plan=" + fmt.Sprint(id)
	case "test_execution":
		return "/tests?tab=executions&execution=" + fmt.Sprint(id)
	case "project":
		return "/projects"
	case "user":
		return "/profile"
	}
	return "/notifications"
}

func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	uid := a.uid()
	readFilter := r.URL.Query().Get("read")
	if readFilter != "" && readFilter != "unread" && readFilter != "read" {
		fail(w, 400, "invalid_notification_filter", "通知已读状态筛选无效")
		return
	}
	event := r.URL.Query().Get("eventType")
	group := r.URL.Query().Get("group")
	if !validNotificationGroup(group) {
		fail(w, 400, "invalid_notification_group", "通知分类无效")
		return
	}
	pid := r.URL.Query().Get("project")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	q := `SELECT n.id,n.project_id,p.name,n.actor_user_id,COALESCE(u.name,''),n.event_type,n.subject_type,n.subject_id,n.title,n.body,n.read_at,n.created_at FROM user_notifications n JOIN projects p ON p.id=n.project_id AND p.tenant_id=n.tenant_id LEFT JOIN users u ON u.id=n.actor_user_id AND u.tenant_id=n.tenant_id WHERE n.tenant_id=? AND n.recipient_user_id=?` + visibleNotificationSQL
	args := []any{tenantID, uid}
	if pid != "" {
		q += " AND n.project_id=?"
		args = append(args, pid)
	}
	if event != "" {
		q += " AND n.event_type=?"
		args = append(args, event)
	}
	if readFilter == "unread" {
		q += " AND n.read_at IS NULL"
	} else if readFilter == "read" {
		q += " AND n.read_at IS NOT NULL"
	}
	if group != "" {
		q += " AND (" + notificationGroupSQL + ")=?"
		args = append(args, group)
	}
	// 列表、总数及分类角标读取同一快照，避免并发标记时互相矛盾。
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failNotificationState(w, err)
		return
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM ("+q+")", args...).Scan(&total); err != nil {
		fail(w, 503, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	q += " ORDER BY n.created_at DESC,n.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := tx.QueryContext(r.Context(), q, args...)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, sid int64
		var project, pname, actorID, actor, eventType, subject, title, body, created string
		var readAt sql.NullString
		if err := rows.Scan(&id, &project, &pname, &actorID, &actor, &eventType, &subject, &sid, &title, &body, &readAt, &created); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
			return
		}
		title, body = localizedNotification(w.Header().Get("Content-Language"), eventType, title, body)
		items = append(items, map[string]any{"id": id, "projectId": project, "projectName": pname, "actorUserId": actorID, "actor": actor, "eventType": eventType, "subjectType": subject, "subjectId": sid, "title": title, "body": body, "readAt": readAt.String, "createdAt": created, "url": notificationURL(subject, sid)})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	var unread int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM user_notifications n WHERE n.tenant_id=? AND n.recipient_user_id=? AND n.read_at IS NULL`+visibleNotificationSQL, tenantID, uid).Scan(&unread); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	groupQuery := `SELECT ` + notificationGroupSQL + `,COUNT(*) FROM user_notifications n WHERE n.tenant_id=? AND n.recipient_user_id=? AND n.read_at IS NULL` + visibleNotificationSQL
	groupArgs := []any{tenantID, uid}
	if pid != "" {
		groupQuery += " AND n.project_id=?"
		groupArgs = append(groupArgs, pid)
	}
	groupRows, err := tx.QueryContext(r.Context(), groupQuery+" GROUP BY 1", groupArgs...)
	if err != nil {
		fail(w, 503, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	defer groupRows.Close()
	groupUnread := map[string]int{"mentions": 0, "handoffs": 0, "changes": 0, "activity": 0}
	for groupRows.Next() {
		var key string
		var count int
		if err := groupRows.Scan(&key, &count); err != nil {
			fail(w, 503, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
			return
		}
		groupUnread[key] = count
	}
	if err := groupRows.Err(); err != nil {
		fail(w, 503, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	groupRows.Close()
	if err := tx.Commit(); err != nil {
		failNotificationState(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "unread": unread, "groupUnread": groupUnread, "total": total, "limit": limit, "offset": offset, "hasMore": offset+len(items) < total})
}

func (a *App) notification(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/")
	if path == "unread-count" && r.Method == "GET" {
		var n int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM user_notifications n WHERE n.tenant_id=? AND n.recipient_user_id=? AND n.read_at IS NULL`+visibleNotificationSQL, tenantID, a.uid()).Scan(&n); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
			return
		}
		write(w, 200, map[string]any{"unread": n})
		return
	}
	if path == "read-all" || path == "bulk-read" {
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed", "不支持的方法")
			return
		}
		a.notificationBatchState(w, r, path == "read-all")
		return
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		fail(w, 400, "invalid_id", "通知编号不正确")
		return
	}
	if r.Method == "PATCH" {
		var b struct {
			Read *bool `json:"read"`
		}
		if decodeNotificationState(w, r, &b) != nil || b.Read == nil || id <= 0 {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		result, err := a.setNotificationState(r.Context(), []int64{id}, *b.Read, false)
		if err != nil {
			failNotificationState(w, err)
			return
		}
		write(w, 200, map[string]any{"id": id, "read": *b.Read, "updated": result.Updated, "unread": result.Unread})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
