package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteProductionFullDurabilityOnEveryConnection(t *testing.T) {
	t.Setenv("DEVFLOW_SQLITE_SYNCHRONOUS", "FULL")
	db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "production.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	connections := []*sql.Conn{}
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		var value int
		if err := conn.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&value); err != nil || value != 2 {
			t.Fatalf("connection %d synchronous=%d err=%v", i, value, err)
		}
	}
}
func TestSQLiteRejectsUnsafeDurabilityConfiguration(t *testing.T) {
	for _, mode := range []string{"OFF", "0", "FULL);DROP TABLE users;"} {
		t.Setenv("DEVFLOW_SQLITE_SYNCHRONOUS", mode)
		if db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "rejected.db")); err == nil {
			db.Close()
			t.Fatal("unsafe configuration accepted")
		}
	}
}
func TestSQLiteContainsWALResetFix(t *testing.T) {
	a := testApp(t)
	var version string
	if err := a.db.QueryRow(`SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "3.51.3" {
		t.Fatalf("review new SQLite release and update pinned version assertion: got %s", version)
	}
}
