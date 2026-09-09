package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestSQLiteMigratedFixtureIntegrityAndForeignKeyEnforcement(t *testing.T) {
	a := fileSQLiteTestApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var integrity string
	if err := a.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("临时迁移库完整性检查失败: %q %v", integrity, err)
	}
	var violations int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("临时迁移库外键错误: %d %v", violations, err)
	}
	bulkFixtureExec(t, a, `CREATE TABLE boundary_test_parent(id INTEGER PRIMARY KEY)`)
	bulkFixtureExec(t, a, `CREATE TABLE boundary_test_child(id INTEGER PRIMARY KEY,parent_id INTEGER NOT NULL REFERENCES boundary_test_parent(id))`)
	connections := []*sql.Conn{}
	defer func() {
		for _, connection := range connections {
			connection.Close()
		}
	}()
	for index := 0; index < 8; index++ {
		connection, err := a.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
		if _, err := connection.ExecContext(ctx, `INSERT INTO boundary_test_child(id,parent_id)VALUES(?,999)`, index); err == nil {
			t.Fatalf("连接 %d 没有实际拒绝悬空外键", index)
		}
		var total int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM boundary_test_child`).Scan(&total); err != nil || total != 0 {
			t.Fatalf("外键失败留下部分数据: %d %v", total, err)
		}
	}
}
