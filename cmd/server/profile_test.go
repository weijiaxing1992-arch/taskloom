package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestProfileReadAndViewerCanEditOwnInformation(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodGet, "/api/profile", "u_viewer", projectID, "")
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"id":"u_viewer"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"memberships"`)) {
		t.Fatalf("profile read failed: %d %s", w.Code, w.Body.String())
	}
	body := `{"name":"顾远舟","email":"guyuanzhou@devflow.local","phone":"+86 138 0000 0000","jobTitle":"质量观察员","bio":"关注交付质量与研发效能。","avatarColor":"#3478F6","locale":"zh-CN","timezone":"Asia/Shanghai","emailNotifications":false}`
	w = apiRequest(a, http.MethodPatch, "/api/profile", "u_viewer", projectID, body)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("顾远舟")) || !bytes.Contains(w.Body.Bytes(), []byte(`"emailNotifications":false`)) {
		t.Fatalf("viewer own profile update failed: %d %s", w.Code, w.Body.String())
	}
	var name, email, color string
	var notifications bool
	a.db.QueryRow(`SELECT name,email,avatar_color,email_notifications FROM users WHERE tenant_id=? AND id='u_viewer'`, tenantID).Scan(&name, &email, &color, &notifications)
	if name != "顾远舟" || email != "guyuanzhou@devflow.local" || color != "#3478F6" || notifications {
		t.Fatalf("profile persistence mismatch: %q %q %q %v", name, email, color, notifications)
	}
	w = apiRequest(a, http.MethodGet, "/api/session", "u_viewer", projectID, "")
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"avatarColor":"#3478F6"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"顾远舟"`)) {
		t.Fatalf("session did not reflect profile: %d %s", w.Code, w.Body.String())
	}
}

func TestProfileNamePropagationAndEmailConflict(t *testing.T) {
	a := testApp(t)
	body := `{"name":"林夏新","email":"linxia.new@devflow.local","phone":"","jobTitle":"研发负责人","bio":"","avatarColor":"#665FE8","locale":"zh-CN","timezone":"Asia/Shanghai","emailNotifications":true}`
	w := apiRequest(a, http.MethodPatch, "/api/profile", "u_admin", projectID, body)
	if w.Code != http.StatusOK {
		t.Fatalf("admin profile update failed: %d %s", w.Code, w.Body.String())
	}
	var stale int
	a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND owner_user_id='u_admin' AND owner!='林夏新'`, tenantID).Scan(&stale)
	if stale != 0 {
		t.Fatalf("denormalized owner name was not updated: %d stale rows", stale)
	}
	body = `{"name":"林夏新","email":"chencheng@devflow.local","phone":"","jobTitle":"研发负责人","bio":"","avatarColor":"#665FE8","locale":"zh-CN","timezone":"Asia/Shanghai","emailNotifications":true}`
	w = apiRequest(a, http.MethodPatch, "/api/profile", "u_admin", projectID, body)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate email should be rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestPasswordSetupAndRotation(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, http.MethodPost, "/api/profile/password", "u_admin", projectID, `{"currentPassword":"TaskLoom2026!","newPassword":"Secure2026","confirmPassword":"Secure2026"}`)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"passwordConfigured":true`)) {
		t.Fatalf("initial password setup failed: %d %s", w.Code, w.Body.String())
	}
	var firstHash string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&firstHash)
	if !strings.HasPrefix(firstHash, "$2") || bytes.Contains([]byte(firstHash), []byte("Secure2026")) || !verifyPassword("Secure2026", firstHash) {
		t.Fatalf("password was not securely persisted: %q", firstHash)
	}
	w = apiRequest(a, http.MethodPost, "/api/profile/password", "u_admin", projectID, `{"currentPassword":"wrong","newPassword":"Better2027","confirmPassword":"Better2027"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong current password should fail: %d %s", w.Code, w.Body.String())
	}
	var unchanged string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&unchanged)
	if unchanged != firstHash {
		t.Fatal("password changed after failed current-password validation")
	}
	w = apiRequest(a, http.MethodPost, "/api/profile/password", "u_admin", projectID, `{"currentPassword":"Secure2026","newPassword":"Better2027","confirmPassword":"Better2027"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("password rotation failed: %d %s", w.Code, w.Body.String())
	}
	var rotated string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE tenant_id=? AND id='u_admin'`, tenantID).Scan(&rotated)
	if rotated == firstHash || !verifyPassword("Better2027", rotated) || verifyPassword("Secure2026", rotated) {
		t.Fatal("password rotation did not replace the credential")
	}
	var auditCount, noticeCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND actor_id='u_admin' AND action='password_changed' AND before_json='{}' AND after_json='{}'`, tenantID).Scan(&auditCount)
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications WHERE tenant_id=? AND recipient_user_id='u_admin' AND event_type='user.password_changed'`, tenantID).Scan(&noticeCount)
	if auditCount != 2 || noticeCount != 2 || notificationURL("user", 0) != "/profile" {
		t.Fatalf("password security audit incomplete: audits=%d notices=%d", auditCount, noticeCount)
	}
}

func TestPasswordValidation(t *testing.T) {
	for _, weak := range []string{"short1", "onlyletters", "12345678", strings.Repeat("密", 30) + "a1"} {
		if validatePassword(weak) == nil {
			t.Fatalf("weak password accepted: %q", weak)
		}
	}
}
