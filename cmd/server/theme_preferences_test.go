package main

import (
	"encoding/json"
	"testing"
)

func TestThemePreferencesMergeValidationAndIsolation(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	path := "/api/preferences/display"
	check := func(method, body, font, theme string) {
		t.Helper()
		w := impersonationRequest(a, member, method, path, body, "stale-project")
		var value displayPreference
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.FontSize != font || value.ThemeMode != theme {
			t.Fatalf("%s %s: %d %s", method, body, w.Code, w.Body)
		}
	}
	check("GET", "", "standard", "light")
	check("PATCH", `{"themeMode":"dark"}`, "standard", "dark")
	check("PATCH", `{"fontSize":"large"}`, "large", "dark")
	check("PATCH", `{"themeMode":"auto"}`, "large", "auto")
	check("GET", "", "large", "auto")
	for _, body := range []string{`null`, `{}`, `{"themeMode":null}`, `{"themeMode":1}`, `{"themeMode":"system"}`, `{"themeMode":"dark","userId":"u_admin"}`, `{"themeMode":"light","fontSize":"huge"}`, `{"themeMode":"light"} {}`} {
		w := impersonationRequest(a, member, "PATCH", path, body, projectID)
		if w.Code != 422 {
			t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body)
		}
	}
	check("GET", "", "large", "auto")
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	w := impersonationRequest(a, admin, "GET", path, "", projectID)
	var value displayPreference
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.ThemeMode != "light" {
		t.Fatal("account theme leaked", w.Body)
	}
}

func TestThemePreferencesLegacyFontAndFailureRollback(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	path := "/api/preferences/display"
	if w := impersonationRequest(a, member, "PATCH", path, `{"fontSize":"small"}`, projectID); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if _, err := a.db.Exec(`UPDATE user_view_preferences SET columns_json='{"fontSize":"small"}' WHERE view_key='display'`); err != nil {
		t.Fatal(err)
	}
	w := impersonationRequest(a, member, "GET", path, "", projectID)
	var value displayPreference
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || value.ThemeMode != "light" {
		t.Fatal("legacy default", w.Body)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER reject_display_update BEFORE UPDATE ON user_view_preferences BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = impersonationRequest(a, member, "PATCH", path, `{"themeMode":"dark"}`, projectID)
	if w.Code != 500 {
		t.Fatal("failure not reported", w.Code)
	}
	w = impersonationRequest(a, member, "GET", path, "", projectID)
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || value.ThemeMode != "light" || value.FontSize != "small" {
		t.Fatal("failed save mutated preferences", w.Body)
	}
}
