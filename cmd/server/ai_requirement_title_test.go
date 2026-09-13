package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const titlePath = "/api/ai/requirement-title"
const titleDescription = "在客户管理页面新增按负责人筛选客户的功能，并保留当前列表的分页条件。"
const generatedTitle = "客户管理新增负责人筛选功能"

func titleBody(id any) string {
	b := map[string]any{"confirmed": true, "description": titleDescription}
	if id != nil {
		b["requirementId"] = id
	}
	return jsonText(b)
}
func titleResponse(title string) *http.Response {
	return aiResponse(jsonText(map[string]any{"title": title, "insufficient": false}))
}

func TestAITitleCapabilityPermissionsAndNoConfiguration(t *testing.T) {
	a := testApp(t)
	aiMock(a, func(*http.Request) (*http.Response, error) {
		t.Error("unexpected paid call")
		return nil, errors.New("unexpected")
	})
	for _, user := range []string{"u_admin", "u_front", "u_viewer"} {
		w := apiRequest(a, "GET", titlePath, user, projectID, "")
		if w.Code != 200 {
			t.Fatalf("capability %d %s", w.Code, w.Body.String())
		}
		v := jsonMap(t, w)
		if v["configured"] != false || v["enabled"] != false || v["model"] != "gpt-5-mini" || v["canGenerate"] != (user != "u_viewer") || v["maxDescriptionLength"] != float64(100000) || v["maxTitleLength"] != float64(80) {
			t.Fatalf("bad capabilities %v", v)
		}
		w = apiRequest(a, "POST", titlePath, user, projectID, titleBody(nil))
		want := 409
		if user == "u_viewer" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("unconfigured/permission %d want%d %s", w.Code, want, w.Body.String())
		}
	}
	for _, fixture := range []struct {
		user, project string
		id            any
		want          int
	}{{"u_front", insightProjectID, nil, 403}, {"u_admin", projectID, 999999, 404}, {"u_admin", insightProjectID, 1, 404}, {"u_admin", projectID, -1, 422}} {
		w := apiRequest(a, "POST", titlePath, fixture.user, fixture.project, titleBody(fixture.id))
		if w.Code != fixture.want {
			t.Fatalf("scope %v: %d %s", fixture, w.Code, w.Body.String())
		}
	}
	if tableCount(t, a, "ai_title_requests") != 0 {
		t.Fatal("unconfigured/unauthorized calls reserved quota")
	}
}

func TestAITitleProviderOnlySendsDescriptionAndNeverMutatesRequirements(t *testing.T) {
	a := aiApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"title": "原有标题不得被覆盖", "description": "已保存描述不会被发送", "acceptance": "PRIVATE ACCEPTANCE", "remarks": "PRIVATE REMARKS"})
	before, _ := a.get(x.ID)
	w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, `{"model":"gpt-5-mini-2025-08-07"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var calls atomic.Int32
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != aiEndpoint || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+aiTestSecret {
			t.Error("unsafe destination/auth")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 45*time.Second {
			t.Error("no bounded request deadline")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		if body["model"] != "gpt-5-mini-2025-08-07" || body["store"] != false || body["tools"] != nil || body["max_output_tokens"] != float64(1500) {
			t.Errorf("unexpected provider config %v", body)
		}
		format := body["text"].(map[string]any)["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true || format["schema"].(map[string]any)["additionalProperties"] != false {
			t.Error("not strict schema")
		}
		var input map[string]string
		if json.Unmarshal([]byte(body["input"].(string)), &input) != nil || len(input) != 1 || input["description"] != titleDescription {
			t.Errorf("wrong sent input %v", input)
		}
		if strings.Contains(jsonText(body), "PRIVATE") || strings.Contains(jsonText(body), "原有标题") || strings.Contains(jsonText(body), "已保存描述") {
			t.Error("extra business data sent")
		}
		return titleResponse(generatedTitle), nil
	})
	for _, id := range []any{nil, x.ID} {
		w = apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(id))
		if w.Code != 200 || jsonMap(t, w)["title"] != generatedTitle || jsonMap(t, w)["model"] != "gpt-5-mini-2025-08-07" || strings.Contains(w.Body.String(), aiTestSecret) {
			t.Fatalf("preview %d %s", w.Code, w.Body.String())
		}
	}
	after, _ := a.get(x.ID)
	if jsonText(before) != jsonText(after) || calls.Load() != 2 {
		t.Fatal("generation changed requirement or called twice")
	}
	var audit string
	if err := a.db.QueryRow(`SELECT group_concat(after_json) FROM audit_logs WHERE action='ai_requirement_title_generated'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, titleDescription) || strings.Contains(audit, generatedTitle) || strings.Contains(audit, aiTestSecret) {
		t.Fatal("description/title/key stored in audit")
	}
	rows, err := a.db.Query(`SELECT name FROM pragma_table_info('ai_title_requests')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			t.Fatal("invalid schema")
		}
		if strings.Contains(name, "description") || name == "title" || strings.Contains(name, "key") {
			t.Fatalf("sensitive title request column %s", name)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The requirement POST still enforces a real non-empty title.
	w = apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, `{"title":"","description":"新增负责人筛选功能"}`)
	if w.Code != 422 {
		t.Fatalf("empty title creation bypassed: %d %s", w.Code, w.Body.String())
	}
}

func TestAITitleRejectsEmptyMediaAndOversizedInputWithoutProvider(t *testing.T) {
	a := aiApp(t)
	aiMock(a, func(*http.Request) (*http.Response, error) {
		t.Error("invalid input reached provider")
		return nil, errors.New("unexpected")
	})
	for _, description := range []string{"", " \n\t ", "🖼️🎉👍", "https://private.example/image.png", "![截图](https://private.example/screenshot.png)", "<img src='https://private.example/private.png'>", "screenshot.png\n另一个截图.gif", "data:image/png;base64,aGVsbG8=", "1234567890", strings.Repeat("中", 33334), "请生成\x00标题"} {
		w := apiRequest(a, "POST", titlePath, "u_admin", projectID, jsonText(map[string]any{"description": description, "confirmed": true}))
		if w.Code != 422 {
			t.Fatalf("input accepted %q: %d %s", description[:min(len(description), 50)], w.Code, w.Body.String())
		}
	}
	for _, raw := range []string{`{"description":"新增负责人筛选功能"}`, `{"description":"新增负责人筛选功能","confirmed":false}`, `{"description":"新增负责人筛选功能","confirmed":true,"title":"不可由此覆盖"}`, `{"description":"新增负责人筛选功能","confirmed":true} {}`} {
		w := apiRequest(a, "POST", titlePath, "u_admin", projectID, raw)
		if w.Code != 422 {
			t.Fatalf("invalid JSON/confirmation accepted %d %s", w.Code, w.Body.String())
		}
	}
	if tableCount(t, a, "ai_title_requests") != 0 {
		t.Fatal("invalid text consumed quota")
	}
}

func TestAITitleRejectsInvalidRefusedAndUnsafeProviderResponses(t *testing.T) {
	fixtures := []struct {
		name, raw string
		want      int
	}{
		{"missing-flag", `{"title":"新增筛选功能"}`, 502}, {"null-title", `{"title":null,"insufficient":false}`, 502}, {"null-flag", `{"title":"新增筛选功能","insufficient":null}`, 502}, {"duplicate", `{"title":"一","title":"新增筛选功能","insufficient":false}`, 502}, {"extra-key", `{"title":"新增筛选功能","insufficient":false,"secret":"x"}`, 502}, {"extra-json", `{"title":"新增筛选功能","insufficient":false}{}`, 502}, {"wrong-root", `[]`, 502}, {"low-info", `{"title":"","insufficient":true}`, 422},
	}
	for _, title := range []string{"", "短", "新增\n筛选功能", "  新增筛选功能", "新增筛选功能 ", "**新增筛选功能**", "1. 新增筛选功能", "REQ-001 新增筛选功能", "开发中：新增筛选功能", "新增\u2028筛选功能", strings.Repeat("字", 81)} {
		fixtures = append(fixtures, struct {
			name, raw string
			want      int
		}{title, jsonText(map[string]any{"title": strings.ReplaceAll(strings.ReplaceAll(title, "\\n", "\n"), "\\u2028", "\u2028"), "insufficient": false}), 502})
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			a := aiApp(t)
			aiMock(a, func(*http.Request) (*http.Response, error) { return aiResponse(fixture.raw), nil })
			w := apiRequest(a, "POST", titlePath, "u_admin", projectID, titleBody(nil))
			if w.Code != fixture.want || strings.Contains(w.Body.String(), aiTestSecret) {
				t.Fatalf("provider response accepted %d %s", w.Code, w.Body.String())
			}
			var status string
			if err := a.db.QueryRow(`SELECT status FROM ai_title_requests`).Scan(&status); err != nil || status != "failed" {
				t.Fatalf("quota failure not retained %s %v", status, err)
			}
		})
	}
	for _, fixture := range []struct {
		name     string
		response *http.Response
		err      error
		want     int
	}{
		{"http-key", &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(aiTestSecret))}, nil, 502},
		{"network-key", nil, errors.New(aiTestSecret), 502},
		{"redirect", &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://attacker.invalid"}}, Body: io.NopCloser(strings.NewReader(""))}, nil, 502},
		{"incomplete", &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"incomplete","output":[]}`))}, nil, 502},
		{"refusal", &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"private"}]}]}`))}, nil, 422},
		{"oversized", &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 65537)))}, nil, 502},
		{"trailing", &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[]} {}`))}, nil, 502},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			a := aiApp(t)
			aiMock(a, func(*http.Request) (*http.Response, error) { return fixture.response, fixture.err })
			w := apiRequest(a, "POST", titlePath, "u_admin", projectID, titleBody(nil))
			if w.Code != fixture.want || strings.Contains(w.Body.String(), aiTestSecret) {
				t.Fatalf("unsafe provider error %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAITitleRechecksRevocationConfigurationSourceAndExpiry(t *testing.T) {
	for _, fixture := range []struct {
		name, mutation string
		want           int
	}{
		{"config-version", `UPDATE organization_ai_settings SET version=version+1`, 409},
		{"disabled-ai", `UPDATE organization_ai_settings SET enabled=0`, 409},
		{"expired", `UPDATE ai_title_requests SET expires_at='2000-01-01T00:00:00Z'`, 409},
		{"already-failed", `UPDATE ai_title_requests SET status='failed'`, 409},
		{"revoked-project", `DELETE FROM project_members WHERE user_id='u_front'`, 403},
		{"changed-role", `UPDATE project_members SET role='viewer' WHERE user_id='u_front'`, 403},
		{"disabled-user", `UPDATE users SET operation_disabled=1 WHERE id='u_front'`, 403},
		{"changed-source", `UPDATE requirements SET title='其他人修改后的标题' WHERE id=1`, 409},
		{"deleted-source", `DELETE FROM requirements WHERE id=1`, 404},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			a := aiApp(t)
			aiMock(a, func(*http.Request) (*http.Response, error) {
				if _, err := a.db.Exec(fixture.mutation); err != nil {
					t.Error(err)
				}
				return titleResponse(generatedTitle), nil
			})
			w := apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(1))
			if w.Code != fixture.want || strings.Contains(w.Body.String(), generatedTitle) {
				t.Fatalf("stale result returned %d %s", w.Code, w.Body.String())
			}
			var n int
			if err := a.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE action='ai_requirement_title_generated'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("stale result audited as generated %d %v", n, err)
			}
		})
	}
}

func TestAITitleHourlyAndConcurrentReservationsBoundCost(t *testing.T) {
	a := aiApp(t)
	var calls atomic.Int32
	aiMock(a, func(*http.Request) (*http.Response, error) { calls.Add(1); return titleResponse(generatedTitle), nil })
	for i := 0; i < 11; i++ {
		w := apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(nil))
		want := 200
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("hourly request%d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("identical successful inputs were charged again %d", calls.Load())
	}
	// All failed/generating/completed reservations count toward tenant quotas.
	for i := 0; i < 70; i++ {
		if _, err := a.db.Exec(`INSERT INTO ai_title_requests(id,tenant_id,project_id,user_id,settings_version,model,status,created_at,expires_at)VALUES(?,?,?,'another-user',1,'gpt-5-mini','failed',?,?)`, fmt.Sprint(i), tenantID, projectID, orgNow(), orgNow()); err != nil {
			t.Fatal(err)
		}
	}
	w := apiRequest(a, "POST", titlePath, "u_back", projectID, titleBody(nil))
	if w.Code != 429 || calls.Load() != 1 {
		t.Fatalf("tenant quota bypassed %d %s", w.Code, w.Body.String())
	}
	// Independent fixture: a second request by the same user cannot join a
	// currently paid request, but another permitted user can proceed.
	t.Run("concurrent", func(t *testing.T) {
		b := aiApp(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		defer releaseOnce.Do(func() { close(release) })
		var concurrentCalls atomic.Int32
		aiMock(b, func(*http.Request) (*http.Response, error) {
			if concurrentCalls.Add(1) == 1 {
				close(entered)
				<-release
			}
			return titleResponse(generatedTitle), nil
		})
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- apiRequest(b, "POST", titlePath, "u_front", projectID, titleBody(nil)) }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("first request never reached provider")
		}
		w = apiRequest(b, "POST", titlePath, "u_front", projectID, titleBody(nil))
		releaseOnce.Do(func() { close(release) })
		if w.Code != 429 {
			t.Fatalf("concurrent reservation bypassed %d %s", w.Code, w.Body.String())
		}
		select {
		case result := <-done:
			if result.Code != 200 {
				t.Fatalf("reserved request failed %d %s", result.Code, result.Body.String())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request did not finish")
		}
		if concurrentCalls.Load() != 1 {
			t.Fatalf("extra parallel charge %d", concurrentCalls.Load())
		}
	})
}

func TestAITitleCancellationImpersonationAndAuditFailure(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		a := aiApp(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		aiMock(a, func(r *http.Request) (*http.Response, error) { cancel(); return nil, r.Context().Err() })
		b := administrationSessionFixture(t, a, "u_front")
		r := httptest.NewRequest("POST", titlePath, strings.NewReader(titleBody(nil))).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		b.requirementAITitle(w, r)
		if w.Code != 504 || strings.Contains(w.Body.String(), generatedTitle) {
			t.Fatalf("cancel result %d %s", w.Code, w.Body.String())
		}
		var status string
		if a.db.QueryRow(`SELECT status FROM ai_title_requests`).Scan(&status) != nil || status != "failed" {
			t.Fatal("cancelled reservation not failed")
		}
	})
	t.Run("impersonation", func(t *testing.T) {
		a := aiApp(t)
		aiMock(a, func(*http.Request) (*http.Response, error) {
			t.Error("impersonation invoked AI")
			return nil, errors.New("unexpected")
		})
		_, cookie := loginRequest(a, "linxia@devflow.local", seedPassword)
		w := impersonationRequest(a, cookie, "POST", "/api/auth/impersonation", `{"userId":"u_front","reason":"test title impersonation"}`, projectID)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		w = impersonationRequest(a, cookie, "POST", titlePath, titleBody(nil), projectID)
		if w.Code != 403 {
			t.Fatalf("impersonated call %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("audit", func(t *testing.T) {
		a := aiApp(t)
		aiMock(a, func(*http.Request) (*http.Response, error) { return titleResponse(generatedTitle), nil })
		if _, err := a.db.Exec(`CREATE TRIGGER fail_title_audit BEFORE INSERT ON audit_logs WHEN NEW.action='ai_requirement_title_generated' BEGIN SELECT RAISE(ABORT,'private audit error');END`); err != nil {
			t.Fatal(err)
		}
		w := apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(nil))
		if w.Code != 503 || strings.Contains(w.Body.String(), "private audit error") || strings.Contains(w.Body.String(), generatedTitle) {
			t.Fatalf("partial audit response %d %s", w.Code, w.Body.String())
		}
		var status string
		if a.db.QueryRow(`SELECT status FROM ai_title_requests`).Scan(&status) != nil || status != "failed" {
			t.Fatal("audit failure left completed reservation")
		}
	})
}

func TestAITitleTenantConcurrencyExpiryAndEnglishTitle(t *testing.T) {
	a := aiApp(t)
	var calls atomic.Int32
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return titleResponse("Customer list: add owner filtering"), nil
	})
	for i := 0; i < 4; i++ {
		if _, err := a.db.Exec(`INSERT INTO ai_title_requests(id,tenant_id,project_id,user_id,settings_version,model,status,created_at,expires_at)VALUES(?,?,?,?,1,'gpt-5-mini','generating',?,?)`, fmt.Sprint(i), tenantID, projectID, fmt.Sprintf("other-%d", i), orgNow(), time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	w := apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(nil))
	if w.Code != 429 || calls.Load() != 0 {
		t.Fatalf("tenant concurrent ceiling bypassed %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`UPDATE ai_title_requests SET expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", titlePath, "u_front", projectID, `{"description":"Add filtering by customer owner to the customer list while retaining pagination.","confirmed":true}`)
	if w.Code != 200 || jsonMap(t, w)["title"] != "Customer list: add owner filtering" || calls.Load() != 1 {
		t.Fatalf("expired lease blocked English preview %d %s", w.Code, w.Body.String())
	}
	// The common response locale translates system errors, never generated text.
	b := *a
	b.user = "u_front"
	w = httptest.NewRecorder()
	setResponseLocale(w, "en-US")
	r := httptest.NewRequest("POST", titlePath, strings.NewReader(`{"description":"","confirmed":true}`))
	r.Header.Set("Content-Type", "application/json")
	b.requirementAITitle(w, r)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "functional scope") {
		t.Fatalf("title validation not localized %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`DROP TABLE organization_ai_settings`); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	b.requirementAITitle(w, httptest.NewRequest("GET", titlePath, nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "no such table") {
		t.Fatalf("capability database failure pretended unconfigured %d %s", w.Code, w.Body.String())
	}
}
