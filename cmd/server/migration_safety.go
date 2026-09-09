package main

import (
	"database/sql"
	"fmt"
	"strings"
)

// migrationQueryer 由 *sql.DB 和 *sql.Tx 共同实现。迁移既要支持启动期的
// 独立结构检查，也要支持在同一事务内读取刚写入的演示数据，避免连接池读到旧快照。
type migrationQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

type migrationExecutor interface {
	migrationQueryer
	Exec(query string, args ...any) (sql.Result, error)
}

// migrationColumn 只保存源码中的固定 SQL，禁止把外部输入拼进 ALTER TABLE。
// 已有列不是错误；其余 DDL 错误必须原样返回，不能把损坏的旧库当成已升级。
type migrationColumn struct {
	table     string
	name      string
	statement string
}

// 迁移用真实表结构识别首次初始化；查询失败必须中止，不能当作空库重新灌入演示数据。
func (a *App) databaseHasTable(name string) (bool, error) {
	var count int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count)
	return count > 0, err
}

// 演示初始数据横跨多个历史迁移。新库第一次启动先写入 pending 标记，只有全部
// 迁移成功才改为 complete；进程在中途退出时，下一次可安全重试而不会因 tenants
// 已存在就把半套演示数据误认为完成。旧业务库没有该标记时始终不补灌演示数据。
func (a *App) demoSeedPending() (bool, error) {
	exists, err := a.databaseHasTable("migration_bootstrap")
	if err != nil || !exists {
		return false, err
	}
	var state string
	err = a.db.QueryRow(`SELECT state FROM migration_bootstrap WHERE key='demo_seed'`).Scan(&state)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == "pending", nil
}

func (a *App) markDemoSeedPending(now string) error {
	_, err := a.db.Exec(`INSERT INTO migration_bootstrap(key,state,updated_at)VALUES('demo_seed','pending',?) ON CONFLICT(key) DO UPDATE SET state='pending',updated_at=excluded.updated_at`, now)
	return err
}

func (a *App) completeDemoSeed(now string) error {
	result, err := a.db.Exec(`UPDATE migration_bootstrap SET state='complete',updated_at=? WHERE key='demo_seed' AND state='pending'`, now)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("演示初始化状态不存在或已损坏")
	}
	return nil
}

// 只检查已知迁移所需列，不修改业务记录；调用方负责决定是否执行对应的一次性迁移。
func (a *App) databaseHasColumns(table string, columns ...string) (bool, error) {
	return databaseHasColumnsUsing(a.db, table, columns...)
}

func databaseHasColumnsUsing(query migrationQueryer, table string, columns ...string) (bool, error) {
	if len(columns) == 0 {
		return true, nil
	}
	args := []any{table}
	placeholders := make([]string, len(columns))
	for index, column := range columns {
		placeholders[index] = "?"
		args = append(args, column)
	}
	var count int
	err := query.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name IN (`+strings.Join(placeholders, ",")+`)`, args...).Scan(&count)
	return count == len(columns), err
}

// addMigrationColumn 是可重试的列迁移原语。先检查再执行并在执行后复核，因而
// 某次启动在多条 ALTER 之间中断后，下次会保留已经成功的列、继续缺失的列；
// 但 SQL 语法、磁盘、锁或异常旧结构导致的真实错误不会被“重复列”掩盖。
func addMigrationColumn(exec migrationExecutor, column migrationColumn) (bool, error) {
	exists, err := databaseHasColumnsUsing(exec, column.table, column.name)
	if err != nil {
		return false, fmt.Errorf("检查迁移列 %s.%s: %w", column.table, column.name, err)
	}
	if exists {
		return false, nil
	}
	if _, err := exec.Exec(column.statement); err != nil {
		return false, fmt.Errorf("添加迁移列 %s.%s: %w", column.table, column.name, err)
	}
	exists, err = databaseHasColumnsUsing(exec, column.table, column.name)
	if err != nil {
		return false, fmt.Errorf("复核迁移列 %s.%s: %w", column.table, column.name, err)
	}
	if !exists {
		return false, fmt.Errorf("添加迁移列 %s.%s 后未找到该列", column.table, column.name)
	}
	return true, nil
}

// migrationProjectPerson 在演示数据事务中解析稳定成员 ID。显示名可能被人工修改，
// 所以同名、停用或已移出项目的成员都视为初始化失败并整体回滚。
func (a *App) migrationProjectPerson(query migrationQueryer, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	rows, err := query.Query(`SELECT DISTINCT u.id FROM users u JOIN project_members pm ON pm.user_id=u.id AND pm.tenant_id=u.tenant_id JOIN tenant_memberships tm ON tm.user_id=u.id AND tm.tenant_id=u.tenant_id WHERE pm.tenant_id=? AND pm.project_id=? AND u.name=? AND u.active=1 AND tm.status='active'`, tenantID, a.pid(), name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("负责人不是当前项目唯一有效成员，请重新选择")
	}
	return ids[0], nil
}

func migrationUserIDByName(query migrationQueryer, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var id string
	if err := query.QueryRow(`SELECT id FROM users WHERE tenant_id=? AND name=? LIMIT 1`, tenantID, name).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}
