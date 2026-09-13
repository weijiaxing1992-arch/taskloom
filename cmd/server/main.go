package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// 当前进程服务固定企业；数据库带企业隔离字段不代表已实现任意企业的动态路由。
// 后续多企业改造须同时检查认证、所有查询、通知与后台任务，不能只替换这一常量。
const tenantID = "tn_acme"
const projectID = "prj_orbit"
const tenantName = "星河示例企业"
const projectName = "星河示例研发"

// App 保存共享连接池/客户端和请求上下文。scopedAPI 按请求复制上下文后填入有效用户、项目；
// 不可在全局实例上修改 user/project，否则并发请求可能串身份。
type App struct {
	db            *sql.DB
	web           string
	databasePath  string
	startedAt     time.Time
	project       string
	user          string
	sessionToken  string
	signingKey    []byte
	sessionTTL    time.Duration
	cookieSecure  bool
	passwordCost  int
	apiSlots      chan struct{}
	seedDemo      bool
	impersonation *impersonationContext
	wecomKey      []byte
	wecomHTTP     *http.Client
	// 企业微信自建应用的 access_token 只驻留在进程内存中。使用指针是因为
	// 请求处理会复制 App 的身份上下文，不能把已使用的互斥锁按值复制。
	wecomAppTokens *wecomAppTokenCache
	wechatHTTP     *http.Client
	aiHTTP         *http.Client
}

// pid/uid 的默认值兼容启动迁移与测试，不能作为未认证请求的授权依据。
func (a *App) pid() string {
	if a.project != "" {
		return a.project
	}
	return projectID
}
func (a *App) uid() string {
	if a.user != "" {
		return a.user
	}
	return "u_admin"
}

// Requirement 是 API 传输模型：稳定人员 ID 与显示名并存以兼容旧客户端；
// 富文档为受限 JSON，字段/附件/人员关联的权限与完整性由对应写入服务检查。
type Requirement struct {
	TapdImport                *tapdImport           `json:"tapdImport,omitempty"`
	ID                        int64                 `json:"id"`
	Code                      string                `json:"code,omitempty"`
	Title                     string                `json:"title"`
	Type                      string                `json:"type"`
	Description               string                `json:"description"`
	DescriptionDoc            json.RawMessage       `json:"descriptionDoc"`
	Acceptance                string                `json:"acceptance"`
	ParentID                  *int64                `json:"parentId"`
	Category                  string                `json:"category"`
	Sprint                    string                `json:"sprint"`
	IterationDelayCount       int                   `json:"iterationDelayCount"`
	Status                    string                `json:"status"`
	StatusName                string                `json:"statusName"`
	StatusColor               string                `json:"statusColor"`
	StatusCategory            string                `json:"statusCategory"`
	StatusSystem              bool                  `json:"statusSystem"`
	IsEnd                     bool                  `json:"isEnd"`
	Priority                  string                `json:"priority"`
	Owner                     string                `json:"owner"`
	Assignee                  string                `json:"assignee"`
	AssigneeUserID            string                `json:"assigneeUserId,omitempty"`
	AssigneeUserIDs           []string              `json:"assigneeUserIds"`
	Assignees                 []RequirementAssignee `json:"assignees"`
	DescriptionMentionUserIDs []string              `json:"descriptionMentionUserIds"`
	RemarksMentionUserIDs     []string              `json:"remarksMentionUserIds"`
	DescriptionMentionNames   map[string]string     `json:"descriptionMentionNames"`
	RemarksMentionNames       map[string]string     `json:"remarksMentionNames"`
	OwnerUserID               string                `json:"ownerUserId,omitempty"`
	OwnerUserIDs              []string              `json:"ownerUserIds"`
	Owners                    []RequirementAssignee `json:"owners"`
	Tags                      string                `json:"tags"`
	TagColors                 map[string]string     `json:"tagColors"`
	RoleWeights               map[string]RoleWeight `json:"roleWeights"`
	WeightTotal               float64               `json:"weightTotal"`
	Remarks                   string                `json:"remarks"`
	StartDate                 string                `json:"startDate"`
	EndDate                   string                `json:"endDate"`
	Discipline                string                `json:"discipline"`
	Progress                  int                   `json:"progress"`
	EstimatedHours            float64               `json:"estimatedHours"`
	ActualHours               float64               `json:"actualHours"`
	Sensitive                 bool                  `json:"sensitive"`
	AuthImpact                bool                  `json:"authImpact"`
	CreatedAt                 string                `json:"createdAt"`
	UpdatedAt                 string                `json:"updatedAt,omitempty"`
	// DependencyStatus 仅在需求列表按依赖状态筛选或排序时返回。依赖目标可能跨项目，
	// 因此由受限的批量查询生成，不能由客户端根据关联数据自行推断。
	DependencyStatus *requirementDependencyState `json:"dependencyStatus,omitempty"`
	CustomFields     map[string]any              `json:"customFields,omitempty"`
}
type Comment struct {
	commentReply
	ID             int64           `json:"id"`
	RequirementID  int64           `json:"requirementId"`
	Author         string          `json:"author"`
	AuthorUserID   string          `json:"authorUserId"`
	Body           string          `json:"body"`
	ContentDoc     json.RawMessage `json:"contentDoc"`
	CreatedAt      string          `json:"createdAt"`
	MentionUserIDs []string        `json:"mentionUserIds"`
}

func main() {
	_ = os.Setenv("TASKLOOM_COMMUNITY", "1")
	// 相对路径以进程工作目录为准。已有业务库升级前须核实 DB 路径并生成一致性备份，
	// 不能把新建的空路径误当原库；直接启动与拒绝空库的部署启动器有不同边界。
	dbPath := env("DEVFLOW_DB", "./data/devflow.db")
	db, err := openSQLiteDatabase(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	a := &App{
		db:             db,
		web:            env("DEVFLOW_WEB_DIR", "./dist"),
		databasePath:   dbPath,
		startedAt:      time.Now().UTC(),
		signingKey:     []byte(env("DEVFLOW_SESSION_SECRET", localSigningSecret)),
		sessionTTL:     30 * 24 * time.Hour,
		cookieSecure:   env("DEVFLOW_COOKIE_SECURE", "false") == "true",
		apiSlots:       make(chan struct{}, sqliteMaxAPIRequests),
		wecomAppTokens: &wecomAppTokenCache{},
	}
	if len(a.signingKey) < 32 {
		log.Fatal("DEVFLOW_SESSION_SECRET must contain at least 32 bytes")
	}
	if string(a.signingKey) == localSigningSecret {
		log.Print("warning: using local session signing secret; set DEVFLOW_SESSION_SECRET in production")
	}
	if err = a.migrate(); err != nil {
		log.Fatal(err)
	}
	// 数据库密文和主密钥必须匹配；已有密文却丢失密钥时中止启动，不静默重置配置。
	a.wecomKey, err = a.initializeWecomKey(env("DEVFLOW_WECOM_KEY_FILE", dbPath+".wecom-key"))
	if err != nil {
		log.Fatal(err)
	}
	a.wecomHTTP = newWecomHTTPClient()
	go a.runUserWecom(context.Background())
	go a.runWecomAppDeliveries(context.Background())
	go a.runReleaseNotes(context.Background())
	mux := http.NewServeMux()
	mux.Handle("/api/", a.scopedAPI())
	mux.Handle("/", spa(a.web))
	addr := env("DEVFLOW_ADDR", "127.0.0.1:8080")
	if err := a.checkCommunityBind(addr); err != nil {
		log.Fatal(err)
	}
	log.Printf("TaskLoom listening on %s", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           withJSON(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	// 当前入口没有显式信号驱动的优雅停机：发布时应预留维护窗口，避免中断正在保存的请求。
	log.Fatal(server.ListenAndServe())
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func (a *App) migrate() error {
	existing, err := a.databaseHasTable("tenants")
	if err != nil {
		return err
	}
	// 旧库没有 bootstrap 标记时不能补灌演示数据；只有全新库或上次初始化
	// 明确留下 pending 标记才允许继续。这保证中途失败后的重启可恢复。
	a.seedDemo = !existing
	if existing {
		pending, err := a.demoSeedPending()
		if err != nil {
			return err
		}
		a.seedDemo = pending
	}
	defer func() { a.seedDemo = false }()
	now := time.Now().UTC().Format(time.RFC3339)
	// 首次建库时先落下可恢复标记，再建其余表。这样即使多语句建表在中途
	// 遇到损坏的旧对象而提前退出，下一次也不会因为 tenants 已存在而遗漏 seed。
	if !existing {
		if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS migration_bootstrap(key TEXT PRIMARY KEY,state TEXT NOT NULL,updated_at TEXT NOT NULL)`); err != nil {
			return fmt.Errorf("创建初始化状态表: %w", err)
		}
		if err := a.markDemoSeedPending(now); err != nil {
			return fmt.Errorf("标记演示初始化: %w", err)
		}
	}
	schema := `PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS tenants(id TEXT PRIMARY KEY,name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS migration_bootstrap(key TEXT PRIMARY KEY,state TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,name TEXT NOT NULL,email TEXT NOT NULL,active INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS memberships(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,role TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id));
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,tenant_id TEXT NOT NULL,name TEXT NOT NULL,code TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS requirements(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,code TEXT NOT NULL,title TEXT NOT NULL,type TEXT NOT NULL DEFAULT '产品需求',description TEXT NOT NULL DEFAULT '',acceptance TEXT NOT NULL DEFAULT '',parent_id INTEGER,category TEXT NOT NULL DEFAULT '未分类',sprint TEXT NOT NULL DEFAULT '待规划',status TEXT NOT NULL DEFAULT '草稿',priority TEXT NOT NULL DEFAULT 'P2',owner TEXT NOT NULL DEFAULT '',assignee TEXT NOT NULL DEFAULT '',tags TEXT NOT NULL DEFAULT '',start_date TEXT NOT NULL DEFAULT '',end_date TEXT NOT NULL DEFAULT '',sensitive INTEGER NOT NULL DEFAULT 0,auth_impact INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_req_scope_status ON requirements(tenant_id,project_id,status);
CREATE TABLE IF NOT EXISTS comments(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,author TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS checklist_items(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,text TEXT NOT NULL,done INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS activities(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,requirement_id INTEGER NOT NULL,actor TEXT NOT NULL,event TEXT NOT NULL,detail TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS notification_outbox(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,event_type TEXT NOT NULL,payload TEXT NOT NULL,channel TEXT NOT NULL DEFAULT 'wecom',status TEXT NOT NULL DEFAULT 'mock_pending',created_at TEXT NOT NULL);`
	if _, e := a.db.Exec(schema); e != nil {
		return e
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	users := [][]string{}
	if a.seedDemo {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tenants(id,name)VALUES(?,?)`, tenantID, tenantName); err != nil {
			return fmt.Errorf("初始化企业: %w", err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO projects(id,tenant_id,name,code)VALUES(?,?,?,?)`, a.pid(), tenantID, projectName, "ORBIT"); err != nil {
			return fmt.Errorf("初始化项目: %w", err)
		}
		users = [][]string{{"u_admin", "林夏", "linxia@devflow.local", "admin"}, {"u_member", "周屿", "zhouyu@devflow.local", "member"}, {"u_pm", "陈澄", "chencheng@devflow.local", "member"}}
	}
	for _, u := range users {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users(id,tenant_id,name,email)VALUES(?,?,?,?)`, u[0], tenantID, u[1], u[2]); err != nil {
			return fmt.Errorf("初始化成员 %s: %w", u[0], err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO memberships VALUES(?,?,?,?)`, tenantID, a.pid(), u[0], u[3]); err != nil {
			return fmt.Errorf("初始化项目成员 %s: %w", u[0], err)
		}
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM requirements`).Scan(&n); err != nil {
		return fmt.Errorf("检查初始需求: %w", err)
	}
	if a.seedDemo && n == 0 {
		samples := []Requirement{{Title: "统一登录与企业身份切换", Category: "待定", Sprint: "V1.0", Status: "开发中", Priority: "P0", Owner: "林夏", Assignee: "周屿", Tags: "账号,多租户"}, {Title: "需求详情支持行内更新核心字段", Category: "待定", Sprint: "V1.0", Status: "评审中", Priority: "P1", Owner: "陈澄", Assignee: "周屿", Tags: "体验,需求"}, {Title: "企业微信状态变化通知", Category: "待定", Sprint: "V1.1", Status: "规划中", Priority: "P1", Owner: "林夏", Assignee: "", Tags: "企业微信"}, {Title: "检查清单与验收标准联动", Category: "待定", Sprint: "V1.0", Status: "待开发", Priority: "P2", Owner: "陈澄", Assignee: "周屿", Tags: "验收"}, {Title: "权限认证影响标记", Category: "待定", Sprint: "V1.0", Status: "测试中", Priority: "P0", Owner: "林夏", Assignee: "周屿", Tags: "安全"}, {Title: "需求列表组合筛选与排序", Category: "待定", Sprint: "V1.0", Status: "已完成", Priority: "P1", Owner: "陈澄", Assignee: "周屿", Tags: "列表,效率"}}
		for _, r := range samples {
			res, err := tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,description,acceptance,category,sprint,status,priority,owner,assignee,tags,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", r.Title, "围绕「"+r.Title+"」完成可验证的产品能力。", "核心路径可用；异常状态有反馈；操作会记录活动。", r.Category, r.Sprint, r.Status, r.Priority, r.Owner, r.Assignee, r.Tags, now, now)
			if err != nil {
				return fmt.Errorf("初始化需求 %q: %w", r.Title, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("读取初始需求 ID: %w", err)
			}
			code, err := requirementSerialCode(id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE requirements SET code=? WHERE id=?`, code, id); err != nil {
				return fmt.Errorf("设置初始需求编号: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := a.migrateV2(); err != nil {
		return err
	}
	if err := a.migrateRequirementTestCaseTraceability(); err != nil {
		return err
	}
	if err := a.migrateRequirementPlanning(); err != nil {
		return err
	}
	if err := a.migrateRequirementCollaboration(); err != nil {
		return err
	}
	if err := a.migrateRequirementCategoryPresets(); err != nil {
		return err
	}
	if err := a.migrateRequirementCategoryOrdering(); err != nil {
		return err
	}
	if err := a.migrateRequirementPeople(); err != nil {
		return err
	}
	if err := a.migrateRequirementResources(); err != nil {
		return err
	}
	if err := a.migrateTapdImports(); err != nil {
		return err
	}
	if err := a.migrateRequirementRichDocuments(); err != nil {
		return err
	}
	if err := a.migrateRequirementFavorites(); err != nil {
		return err
	}
	if err := a.migrateRequirementShares(); err != nil {
		return err
	}
	if err := a.migrateRequirementDependencies(); err != nil {
		return err
	}
	if err := a.migrateRequirementStates(); err != nil {
		return err
	}
	if err := a.migrateAutomationRules(); err != nil {
		return err
	}
	if err := a.migrateOrganizationAdministration(); err != nil {
		return err
	}
	if err := a.migrateProjectMemberRoles(); err != nil {
		return err
	}
	if err := a.migrateQualityCollaboration(); err != nil {
		return err
	}
	if err := a.migrateCommentThreads(); err != nil {
		return err
	}
	if err := a.migrateDefaultTestersField(); err != nil {
		return err
	}
	if err := a.migrateRequirementEditorFields(); err != nil {
		return err
	}
	if err := a.migrateAI(); err != nil {
		return err
	}
	if err := a.migrateReleaseNotes(); err != nil {
		return err
	}
	if err := a.migrateTestingWorkspace(); err != nil {
		return err
	}
	if err := a.migrateTestCaseAIReview(); err != nil {
		return err
	}
	if err := a.migrateDatabaseScale(); err != nil {
		return err
	}
	if err := a.migrateAuditHistory(); err != nil {
		return err
	}
	if err := a.migrateRequirementHistory(); err != nil {
		return err
	}
	if err := a.migratePrivateDrafts(); err != nil {
		return err
	}
	if err := a.migrateRequirementTemplates(); err != nil {
		return err
	}
	if err := a.migrateUserWecom(); err != nil {
		return err
	}
	if err := a.migrateWechatLogin(); err != nil {
		return err
	}
	if err := a.migrateWecomCustomApp(); err != nil {
		return err
	}
	if err := a.migrateIntegrations(); err != nil {
		return err
	}
	if a.seedDemo {
		if err := a.initializeCommunity(); err != nil {
			return err
		}
		if err := a.completeDemoSeed(time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("完成演示初始化: %w", err)
		}
	}
	return nil
}

// withJSON 统一请求体限额和错误语言；正文/文件使用单独限额，不能给所有 API 放开大包。
// MaxBytesReader 同时限制没有可信 Content-Length 的流式请求。
func withJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			setResponseLocale(w, negotiatedLocale(r.Header.Get("Accept-Language"), ""))
			maxSize := int64(2 << 20)
			if isRichDocumentWrite(r) {
				maxSize = richRequestMaxSize
			}
			if isRequirementAttachmentUpload(r) {
				maxSize = requirementAttachmentRequestMaxSize
			}
			if isPrivateDraftWrite(r) {
				maxSize = privateDraftRequestMaxSize
			}
			if r.ContentLength > maxSize {
				if isPrivateDraftWrite(r) {
					fail(w, http.StatusRequestEntityTooLarge, "draft_too_large", "单份草稿不能超过 30 MB")
				} else if isRequirementAttachmentUpload(r) {
					fail(w, http.StatusRequestEntityTooLarge, "attachment_too_large", "单个附件不能超过 10 MB")
				} else if isRichDocumentWrite(r) {
					fail(w, http.StatusRequestEntityTooLarge, "request_too_large", "富文本内容不能超过 30 MB")
				} else {
					fail(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求内容不能超过 2 MB")
				}
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-TaskLoom-Project,X-TaskLoom-Expected-User,Accept-Language")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, status int, v any) {
	setResponseLocale(w, w.Header().Get("Content-Language"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, msg string) {
	msg = localizedError(w.Header().Get("Content-Language"), status, code, msg)
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// health 只证明 HTTP 进程存活，不证明数据库可写、通知已送达或全部依赖正常。
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]string{"status": "ok"})
}
func (a *App) session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	uid := a.uid()
	var name, role, avatarColor, locale, timezone string
	var operationDisabled, mustChange bool
	e := a.db.QueryRowContext(r.Context(), `SELECT u.name,COALESCE(m.role,tm.role),u.avatar_color,u.locale,u.timezone,u.operation_disabled,u.must_change_password FROM users u JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=u.tenant_id AND m.project_id=? WHERE u.id=? AND u.tenant_id=? AND u.active=1 AND (m.user_id IS NOT NULL OR tm.role='tenant_admin')`, a.pid(), uid, tenantID).Scan(&name, &role, &avatarColor, &locale, &timezone, &operationDisabled, &mustChange)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	if e != nil {
		fail(w, 401, "unauthorized", "账号不可用")
		return
	}
	if operationDisabled {
		a.operationDisabledSession(w, r, uid)
		return
	}
	// 受限代看不会改变成员的首改密码状态；它只在服务端已锁定全部写
	// 请求时临时跳过这一个界面门禁，给企业管理员核对成员实际可见范围。
	// 普通登录和常规代访问仍必须先完成首改密码。
	if mustChange && !(a.impersonation != nil && a.impersonation.ReadOnly) {
		a.initialPasswordSession(w, r, uid, a.impersonation)
		return
	}
	var pn, pc, tn string
	if err := a.db.QueryRowContext(r.Context(), `SELECT p.name,p.code,t.name FROM projects p JOIN tenants t ON t.id=p.tenant_id WHERE p.id=? AND p.tenant_id=?`, a.pid(), tenantID).Scan(&pn, &pc, &tn); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "项目资料暂时无法读取，请稍后重试")
		return
	}
	canImpersonate, err := a.isTenantAdminChecked(uid)
	if err != nil {
		fail(w, 503, "database_unavailable", "账号服务暂时繁忙，请稍后重试")
		return
	}
	organizationGrants, _, err := a.organizationAccess(r.Context(), a.db)
	if err != nil {
		failOrganization(w, err)
		return
	}
	permissionKeys := []string{}
	for _, permission := range organizationPermissions {
		if organizationGrants[permission.Key] {
			permissionKeys = append(permissionKeys, permission.Key)
		}
	}
	projectRoles, err := memberProjectRoles(r.Context(), a.db, a.pid(), uid)
	if err != nil {
		failOrganization(w, err)
		return
	}
	// A pending first-password target can only reach this normal workspace
	// session through the explicitly read-only delegation path. Expose the
	// limitation on impersonation instead of presenting the password-change
	// screen, which would otherwise make the authorised review unusable. The
	// underlying user row is never changed and all non-GET requests are denied
	// earlier by resolveImpersonation.
	mustChangeForSession := mustChange && !(a.impersonation != nil && a.impersonation.ReadOnly)
	write(w, 200, map[string]any{"tenant": map[string]string{"id": tenantID, "name": tn}, "project": map[string]string{"id": a.pid(), "name": pn, "code": pc}, "user": map[string]any{"id": uid, "name": name, "role": role, "projectRoles": projectRoles, "avatarColor": avatarColor, "locale": storedLocale(locale), "timezone": timezone, "operationDisabled": false, "mustChangePassword": mustChangeForSession}, "supportedLocales": supportedLocales, "impersonation": a.impersonation, "canImpersonate": canImpersonate && a.impersonation == nil, "organizationPermissions": permissionKeys})
}
func (a *App) meta(w http.ResponseWriter, r *http.Request) {
	sprints := []string{"待规划"}
	rows, err := a.db.QueryContext(r.Context(), `SELECT name FROM sprints WHERE tenant_id=? AND project_id=? AND status IN ('规划中','进行中') ORDER BY start_date DESC,id DESC`, tenantID, a.pid())
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代列表暂时无法读取，请稍后重试")
			return
		}
		sprints = append(sprints, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代列表暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	categories, err := a.listRequirementCategories()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	statusItems, err := listRequirementStatuses(r.Context(), a.db, tenantID, a.pid())
	if err != nil {
		failState(w, err)
		return
	}
	statuses := []string{}
	for _, status := range statusItems {
		if status.Enabled {
			statuses = append(statuses, status.Key)
		}
	}
	write(w, 200, map[string]any{"statuses": statuses, "statusDefinitions": statusItems, "priorities": []string{"P0", "P1", "P2", "P3"}, "sprints": sprints, "categories": categories})
}
func (a *App) requirements(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		a.create(w, r)
		return
	}
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	listQuery, queryErr := a.parseRequirementListQuery(r)
	if queryErr != nil {
		failWorkQuery(w, queryErr)
		return
	}
	pageQuery, queryErr := parseRequirementPage(r)
	if queryErr != nil {
		failWorkQuery(w, queryErr)
		return
	}
	q := ` FROM requirements WHERE tenant_id=? AND project_id=?`
	args := []any{tenantID, a.pid()}
	switch r.URL.Query().Get("mine") {
	case "", "0", "false":
	case "1", "true":
		predicate, relatedArgs := workItemRelatedSQL("requirement", "requirements", a.uid())
		q += " AND " + predicate
		args = append(args, relatedArgs...)
	default:
		fail(w, 422, "invalid_requirement_filter", "与我相关筛选无效")
		return
	}
	statusItems, err := listRequirementStatuses(r.Context(), a.db, tenantID, a.pid())
	if err != nil {
		failState(w, err)
		return
	}
	statusMap := requirementCategoryMap(statusItems)
	known := map[string]bool{}
	for key := range statusMap {
		known[key] = true
	}
	selected, err := parseStatusSelection(r, known)
	if err != nil {
		failState(w, err)
		return
	}
	q, args = addStatusSelection(q, args, selected)
	if category := r.URL.Query().Get("statusCategory"); category != "" {
		if !validChoice(category, []string{"todo", "doing", "done", "cancelled"}) {
			failState(w, invalidState("状态类别无效"))
			return
		}
		q += ` AND EXISTS(SELECT 1 FROM requirement_statuses rs WHERE rs.tenant_id=requirements.tenant_id AND rs.project_id=requirements.project_id AND rs.key=requirements.status AND rs.category=?)`
		args = append(args, category)
	}
	for _, f := range []struct{ k, c string }{{"priority", "priority"}, {"category", "category"}} {
		if v := r.URL.Query().Get(f.k); v != "" {
			q += " AND " + f.c + "=?"
			args = append(args, v)
		}
	}
	if assignee := r.URL.Query().Get("assignee"); assignee != "" {
		q += ` AND (assignee=? OR EXISTS(SELECT 1 FROM json_each(requirements.assignee_user_ids_json) picked LEFT JOIN users u ON u.id=picked.value AND u.tenant_id=requirements.tenant_id WHERE picked.value=? OR u.name=?))`
		args = append(args, assignee, assignee, assignee)
	}
	if assigneeID := r.URL.Query().Get("assigneeUserId"); assigneeID != "" {
		q += ` AND (assignee_user_id=? OR EXISTS(SELECT 1 FROM json_each(requirements.assignee_user_ids_json) picked WHERE picked.value=?))`
		args = append(args, assigneeID, assigneeID)
	}
	if value := r.URL.Query().Get("sprint"); value != "" {
		canonical, err := a.resolveRequirementSprint(value, false)
		if err != nil {
			write(w, 200, pageQuery.response([]Requirement{}))
			return
		}
		q += ` AND sprint=?`
		args = append(args, canonical)
	}
	if s := r.URL.Query().Get("q"); s != "" {
		codeID := requirementCodeQueryID(s)
		q += ` AND (title LIKE ? OR code LIKE ? OR EXISTS (SELECT 1 FROM field_values fv JOIN field_definitions fd ON fd.id=fv.field_definition_id WHERE fv.tenant_id=requirements.tenant_id AND fv.project_id=requirements.project_id AND fv.object_type='requirement' AND fv.object_id=requirements.id AND fd.searchable=1 AND fv.value_json LIKE ?) OR instr(lower(` + workItemPeopleSearchSQL("requirement", "requirements") + `),lower(?))>0`
		args = append(args, "%"+s+"%", "%"+s+"%", "%"+s+"%", strings.TrimSpace(s))
		if codeID > 0 {
			q += ` OR requirements.id=?`
			args = append(args, codeID)
		}
		q += `)`
	}
	sqlPage, e := a.readRequirementSQLPage(r.Context(), r, pageQuery, listQuery, q, args)
	if e != nil {
		failWorkQuery(w, e)
		return
	}
	items := []Requirement{}
	if sqlPage != nil {
		items = sqlPage.items
	} else {
		rows, err := a.db.QueryContext(r.Context(), `SELECT `+pageQuery.selectColumns()+q+" ORDER BY id", args...)
		if err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
		defer rows.Close()
		for rows.Next() {
			var x Requirement
			if err := scanRequirement(rows, &x); err != nil {
				fail(w, 500, "db_error", err.Error())
				return
			}
			items = append(items, x)
		}
		if err := rows.Err(); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		rows.Close()
	}
	if err := a.hydrateRequirementBatch(r.Context(), a.db, items); err != nil {
		failWorkQuery(w, err)
		return
	}
	// 依赖状态是计算字段。仅在它参与筛选或排序时加载，避免普通需求池列表
	// 为没有使用依赖功能的项目额外读取整张依赖表。
	if requirementListNeedsDependencyState(listQuery) {
		if err := a.hydrateRequirementDependencyStates(r.Context(), items); err != nil {
			failWorkQuery(w, err)
			return
		}
	}
	hydrated := make([]Requirement, 0, len(items))
	for _, x := range items {
		if state, ok := statusMap[x.Status]; ok {
			x.StatusSystem = state.System
			x.StatusName, x.StatusColor, x.StatusCategory, x.IsEnd = state.Name, state.Color, state.Category, terminalCategory(state.Category)
		} else {
			x.StatusName, x.StatusCategory = x.Status, "todo"
		}
		matched := true
		for key, values := range r.URL.Query() {
			if strings.HasPrefix(key, "cf.") && len(values) > 0 && fmt.Sprint(x.CustomFields[strings.TrimPrefix(key, "cf.")]) != values[0] {
				matched = false
			}
		}
		if !matched {
			continue
		}
		hydrated = append(hydrated, x)
	}
	if sqlPage != nil {
		write(w, 200, sqlPage.response(hydrated))
		return
	}
	items, e = a.applyRequirementListQuery(hydrated, listQuery)
	if e != nil {
		failWorkQuery(w, e)
		return
	}
	write(w, 200, pageQuery.response(items))
}
func (a *App) create(w http.ResponseWriter, r *http.Request) {
	a.createRequirement(w, r)
}
func defaults(x *Requirement) {
	if x.Type == "" {
		x.Type = "产品需求"
	}
	if x.Category == "" {
		x.Category = "未分类"
	}
	if x.Sprint == "" {
		x.Sprint = "待规划"
	}
	if x.Priority == "" {
		x.Priority = "P2"
	}
	if x.Status == "" {
		x.Status = "草稿"
	}
	if x.Discipline == "" {
		x.Discipline = "product"
	}
}
func (a *App) requirement(w http.ResponseWriter, r *http.Request) {
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/requirements/"), "/")
	parts := strings.Split(p, "/")
	id, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil {
		fail(w, 400, "invalid_id", "需求编号不正确")
		return
	}
	if len(parts) > 1 {
		if !a.requireEntity(w, "requirement", id) {
			return
		}
		switch parts[1] {
		case "export":
			a.requirementExport(w, r, id, parts[2:])
		case "ai-test-cases":
			a.requirementAITestCases(w, r, id, parts[2:])
		case "test-cases":
			a.requirementTestCases(w, r, id, parts[2:])
		case "transitions":
			a.requirementTransitions(w, r, id)
		case "favorite":
			a.requirementFavorite(w, r, id)
		case "attachments":
			a.requirementAttachments(w, r, id, parts[2:])
		case "design-links":
			a.requirementDesignLinks(w, r, id, parts[2:])
		case "links":
			a.requirementLinks(w, r, id, parts[2:])
		case "dependencies":
			a.requirementDependencies(w, r, id, parts[2:])
		case "comments":
			a.comments(w, r, id)
		case "checklist":
			a.checklist(w, r, id)
		case "activities":
			a.requirementHistory(w, r, id)
		default:
			fail(w, 404, "not_found", "资源不存在")
		}
		return
	}
	if r.Method == "GET" {
		x, e := a.get(id)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				fail(w, 404, "not_found", "需求不存在")
			} else {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			}
			return
		}
		write(w, 200, x)
		return
	}
	if r.Method == "PATCH" {
		a.patchRequirement(w, r, id)
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
func (a *App) get(id int64) (Requirement, error) {
	var x Requirement
	e := scanRequirement(a.db.QueryRow(`SELECT `+requirementSelectColumns+` FROM requirements WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()), &x)
	if e != nil {
		return x, e
	}
	if err := a.loadRequirementAssignees(a.db, &x); err != nil {
		return x, err
	}
	if err := a.hydrateRequirementState(context.Background(), &x); err != nil {
		return x, err
	}
	x.CustomFields = a.customFields("requirement", id)
	return x, e
}
func (a *App) comments(w http.ResponseWriter, r *http.Request, id int64) {
	a.requirementComments(w, r, id)
}
func (a *App) checklist(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method == "POST" {
		var b struct {
			Text string `json:"text"`
		}
		if decodeJSON(r, &b) != nil {
			fail(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
			return
		}
		if strings.TrimSpace(b.Text) == "" {
			fail(w, 422, "validation_error", "检查项不能为空")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法保存，请稍后重试")
			return
		}
		defer tx.Rollback()
		res, err := tx.ExecContext(r.Context(), `INSERT INTO checklist_items(tenant_id,project_id,requirement_id,text)VALUES(?,?,?,?)`, tenantID, a.pid(), id, b.Text)
		var cid int64
		if err == nil {
			cid, err = res.LastInsertId()
		}
		if err == nil {
			err = a.recordRequirementChecklistHistory(tx, id, nil, map[string]any{"id": cid, "text": b.Text, "done": false})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法保存，请稍后重试")
			return
		}
		write(w, 201, map[string]any{"id": cid, "text": b.Text, "done": false})
		return
	}
	if r.Method == "PATCH" {
		var b struct {
			ID   int64 `json:"id"`
			Done bool  `json:"done"`
		}
		if decodeJSON(r, &b) != nil {
			fail(w, http.StatusBadRequest, "invalid_json", "请求格式不正确")
			return
		}
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法保存，请稍后重试")
			return
		}
		defer tx.Rollback()
		var text string
		var done bool
		err = tx.QueryRowContext(r.Context(), `SELECT text,done FROM checklist_items WHERE id=? AND tenant_id=? AND project_id=? AND requirement_id=?`, b.ID, tenantID, a.pid(), id).Scan(&text, &done)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "not_found", "检查项不存在")
			return
		}
		if err == nil && done != b.Done {
			_, err = tx.ExecContext(r.Context(), `UPDATE checklist_items SET done=? WHERE id=? AND tenant_id=? AND project_id=? AND requirement_id=?`, b.Done, b.ID, tenantID, a.pid(), id)
			if err == nil {
				err = a.recordRequirementChecklistHistory(tx, id, map[string]any{"id": b.ID, "text": text, "done": done}, map[string]any{"id": b.ID, "text": text, "done": b.Done})
			}
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法保存，请稍后重试")
			return
		}
	} else if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,text,done FROM checklist_items WHERE tenant_id=? AND project_id=? AND requirement_id=?`, tenantID, a.pid(), id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var cid int64
		var t string
		var d bool
		if err := rows.Scan(&cid, &t, &d); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法读取，请稍后重试")
			return
		}
		out = append(out, map[string]any{"id": cid, "text": t, "done": d})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "检查项暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) activity(id int64, actor, event, detail string) {
	a.db.Exec(`INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), id, actor, event, detail, time.Now().UTC().Format(time.RFC3339))
}
func (a *App) activities(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,actor,event,detail,created_at FROM activities WHERE tenant_id=? AND project_id=? AND requirement_id=? ORDER BY id DESC`, tenantID, a.pid(), id)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "动态暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var aid int64
		var actor, event, detail, at string
		if err := rows.Scan(&aid, &actor, &event, &detail, &at); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "动态暂时无法读取，请稍后重试")
			return
		}
		out = append(out, map[string]any{"id": aid, "actor": actor, "event": event, "detail": detail, "createdAt": at})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "动态暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) notify(event string, id int64, payload string) {
	subject := strings.SplitN(event, ".", 2)[0]
	if !validChoice(subject, []string{"requirement", "defect", "test_case", "test_plan", "test_execution", "sprint"}) {
		subject = "requirement"
	}
	body, _ := json.Marshal(map[string]any{"subjectType": subject, "subjectId": id, "value": payload})
	now := time.Now().UTC().Format(time.RFC3339)
	dedupe := fmt.Sprintf("%s:%s:%d:%s", a.pid(), event, id, payload)
	a.db.Exec(`INSERT OR IGNORE INTO notification_outbox(tenant_id,project_id,event_type,payload,dedupe_key,created_at,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), event, string(body), dedupe, now, now)
	recipients := []string{}
	if strings.Contains(event, "mentioned") {
		recipients, _ = a.commentMentionRecipients(payload, nil)
	} else if strings.Contains(event, "assigned") || strings.HasSuffix(event, ".assignee_changed") {
		// Display names are not unique. Address assignment events using the
		// saved stable identity of this exact tenant/project work item.
		var uid string
		if subject == "requirement" {
			_ = a.db.QueryRow(`SELECT assignee_user_id FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&uid)
		} else if subject == "defect" {
			_ = a.db.QueryRow(`SELECT assignee_user_id FROM defects WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&uid)
		}
		if uid != "" {
			recipients = append(recipients, uid)
		}
	} else if subject == "requirement" {
		var uid string
		_ = a.db.QueryRow(`SELECT assignee_user_id FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&uid)
		recipients = append(recipients, uid)
	} else {
		var assignee, verifier, status string
		_ = a.db.QueryRow(`SELECT assignee_user_id,verifier_user_id,status FROM defects WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&assignee, &verifier, &status)
		recipients = append(recipients, assignee)
		if status == "待验证" {
			recipients = append(recipients, verifier)
		}
	}
	seen := map[string]bool{}
	for _, recipient := range recipients {
		if recipient == "" || seen[recipient] {
			continue
		}
		seen[recipient] = true
		title := "工作项有新动态"
		if strings.Contains(event, "mentioned") {
			title = "你在评论中被提及"
		}
		if strings.Contains(event, "assigned") || strings.HasSuffix(event, ".assignee_changed") {
			title = "有新的工作项分配给你"
		}
		if subject == "defect" && strings.Contains(event, "status") {
			title = "缺陷状态已更新"
		}
		a.stationNotify(a.pid(), recipient, a.uid(), event, subject, id, title, payload, dedupe+":"+recipient)
	}
}
func (a *App) outbox(w http.ResponseWriter, r *http.Request) {
	_, _, role, active, userErr := a.currentUserState(r)
	if userErr != nil && !errors.Is(userErr, sql.ErrNoRows) {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "账号权限服务暂时繁忙，请稍后重试")
		return
	}
	if !active || (role != "tenant_admin" && role != "project_admin") {
		fail(w, 403, "admin_required", "仅管理员可查看和重试外发队列")
		return
	}
	if r.Method == "POST" {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notifications/outbox/"), "/")
		parts := strings.Split(path, "/")
		id, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil || len(parts) < 2 || parts[1] != "retry" {
			fail(w, 400, "invalid_id", "投递记录编号不正确")
			return
		}
		res, err := a.db.ExecContext(r.Context(), `UPDATE notification_outbox SET status='mock_pending',retry_count=retry_count+1,last_error='',updated_at=? WHERE id=? AND tenant_id=? AND project_id=? AND status='failed'`, time.Now().UTC().Format(time.RFC3339), id, tenantID, a.pid())
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法保存，请稍后重试")
			return
		}
		n, err := res.RowsAffected()
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法保存，请稍后重试")
			return
		}
		if n == 0 {
			fail(w, 409, "not_retryable", "仅失败记录可重试")
			return
		}
		write(w, 200, map[string]any{"id": id, "status": "mock_pending"})
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,event_type,payload,channel,status,retry_count,last_error,created_at,updated_at FROM notification_outbox WHERE tenant_id=? AND project_id=? ORDER BY id DESC LIMIT 100`, tenantID, a.pid())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var retry int
		var e, p, c, s, lastError, created, updated string
		if err := rows.Scan(&id, &e, &p, &c, &s, &retry, &lastError, &created, &updated); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
			return
		}
		var meta struct {
			SubjectType string `json:"subjectType"`
			SubjectID   int64  `json:"subjectId"`
			Requirement int64  `json:"requirementId"`
		}
		_ = json.Unmarshal([]byte(p), &meta)
		if meta.SubjectType == "" {
			meta.SubjectType = strings.SplitN(e, ".", 2)[0]
		}
		if meta.SubjectID == 0 {
			meta.SubjectID = meta.Requirement
		}
		out = append(out, map[string]any{"id": id, "eventType": e, "subjectType": meta.SubjectType, "subjectId": meta.SubjectID, "payload": localizedOutboxPayload(w.Header().Get("Content-Language"), e, p), "channel": c, "status": s, "retryCount": retry, "lastError": lastError, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "通知记录暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"mode": env("DEVFLOW_WECOM_MODE", "mock"), "items": out})
}
func (a *App) members(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		a.legacyMemberWrite(w, r)
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	uid := a.uid()
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.name,u.email,u.employee_no,u.department,u.active,u.must_change_password,COALESCE(tm.role,'member'),m.role,u.last_active,COALESCE(tm.status,''),EXISTS(SELECT 1 FROM project_members pm WHERE pm.tenant_id=m.tenant_id AND pm.project_id=m.project_id AND pm.user_id=m.user_id) FROM users u JOIN memberships m ON m.user_id=u.id AND m.tenant_id=u.tenant_id LEFT JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE u.tenant_id=? AND m.project_id=? ORDER BY u.active DESC,u.name`, tenantID, a.pid())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "成员列表暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, n, e, employeeNo, department, tenantRole, projectRole, lastActive, tenantStatus string
		var active, mustChangePassword, projectMember bool
		if err := rows.Scan(&id, &n, &e, &employeeNo, &department, &active, &mustChangePassword, &tenantRole, &projectRole, &lastActive, &tenantStatus, &projectMember); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "成员列表暂时无法读取，请稍后重试")
			return
		}
		active = active && tenantStatus == "active" && projectMember
		out = append(out, map[string]any{"id": id, "name": n, "email": e, "employeeNo": employeeNo, "department": department, "active": active, "mustChangePassword": mustChangePassword, "tenantRole": tenantRole, "projectRole": projectRole, "role": projectRole, "lastActive": lastActive, "isCurrent": id == uid})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "成员列表暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	if err := a.addMemberDepartmentIDs(out); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "成员列表暂时无法读取，请稍后重试")
		return
	}
	for _, member := range out {
		roles, err := memberProjectRoles(r.Context(), a.db, a.pid(), member["id"].(string))
		if err != nil {
			failOrganization(w, err)
			return
		}
		member["projectRoles"] = roles
	}
	write(w, 200, map[string]any{"items": out})
}

// spa 提供前端路由回退。发布目录只能存公开构建产物，不能放数据库、名单或密钥文件。
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	serveHTML := func(w http.ResponseWriter, r *http.Request, name string) {
		w.Header().Set("Cache-Control", "no-store, max-age=0, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		file, err := os.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		// 即使复制发布包保留了旧修改时间，也返回最新 HTML；哈希资源仍应保留给已打开页面。
		request := r.Clone(r.Context())
		request.Header.Del("If-Modified-Since")
		request.Header.Del("If-None-Match")
		http.ServeContent(w, request, filepath.Base(name), time.Time{}, file)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleanPath := filepath.Clean("/" + r.URL.Path)
		p := filepath.Join(dir, strings.TrimPrefix(cleanPath, "/"))
		info, err := os.Stat(p)
		// The public download portal owns its directory entry; other paths remain SPA routes.
		if cleanPath == "/portal" && err == nil && info.IsDir() {
			serveHTML(w, r, filepath.Join(p, "index.html"))
			return
		}
		if err == nil && !info.IsDir() {
			if strings.EqualFold(filepath.Ext(p), ".html") {
				serveHTML(w, r, p)
			} else {
				fs.ServeHTTP(w, r)
			}
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fs.ServeHTTP(w, r)
			return
		}
		// 缺失脚本/样式必须返回 404，不能用 HTML 冒充，否则懒加载会报难定位的 MIME 错误。
		if cleanPath == "/assets" || strings.HasPrefix(cleanPath, "/assets/") || filepath.Ext(cleanPath) != "" {
			http.NotFound(w, r)
			return
		}
		serveHTML(w, r, filepath.Join(dir, "index.html"))
	})
}
