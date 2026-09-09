package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type RequirementCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Count     int64  `json:"count"`
	SortOrder int    `json:"sortOrder"`
}

const (
	requirementCategoryOrderVersion = "requirement-category-sort-order-v1"
	requirementCategoryOrderStep    = 100
)

func (a *App) migrateRequirementCollaboration() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_categories(id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,name TEXT NOT NULL,sort_order INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(tenant_id,project_id,name));
CREATE INDEX IF NOT EXISTS idx_requirement_categories_scope ON requirement_categories(tenant_id,project_id,name);`); err != nil {
		return err
	}
	hasSortOrder, err := a.databaseHasColumns("requirement_categories", "sort_order")
	if err != nil {
		return err
	}
	if !hasSortOrder {
		if _, err := a.db.Exec(`ALTER TABLE requirement_categories ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if _, err := a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_requirement_categories_scope_order ON requirement_categories(tenant_id,project_id,sort_order,id)`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO requirement_categories(tenant_id,project_id,name,created_at,updated_at) SELECT tenant_id,id,'未分类',?,? FROM projects`, now, now); err != nil {
		return err
	}
	// Register historic names verbatim so legacy category strings keep working.
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO requirement_categories(tenant_id,project_id,name,created_at,updated_at) SELECT DISTINCT tenant_id,project_id,category,?,? FROM requirements WHERE trim(category)!=''`, now, now); err != nil {
		return err
	}
	var exists int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('comments') WHERE name='mention_user_ids_json'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if _, err := a.db.Exec(`ALTER TABLE comments ADD COLUMN mention_user_ids_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return err
		}
	}
	// 我的工作逐需求核对评论提及，按对象定位，避免扫描整个项目的评论。
	_, err = a.db.Exec(`CREATE INDEX IF NOT EXISTS idx_comments_requirement_scope ON comments(tenant_id,project_id,requirement_id)`)
	return err
}

func (a *App) listRequirementCategories() ([]RequirementCategory, error) {
	return listRequirementCategoriesUsing(context.Background(), a.db, tenantID, a.pid())
}

// requirementCategoryReader 让排序写入在同一事务内读取；否则读到的列表
// 可能来自写入前的连接，导致返回顺序和实际持久化结果不一致。
type requirementCategoryReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listRequirementCategoriesUsing(ctx context.Context, query requirementCategoryReader, tenant, project string) ([]RequirementCategory, error) {
	rows, err := query.QueryContext(ctx, `SELECT c.id,c.name,COUNT(r.id),c.sort_order FROM requirement_categories c LEFT JOIN requirements r ON r.tenant_id=c.tenant_id AND r.project_id=c.project_id AND r.category=c.name WHERE c.tenant_id=? AND c.project_id=? GROUP BY c.id,c.name,c.sort_order ORDER BY c.sort_order,c.id`, tenant, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RequirementCategory{}
	for rows.Next() {
		var item RequirementCategory
		if err := rows.Scan(&item.ID, &item.Name, &item.Count, &item.SortOrder); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// categoryOrderFingerprint 是客户端乐观并发控制用的轻量版本号。它仅表达
// 当前项目分类的 ID 与顺序，分类名称/数量变化不会造成无意义的排序冲突。
func categoryOrderFingerprint(items []RequirementCategory) string {
	var input strings.Builder
	for _, item := range items {
		fmt.Fprintf(&input, "%d:%d;", item.ID, item.SortOrder)
	}
	sum := sha256.Sum256([]byte(input.String()))
	return fmt.Sprintf("%x", sum[:])
}

func categoryListPayload(items []RequirementCategory, canManage bool) map[string]any {
	return map[string]any{
		"items":        items,
		"canManage":    canManage,
		"orderVersion": categoryOrderFingerprint(items),
	}
}

// migrateRequirementCategoryOrdering 为已有项目一次性赋予稳定顺序。迁移标记
// 能避免每次服务重启把管理员后来调整过的顺序覆盖回默认顺序。
func (a *App) migrateRequirementCategoryOrdering() error {
	rows, err := a.db.Query(`SELECT tenant_id,id FROM projects WHERE NOT EXISTS(SELECT 1 FROM requirement_category_migrations m WHERE m.tenant_id=projects.tenant_id AND m.project_id=projects.id AND m.version=?)`, requirementCategoryOrderVersion)
	if err != nil {
		return err
	}
	type categoryScope struct{ tenant, project string }
	scopes := []categoryScope{}
	for rows.Next() {
		var scope categoryScope
		if err = rows.Scan(&scope.tenant, &scope.project); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	for _, scope := range scopes {
		tx, err := a.db.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		items, err := listRequirementCategoriesUsing(context.Background(), tx, scope.tenant, scope.project)
		if err == nil {
			// sort_order 刚增加时全部为零；采用发布前已经稳定使用的默认顺序
			// 初始化一次，既保留用户分类的创建顺序，也让“未分类”固定在首位。
			for index, item := range items {
				_, err = tx.Exec(`UPDATE requirement_categories SET sort_order=? WHERE tenant_id=? AND project_id=? AND id=?`, index*requirementCategoryOrderStep, scope.tenant, scope.project, item.ID)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO requirement_category_migrations(tenant_id,project_id,version,applied_at)VALUES(?,?,?,?)`, scope.tenant, scope.project, requirementCategoryOrderVersion, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// markRequirementCategoryOrderingInitialized 标记运行时新建的项目已经带有
// 显式顺序，避免下一次服务启动误把用户第一次手动排序覆盖成默认顺序。
func markRequirementCategoryOrderingInitialized(ctx context.Context, tx *sql.Tx, tenant, project, now string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO requirement_category_migrations(tenant_id,project_id,version,applied_at)VALUES(?,?,?,?)`, tenant, project, requirementCategoryOrderVersion, now)
	return err
}

func (a *App) canManageRequirementCategories(r *http.Request) bool {
	_, _, role, active := a.currentUser(r)
	return active && validChoice(role, []string{"tenant_admin", "project_admin", "product", "frontend_lead", "backend_lead"})
}

func (a *App) validateRequirementCategory(name string) error {
	return a.validateRequirementCategoryUsing(a.db, name)
}

func (a *App) validateRequirementCategoryUsing(query interface{ QueryRow(string, ...any) *sql.Row }, name string) error {
	var exists int
	if err := query.QueryRow(`SELECT COUNT(*) FROM requirement_categories WHERE tenant_id=? AND project_id=? AND name=?`, tenantID, a.pid(), name).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("需求分类不属于当前项目或已被删除，请重新选择")
	}
	return nil
}

func normalizedCategoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return "", fmt.Errorf("分类名称须为 1–120 字")
	}
	if name == "未分类" || name == "全部" || name == "所有的" {
		return "", fmt.Errorf("该分类名称为系统保留名称")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", fmt.Errorf("分类名称不能包含换行或控制字符")
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("分类路径不能包含空层级")
		}
	}
	return name, nil
}

func (a *App) requirementCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		items, err := a.listRequirementCategories()
		if err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
		write(w, 200, categoryListPayload(items, a.canManageRequirementCategories(r)))
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.canManageRequirementCategories(r) {
		fail(w, 403, "category_manager_required", "仅管理员、产品和研发组长可管理需求分类")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	name, err := normalizedCategoryName(body.Name)
	if err != nil {
		fail(w, 422, "validation_error", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// 单条 INSERT 内计算尾部位置，避免“先查最大值、再插入”在并发新建时
	// 产生相同位置；即使外部导入留下空档，列表仍以 sort_order、id 稳定排序。
	res, err := a.db.Exec(`INSERT INTO requirement_categories(tenant_id,project_id,name,sort_order,created_at,updated_at) SELECT ?,?,?,COALESCE(MAX(sort_order),0)+?,?,? FROM requirement_categories WHERE tenant_id=? AND project_id=?`, tenantID, a.pid(), name, requirementCategoryOrderStep, now, now, tenantID, a.pid())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			fail(w, 409, "category_exists", "当前项目已存在同名分类")
		} else {
			fail(w, 500, "db_error", err.Error())
		}
		return
	}
	id, _ := res.LastInsertId()
	var sortOrder int
	if err = a.db.QueryRow(`SELECT sort_order FROM requirement_categories WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&sortOrder); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 201, RequirementCategory{ID: id, Name: name, SortOrder: sortOrder})
}

// requirementCategoryOrder 接收完整分类 ID 序列，事务内校验范围和版本后统一写入。
// 完整序列比“移动到第 N 位”更容易避免跨项目 ID、重复项及陈旧列表造成的数据漂移。
func (a *App) requirementCategoryOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.canManageRequirementCategories(r) {
		fail(w, 403, "category_manager_required", "仅管理员、产品和研发组长可管理需求分类")
		return
	}
	var body struct {
		OrderedIDs   []int64 `json:"orderedIds"`
		OrderVersion string  `json:"orderVersion"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		fail(w, 400, "invalid_json", "请求格式不正确")
		return
	}
	if len(body.OrderedIDs) == 0 {
		fail(w, 422, "invalid_category_order", "排序请求必须包含完整且不重复的分类编号")
		return
	}
	seen := make(map[int64]struct{}, len(body.OrderedIDs))
	for _, id := range body.OrderedIDs {
		if id <= 0 {
			fail(w, 422, "invalid_category_order", "排序请求必须包含完整且不重复的分类编号")
			return
		}
		if _, duplicate := seen[id]; duplicate {
			fail(w, 422, "invalid_category_order", "排序请求必须包含完整且不重复的分类编号")
			return
		}
		seen[id] = struct{}{}
	}

	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	items, err := listRequirementCategoriesUsing(r.Context(), tx, tenantID, a.pid())
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if len(items) != len(body.OrderedIDs) || body.OrderVersion != "" && body.OrderVersion != categoryOrderFingerprint(items) {
		fail(w, 409, "category_order_changed", "分类已发生变化，请刷新后重新排序")
		return
	}
	byID := make(map[int64]RequirementCategory, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range body.OrderedIDs {
		if _, exists := byID[id]; !exists {
			fail(w, 409, "category_order_changed", "分类已发生变化，请刷新后重新排序")
			return
		}
	}
	if first := byID[body.OrderedIDs[0]]; first.Name != "未分类" {
		fail(w, 422, "reserved_category_order", "系统分类“未分类”必须固定在首位")
		return
	}

	sameOrder := true
	for index, item := range items {
		if item.ID != body.OrderedIDs[index] {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		if err = tx.Commit(); err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
		write(w, 200, categoryListPayload(items, true))
		return
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index, id := range body.OrderedIDs {
		if _, err = tx.ExecContext(r.Context(), `UPDATE requirement_categories SET sort_order=?,updated_at=? WHERE tenant_id=? AND project_id=? AND id=?`, index*requirementCategoryOrderStep, now, tenantID, a.pid(), id); err != nil {
			fail(w, 500, "db_error", err.Error())
			return
		}
	}
	reordered, err := listRequirementCategoriesUsing(r.Context(), tx, tenantID, a.pid())
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'requirement_category',?,'reordered',?,?,?)`, tenantID, a.pid(), a.uid(), a.pid(), jsonText(items), jsonText(reordered), now)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	write(w, 200, categoryListPayload(reordered, true))
}

func (a *App) requirementCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/requirement-categories/"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 400, "invalid_id", "分类编号无效")
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	if !a.canManageRequirementCategories(r) {
		fail(w, 403, "category_manager_required", "仅管理员、产品和研发组长可管理需求分类")
		return
	}
	var currentName string
	var currentSortOrder int
	if err := a.db.QueryRow(`SELECT name,sort_order FROM requirement_categories WHERE id=? AND tenant_id=? AND project_id=?`, id, tenantID, a.pid()).Scan(&currentName, &currentSortOrder); err != nil {
		fail(w, 404, "not_found", "分类不存在")
		return
	}
	if currentName == "未分类" {
		fail(w, 409, "reserved_category", "系统分类“未分类”不能重命名或删除")
		return
	}
	target := "未分类"
	if r.Method == http.MethodPatch {
		var body struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			fail(w, 400, "invalid_json", "请求格式不正确")
			return
		}
		target, err = normalizedCategoryName(body.Name)
		if err != nil {
			fail(w, 422, "validation_error", err.Error())
			return
		}
	}
	tx, err := a.db.Begin()
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	var res sql.Result
	if r.Method == http.MethodPatch {
		res, err = tx.Exec(`UPDATE requirement_categories SET name=?,updated_at=? WHERE id=? AND tenant_id=? AND project_id=? AND name=?`, target, now, id, tenantID, a.pid(), currentName)
	} else {
		res, err = tx.Exec(`DELETE FROM requirement_categories WHERE id=? AND tenant_id=? AND project_id=? AND name=?`, id, tenantID, a.pid(), currentName)
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			fail(w, 409, "category_exists", "当前项目已存在同名分类")
		} else {
			fail(w, 500, "db_error", err.Error())
		}
		return
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		fail(w, 409, "category_changed", "分类已被其他人修改，请刷新后重试")
		return
	}
	res, err = tx.Exec(`UPDATE requirements SET category=?,updated_at=? WHERE tenant_id=? AND project_id=? AND category=?`, target, now, tenantID, a.pid(), currentName)
	if err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	moved, _ := res.RowsAffected()
	if _, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,?,'requirement_category',?,?,?,?,?)`, tenantID, a.pid(), a.uid(), fmt.Sprint(id), strings.ToLower(r.Method), jsonText(map[string]any{"name": currentName}), jsonText(map[string]any{"name": target, "movedRequirements": moved}), now); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "db_error", err.Error())
		return
	}
	if r.Method == http.MethodDelete {
		write(w, 200, map[string]any{"movedRequirements": moved, "targetCategory": target})
	} else {
		write(w, 200, RequirementCategory{ID: id, Name: target, Count: moved, SortOrder: currentSortOrder})
	}
}
