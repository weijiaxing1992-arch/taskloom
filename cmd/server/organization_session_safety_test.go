package main

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 固定认证已完成的请求快照，再改变会话表，确定性模拟等待写锁期间的注销/撤销。
func administrationSessionFixture(t *testing.T, a *App, user string) App {
	t.Helper()
	cookie, err := a.issueSession(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), user)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := a.parseSignedSession(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	scoped := *a
	scoped.user, scoped.project, scoped.sessionToken = user, projectID, tokenDigest(raw)
	return scoped
}

func TestAdministrationWritesRejectRevokedAuthenticatedSnapshot(t *testing.T) {
	for _, operation := range []string{"member-bulk", "project-members", "project-archive"} {
		for _, scenario := range []string{"revoked", "expired", "malformed-expiry", "missing", "foreign-user", "missing-context", "impersonation", "storage-failure"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				a := testApp(t)
				scoped := administrationSessionFixture(t, a, "u_admin")
				expectedStatus := 401
				switch scenario {
				case "revoked":
					bulkFixtureExec(t, a, `UPDATE auth_sessions SET revoked_at=? WHERE token_hash=?`, orgNow(), scoped.sessionToken)
				case "expired":
					bulkFixtureExec(t, a, `UPDATE auth_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, scoped.sessionToken)
				case "malformed-expiry":
					bulkFixtureExec(t, a, `UPDATE auth_sessions SET expires_at='not-a-timestamp' WHERE token_hash=?`, scoped.sessionToken)
				case "missing":
					bulkFixtureExec(t, a, `DELETE FROM auth_sessions WHERE token_hash=?`, scoped.sessionToken)
				case "foreign-user":
					other := administrationSessionFixture(t, a, "u_front")
					scoped.sessionToken = other.sessionToken
				case "missing-context":
					scoped.sessionToken = ""
				case "impersonation":
					bulkFixtureExec(t, a, `INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at)VALUES(?,?,'u_admin','u_front',?,'isolated fixture',?,'2100-01-01T00:00:00Z')`, tenantID, scoped.sessionToken, projectID, orgNow())
					expectedStatus = 403
				case "storage-failure":
					bulkFixtureExec(t, a, `DROP TABLE auth_sessions`)
					expectedStatus = 503
				}
				beforeAudit := bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`)
				beforeRevision := bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID)
				method, path, body := "POST", "/api/organization/members/bulk", `{"action":"deactivate","userIds":["u_front"]}`
				if operation == "project-members" {
					method, path, body = "PATCH", "/api/projects/"+insightProjectID+"/members", `{"addUserIds":["u_front"]}`
				} else if operation == "project-archive" {
					method, path, body = "POST", "/api/projects/"+projectID+"/archive", `{}`
				}
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				if operation == "member-bulk" {
					scoped.organizationMembersBulk(w, r)
				} else if operation == "project-members" {
					scoped.manageProjectMembers(w, r, insightProjectID)
				} else {
					scoped.projectResource(w, r)
				}
				if w.Code != expectedStatus {
					t.Fatalf("失效会话仍使用旧管理身份: %d %s", w.Code, w.Body.String())
				}
				if w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), "no such table") {
					t.Fatal("拒绝请求修改了 Cookie 或泄露存储细节")
				}
				if bulkFixtureCount(t, a, `SELECT active FROM users WHERE id='u_front'`) != 1 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE project_id=? AND user_id='u_front'`, insightProjectID) != 0 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM audit_logs`) != beforeAudit || lifecycleStatus(t, a, projectID) != "active" || bulkFixtureCount(t, a, `SELECT revision FROM organization_write_locks WHERE tenant_id=?`, tenantID) != beforeRevision {
					t.Fatal("被拒绝的旧请求改变了成员、授权或审计")
				}
			})
		}
	}
}

type administrationBodyGate struct {
	ctx     context.Context
	reader  io.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *administrationBodyGate) Read(buffer []byte) (int, error) {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
		return g.reader.Read(buffer)
	case <-g.ctx.Done():
		return 0, g.ctx.Err()
	}
}

// 使用真实 Cookie 和完整 HTTP 入口，在认证后、读取提交内容期间撤销会话。
// 延期 Reader 提供确定性同步，不依赖睡眠或将已撤销 Cookie 重新送入认证的伪竞态。
func TestAdministrationHTTPRevocationBetweenAuthenticationAndWrite(t *testing.T) {
	for _, target := range []struct{ method, path, body string }{
		{"POST", "/api/organization/members/bulk", `{"action":"deactivate","userIds":["u_front"]}`},
		{"PATCH", "/api/projects/" + insightProjectID + "/members", `{"addUserIds":["u_front"]}`},
		{"PATCH", "/api/projects/" + projectID, `{"description":"revoked-http"}`},
	} {
		t.Run(target.path, func(t *testing.T) {
			a := fileSQLiteTestApp(t)
			cookie := fileSQLiteLogin(t, a)
			raw, _, err := a.parseSignedSession(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			gate := &administrationBodyGate{ctx: ctx, reader: strings.NewReader(target.body), entered: make(chan struct{}), release: make(chan struct{})}
			r := httptest.NewRequest(target.method, target.path, nil).WithContext(ctx)
			r.Body = io.NopCloser(gate)
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(cookie)
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { w := httptest.NewRecorder(); a.scopedAPI().ServeHTTP(w, r); done <- w }()
			select {
			case <-gate.entered:
			case response := <-done:
				t.Fatalf("请求未到达认证后的提交读取: %d", response.Code)
			case <-ctx.Done():
				t.Fatal("等待认证后的提交读取超时")
			}
			bulkFixtureExec(t, a, `UPDATE auth_sessions SET revoked_at=? WHERE token_hash=?`, orgNow(), tokenDigest(raw))
			close(gate.release)
			select {
			case response := <-done:
				if response.Code != 401 {
					t.Fatalf("已注销的在途 HTTP 请求仍然提交: %d", response.Code)
				}
			case <-ctx.Done():
				t.Fatal("撤销后的请求没有完成")
			}
			if bulkFixtureCount(t, a, `SELECT active FROM users WHERE id='u_front'`) != 1 || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM project_members WHERE project_id=? AND user_id='u_front'`, insightProjectID) != 0 || lifecycleStatus(t, a, projectID) != "active" || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM projects WHERE id=? AND description='revoked-http'`, projectID) != 0 {
				t.Fatal("已注销请求修改了业务数据")
			}
		})
	}
}
