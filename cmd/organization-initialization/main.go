// organization-initialization 从已审核的当前库生成独立的新环境组织初始化库。
// 仅复制结构白名单；不复制业务数据、旧口令、会话、令牌或外发配置，不修改源库。
package main

import (
	"bufio"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type copySpec struct{ table, columns, where string }

// 列也使用显式白名单，未来 users 增加敏感字段时不会被自动带入交付。
var organizationTables = []copySpec{
	{"tenants", "id,name", "id=?"},
	{"projects", "id,tenant_id,name,code,description,status,owner_user_id,icon,color,created_at,updated_at", "tenant_id=? AND status='active'"},
	{"departments", "id,tenant_id,parent_id,name,code,source,external_id,status,sort_order,created_at,updated_at", "tenant_id=? AND status='active'"},
	{"users", "id,tenant_id,name,email,department,employee_no,job_title,avatar_color,locale,timezone", "tenant_id=? AND id IN (SELECT user_id FROM tenant_memberships WHERE status<>'removed' AND tenant_id=users.tenant_id)"},
	{"tenant_memberships", "tenant_id,user_id,role,status,created_at,updated_at", "tenant_id=? AND status<>'removed'"},
	{"project_members", "tenant_id,project_id,user_id,role,created_at,updated_at", "tenant_id=? AND project_id IN (SELECT id FROM projects WHERE status='active') AND user_id IN (SELECT user_id FROM tenant_memberships WHERE status<>'removed' AND tenant_id=project_members.tenant_id)"},
	{"memberships", "tenant_id,project_id,user_id,role", "tenant_id=? AND project_id IN (SELECT id FROM projects WHERE status='active') AND user_id IN (SELECT user_id FROM tenant_memberships WHERE status<>'removed' AND tenant_id=memberships.tenant_id)"},
	{"department_memberships", "tenant_id,department_id,user_id,is_primary,title,source,external_id,status,joined_at,updated_at", "tenant_id=? AND status='active' AND department_id IN (SELECT id FROM departments WHERE status='active') AND user_id IN (SELECT user_id FROM tenant_memberships WHERE status<>'removed' AND tenant_id=department_memberships.tenant_id)"},
	{"organization_groups", "id,tenant_id,name,description,created_at,updated_at", "tenant_id=?"},
	{"organization_group_permissions", "tenant_id,group_id,permission", "tenant_id=?"},
	{"organization_group_members", "tenant_id,group_id,user_id", "tenant_id=? AND user_id IN (SELECT user_id FROM tenant_memberships WHERE status<>'removed' AND tenant_id=organization_group_members.tenant_id)"},
}

func main() {
	source := flag.String("source-db", "", "当前已审核数据库的绝对路径，只读")
	output := flag.String("output-db", "", "全新初始化库绝对路径，禁止覆盖")
	tenant := flag.String("tenant", "tn_acme", "当前企业 ID")
	admin := flag.String("admin-email", "", "已有人员名单中的初始管理员邮箱")
	flag.Parse()
	// 从标准输入读取，不把口令写到命令参数、源码、文档或日志中。
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(password) == 0 {
		fmt.Fprintln(os.Stderr, "请从标准输入提供初始密码")
		os.Exit(1)
	}
	if err = initialize(*source, *output, *tenant, *admin, strings.TrimRight(password, "\r\n")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("新环境组织初始化库已生成；仅初始管理员可登录，首次登录必须改密。源数据库未修改。")
}

func initialize(source, output, tenant, admin, password string) (result error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(output) || filepath.Clean(source) == filepath.Clean(output) {
		return errors.New("源库与输出库必须是不同的绝对路径")
	}
	if len(password) < 8 || len(password) > 72 || admin == "" || tenant == "" {
		return errors.New("请提供管理员邮箱、企业及 8–72 字节初始密码")
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("源库必须为非空普通文件")
	}
	sourceURL := url.URL{Scheme: "file", Path: source, RawQuery: "mode=ro"}
	src, err := sql.Open("sqlite", sourceURL.String())
	if err != nil {
		return err
	}
	defer src.Close()
	snapshot, err := src.Begin()
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	var adminID string
	err = snapshot.QueryRow("SELECT u.id FROM users u JOIN tenant_memberships m ON m.tenant_id=u.tenant_id AND m.user_id=u.id WHERE u.tenant_id=? AND lower(u.email)=lower(?) AND m.status<>'removed'", tenant, admin).Scan(&adminID)
	if err != nil {
		return errors.New("指定管理员必须存在于当前企业未删除的名单中")
	}
	// 独占创建同时拒绝已有文件和符号链接，失败时仅清理本次新建文件。
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	file.Close()
	defer func() {
		if result != nil {
			_ = os.Remove(output)
		}
	}()
	dst, err := sql.Open("sqlite", output)
	if err != nil {
		return err
	}
	defer dst.Close()
	dst.SetMaxOpenConns(1)
	tx, err := dst.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	schema, err := snapshot.Query("SELECT type,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'index' THEN 1 ELSE 2 END")
	if err != nil {
		return err
	}
	var later []string
	for schema.Next() {
		var kind, statement string
		if err = schema.Scan(&kind, &statement); err != nil {
			schema.Close()
			return err
		}
		if kind == "table" {
			if _, err = tx.Exec(statement); err != nil {
				schema.Close()
				return err
			}
		} else {
			later = append(later, statement)
		}
	}
	err = schema.Err()
	schema.Close()
	if err != nil {
		return err
	}
	for _, spec := range organizationTables {
		if err = copyRows(snapshot, tx, spec, tenant); err != nil {
			return fmt.Errorf("复制组织表 %s: %w", spec.table, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost+2)
	if err != nil {
		return err
	}
	// 所有旧凭据被排除；其他成员只能由管理员激活并初始化，不能沿用测试站密码。
	if _, err = tx.Exec("UPDATE users SET active=0,password_hash='',operation_disabled=0,must_change_password=0"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tenant_memberships SET status='disabled',updated_at=?", now); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE users SET active=1,password_hash=?,password_changed_at=?,must_change_password=1 WHERE id=?", string(hash), now, adminID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE tenant_memberships SET role='tenant_admin',status='active' WHERE tenant_id=? AND user_id=?", tenant, adminID); err != nil {
		return err
	}
	// 不带测试用例/需求等业务配置数据；应用首次启动会完成非演示的缺省配置迁移。
	for _, statement := range later {
		if _, err = tx.Exec(statement); err != nil {
			return err
		}
	}
	var check string
	if err = tx.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return errors.New("初始化库完整性检查失败")
	}
	fk, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := fk.Next()
	fkErr := fk.Err()
	fk.Close()
	if fkErr != nil {
		return fkErr
	}
	if invalid {
		return errors.New("初始化库包含无效外键")
	}
	return tx.Commit()
}

func copyRows(src *sql.Tx, dst *sql.Tx, spec copySpec, tenant string) error {
	rows, err := src.Query("SELECT "+spec.columns+" FROM "+spec.table+" WHERE "+spec.where, tenant)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns := strings.Split(spec.columns, ",")
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return err
		}
		if _, err = dst.Exec("INSERT INTO "+spec.table+"("+spec.columns+") VALUES("+placeholders+")", values...); err != nil {
			return err
		}
	}
	return rows.Err()
}
