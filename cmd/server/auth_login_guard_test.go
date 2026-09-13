package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func guardedLogin(t *testing.T, a *App, email, password, ip string, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"email": email, "password": password}
	for k, v := range extra {
		body[k] = v
	}
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(jsonText(body)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip
	r.Header.Set("User-Agent", "isolated-login-test")
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}
func challengeFromResponse(t *testing.T, w *httptest.ResponseRecorder) loginChallenge {
	t.Helper()
	var data struct {
		Error struct {
			Code      string         `json:"code"`
			Challenge loginChallenge `json:"challenge"`
		} `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &data) != nil || w.Code != 401 || data.Error.Code != "login_challenge_required" || len(data.Error.Challenge.ID) != 32 {
		t.Fatalf("missing challenge: status=%d", w.Code)
	}
	return data.Error.Challenge
}
func fixedChallengeAnswer(t *testing.T, a *App, c loginChallenge) string {
	t.Helper()
	answer := "AB2345"
	bulkFixtureExec(t, a, `UPDATE auth_login_challenges SET answer_hash=? WHERE id_hash=?`, a.loginGuardHash("answer:"+c.ID+":"+answer), a.loginGuardHash("id:"+c.ID))
	return answer
}
func challengeFixture(t *testing.T, a *App, email, ip string) loginChallenge {
	t.Helper()
	for i := 0; i < 4; i++ {
		w := guardedLogin(t, a, email, "synthetic-wrong-password", ip, nil)
		if w.Code != 401 || jsonMap(t, w)["error"].(map[string]any)["code"] != "invalid_credentials" {
			t.Fatal("ordinary failure changed", w.Code)
		}
	}
	return challengeFromResponse(t, guardedLogin(t, a, email, "synthetic-wrong-password", ip, nil))
}
func TestLoginGuardThresholdEnumerationAndRasterOnlyChallenge(t *testing.T) {
	for _, kind := range []string{"known", "unknown", "inactive"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			email := "linxia@devflow.local"
			if kind == "unknown" {
				email = "nonexistent@example.test"
			}
			if kind == "inactive" {
				bulkFixtureExec(t, a, `UPDATE users SET active=0 WHERE id='u_admin'`)
			}
			c := challengeFixture(t, a, email, "198.51.100.10:5000")
			if c.ExpiresAt <= time.Now().Unix() || c.ExpiresAt > time.Now().Unix()+120 {
				t.Fatal("unbounded challenge expiry")
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c.Image, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(strings.NewReader(string(raw)))
			if err != nil || img.Bounds().Dx() != 190 || img.Bounds().Dy() != 52 {
				t.Fatal("challenge is not a valid raster image")
			}
			var rows string
			a.db.QueryRow(`SELECT group_concat(key_hash) FROM auth_login_limits`).Scan(&rows)
			if strings.Contains(rows, email) || strings.Contains(rows, "198.51.100.10") {
				t.Fatal("identifier leaked into limiter keys")
			}
			w := guardedLogin(t, a, email, seedPassword, "198.51.100.10:5000", nil)
			challengeFromResponse(t, w)
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("missing verification issued a session")
			}
		})
	}
}
func TestLoginGuardChallengeIsBoundExpiringAndOneUse(t *testing.T) {
	for _, scenario := range []string{"wrong-answer", "other-account", "other-ip", "expired", "refresh", "replay", "oversized-answer"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			email, ip := "linxia@devflow.local", "198.51.100.11:4000"
			c := challengeFixture(t, a, email, ip)
			answer := fixedChallengeAnswer(t, a, c)
			attemptEmail, attemptIP, attemptAnswer := email, ip, answer
			extra := map[string]any{"challengeId": c.ID, "challengeAnswer": answer}
			switch scenario {
			case "wrong-answer":
				attemptAnswer = "WRONG1"
			case "other-account":
				attemptEmail = "someone@example.test"
			case "other-ip":
				attemptIP = "198.51.100.12:4000"
			case "expired":
				bulkFixtureExec(t, a, `UPDATE auth_login_challenges SET expires_at=0`)
			case "refresh":
				extra["refreshChallenge"] = true
			case "oversized-answer":
				attemptAnswer = strings.Repeat("A", 40)
			}
			extra["challengeAnswer"] = attemptAnswer
			w := guardedLogin(t, a, attemptEmail, seedPassword, attemptIP, extra)
			if scenario == "replay" {
				if w.Code != 200 || len(w.Result().Cookies()) != 1 {
					t.Fatal("correct bound challenge did not sign in", w.Code)
				}
			} else {
				challengeFromResponse(t, w)
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("invalid verification issued a session")
				}
			}
			w = guardedLogin(t, a, email, seedPassword, ip, map[string]any{"challengeId": c.ID, "challengeAnswer": answer})
			challengeFromResponse(t, w)
		})
	}
}
func TestLoginGuardChallengeConcurrencyAdmitsExactlyOne(t *testing.T) {
	a := testApp(t)
	a.db.SetMaxOpenConns(1)
	email, ip := "linxia@devflow.local", "198.51.100.13:1234"
	c := challengeFixture(t, a, email, ip)
	answer := fixedChallengeAnswer(t, a, c)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- guardedLogin(t, a, email, seedPassword, ip, map[string]any{"challengeId": c.ID, "challengeAnswer": answer}).Code
		}()
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		} else if code != 401 {
			t.Fatal("unexpected concurrency result", code)
		}
	}
	if success != 1 {
		t.Fatal("challenge replay minted multiple sessions", success)
	}
}
func TestLoginGuardNormalizedEmailSharesFailureThreshold(t *testing.T) {
	a := testApp(t)
	variants := []string{"LINXIA@DEVFLOW.LOCAL", " linxia@devflow.local ", "LinXia@DevFlow.Local", "linxia@devflow.local", "  LINXIA@devflow.local  "}
	for index, email := range variants {
		w := guardedLogin(t, a, email, "synthetic-wrong-password", fmt.Sprintf("198.51.100.%d:1234", index+100), nil)
		if index < 4 {
			if w.Code != 401 || jsonMap(t, w)["error"].(map[string]any)["code"] != "invalid_credentials" {
				t.Fatal("ordinary failure changed", w.Code)
			}
		} else {
			challengeFromResponse(t, w)
		}
	}
	challengeFromResponse(t, guardedLogin(t, a, "linxia@devflow.local", seedPassword, "198.51.100.105:1234", nil))
}
func TestLoginGuardAccountAndIPQuotasCannotBeBypassedOrPermanentlyLockAccounts(t *testing.T) {
	a := testApp(t)
	now := time.Now().Unix()
	for i := 0; i < 13; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i+1)
		scope := a.loginScope(r, "normalized@example.test")
		result, err := a.beginPasswordLogin(r, scope, "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if i < 12 && result.RetryAfter != 0 || i == 12 && (result.RetryAfter < 1 || result.RetryAfter > 300) {
			t.Fatal("account quota bypassed across peers", i, result.RetryAfter)
		}
	}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "[::ffff:198.51.100.99]:2345"
	first := a.loginScope(r, "a@example.test")
	r.RemoteAddr = "198.51.100.99:9999"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	r.Header.Set("X-Real-IP", "9.9.9.9")
	if first.IP != a.loginScope(r, "a@example.test").IP {
		t.Fatal("IP normalization or forged headers bypassed quota")
	}
	for i := 0; i < 61; i++ {
		r.Header.Set("X-Forwarded-For", fmt.Sprint(i))
		result, err := a.beginPasswordLogin(r, a.loginScope(r, fmt.Sprintf("account%d@example.test", i)), "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if i == 60 && (result.RetryAfter < 1 || result.RetryAfter > 60) {
			t.Fatal("IP quota bypassed by account rotation")
		}
	}
	bulkFixtureExec(t, a, `UPDATE auth_login_limits SET expires_at=?`, now-1)
	w := guardedLogin(t, a, "linxia@devflow.local", seedPassword, "198.51.100.99:9999", nil)
	if w.Code != 200 {
		t.Fatal("temporary quota did not recover", w.Code)
	}
}
func TestLoginGuardPersistenceBoundsAndFailClosedStorage(t *testing.T) {
	for _, kind := range []string{"limits", "challenges", "database-failure"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			if kind == "database-failure" {
				bulkFixtureExec(t, a, `DROP TABLE auth_login_limits`)
			} else if kind == "limits" {
				bulkFixtureExec(t, a, `WITH RECURSIVE n(x)AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO auth_login_limits SELECT 'fixture'||x,0,1,? FROM n`, loginLimitRows, time.Now().Unix()+600)
			} else {
				bulkFixtureExec(t, a, `WITH RECURSIVE n(x)AS(SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO auth_login_challenges SELECT 'fixture'||x,'binding','digest',? FROM n`, loginChallengeRows, time.Now().Unix()+600)
			}
			w := guardedLogin(t, a, "linxia@devflow.local", seedPassword, "198.51.100.20:1234", map[string]any{"refreshChallenge": true})
			want := 503
			if kind == "limits" {
				want = 429
			}
			if w.Code != want || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "table") || strings.Contains(w.Body.String(), "capacity") {
				t.Fatal("storage capacity/failure opened login or leaked internals", w.Code)
			}
			if kind == "limits" && bulkFixtureCount(t, a, `SELECT COUNT(*) FROM auth_login_limits`) > loginLimitRows || kind == "challenges" && bulkFixtureCount(t, a, `SELECT COUNT(*) FROM auth_login_challenges`) > loginChallengeRows {
				t.Fatal("guard storage exceeded hard limit")
			}
		})
	}
}
func TestLoginGuardExistingSessionRestoresWithoutPasswordOrChallenge(t *testing.T) {
	a := testApp(t)
	w, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
	if w.Code != 200 || cookie == nil {
		t.Fatal("fixture sign-in failed")
	}
	if !cookie.HttpOnly || cookie.MaxAge != 30*24*60*60 {
		t.Fatal("persistent cookie contract changed")
	}
	challengeFixture(t, a, "linxia@devflow.local", "198.51.100.21:1234")
	restarted := *a
	if err := restarted.migrateAuth(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/session", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	restarted.scopedAPI().ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "challenge") || bulkFixtureCount(t, a, `SELECT COUNT(*) FROM auth_sessions WHERE revoked_at IS NOT NULL`) != 0 {
		t.Fatal("valid session needlessly required reauthentication", w.Code)
	}
	r = httptest.NewRequest("GET", "/api/session", nil)
	r.Header.Set("X-TaskLoom-User", "u_admin")
	w = httptest.NewRecorder()
	restarted.scopedAPI().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("identity-only restoration bypassed session")
	}
}
