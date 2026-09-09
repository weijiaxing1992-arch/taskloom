package main

import "testing"

func TestRequirementTemplatePlanning(t *testing.T) {
	a := testApp(t)
	path := "/api/requirement-templates/" + draftTestID(90)
	body := `{"name":"职能模板","description":"正文","ownerUserIds":["u_pm"],"testerUserIds":["u_qa"],"roleWeights":{"frontend":{"userIds":["u_front"],"value":50},"ui":{"value":0}},"baseVersion":0}`
	w := apiRequest(a, "PUT", path, "u_pm", projectID, body)
	if w.Code != 200 {
		t.Fatalf("save planning: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_pm", projectID, "")
	item := jsonMap(t, w)["template"].(map[string]any)
	if item["ownerUserIds"].([]any)[0] != "u_pm" || item["testerUserIds"].([]any)[0] != "u_qa" {
		t.Fatal(item)
	}
	if item["roleWeights"].(map[string]any)["frontend"].(map[string]any)["value"] != float64(50) {
		t.Fatal(item)
	}
	for _, fragment := range []string{`"ownerUserIds":["u_front"]`, `"testerUserIds":["u_front"]`, `"roleWeights":{"frontend":{"userIds":["u_pm"]}}`, `"roleWeights":{"frontend":{"value":-1}}`, `"roleWeights":{"unknown":{"value":1}}`, `"ownerUserIds":["missing"]`} {
		w = apiRequest(a, "PUT", path, "u_pm", projectID, `{"name":"错误","description":"正文","baseVersion":1,`+fragment+`}`)
		if w.Code != 422 {
			t.Fatalf("invalid planning accepted: %s %d %s", fragment, w.Code, w.Body.String())
		}
	}
	if err := a.migrateRequirementTemplates(); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalRequirementTemplates(t *testing.T) {
	a := testApp(t)
	id := draftTestID(88)
	path := "/api/requirement-templates/" + id
	body := `{"name":"验收模板","description":"## 功能说明\n\n待补充","acceptance":"验证步骤","remarks":"边界条件","baseVersion":0}`
	w := apiRequest(a, "PUT", path, "u_pm", projectID, body)
	if w.Code != 200 {
		t.Fatalf("product create: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", "/api/requirement-templates", "u_pm", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 1 {
		t.Fatalf("own list: %s", w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_admin", projectID, "")
	if w.Code != 404 {
		t.Fatalf("administrator must not read another user's template: %d", w.Code)
	}
	w = apiRequest(a, "PUT", path, "u_front", projectID, body)
	if w.Code != 403 {
		t.Fatalf("non-product write: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PUT", path, "u_pm", projectID, body)
	if w.Code != 409 {
		t.Fatalf("stale overwrite: %d", w.Code)
	}
	w = apiRequest(a, "DELETE", path, "u_pm", projectID, `{"baseVersion":0}`)
	if w.Code != 409 {
		t.Fatalf("stale delete: %d", w.Code)
	}
	w = apiRequest(a, "PUT", path, "u_pm", projectID, `{"name":"更新","description":"新内容","baseVersion":1}`)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "DELETE", path, "u_pm", projectID, `{"baseVersion":2}`)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "GET", path, "u_pm", projectID, "")
	if w.Code != 404 {
		t.Fatalf("deleted: %d", w.Code)
	}
}
func TestRequirementTemplateValidationAndNoBusinessSideEffects(t *testing.T) {
	a := testApp(t)
	var before, after int
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&before)
	path := "/api/requirement-templates/" + draftTestID(89)
	for _, body := range []string{`{"name":"","description":"text"}`, `{"name":"模板"}`, `{"name":"模板","description":"text","ownerUserId":"u_admin"}`} {
		w := apiRequest(a, "PUT", path, "u_pm", projectID, body)
		if w.Code < 400 {
			t.Fatalf("invalid accepted: %s", body)
		}
	}
	w := apiRequest(a, "PUT", path, "u_pm", projectID, `{"name":"我的模板","description":"@用户只是模板文本","baseVersion":0}`)
	if w.Code != 200 {
		t.Fatalf("create: %s", w.Body.String())
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM user_notifications`).Scan(&after)
	if before != after {
		t.Fatal("saving a template sent notifications")
	}
	if _, err := a.db.Exec(`UPDATE project_members SET role='backend' WHERE user_id='u_pm' AND project_id=?`, projectID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "DELETE", path, "u_pm", projectID, `{"baseVersion":1}`)
	if w.Code != 403 {
		t.Fatalf("revoked product role can delete: %d", w.Code)
	}
}
