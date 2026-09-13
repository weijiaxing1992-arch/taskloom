package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const loginLimitRows = 16384
const loginChallengeRows = 4096
const loginChallengeTTL = 120

type loginChallenge struct {
	ID        string `json:"id"`
	Image     string `json:"image"`
	ExpiresAt int64  `json:"expiresAt"`
}
type loginGuardResult struct {
	Challenge  *loginChallenge
	RetryAfter int
}
type loginGuardScope struct{ Account, IP, Binding string }

func (a *App) migrateLoginProtection() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS auth_login_limits(key_hash TEXT PRIMARY KEY,window_start INTEGER NOT NULL,count INTEGER NOT NULL,expires_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_auth_login_limits_expiry ON auth_login_limits(expires_at);
CREATE TABLE IF NOT EXISTS auth_login_challenges(id_hash TEXT PRIMARY KEY,binding_hash TEXT NOT NULL,answer_hash TEXT NOT NULL,expires_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_auth_login_challenges_expiry ON auth_login_challenges(expires_at);`)
	return err
}
func (a *App) loginGuardHash(value string) string {
	mac := hmac.New(sha256.New, a.authSigningKey())
	_, _ = mac.Write([]byte("devflow:login-guard:v1:" + value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *App) loginScope(r *http.Request, account string) loginGuardScope {
	// Only a server-observed peer is trusted. Arbitrary forwarded headers must
	// never turn a client-selected IP into a rate-limit bypass. Shared proxies
	// need an explicitly configured, independently verified edge policy.
	ip := watermarkPeerIP(r.RemoteAddr)
	if ip == "" {
		ip = "unknown"
	}
	return loginGuardScope{Account: a.loginGuardHash("account:" + account), IP: a.loginGuardHash("ip:" + ip), Binding: a.loginGuardHash("binding:" + account + "\n" + ip + "\n" + truncate(r.UserAgent(), 255))}
}
func loginGuardCleanup(tx *sql.Tx, now int64) error {
	if _, err := tx.Exec(`DELETE FROM auth_login_limits WHERE expires_at<=?`, now); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM auth_login_challenges WHERE expires_at<=?`, now)
	return err
}

// All counters are committed under the same SQLite writer reservation. The
// fixed window never extends on denial; no user/session is locked or revoked.
func loginCounter(tx *sql.Tx, key string, now, duration int64, maximum int, consume bool) (int, int, error) {
	start := now - now%duration
	var previous int64
	var count int
	err := tx.QueryRow(`SELECT window_start,count FROM auth_login_limits WHERE key_hash=?`, key).Scan(&previous, &count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, err
	}
	if previous != start {
		count = 0
	}
	if count >= maximum {
		return count, int(start + duration - now), nil
	}
	if !consume {
		return count, 0, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		var size int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM auth_login_limits`).Scan(&size); err != nil {
			return 0, 0, err
		}
		if size >= loginLimitRows {
			return 0, 60, nil
		}
	}
	count++
	_, err = tx.Exec(`INSERT INTO auth_login_limits(key_hash,window_start,count,expires_at)VALUES(?,?,?,?) ON CONFLICT(key_hash)DO UPDATE SET window_start=excluded.window_start,count=excluded.count,expires_at=excluded.expires_at`, key, start, count, start+duration)
	return count, 0, err
}
func (a *App) issueLoginChallenge(tx *sql.Tx, scope loginGuardScope, now int64) (*loginChallenge, error) {
	var size int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM auth_login_challenges`).Scan(&size); err != nil {
		return nil, err
	}
	if size >= loginChallengeRows {
		return nil, errors.New("login challenge capacity")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	answer := make([]byte, 6)
	if _, err := rand.Read(answer); err != nil {
		return nil, err
	}
	for i := range answer {
		answer[i] = alphabet[int(answer[i])%len(alphabet)]
	}
	encoded, err := loginChallengeImage(string(answer))
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(`INSERT INTO auth_login_challenges(id_hash,binding_hash,answer_hash,expires_at)VALUES(?,?,?,?)`, a.loginGuardHash("id:"+id), scope.Binding, a.loginGuardHash("answer:"+id+":"+string(answer)), now+loginChallengeTTL)
	if err != nil {
		return nil, err
	}
	return &loginChallenge{ID: id, Image: encoded, ExpiresAt: now + loginChallengeTTL}, nil
}
func (a *App) consumeLoginChallenge(tx *sql.Tx, scope loginGuardScope, id, answer string, now int64) (bool, error) {
	if len(id) != 32 {
		return false, nil
	}
	var binding, digest string
	var expiry int64
	err := tx.QueryRow(`SELECT binding_hash,answer_hash,expires_at FROM auth_login_challenges WHERE id_hash=?`, a.loginGuardHash("id:"+id)).Scan(&binding, &digest, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Every attempt consumes the challenge, including a wrong answer/binding.
	if _, err = tx.Exec(`DELETE FROM auth_login_challenges WHERE id_hash=?`, a.loginGuardHash("id:"+id)); err != nil {
		return false, err
	}
	actual := a.loginGuardHash("answer:" + id + ":" + strings.ToUpper(strings.TrimSpace(answer)))
	return len(answer) <= 32 && expiry > now && binding == scope.Binding && subtle.ConstantTimeCompare([]byte(actual), []byte(digest)) == 1, nil
}
func (a *App) beginPasswordLogin(r *http.Request, scope loginGuardScope, id, answer string, refresh bool) (loginGuardResult, error) {
	var result loginGuardResult
	now := time.Now().Unix()
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = loginGuardCleanup(tx, now); err != nil {
		return result, err
	}
	for _, quota := range []struct {
		key      string
		duration int64
		maximum  int
	}{{"minute:" + scope.IP, 60, 60}, {"burst:" + scope.IP, 600, 300}} {
		_, wait, e := loginCounter(tx, quota.key, now, quota.duration, quota.maximum, true)
		if e != nil {
			return result, e
		}
		if wait > 0 {
			result.RetryAfter = wait
			return result, tx.Commit()
		}
	}
	account, _, err := loginCounter(tx, "failure:"+scope.Account, now, 600, 1000, false)
	if err != nil {
		return result, err
	}
	ip, _, err := loginCounter(tx, "failure:"+scope.IP, now, 600, 1000, false)
	if err != nil {
		return result, err
	}
	if account >= 5 || ip >= 20 || id != "" || refresh {
		valid := false
		if refresh && id != "" {
			_, err = a.consumeLoginChallenge(tx, scope, id, "", now)
			if err != nil {
				return result, err
			}
		}
		if !refresh {
			valid, err = a.consumeLoginChallenge(tx, scope, id, answer, now)
			if err != nil {
				return result, err
			}
		}
		if !valid {
			result.Challenge, err = a.issueLoginChallenge(tx, scope, now)
			if err != nil {
				return result, err
			}
			return result, tx.Commit()
		}
	}
	_, result.RetryAfter, err = loginCounter(tx, "attempt:"+scope.Account, now, 300, 12, true)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (a *App) finishPasswordLogin(r *http.Request, scope loginGuardScope, success bool) (loginGuardResult, error) {
	var result loginGuardResult
	now := time.Now().Unix()
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = loginGuardCleanup(tx, now); err != nil {
		return result, err
	}
	if success {
		_, err = tx.Exec(`DELETE FROM auth_login_limits WHERE key_hash=?`, "failure:"+scope.Account)
		if err != nil {
			return result, err
		}
		return result, tx.Commit()
	}
	account, wait, err := loginCounter(tx, "failure:"+scope.Account, now, 600, 1000, true)
	if err != nil {
		return result, err
	}
	if wait > 0 {
		result.RetryAfter = wait
	}
	ip, wait, err := loginCounter(tx, "failure:"+scope.IP, now, 600, 1000, true)
	if err != nil {
		return result, err
	}
	if wait > 0 {
		result.RetryAfter = wait
	}
	if result.RetryAfter == 0 && (account >= 5 || ip >= 20) {
		result.Challenge, err = a.issueLoginChallenge(tx, scope, now)
		if err != nil {
			return result, err
		}
	}
	return result, tx.Commit()
}
func writeLoginGuard(w http.ResponseWriter, result loginGuardResult) bool {
	if result.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(result.RetryAfter))
		message := "登录尝试过于频繁，请稍后重试"
		if w.Header().Get("Content-Language") == "en-US" {
			message = "Too many sign-in attempts. Please try again shortly."
		}
		write(w, 429, map[string]any{"error": map[string]any{"code": "login_rate_limited", "message": message, "retryAfterSeconds": result.RetryAfter}})
		return true
	}
	if result.Challenge != nil {
		message := "请完成安全验证后重试登录"
		if w.Header().Get("Content-Language") == "en-US" {
			message = "Complete the security check and try signing in again."
		}
		write(w, 401, map[string]any{"error": map[string]any{"code": "login_challenge_required", "message": message, "challenge": result.Challenge}})
		return true
	}
	return false
}

var loginDummyHashes = struct {
	sync.Mutex
	values map[int]string
}{values: map[int]string{}}

func (a *App) dummyLoginHash() string {
	// At most one value per bcrypt cost (4..31), never one entry per account.
	loginDummyHashes.Lock()
	defer loginDummyHashes.Unlock()
	cost := a.bcryptCost()
	if hash := loginDummyHashes.values[cost]; hash != "" {
		return hash
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("devflow-dummy-verification-not-an-account"), cost)
	loginDummyHashes.values[cost] = string(hash)
	return string(hash)
}

// A local, deliberately readable visual challenge is defense in depth, not
// MFA or an OCR-proof CAPTCHA. Answers are rasterized, never returned as text.
var loginGlyphs = map[byte][7]string{
	'2': {"11110", "00001", "00001", "01110", "10000", "10000", "11111"}, '3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"}, '4': {"10010", "10010", "10010", "11111", "00010", "00010", "00010"}, '5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"}, '6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"}, '7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"}, '8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"}, '9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"}, 'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"}, 'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"}, 'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"}, 'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"}, 'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"}, 'G': {"01111", "10000", "10000", "10111", "10001", "10001", "01111"}, 'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"}, 'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"}, 'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"}, 'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"}, 'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"}, 'N': {"10001", "11001", "11001", "10101", "10011", "10011", "10001"}, 'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"}, 'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"}, 'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"}, 'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"}, 'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"}, 'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"}, 'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"}, 'W': {"10001", "10001", "10001", "10101", "10101", "11011", "10001"}, 'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"}, 'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"}, 'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
}

func loginChallengeImage(answer string) (string, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, 190, 52))
	background := color.RGBA{247, 250, 255, 255}
	ink := color.RGBA{35, 67, 112, 255}
	for y := 0; y < 52; y++ {
		for x := 0; x < 190; x++ {
			canvas.SetRGBA(x, y, background)
		}
	}
	for index, char := range []byte(answer) {
		glyph := loginGlyphs[char]
		for row, line := range glyph {
			for column, pixel := range line {
				if pixel == '1' {
					for dy := 0; dy < 4; dy++ {
						for dx := 0; dx < 4; dx++ {
							canvas.SetRGBA(10+index*29+column*4+dx, 12+row*4+dy, ink)
						}
					}
				}
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}
