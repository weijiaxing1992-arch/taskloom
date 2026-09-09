package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fileSQLiteTestApp(t *testing.T) *App {
	t.Helper()
	db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "并发 ?# database.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, passwordCost: 4, signingKey: []byte("file-test-session-signing-key-at-least-32-bytes"), apiSlots: make(chan struct{}, sqliteMaxAPIRequests)}
	t.Cleanup(func() { db.Close() })
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	return a
}

func fileSQLiteLogin(t *testing.T, a *App) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(jsonText(map[string]string{"email": "linxia@devflow.local", "password": seedPassword})))
	r.Header.Set("Content-Type", "application/json")
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("real login handler failed: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatal("login did not issue a session cookie")
	return nil
}

func fileSQLiteRead(ctx context.Context, handler http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	r.Header.Set("X-DevFlow-Project", projectID)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestSQLiteSettingsApplyToEveryPooledConnection(t *testing.T) {
	db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "逐连接配置.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connections := []*sql.Conn{}
	defer func() {
		for _, conn := range connections {
			conn.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn) // Hold all to force distinct opens.
		for _, setting := range []struct {
			name string
			want int
		}{{"busy_timeout", sqliteBusyTimeoutMS}, {"foreign_keys", 1}, {"synchronous", 1}} {
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+setting.name).Scan(&got); err != nil || got != setting.want {
				t.Fatalf("connection %d %s=%d, want %d, err=%v", i, setting.name, got, setting.want, err)
			}
		}
		var mode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
			t.Fatalf("connection %d journal mode=%s err=%v", i, mode, err)
		}
	}
	if stats := db.Stats(); stats.MaxOpenConnections != sqliteMaxOpenConns || stats.OpenConnections < len(connections) || stats.MaxOpenConnections <= 1 {
		t.Fatalf("invalid bounded pool: %+v", stats)
	}
}

func TestSQLiteFreshSessionReadsDoNotContendForWriterLock(t *testing.T) {
	a := fileSQLiteTestApp(t)
	cookie := fileSQLiteLogin(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	writer, err := a.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer writer.ExecContext(context.Background(), "ROLLBACK")
	if _, err := writer.ExecContext(ctx, `UPDATE users SET bio='writer holding lock' WHERE id='u_admin'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- fileSQLiteRead(ctx, a.scopedAPI(), cookie, "/api/session") }()
	select {
	case response := <-done:
		if response.Code != 200 {
			t.Fatalf("valid session read failed during write: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		writer.ExecContext(context.Background(), "ROLLBACK")
		<-done
		t.Fatal("fresh authenticated GET tried to acquire a writer lock")
	}
}

func TestSQLiteConcurrentAuthenticatedReadsAvoidFalse401And403(t *testing.T) {
	a := fileSQLiteTestApp(t)
	cookie := fileSQLiteLogin(t, a)
	// Exercise the occasional last_seen update as well as normal fresh reads.
	if _, err := a.db.Exec(`UPDATE auth_sessions SET last_seen_at=?`, time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handler := a.scopedAPI()
	paths := []string{"/api/session", "/api/projects", "/api/sprints", "/api/meta", "/api/requirement-categories", "/api/requirements", "/api/profile", "/api/members"}
	errorsFound := make(chan error, 512)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 8; i++ {
			conn, err := a.db.Conn(ctx)
			if err != nil {
				errorsFound <- err
				return
			}
			_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
			if err == nil {
				_, err = conn.ExecContext(ctx, `UPDATE users SET bio=? WHERE id='u_admin'`, fmt.Sprintf("concurrent write %d", i))
			}
			if err == nil {
				time.Sleep(15 * time.Millisecond)
				_, err = conn.ExecContext(ctx, "COMMIT")
			}
			if err != nil {
				conn.ExecContext(context.Background(), "ROLLBACK")
				errorsFound <- fmt.Errorf("concurrent writer: %w", err)
			}
			conn.Close()
		}
	}()
	for worker := 0; worker < 48; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for index := range paths {
				path := paths[(index+offset)%len(paths)]
				response := fileSQLiteRead(ctx, handler, cookie, path)
				if response.Code != http.StatusOK {
					errorsFound <- fmt.Errorf("%s: %d %s", path, response.Code, response.Body.String())
				}
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	var integrity string
	if err := a.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("database integrity=%q err=%v", integrity, err)
	}
}

func TestSQLiteInfrastructureFailuresAreNotAuthenticationOrPermissionDenials(t *testing.T) {
	for _, table := range []string{"auth_sessions", "projects"} {
		t.Run(table, func(t *testing.T) {
			a := fileSQLiteTestApp(t)
			cookie := fileSQLiteLogin(t, a)
			if _, err := a.db.Exec("DROP TABLE " + table); err != nil {
				t.Fatal(err)
			}
			response := fileSQLiteRead(context.Background(), a.scopedAPI(), cookie, "/api/requirement-categories")
			if response.Code != http.StatusServiceUnavailable || !bytes.Contains(response.Body.Bytes(), []byte("database_unavailable")) {
				t.Fatalf("storage error was disguised as authentication/permission error: %d %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Set-Cookie") != "" {
				t.Fatal("infrastructure failure changed a valid session cookie")
			}
		})
	}
	// Actual invalid sessions and unauthorized project access still fail closed.
	a := fileSQLiteTestApp(t)
	cookie := fileSQLiteLogin(t, a)
	response := fileSQLiteRead(context.Background(), a.scopedAPI(), &http.Cookie{Name: sessionCookieName, Value: "invalid-session"}, "/api/session")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid session accepted: %d", response.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-DevFlow-Project", "not-an-accessible-project")
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthorized project accepted: %d", w.Code)
	}
}
