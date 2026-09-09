package main

import (
	"context"
	"database/sql"
	"time"
)

const requirementCategoryPresetVersion = "yunbat-categories-v1"

// 未分类是系统兜底项，固定置顶；其余顺序即新项目的默认菜单顺序。
var defaultRequirementCategoryNames = []string{"未分类", "客户端", "管理端", "全球化", "定制开发", "待定"}

// Exact legacy seed names only: never remove an arbitrary user-defined child path.
var legacyRequirementCategoryNames = []string{"基础能力", "基础能力/账号体系", "研发协作", "研发协作/需求", "协作开发", "协作开发/需求", "集成", "集成/企业微信", "安全合规"}

func seedDefaultRequirementCategories(ctx context.Context, tx *sql.Tx, tenant, project, now string) error {
	for index, name := range defaultRequirementCategoryNames {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO requirement_categories(tenant_id,project_id,name,sort_order,created_at,updated_at)VALUES(?,?,?,?,?,?)`, tenant, project, name, index*requirementCategoryOrderStep, now, now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO requirement_category_migrations(tenant_id,project_id,version,applied_at)VALUES(?,?,?,?)`, tenant, project, requirementCategoryPresetVersion, now)
	return err
}

func (a *App) migrateRequirementCategoryPresets() error {
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS requirement_category_migrations(tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,version TEXT NOT NULL,applied_at TEXT NOT NULL,PRIMARY KEY(tenant_id,project_id,version));`); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT tenant_id,id FROM projects WHERE NOT EXISTS(SELECT 1 FROM requirement_category_migrations m WHERE m.tenant_id=projects.tenant_id AND m.project_id=projects.id AND m.version=?)`, requirementCategoryPresetVersion)
	if err != nil {
		return err
	}
	type projectScope struct{ tenant, project string }
	projects := []projectScope{}
	for rows.Next() {
		var p projectScope
		if err = rows.Scan(&p.tenant, &p.project); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range projects {
		if err = seedDefaultRequirementCategories(context.Background(), tx, p.tenant, p.project, now); err != nil {
			return err
		}
		changes := []map[string]any{}
		for _, name := range legacyRequirementCategoryNames {
			var categoryID int64
			err = tx.QueryRow(`SELECT id FROM requirement_categories WHERE tenant_id=? AND project_id=? AND name=?`, p.tenant, p.project, name).Scan(&categoryID)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			result, updateErr := tx.Exec(`UPDATE requirements SET category='待定',updated_at=? WHERE tenant_id=? AND project_id=? AND category=?`, now, p.tenant, p.project, name)
			if updateErr != nil {
				return updateErr
			}
			moved, updateErr := result.RowsAffected()
			if updateErr != nil {
				return updateErr
			}
			if categoryID > 0 {
				if _, err = tx.Exec(`DELETE FROM requirement_categories WHERE tenant_id=? AND project_id=? AND id=? AND name=?`, p.tenant, p.project, categoryID, name); err != nil {
					return err
				}
			}
			if categoryID > 0 || moved > 0 {
				changes = append(changes, map[string]any{"id": categoryID, "name": name, "movedRequirements": moved})
			}
		}
		if len(changes) > 0 {
			if _, err = tx.Exec(`INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at)VALUES(?,?,'system','requirement_category',?,'category_presets_migration',?,?,?)`, p.tenant, p.project, requirementCategoryPresetVersion, jsonText(changes), jsonText(map[string]any{"targetCategory": "待定", "defaults": defaultRequirementCategoryNames}), now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
