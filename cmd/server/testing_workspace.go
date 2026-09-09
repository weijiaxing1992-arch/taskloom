package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TestingLibrary、TestingFolder 与 TestingCaseLocation 组成测试用例库树。
// 目录归属始终带 tenant_id/project_id，不能只依赖自增 ID 作为权限边界。
type TestingLibrary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Count     int    `json:"count"`
}

type TestingFolder struct {
	ID        int64  `json:"id"`
	LibraryID int64  `json:"libraryId"`
	ParentID  *int64 `json:"parentId,omitempty"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
	Count     int    `json:"count"`
}

type TestingCaseLocation struct {
	CaseID    int64  `json:"caseId"`
	LibraryID int64  `json:"libraryId"`
	FolderID  *int64 `json:"folderId,omitempty"`
}

// TestingCaseMetadata 保存不适合塞入旧 test_cases 表的扩展内容。顶层
// preconditions/requirementId 仍保留在旧模型中，保持已有 API 的兼容性。
type TestingCaseMetadata struct {
	Description      string `json:"description"`
	TestData         string `json:"testData"`
	EstimatedMinutes int    `json:"estimatedMinutes"`
	LibraryID        *int64 `json:"libraryId,omitempty"`
	FolderID         *int64 `json:"folderId,omitempty"`
	// 以下两个字段是旧 test_cases 的一等字段；嵌套回显让动态表单只需读取
	// metadata 即可渲染，同时服务端仍在同一事务维护真实关联与前置条件。
	Preconditions string `json:"preconditions,omitempty"`
	RequirementID *int64 `json:"requirementId,omitempty"`
}

type TestingFieldSpec struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Required     bool   `json:"required"`
	ListVisible  bool   `json:"listVisible"`
	DefaultValue any    `json:"defaultValue"`
	Enabled      bool   `json:"enabled"`
}

// TestingSettings 是项目级测试治理配置。AI 规则只保存规则文本，不保存 API Key
// 或请求内容；真实密钥仍由企业级 AI 配置单独管理。
type TestingSettings struct {
	Version         int64              `json:"version"`
	Fields          []TestingFieldSpec `json:"fields"`
	BlockedEnabled  bool               `json:"blockedEnabled"`
	AIReviewRules   []string           `json:"aiReviewRules"`
	AILogicRules    []string           `json:"aiLogicRules"`
	BusinessContext string             `json:"businessContext"`
}

type TestingCaseHistoryItem struct {
	ID          int64          `json:"id"`
	CaseID      int64          `json:"caseId"`
	ActorUserID string         `json:"actorUserId"`
	ActorName   string         `json:"actorName"`
	Event       string         `json:"event"`
	Detail      map[string]any `json:"detail"`
	CreatedAt   string         `json:"createdAt"`
}

type TestingCaseReview struct {
	ID          int64  `json:"id"`
	CaseID      int64  `json:"caseId"`
	Decision    string `json:"decision"`
	Comment     string `json:"comment"`
	ActorUserID string `json:"actorUserId"`
	ActorName   string `json:"actorName"`
	CreatedAt   string `json:"createdAt"`
}

type TestingDesign struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	RequirementID *int64 `json:"requirementId,omitempty"`
	Description   string `json:"description"`
	OwnerUserID   string `json:"ownerUserId,omitempty"`
	// Tags 对外兼容旧页面的逗号文本；数据库仍保存规范 JSON 数组。
	Tags      string               `json:"tags"`
	Points    []TestingDesignPoint `json:"points"`
	CreatedAt string               `json:"createdAt"`
	UpdatedAt string               `json:"updatedAt"`
}

type TestingDesignPoint struct {
	ID       int64   `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Priority string  `json:"priority"`
	CaseIDs  []int64 `json:"caseIds"`
}

const (
	testingNameLimit       = 120
	testingTextLimit       = 20000
	testingContextLimit    = 20000
	testingMaxCaseMove     = 200
	testingMaxDesignPoints = 200
)

// 新项目的用例列表默认保持紧凑：长文本和时长在详情中查看，只有关联需求作为
// 可追溯性的高价值摘要默认展示。已持久化的 fields_json 完全不读取这里，因而不会
// 覆盖管理员已经调整过的列可见性。
var testingFieldDefaults = []TestingFieldSpec{
	{Key: "description", Name: "用例描述", Description: "说明测试目标、范围与边界", Required: false, ListVisible: false, DefaultValue: "", Enabled: true},
	{Key: "testData", Name: "测试数据", Description: "记录测试账号、输入数据或前置数据", Required: false, ListVisible: false, DefaultValue: "", Enabled: true},
	{Key: "preconditions", Name: "前置条件", Description: "执行前必须满足的条件", Required: false, ListVisible: false, DefaultValue: "", Enabled: true},
	{Key: "estimatedMinutes", Name: "预计时长", Description: "单次执行预计分钟数", Required: false, ListVisible: false, DefaultValue: 0, Enabled: true},
	{Key: "requirementId", Name: "关联需求", Description: "关联当前项目内的有效需求", Required: false, ListVisible: true, DefaultValue: nil, Enabled: true},
}

func defaultTestingSettings() TestingSettings {
	fields := make([]TestingFieldSpec, len(testingFieldDefaults))
	copy(fields, testingFieldDefaults)
	return TestingSettings{
		Version:         1,
		Fields:          fields,
		BlockedEnabled:  true,
		AIReviewRules:   []string{"检查步骤与预期结果是否一致", "检查关联需求覆盖范围"},
		AILogicRules:    []string{"优先发现边界条件和异常分支"},
		BusinessContext: "",
	}
}

// migrateTestingWorkspace 只创建独立的测试管理表和索引；不修改已有 test_cases
// 的列或删除旧分类。历史分类被幂等映射为默认库的根目录，便于平滑升级。
func (a *App) migrateTestingWorkspace() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS testing_libraries(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,name TEXT NOT NULL,is_default INTEGER NOT NULL DEFAULT 0,created_by TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,name));`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_testing_libraries_single_default ON testing_libraries(tenant_id,project_id) WHERE is_default=1;`,
		`CREATE INDEX IF NOT EXISTS idx_testing_libraries_scope ON testing_libraries(tenant_id,project_id,id);`,
		`CREATE TABLE IF NOT EXISTS testing_folders(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,library_id INTEGER NOT NULL,parent_id INTEGER NOT NULL DEFAULT 0,name TEXT NOT NULL,sort_order INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,library_id,parent_id,name));`,
		`CREATE INDEX IF NOT EXISTS idx_testing_folders_tree ON testing_folders(tenant_id,project_id,library_id,parent_id,sort_order,id);`,
		`CREATE TABLE IF NOT EXISTS testing_case_locations(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,case_id INTEGER NOT NULL,library_id INTEGER NOT NULL,folder_id INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,case_id));`,
		`CREATE INDEX IF NOT EXISTS idx_testing_case_locations_folder ON testing_case_locations(tenant_id,project_id,library_id,folder_id,case_id);`,
		`CREATE TABLE IF NOT EXISTS testing_case_metadata(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,case_id INTEGER NOT NULL,description TEXT NOT NULL DEFAULT '',test_data TEXT NOT NULL DEFAULT '',estimated_minutes INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,case_id));`,
		`CREATE TABLE IF NOT EXISTS testing_case_history(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,case_id INTEGER NOT NULL,actor_user_id TEXT NOT NULL,event TEXT NOT NULL,detail_json TEXT NOT NULL DEFAULT '{}',created_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_testing_case_history_timeline ON testing_case_history(tenant_id,project_id,case_id,id DESC);`,
		`CREATE TABLE IF NOT EXISTS testing_case_reviews(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,case_id INTEGER NOT NULL,decision TEXT NOT NULL,comment TEXT NOT NULL DEFAULT '',actor_user_id TEXT NOT NULL,created_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_testing_case_reviews_timeline ON testing_case_reviews(tenant_id,project_id,case_id,id DESC);`,
		`CREATE TABLE IF NOT EXISTS testing_settings(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,fields_json TEXT NOT NULL,blocked_enabled INTEGER NOT NULL DEFAULT 1,ai_review_rules_json TEXT NOT NULL DEFAULT '[]',ai_logic_rules_json TEXT NOT NULL DEFAULT '[]',business_context TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,updated_by TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id));`,
		`CREATE TABLE IF NOT EXISTS testing_designs(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,name TEXT NOT NULL,requirement_id INTEGER,description TEXT NOT NULL DEFAULT '',owner_user_id TEXT NOT NULL DEFAULT '',tags_json TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_testing_designs_scope ON testing_designs(tenant_id,project_id,updated_at DESC,id DESC);`,
		`CREATE TABLE IF NOT EXISTS testing_design_points(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,design_id INTEGER NOT NULL,title TEXT NOT NULL,category TEXT NOT NULL DEFAULT '',priority TEXT NOT NULL DEFAULT 'P2',sort_order INTEGER NOT NULL DEFAULT 0);`,
		`CREATE INDEX IF NOT EXISTS idx_testing_design_points_scope ON testing_design_points(tenant_id,project_id,design_id,sort_order,id);`,
		`CREATE TABLE IF NOT EXISTS testing_design_point_cases(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,point_id INTEGER NOT NULL,case_id INTEGER NOT NULL,PRIMARY KEY(tenant_id,project_id,point_id,case_id));`,
		`CREATE INDEX IF NOT EXISTS idx_testing_design_point_cases_case ON testing_design_point_cases(tenant_id,project_id,case_id,point_id);`,
	}
	for _, statement := range statements {
		if _, err := a.db.Exec(statement); err != nil {
			return err
		}
	}
	now := orgNow()
	// 若升级前已经有人建了同名库，优先把它安全提升为默认库；不会覆盖现有默认库。
	if _, err := a.db.Exec(`UPDATE testing_libraries SET is_default=1,updated_at=? WHERE id IN (SELECT l.id FROM testing_libraries l WHERE l.name='默认用例库' AND NOT EXISTS(SELECT 1 FROM testing_libraries d WHERE d.tenant_id=l.tenant_id AND d.project_id=l.project_id AND d.is_default=1))`, now); err != nil {
		return err
	}
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO testing_libraries(tenant_id,project_id,name,is_default,created_by,created_at,updated_at) SELECT p.tenant_id,p.id,'默认用例库',1,'system',?,? FROM projects p WHERE NOT EXISTS(SELECT 1 FROM testing_libraries l WHERE l.tenant_id=p.tenant_id AND l.project_id=p.id AND l.is_default=1)`, now, now); err != nil {
		return err
	}
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO testing_settings(tenant_id,project_id,fields_json,blocked_enabled,ai_review_rules_json,ai_logic_rules_json,business_context,version,updated_by,updated_at) SELECT p.tenant_id,p.id,?,1,?,?,'',1,'system',? FROM projects p`, jsonText(defaultTestingSettings().Fields), jsonText(defaultTestingSettings().AIReviewRules), jsonText(defaultTestingSettings().AILogicRules), now); err != nil {
		return err
	}
	// 历史 category 是展示分类，迁移后保留它并为默认库创建同名根目录。
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO testing_folders(tenant_id,project_id,library_id,parent_id,name,sort_order,created_at,updated_at) SELECT c.tenant_id,c.project_id,l.id,0,CASE WHEN trim(c.category)='' THEN '未分类' ELSE c.category END,0,?,? FROM test_cases c JOIN testing_libraries l ON l.tenant_id=c.tenant_id AND l.project_id=c.project_id AND l.is_default=1`, now, now); err != nil {
		return err
	}
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO testing_case_locations(tenant_id,project_id,case_id,library_id,folder_id,updated_at) SELECT c.tenant_id,c.project_id,c.id,l.id,COALESCE(f.id,0),? FROM test_cases c JOIN testing_libraries l ON l.tenant_id=c.tenant_id AND l.project_id=c.project_id AND l.is_default=1 LEFT JOIN testing_folders f ON f.tenant_id=c.tenant_id AND f.project_id=c.project_id AND f.library_id=l.id AND f.parent_id=0 AND f.name=CASE WHEN trim(c.category)='' THEN '未分类' ELSE c.category END`, now); err != nil {
		return err
	}
	return nil
}

func (a *App) ensureTestingDefaultLibrary(ctx context.Context, q stateStore) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM testing_libraries WHERE tenant_id=? AND project_id=? AND is_default=1`, tenantID, a.pid()).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := orgNow()
	if _, err = q.ExecContext(ctx, `INSERT OR IGNORE INTO testing_libraries(tenant_id,project_id,name,is_default,created_by,created_at,updated_at)VALUES(?,?, '默认用例库',1,'system',?,?)`, tenantID, a.pid(), now, now); err != nil {
		return 0, err
	}
	err = q.QueryRowContext(ctx, `SELECT id FROM testing_libraries WHERE tenant_id=? AND project_id=? AND is_default=1`, tenantID, a.pid()).Scan(&id)
	return id, err
}

func testingFieldDefault(key string) TestingFieldSpec {
	for _, item := range testingFieldDefaults {
		if item.Key == key {
			return item
		}
	}
	return TestingFieldSpec{}
}

func validateTestingFieldDefault(key string, value any) (any, error) {
	switch key {
	case "description", "testData", "preconditions":
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > testingTextLimit {
			return nil, orgInvalid("测试字段默认值格式不正确")
		}
		return text, nil
	case "estimatedMinutes":
		if value == nil {
			return 0, nil
		}
		number, ok := value.(float64)
		if !ok || number < 0 || number > 10080 || number != float64(int(number)) {
			return nil, orgInvalid("预计时长默认值必须为 0–10080 的整数")
		}
		return int(number), nil
	case "requirementId":
		if value == nil {
			return nil, nil
		}
		number, ok := value.(float64)
		if !ok || number < 1 || number > 9007199254740991 || number != float64(int64(number)) {
			return nil, orgInvalid("关联需求默认值格式不正确")
		}
		return int64(number), nil
	default:
		return nil, orgInvalid("测试字段配置无效")
	}
}

func normalizeTestingSettings(input TestingSettings) (TestingSettings, error) {
	defaults := defaultTestingSettings()
	if input.Fields == nil {
		input.Fields = defaults.Fields
	}
	if len(input.Fields) > len(testingFieldDefaults) {
		return TestingSettings{}, orgInvalid("测试字段数量无效")
	}
	byKey := map[string]TestingFieldSpec{}
	for _, field := range input.Fields {
		base := testingFieldDefault(field.Key)
		if base.Key == "" || byKey[field.Key].Key != "" {
			return TestingSettings{}, orgInvalid("测试字段配置无效")
		}
		field.Name = strings.TrimSpace(field.Name)
		field.Description = strings.TrimSpace(field.Description)
		if field.Name == "" {
			field.Name = base.Name
		}
		if utf8.RuneCountInString(field.Name) > testingNameLimit || utf8.RuneCountInString(field.Description) > 500 {
			return TestingSettings{}, orgInvalid("测试字段名称或说明过长")
		}
		if field.Required && !field.Enabled {
			return TestingSettings{}, orgInvalid("必填测试字段不能被停用")
		}
		var err error
		field.DefaultValue, err = validateTestingFieldDefault(field.Key, field.DefaultValue)
		if err != nil {
			return TestingSettings{}, err
		}
		byKey[field.Key] = field
	}
	// 省略字段不等于删除内置能力；补齐默认项保证创建校验和 AI 输入契约稳定。
	fields := make([]TestingFieldSpec, 0, len(testingFieldDefaults))
	for _, base := range testingFieldDefaults {
		if field := byKey[base.Key]; field.Key != "" {
			fields = append(fields, field)
		} else {
			fields = append(fields, base)
		}
	}
	input.Fields = fields
	var err error
	if input.AIReviewRules, err = stringSet(input.AIReviewRules, 30); err != nil {
		return TestingSettings{}, err
	}
	if input.AILogicRules, err = stringSet(input.AILogicRules, 30); err != nil {
		return TestingSettings{}, err
	}
	input.BusinessContext = strings.TrimSpace(input.BusinessContext)
	if utf8.RuneCountInString(input.BusinessContext) > testingContextLimit {
		return TestingSettings{}, orgInvalid("业务上下文不能超过 20000 字")
	}
	return input, nil
}

// readTestingSettings 是 AI 审阅与测试工作台共用的只读入口。q 可为 *sql.DB 或
// *sql.Tx，使 AI/用例写入能在同一个事务读取不可变的规则快照。
func (a *App) readTestingSettings(ctx context.Context, q stateStore) (TestingSettings, error) {
	settings := defaultTestingSettings()
	var fieldsJSON, reviewJSON, logicJSON string
	err := q.QueryRowContext(ctx, `SELECT fields_json,blocked_enabled,ai_review_rules_json,ai_logic_rules_json,business_context,version FROM testing_settings WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&fieldsJSON, &settings.BlockedEnabled, &reviewJSON, &logicJSON, &settings.BusinessContext, &settings.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return TestingSettings{}, err
	}
	if json.Unmarshal([]byte(fieldsJSON), &settings.Fields) != nil || json.Unmarshal([]byte(reviewJSON), &settings.AIReviewRules) != nil || json.Unmarshal([]byte(logicJSON), &settings.AILogicRules) != nil {
		return TestingSettings{}, errors.New("invalid testing settings")
	}
	return normalizeTestingSettings(settings)
}

func (a *App) testingWorkspaceAccess(ctx context.Context, q stateStore) (canEdit, canManage bool, err error) {
	var active, disabled, member, tenantAdmin bool
	var role string
	err = q.QueryRowContext(ctx, `SELECT u.active,u.operation_disabled,EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active'),EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' AND tm.role='tenant_admin'),COALESCE((SELECT pm.role FROM project_members pm WHERE pm.tenant_id=u.tenant_id AND pm.project_id=? AND pm.user_id=u.id),'') FROM users u WHERE u.tenant_id=? AND u.id=?`, a.pid(), tenantID, a.uid()).Scan(&active, &disabled, &member, &tenantAdmin, &role)
	if errors.Is(err, sql.ErrNoRows) || !active || !member {
		return false, false, &organizationError{403, "forbidden", "账号不可用"}
	}
	if err != nil {
		return false, false, err
	}
	if disabled {
		return false, false, &organizationError{403, "account_disabled", operationDisabledMessage}
	}
	if role == "" && !tenantAdmin {
		return false, false, &organizationError{403, "project_forbidden", "无权访问该项目"}
	}
	canEdit = tenantAdmin || (role != "" && role != "viewer")
	canManage = tenantAdmin || role == "project_admin"
	return canEdit, canManage, nil
}

func failTesting(w http.ResponseWriter, err error) {
	var specific *organizationError
	if errors.As(err, &specific) {
		fail(w, specific.Status, specific.Code, specific.Message)
		return
	}
	if failCustomFieldValidation(w, err) {
		return
	}
	fail(w, http.StatusServiceUnavailable, "testing_unavailable", "测试管理服务暂时不可用，请稍后重试")
}

func (a *App) beginTestingWrite(r *http.Request, manage bool) (*sql.Tx, error) {
	tx, err := a.beginCollaborationWrite(r)
	if err != nil {
		return nil, err
	}
	canEdit, canManage, err := a.testingWorkspaceAccess(r.Context(), tx)
	if err == nil && (!canEdit || manage && !canManage) {
		err = &organizationError{403, "forbidden", "当前角色没有测试管理权限"}
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func testingPositiveID(value int64) bool { return value > 0 && value <= 9007199254740991 }

func testingNullableID(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func testingString(value string, max int, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > max {
		return "", orgInvalid(label + "不能为空或过长")
	}
	return value, nil
}

func testingPathID(path, prefix string) (int64, []string, error) {
	value := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if value == "" {
		return 0, nil, errors.New("missing id")
	}
	parts := strings.Split(value, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !testingPositiveID(id) {
		return 0, nil, errors.New("invalid id")
	}
	return id, parts, nil
}

func testingSQLPlaceholders(size int) string {
	if size <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", size), ",")
}

// testingCaseScopeExists 不接受只靠 case_id 的读取。所有用例库、审核和设计
// 关联先走这条联合条件，历史记录或猜测 ID 都不能跨租户/项目操作对象。
func (a *App) testingCaseScopeExists(ctx context.Context, q stateStore, caseID int64) error {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM test_cases WHERE id=? AND tenant_id=? AND project_id=?`, caseID, tenantID, a.pid()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return &organizationError{Status: http.StatusNotFound, Code: "not_found", Message: "测试用例不存在"}
	}
	return err
}

func (a *App) testingLibraryExists(ctx context.Context, q stateStore, libraryID int64) error {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM testing_libraries WHERE id=? AND tenant_id=? AND project_id=?`, libraryID, tenantID, a.pid()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return orgInvalid("用例库不属于当前项目或不存在")
	}
	return err
}

func (a *App) testingFolder(ctx context.Context, q stateStore, folderID int64) (TestingFolder, error) {
	var item TestingFolder
	var parent int64
	err := q.QueryRowContext(ctx, `SELECT id,library_id,parent_id,name,sort_order FROM testing_folders WHERE id=? AND tenant_id=? AND project_id=?`, folderID, tenantID, a.pid()).Scan(&item.ID, &item.LibraryID, &parent, &item.Name, &item.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return item, orgInvalid("目录不属于当前项目或不存在")
	}
	if err != nil {
		return item, err
	}
	item.ParentID = testingNullableID(parent)
	return item, nil
}

func (a *App) testingCaseMetadata(ctx context.Context, q stateStore, caseID int64) (TestingCaseMetadata, error) {
	metadata := TestingCaseMetadata{}
	var libraryID, folderID int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(m.description,''),COALESCE(m.test_data,''),COALESCE(m.estimated_minutes,0),COALESCE(l.library_id,0),COALESCE(l.folder_id,0)
		FROM test_cases c
		LEFT JOIN testing_case_metadata m ON m.tenant_id=c.tenant_id AND m.project_id=c.project_id AND m.case_id=c.id
		LEFT JOIN testing_case_locations l ON l.tenant_id=c.tenant_id AND l.project_id=c.project_id AND l.case_id=c.id
		WHERE c.id=? AND c.tenant_id=? AND c.project_id=?`, caseID, tenantID, a.pid()).Scan(&metadata.Description, &metadata.TestData, &metadata.EstimatedMinutes, &libraryID, &folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return metadata, &organizationError{Status: http.StatusNotFound, Code: "not_found", Message: "测试用例不存在"}
	}
	if err != nil {
		return metadata, err
	}
	metadata.LibraryID = testingNullableID(libraryID)
	metadata.FolderID = testingNullableID(folderID)
	return metadata, nil
}

// assignTestingCaseLocation 在写事务中写入定位。libraryId/folderId 都为空时仅在
// 尚未映射的历史用例上补默认库，已有映射绝不被“空请求”覆盖；这是 AI 导入和
// 复制用例可以安全复用的惰性补偿入口。
func (a *App) assignTestingCaseLocation(ctx context.Context, q stateStore, caseID int64, libraryID, folderID *int64, now string) (TestingCaseMetadata, error) {
	if err := a.testingCaseScopeExists(ctx, q, caseID); err != nil {
		return TestingCaseMetadata{}, err
	}
	if libraryID == nil && folderID == nil {
		var currentLibrary int64
		err := q.QueryRowContext(ctx, `SELECT library_id FROM testing_case_locations WHERE tenant_id=? AND project_id=? AND case_id=?`, tenantID, a.pid(), caseID).Scan(&currentLibrary)
		if err == nil {
			return a.testingCaseMetadata(ctx, q, caseID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return TestingCaseMetadata{}, err
		}
		id, err := a.ensureTestingDefaultLibrary(ctx, q)
		if err != nil {
			return TestingCaseMetadata{}, err
		}
		libraryID = &id
	}
	if folderID != nil {
		folder, err := a.testingFolder(ctx, q, *folderID)
		if err != nil {
			return TestingCaseMetadata{}, err
		}
		if libraryID == nil {
			libraryID = &folder.LibraryID
		} else if *libraryID != folder.LibraryID {
			return TestingCaseMetadata{}, orgInvalid("目录不属于所选用例库")
		}
	}
	if libraryID == nil || !testingPositiveID(*libraryID) {
		return TestingCaseMetadata{}, orgInvalid("请选择有效用例库")
	}
	if err := a.testingLibraryExists(ctx, q, *libraryID); err != nil {
		return TestingCaseMetadata{}, err
	}
	folder := int64(0)
	if folderID != nil {
		folder = *folderID
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO testing_case_locations(tenant_id,project_id,case_id,library_id,folder_id,updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,case_id) DO UPDATE SET library_id=excluded.library_id,folder_id=excluded.folder_id,updated_at=excluded.updated_at`, tenantID, a.pid(), caseID, *libraryID, folder, now); err != nil {
		return TestingCaseMetadata{}, err
	}
	return a.testingCaseMetadata(ctx, q, caseID)
}

func (a *App) writeTestingCaseMetadata(ctx context.Context, q stateStore, caseID int64, metadata TestingCaseMetadata, now string) error {
	if utf8.RuneCountInString(metadata.Description) > testingTextLimit || utf8.RuneCountInString(metadata.TestData) > testingTextLimit || metadata.EstimatedMinutes < 0 || metadata.EstimatedMinutes > 10080 {
		return orgInvalid("用例描述、测试数据或预计时长无效")
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO testing_case_metadata(tenant_id,project_id,case_id,description,test_data,estimated_minutes,updated_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,case_id) DO UPDATE SET description=excluded.description,test_data=excluded.test_data,estimated_minutes=excluded.estimated_minutes,updated_at=excluded.updated_at`, tenantID, a.pid(), caseID, metadata.Description, metadata.TestData, metadata.EstimatedMinutes, now); err != nil {
		return err
	}
	_, err := a.assignTestingCaseLocation(ctx, q, caseID, metadata.LibraryID, metadata.FolderID, now)
	return err
}

func testingFieldByKey(settings TestingSettings, key string) TestingFieldSpec {
	for _, field := range settings.Fields {
		if field.Key == key {
			return field
		}
	}
	return testingFieldDefault(key)
}

// normalizeTestingCaseMetadata 合并设置默认值并检查“必填”规则。这样创建、编辑、
// 复制和 AI 导入走完全相同的服务端约束，前端隐藏字段不能绕过质量门禁。
func (a *App) normalizeTestingCaseMetadata(ctx context.Context, q stateStore, c *TestCase, metadata TestingCaseMetadata, supplied map[string]bool) (TestingCaseMetadata, error) {
	settings, err := a.readTestingSettings(ctx, q)
	if err != nil {
		return metadata, err
	}
	for _, key := range []string{"description", "testData", "preconditions", "estimatedMinutes", "requirementId"} {
		field := testingFieldByKey(settings, key)
		if !field.Enabled {
			continue
		}
		if !supplied[key] {
			switch key {
			case "description":
				if value, ok := field.DefaultValue.(string); ok && metadata.Description == "" {
					metadata.Description = value
				}
			case "testData":
				if value, ok := field.DefaultValue.(string); ok && metadata.TestData == "" {
					metadata.TestData = value
				}
			case "preconditions":
				if value, ok := field.DefaultValue.(string); ok && c.Preconditions == "" {
					c.Preconditions = value
				}
			case "estimatedMinutes":
				if value, ok := field.DefaultValue.(int); ok && metadata.EstimatedMinutes == 0 {
					metadata.EstimatedMinutes = value
				}
			case "requirementId":
				if value, ok := field.DefaultValue.(int64); ok && c.RequirementID == nil {
					c.RequirementID = &value
				}
			}
		}
		if field.Required {
			switch key {
			case "description":
				if strings.TrimSpace(metadata.Description) == "" {
					return metadata, orgInvalid("用例描述为必填项")
				}
			case "testData":
				if strings.TrimSpace(metadata.TestData) == "" {
					return metadata, orgInvalid("测试数据为必填项")
				}
			case "preconditions":
				if strings.TrimSpace(c.Preconditions) == "" {
					return metadata, orgInvalid("前置条件为必填项")
				}
			case "estimatedMinutes":
				if metadata.EstimatedMinutes <= 0 {
					return metadata, orgInvalid("预计时长为必填项")
				}
			case "requirementId":
				if c.RequirementID == nil {
					return metadata, orgInvalid("关联需求为必填项")
				}
			}
		}
	}
	metadata.Description = strings.TrimSpace(metadata.Description)
	metadata.TestData = strings.TrimSpace(metadata.TestData)
	if utf8.RuneCountInString(metadata.Description) > testingTextLimit || utf8.RuneCountInString(metadata.TestData) > testingTextLimit || metadata.EstimatedMinutes < 0 || metadata.EstimatedMinutes > 10080 {
		return metadata, orgInvalid("用例描述、测试数据或预计时长无效")
	}
	metadata.Preconditions = c.Preconditions
	metadata.RequirementID = c.RequirementID
	return metadata, nil
}

func (a *App) hydrateTestingCase(ctx context.Context, c *TestCase) error {
	metadata, err := a.testingCaseMetadata(ctx, a.db, c.ID)
	if err != nil {
		return err
	}
	metadata.Preconditions = c.Preconditions
	metadata.RequirementID = c.RequirementID
	c.Metadata = &metadata
	var decision string
	err = a.db.QueryRowContext(ctx, `SELECT decision FROM testing_case_reviews WHERE tenant_id=? AND project_id=? AND case_id=? ORDER BY id DESC LIMIT 1`, tenantID, a.pid(), c.ID).Scan(&decision)
	if err == nil {
		switch decision {
		case "submit":
			c.ReviewStatus = "submitted"
		case "approve":
			c.ReviewStatus = "approved"
		case "reject":
			c.ReviewStatus = "rejected"
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	} else if c.Status == "待评审" {
		c.ReviewStatus = "submitted"
	} else if c.Status == "已通过" {
		c.ReviewStatus = "approved"
	} else {
		c.ReviewStatus = "draft"
	}
	return nil
}

func (a *App) appendTestingCaseHistory(ctx context.Context, q stateStore, caseID int64, event string, detail map[string]any, now string) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO testing_case_history(tenant_id,project_id,case_id,actor_user_id,event,detail_json,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), caseID, a.uid(), event, jsonText(detail), now); err != nil {
		return err
	}
	return a.auditRequirementState(ctx, q, "test_case", "test_case."+event, caseID, detail)
}

// appendTestingCaseCreationHistory 与既有 entity_activities 并行保存一条轻量变更
// 轨迹。测试用例创建、复制和 AI 导入都会调用 recordTestCaseTrace；将其写入专属
// 时间线后，评审页无需从通用活动表猜测事件含义，也不会把步骤、测试数据等大字段
// 复制进审计表。
func (a *App) appendTestingCaseCreationHistory(ctx context.Context, q stateStore, caseID int64, event, detail, now string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO testing_case_history(tenant_id,project_id,case_id,actor_user_id,event,detail_json,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), caseID, a.uid(), event, jsonText(map[string]any{"detail": detail}), now)
	return err
}

func (a *App) listTestingLibraries(ctx context.Context, q stateStore) ([]TestingLibrary, error) {
	rows, err := q.QueryContext(ctx, `SELECT l.id,l.name,l.is_default,COUNT(c.case_id)
		FROM testing_libraries l LEFT JOIN testing_case_locations c ON c.tenant_id=l.tenant_id AND c.project_id=l.project_id AND c.library_id=l.id
		WHERE l.tenant_id=? AND l.project_id=? GROUP BY l.id,l.name,l.is_default ORDER BY l.is_default DESC,l.name COLLATE NOCASE,l.id`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TestingLibrary{}
	for rows.Next() {
		var item TestingLibrary
		if err := rows.Scan(&item.ID, &item.Name, &item.IsDefault, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) listTestingFolders(ctx context.Context, q stateStore) ([]TestingFolder, error) {
	rows, err := q.QueryContext(ctx, `SELECT f.id,f.library_id,f.parent_id,f.name,f.sort_order,COUNT(c.case_id)
		FROM testing_folders f LEFT JOIN testing_case_locations c ON c.tenant_id=f.tenant_id AND c.project_id=f.project_id AND c.folder_id=f.id
		WHERE f.tenant_id=? AND f.project_id=? GROUP BY f.id,f.library_id,f.parent_id,f.name,f.sort_order
		ORDER BY f.library_id,f.sort_order,f.name COLLATE NOCASE,f.id`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TestingFolder{}
	for rows.Next() {
		var item TestingFolder
		var parent int64
		if err := rows.Scan(&item.ID, &item.LibraryID, &parent, &item.Name, &item.SortOrder, &item.Count); err != nil {
			return nil, err
		}
		item.ParentID = testingNullableID(parent)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) listTestingLocations(ctx context.Context, q stateStore) ([]TestingCaseLocation, error) {
	rows, err := q.QueryContext(ctx, `SELECT case_id,library_id,folder_id FROM testing_case_locations WHERE tenant_id=? AND project_id=? ORDER BY case_id`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TestingCaseLocation{}
	for rows.Next() {
		var item TestingCaseLocation
		var folder int64
		if err := rows.Scan(&item.CaseID, &item.LibraryID, &folder); err != nil {
			return nil, err
		}
		item.FolderID = testingNullableID(folder)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) testingWorkspaceSnapshot(ctx context.Context, q stateStore) (map[string]any, error) {
	canEdit, canManage, err := a.testingWorkspaceAccess(ctx, q)
	if err != nil {
		return nil, err
	}
	libraries, err := a.listTestingLibraries(ctx, q)
	if err != nil {
		return nil, err
	}
	folders, err := a.listTestingFolders(ctx, q)
	if err != nil {
		return nil, err
	}
	locations, err := a.listTestingLocations(ctx, q)
	if err != nil {
		return nil, err
	}
	settings, err := a.readTestingSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	designs, err := a.listTestingDesigns(ctx, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"libraries": libraries, "folders": folders, "locations": locations, "settings": settings, "canManage": canManage, "canEdit": canEdit, "designs": designs}, nil
}

// testingWorkspace 统一测试库的项目级入口。每个分支都会通过工作区成员检查；
// 不把前端隐藏菜单当成权限控制，直接请求路径同样无法越权。
func (a *App) testingWorkspace(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/testing"), "/")
	if path == "" || path == "workspace" {
		if r.Method != http.MethodGet {
			fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
			return
		}
		snapshot, err := a.testingWorkspaceSnapshot(r.Context(), a.db)
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, http.StatusOK, snapshot)
		return
	}
	parts := strings.Split(path, "/")
	switch parts[0] {
	case "libraries":
		a.testingLibraries(w, r, parts[1:])
	case "folders":
		a.testingFolders(w, r, parts[1:])
	case "cases":
		if len(parts) == 2 && parts[1] == "move" {
			a.moveTestingCases(w, r)
			return
		}
		if len(parts) >= 3 {
			a.testingCaseWorkspaceResource(w, r, parts[1], parts[2:])
			return
		}
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
	case "settings":
		a.testingSettings(w, r)
	case "designs":
		a.testingDesigns(w, r, parts[1:])
	default:
		fail(w, http.StatusNotFound, "not_found", "资源不存在")
	}
}

func (a *App) testingLibraries(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
				failTesting(w, err)
				return
			}
			items, err := a.listTestingLibraries(r.Context(), a.db)
			if err != nil {
				failTesting(w, err)
				return
			}
			write(w, 200, map[string]any{"items": items})
		case http.MethodPost:
			var input struct {
				Name string `json:"name"`
			}
			if decodeJSON(r, &input) != nil {
				fail(w, 400, "invalid_json", "请求格式不正确")
				return
			}
			input.Name, _ = testingString(input.Name, testingNameLimit, "用例库名称")
			if input.Name == "" {
				fail(w, 422, "validation_error", "用例库名称不能为空或过长")
				return
			}
			tx, err := a.beginTestingWrite(r, true)
			if err != nil {
				failTesting(w, err)
				return
			}
			defer tx.Rollback()
			now := orgNow()
			result, err := tx.ExecContext(r.Context(), `INSERT INTO testing_libraries(tenant_id,project_id,name,is_default,created_by,created_at,updated_at)VALUES(?,?,?,0,?,?,?)`, tenantID, a.pid(), input.Name, a.uid(), now, now)
			if err == nil {
				id, _ := result.LastInsertId()
				err = a.auditRequirementState(r.Context(), tx, "testing_library", "testing_library.created", id, map[string]any{"name": input.Name})
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				failTesting(w, err)
				return
			}
			write(w, 201, map[string]any{"id": resultLastID(result), "name": input.Name, "isDefault": false, "count": 0})
		default:
			fail(w, 405, "method_not_allowed", "不支持的方法")
		}
		return
	}
	if len(parts) != 1 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !testingPositiveID(id) {
		fail(w, 400, "invalid_id", "编号不正确")
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Name string `json:"name"`
		}
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		name, err := testingString(input.Name, testingNameLimit, "用例库名称")
		if err != nil {
			failTesting(w, err)
			return
		}
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		if err = a.testingLibraryExists(r.Context(), tx, id); err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE testing_libraries SET name=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, name, orgNow(), id, tenantID, a.pid())
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_library", "testing_library.updated", id, map[string]any{"name": name})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"id": id, "name": name})
		return
	}
	if r.Method == http.MethodDelete {
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		var defaultLibrary bool
		err = tx.QueryRowContext(r.Context(), `SELECT is_default FROM testing_libraries WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&defaultLibrary)
		if errors.Is(err, sql.ErrNoRows) {
			err = orgInvalid("用例库不属于当前项目或不存在")
		}
		if err == nil && defaultLibrary {
			err = orgInvalid("默认用例库不能删除")
		}
		if err == nil {
			var children int
			err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM testing_folders WHERE tenant_id=? AND project_id=? AND library_id=?)+(SELECT COUNT(*) FROM testing_case_locations WHERE tenant_id=? AND project_id=? AND library_id=?)`, tenantID, a.pid(), id, tenantID, a.pid(), id).Scan(&children)
			if err == nil && children > 0 {
				err = orgInvalid("用例库非空，不能删除")
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM testing_libraries WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid())
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_library", "testing_library.deleted", id, map[string]any{})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"ok": true})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}

func resultLastID(result sql.Result) int64 { id, _ := result.LastInsertId(); return id }

type testingFolderInput struct {
	LibraryID int64  `json:"libraryId"`
	ParentID  *int64 `json:"parentId"`
	Name      string `json:"name"`
	// 指针用于区分“客户端明确排到 0”和旧客户端未提交排序字段。后者在
	// PATCH 时必须保留原排序，避免改名意外把目录挪到列表顶部。
	SortOrder *int `json:"sortOrder"`
}

func (input testingFolderInput) sortOrder() int {
	if input.SortOrder == nil {
		return 0
	}
	return *input.SortOrder
}

func (a *App) validateTestingFolderInput(ctx context.Context, q stateStore, input testingFolderInput, currentID int64) (testingFolderInput, error) {
	name, err := testingString(input.Name, testingNameLimit, "目录名称")
	if err != nil {
		return input, err
	}
	input.Name = name
	if !testingPositiveID(input.LibraryID) {
		return input, orgInvalid("请选择有效用例库")
	}
	if err = a.testingLibraryExists(ctx, q, input.LibraryID); err != nil {
		return input, err
	}
	if input.sortOrder() < 0 || input.sortOrder() > 1000000 {
		return input, orgInvalid("目录排序值无效")
	}
	if input.ParentID != nil {
		if *input.ParentID == currentID {
			return input, orgInvalid("目录不能以自己为父级")
		}
		parent, err := a.testingFolder(ctx, q, *input.ParentID)
		if err != nil {
			return input, err
		}
		if parent.LibraryID != input.LibraryID {
			return input, orgInvalid("父目录不属于所选用例库")
		}
		// 向上检查祖先，避免 PATCH 将目录拖进自己的子树形成无限循环。
		seen := map[int64]bool{currentID: true}
		for parent.ParentID != nil {
			if seen[parent.ID] {
				return input, orgInvalid("目录层级不能形成循环")
			}
			seen[parent.ID] = true
			parent, err = a.testingFolder(ctx, q, *parent.ParentID)
			if err != nil {
				return input, err
			}
		}
		if seen[parent.ID] {
			return input, orgInvalid("目录层级不能形成循环")
		}
	}
	return input, nil
}

func (a *App) testingFolders(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		if r.Method == http.MethodGet {
			if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
				failTesting(w, err)
				return
			}
			items, err := a.listTestingFolders(r.Context(), a.db)
			if err != nil {
				failTesting(w, err)
				return
			}
			write(w, 200, map[string]any{"items": items})
			return
		}
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed", "不支持的方法")
			return
		}
		var input testingFolderInput
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		input, err = a.validateTestingFolderInput(r.Context(), tx, input, 0)
		if err == nil {
			parent := int64(0)
			if input.ParentID != nil {
				parent = *input.ParentID
			}
			now := orgNow()
			res, e := tx.ExecContext(r.Context(), `INSERT INTO testing_folders(tenant_id,project_id,library_id,parent_id,name,sort_order,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), input.LibraryID, parent, input.Name, input.sortOrder(), now, now)
			err = e
			if err == nil {
				id, _ := res.LastInsertId()
				err = a.auditRequirementState(r.Context(), tx, "testing_folder", "testing_folder.created", id, map[string]any{"libraryId": input.LibraryID, "parentId": input.ParentID, "name": input.Name})
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				failTesting(w, err)
				return
			}
			write(w, 201, map[string]any{"id": resultLastID(res), "libraryId": input.LibraryID, "parentId": input.ParentID, "name": input.Name, "sortOrder": input.sortOrder(), "count": 0})
			return
		}
		failTesting(w, err)
		return
	}
	if len(parts) != 1 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !testingPositiveID(id) {
		fail(w, 400, "invalid_id", "编号不正确")
		return
	}
	if r.Method == http.MethodPatch {
		var input testingFolderInput
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		current, err := a.testingFolder(r.Context(), tx, id)
		if err != nil {
			failTesting(w, err)
			return
		}
		if input.SortOrder == nil {
			// 与旧版本页面兼容：其目录编辑请求尚未带 sortOrder。
			sortOrder := current.SortOrder
			input.SortOrder = &sortOrder
		}
		// PATCH 必须完整给出库、父级和名称，避免隐藏的旧父级导致跨库移动。
		input, err = a.validateTestingFolderInput(r.Context(), tx, input, id)
		if err == nil && input.LibraryID != current.LibraryID {
			// 目录下仍有子目录或用例时不能单独换库，否则后代与定位记录会
			// 留在旧库而形成不可达数据。调用方应先移动用例并整理空树。
			var descendants int
			err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM testing_folders WHERE tenant_id=? AND project_id=? AND parent_id=?)+(SELECT COUNT(*) FROM testing_case_locations WHERE tenant_id=? AND project_id=? AND folder_id=?)`, tenantID, a.pid(), id, tenantID, a.pid(), id).Scan(&descendants)
			if err == nil && descendants > 0 {
				err = orgInvalid("非空目录不能直接切换用例库")
			}
		}
		if err == nil {
			parent := int64(0)
			if input.ParentID != nil {
				parent = *input.ParentID
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE testing_folders SET library_id=?,parent_id=?,name=?,sort_order=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, input.LibraryID, parent, input.Name, input.sortOrder(), orgNow(), id, tenantID, a.pid())
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_folder", "testing_folder.updated", id, map[string]any{"libraryId": input.LibraryID, "parentId": input.ParentID, "name": input.Name})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"id": id, "libraryId": input.LibraryID, "parentId": input.ParentID, "name": input.Name, "sortOrder": input.sortOrder()})
		return
	}
	if r.Method == http.MethodDelete {
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = a.testingFolder(r.Context(), tx, id); err == nil {
			var used int
			err = tx.QueryRowContext(r.Context(), `SELECT (SELECT COUNT(*) FROM testing_folders WHERE tenant_id=? AND project_id=? AND parent_id=?)+(SELECT COUNT(*) FROM testing_case_locations WHERE tenant_id=? AND project_id=? AND folder_id=?)`, tenantID, a.pid(), id, tenantID, a.pid(), id).Scan(&used)
			if err == nil && used > 0 {
				err = orgInvalid("目录非空，不能删除")
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM testing_folders WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid())
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_folder", "testing_folder.deleted", id, map[string]any{})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"ok": true})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}

func (a *App) moveTestingCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		CaseIDs   []int64 `json:"caseIds"`
		LibraryID *int64  `json:"libraryId"`
		FolderID  *int64  `json:"folderId"`
	}
	if decodeJSON(r, &input) != nil || len(input.CaseIDs) == 0 || len(input.CaseIDs) > testingMaxCaseMove {
		fail(w, 422, "validation_error", "请选择 1–200 个测试用例")
		return
	}
	seen := map[int64]bool{}
	for _, id := range input.CaseIDs {
		if !testingPositiveID(id) || seen[id] {
			fail(w, 422, "validation_error", "测试用例编号无效")
			return
		}
		seen[id] = true
	}
	tx, err := a.beginTestingWrite(r, false)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer tx.Rollback()
	// 先全量确认目标和源对象，再逐条写入；任一条失败会整体回滚，不留下半移动状态。
	if input.LibraryID == nil && input.FolderID == nil {
		fail(w, 422, "validation_error", "请选择目标用例库或目录")
		return
	}
	if input.FolderID != nil {
		folder, e := a.testingFolder(r.Context(), tx, *input.FolderID)
		err = e
		if err == nil && input.LibraryID != nil && folder.LibraryID != *input.LibraryID {
			err = orgInvalid("目录不属于所选用例库")
		}
		if err == nil && input.LibraryID == nil {
			input.LibraryID = &folder.LibraryID
		}
	}
	if err == nil {
		if input.LibraryID == nil {
			err = orgInvalid("请选择目标用例库")
		} else {
			err = a.testingLibraryExists(r.Context(), tx, *input.LibraryID)
		}
	}
	if err == nil {
		for _, id := range input.CaseIDs {
			if e := a.testingCaseScopeExists(r.Context(), tx, id); e != nil {
				err = e
				break
			}
		}
	}
	now := orgNow()
	if err == nil {
		for _, id := range input.CaseIDs {
			if _, e := a.assignTestingCaseLocation(r.Context(), tx, id, input.LibraryID, input.FolderID, now); e != nil {
				err = e
				break
			}
			if e := a.appendTestingCaseHistory(r.Context(), tx, id, "moved", map[string]any{"libraryId": input.LibraryID, "folderId": input.FolderID}, now); e != nil {
				err = e
				break
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failTesting(w, err)
		return
	}
	write(w, 200, map[string]any{"moved": len(input.CaseIDs), "libraryId": input.LibraryID, "folderId": input.FolderID})
}

type testingMetadataPatch struct {
	Metadata         TestingCaseMetadata
	Supplied         map[string]bool
	RequirementSet   bool
	PreconditionsSet bool
}

func testingDecodeOptionalID(raw json.RawMessage) (*int64, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || !testingPositiveID(value) {
		return nil, orgInvalid("关联编号格式无效")
	}
	return &value, nil
}

// parseTestingMetadataPatch 用 RawMessage 保留“未传、显式清空、传零值”的区别；
// 普通结构体无法区分这三种 PATCH 语义，容易把目录或需求意外清空。
func parseTestingMetadataPatch(raw json.RawMessage, base TestingCaseMetadata) (testingMetadataPatch, error) {
	result := testingMetadataPatch{Metadata: base, Supplied: map[string]bool{}}
	if len(raw) == 0 || string(raw) == "null" {
		return result, orgInvalid("metadata 必须是对象")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return result, orgInvalid("metadata 格式无效")
	}
	for key, value := range values {
		result.Supplied[key] = true
		switch key {
		case "description":
			if string(value) == "null" || json.Unmarshal(value, &result.Metadata.Description) != nil {
				return result, orgInvalid("description 格式无效")
			}
		case "testData":
			if string(value) == "null" || json.Unmarshal(value, &result.Metadata.TestData) != nil {
				return result, orgInvalid("testData 格式无效")
			}
		case "estimatedMinutes":
			if string(value) == "null" || json.Unmarshal(value, &result.Metadata.EstimatedMinutes) != nil {
				return result, orgInvalid("estimatedMinutes 格式无效")
			}
		case "libraryId":
			id, err := testingDecodeOptionalID(value)
			if err != nil {
				return result, err
			}
			result.Metadata.LibraryID = id
		case "folderId":
			id, err := testingDecodeOptionalID(value)
			if err != nil {
				return result, err
			}
			result.Metadata.FolderID = id
		case "preconditions":
			if string(value) == "null" || json.Unmarshal(value, &result.Metadata.Preconditions) != nil {
				return result, orgInvalid("preconditions 格式无效")
			}
			result.PreconditionsSet = true
		case "requirementId":
			id, err := testingDecodeOptionalID(value)
			if err != nil {
				return result, err
			}
			result.Metadata.RequirementID = id
			result.RequirementSet = true
		default:
			return result, orgInvalid("metadata 包含不支持的字段")
		}
	}
	return result, nil
}

func (a *App) testingCaseWorkspaceResource(w http.ResponseWriter, r *http.Request, rawID string, parts []string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || !testingPositiveID(id) {
		fail(w, 400, "invalid_id", "用例编号不正确")
		return
	}
	if len(parts) != 1 {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	switch parts[0] {
	case "metadata":
		a.testingCaseMetadataResource(w, r, id)
	case "history":
		a.testingCaseHistoryResource(w, r, id)
	case "reviews":
		a.testingCaseReviewsResource(w, r, id)
	default:
		fail(w, 404, "not_found", "资源不存在")
	}
}

func (a *App) testingCaseMetadataResource(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method == http.MethodGet {
		if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
			failTesting(w, err)
			return
		}
		c, err := a.getTestCase(id)
		if err != nil {
			failTesting(w, err)
			return
		}
		if c.Metadata == nil {
			fail(w, 404, "not_found", "测试用例不存在")
			return
		}
		write(w, 200, c.Metadata)
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body map[string]json.RawMessage
	if decodeJSON(r, &body) != nil || body == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	raw, ok := body["metadata"]
	if !ok {
		raw, _ = json.Marshal(body)
	}
	current, err := a.getTestCase(id)
	if err != nil {
		failTesting(w, err)
		return
	}
	if current.Metadata == nil {
		current.Metadata = &TestingCaseMetadata{}
	}
	patch, err := parseTestingMetadataPatch(raw, *current.Metadata)
	if err != nil {
		failTesting(w, err)
		return
	}
	next := current
	next.Preconditions = patch.Metadata.Preconditions
	next.RequirementID = patch.Metadata.RequirementID
	tx, err := a.beginTestingWrite(r, false)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer tx.Rollback()
	// 仅对本次 metadata PATCH 校验必填约束，不静默给既有用例灌入后来新增的默认值。
	allSupplied := map[string]bool{"description": true, "testData": true, "preconditions": true, "estimatedMinutes": true, "requirementId": true}
	patch.Metadata, err = a.normalizeTestingCaseMetadata(r.Context(), tx, &next, patch.Metadata, allSupplied)
	if err == nil && patch.RequirementSet {
		err = a.validateTestCaseRequirement(r.Context(), tx, next.RequirementID)
	}
	now := orgNow()
	if err == nil && (patch.PreconditionsSet || patch.RequirementSet) {
		_, err = tx.ExecContext(r.Context(), `UPDATE test_cases SET preconditions=?,requirement_id=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, next.Preconditions, next.RequirementID, now, id, tenantID, a.pid())
	}
	if err == nil {
		err = a.writeTestingCaseMetadata(r.Context(), tx, id, patch.Metadata, now)
	}
	if err == nil {
		err = a.appendTestingCaseHistory(r.Context(), tx, id, "metadata_updated", map[string]any{"fields": patch.Supplied}, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failTesting(w, err)
		return
	}
	saved, err := a.getTestCase(id)
	if err != nil {
		failTesting(w, err)
		return
	}
	write(w, 200, saved.Metadata)
}

func (a *App) testingCaseHistoryResource(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
		failTesting(w, err)
		return
	}
	if err := a.testingCaseScopeExists(r.Context(), a.db, id); err != nil {
		failTesting(w, err)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT h.id,h.case_id,h.actor_user_id,COALESCE(u.name,''),h.event,h.detail_json,h.created_at FROM testing_case_history h LEFT JOIN users u ON u.tenant_id=h.tenant_id AND u.id=h.actor_user_id WHERE h.tenant_id=? AND h.project_id=? AND h.case_id=? ORDER BY h.id DESC LIMIT 200`, tenantID, a.pid(), id)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer rows.Close()
	items := []TestingCaseHistoryItem{}
	for rows.Next() {
		var item TestingCaseHistoryItem
		var detail string
		if err := rows.Scan(&item.ID, &item.CaseID, &item.ActorUserID, &item.ActorName, &item.Event, &detail, &item.CreatedAt); err != nil {
			failTesting(w, err)
			return
		}
		if json.Unmarshal([]byte(detail), &item.Detail) != nil {
			item.Detail = map[string]any{}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		failTesting(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items})
}

func (a *App) testingReviewPermission(ctx context.Context, q stateStore) (bool, error) {
	var tenantAdmin bool
	var role string
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_memberships tm WHERE tm.tenant_id=? AND tm.user_id=? AND tm.status='active' AND tm.role='tenant_admin'),COALESCE((SELECT role FROM project_members WHERE tenant_id=? AND project_id=? AND user_id=?),'')`, tenantID, a.uid(), tenantID, a.pid(), a.uid()).Scan(&tenantAdmin, &role)
	return tenantAdmin || role == "project_admin" || role == "qa", err
}

func (a *App) testingCurrentReviewState(ctx context.Context, q stateStore, id int64, fallback string) (string, error) {
	var decision string
	err := q.QueryRowContext(ctx, `SELECT decision FROM testing_case_reviews WHERE tenant_id=? AND project_id=? AND case_id=? ORDER BY id DESC LIMIT 1`, tenantID, a.pid(), id).Scan(&decision)
	if errors.Is(err, sql.ErrNoRows) {
		if fallback == "待评审" {
			return "submitted", nil
		}
		if fallback == "已通过" {
			return "approved", nil
		}
		return "draft", nil
	}
	if err != nil {
		return "", err
	}
	if decision == "submit" {
		return "submitted", nil
	}
	if decision == "approve" {
		return "approved", nil
	}
	return "rejected", nil
}

func (a *App) testingCaseReviewsResource(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method == http.MethodGet {
		if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
			failTesting(w, err)
			return
		}
		if err := a.testingCaseScopeExists(r.Context(), a.db, id); err != nil {
			failTesting(w, err)
			return
		}
		rows, err := a.db.QueryContext(r.Context(), `SELECT r.id,r.case_id,r.decision,r.comment,r.actor_user_id,COALESCE(u.name,''),r.created_at FROM testing_case_reviews r LEFT JOIN users u ON u.tenant_id=r.tenant_id AND u.id=r.actor_user_id WHERE r.tenant_id=? AND r.project_id=? AND r.case_id=? ORDER BY r.id DESC LIMIT 200`, tenantID, a.pid(), id)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer rows.Close()
		items := []TestingCaseReview{}
		for rows.Next() {
			var item TestingCaseReview
			if err := rows.Scan(&item.ID, &item.CaseID, &item.Decision, &item.Comment, &item.ActorUserID, &item.ActorName, &item.CreatedAt); err != nil {
				failTesting(w, err)
				return
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var input struct {
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if decodeJSON(r, &input) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.Comment = strings.TrimSpace(input.Comment)
	if !validChoice(input.Decision, []string{"submit", "approve", "reject"}) || utf8.RuneCountInString(input.Comment) > testingTextLimit {
		fail(w, 422, "validation_error", "审核决定或评论无效")
		return
	}
	tx, err := a.beginTestingWrite(r, false)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer tx.Rollback()
	var status string
	if err = a.testingCaseScopeExists(r.Context(), tx, id); err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT status FROM test_cases WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&status)
	}
	var reviewer bool
	if err == nil {
		reviewer, err = a.testingReviewPermission(r.Context(), tx)
	}
	state := ""
	if err == nil {
		state, err = a.testingCurrentReviewState(r.Context(), tx, id, status)
	}
	if err == nil && input.Decision == "submit" && state != "draft" && state != "rejected" {
		err = orgInvalid("当前审核状态不能提交")
	}
	if err == nil && (input.Decision == "approve" || input.Decision == "reject") && !reviewer {
		err = &organizationError{Status: 403, Code: "forbidden", Message: "仅测试人员或管理员可审核"}
	}
	if err == nil && (input.Decision == "approve" || input.Decision == "reject") && state != "submitted" {
		err = orgInvalid("当前审核状态不能审批或驳回")
	}
	now := orgNow()
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO testing_case_reviews(tenant_id,project_id,case_id,decision,comment,actor_user_id,created_at)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), id, input.Decision, input.Comment, a.uid(), now)
	}
	if err == nil {
		nextStatus := status
		if input.Decision == "submit" {
			nextStatus = "待评审"
		}
		if input.Decision == "approve" {
			nextStatus = "已通过"
		}
		if input.Decision == "reject" {
			nextStatus = "草稿"
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE test_cases SET status=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, nextStatus, now, id, tenantID, a.pid())
	}
	if err == nil {
		err = a.appendTestingCaseHistory(r.Context(), tx, id, "review_"+input.Decision, map[string]any{"decision": input.Decision, "comment": input.Comment}, now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failTesting(w, err)
		return
	}
	write(w, 201, map[string]any{"caseId": id, "decision": input.Decision, "comment": input.Comment, "actorUserId": a.uid(), "actorName": a.actorName(), "createdAt": now})
}

func (a *App) validateTestingSettingsReferences(ctx context.Context, q stateStore, settings TestingSettings) error {
	field := testingFieldByKey(settings, "requirementId")
	if field.DefaultValue == nil {
		return nil
	}
	id, ok := field.DefaultValue.(int64)
	if !ok {
		return orgInvalid("关联需求默认值格式不正确")
	}
	return a.validateTestCaseRequirement(ctx, q, &id)
}

func (a *App) testingSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		_, canManage, err := a.testingWorkspaceAccess(r.Context(), a.db)
		if err != nil {
			failTesting(w, err)
			return
		}
		settings, err := a.readTestingSettings(r.Context(), a.db)
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"version": settings.Version, "fields": settings.Fields, "blockedEnabled": settings.BlockedEnabled, "aiReviewRules": settings.AIReviewRules, "aiLogicRules": settings.AILogicRules, "businessContext": settings.BusinessContext, "canManage": canManage})
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var patch map[string]json.RawMessage
	if decodeJSON(r, &patch) != nil || patch == nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	allowed := map[string]bool{"version": true, "fields": true, "blockedEnabled": true, "aiReviewRules": true, "aiLogicRules": true, "businessContext": true, "canManage": true}
	for key := range patch {
		if !allowed[key] {
			fail(w, 422, "validation_error", "测试设置包含不支持的字段")
			return
		}
	}
	tx, err := a.beginTestingWrite(r, true)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer tx.Rollback()
	settings, err := a.readTestingSettings(r.Context(), tx)
	if err != nil {
		failTesting(w, err)
		return
	}
	var requestedVersion *int64
	if raw, ok := patch["version"]; ok {
		var value int64
		if json.Unmarshal(raw, &value) != nil || value < 1 {
			fail(w, 422, "validation_error", "设置版本无效")
			return
		}
		requestedVersion = &value
	}
	if raw, ok := patch["fields"]; ok {
		if json.Unmarshal(raw, &settings.Fields) != nil {
			fail(w, 422, "validation_error", "字段配置格式无效")
			return
		}
	}
	if raw, ok := patch["blockedEnabled"]; ok {
		if json.Unmarshal(raw, &settings.BlockedEnabled) != nil {
			fail(w, 422, "validation_error", "阻塞开关格式无效")
			return
		}
	}
	if raw, ok := patch["aiReviewRules"]; ok {
		if json.Unmarshal(raw, &settings.AIReviewRules) != nil {
			fail(w, 422, "validation_error", "AI 审阅规则格式无效")
			return
		}
	}
	if raw, ok := patch["aiLogicRules"]; ok {
		if json.Unmarshal(raw, &settings.AILogicRules) != nil {
			fail(w, 422, "validation_error", "AI 逻辑规则格式无效")
			return
		}
	}
	if raw, ok := patch["businessContext"]; ok {
		if json.Unmarshal(raw, &settings.BusinessContext) != nil {
			fail(w, 422, "validation_error", "业务上下文格式无效")
			return
		}
	}
	// canManage 是 GET 回包中的展示能力；允许前端把完整设置对象原样回传，
	// 但绝不采纳客户端声称的权限值，真实权限仍由 beginTestingWrite 校验。
	settings, err = normalizeTestingSettings(settings)
	if err == nil {
		err = a.validateTestingSettingsReferences(r.Context(), tx, settings)
	}
	if err == nil && requestedVersion != nil && *requestedVersion != settings.Version {
		err = &organizationError{Status: 409, Code: "version_conflict", Message: "测试设置已被其他成员更新，请刷新后重试"}
	}
	now := orgNow()
	if err == nil {
		nextVersion := settings.Version + 1
		_, err = tx.ExecContext(r.Context(), `UPDATE testing_settings SET fields_json=?,blocked_enabled=?,ai_review_rules_json=?,ai_logic_rules_json=?,business_context=?,version=?,updated_by=?,updated_at=? WHERE tenant_id=? AND project_id=? AND version=?`, jsonText(settings.Fields), settings.BlockedEnabled, jsonText(settings.AIReviewRules), jsonText(settings.AILogicRules), settings.BusinessContext, nextVersion, a.uid(), now, tenantID, a.pid(), settings.Version)
		if err == nil {
			settings.Version = nextVersion
		}
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "testing_settings", "testing_settings.updated", a.pid(), map[string]any{"version": settings.Version})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failTesting(w, err)
		return
	}
	write(w, 200, map[string]any{"version": settings.Version, "fields": settings.Fields, "blockedEnabled": settings.BlockedEnabled, "aiReviewRules": settings.AIReviewRules, "aiLogicRules": settings.AILogicRules, "businessContext": settings.BusinessContext, "canManage": true})
}

type testingDesignInput struct {
	Name          string               `json:"name"`
	RequirementID *int64               `json:"requirementId"`
	Description   string               `json:"description"`
	OwnerUserID   string               `json:"ownerUserId"`
	Tags          json.RawMessage      `json:"tags"`
	Points        []TestingDesignPoint `json:"points"`
	tagList       []string
}

func parseTestingDesignTags(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return stringSet(values, 30)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, orgInvalid("设计标签格式无效")
	}
	// 标签字符串是可选输入。表单初始值会序列化为 ""，把空白归一为空数组，
	// 再交给 stringSet 校验真实标签，避免把“未填写”误判成一个空标签。
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	values = strings.Split(value, ",")
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	return stringSet(values, 30)
}

func (a *App) validateTestingDesignInput(ctx context.Context, q stateStore, input testingDesignInput, defaultOwner string) (testingDesignInput, error) {
	name, err := testingString(input.Name, testingNameLimit, "测试设计名称")
	if err != nil {
		return input, err
	}
	input.Name = name
	input.Description = strings.TrimSpace(input.Description)
	if utf8.RuneCountInString(input.Description) > testingTextLimit {
		return input, orgInvalid("测试设计说明过长")
	}
	if input.tagList, err = parseTestingDesignTags(input.Tags); err != nil {
		return input, err
	}
	if input.RequirementID != nil {
		if err = a.validateTestCaseRequirement(ctx, q, input.RequirementID); err != nil {
			return input, err
		}
	}
	if input.OwnerUserID == "" {
		input.OwnerUserID = defaultOwner
	}
	if input.OwnerUserID != "" {
		var active int
		err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users u JOIN tenant_memberships tm ON tm.tenant_id=u.tenant_id AND tm.user_id=u.id AND tm.status='active' LEFT JOIN project_members pm ON pm.tenant_id=u.tenant_id AND pm.project_id=? AND pm.user_id=u.id WHERE u.tenant_id=? AND u.id=? AND u.active=1 AND (tm.role='tenant_admin' OR pm.user_id IS NOT NULL)`, a.pid(), tenantID, input.OwnerUserID).Scan(&active)
		if err != nil {
			return input, err
		}
		if active != 1 {
			return input, orgInvalid("设计负责人不是当前项目有效成员")
		}
	}
	if len(input.Points) > testingMaxDesignPoints {
		return input, orgInvalid("测试设计点不能超过 200 个")
	}
	caseSeen := map[int64]bool{}
	for i := range input.Points {
		point := &input.Points[i]
		title, e := testingString(point.Title, testingNameLimit, "设计点标题")
		if e != nil {
			return input, e
		}
		point.Title = title
		point.Category = strings.TrimSpace(point.Category)
		if utf8.RuneCountInString(point.Category) > testingNameLimit {
			return input, orgInvalid("设计点分类过长")
		}
		if point.Priority == "" {
			point.Priority = "P2"
		}
		if !validChoice(point.Priority, []string{"P0", "P1", "P2", "P3"}) {
			return input, orgInvalid("设计点优先级无效")
		}
		if len(point.CaseIDs) > testingMaxCaseMove {
			return input, orgInvalid("单个设计点关联用例过多")
		}
		for _, caseID := range point.CaseIDs {
			if !testingPositiveID(caseID) || caseSeen[caseID] {
				return input, orgInvalid("设计点关联用例无效或重复")
			}
			caseSeen[caseID] = true
		}
	}
	for caseID := range caseSeen {
		if err = a.testingCaseScopeExists(ctx, q, caseID); err != nil {
			return input, err
		}
	}
	return input, nil
}

func (a *App) listTestingDesigns(ctx context.Context, q stateStore) ([]TestingDesign, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,name,requirement_id,description,owner_user_id,tags_json,created_at,updated_at FROM testing_designs WHERE tenant_id=? AND project_id=? ORDER BY updated_at DESC,id DESC LIMIT 200`, tenantID, a.pid())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TestingDesign{}
	for rows.Next() {
		var item TestingDesign
		var tags string
		if err := rows.Scan(&item.ID, &item.Name, &item.RequirementID, &item.Description, &item.OwnerUserID, &tags, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		var tagList []string
		if json.Unmarshal([]byte(tags), &tagList) != nil {
			tagList = []string{}
		}
		item.Tags = strings.Join(tagList, ",")
		points, e := a.listTestingDesignPoints(ctx, q, item.ID)
		if e != nil {
			return nil, e
		}
		item.Points = points
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) listTestingDesignPoints(ctx context.Context, q stateStore, designID int64) ([]TestingDesignPoint, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,title,category,priority FROM testing_design_points WHERE tenant_id=? AND project_id=? AND design_id=? ORDER BY sort_order,id`, tenantID, a.pid(), designID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TestingDesignPoint{}
	for rows.Next() {
		var item TestingDesignPoint
		if err := rows.Scan(&item.ID, &item.Title, &item.Category, &item.Priority); err != nil {
			return nil, err
		}
		cases, e := q.QueryContext(ctx, `SELECT case_id FROM testing_design_point_cases WHERE tenant_id=? AND project_id=? AND point_id=? ORDER BY case_id`, tenantID, a.pid(), item.ID)
		if e != nil {
			return nil, e
		}
		for cases.Next() {
			var id int64
			if e = cases.Scan(&id); e != nil {
				cases.Close()
				return nil, e
			}
			item.CaseIDs = append(item.CaseIDs, id)
		}
		e = cases.Err()
		cases.Close()
		if e != nil {
			return nil, e
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) getTestingDesign(ctx context.Context, q stateStore, id int64) (TestingDesign, error) {
	// 不能复用 workspace 列表的 LIMIT 200：历史设计超过一页时仍必须能按稳定
	// ID 查看、编辑和删除，且查询始终附带租户/项目条件。
	var item TestingDesign
	var tags string
	err := q.QueryRowContext(ctx, `SELECT id,name,requirement_id,description,owner_user_id,tags_json,created_at,updated_at FROM testing_designs WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&item.ID, &item.Name, &item.RequirementID, &item.Description, &item.OwnerUserID, &tags, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return TestingDesign{}, err
	}
	var tagList []string
	if json.Unmarshal([]byte(tags), &tagList) != nil {
		tagList = []string{}
	}
	item.Tags = strings.Join(tagList, ",")
	item.Points, err = a.listTestingDesignPoints(ctx, q, item.ID)
	if err != nil {
		return TestingDesign{}, err
	}
	return item, nil
}

func (a *App) replaceTestingDesignPoints(ctx context.Context, q stateStore, designID int64, points []TestingDesignPoint) error {
	// 调用前已经验证全部 caseId。先写入新集合到临时内存，之后一次事务替换，避免
	// 单点更新失败导致“部分设计点已删、部分仍旧”的数据丢失。
	if _, err := q.ExecContext(ctx, `DELETE FROM testing_design_point_cases WHERE tenant_id=? AND project_id=? AND point_id IN (SELECT id FROM testing_design_points WHERE tenant_id=? AND project_id=? AND design_id=?)`, tenantID, a.pid(), tenantID, a.pid(), designID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM testing_design_points WHERE tenant_id=? AND project_id=? AND design_id=?`, tenantID, a.pid(), designID); err != nil {
		return err
	}
	for index, point := range points {
		result, err := q.ExecContext(ctx, `INSERT INTO testing_design_points(tenant_id,project_id,design_id,title,category,priority,sort_order)VALUES(?,?,?,?,?,?,?)`, tenantID, a.pid(), designID, point.Title, point.Category, point.Priority, index)
		if err != nil {
			return err
		}
		pointID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		for _, caseID := range point.CaseIDs {
			if _, err = q.ExecContext(ctx, `INSERT INTO testing_design_point_cases(tenant_id,project_id,point_id,case_id)VALUES(?,?,?,?)`, tenantID, a.pid(), pointID, caseID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *App) testingDesigns(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		if r.Method == http.MethodGet {
			if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
				failTesting(w, err)
				return
			}
			items, err := a.listTestingDesigns(r.Context(), a.db)
			if err != nil {
				failTesting(w, err)
				return
			}
			write(w, 200, map[string]any{"items": items})
			return
		}
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed", "不支持的方法")
			return
		}
		var input testingDesignInput
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		tx, err := a.beginTestingWrite(r, false)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		input, err = a.validateTestingDesignInput(r.Context(), tx, input, a.uid())
		now := orgNow()
		var id int64
		if err == nil {
			result, e := tx.ExecContext(r.Context(), `INSERT INTO testing_designs(tenant_id,project_id,name,requirement_id,description,owner_user_id,tags_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), input.Name, input.RequirementID, input.Description, input.OwnerUserID, jsonText(input.tagList), now, now)
			err = e
			if err == nil {
				id, e = result.LastInsertId()
				err = e
			}
		}
		if err == nil {
			err = a.replaceTestingDesignPoints(r.Context(), tx, id, input.Points)
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_design", "testing_design.created", id, map[string]any{"name": input.Name, "requirementId": input.RequirementID})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		item, err := a.getTestingDesign(r.Context(), a.db, id)
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 201, item)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !testingPositiveID(id) {
		fail(w, 400, "invalid_id", "设计编号不正确")
		return
	}
	if len(parts) > 1 {
		a.testingDesignPointLink(w, r, id, parts[1:])
		return
	}
	if r.Method == http.MethodGet {
		if _, _, err := a.testingWorkspaceAccess(r.Context(), a.db); err != nil {
			failTesting(w, err)
			return
		}
		item, err := a.getTestingDesign(r.Context(), a.db, id)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "测试设计不存在")
			return
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, item)
		return
	}
	if r.Method == http.MethodPatch {
		var input testingDesignInput
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		tx, err := a.beginTestingWrite(r, false)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		old, err := a.getTestingDesign(r.Context(), tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "not_found", "测试设计不存在")
			return
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		input, err = a.validateTestingDesignInput(r.Context(), tx, input, old.OwnerUserID)
		now := orgNow()
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE testing_designs SET name=?,requirement_id=?,description=?,owner_user_id=?,tags_json=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=?`, input.Name, input.RequirementID, input.Description, input.OwnerUserID, jsonText(input.tagList), now, id, tenantID, a.pid())
		}
		if err == nil {
			err = a.replaceTestingDesignPoints(r.Context(), tx, id, input.Points)
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_design", "testing_design.updated", id, map[string]any{"name": input.Name, "requirementId": input.RequirementID})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		item, err := a.getTestingDesign(r.Context(), a.db, id)
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, item)
		return
	}
	if r.Method == http.MethodDelete {
		tx, err := a.beginTestingWrite(r, true)
		if err != nil {
			failTesting(w, err)
			return
		}
		defer tx.Rollback()
		if _, err = a.getTestingDesign(r.Context(), tx, id); err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM testing_design_point_cases WHERE tenant_id=? AND project_id=? AND point_id IN (SELECT id FROM testing_design_points WHERE tenant_id=? AND project_id=? AND design_id=?)`, tenantID, a.pid(), tenantID, a.pid(), id)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM testing_design_points WHERE tenant_id=? AND project_id=? AND design_id=?`, tenantID, a.pid(), id)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM testing_designs WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid())
		}
		if err == nil {
			err = a.auditRequirementState(r.Context(), tx, "testing_design", "testing_design.deleted", id, map[string]any{})
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			failTesting(w, err)
			return
		}
		write(w, 200, map[string]any{"ok": true})
		return
	}
	fail(w, 405, "method_not_allowed", "不支持的方法")
}

// testingDesignPointLink 方便设计画布把已有用例逐点挂接；它复用完整作用域
// 校验，不允许浏览器借一个设计点 ID 关联别的项目的测试用例。
func (a *App) testingDesignPointLink(w http.ResponseWriter, r *http.Request, designID int64, parts []string) {
	if len(parts) != 3 || parts[0] != "points" || parts[2] != "case" || r.Method != http.MethodPost {
		fail(w, 404, "not_found", "资源不存在")
		return
	}
	pointID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !testingPositiveID(pointID) {
		fail(w, 400, "invalid_id", "设计点编号不正确")
		return
	}
	var input struct {
		CaseID int64 `json:"caseId"`
	}
	if decodeJSON(r, &input) != nil || !testingPositiveID(input.CaseID) {
		fail(w, 422, "validation_error", "测试用例编号无效")
		return
	}
	tx, err := a.beginTestingWrite(r, false)
	if err != nil {
		failTesting(w, err)
		return
	}
	defer tx.Rollback()
	var found int
	err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM testing_design_points WHERE id=? AND design_id=? AND tenant_id=? AND project_id=?`, pointID, designID, tenantID, a.pid()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		err = orgInvalid("设计点不属于当前项目")
	}
	if err == nil {
		err = a.testingCaseScopeExists(r.Context(), tx, input.CaseID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO testing_design_point_cases(tenant_id,project_id,point_id,case_id)VALUES(?,?,?,?)`, tenantID, a.pid(), pointID, input.CaseID)
	}
	if err == nil {
		err = a.auditRequirementState(r.Context(), tx, "testing_design", "testing_design.case_linked", designID, map[string]any{"pointId": pointID, "caseId": input.CaseID})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failTesting(w, err)
		return
	}
	write(w, 200, map[string]any{"ok": true, "designId": designID, "pointId": pointID, "caseId": input.CaseID})
}
