package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// 发布前可显式指定一致性备份。只读打开输入并复制进测试临时目录，
// 不连接原业务库、不启动通知工作器、不读取密钥文件。
func TestIntegrationUpgradeOfflineSnapshot(t *testing.T) {
	source := os.Getenv("DEVFLOW_UPGRADE_SNAPSHOT")
	if source == "" {
		t.Skip("set DEVFLOW_UPGRADE_SNAPSHOT to a consistent offline snapshot")
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	path := filepath.Join(t.TempDir(), "upgrade-copy.db")
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLiteDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &App{db: db, passwordCost: 4, signingKey: []byte("isolated-upgrade-test-signing-key-32-bytes")}
	counts := map[string]int{}
	for _, table := range []string{"requirements", "sprints", "defects", "test_cases", "test_executions", "users", "comments", "user_notifications"} {
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	for i := 0; i < 2; i++ {
		if err = a.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	for table, before := range counts {
		var after int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("migration changed %s count %d -> %d", table, before, after)
		}
	}
	var check string
	if err = db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity: %s %v", check, err)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after migration")
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("offline migration twice succeeded; unchanged business row counts: %v", counts))
}
