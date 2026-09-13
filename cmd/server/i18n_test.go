package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func languageRequest(t *testing.T, a *App, method, path, user, project, language, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-TaskLoom-Project", project)
	if language != "" {
		r.Header.Set("Accept-Language", language)
	}
	if user != "" {
		cookie, err := a.issueSession(httptest.NewRecorder(), r, user)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	withJSON(a.scopedAPI()).ServeHTTP(w, r)
	return w
}

func TestAcceptLanguageNegotiation(t *testing.T) {
	for _, test := range []struct{ header, preference, want string }{
		{"", "", "zh-CN"}, {"", "en-US", "en-US"}, {"", "invalid", "zh-CN"},
		{"en-US", "zh-CN", "en-US"}, {"zh-CN", "en-US", "zh-CN"},
		{" EN-us ", "zh-CN", "en-US"}, {"en-GB,en;q=0.8", "zh-CN", "en-US"},
		{"zh-Hans-CN", "en-US", "zh-CN"}, {"fr-FR;q=1,en-US;q=0.8,zh-CN;q=0.5", "zh-CN", "en-US"},
		{"en-US;q=0.4,zh-CN;q=0.9", "en-US", "zh-CN"},
		{"en-US;q=0.5,zh-CN;q=0.5", "zh-CN", "en-US"},
		{"en-US;q=0,zh-CN;q=0.1", "en-US", "zh-CN"},
		{"fr-FR,*;q=0.8", "en-US", "en-US"}, {"*", "", "zh-CN"},
		{"en-US;q=NaN", "zh-CN", "zh-CN"}, {"en-US;q=2", "zh-CN", "zh-CN"},
		{"en-US;q=-1", "zh-CN", "zh-CN"}, {"en-US;q=0.9999", "zh-CN", "zh-CN"},
		{"en-US;q=1;q=0", "zh-CN", "zh-CN"}, {"en_US", "zh-CN", "zh-CN"},
		{"en-US;other=1", "zh-CN", "zh-CN"}, {"en-US;q=broken,zh;q=0.1", "en-US", "zh-CN"},
	} {
		t.Run(test.header+"/"+test.preference, func(t *testing.T) {
			if got := negotiatedLocale(test.header, test.preference); got != test.want {
				t.Fatalf("negotiated %q, want %q", got, test.want)
			}
		})
	}
}

func TestPersonalLocalePersistsWithoutChangingProfile(t *testing.T) {
	a := fileSQLiteTestApp(t)
	viewer := *a
	viewer.user, viewer.project = "u_viewer", insightProjectID
	before, err := viewer.getProfile()
	if err != nil {
		t.Fatal(err)
	}
	var otherLocale string
	if err := a.db.QueryRow(`SELECT locale FROM users WHERE id='u_admin'`).Scan(&otherLocale); err != nil {
		t.Fatal(err)
	}
	// The obsolete project header cannot prevent changing a tenant-wide personal setting.
	w := languageRequest(t, a, "PATCH", "/api/preferences/locale", "u_viewer", "no-longer-accessible", "", `{"locale":"en-US"}`)
	if w.Code != http.StatusOK || w.Header().Get("Content-Language") != "en-US" || jsonMap(t, w)["locale"] != "en-US" {
		t.Fatalf("viewer locale save failed: %d %s", w.Code, w.Body.String())
	}
	after, err := viewer.getProfile()
	if err != nil {
		t.Fatal(err)
	}
	if after.Locale != "en-US" {
		t.Fatalf("locale not persisted: %s", after.Locale)
	}
	after.Locale = before.Locale
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("saving locale changed unrelated profile data: before=%+v after=%+v", before, after)
	}
	var gotOtherLocale string
	if err := a.db.QueryRow(`SELECT locale FROM users WHERE id='u_admin'`).Scan(&gotOtherLocale); err != nil || otherLocale != gotOtherLocale {
		t.Fatalf("another user preference changed: %q -> %q (%v)", otherLocale, gotOtherLocale, err)
	}
	if err := a.migrate(); err != nil {
		t.Fatal(err)
	}
	w = languageRequest(t, a, "GET", "/api/preferences/locale", "u_viewer", insightProjectID, "fr-FR", "")
	if w.Code != 200 || jsonMap(t, w)["locale"] != "en-US" || w.Header().Get("Content-Language") != "en-US" {
		t.Fatalf("preference was lost across migration or unsupported header: %d %s", w.Code, w.Body.String())
	}
	w = languageRequest(t, a, "GET", "/api/session", "u_viewer", insightProjectID, "zh-CN", "")
	payload := jsonMap(t, w)
	if w.Code != 200 || payload["user"].(map[string]any)["locale"] != "en-US" || w.Header().Get("Content-Language") != "zh-CN" || len(payload["supportedLocales"].([]any)) != 2 {
		t.Fatalf("session did not distinguish saved and request languages: %d %s", w.Code, w.Body.String())
	}
	if payload["user"].(map[string]any)["name"] != before.Name {
		t.Fatalf("user-authored name was translated: %s", w.Body.String())
	}
	for _, body := range []string{`{}`, `{"locale":"fr-FR"}`, `{"locale":null}`, `{"locale":1}`, `{"locale":"en-US","name":"覆盖姓名"}`, `{"locale":"en-US","userId":"u_admin"}`, `{"locale":"en-US"} {}`, `{"locale":"` + strings.Repeat("a", 5000) + `"}`} {
		w = languageRequest(t, a, "PATCH", "/api/preferences/locale", "u_viewer", insightProjectID, "en-US", body)
		if w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid locale input accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w = languageRequest(t, a, "PATCH", "/api/preferences/locale", "", projectID, "en-US", `{"locale":"en-US"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous locale write accepted: %d", w.Code)
	}
	w = languageRequest(t, a, "POST", "/api/requirements", "u_viewer", insightProjectID, "en-US", `{"title":"不允许写入"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("personal preference authorization allowed business writes: %d", w.Code)
	}
}

func TestLocaleHeadersAndErrors(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE users SET locale='en-US' WHERE id='u_pm'`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path, user, header, body, locale, message string
		status                                            int
	}{
		{"GET", "/api/session", "", "en-US", "", "en-US", "Your session has expired. Please sign in again.", 401},
		{"GET", "/api/requirements/not-an-id", "u_pm", "", "", "en-US", "The requirement ID is invalid.", 400},
		{"GET", "/api/requirements/not-an-id", "u_pm", "zh-CN", "", "zh-CN", "需求编号不正确", 400},
		{"POST", "/api/requirements", "u_pm", "en-US", `{"title":""}`, "en-US", "Title is required.", 422},
		{"OPTIONS", "/api/requirements", "", "en-US", "", "en-US", "", 204},
		{"GET", "/api/health", "", "en-US", "", "en-US", "", 200},
	} {
		w := languageRequest(t, a, test.method, test.path, test.user, projectID, test.header, test.body)
		if w.Code != test.status || w.Header().Get("Content-Language") != test.locale {
			t.Fatalf("%s: status=%d locale=%q body=%s", test.path, w.Code, w.Header().Get("Content-Language"), w.Body.String())
		}
		vary := strings.Join(w.Header().Values("Vary"), ",")
		if !strings.Contains(vary, "Accept-Language") || !strings.Contains(vary, "Cookie") {
			t.Fatalf("missing language/preference cache variation: %q", vary)
		}
		if test.message != "" && jsonMap(t, w)["error"].(map[string]any)["message"] != test.message {
			t.Fatalf("message not localized: %s", w.Body.String())
		}
	}
	for _, locale := range supportedLocales {
		w := httptest.NewRecorder()
		setResponseLocale(w, locale)
		fail(w, 500, "db_error", "SQLITE_BUSY: SELECT password_hash FROM /private/sensitive.db")
		if strings.Contains(w.Body.String(), "SQLITE") || strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "/private") {
			t.Fatalf("database internals exposed: %s", w.Body.String())
		}
	}
	if got := localizedError("en-US", 422, "validation_error", "前端业务难度 为必填字段"); got != "前端业务难度 is required." {
		t.Fatalf("custom field name not preserved: %q", got)
	}
}

func TestEnglishAPILeavesCanonicalRequirementDataUnchanged(t *testing.T) {
	a := testApp(t)
	w := languageRequest(t, a, "POST", "/api/requirements", "u_admin", projectID, "en-US", `{"title":"中文需求标题 / bilingual","description":"不要翻译这段业务描述","category":"未分类","status":"草稿","remarks":"保留用户备注"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := jsonMap(t, w)
	id := int64(created["id"].(float64))
	for _, language := range supportedLocales {
		w = languageRequest(t, a, "GET", fmt.Sprintf("/api/requirements/%d", id), "u_admin", projectID, language, "")
		got := jsonMap(t, w)
		for _, key := range []string{"title", "description", "category", "status", "remarks"} {
			if got[key] != created[key] {
				t.Fatalf("business field %s translated: %v -> %v", key, created[key], got[key])
			}
		}
	}
	var status, title string
	if err := a.db.QueryRow(`SELECT status,title FROM requirements WHERE id=?`, id).Scan(&status, &title); err != nil || status != "草稿" || title != "中文需求标题 / bilingual" {
		t.Fatalf("canonical storage changed: status=%s title=%s err=%v", status, title, err)
	}
}

func TestSearchSnippetKeepsPlaceholderLikeBusinessText(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"", ""},
		{" \n\t ", ""},
		{"暂无摘要", "暂无摘要"},
		{"产品填写的中文摘要", "产品填写的中文摘要"},
	} {
		if got := excerpt(test.input, ""); got != test.want {
			t.Fatalf("excerpt(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestSystemNotificationsTranslateWithoutTouchingUserContentOrStorage(t *testing.T) {
	a := testApp(t)
	for _, notice := range []struct{ event, title, body string }{
		{"requirement.mentioned", "你在评论中被提及", "@林夏 请看这个需求：迭代状态已变更为 规划中"},
		{"test.failed", "测试执行失败", "用户输入的失败备注：已关闭"},
		{"sprint.completed", "迭代已完成", "完成迭代，将 2 个需求、3 个缺陷迁移到 用户的迭代中文名称"},
		{"defect.status_changed", "缺陷已就绪，等待验证", "BUG-0001 已流转至待验证"},
	} {
		if err := a.recordWorkflowEvent(a.db, "u_pm", notice.event, "requirement", 1, notice.title, notice.body, "i18n:"+notice.event); err != nil {
			t.Fatal(err)
		}
	}
	w := languageRequest(t, a, "GET", "/api/notifications", "u_pm", projectID, "en-US", "")
	if w.Code != 200 {
		t.Fatalf("notification read: %d %s", w.Code, w.Body.String())
	}
	expectations := map[string][2]string{
		"requirement.mentioned": {"You were mentioned in a comment", "@林夏 请看这个需求：迭代状态已变更为 规划中"},
		"test.failed":           {"Test execution failed", "用户输入的失败备注：已关闭"},
		"sprint.completed":      {"Sprint completed", "Sprint completed: moved 2 requirements and 3 defects to 用户的迭代中文名称"},
		"defect.status_changed": {"Defect ready for verification", "BUG-0001 moved to Ready for verification"},
	}
	seen := map[string]bool{}
	for _, value := range jsonMap(t, w)["items"].([]any) {
		item := value.(map[string]any)
		event := item["eventType"].(string)
		if wanted, ok := expectations[event]; ok {
			if item["title"] != wanted[0] || item["body"] != wanted[1] {
				t.Fatalf("notification copy: got=%v want=%v", item, wanted)
			}
			seen[event] = true
		}
	}
	if len(seen) != len(expectations) {
		t.Fatalf("missing notifications: %v", seen)
	}
	w = languageRequest(t, a, "GET", "/api/notifications/outbox", "u_admin", projectID, "en-US", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Defect ready for verification") || !strings.Contains(w.Body.String(), "用户输入的失败备注") {
		t.Fatalf("outbox translation/preservation failed: %d %s", w.Code, w.Body.String())
	}
	var title, body, payload string
	if err := a.db.QueryRow(`SELECT title,body FROM user_notifications WHERE dedupe_key='i18n:defect.status_changed:u_pm'`).Scan(&title, &body); err != nil || title != "缺陷已就绪，等待验证" || body != "BUG-0001 已流转至待验证" {
		t.Fatalf("read rewrote persisted notification: %q %q %v", title, body, err)
	}
	if err := a.db.QueryRow(`SELECT payload FROM notification_outbox WHERE dedupe_key='i18n:defect.status_changed'`).Scan(&payload); err != nil || !strings.Contains(payload, "缺陷已就绪") {
		t.Fatalf("read rewrote persisted delivery: %q %v", payload, err)
	}
	for _, event := range []string{"requirement.mentioned", "test.failed", "custom.user_event"} {
		title, body := localizedNotification("en-US", event, "用户自己写的标题", "BUG-0001 已流转至待验证")
		if title != "用户自己写的标题" || body != "BUG-0001 已流转至待验证" {
			t.Fatalf("unrecognized/user content changed for %s: %s %s", event, title, body)
		}
	}
}

func TestLocaleCatalogCoversStaticClientErrors(t *testing.T) {
	if got := localizedError("en-US", 422, "validation_error", "当前状态“修复中”不能流转到“已关闭”"); strings.Contains(got, "修复中") || strings.Contains(got, "已关闭") {
		t.Fatalf("workflow state labels not translated: %s", got)
	}
	if got := localizedError("en-US", 422, "custom_field_invalid", "规划中 为必填字段"); !strings.Contains(got, "规划中") {
		t.Fatalf("custom field name was translated: %s", got)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(source, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "fail" {
				return true
			}
			code, codeOK := call.Args[2].(*ast.BasicLit)
			message, messageOK := call.Args[3].(*ast.BasicLit)
			if !codeOK || !messageOK {
				return true
			}
			codeValue, _ := strconv.Unquote(code.Value)
			if codeValue == "database_unavailable" || codeValue == "db_error" {
				return true // These deliberately have a single safe localized message.
			}
			value, _ := strconv.Unquote(message.Value)
			if _, ok := englishErrors[value]; !ok {
				t.Errorf("%s: missing English server error: %q", path, value)
			}
			return true
		})
	}
}

func TestPersonalLocaleResponseIsJSON(t *testing.T) {
	a := testApp(t)
	w := languageRequest(t, a, "GET", "/api/preferences/locale", "u_admin", projectID, "en-US", "")
	var response struct {
		Locale           string   `json:"locale"`
		SupportedLocales []string `json:"supportedLocales"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Locale != defaultLocale || !reflect.DeepEqual(response.SupportedLocales, supportedLocales) {
		t.Fatalf("locale API contract: %d %s", w.Code, w.Body.String())
	}
}
