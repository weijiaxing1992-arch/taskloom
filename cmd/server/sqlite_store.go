package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sqliteBusyTimeoutMS  = 5000
	sqliteMaxOpenConns   = 32
	sqliteMaxAPIRequests = 12
)

// modernc sqlite applies every _pragma parameter in Driver.Open to each new
// connection (not just the first pooled connection). It explicitly orders
// busy_timeout before other pragmas. See the installed sqlite.go applyQueryParams.
// 运维约束：PRAGMA 必须通过 DSN 配给每一个连接，不能只在连接池首连接执行。
// 生产启动模板要求 FULL；NORMAL 仅为本地兼容默认。WAL 文件不得在服务运行时手工删除。
func openSQLiteDatabase(path string) (*sql.DB, error) {
	synchronous := strings.ToUpper(env("DEVFLOW_SQLITE_SYNCHRONOUS", "NORMAL"))
	if synchronous != "NORMAL" && synchronous != "FULL" {
		return nil, fmt.Errorf("DEVFLOW_SQLITE_SYNCHRONOUS must be FULL or NORMAL")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMS))
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "synchronous("+synchronous+")")
	// Acquire the writer reservation at BEGIN, before reading then writing in
	// one transaction; avoid failing a deferred read-to-write lock upgrade.
	params.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: params.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Existing list handlers hold a result set while reading its associations.
	// Keep several connections, and admit fewer API requests than half the pool
	// so simultaneous outer queries cannot exhaust it before nested reads.
	// 列表有“持有外层 rows 再查关联”的路径；池缩到 1 会死锁，限流数也要给关联读取留余量。
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(16)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
