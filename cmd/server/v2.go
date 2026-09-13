package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type FieldDefinition struct {
	ID           int64    `json:"id"`
	ObjectType   string   `json:"objectType"`
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	DepartmentID string   `json:"departmentId"`
	MemberRoles  []string `json:"memberRoles"`
	Description  string   `json:"description"`
	Required     bool     `json:"required"`
	Searchable   bool     `json:"searchable"`
	Filterable   bool     `json:"filterable"`
	ListVisible  bool     `json:"listVisible"`
	Enabled      bool     `json:"enabled"`
	SortOrder    int      `json:"sortOrder"`
	DefaultValue any      `json:"defaultValue,omitempty"`
	Options      []string `json:"options,omitempty"`
}

type Sprint struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Status    string `json:"status"`
	Capacity  int    `json:"capacity"`
	UpdatedAt string `json:"updatedAt"`
}

type Defect struct {
	ID                 int64          `json:"id"`
	Code               string         `json:"code"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	Steps              string         `json:"steps"`
	Actual             string         `json:"actual"`
	Expected           string         `json:"expected"`
	Environment        string         `json:"environment"`
	FoundVersion       string         `json:"foundVersion"`
	FixVersion         string         `json:"fixVersion"`
	Severity           string         `json:"severity"`
	Priority           string         `json:"priority"`
	Status             string         `json:"status"`
	Assignee           string         `json:"assignee"`
	AssigneeUserID     string         `json:"assigneeUserId,omitempty"`
	Verifier           string         `json:"verifier"`
	VerifierUserID     string         `json:"verifierUserId,omitempty"`
	Sprint             string         `json:"sprint"`
	Discipline         string         `json:"discipline"`
	Progress           int            `json:"progress"`
	EstimatedHours     float64        `json:"estimatedHours"`
	ActualHours        float64        `json:"actualHours"`
	RequirementID      *int64         `json:"requirementId"`
	Tags               string         `json:"tags"`
	SourceExecutionID  *int64         `json:"sourceExecutionId,omitempty"`
	SourcePlanID       *int64         `json:"sourcePlanId,omitempty"`
	SourceCaseID       *int64         `json:"sourceCaseId,omitempty"`
	SourcePlanName     string         `json:"sourcePlanName,omitempty"`
	SourceCaseTitle    string         `json:"sourceCaseTitle,omitempty"`
	SourceExecStatus   string         `json:"sourceExecutionStatus,omitempty"`
	AllowedTransitions []string       `json:"allowedTransitions"`
	UpdatedAt          string         `json:"updatedAt"`
	CustomFields       map[string]any `json:"customFields,omitempty"`
}

type TestCase struct {
	ID            int64      `json:"id"`
	Code          string     `json:"code"`
	Category      string     `json:"category"`
	Title         string     `json:"title"`
	Preconditions string     `json:"preconditions"`
	Steps         string     `json:"steps"`
	Expected      string     `json:"expected"`
	Priority      string     `json:"priority"`
	Status        string     `json:"status"`
	Owner         string     `json:"owner"`
	OwnerUserID   string     `json:"ownerUserId,omitempty"`
	CaseType      string     `json:"caseType"`
	Tags          string     `json:"tags"`
	Enabled       bool       `json:"enabled"`
	StepsDetail   []TestStep `json:"stepsDetail,omitempty"`
	RequirementID *int64     `json:"requirementId"`
	// Requirement 只返回同租户同项目的轻量摘要；关联记录异常时不回显原始外部 ID，
	// 防止历史脏数据成为跨项目读取入口。
	Requirement *RequirementReference `json:"requirement,omitempty"`
	// Metadata 是测试工作台的扩展字段；保留顶层旧字段以兼容原有测试计划和导出。
	Metadata     *TestingCaseMetadata `json:"metadata,omitempty"`
	ReviewStatus string               `json:"reviewStatus,omitempty"`
	UpdatedAt    string               `json:"updatedAt"`
	CustomFields map[string]any       `json:"customFields,omitempty"`
}
type TestStep struct {
	Order    int    `json:"order"`
	Action   string `json:"action"`
	Expected string `json:"expected"`
}

type TestPlan struct {
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Sprint  string `json:"sprint"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
	Owner   string `json:"owner"`
	// OwnerUserID is the immutable member reference for Owner.  Keeping the
	// display name as well preserves existing clients while notifications and
	// authorization never need to infer an identity from a mutable name.
	OwnerUserID    string  `json:"ownerUserId,omitempty"`
	StartDate      string  `json:"startDate"`
	EndDate        string  `json:"endDate"`
	Status         string  `json:"status"`
	Environment    string  `json:"environment"`
	ExecutorUserID string  `json:"executorUserId"`
	CaseIDs        []int64 `json:"caseIds"`
	UpdatedAt      string  `json:"updatedAt"`
}
type TestExecution struct {
	ID             int64  `json:"id"`
	PlanID         int64  `json:"planId"`
	CaseID         int64  `json:"caseId"`
	Status         string `json:"status"`
	Executor       string `json:"executor"`
	ExecutedAt     string `json:"executedAt"`
	Note           string `json:"note"`
	ActualResult   string `json:"actualResult"`
	ExecutorUserID string `json:"executorUserId,omitempty"`
	DefectID       *int64 `json:"defectId,omitempty"`
	CaseTitle      string `json:"caseTitle,omitempty"`
	PlanName       string `json:"planName,omitempty"`
}

func (a *App) migrateV2() error {
	schema := `
CREATE TABLE IF NOT EXISTS field_definitions(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,object_type TEXT NOT NULL,key TEXT NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',required INTEGER NOT NULL DEFAULT 0,searchable INTEGER NOT NULL DEFAULT 0,filterable INTEGER NOT NULL DEFAULT 0,list_visible INTEGER NOT NULL DEFAULT 0,enabled INTEGER NOT NULL DEFAULT 1,sort_order INTEGER NOT NULL DEFAULT 0,default_value TEXT NOT NULL DEFAULT 'null',options TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,object_type,key));
CREATE INDEX IF NOT EXISTS idx_field_defs_scope ON field_definitions(tenant_id,project_id,object_type,enabled,sort_order);
CREATE TABLE IF NOT EXISTS field_values(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,object_type TEXT NOT NULL,object_id INTEGER NOT NULL,field_definition_id INTEGER NOT NULL,value_json TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,object_type,object_id,field_definition_id));
CREATE INDEX IF NOT EXISTS idx_field_values_object ON field_values(tenant_id,project_id,object_type,object_id);
CREATE TABLE IF NOT EXISTS sprints(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,code TEXT NOT NULL,name TEXT NOT NULL,goal TEXT NOT NULL DEFAULT '',start_date TEXT NOT NULL,end_date TEXT NOT NULL,status TEXT NOT NULL DEFAULT '规划中',capacity INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_sprints_scope_status ON sprints(tenant_id,project_id,status);
CREATE TABLE IF NOT EXISTS defects(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,code TEXT NOT NULL,title TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',steps TEXT NOT NULL DEFAULT '',actual TEXT NOT NULL DEFAULT '',expected TEXT NOT NULL DEFAULT '',environment TEXT NOT NULL DEFAULT '',found_version TEXT NOT NULL DEFAULT '',fix_version TEXT NOT NULL DEFAULT '',severity TEXT NOT NULL DEFAULT '一般',priority TEXT NOT NULL DEFAULT 'P2',status TEXT NOT NULL DEFAULT '新建',assignee TEXT NOT NULL DEFAULT '',verifier TEXT NOT NULL DEFAULT '',sprint TEXT NOT NULL DEFAULT '待规划',requirement_id INTEGER,tags TEXT NOT NULL DEFAULT '',source_execution_id INTEGER,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_defects_scope_status ON defects(tenant_id,project_id,status,severity);
CREATE TABLE IF NOT EXISTS test_cases(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,code TEXT NOT NULL,category TEXT NOT NULL DEFAULT '未分类',title TEXT NOT NULL,preconditions TEXT NOT NULL DEFAULT '',steps TEXT NOT NULL DEFAULT '',expected TEXT NOT NULL DEFAULT '',priority TEXT NOT NULL DEFAULT 'P2',status TEXT NOT NULL DEFAULT '草稿',owner TEXT NOT NULL DEFAULT '',requirement_id INTEGER,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_test_cases_scope_status ON test_cases(tenant_id,project_id,status,category);
CREATE TABLE IF NOT EXISTS test_plans(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,code TEXT NOT NULL,name TEXT NOT NULL,sprint TEXT NOT NULL DEFAULT '',version TEXT NOT NULL DEFAULT '',scope TEXT NOT NULL DEFAULT '',owner TEXT NOT NULL DEFAULT '',owner_user_id TEXT NOT NULL DEFAULT '',start_date TEXT NOT NULL DEFAULT '',end_date TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT '规划中',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS test_plan_cases(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,plan_id INTEGER NOT NULL,case_id INTEGER NOT NULL,PRIMARY KEY(tenant_id,project_id,plan_id,case_id));
CREATE TABLE IF NOT EXISTS test_executions(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,plan_id INTEGER NOT NULL,case_id INTEGER NOT NULL,status TEXT NOT NULL DEFAULT '未执行',executor TEXT NOT NULL DEFAULT '',executed_at TEXT NOT NULL DEFAULT '',note TEXT NOT NULL DEFAULT '',defect_id INTEGER,UNIQUE(tenant_id,project_id,plan_id,case_id));
CREATE INDEX IF NOT EXISTS idx_test_exec_scope_status ON test_executions(tenant_id,project_id,status);
CREATE TABLE IF NOT EXISTS entity_activities(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,object_type TEXT NOT NULL,object_id INTEGER NOT NULL,actor TEXT NOT NULL,event TEXT NOT NULL,detail TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS entity_comments(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,object_type TEXT NOT NULL,object_id INTEGER NOT NULL,author TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL);`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}
	if err := a.migrateFieldDepartments(); err != nil {
		return err
	}
	if err := a.migrateFieldSoftDelete(); err != nil {
		return err
	}
	for _, column := range []migrationColumn{
		{"users", "department", `ALTER TABLE users ADD COLUMN department TEXT NOT NULL DEFAULT '产品研发中心'`},
		{"users", "employee_no", `ALTER TABLE users ADD COLUMN employee_no TEXT NOT NULL DEFAULT ''`},
		{"users", "last_active", `ALTER TABLE users ADD COLUMN last_active TEXT NOT NULL DEFAULT ''`},
		{"requirements", "discipline", `ALTER TABLE requirements ADD COLUMN discipline TEXT NOT NULL DEFAULT 'product'`},
		{"requirements", "progress", `ALTER TABLE requirements ADD COLUMN progress INTEGER NOT NULL DEFAULT 0`},
		{"requirements", "estimated_hours", `ALTER TABLE requirements ADD COLUMN estimated_hours REAL NOT NULL DEFAULT 0`},
		{"requirements", "actual_hours", `ALTER TABLE requirements ADD COLUMN actual_hours REAL NOT NULL DEFAULT 0`},
		{"defects", "discipline", `ALTER TABLE defects ADD COLUMN discipline TEXT NOT NULL DEFAULT 'backend'`},
		{"defects", "progress", `ALTER TABLE defects ADD COLUMN progress INTEGER NOT NULL DEFAULT 0`},
		{"defects", "estimated_hours", `ALTER TABLE defects ADD COLUMN estimated_hours REAL NOT NULL DEFAULT 0`},
		{"defects", "actual_hours", `ALTER TABLE defects ADD COLUMN actual_hours REAL NOT NULL DEFAULT 0`},
		{"notification_outbox", "dedupe_key", `ALTER TABLE notification_outbox ADD COLUMN dedupe_key TEXT NOT NULL DEFAULT ''`},
		{"notification_outbox", "retry_count", `ALTER TABLE notification_outbox ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0`},
		{"notification_outbox", "last_error", `ALTER TABLE notification_outbox ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`},
	} {
		if _, err := addMigrationColumn(a.db, column); err != nil {
			return err
		}
	}

	// 角色归一化与成员目录回填必须一起提交；否则中断后可能产生已升级角色却没有
	// tenant_memberships/project_members 的半成品权限数据。
	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("启动 V2 数据迁移: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []struct {
		name string
		sql  string
	}{
		{"创建通知去重索引", `CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_dedupe ON notification_outbox(tenant_id,dedupe_key) WHERE dedupe_key!=''`},
		{"创建附件表", `CREATE TABLE IF NOT EXISTS attachments(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,object_type TEXT NOT NULL,object_id INTEGER NOT NULL,file_name TEXT NOT NULL,mime_type TEXT NOT NULL,size_bytes INTEGER NOT NULL DEFAULT 0,storage_key TEXT NOT NULL DEFAULT '',created_by TEXT NOT NULL,created_at TEXT NOT NULL,deleted_at TEXT)`},
		{"创建关联表", `CREATE TABLE IF NOT EXISTS work_item_relations(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,source_type TEXT NOT NULL,source_id INTEGER NOT NULL,target_type TEXT NOT NULL,target_id INTEGER NOT NULL,relation_type TEXT NOT NULL,created_by TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,source_type,source_id,target_type,target_id,relation_type))`},
		{"创建审计表", `CREATE TABLE IF NOT EXISTS audit_logs(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT,actor_id TEXT NOT NULL,object_type TEXT NOT NULL,object_id TEXT NOT NULL,action TEXT NOT NULL,before_json TEXT NOT NULL DEFAULT '{}',after_json TEXT NOT NULL DEFAULT '{}',created_at TEXT NOT NULL)`},
		{"升级租户管理员角色", `UPDATE memberships SET role='tenant_admin' WHERE role='admin'`},
		{"升级默认成员角色", `UPDATE memberships SET role='developer' WHERE role='member' AND user_id='u_member'`},
		{"升级产品角色", `UPDATE memberships SET role='product' WHERE role='member'`},
		{"升级后端角色", `UPDATE memberships SET role='backend' WHERE role='developer'`},
		{"创建企业成员表", `CREATE TABLE IF NOT EXISTS tenant_memberships(tenant_id TEXT NOT NULL,user_id TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'member',status TEXT NOT NULL DEFAULT 'active',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,user_id))`},
		{"创建项目成员表", `CREATE TABLE IF NOT EXISTS project_members(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,user_id TEXT NOT NULL,role TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,user_id))`},
	} {
		if _, err := tx.Exec(statement.sql); err != nil {
			return fmt.Errorf("V2 迁移%s: %w", statement.name, err)
		}
	}
	nowMembership := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at) SELECT tenant_id,user_id,CASE WHEN role='tenant_admin' THEN 'tenant_admin' ELSE 'member' END,'active',?,? FROM memberships`, nowMembership, nowMembership); err != nil {
		return fmt.Errorf("回填企业成员目录: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at) SELECT tenant_id,project_id,user_id,role,?,? FROM memberships`, nowMembership, nowMembership); err != nil {
		return fmt.Errorf("回填项目成员目录: %w", err)
	}
	if a.seedDemo {
		if _, err := tx.Exec(`UPDATE users SET employee_no=CASE id WHEN 'u_admin' THEN 'DF001' WHEN 'u_member' THEN 'DF002' ELSE 'DF003' END,last_active=? WHERE tenant_id=? AND last_active=''`, time.Now().UTC().Format(time.RFC3339), tenantID); err != nil {
			return fmt.Errorf("补充演示成员工号: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 V2 数据迁移: %w", err)
	}
	if err := a.seedV2(); err != nil {
		return err
	}
	if err := a.migrateV3(); err != nil {
		return err
	}
	if _, err := a.db.Exec(`PRAGMA optimize`); err != nil {
		return fmt.Errorf("优化 V2 数据库: %w", err)
	}
	return nil
}

func (a *App) seedV2() error {
	if !a.seedDemo {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("启动 V2 演示初始化: %w", err)
	}
	defer tx.Rollback()
	exec := func(name, statement string, args ...any) error {
		if _, err := tx.Exec(statement, args...); err != nil {
			return fmt.Errorf("V2 演示初始化%s: %w", name, err)
		}
		return nil
	}
	for _, u := range []struct{ id, name, email, no, dept, projectRole string }{
		{"u_qa", "苏禾", "suhe@devflow.local", "DF004", "质量保障部", "qa"},
		{"u_front", "沈星", "shenxing@devflow.local", "DF006", "前端研发组", "frontend"},
		{"u_back", "陆川", "luchuan@devflow.local", "DF007", "后端研发组", "backend"},
		{"u_algo", "唐果", "tangguo@devflow.local", "DF008", "算法平台组", "algorithm"},
		{"u_ui", "许知", "xuzhi@devflow.local", "DF009", "设计体验部", "ui"},
		{"u_front_lead", "夏唯", "xiawei@devflow.local", "DF010", "前端研发组", "frontend_lead"},
		{"u_back_lead", "韩舟", "hanzhou@devflow.local", "DF011", "后端研发组", "backend_lead"},
		{"u_viewer", "顾远", "guyuan@devflow.local", "DF005", "业务运营部", "viewer"},
	} {
		if err := exec("创建成员 "+u.id, `INSERT OR IGNORE INTO users(id,tenant_id,name,email,active,department,employee_no,last_active)VALUES(?,?,?,?,1,?,?,?)`, u.id, tenantID, u.name, u.email, u.dept, u.no, now); err != nil {
			return err
		}
		if err := exec("创建旧成员关系 "+u.id, `INSERT OR IGNORE INTO memberships VALUES(?,?,?,?)`, tenantID, a.pid(), u.id, u.projectRole); err != nil {
			return err
		}
		if err := exec("创建企业成员关系 "+u.id, `INSERT OR IGNORE INTO tenant_memberships VALUES(?,?,?,?,?,?)`, tenantID, u.id, "member", "active", now, now); err != nil {
			return err
		}
		if err := exec("创建项目成员关系 "+u.id, `INSERT OR IGNORE INTO project_members VALUES(?,?,?,?,?,?)`, tenantID, a.pid(), u.id, u.projectRole, now, now); err != nil {
			return err
		}
	}
	defs := []FieldDefinition{{ObjectType: "requirement", Key: "business_value", Name: "业务价值", Type: "single_select", Description: "该需求对业务目标的贡献", Required: true, Searchable: true, Filterable: true, ListVisible: true, Enabled: true, SortOrder: 10, DefaultValue: "中", Options: []string{"高", "中", "低"}}, {ObjectType: "defect", Key: "escape_stage", Name: "逃逸阶段", Type: "single_select", Filterable: true, ListVisible: true, Enabled: true, SortOrder: 10, DefaultValue: "测试", Options: []string{"开发", "测试", "灰度", "生产"}}, {ObjectType: "test_case", Key: "automation", Name: "可自动化", Type: "boolean", Filterable: true, ListVisible: true, Enabled: true, SortOrder: 10, DefaultValue: false}}
	for _, d := range defs {
		defb, err := json.Marshal(d.DefaultValue)
		if err != nil {
			return fmt.Errorf("编码默认字段 %s: %w", d.Key, err)
		}
		opts, err := json.Marshal(d.Options)
		if err != nil {
			return fmt.Errorf("编码字段选项 %s: %w", d.Key, err)
		}
		if err := exec("创建字段 "+d.Key, `INSERT OR IGNORE INTO field_definitions(tenant_id,project_id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), d.ObjectType, d.Key, d.Name, d.Type, d.Description, d.Required, d.Searchable, d.Filterable, d.ListVisible, d.Enabled, d.SortOrder, string(defb), string(opts), now, now); err != nil {
			return err
		}
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sprints WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&n); err != nil {
		return fmt.Errorf("检查演示迭代: %w", err)
	}
	if n == 0 {
		for _, s := range []Sprint{{Name: "V1.0 核心闭环", Goal: "交付需求到测试的最小完整闭环", StartDate: "2026-09-01", EndDate: "2026-09-18", Status: "进行中", Capacity: 80}, {Name: "V1.1 协作增强", Goal: "完善企业微信与跨模块效率", StartDate: "2026-09-21", EndDate: "2026-10-09", Status: "规划中", Capacity: 60}} {
			res, err := tx.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,goal,start_date,end_date,status,capacity,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", s.Name, s.Goal, s.StartDate, s.EndDate, s.Status, s.Capacity, now, now)
			if err != nil {
				return fmt.Errorf("创建演示迭代 %q: %w", s.Name, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("读取演示迭代 ID: %w", err)
			}
			if err := exec("设置演示迭代编号", `UPDATE sprints SET code=? WHERE id=?`, fmt.Sprintf("SPR-%03d", id), id); err != nil {
				return err
			}
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM defects WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&n); err != nil {
		return fmt.Errorf("检查演示缺陷: %w", err)
	}
	if n == 0 {
		for _, d := range []Defect{{Title: "详情抽屉切换标签后滚动位置异常", Description: "切换活动标签后内容区滚动未复位", Steps: "打开任意需求详情；滚动到底部；切换活动标签", Actual: "仍停留在底部", Expected: "切换后从顶部显示", Environment: "Chrome 128 / macOS", Severity: "一般", Priority: "P1", Status: "修复中", Assignee: "周屿", Verifier: "陈澄", Sprint: "V1.0", Tags: "交互,回归"}, {Title: "筛选条件清空后列表未及时刷新", Severity: "严重", Priority: "P0", Status: "待验证", Assignee: "周屿", Verifier: "林夏", Sprint: "V1.0", Tags: "列表"}} {
			// V2 仍未添加稳定人员 ID 列，先只写该版本已有的显示字段；V3 的
			// 严格回填会在同租户同项目目录中解析稳定 ID，不能提前调用新列写入。
			res, err := tx.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,description,steps,actual,expected,environment,found_version,fix_version,severity,priority,status,assignee,verifier,sprint,discipline,progress,estimated_hours,actual_hours,requirement_id,tags,source_execution_id,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", d.Title, d.Description, d.Steps, d.Actual, d.Expected, d.Environment, d.FoundVersion, d.FixVersion, d.Severity, d.Priority, d.Status, d.Assignee, d.Verifier, d.Sprint, d.Discipline, d.Progress, d.EstimatedHours, d.ActualHours, d.RequirementID, d.Tags, d.SourceExecutionID, now, now)
			if err != nil {
				return fmt.Errorf("创建演示缺陷 %q: %w", d.Title, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("读取演示缺陷 ID: %w", err)
			}
			if err := exec("设置演示缺陷编号", `UPDATE defects SET code=? WHERE id=?`, fmt.Sprintf("BUG-%04d", id), id); err != nil {
				return err
			}
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM test_cases WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&n); err != nil {
		return fmt.Errorf("检查演示用例: %w", err)
	}
	if n == 0 {
		for _, c := range []TestCase{{Category: "需求/创建", Title: "创建完整字段需求", Preconditions: "已登录且拥有产品角色", Steps: "进入新建需求；填写必填和自定义字段；提交", Expected: "需求创建成功并在列表显示自定义字段", Priority: "P0", Status: "已通过", Owner: "陈澄"}, {Category: "缺陷/流转", Title: "缺陷从新建流转到关闭", Preconditions: "存在一条新建缺陷", Steps: "依次确认、修复、解决、验证并关闭", Expected: "每次流转写入活动和通知 outbox", Priority: "P1", Status: "待评审", Owner: "周屿"}} {
			res, err := tx.Exec(`INSERT INTO test_cases(tenant_id,project_id,code,category,title,preconditions,steps,expected,priority,status,owner,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", c.Category, c.Title, c.Preconditions, c.Steps, c.Expected, c.Priority, c.Status, c.Owner, now, now)
			if err != nil {
				return fmt.Errorf("创建演示用例 %q: %w", c.Title, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("读取演示用例 ID: %w", err)
			}
			if err := exec("设置演示用例编号", `UPDATE test_cases SET code=? WHERE id=?`, fmt.Sprintf("TC-%04d", id), id); err != nil {
				return err
			}
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM test_plans WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&n); err != nil {
		return fmt.Errorf("检查演示计划: %w", err)
	}
	if n == 0 {
		res, err := tx.Exec(`INSERT INTO test_plans(tenant_id,project_id,code,name,sprint,version,scope,owner,start_date,end_date,status,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", "V1.0 发布回归", "V1.0", "1.0.0", "需求与缺陷核心闭环", "陈澄", "2026-09-14", "2026-09-18", "执行中", now, now)
		if err != nil {
			return fmt.Errorf("创建演示计划: %w", err)
		}
		pid, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("读取演示计划 ID: %w", err)
		}
		if err := exec("设置演示计划编号", `UPDATE test_plans SET code=? WHERE id=?`, fmt.Sprintf("TP-%03d", pid), pid); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT id FROM test_cases WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
		if err != nil {
			return fmt.Errorf("读取演示用例关联: %w", err)
		}
		caseIDs := []int64{}
		for rows.Next() {
			var cid int64
			if err := rows.Scan(&cid); err != nil {
				rows.Close()
				return fmt.Errorf("读取演示用例关联: %w", err)
			}
			caseIDs = append(caseIDs, cid)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("读取演示用例关联: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("关闭演示用例关联读取: %w", err)
		}
		for _, cid := range caseIDs {
			if err := exec("关联演示计划用例", `INSERT OR IGNORE INTO test_plan_cases VALUES(?,?,?,?)`, tenantID, a.pid(), pid, cid); err != nil {
				return err
			}
			if err := exec("创建演示执行记录", `INSERT OR IGNORE INTO test_executions(tenant_id,project_id,plan_id,case_id)VALUES(?,?,?,?)`, tenantID, a.pid(), pid, cid); err != nil {
				return err
			}
		}
	}
	if err := exec("补齐演示计划用例", `INSERT OR IGNORE INTO test_plan_cases(tenant_id,project_id,plan_id,case_id) SELECT ?,?,p.id,c.id FROM test_plans p CROSS JOIN test_cases c WHERE p.tenant_id=? AND p.project_id=? AND c.tenant_id=? AND c.project_id=?`, tenantID, a.pid(), tenantID, a.pid(), tenantID, a.pid()); err != nil {
		return err
	}
	if err := exec("补齐演示执行记录", `INSERT OR IGNORE INTO test_executions(tenant_id,project_id,plan_id,case_id) SELECT tenant_id,project_id,plan_id,case_id FROM test_plan_cases WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id FROM requirements WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
	if err != nil {
		return fmt.Errorf("读取演示需求字段: %w", err)
	}
	reqIDs := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("读取演示需求字段: %w", err)
		}
		reqIDs = append(reqIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("读取演示需求字段: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭演示需求字段读取: %w", err)
	}
	for i, id := range reqIDs {
		for key, value := range map[string]any{
			"business_value": []string{"高", "中", "低"}[i%3],
		} {
			var definitionID int64
			if err := tx.QueryRow(`SELECT id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key=? AND deleted_at=''`, tenantID, a.pid(), key).Scan(&definitionID); err != nil {
				return fmt.Errorf("读取演示字段定义 %s: %w", key, err)
			}
			if err := exec("写入演示字段 "+key, `INSERT OR IGNORE INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), "requirement", id, definitionID, jsonText(value), now); err != nil {
				return err
			}
		}
	}
	if err := exec("补齐演示需求工作量", `UPDATE requirements SET discipline=CASE id%6 WHEN 0 THEN 'qa' WHEN 1 THEN 'product' WHEN 2 THEN 'frontend' WHEN 3 THEN 'backend' WHEN 4 THEN 'algorithm' ELSE 'ui' END,assignee=CASE id%6 WHEN 0 THEN '苏禾' WHEN 1 THEN '陈澄' WHEN 2 THEN '沈星' WHEN 3 THEN '陆川' WHEN 4 THEN '唐果' ELSE '许知' END,progress=CASE status WHEN '已完成' THEN 100 WHEN '测试中' THEN 80 WHEN '开发中' THEN 55 WHEN '待开发' THEN 20 ELSE 10 END,estimated_hours=8+(id%4)*4,actual_hours=(id%5)*2 WHERE tenant_id=? AND project_id=? AND progress=0 AND estimated_hours=0 AND actual_hours=0`, tenantID, a.pid()); err != nil {
		return err
	}
	if err := exec("补齐演示缺陷工作量", `UPDATE defects SET discipline='backend',assignee=CASE id%2 WHEN 0 THEN '韩舟' ELSE '陆川' END,progress=CASE status WHEN '已关闭' THEN 100 WHEN '待验证' THEN 85 WHEN '已解决' THEN 70 WHEN '修复中' THEN 45 ELSE 10 END,estimated_hours=4+(id%3)*2,actual_hours=(id%4)*2 WHERE tenant_id=? AND project_id=? AND progress=0 AND estimated_hours=0 AND actual_hours=0`, tenantID, a.pid()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 V2 演示初始化: %w", err)
	}
	return nil
}

func (a *App) currentUser(r *http.Request) (id, name, role string, active bool) {
	id, name, role, active, _ = a.currentUserState(r)
	return
}

func (a *App) currentUserState(r *http.Request) (id, name, role string, active bool, err error) {
	id = a.uid()
	err = a.db.QueryRowContext(r.Context(), `SELECT u.name,COALESCE(m.role,tm.role),u.active FROM users u JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id LEFT JOIN memberships m ON m.user_id=u.id AND m.tenant_id=u.tenant_id AND m.project_id=? WHERE u.id=? AND u.tenant_id=? AND (m.user_id IS NOT NULL OR tm.role='tenant_admin')`, a.pid(), id, tenantID).Scan(&name, &role, &active)
	return
}
func (a *App) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.Method == "GET" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		if err := a.requireOperationAccess(r.Context(), a.db); err != nil {
			failOrganization(w, err)
			return
		}
		_, _, role, active, userErr := a.currentUserState(r)
		if userErr != nil && !errors.Is(userErr, sql.ErrNoRows) {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "账号权限服务暂时繁忙，请稍后重试")
			return
		}
		if !active {
			fail(w, 403, "account_disabled", "账号已停用")
			return
		}
		personalMutation := requirementFavoriteMutation(r) || strings.HasPrefix(r.URL.Path, "/api/profile") || (r.URL.Path == "/api/preferences/requirement-list" || r.URL.Path == "/api/preferences/requirement-detail" || r.URL.Path == "/api/preferences/locale") || (strings.HasPrefix(r.URL.Path, "/api/notifications") && !strings.HasPrefix(r.URL.Path, "/api/notifications/outbox")) || (strings.HasPrefix(r.URL.Path, "/api/projects/") && strings.HasSuffix(r.URL.Path, "/visit"))
		fieldConfiguration := strings.HasPrefix(r.URL.Path, "/api/field-definitions") || strings.HasPrefix(r.URL.Path, "/api/field-presets")
		fieldManager := fieldConfiguration && a.canManageProject(a.uid(), a.pid())
		stateConfiguration := r.URL.Path == "/api/requirement-statuses" || strings.HasPrefix(r.URL.Path, "/api/requirement-statuses/") || r.URL.Path == "/api/requirement-workflow" || r.URL.Path == "/api/automation-rules" || strings.HasPrefix(r.URL.Path, "/api/automation-rules/")
		// These exact configuration handlers recheck the active tenant/project
		// manager inside their transaction, including tenant admins with a viewer
		// project membership. No business-write permission is widened here.
		if (role == "viewer" || role == "") && !personalMutation && !fieldManager && !stateConfiguration {
			fail(w, 403, "forbidden", "当前角色仅可查看")
			return
		}
		if (fieldConfiguration && !fieldManager) || (strings.HasPrefix(r.URL.Path, "/api/members") && role != "tenant_admin" && role != "project_admin") {
			fail(w, 403, "admin_required", "仅管理员可执行此操作")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/sprints") && !validChoice(role, []string{"tenant_admin", "project_admin", "product", "frontend_lead", "backend_lead"}) {
			fail(w, 403, "sprint_manager_required", "当前角色不可管理迭代")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }
func jsonText(v any) string                   { b, _ := json.Marshal(v); return string(b) }
func parseJSON(s string, v any)               { _ = json.Unmarshal([]byte(s), v) }
func validChoice(v string, allowed []string) bool {
	for _, x := range allowed {
		if v == x {
			return true
		}
	}
	return false
}
func validProjectRole(v string) bool {
	return validChoice(v, []string{"project_admin", "product", "frontend", "backend", "algorithm", "ui", "frontend_lead", "backend_lead", "qa", "viewer"})
}
func number(v any) float64 {
	n, _ := strconv.ParseFloat(fmt.Sprint(v), 64)
	return n
}
func canTransition(current, next string, graph map[string][]string) bool {
	if current == next {
		return true
	}
	return validChoice(next, graph[current])
}

func defectTransitions(status string) []string {
	graph := map[string][]string{
		"新建":   {"已确认", "已拒绝"},
		"已确认":  {"修复中", "已拒绝"},
		"修复中":  {"已解决", "已拒绝"},
		"已解决":  {"待验证", "重新打开"},
		"待验证":  {"已关闭", "重新打开"},
		"已关闭":  {"重新打开"},
		"重新打开": {"已确认", "修复中"},
		"已拒绝":  {"重新打开"},
	}
	return append([]string{}, graph[status]...)
}
func pathID(path, prefix string) (int64, []string, error) {
	p := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	parts := strings.Split(p, "/")
	id, e := strconv.ParseInt(parts[0], 10, 64)
	return id, parts, e
}

func (a *App) fieldDefinitions(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var d FieldDefinition
		if decodeJSON(r, &d) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		if err := validateDefinition(&d); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		d.MemberRoles = fieldMemberRoles(d)
		tx, err := a.beginFieldConfigurationWrite(r)
		if err != nil {
			failFieldMutation(w, err)
			return
		}
		defer tx.Rollback()
		// 历史排序保留用于展示，新字段由服务端接到末尾；不再接受表单排序参数。
		if err := tx.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(sort_order),0)+10 FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type=? AND deleted_at=''`, tenantID, a.pid(), d.ObjectType).Scan(&d.SortOrder); err != nil {
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		if err := a.validateFieldDepartment(tx, d, nil); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		if err := a.validateFieldPeople(tx, d, d.DefaultValue, nil); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		res, e := tx.Exec(`INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,created_at,updated_at,department_id)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), d.ObjectType, d.Key, d.Name, d.Type, d.Description, d.Required, d.Searchable, d.Filterable, d.ListVisible, true, d.SortOrder, jsonText(d.DefaultValue), jsonText(d.Options), now, now, d.DepartmentID)
		if e != nil {
			fail(w, 409, "field_exists", "字段 key 已存在")
			return
		}
		d.ID, _ = res.LastInsertId()
		if err := tx.Commit(); err != nil {
			fail(w, 503, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		d.Enabled = true
		write(w, 201, d)
		return
	}
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	object := r.URL.Query().Get("objectType")
	q := `SELECT id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id FROM field_definitions WHERE tenant_id=? AND project_id=? AND deleted_at=''`
	args := []any{tenantID, a.pid()}
	if object != "" {
		q += " AND object_type=?"
		args = append(args, object)
	}
	q += " ORDER BY object_type,sort_order,id"
	rows, e := a.db.QueryContext(r.Context(), q, args...)
	if e != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []FieldDefinition{}
	for rows.Next() {
		var d FieldDefinition
		var def, opts string
		if err := rows.Scan(&d.ID, &d.ObjectType, &d.Key, &d.Name, &d.Type, &d.Description, &d.Required, &d.Searchable, &d.Filterable, &d.ListVisible, &d.Enabled, &d.SortOrder, &def, &opts, &d.DepartmentID); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
			return
		}
		parseJSON(def, &d.DefaultValue)
		parseJSON(opts, &d.Options)
		d.MemberRoles = fieldMemberRoles(d)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "字段配置暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) fieldDefinition(w http.ResponseWriter, r *http.Request) {
	id, parts, e := pathID(r.URL.Path, "/api/field-definitions/")
	if e != nil || id <= 0 || len(parts) != 1 {
		fail(w, 400, "invalid_id", "字段编号不正确")
		return
	}
	if r.Method == http.MethodDelete {
		a.deleteFieldDefinition(w, r, id)
		return
	}
	if r.Method != "PATCH" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var patch map[string]json.RawMessage
	if decodeJSON(r, &patch) != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	var current FieldDefinition
	var def, opts string
	tx, e := a.beginFieldConfigurationWrite(r)
	if e != nil {
		failFieldMutation(w, e)
		return
	}
	defer tx.Rollback()
	e = tx.QueryRow(`SELECT object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id FROM field_definitions WHERE id=? AND tenant_id=? AND project_id=? AND deleted_at=''`, id, tenantID, a.pid()).Scan(&current.ObjectType, &current.Key, &current.Name, &current.Type, &current.Description, &current.Required, &current.Searchable, &current.Filterable, &current.ListVisible, &current.Enabled, &current.SortOrder, &def, &opts, &current.DepartmentID)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			fail(w, 404, "not_found", "字段不存在")
		} else {
			failFieldMutation(w, e)
		}
		return
	}
	parseJSON(def, &current.DefaultValue)
	parseJSON(opts, &current.Options)
	previous := current
	for key, target := range map[string]any{"name": &current.Name, "description": &current.Description, "options": &current.Options, "defaultValue": &current.DefaultValue, "required": &current.Required, "searchable": &current.Searchable, "filterable": &current.Filterable, "listVisible": &current.ListVisible, "enabled": &current.Enabled, "departmentId": &current.DepartmentID} {
		if raw, ok := patch[key]; ok {
			if string(raw) == "null" && key != "defaultValue" {
				fail(w, 422, "validation_error", "字段值类型不正确")
				return
			}
			if json.Unmarshal(raw, target) != nil {
				fail(w, 422, "validation_error", "字段值类型不正确")
				return
			}
		}
	}
	// Keys and types are immutable: existing values must never be reinterpreted.
	for key, expected := range map[string]string{"type": current.Type, "key": current.Key, "objectType": current.ObjectType} {
		if raw, ok := patch[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) != nil || value != expected {
				fail(w, 422, "validation_error", "已有字段的 key、对象和类型不可修改")
				return
			}
		}
	}
	if err := validateDefinition(&current); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	if err := a.validateFieldDepartment(tx, current, &previous); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	var savedDefault any
	if current.DepartmentID == previous.DepartmentID {
		savedDefault = previous.DefaultValue
	}
	if err := a.validateFieldPeople(tx, current, current.DefaultValue, savedDefault); err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	_, e = tx.Exec(`UPDATE field_definitions SET name=?,description=?,required=?,searchable=?,filterable=?,list_visible=?,enabled=?,sort_order=?,default_value=?,options=?,department_id=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, current.Name, current.Description, current.Required, current.Searchable, current.Filterable, current.ListVisible, current.Enabled, current.SortOrder, jsonText(current.DefaultValue), jsonText(current.Options), current.DepartmentID, time.Now().UTC().Format(time.RFC3339), id, tenantID, a.pid())
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		fail(w, 500, "db_error", e.Error())
		return
	}
	current.ID = id
	current.MemberRoles = fieldMemberRoles(current)
	write(w, 200, current)
}
func validateDefinition(d *FieldDefinition) error {
	objects := []string{"requirement", "defect", "test_case", "sprint"}
	types := []string{"text", "textarea", "number", "single_select", "multi_select", "boolean", "date", "user", "users"}
	if !validChoice(d.ObjectType, objects) {
		return fmt.Errorf("不支持的对象类型")
	}
	if strings.TrimSpace(d.Key) == "" || strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("key 和名称不能为空")
	}
	if !validChoice(d.Type, types) {
		return fmt.Errorf("不支持的字段类型")
	}
	if d.DepartmentID != "" && d.Type != "user" && d.Type != "users" {
		return fmt.Errorf("仅人员字段可以限定部门")
	}
	if (d.Type == "single_select" || d.Type == "multi_select") && len(d.Options) == 0 {
		return fmt.Errorf("选择字段必须配置选项")
	}
	if d.DefaultValue != nil {
		return validateFieldValue(*d, d.DefaultValue)
	}
	return nil
}
func validateFieldValue(d FieldDefinition, v any) error {
	if v == nil || v == "" {
		if d.Required {
			return fmt.Errorf("%s 为必填字段", d.Name)
		}
		return nil
	}
	switch d.Type {
	case "text", "textarea", "date", "user":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s 类型不正确", d.Name)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s 必须是数字", d.Name)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s 必须是布尔值", d.Name)
		}
	case "single_select":
		s, ok := v.(string)
		if !ok || !validChoice(s, d.Options) {
			return fmt.Errorf("%s 选项无效", d.Name)
		}
	case "multi_select":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是多选数组", d.Name)
		}
		for _, x := range arr {
			s, ok := x.(string)
			if !ok || !validChoice(s, d.Options) {
				return fmt.Errorf("%s 选项无效", d.Name)
			}
		}
	case "users":
		ids, ok := fieldPersonIDs(v, true)
		if !ok || len(ids) > 100 {
			return fmt.Errorf("%s 必须是最多 100 人的成员 ID 数组", d.Name)
		}
		if d.Required && len(ids) == 0 {
			return fmt.Errorf("%s 为必填字段", d.Name)
		}
	}
	return nil
}
func (a *App) definitions(object string, enabledOnly bool) ([]FieldDefinition, error) {
	q := `SELECT id,object_type,key,name,type,description,required,searchable,filterable,list_visible,enabled,sort_order,default_value,options,department_id FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type=? AND deleted_at=''`
	if enabledOnly {
		q += " AND enabled=1"
	}
	q += " ORDER BY sort_order,id"
	rows, e := a.db.Query(q, tenantID, a.pid(), object)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FieldDefinition{}
	for rows.Next() {
		var d FieldDefinition
		var def, opts string
		if err := rows.Scan(&d.ID, &d.ObjectType, &d.Key, &d.Name, &d.Type, &d.Description, &d.Required, &d.Searchable, &d.Filterable, &d.ListVisible, &d.Enabled, &d.SortOrder, &def, &opts, &d.DepartmentID); err != nil {
			return nil, err
		}
		parseJSON(def, &d.DefaultValue)
		parseJSON(opts, &d.Options)
		d.MemberRoles = fieldMemberRoles(d)
		out = append(out, d)
	}
	return out, rows.Err()
}
func (a *App) saveCustomFields(object string, id int64, values map[string]any, creating bool) error {
	writes, err := a.prepareObjectFields(object, values, creating)
	if err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = a.writeObjectFields(tx, object, id, writes, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) customFields(object string, id int64) map[string]any {
	rows, e := a.db.Query(`SELECT d.key,v.value_json FROM field_values v JOIN field_definitions d ON d.id=v.field_definition_id AND d.deleted_at='' WHERE v.tenant_id=? AND v.project_id=? AND v.object_type=? AND v.object_id=?`, tenantID, a.pid(), object, id)
	if e != nil {
		return map[string]any{}
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k, s string
		rows.Scan(&k, &s)
		var v any
		parseJSON(s, &v)
		out[k] = v
	}
	return out
}

func (a *App) entityActivity(object string, id int64, event, detail string) {
	_, name, _, _ := a.currentUser(&http.Request{Header: http.Header{}})
	if name == "" {
		name = "林夏"
	}
	a.db.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), object, id, name, event, detail, time.Now().UTC().Format(time.RFC3339))
}
func (a *App) entityActivities(w http.ResponseWriter, object string, id int64) {
	if !a.requireEntity(w, object, id) {
		return
	}
	rows, queryErr := a.db.Query(`SELECT id,actor,event,detail,created_at FROM entity_activities WHERE tenant_id=? AND project_id=? AND object_type=? AND object_id=? ORDER BY id DESC`, tenantID, a.pid(), object, id)
	if queryErr != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var aid int64
		var actor, event, detail, at string
		if scanErr := rows.Scan(&aid, &actor, &event, &detail, &at); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		out = append(out, map[string]any{"id": aid, "actor": actor, "event": event, "detail": detail, "createdAt": at})
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) entityComments(w http.ResponseWriter, r *http.Request, object string, id int64) {
	a.qualityComments(w, r, object, id)
}

func (a *App) sprints(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var s Sprint
		if decodeJSON(r, &s) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		s.Name = strings.TrimSpace(s.Name)
		if strings.TrimSpace(s.Name) == "" || s.StartDate == "" || s.EndDate == "" {
			fail(w, 422, "validation_error", "名称和日期不能为空")
			return
		}
		if !validDateRange(s.StartDate, s.EndDate) || s.Capacity < 0 {
			fail(w, 422, "validation_error", "日期范围或容量无效")
			return
		}
		if s.Status == "" {
			s.Status = "规划中"
		}
		// 已完成必须经过 /complete 的原子迁移、通知和升级日志任务；
		// 已取消同样不能伪造成一个从未存在过的历史迭代。
		if !validChoice(s.Status, []string{"规划中", "进行中"}) {
			fail(w, 422, "validation_error", "新建迭代只能选择规划中或进行中状态")
			return
		}
		if err := a.validateSprintName(s.Name, 0); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		tx, e := a.db.BeginTx(r.Context(), nil)
		if e != nil {
			fail(w, 503, "database_unavailable", "迭代暂时无法创建，请稍后重试")
			return
		}
		defer tx.Rollback()
		res, e := tx.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,goal,start_date,end_date,status,capacity,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", s.Name, s.Goal, s.StartDate, s.EndDate, s.Status, s.Capacity, now, now)
		if e == nil {
			s.ID, e = res.LastInsertId()
		}
		if e == nil {
			s.Code = fmt.Sprintf("SPR-%03d", s.ID)
			_, e = tx.Exec(`UPDATE sprints SET code=? WHERE id=? AND tenant_id=? AND project_id=?`, s.Code, s.ID, tenantID, a.pid())
		}
		if e == nil {
			_, e = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "sprint", s.ID, a.actorName(), "created", "创建了迭代", now)
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		write(w, 201, s)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,code,name,goal,start_date,end_date,status,capacity,updated_at FROM sprints WHERE tenant_id=? AND project_id=? ORDER BY start_date DESC`, tenantID, a.pid())
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代列表暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	sprints := []Sprint{}
	for rows.Next() {
		var s Sprint
		if err := rows.Scan(&s.ID, &s.Code, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status, &s.Capacity, &s.UpdatedAt); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代列表暂时无法读取，请稍后重试")
			return
		}
		sprints = append(sprints, s)
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代列表暂时无法读取，请稍后重试")
		return
	}
	rows.Close()
	out := []map[string]any{}
	for _, s := range sprints {
		a1, a2 := a.scopedSprintAliases(s.Name)
		var reqTotal, reqDone, reqCancelled, bugTotal, bugDone int
		var estimated, actual float64
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*),COALESCE(SUM(CASE WHEN rs.category='done' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN rs.category='cancelled' THEN 1 ELSE 0 END),0),COALESCE(SUM(r.estimated_hours),0),COALESCE(SUM(r.actual_hours),0) FROM requirements r LEFT JOIN requirement_statuses rs ON rs.tenant_id=r.tenant_id AND rs.project_id=r.project_id AND rs.key=r.status WHERE r.tenant_id=? AND r.project_id=? AND r.sprint IN (?,?)`, tenantID, a.pid(), a1, a2).Scan(&reqTotal, &reqDone, &reqCancelled, &estimated, &actual); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
			return
		}
		var bugEstimated, bugActual float64
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status IN ('已关闭','已拒绝') THEN 1 ELSE 0 END),0),COALESCE(SUM(estimated_hours),0),COALESCE(SUM(actual_hours),0) FROM defects WHERE tenant_id=? AND project_id=? AND sprint IN (?,?)`, tenantID, a.pid(), a1, a2).Scan(&bugTotal, &bugDone, &bugEstimated, &bugActual); err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
			return
		}
		total, done := reqTotal+bugTotal, reqDone+bugDone
		out = append(out, map[string]any{"id": s.ID, "code": s.Code, "name": s.Name, "goal": s.Goal, "startDate": s.StartDate, "endDate": s.EndDate, "status": s.Status, "capacity": s.Capacity, "total": total, "done": done, "cancelled": reqCancelled, "unfinished": total - done - reqCancelled, "defects": bugTotal, "estimatedHours": estimated + bugEstimated, "actualHours": actual + bugActual})
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) sprint(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/sprints/backlog/items" {
		a.backlogWorkItems(w, r)
		return
	}
	if r.URL.Path == "/api/sprints/backlog/weights" {
		a.backlogWeights(w, r)
		return
	}
	id, parts, e := pathID(r.URL.Path, "/api/sprints/")
	if e != nil {
		fail(w, 400, "invalid_id", "迭代编号不正确")
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "activities":
			a.entityActivities(w, "sprint", id)
			return
		case "complete":
			a.completeSprint(w, r, id)
			return
		case "release-notes":
			if len(parts) > 3 || len(parts) == 3 && parts[2] != "bundle" {
				fail(w, 404, "not_found", "资源不存在")
				return
			}
			a.sprintReleaseNotes(w, r, id, parts[2:])
			return
		}
	}
	if r.Method == "GET" {
		var s Sprint
		e = a.db.QueryRow(`SELECT id,code,name,goal,start_date,end_date,status,capacity,updated_at FROM sprints WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&s.ID, &s.Code, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status, &s.Capacity, &s.UpdatedAt)
		if e != nil {
			fail(w, 404, "not_found", "迭代不存在")
			return
		}
		weights, err := a.sprintWeightSummary(r.Context(), s.Name)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
			return
		}
		all, err := a.sprintWorkItems(r.Context(), s.Name)
		if err != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
			return
		}
		var estimated, actual float64
		var done, cancelled int
		for _, x := range all {
			estimated += number(x["estimatedHours"])
			actual += number(x["actualHours"])
			if (x["objectType"] == "requirement" && x["statusCategory"] == "done") || (x["objectType"] == "defect" && isWorkItemDone("defect", fmt.Sprint(x["status"]))) {
				done++
			}
			if x["objectType"] == "requirement" && x["statusCategory"] == "cancelled" {
				cancelled++
			}
		}
		write(w, 200, map[string]any{"sprint": s, "items": all, "weightSummary": weights, "summary": map[string]any{"total": len(all), "done": done, "cancelled": cancelled, "unfinished": len(all) - done - cancelled, "estimatedHours": estimated, "actualHours": actual}})
		return
	}
	if r.Method == "PATCH" {
		a.patchSprint(w, r, id)
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
func httptestGet(path string) *http.Request { r, _ := http.NewRequest("GET", path, nil); return r }
func (a *App) rowsAsMaps(q string, args ...any) ([]map[string]any, error) {
	rows, e := a.db.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	people := map[string]bool{}
	for rows.Next() {
		var id int64
		var code, title, status, priority, assignee, discipline, typ, primary, rawIDs string
		var progress int
		var estimated, actual float64
		if err := rows.Scan(&id, &code, &title, &status, &priority, &assignee, &discipline, &progress, &estimated, &actual, &typ, &primary, &rawIDs); err != nil {
			return nil, err
		}
		ids := []string{}
		if err := json.Unmarshal([]byte(rawIDs), &ids); err != nil {
			return nil, err
		}
		if len(ids) == 0 && primary != "" {
			ids = []string{primary}
		}
		// Defensive deduplication preserves the primary/participant order and
		// prevents an old duplicate identity from generating duplicate cards.
		unique, seen := []string{}, map[string]bool{}
		for _, userID := range ids {
			if userID != "" && !seen[userID] {
				unique = append(unique, userID)
				seen[userID] = true
				people[userID] = true
			}
		}
		item := map[string]any{"id": id, "code": code, "title": title, "status": status, "priority": priority, "assignee": assignee, "assigneeUserId": primary, "discipline": discipline, "progress": progress, "estimatedHours": estimated, "actualHours": actual, "objectType": typ}
		if typ == "requirement" {
			item["assigneeUserIds"] = unique
			item["assignees"] = []RequirementAssignee{}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ids := make([]string, 0, len(people))
	for id := range people {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	names := map[string]string{}
	// Batch name hydration rather than issuing a query per requirement. Keep
	// historical inactive users, but never resolve a name across tenant scope.
	for start := 0; start < len(ids); start += 400 {
		end := min(start+400, len(ids))
		marks, parameters := []string{}, []any{tenantID}
		for _, id := range ids[start:end] {
			marks = append(marks, "?")
			parameters = append(parameters, id)
		}
		peopleRows, err := a.db.Query(`SELECT id,name FROM users WHERE tenant_id=? AND id IN (`+strings.Join(marks, ",")+`)`, parameters...)
		if err != nil {
			return nil, err
		}
		for peopleRows.Next() {
			var id, name string
			if err := peopleRows.Scan(&id, &name); err != nil {
				peopleRows.Close()
				return nil, err
			}
			names[id] = name
		}
		err = peopleRows.Err()
		peopleRows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, item := range out {
		if item["objectType"] != "requirement" {
			continue
		}
		assignees := []RequirementAssignee{}
		for _, id := range item["assigneeUserIds"].([]string) {
			name := names[id]
			if name == "" {
				name = id
			}
			assignees = append(assignees, RequirementAssignee{ID: id, Name: name})
		}
		item["assignees"] = assignees
		if len(assignees) > 0 {
			item["assigneeUserId"] = assignees[0].ID
			item["assignee"] = assignees[0].Name
		}
	}
	return out, nil
}

func sprintAliases(name string) (string, string) {
	short := name
	if f := strings.Fields(name); len(f) > 0 {
		short = f[0]
	}
	return name, short
}
func validDateRange(start, end string) bool {
	s, e1 := time.Parse("2006-01-02", start)
	e, e2 := time.Parse("2006-01-02", end)
	return e1 == nil && e2 == nil && !s.After(e)
}
func isWorkItemDone(typ, status string) bool {
	if typ == "defect" {
		return status == "已关闭" || status == "已拒绝"
	}
	return false // Requirement completion is project-configured, never name-based.
}
func (a *App) completeSprint(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != "POST" {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var b struct {
		TargetSprint string `json:"targetSprint"`
	}
	if decodeJSON(r, &b) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if b.TargetSprint == "" {
		b.TargetSprint = "待规划"
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	// 完成、迁移与通知共享一个事务内快照。生产 SQLite 以 IMMEDIATE 开始写事务；
	// 条件更新仍保留在 SQL 中，作为其它驱动和重试场景下的最终并发闸门。
	snapshot, err := a.readSprintMutationSnapshot(r.Context(), tx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "迭代不存在")
		} else {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代暂时无法读取，请稍后重试")
		}
		return
	}
	sprint := snapshot.sprint
	if sprint.Status != "进行中" {
		fail(w, 409, "sprint_not_active", "仅进行中的迭代可以完成")
		return
	}
	target, resolveErr := a.resolveRequirementSprintFrom(r.Context(), tx, b.TargetSprint, true)
	if resolveErr != nil || target == sprint.Name {
		fail(w, 422, "invalid_target", "迁移目标迭代不存在、不可用或与当前迭代相同")
		return
	}
	b.TargetSprint = target
	a1, a2, err := a.scopedSprintAliasesFrom(r.Context(), tx, sprint.Name)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代关联关系暂时无法读取，请稍后重试")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	actor := a.uid()
	_ = tx.QueryRowContext(r.Context(), `SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, a.uid()).Scan(&actor)
	if err = a.recordSprintRequirementHistory(r.Context(), tx, a1, a2, b.TargetSprint, actor, now, true); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	reqRes, err := tx.ExecContext(r.Context(), `UPDATE requirements SET sprint=?,updated_at=? WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) AND NOT EXISTS(SELECT 1 FROM requirement_statuses rs WHERE rs.tenant_id=requirements.tenant_id AND rs.project_id=requirements.project_id AND rs.key=requirements.status AND rs.category IN ('done','cancelled'))`, b.TargetSprint, now, tenantID, a.pid(), a1, a2)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	bugRes, err := tx.ExecContext(r.Context(), `UPDATE defects SET sprint=?,updated_at=? WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) AND status NOT IN ('已关闭','已拒绝')`, b.TargetSprint, now, tenantID, a.pid(), a1, a2)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	result, err := tx.ExecContext(r.Context(), `UPDATE sprints SET status='已完成',updated_at=?
		WHERE id=? AND tenant_id=? AND project_id=?
			AND status='进行中' AND name=? AND goal=? AND start_date=? AND end_date=? AND capacity=?`, now, id, tenantID, a.pid(), sprint.Name, sprint.Goal, sprint.StartDate, sprint.EndDate, sprint.Capacity)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if changed != 1 {
		failSprintConflict(w)
		return
	}
	reqN, _ := reqRes.RowsAffected()
	bugN, _ := bugRes.RowsAffected()
	detail := fmt.Sprintf("完成迭代，将 %d 个需求、%d 个缺陷迁移到 %s", reqN, bugN, b.TargetSprint)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "sprint", id, actor, "completed", detail, now); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "sprint", fmt.Sprint(id), "complete", jsonText(map[string]any{"targetSprint": b.TargetSprint, "requirements": reqN, "defects": bugN}), now); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if notices, noticeErr := a.sprintLifecycleNotices(r.Context(), tx, id, "sprint.completed", "迭代已完成", detail); noticeErr != nil {
		fail(w, 500, "db_error", noticeErr.Error())
		return
	} else if err = a.writeAssignmentNotices(r.Context(), tx, notices, now); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if err = a.enqueueAutomaticReleaseNotes(r.Context(), tx, id); err != nil {
		fail(w, 503, "release_notes_unavailable", "升级日志任务暂时无法保存，请稍后重试")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 200, map[string]any{"status": "已完成", "targetSprint": b.TargetSprint, "migratedRequirements": reqN, "migratedDefects": bugN})
}

type statementExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func (a *App) insertDefectWith(exec statementExecutor, d Defect) (int64, error) {
	if d.Severity == "" {
		d.Severity = "一般"
	}
	if d.Priority == "" {
		d.Priority = "P2"
	}
	if d.Status == "" {
		d.Status = "新建"
	}
	if d.Sprint == "" {
		d.Sprint = "待规划"
	}
	if d.Discipline == "" {
		d.Discipline = "backend"
	}
	if d.Progress < 0 || d.Progress > 100 || d.EstimatedHours < 0 || d.ActualHours < 0 {
		return 0, fmt.Errorf("进度须为 0–100，工时不能为负数")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, e := exec.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,description,steps,actual,expected,environment,found_version,fix_version,severity,priority,status,assignee,verifier,sprint,discipline,progress,estimated_hours,actual_hours,requirement_id,tags,source_execution_id,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "", d.Title, d.Description, d.Steps, d.Actual, d.Expected, d.Environment, d.FoundVersion, d.FixVersion, d.Severity, d.Priority, d.Status, d.Assignee, d.Verifier, d.Sprint, d.Discipline, d.Progress, d.EstimatedHours, d.ActualHours, d.RequirementID, d.Tags, d.SourceExecutionID, now, now)
	if e != nil {
		return 0, e
	}
	id, e := res.LastInsertId()
	if e != nil {
		return 0, e
	}
	if _, e = exec.Exec(`UPDATE defects SET code=?,assignee_user_id=?,verifier_user_id=?,created_by=? WHERE id=? AND tenant_id=? AND project_id=?`, fmt.Sprintf("BUG-%04d", id), d.AssigneeUserID, d.VerifierUserID, a.uid(), id, tenantID, a.pid()); e != nil {
		return 0, e
	}
	return id, nil
}

func (a *App) insertDefect(d Defect) (int64, error) {
	// Demo seeding has names rather than validated IDs. Resolve them before
	// insertion; transaction callers supply their project-scoped IDs directly.
	var err error
	if d.AssigneeUserID == "" {
		d.AssigneeUserID, err = a.projectPerson(d.Assignee)
		if err != nil {
			return 0, err
		}
	}
	if d.VerifierUserID == "" {
		d.VerifierUserID, err = a.projectPerson(d.Verifier)
		if err != nil {
			return 0, err
		}
	}
	return a.insertDefectWith(a.db, d)
}
func (a *App) defects(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var d Defect
		if decodeJSON(r, &d) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		a.createDefectAtomic(w, r, d)
		return
	}
	q := `SELECT id,code,title,description,steps,actual,expected,environment,found_version,fix_version,severity,priority,status,assignee,verifier,sprint,discipline,progress,estimated_hours,actual_hours,requirement_id,tags,source_execution_id,updated_at,assignee_user_id,verifier_user_id FROM defects WHERE tenant_id=? AND project_id=?`
	args := []any{tenantID, a.pid()}
	known := map[string]bool{}
	for _, status := range []string{"新建", "已确认", "修复中", "已解决", "待验证", "已关闭", "重新打开", "已拒绝"} {
		known[status] = true
	}
	selected, err := parseStatusSelection(r, known)
	if err != nil {
		failState(w, err)
		return
	}
	q, args = addStatusSelection(q, args, selected)
	for _, f := range []struct{ k, c string }{{"severity", "severity"}, {"priority", "priority"}, {"assignee", "assignee"}, {"sprint", "sprint"}} {
		if v := r.URL.Query().Get(f.k); v != "" {
			q += " AND " + f.c + "=?"
			args = append(args, v)
		}
	}
	// 列表筛选使用不会随改名变化的成员 ID，姓名仅保留给兼容旧链接。
	// 前面的租户/项目谓词保证查询始终局限于当前项目的数据范围。
	for _, f := range []struct{ k, c string }{{"assigneeUserId", "assignee_user_id"}, {"verifierUserId", "verifier_user_id"}} {
		if v := strings.TrimSpace(r.URL.Query().Get(f.k)); v != "" {
			q += " AND " + f.c + "=?"
			args = append(args, v)
		}
	}
	// “与我相关”完全由当前会话推导，不接受前端传来的用户 ID；否则既会让
	// 快捷筛选语义含混，也可能暴露其他成员是否被分配到某个工作项。
	switch mine := strings.TrimSpace(r.URL.Query().Get("mine")); mine {
	case "", "0", "false":
	case "1", "true":
		predicate, relatedArgs := workItemRelatedSQL("defect", "defects", a.uid())
		q += " AND " + predicate
		args = append(args, relatedArgs...)
	default:
		fail(w, http.StatusUnprocessableEntity, "invalid_defect_filter", "与我相关筛选无效")
		return
	}
	if s := r.URL.Query().Get("q"); s != "" {
		q += " AND (title LIKE ? OR code LIKE ? OR instr(lower(" + workItemPeopleSearchSQL("defect", "defects") + "),lower(?))>0)"
		args = append(args, "%"+s+"%", "%"+s+"%", strings.TrimSpace(s))
	}
	q += " ORDER BY updated_at DESC"
	rows, queryErr := a.db.QueryContext(r.Context(), q, args...)
	if queryErr != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []Defect{}
	for rows.Next() {
		var d Defect
		if scanErr := rows.Scan(&d.ID, &d.Code, &d.Title, &d.Description, &d.Steps, &d.Actual, &d.Expected, &d.Environment, &d.FoundVersion, &d.FixVersion, &d.Severity, &d.Priority, &d.Status, &d.Assignee, &d.Verifier, &d.Sprint, &d.Discipline, &d.Progress, &d.EstimatedHours, &d.ActualHours, &d.RequirementID, &d.Tags, &d.SourceExecutionID, &d.UpdatedAt, &d.AssigneeUserID, &d.VerifierUserID); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		d.AllowedTransitions = defectTransitions(d.Status)
		d.CustomFields = a.customFields("defect", d.ID)
		out = append(out, d)
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out, "total": len(out)})
}
func (a *App) defect(w http.ResponseWriter, r *http.Request) {
	id, parts, e := pathID(r.URL.Path, "/api/defects/")
	if e != nil {
		fail(w, 400, "invalid_id", "缺陷编号不正确")
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "comments":
			a.entityComments(w, r, "defect", id)
		case "activities":
			a.entityActivities(w, "defect", id)
		default:
			fail(w, 404, "not_found", "资源不存在")
		}
		return
	}
	if r.Method == "GET" {
		d, e := a.getDefect(id)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				fail(w, 404, "not_found", "缺陷不存在")
			} else {
				fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
			}
			return
		}
		write(w, 200, d)
		return
	}
	if r.Method == "PATCH" {
		var p map[string]any
		if decodeJSON(r, &p) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		var currentStatus string
		if e := a.db.QueryRow(`SELECT status FROM defects WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&currentStatus); e != nil {
			fail(w, 404, "not_found", "缺陷不存在")
			return
		}
		if err := a.prepareDefectPatch(id, p); err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
		// prepareDefectPatch canonicalizes names to scoped stable IDs.  Keep the
		// before/after IDs outside the SQL loop so both assignee and verifier
		// notifications are emitted exactly once with the same transaction.
		var fieldWrites []requirementFieldWrite
		if cf, ok := p["customFields"].(map[string]any); ok {
			var err error
			fieldWrites, err = a.prepareObjectFields("defect", cf, false)
			if err != nil {
				fail(w, 422, "custom_field_invalid", err.Error())
				return
			}
		}
		allowed := map[string]string{"title": "title", "description": "description", "steps": "steps", "actual": "actual", "expected": "expected", "environment": "environment", "foundVersion": "found_version", "fixVersion": "fix_version", "severity": "severity", "priority": "priority", "status": "status", "assignee": "assignee", "verifier": "verifier", "sprint": "sprint", "tags": "tags", "discipline": "discipline", "progress": "progress", "estimatedHours": "estimated_hours", "actualHours": "actual_hours", "requirementId": "requirement_id"}
		for k, v := range p {
			if col := allowed[k]; col != "" {
				if (k == "progress" && (number(v) < 0 || number(v) > 100)) || ((k == "estimatedHours" || k == "actualHours") && number(v) < 0) {
					fail(w, 422, "validation_error", "进度须为 0–100，工时不能为负数")
					return
				}
				if k == "status" && !canTransition(currentStatus, fmt.Sprint(v), map[string][]string{currentStatus: defectTransitions(currentStatus)}) {
					fail(w, 422, "validation_error", fmt.Sprintf("当前状态“%s”不能流转到“%v”", currentStatus, v))
					return
				}
			}
		}
		statusValue, statusRequested := p["status"]
		statusText := fmt.Sprint(statusValue)
		actor := a.actorName()
		tx, e := a.db.Begin()
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		defer tx.Rollback()
		if !a.checkIntegrationPrecondition(w, r, tx) {
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		// 请求进入写事务前，另一位协作者可能已经完成同一状态流转。通知与
		// 责任人必须以事务内快照为准：不能用外层旧快照把“同状态重试”误当
		// 成新的状态变更，也不能向已被改掉的责任人投递。
		var txCurrentStatus, txCurrentTitle, txCurrentAssigneeID, txCurrentVerifierID string
		if e = tx.QueryRow(`SELECT status,title,assignee_user_id,verifier_user_id FROM defects WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&txCurrentStatus, &txCurrentTitle, &txCurrentAssigneeID, &txCurrentVerifierID); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				fail(w, 404, "not_found", "缺陷不存在")
			} else {
				fail(w, 500, "db_error", e.Error())
			}
			return
		}
		if statusRequested && !canTransition(txCurrentStatus, statusText, map[string][]string{txCurrentStatus: defectTransitions(txCurrentStatus)}) {
			fail(w, 422, "validation_error", fmt.Sprintf("当前状态“%s”不能流转到“%v”", txCurrentStatus, statusValue))
			return
		}
		nextTitle, nextAssigneeID, nextVerifierID := txCurrentTitle, txCurrentAssigneeID, txCurrentVerifierID
		if value, ok := p["title"]; ok {
			nextTitle = fmt.Sprint(value)
		}
		if value, ok := p["assigneeUserId"]; ok {
			nextAssigneeID = fmt.Sprint(value)
		}
		if value, ok := p["verifierUserId"]; ok {
			nextVerifierID = fmt.Sprint(value)
		}
		// 仅真实 old→new 流转才生成通知。状态值来自同一写事务，因此并发
		// 请求在这里看到已提交的新状态后不会制造第二条状态动态。
		statusChanged := statusRequested && statusText != txCurrentStatus
		statusRecipientCandidate := nextAssigneeID
		if statusText == "待验证" {
			statusRecipientCandidate = nextVerifierID
		}
		if err := a.writeObjectFields(tx, "defect", id, fieldWrites, now); err != nil {
			if failCustomFieldValidation(w, err) {
				return
			}
			fail(w, 500, "db_error", err.Error())
			return
		}
		if err := a.verifyDefectPatchPeople(tx, id, p); err != nil {
			failCollaboration(w, err)
			return
		}
		var statusActivityID int64
		for k, v := range p {
			if col := allowed[k]; col != "" {
				if _, e = tx.Exec(`UPDATE defects SET `+col+`=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, v, now, id, tenantID, a.pid()); e != nil {
					fail(w, 500, "db_error", e.Error())
					return
				}
				if k == "assignee" {
					_, e = tx.Exec(`UPDATE defects SET assignee_user_id=? WHERE id=? AND tenant_id=? AND project_id=?`, p["assigneeUserId"], id, tenantID, a.pid())
				}
				if k == "verifier" {
					_, e = tx.Exec(`UPDATE defects SET verifier_user_id=? WHERE id=? AND tenant_id=? AND project_id=?`, p["verifierUserId"], id, tenantID, a.pid())
				}
				if e != nil {
					fail(w, 500, "db_error", e.Error())
					return
				}
				activity, activityErr := tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "defect", id, actor, "updated", fmt.Sprintf("更新 %s 为 %v", k, v), now)
				if activityErr != nil {
					e = activityErr
					fail(w, 500, "db_error", e.Error())
					return
				}
				if k == "status" && statusChanged {
					statusActivityID, e = activity.LastInsertId()
					if e != nil {
						fail(w, 500, "db_error", e.Error())
						return
					}
				}
			}
		}
		if e == nil {
			e = a.writeAssignmentNotices(r.Context(), tx, defectAssignmentNotices(id, fmt.Sprintf("BUG-%04d", id), nextTitle, nextAssigneeID, nextVerifierID, txCurrentAssigneeID, txCurrentVerifierID), now)
		}
		if e == nil && statusChanged {
			recipient, recipientErr := a.firstActiveAssignmentRecipient(r.Context(), tx, []string{statusRecipientCandidate})
			if recipientErr != nil {
				fail(w, 500, "db_error", recipientErr.Error())
				return
			}
			if statusActivityID <= 0 {
				fail(w, 500, "db_error", "缺陷状态动态未写入")
				return
			}
			title := "缺陷状态已更新"
			if statusText == "待验证" {
				title = "缺陷已就绪，等待验证"
			}
			// 动态行 ID 是本次流转的稳定事件标识：支持“重新打开后再次确认”，
			// 同时不会把网络重试的同一状态误写为另一条通知。
			if e = a.recordWorkflowEvent(tx, recipient, "defect.status_changed", "defect", id, title, fmt.Sprintf("BUG-%04d 已流转至%s", id, statusText), fmt.Sprintf("defect-status:%s:%d:%d", a.pid(), id, statusActivityID)); e != nil {
				fail(w, 500, "db_error", e.Error())
				return
			}
		}
		if e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		d, e := a.getDefect(id)
		if e != nil {
			fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		write(w, 200, d)
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
func (a *App) getDefect(id int64) (Defect, error) {
	var d Defect
	e := a.db.QueryRow(`SELECT id,code,title,description,steps,actual,expected,environment,found_version,fix_version,severity,priority,status,assignee,verifier,sprint,discipline,progress,estimated_hours,actual_hours,requirement_id,tags,source_execution_id,updated_at,assignee_user_id,verifier_user_id FROM defects WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&d.ID, &d.Code, &d.Title, &d.Description, &d.Steps, &d.Actual, &d.Expected, &d.Environment, &d.FoundVersion, &d.FixVersion, &d.Severity, &d.Priority, &d.Status, &d.Assignee, &d.Verifier, &d.Sprint, &d.Discipline, &d.Progress, &d.EstimatedHours, &d.ActualHours, &d.RequirementID, &d.Tags, &d.SourceExecutionID, &d.UpdatedAt, &d.AssigneeUserID, &d.VerifierUserID)
	if e != nil {
		return d, e
	}
	d.AllowedTransitions = defectTransitions(d.Status)
	if d.Code == "" {
		d.Code = fmt.Sprintf("BUG-%04d", d.ID)
	} // Legacy rows: derive display code without rewriting user data.
	if d.SourceExecutionID != nil {
		var planID, caseID int64
		if relationErr := a.db.QueryRow(`SELECT e.plan_id,e.case_id,e.status,p.name,c.title FROM test_executions e JOIN test_plans p ON p.id=e.plan_id AND p.tenant_id=e.tenant_id AND p.project_id=e.project_id JOIN test_cases c ON c.id=e.case_id AND c.tenant_id=e.tenant_id AND c.project_id=e.project_id WHERE e.id=? AND e.tenant_id=? AND e.project_id=?`, *d.SourceExecutionID, tenantID, a.pid()).Scan(&planID, &caseID, &d.SourceExecStatus, &d.SourcePlanName, &d.SourceCaseTitle); relationErr == nil {
			d.SourcePlanID = &planID
			d.SourceCaseID = &caseID
		}
	}
	d.CustomFields, e = a.customFieldsUsing(a.db, "defect", id)
	return d, e
}

func (a *App) testCases(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var c TestCase
		if decodeJSON(r, &c) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		a.insertCaseAtomic(w, r, c, "created")
		return
	}
	values := r.URL.Query()
	// 旧客户端不带查询参数时继续收到 {items}；工作台带任意筛选/分页参数时
	// 使用有界 page/pageSize，避免大量 AI 用例一次性拖垮浏览器。
	paged := values.Has("q") || values.Has("status") || values.Has("priority") || values.Has("caseType") || values.Has("ownerUserId") || values.Has("requirementId") || values.Has("libraryId") || values.Has("folderId") || values.Has("page") || values.Has("pageSize")
	page, pageSize := 1, 20
	var queryErr error
	if paged && values.Has("page") {
		page, queryErr = strconv.Atoi(values.Get("page"))
		if queryErr != nil || page < 1 {
			fail(w, 400, "invalid_pagination", "页码无效")
			return
		}
	}
	if paged && values.Has("pageSize") {
		pageSize, queryErr = strconv.Atoi(values.Get("pageSize"))
		if queryErr != nil || pageSize < 1 || pageSize > 100 {
			fail(w, 400, "invalid_pagination", "每页数量须为 1–100")
			return
		}
	}
	where := []string{"c.tenant_id=?", "c.project_id=?"}
	args := []any{tenantID, a.pid()}
	if s := strings.TrimSpace(values.Get("q")); s != "" {
		if utf8.RuneCountInString(s) > 200 {
			fail(w, 400, "invalid_query", "搜索关键词过长")
			return
		}
		where = append(where, "(c.title LIKE ? OR c.code LIKE ?)")
		args = append(args, "%"+s+"%", "%"+s+"%")
	}
	for key, column := range map[string]string{"status": "c.status", "priority": "c.priority", "caseType": "c.type", "ownerUserId": "c.owner_user_id"} {
		if s := strings.TrimSpace(values.Get(key)); s != "" {
			if utf8.RuneCountInString(s) > 120 {
				fail(w, 400, "invalid_query", "筛选条件过长")
				return
			}
			where = append(where, column+"=?")
			args = append(args, s)
		}
	}
	for key, column := range map[string]string{"requirementId": "c.requirement_id", "libraryId": "l.library_id"} {
		if raw := values.Get(key); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 1 {
				fail(w, 400, "invalid_query", "关联编号无效")
				return
			}
			if key == "libraryId" {
				where = append(where, "EXISTS(SELECT 1 FROM testing_case_locations l WHERE l.tenant_id=c.tenant_id AND l.project_id=c.project_id AND l.case_id=c.id AND l.library_id=?)")
			} else {
				where = append(where, column+"=?")
			}
			args = append(args, id)
		}
	}
	if raw := values.Get("folderId"); raw != "" {
		folderID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || folderID < 1 {
			fail(w, 400, "invalid_query", "目录编号无效")
			return
		}
		// 递归 CTE 将目录及所有子目录收敛为当前项目内的 ID 集合，不能接受前端传来的
		// 任意子孙列表，从而避免跨库/跨项目 folderId 偷看用例。
		where = append(where, `EXISTS(SELECT 1 FROM testing_case_locations l WHERE l.tenant_id=c.tenant_id AND l.project_id=c.project_id AND l.case_id=c.id AND l.folder_id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM testing_folders WHERE id=? AND tenant_id=? AND project_id=? UNION ALL SELECT f.id FROM testing_folders f JOIN tree t ON f.parent_id=t.id WHERE f.tenant_id=? AND f.project_id=?) SELECT id FROM tree))`)
		args = append(args, folderID, tenantID, a.pid(), tenantID, a.pid())
	}
	base := ` FROM test_cases c LEFT JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.id=c.requirement_id WHERE ` + strings.Join(where, " AND ")
	total := 0
	if paged {
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*)`+base, args...).Scan(&total); err != nil {
			fail(w, 503, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
	}
	q := `SELECT c.id,c.code,c.category,c.title,c.preconditions,c.steps,c.expected,c.priority,c.status,c.owner,c.requirement_id,c.owner_user_id,c.type,c.tags,c.enabled,c.steps_json,c.updated_at,r.id,r.code,r.title,r.status` + base + ` ORDER BY c.updated_at DESC,c.id DESC`
	if paged {
		q += " LIMIT ? OFFSET ?"
		args = append(args, pageSize, (page-1)*pageSize)
	}
	rows, queryErr := a.db.QueryContext(r.Context(), q, args...)
	if queryErr != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []TestCase{}
	for rows.Next() {
		var c TestCase
		if scanErr := scanTestCaseWithRequirement(rows, &c); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		c.CustomFields = a.customFields("test_case", c.ID)
		if scanErr := a.hydrateTestingCase(r.Context(), &c); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		out = append(out, c)
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	if paged {
		write(w, 200, map[string]any{"items": out, "total": total, "page": page, "pageSize": pageSize})
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) testCase(w http.ResponseWriter, r *http.Request) {
	id, parts, e := pathID(r.URL.Path, "/api/test-cases/")
	if e != nil {
		fail(w, 400, "invalid_id", "用例编号不正确")
		return
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "comments":
			a.entityComments(w, r, "test_case", id)
		case "activities":
			a.entityActivities(w, "test_case", id)
		case "ai-review":
			a.testCaseAIReview(w, r, id)
		case "copy":
			if r.Method != "POST" {
				fail(w, 405, "method_not_allowed", "不支持的方法")
				return
			}
			c, e := a.getTestCase(id)
			if e != nil {
				fail(w, 404, "not_found", "用例不存在")
				return
			}
			c.Title += "（副本）"
			c.Status = "草稿"
			a.insertCaseAtomic(w, r, c, "copied")
		default:
			fail(w, 404, "not_found", "资源不存在")
		}
		return
	}
	if r.Method == "GET" {
		c, e := a.getTestCase(id)
		if e != nil {
			fail(w, 404, "not_found", "用例不存在")
			return
		}
		write(w, 200, c)
		return
	}
	if r.Method == "PATCH" {
		a.patchCaseAtomic(w, r, id)
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
func (a *App) getTestCase(id int64) (TestCase, error) {
	var c TestCase
	e := scanTestCaseWithRequirement(a.db.QueryRow(`SELECT c.id,c.code,c.category,c.title,c.preconditions,c.steps,c.expected,c.priority,c.status,c.owner,c.requirement_id,c.owner_user_id,c.type,c.tags,c.enabled,c.steps_json,c.updated_at,r.id,r.code,r.title,r.status FROM test_cases c LEFT JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.id=c.requirement_id WHERE c.id=? AND c.tenant_id=? AND c.project_id=?`, id, tenantID, a.pid()), &c)
	if e != nil {
		return c, e
	}
	c.CustomFields = a.customFields("test_case", id)
	if e = a.hydrateTestingCase(context.Background(), &c); e != nil {
		return c, e
	}
	return c, nil
}

func (a *App) testPlans(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		a.createPlanAtomic(w, r)
		return
	}
	rows, queryErr := a.db.QueryContext(r.Context(), `SELECT id,code,name,sprint,version,scope,owner,owner_user_id,start_date,end_date,status,environment,executor_user_id,updated_at FROM test_plans WHERE tenant_id=? AND project_id=? ORDER BY updated_at DESC`, tenantID, a.pid())
	if queryErr != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var p TestPlan
		if scanErr := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Sprint, &p.Version, &p.Scope, &p.Owner, &p.OwnerUserID, &p.StartDate, &p.EndDate, &p.Status, &p.Environment, &p.ExecutorUserID, &p.UpdatedAt); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		var total, executed, passed, failed int
		a.db.QueryRow(`SELECT COUNT(*),SUM(CASE WHEN status!='未执行' THEN 1 ELSE 0 END),SUM(CASE WHEN status='通过' THEN 1 ELSE 0 END),SUM(CASE WHEN status IN ('失败','阻塞') THEN 1 ELSE 0 END) FROM test_executions WHERE tenant_id=? AND project_id=? AND plan_id=?`, tenantID, a.pid(), p.ID).Scan(&total, &executed, &passed, &failed)
		out = append(out, map[string]any{"id": p.ID, "code": p.Code, "name": p.Name, "sprint": p.Sprint, "version": p.Version, "scope": p.Scope, "owner": p.Owner, "ownerUserId": p.OwnerUserID, "startDate": p.StartDate, "endDate": p.EndDate, "status": p.Status, "environment": p.Environment, "executorUserId": p.ExecutorUserID, "total": total, "executed": executed, "passed": passed, "failed": failed})
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out})
}
func (a *App) testPlan(w http.ResponseWriter, r *http.Request) {
	id, parts, e := pathID(r.URL.Path, "/api/test-plans/")
	if e != nil {
		fail(w, 400, "invalid_id", "计划编号不正确")
		return
	}
	if len(parts) > 1 {
		if len(parts) == 2 && parts[1] == "comments" {
			a.qualityComments(w, r, "test_plan", id)
		} else {
			fail(w, 404, "not_found", "资源不存在")
		}
		return
	}
	if r.Method == "GET" {
		var p TestPlan
		e = a.db.QueryRow(`SELECT id,code,name,sprint,version,scope,owner,owner_user_id,start_date,end_date,status,environment,executor_user_id,updated_at FROM test_plans WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&p.ID, &p.Code, &p.Name, &p.Sprint, &p.Version, &p.Scope, &p.Owner, &p.OwnerUserID, &p.StartDate, &p.EndDate, &p.Status, &p.Environment, &p.ExecutorUserID, &p.UpdatedAt)
		if e != nil {
			fail(w, 404, "not_found", "计划不存在")
			return
		}
		rows, queryErr := a.db.QueryContext(r.Context(), `SELECT case_id FROM test_plan_cases WHERE tenant_id=? AND project_id=? AND plan_id=?`, tenantID, a.pid(), id)
		if queryErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var cid int64
			if scanErr := rows.Scan(&cid); scanErr != nil {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
				return
			}
			p.CaseIDs = append(p.CaseIDs, cid)
		}
		if rows.Err() != nil {
			rows.Close()
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		rows.Close()
		write(w, 200, p)
		return
	}
	if r.Method == "PATCH" {
		a.patchPlanAtomic(w, r, id)
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
func (a *App) testExecutions(w http.ResponseWriter, r *http.Request) {
	q := `SELECT e.id,e.plan_id,e.case_id,e.status,e.executor,e.executed_at,e.note,e.defect_id,c.title,p.name,e.actual_result,e.executor_user_id FROM test_executions e JOIN test_cases c ON c.id=e.case_id AND c.project_id=e.project_id AND c.tenant_id=e.tenant_id JOIN test_plans p ON p.id=e.plan_id AND p.project_id=e.project_id AND p.tenant_id=e.tenant_id WHERE e.tenant_id=? AND e.project_id=?`
	args := []any{tenantID, a.pid()}
	if v := r.URL.Query().Get("planId"); v != "" {
		q += " AND e.plan_id=?"
		args = append(args, v)
	}
	q += " ORDER BY e.id"
	rows, queryErr := a.db.QueryContext(r.Context(), q, args...)
	if queryErr != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	defer rows.Close()
	out := []TestExecution{}
	summary := map[string]int{"total": 0, "executed": 0, "passed": 0, "failed": 0, "blocked": 0}
	for rows.Next() {
		var e TestExecution
		if scanErr := rows.Scan(&e.ID, &e.PlanID, &e.CaseID, &e.Status, &e.Executor, &e.ExecutedAt, &e.Note, &e.DefectID, &e.CaseTitle, &e.PlanName, &e.ActualResult, &e.ExecutorUserID); scanErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		out = append(out, e)
		summary["total"]++
		if e.Status != "未执行" {
			summary["executed"]++
		}
		if e.Status == "通过" {
			summary["passed"]++
		}
		if e.Status == "失败" {
			summary["failed"]++
		}
		if e.Status == "阻塞" {
			summary["blocked"]++
		}
	}
	if rows.Err() != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
		return
	}
	write(w, 200, map[string]any{"items": out, "summary": summary})
}

// testExecutionNotificationRecipient 只从测试计划/用例保存的稳定负责人 ID
// 选择收件人。执行失败不能回退到演示账户，也不能通过显示名猜测人员；无有效
// 负责人时仍保存业务历史和 outbox，但不向任意用户误投递站内通知。
func (a *App) testExecutionNotificationRecipient(ctx context.Context, tx *sql.Tx, executionID int64) (string, error) {
	var planOwnerID, caseOwnerID string
	if err := tx.QueryRowContext(ctx, `SELECT p.owner_user_id,c.owner_user_id
		FROM test_executions e
		JOIN test_plans p ON p.id=e.plan_id AND p.tenant_id=e.tenant_id AND p.project_id=e.project_id
		JOIN test_cases c ON c.id=e.case_id AND c.tenant_id=e.tenant_id AND c.project_id=e.project_id
		WHERE e.tenant_id=? AND e.project_id=? AND e.id=?`, tenantID, a.pid(), executionID).Scan(&planOwnerID, &caseOwnerID); err != nil {
		return "", err
	}
	return a.firstActiveAssignmentRecipient(ctx, tx, []string{planOwnerID, caseOwnerID})
}

func (a *App) testExecution(w http.ResponseWriter, r *http.Request) {
	id, parts, e := pathID(r.URL.Path, "/api/test-executions/")
	if e != nil {
		fail(w, 400, "invalid_id", "执行编号不正确")
		return
	}
	if len(parts) == 2 && parts[1] == "comments" {
		a.qualityComments(w, r, "test_execution", id)
		return
	}
	if len(parts) > 1 && parts[1] == "create-defect" {
		if r.Method != "POST" {
			fail(w, 405, "method_not_allowed", "不支持的方法")
			return
		}
		actor := a.actorName()
		tx, e := a.db.Begin()
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		defer tx.Rollback()
		var existing sql.NullInt64
		if err := tx.QueryRow(`SELECT defect_id FROM test_executions WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&existing); err != nil {
			fail(w, 404, "not_found", "执行不存在")
			return
		}
		if existing.Valid {
			fail(w, 409, "defect_already_created", fmt.Sprintf("已生成 BUG-%04d", existing.Int64))
			return
		}
		var ex TestExecution
		var req sql.NullInt64
		var sprint, planOwnerID, caseOwnerID string
		e = tx.QueryRow(`SELECT e.plan_id,e.case_id,e.status,e.note,e.actual_result,e.executor_user_id,c.title,c.requirement_id,c.owner_user_id,p.name,p.sprint,p.owner_user_id FROM test_executions e JOIN test_cases c ON c.id=e.case_id AND c.tenant_id=e.tenant_id AND c.project_id=e.project_id JOIN test_plans p ON p.id=e.plan_id AND p.tenant_id=e.tenant_id AND p.project_id=e.project_id WHERE e.id=? AND e.tenant_id=? AND e.project_id=?`, id, tenantID, a.pid()).Scan(&ex.PlanID, &ex.CaseID, &ex.Status, &ex.Note, &ex.ActualResult, &ex.ExecutorUserID, &ex.CaseTitle, &req, &caseOwnerID, &ex.PlanName, &sprint, &planOwnerID)
		if e != nil {
			fail(w, 404, "not_found", "执行不存在")
			return
		}
		if ex.Status != "失败" {
			fail(w, 422, "validation_error", "仅失败的测试执行可生成缺陷")
			return
		}
		recipient, recipientErr := a.firstActiveAssignmentRecipient(r.Context(), tx, []string{planOwnerID, caseOwnerID})
		if recipientErr != nil {
			fail(w, 500, "db_error", recipientErr.Error())
			return
		}
		var rid *int64
		if req.Valid {
			rid = &req.Int64
		}
		did, e := a.insertDefectWith(tx, Defect{Title: "[测试失败] " + ex.CaseTitle, Description: "来源计划：" + ex.PlanName + "\n执行备注：" + ex.Note, Steps: "参考测试用例执行步骤", Expected: "测试用例预期结果", Actual: ex.ActualResult, Severity: "严重", Priority: "P1", Status: "新建", Sprint: sprint, RequirementID: rid, SourceExecutionID: &id})
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, e = tx.Exec(`UPDATE test_executions SET defect_id=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=? AND defect_id IS NULL`, did, now, id, tenantID, a.pid()); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if _, e = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "test_execution", id, actor, "defect_created", fmt.Sprintf("失败执行生成 BUG-%04d", did), now); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if _, e = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "defect", did, actor, "created_from_execution", fmt.Sprintf("由 EXE-%04d 失败执行生成", id), now); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if _, e = tx.Exec(`INSERT INTO test_execution_history(tenant_id,project_id,execution_id,status,executor_user_id,actual_result,note,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), id, ex.Status, ex.ExecutorUserID, ex.ActualResult, fmt.Sprintf("失败执行已生成 BUG-%04d", did), now); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if e = a.recordWorkflowEvent(tx, recipient, "defect.created_from_execution", "defect", did, "测试失败已生成缺陷", fmt.Sprintf("EXE-%04d 已生成 BUG-%04d", id, did), fmt.Sprintf("defect-created-from-execution:%s:%d", a.pid(), id)); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		write(w, 201, map[string]any{"defectId": did, "code": fmt.Sprintf("BUG-%04d", did)})
		return
	}
	if len(parts) > 1 && parts[1] == "history" && r.Method == "GET" {
		var linkedDefect sql.NullInt64
		_ = a.db.QueryRow(`SELECT defect_id FROM test_executions WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&linkedDefect)
		rows, queryErr := a.db.QueryContext(r.Context(), `SELECT id,status,executor_user_id,actual_result,note,created_at FROM test_execution_history WHERE tenant_id=? AND project_id=? AND execution_id=? ORDER BY id DESC`, tenantID, a.pid(), id)
		if queryErr != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var hid int64
			var status, uid, actual, note, at, name string
			if scanErr := rows.Scan(&hid, &status, &uid, &actual, &note, &at); scanErr != nil {
				fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
				return
			}
			_ = a.db.QueryRow(`SELECT name FROM users WHERE tenant_id=? AND id=?`, tenantID, uid).Scan(&name)
			item := map[string]any{"id": hid, "status": status, "executorUserId": uid, "executor": name, "actualResult": actual, "note": note, "createdAt": at}
			if linkedDefect.Valid && strings.Contains(note, fmt.Sprintf("BUG-%04d", linkedDefect.Int64)) {
				item["defectId"] = linkedDefect.Int64
			}
			items = append(items, item)
		}
		if rows.Err() != nil {
			fail(w, http.StatusServiceUnavailable, "database_unavailable", "数据暂时无法读取，请稍后重试")
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method == "PATCH" {
		var p TestExecution
		decodeJSON(r, &p)
		if !validChoice(p.Status, []string{"未执行", "通过", "失败", "阻塞", "跳过"}) {
			fail(w, 422, "validation_error", "执行状态无效")
			return
		}
		_, name, _, _ := a.currentUser(r)
		at := ""
		if p.Status != "未执行" {
			at = time.Now().UTC().Format(time.RFC3339)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		tx, e := a.db.Begin()
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		defer tx.Rollback()
		if !a.checkIntegrationPrecondition(w, r, tx) {
			return
		}
		var previousStatus string
		if e = tx.QueryRow(`SELECT status FROM test_executions WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&previousStatus); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				fail(w, 404, "not_found", "执行不存在")
			} else {
				fail(w, 500, "db_error", e.Error())
			}
			return
		}
		statusChanged := previousStatus != p.Status
		// “阻塞”是团队可配置的执行状态。服务端在写事务里读取开关，不能仅靠
		// 前端隐藏选项，否则旧页面或直接 API 调用仍可绕过流程约束。
		if p.Status == "阻塞" {
			settings, settingsErr := a.readTestingSettings(r.Context(), tx)
			if settingsErr != nil {
				fail(w, 503, "database_unavailable", "测试设置暂时无法读取，请稍后重试")
				return
			}
			if !settings.BlockedEnabled {
				fail(w, 422, "validation_error", "当前项目未启用阻塞执行状态")
				return
			}
		}
		result, e := tx.Exec(`UPDATE test_executions SET status=?,executor=?,executor_user_id=?,executed_at=?,note=?,actual_result=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, p.Status, name, a.uid(), at, p.Note, p.ActualResult, now, id, tenantID, a.pid())
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			fail(w, 404, "not_found", "执行不存在")
			return
		}
		historyResult, e := tx.Exec(`INSERT INTO test_execution_history(tenant_id,project_id,execution_id,status,executor_user_id,actual_result,note,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), id, p.Status, a.uid(), p.ActualResult, p.Note, now)
		if e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if _, e = tx.Exec(`INSERT INTO entity_activities(tenant_id,project_id,object_type,object_id,actor,event,detail,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), "test_execution", id, name, "executed", "执行结果："+p.Status, now); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		if p.Status == "失败" && statusChanged {
			historyID, historyErr := historyResult.LastInsertId()
			if historyErr != nil {
				fail(w, 500, "db_error", historyErr.Error())
				return
			}
			recipient, recipientErr := a.testExecutionNotificationRecipient(r.Context(), tx, id)
			if recipientErr != nil {
				fail(w, 500, "db_error", recipientErr.Error())
				return
			}
			body := p.Note
			if body == "" {
				body = p.ActualResult
			}
			// 历史记录 ID 区分一次真实失败与“通过后再次失败”；同状态重试由于
			// statusChanged 为 false 不会生成新的事件或企业微信投递。
			if e = a.recordWorkflowEvent(tx, recipient, "test.failed", "test_execution", id, "测试执行失败", body, fmt.Sprintf("test-failed:%s:%d:%d", a.pid(), id, historyID)); e != nil {
				fail(w, 500, "db_error", e.Error())
				return
			}
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, "db_error", e.Error())
			return
		}
		write(w, 200, map[string]any{"id": id, "status": p.Status, "executor": name, "executorUserId": a.uid(), "executedAt": at, "note": p.Note, "actualResult": p.ActualResult})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}
