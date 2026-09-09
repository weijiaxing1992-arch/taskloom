package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 确定性地拆开“验证旧摘要”和“事务签发”，模拟 bcrypt 期间的并发改密，无需依赖调度时序。
func TestInitialPasswordVerifiedLoginCannotSurviveCredentialChange(t *testing.T) {
	a := testApp(t)
	verified := forceInitialPassword(t, a, "u_member")
	_, cookie := loginRequest(a, "zhouyu@devflow.local", temporaryTestPassword)
	w := initialRequest(a, cookie, "POST", "/api/auth/initial-password", initialBody(temporaryTestPassword, "Personal2027!"), "u_member")
	if w.Code != 200 {
		t.Fatalf("initial change: %d %s", w.Code, w.Body.String())
	}
	var sessionsBefore int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions`).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	w = httptest.NewRecorder()
	created, err := a.issueSessionWithVerifiedHash(w, r, "u_member", verified, "")
	if !errors.Is(err, errVerifiedCredentialChanged) || created != nil || len(w.Result().Cookies()) != 0 {
		t.Fatal("an obsolete temporary password minted a session", err)
	}
	var sessionsAfter int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions`).Scan(&sessionsAfter); err != nil || sessionsAfter != sessionsBefore {
		t.Fatal("stale login changed session rows", err)
	}
}

func TestInitialPasswordVerifiedLoginChecksActivationAndLegacyUpgrade(t *testing.T) {
	for _, scenario := range []string{"legacy-upgrade", "changed-hash", "inactive-user", "inactive-membership", "insert-failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			salt := []byte("test-only-legacy-salt")
			legacy := "pbkdf2_sha256$1000$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(derivePasswordKey([]byte(temporaryTestPassword), salt, 1000, 32))
			if _, err := a.db.Exec(`UPDATE users SET password_hash=? WHERE id='u_member' AND tenant_id=?`, legacy, tenantID); err != nil {
				t.Fatal(err)
			}
			upgraded, err := a.encodePassword(temporaryTestPassword)
			if err != nil {
				t.Fatal(err)
			}
			expected := legacy
			switch scenario {
			case "changed-hash":
				expected, err = a.encodePassword("Independent2028!")
				if err == nil {
					_, err = a.db.Exec(`UPDATE users SET password_hash=? WHERE id='u_member' AND tenant_id=?`, expected, tenantID)
				}
			case "inactive-user":
				_, err = a.db.Exec(`UPDATE users SET active=0 WHERE id='u_member' AND tenant_id=?`, tenantID)
			case "inactive-membership":
				_, err = a.db.Exec(`UPDATE tenant_memberships SET status='inactive' WHERE user_id='u_member' AND tenant_id=?`, tenantID)
			case "insert-failure":
				_, err = a.db.Exec(`CREATE TRIGGER deny_session BEFORE INSERT ON auth_sessions BEGIN SELECT RAISE(ABORT,'private database failure'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/auth/login", nil)
			cookie, err := a.issueSessionWithVerifiedHash(w, r, "u_member", legacy, upgraded)
			if scenario == "legacy-upgrade" {
				expected = upgraded
				if err != nil || cookie == nil || len(w.Result().Cookies()) != 1 {
					t.Fatal("valid legacy upgrade failed", err)
				}
			} else if err == nil || cookie != nil || len(w.Result().Cookies()) != 0 {
				t.Fatal("failed/stale login issued a cookie", err)
			}
			var stored string
			if err = a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_member' AND tenant_id=?`, tenantID).Scan(&stored); err != nil || stored != expected {
				t.Fatal("legacy upgrade overwrote newer credentials or failed to roll back", err)
			}
		})
	}
}

func TestProfilePasswordTransactionRevalidatesSecuritySnapshot(t *testing.T) {
	for _, scenario := range []string{"changed-hash", "initial-reset", "revoked-session", "expired-session", "no-session", "disabled", "inactive-user", "inactive-membership", "impersonation", "audit-failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
			if cookie == nil {
				t.Fatal("login failed")
			}
			raw, _, err := a.parseSignedSession(cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
			scoped := *a
			scoped.user, scoped.project, scoped.sessionToken = "u_admin", projectID, tokenDigest(raw)
			var original string
			if err = a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin' AND tenant_id=?`, tenantID).Scan(&original); err != nil {
				t.Fatal(err)
			}
			encoded, err := a.encodePassword("Delayed2028!")
			if err != nil {
				t.Fatal(err)
			}
			expected := original
			switch scenario {
			case "changed-hash":
				expected, err = a.encodePassword("Independent2028!")
				if err == nil {
					_, err = a.db.Exec(`UPDATE users SET password_hash=? WHERE id='u_admin' AND tenant_id=?`, expected, tenantID)
				}
			case "initial-reset":
				expected = forceInitialPassword(t, a, "u_admin")
			case "revoked-session":
				_, err = a.db.Exec(`UPDATE auth_sessions SET revoked_at=? WHERE token_hash=?`, orgNow(), scoped.sessionToken)
			case "expired-session":
				_, err = a.db.Exec(`UPDATE auth_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, scoped.sessionToken)
			case "no-session":
				scoped.sessionToken = ""
			case "disabled":
				_, err = a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='u_admin' AND tenant_id=?`, tenantID)
			case "inactive-user":
				_, err = a.db.Exec(`UPDATE users SET active=0 WHERE id='u_admin' AND tenant_id=?`, tenantID)
			case "inactive-membership":
				_, err = a.db.Exec(`UPDATE tenant_memberships SET status='inactive' WHERE user_id='u_admin' AND tenant_id=?`, tenantID)
			case "impersonation":
				_, err = a.db.Exec(`INSERT INTO auth_impersonations(tenant_id,session_hash,admin_user_id,target_user_id,project_id,reason,started_at,expires_at)VALUES(?,?,'u_admin','u_member',?,'test only',?,?)`, tenantID, scoped.sessionToken, projectID, orgNow(), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			case "audit-failure":
				_, err = a.db.Exec(`CREATE TRIGGER deny_password_audit BEFORE INSERT ON audit_logs WHEN NEW.action='password_changed' BEGIN SELECT RAISE(ABORT,'private audit failure'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = scoped.commitProfilePasswordChange(context.Background(), original, encoded, orgNow()); err == nil {
				t.Fatal("an obsolete password change committed")
			}
			var stored string
			if err = a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin' AND tenant_id=?`, tenantID).Scan(&stored); err != nil || stored != expected {
				t.Fatal("newer credentials overwritten", err)
			}
			var count int
			if err = a.db.QueryRow(`SELECT (SELECT COUNT(*) FROM audit_logs WHERE action='password_changed')+(SELECT COUNT(*) FROM user_notifications WHERE event_type='user.password_changed')`).Scan(&count); err != nil || count != 0 {
				t.Fatal("a rejected update emitted an audit or notification", err)
			}
		})
	}
}

func TestProfilePasswordStorageFailureIsRedactedAndAtomic(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`CREATE TRIGGER deny_password_audit BEFORE INSERT ON audit_logs WHEN NEW.action='password_changed' BEGIN SELECT RAISE(ABORT,'private audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", "/api/profile/password", "u_admin", projectID, initialBody(seedPassword, "Personal2027!"))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private audit") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("failure leaked storage state: %d %s", w.Code, w.Body.String())
	}
	var stored string
	if err := a.db.QueryRow(`SELECT password_hash FROM users WHERE id='u_admin'`).Scan(&stored); err != nil || !verifyPassword(seedPassword, stored) {
		t.Fatal("failed audit changed password", err)
	}
}
