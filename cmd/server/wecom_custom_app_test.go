package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const wecomCustomTestCorpID = "ww0123456789abcdef"
const wecomCustomTestAgentID = "1000001"
const wecomCustomTestSecret = "0123456789abcdef0123456789abcdef"
const wecomCustomTestVerifyFile = "WW_verify_0123456789abcdef.txt"

func wecomCustomRequestBody(version int, enabled bool) string {
	return jsonText(map[string]any{
		"corpId": wecomCustomTestCorpID, "agentId": wecomCustomTestAgentID, "origin": "https://app.example",
		"verifyFilename": wecomCustomTestVerifyFile, "secret": wecomCustomTestSecret, "enabled": enabled, "version": version,
	})
}

func wecomCustomFixture(t *testing.T) (*App, *http.Cookie) {
	t.Helper()
	a := testApp(t)
	a.cookieSecure = true
	a.wecomKey = bytes.Repeat([]byte{79}, 32)
	admin := wxCookie(t, a, "u_admin")
	if w := wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", wecomCustomRequestBody(0, true), admin); w.Code != http.StatusOK {
		t.Fatalf("configure custom app: %d %s", w.Code, w.Body.String())
	}
	return a, admin
}

func TestWecomCustomAppSettingsAreMaskedScopedAndVersioned(t *testing.T) {
	a, admin := wecomCustomFixture(t)
	for _, user := range []string{"u_front", "u_viewer"} {
		if w := wxRequest(a, http.MethodGet, "/api/organization/wecom-app", "", wxCookie(t, a, user)); w.Code != http.StatusForbidden {
			t.Fatalf("custom app settings exposed to %s: %d", user, w.Code)
		}
	}
	w := wxRequest(a, http.MethodGet, "/api/organization/wecom-app", "", admin)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), wecomCustomTestSecret) {
		t.Fatalf("unsafe settings response: %d %s", w.Code, w.Body.String())
	}
	view := jsonMap(t, w)
	if view["secretConfigured"] != true || view["configurationReady"] != true || view["externalCallsEnabled"] != false || view["callbackUrl"] != "https://app.example"+wecomAppCallbackPath || view["verifyUrl"] != "https://app.example/"+wecomCustomTestVerifyFile {
		t.Fatalf("unsafe or incomplete settings view: %#v", view)
	}
	settings, err := readWecomAppSettings(context.Background(), a.db)
	if err != nil || bytes.Contains(settings.Encrypted, []byte(wecomCustomTestSecret)) {
		t.Fatal("custom app secret was not encrypted")
	}
	plain, err := wecomAppSecret(a.wecomKey, settings.Encrypted, "", false)
	if err != nil || string(plain) != wecomCustomTestSecret {
		t.Fatal("custom app secret did not decrypt with its own AAD")
	}
	if _, err = wechatSecret(a.wecomKey, settings.Encrypted, "", false); err == nil {
		t.Fatal("custom app secret was accepted by the personal WeChat AAD")
	}
	if w = wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", wecomCustomRequestBody(0, true), admin); w.Code != http.StatusConflict {
		t.Fatalf("stale configuration accepted: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{
		jsonText(map[string]any{"corpId": "not-a-corp", "agentId": wecomCustomTestAgentID, "origin": "https://app.example", "verifyFilename": wecomCustomTestVerifyFile, "enabled": false, "version": 1}),
		jsonText(map[string]any{"corpId": wecomCustomTestCorpID, "agentId": "0", "origin": "https://app.example", "verifyFilename": wecomCustomTestVerifyFile, "enabled": false, "version": 1}),
		jsonText(map[string]any{"corpId": wecomCustomTestCorpID, "agentId": wecomCustomTestAgentID, "origin": "http://app.example", "verifyFilename": wecomCustomTestVerifyFile, "enabled": false, "version": 1}),
		jsonText(map[string]any{"corpId": wecomCustomTestCorpID, "agentId": wecomCustomTestAgentID, "origin": "https://app.example", "verifyFilename": "../../WW_verify_bad.txt", "enabled": false, "version": 1}),
		jsonText(map[string]any{"corpId": wecomCustomTestCorpID, "agentId": wecomCustomTestAgentID, "origin": "https://app.example", "verifyFilename": wecomCustomTestVerifyFile, "clearSecret": true, "enabled": true, "version": 1}),
	} {
		if w = wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", body, admin); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unsafe configuration accepted: %d %s", w.Code, w.Body.String())
		}
	}
	state, _, err := a.createWecomAppState(context.Background(), "login", "", 1)
	if err != nil || state == "" {
		t.Fatalf("cannot seed callback state: %v", err)
	}
	if w = wxRequest(a, http.MethodPatch, "/api/organization/wecom-app", wecomCustomRequestBody(1, true), admin); w.Code != http.StatusOK {
		t.Fatalf("same configuration update failed: %d %s", w.Code, w.Body.String())
	}
	var states int
	if err = a.db.QueryRow(`SELECT count(*) FROM wecom_app_oauth_states`).Scan(&states); err != nil || states != 0 {
		t.Fatalf("old callback states survived configuration change: %d %v", states, err)
	}
	var leaked int
	if err = a.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE after_json LIKE ? OR before_json LIKE ?`, "%"+wecomCustomTestSecret+"%", "%"+wecomCustomTestSecret+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("secret leaked to audit: %d %v", leaked, err)
	}
}

func TestWecomCustomAppBindingStorageIsExclusiveAndCleansUp(t *testing.T) {
	a, _ := wecomCustomFixture(t)
	settings, err := readWecomAppSettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.bindWecomAppIdentity(context.Background(), tx, "u_front", settings, "zhang.san"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = a.db.QueryRow(`SELECT wecom_user_id_hash FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&stored); err != nil || stored == "zhang.san" || len(stored) < 40 {
		t.Fatalf("enterprise WeChat identity was stored unsafely: %q %v", stored, err)
	}
	tx, err = a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = a.bindWecomAppIdentity(context.Background(), tx, "u_back", settings, "zhang.san")
	tx.Rollback()
	var conflict *organizationError
	if !errors.As(err, &conflict) || conflict.Status != http.StatusConflict {
		t.Fatalf("same WeCom identity was rebound: %v", err)
	}
	if _, err = a.db.Exec(`UPDATE tenant_memberships SET status='removed' WHERE tenant_id=? AND user_id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.db.QueryRow(`SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("removed member retained WeCom binding: %d %v", count, err)
	}
}

func TestWecomCustomAppMigrationRepairsDeletedUserCleanupTrigger(t *testing.T) {
	a, _ := wecomCustomFixture(t)
	// 模拟已经运行过错误版本的生产库：SQLite 会接受触发器定义，直到用户删除时
	// 才因 OLD.user_id 不存在而失败。迁移必须主动替换它，而不是被 IF NOT EXISTS 跳过。
	if _, err := a.db.Exec(`DROP TRIGGER wecom_app_cleanup_deleted_user;
CREATE TRIGGER wecom_app_cleanup_deleted_user AFTER DELETE ON users BEGIN
 DELETE FROM user_wecom_app_bindings WHERE tenant_id=OLD.tenant_id AND user_id=OLD.id;
 DELETE FROM wecom_app_oauth_states WHERE user_id=OLD.user_id;
END;`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateWecomCustomApp(); err != nil {
		t.Fatalf("repair existing custom-app trigger: %v", err)
	}
	var definition string
	if err := a.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='wecom_app_cleanup_deleted_user'`).Scan(&definition); err != nil || strings.Contains(definition, "OLD.user_id") || !strings.Contains(definition, "OLD.id") {
		t.Fatalf("deleted-user trigger was not repaired: %q %v", definition, err)
	}
	settings, err := readWecomAppSettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.bindWecomAppIdentity(context.Background(), tx, "u_front", settings, "delete.user"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec(`DELETE FROM users WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatalf("delete user must not execute a stale trigger: %v", err)
	}
	var bindings int
	if err = a.db.QueryRow(`SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=? AND user_id='u_front'`, tenantID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("deleted user retained custom-app binding: %d %v", bindings, err)
	}
}

func TestWecomCustomAppCallbackConsumesOnlyValidStateAndNeverConnectsExternally(t *testing.T) {
	a, _ := wecomCustomFixture(t)
	state, browser, err := a.createWecomAppState(context.Background(), "bind", "u_front", 1)
	if err != nil {
		t.Fatal(err)
	}
	wrong := &http.Cookie{Name: wecomAppStateCookie, Value: strings.Repeat("a", 64), Path: wecomAppCallbackPath}
	if w := wxRequest(a, http.MethodGet, wecomAppCallbackPath+"?state="+state+"&code=test-code", "", wrong); w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "wecom=expired") {
		t.Fatalf("wrong browser state accepted: %d %s", w.Code, w.Header().Get("Location"))
	}
	valid := &http.Cookie{Name: wecomAppStateCookie, Value: browser, Path: wecomAppCallbackPath}
	w := wxRequest(a, http.MethodGet, wecomAppCallbackPath+"?state="+state+"&code=test-code", "", valid)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "wecom=not_connected") {
		t.Fatalf("valid state did not stop at configuration-only boundary: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == wecomAppStateCookie && (cookie.MaxAge >= 0 || !cookie.HttpOnly || !cookie.Secure || cookie.Path != wecomAppCallbackPath) {
			t.Fatalf("unsafe callback cookie cleanup: %#v", cookie)
		}
	}
	if replay := wxRequest(a, http.MethodGet, wecomAppCallbackPath+"?state="+state+"&code=test-code", "", valid); replay.Code != http.StatusSeeOther || !strings.Contains(replay.Header().Get("Location"), "wecom=expired") {
		t.Fatalf("callback replay accepted: %d %s", replay.Code, replay.Header().Get("Location"))
	}
	var bindings int
	if err = a.db.QueryRow(`SELECT count(*) FROM user_wecom_app_bindings WHERE tenant_id=?`, tenantID).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("configuration-only callback created an account binding: %d %v", bindings, err)
	}
	if _, _, err = a.createWecomAppState(context.Background(), "invalid", "", 1); err == nil {
		t.Fatal("invalid callback purpose accepted")
	}
}
