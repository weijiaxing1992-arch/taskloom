package main

import (
	"context"
	"testing"
)

// 自动生成会外发业务文字，不能因旧企业已配置 AI 而默认获得新用途授权。
func TestReleaseNotesSettingsRequireExplicitConsent(t *testing.T) {
	a := aiApp(t)
	read := func() aiSettings {
		t.Helper()
		s, err := a.readAISettings(context.Background(), a.db)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	initial := read()
	if initial.AutoReleaseNotes {
		t.Fatal("existing AI configuration silently opted in")
	}
	for _, input := range []map[string]any{
		{"autoReleaseNotes": true},
		{"autoReleaseNotes": true, "expectedVersion": initial.Version},
		{"autoReleaseNotes": true, "expectedVersion": initial.Version - 1, "confirmAutomatic": true},
	} {
		w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(input))
		if w.Code != 409 || read().AutoReleaseNotes {
			t.Fatalf("missing/stale consent accepted: %d %s", w.Code, w.Body.String())
		}
	}
	for _, uid := range []string{"u_viewer", "u_pm", "u_front"} {
		w := apiRequest(a, "PATCH", "/api/organization/ai-settings", uid, projectID, jsonText(map[string]any{"autoReleaseNotes": true, "expectedVersion": initial.Version, "confirmAutomatic": true}))
		if w.Code != 403 {
			t.Fatalf("non-admin consent accepted: %d", w.Code)
		}
	}
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"autoReleaseNotes": true, "expectedVersion": initial.Version, "confirmAutomatic": true}))
	if w.Code != 200 || jsonMap(t, w)["autoReleaseNotes"] != true {
		t.Fatal(w.Body.String())
	}
	enabled := read()
	if enabled.Version != initial.Version+1 {
		t.Fatal("consent did not update config version")
	}
	if err := a.migrateAI(); err != nil {
		t.Fatal(err)
	}
	if !read().AutoReleaseNotes {
		t.Fatal("idempotent migration erased consent")
	}
	// 地址切换即接收方变化，需要重新确认自动外发，不仅确认密钥复用。
	change := map[string]any{"baseUrl": "https://api.owlai.tech", "reuseKey": true, "expectedVersion": enabled.Version}
	w = apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(change))
	if w.Code != 409 || read().BaseURL != enabled.BaseURL {
		t.Fatal("changed auto-send destination without consent")
	}
	change["confirmAutomatic"] = true
	w = apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(change))
	if w.Code != 200 || read().BaseURL != "https://api.owlai.tech/v1" {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"clear": true, "expectedVersion": read().Version}))
	if w.Code != 200 || read().AutoReleaseNotes || read().Enabled {
		t.Fatal("clear retained automated sends")
	}
}

func TestReleaseNotesSettingsCannotEnableWithoutConfiguredService(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, `{"autoReleaseNotes":true,"expectedVersion":0,"confirmAutomatic":true}`)
	if w.Code != 409 {
		t.Fatalf("unconfigured automation accepted: %d %s", w.Code, w.Body.String())
	}
}
