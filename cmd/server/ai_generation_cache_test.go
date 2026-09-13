package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAIPreviewCacheBoundsExpiryAndIsolation(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	cache := newAIGenerationCache(2, 8, time.Minute)
	cache.now = func() time.Time { return now }
	a := &App{db: &sql.DB{}, user: "reader-one", project: "project-one"}
	s := aiSettings{Version: 2, Model: "model", BaseURL: "https://provider.example/v1", Encrypted: []byte("encrypted-credential")}
	key := a.aiPreviewCacheKey("title", s, "source-one", "input-one")
	cache.put(key, "private")
	if value, ok := cache.get(key); !ok || value != "private" {
		t.Fatal("same scope cannot recover its preview")
	}
	variants := []aiGenerationCacheKey{}
	for _, field := range []string{"user", "project", "database"} {
		other := *a
		switch field {
		case "user":
			other.user = "reader-two"
		case "project":
			other.project = "project-two"
		case "database":
			other.db = &sql.DB{}
		}
		variants = append(variants, other.aiPreviewCacheKey("title", s, "source-one", "input-one"))
	}
	for _, field := range []string{"version", "model", "endpoint", "credential"} {
		other := s
		switch field {
		case "version":
			other.Version++
		case "model":
			other.Model = "other-model"
		case "endpoint":
			other.BaseURL = "https://other.example/v1"
		case "credential":
			other.Encrypted = []byte("different-encrypted-credential")
		}
		variants = append(variants, a.aiPreviewCacheKey("title", other, "source-one", "input-one"))
	}
	variants = append(variants,
		a.aiPreviewCacheKey("refinement", s, "source-one", "input-one"),
		a.aiPreviewCacheKey("title", s, "source-two", "input-one"),
		a.aiPreviewCacheKey("title", s, "source-one", "input-two"))
	for _, variant := range variants {
		if _, ok := cache.get(variant); ok {
			t.Fatal("preview escaped its identity, operation, source or configuration scope")
		}
	}
	now = now.Add(30 * time.Second)
	cache.get(key)
	now = now.Add(30 * time.Second)
	if _, ok := cache.get(key); ok || len(cache.entries) != 0 || cache.bytes != 0 {
		t.Fatal("cache hits extended expiry or expired private content remained in the map")
	}
	cache.put(key, "123456789")
	if len(cache.entries) != 0 {
		t.Fatal("oversize result cached")
	}
	cache.put(key, "12345")
	now = now.Add(time.Second)
	cache.put(variants[0], "6789")
	if _, ok := cache.get(key); ok || cache.bytes != 4 {
		t.Fatal("byte budget failed to evict oldest result")
	}
	cache.put(variants[1], "x")
	cache.put(variants[2], "y")
	if len(cache.entries) != 2 || cache.bytes > 8 {
		t.Fatal("entry budget exceeded")
	}
}

func TestAITextReusesOnlySuccessfulCurrentAuthorizedPreviews(t *testing.T) {
	for _, path := range []string{titlePath, "/api/ai/requirement-refine"} {
		t.Run(path, func(t *testing.T) {
			a := aiApp(t)
			calls := 0
			aiMock(a, func(*http.Request) (*http.Response, error) {
				calls++
				if path == titlePath {
					return titleResponse(generatedTitle), nil
				}
				return aiResponse(refinementSample), nil
			})
			request := func(user, body string, want int, reused bool) {
				t.Helper()
				w := apiRequest(a, "POST", path, user, projectID, body)
				if w.Code != want || want == 200 && jsonMap(t, w)["reused"] != reused {
					t.Fatalf("request: %d %s", w.Code, w.Body.String())
				}
			}
			request("u_front", titleBody(1), 200, false)
			request("u_front", titleBody(1), 200, true)
			if calls != 1 || tableCount(t, a, "ai_title_requests") != 2 {
				t.Fatal("cache repeated a paid call or skipped request accounting")
			}
			request("u_admin", titleBody(1), 200, false)
			request("u_front", strings.Replace(titleBody(1), titleDescription, titleDescription+"支持清空筛选条件。", 1), 200, false)
			if _, err := a.db.Exec(`UPDATE requirements SET title='保存后的新需求范围' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			request("u_front", titleBody(1), 200, false)
			if _, err := a.db.Exec(`UPDATE organization_ai_settings SET version=version+1`); err != nil {
				t.Fatal(err)
			}
			request("u_front", titleBody(1), 200, false)
			if calls != 5 {
				t.Fatalf("input, source, configuration or user changes reused another result: %d", calls)
			}
			if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='u_front'`); err != nil {
				t.Fatal(err)
			}
			request("u_front", titleBody(1), 403, false)
			if calls != 5 {
				t.Fatal("revoked user reached provider")
			}
		})
	}
}

func TestAIPreviewFailureRetryDoesNotCacheFailures(t *testing.T) {
	for _, kind := range []string{"title", "refinement", "test-cases"} {
		t.Run(kind, func(t *testing.T) {
			a := aiApp(t)
			path, body, success := titlePath, titleBody(nil), 200
			if kind == "refinement" {
				path = "/api/ai/requirement-refine"
			} else if kind == "test-cases" {
				x, err := a.get(1)
				if err != nil {
					t.Fatal(err)
				}
				path, body, success = "/api/requirements/1/ai-test-cases", jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt}), 201
			}
			calls := 0
			aiMock(a, func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("temporary provider failure")
				}
				switch kind {
				case "refinement":
					return aiResponse(refinementSample), nil
				case "test-cases":
					return aiResponse(aiExampleJSON()), nil
				default:
					return titleResponse(generatedTitle), nil
				}
			})
			for i, want := range []int{502, success, success} {
				w := apiRequest(a, "POST", path, "u_admin", projectID, body)
				if w.Code != want || i > 0 && jsonMap(t, w)["reused"] != (i == 2) {
					t.Fatalf("retry %d: %d %s", i, w.Code, w.Body.String())
				}
			}
			if calls != 2 {
				t.Fatalf("failure was cached or successful retry repeated: %d", calls)
			}
		})
	}
}

func TestAICasesCacheOptionsSourceAndRateLimit(t *testing.T) {
	a := aiApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	calls := 0
	aiMock(a, func(*http.Request) (*http.Response, error) { calls++; return aiResponse(aiExampleJSON()), nil })
	path := fmt.Sprintf("/api/requirements/%d/ai-test-cases", x.ID)
	request := func(focus []string, count int, extra string, want int, reused bool) map[string]any {
		t.Helper()
		body := jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt, "focus": focus, "count": count, "extraInstructions": extra})
		w := apiRequest(a, "POST", path, "u_admin", projectID, body)
		if w.Code != want || want == 201 && jsonMap(t, w)["reused"] != reused {
			t.Fatalf("cases: %d %s", w.Code, w.Body.String())
		}
		return jsonMap(t, w)
	}
	first := request([]string{"normal", "failure"}, 5, "", 201, false)
	second := request([]string{"failure", "normal"}, 5, "", 201, true)
	if calls != 1 || first["draftId"] == second["draftId"] {
		t.Fatal("equivalent focus order repeated a paid call or reused another import reservation")
	}
	request([]string{"normal", "failure"}, 4, "", 201, false)
	request([]string{"normal", "failure"}, 4, "补充异常恢复", 201, false)
	if _, err := a.db.Exec(`UPDATE requirements SET acceptance='新验收要求' WHERE id=?`, x.ID); err != nil {
		t.Fatal(err)
	}
	request([]string{"normal", "failure"}, 4, "补充异常恢复", 201, false)
	request([]string{"normal", "failure"}, 4, "补充异常恢复", 429, false)
	if calls != 4 || tableCount(t, a, "ai_test_case_drafts") != 5 {
		t.Fatalf("cache invalidation or quota accounting failed: calls=%d", calls)
	}
}

func TestAIQualityInstructionsAndDistinctTestScenarios(t *testing.T) {
	a := aiApp(t)
	kind := "title"
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		instructions, _ := payload["instructions"].(string)
		for _, constraint := range []string{"untrusted", "待确认", "screenshot", "test evidence", "Preserve explicit exclusions", "Remove repeated ideas"} {
			if !strings.Contains(instructions, constraint) {
				t.Errorf("%s omitted quality constraint %q", kind, constraint)
			}
		}
		if kind != "title" && !strings.Contains(instructions, "observable expected result") {
			t.Errorf("%s does not request verifiable expected outcomes", kind)
		}
		if payload["tools"] != nil || payload["store"] != false {
			t.Fatal("quality prompt broadened external authority or retention")
		}
		switch kind {
		case "refinement":
			return aiResponse(refinementSample), nil
		case "test-cases":
			// A reworded title does not justify importing the same scenario twice.
			var result map[string]any
			if err := json.Unmarshal([]byte(aiExampleJSON()), &result); err != nil {
				t.Fatal(err)
			}
			items := result["cases"].([]any)
			duplicate := map[string]any{}
			for key, value := range items[0].(map[string]any) {
				duplicate[key] = value
			}
			duplicate["title"] = "换一种说法的正常提交"
			result["cases"] = []any{items[0], duplicate, items[1]}
			return aiResponse(jsonText(result)), nil
		default:
			return titleResponse(generatedTitle), nil
		}
	})
	for _, operation := range []string{"title", "refinement", "test-cases"} {
		kind = operation
		if kind != "test-cases" {
			if _, err := a.generateAIRequirementText(context.Background(), aiTestSecret, "gpt-5-mini", aiDefaultBaseURL, titleDescription, kind == "refinement"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		cases, err := a.generateAITestCases(context.Background(), aiTestSecret, "gpt-5-mini", Requirement{Title: "新增筛选功能"})
		if err != nil || len(cases) != 2 || cases[0].Index != 0 || cases[1].Index != 1 || cases[1].Title != "边界校验" {
			t.Fatalf("duplicate scenario retained or import indexes lost: %+v %v", cases, err)
		}
	}
}

func TestAICaseCachedPreviewsStillRequireCurrentIdentityAndSettings(t *testing.T) {
	a := aiApp(t)
	x := createPeopleRequirement(t, a, map[string]any{})
	calls := 0
	aiMock(a, func(*http.Request) (*http.Response, error) { calls++; return aiResponse(aiExampleJSON()), nil })
	path := fmt.Sprintf("/api/requirements/%d/ai-test-cases", x.ID)
	body := jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt})
	request := func(user, project string, status int, reused bool) {
		t.Helper()
		w := apiRequest(a, "POST", path, user, project, body)
		if w.Code != status || status == 201 && jsonMap(t, w)["reused"] != reused {
			t.Fatalf("scope/settings check: %d %s", w.Code, w.Body.String())
		}
		if status != 201 && strings.Contains(w.Body.String(), "正常提交") {
			t.Fatal("rejected request exposed a private preview")
		}
	}
	request("u_front", projectID, 201, false)
	request("u_front", projectID, 201, true)
	request("u_admin", projectID, 201, false)
	request("u_admin", insightProjectID, 404, false)
	if _, err := a.db.Exec(`UPDATE organization_ai_settings SET version=version+1`); err != nil {
		t.Fatal(err)
	}
	request("u_front", projectID, 201, false)
	if calls != 3 {
		t.Fatalf("configuration or identity isolation failed: %d", calls)
	}
	if _, err := a.db.Exec(`UPDATE project_members SET role='viewer' WHERE user_id='u_front'`); err != nil {
		t.Fatal(err)
	}
	request("u_front", projectID, 403, false)
	if _, err := a.db.Exec(`UPDATE organization_ai_settings SET enabled=0`); err != nil {
		t.Fatal(err)
	}
	request("u_admin", projectID, 409, false)
	if calls != 3 {
		t.Fatal("revoked access or disabled AI made a provider request")
	}
}

func TestAITextAuditFailureDoesNotPublishCacheEntry(t *testing.T) {
	a := aiApp(t)
	calls := 0
	aiMock(a, func(*http.Request) (*http.Response, error) { calls++; return titleResponse(generatedTitle), nil })
	if _, err := a.db.Exec(`CREATE TRIGGER fail_cached_title_audit BEFORE INSERT ON audit_logs WHEN NEW.action='ai_requirement_title_generated' BEGIN SELECT RAISE(ABORT,'test audit failure');END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(nil))
	if w.Code != 503 {
		t.Fatalf("failed audit returned success: %d %s", w.Code, w.Body.String())
	}
	if _, err := a.db.Exec(`DROP TRIGGER fail_cached_title_audit`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "POST", titlePath, "u_front", projectID, titleBody(nil))
	if w.Code != 200 || jsonMap(t, w)["reused"] != false || calls != 2 {
		t.Fatalf("uncommitted generation was cached: %d %s calls=%d", w.Code, w.Body.String(), calls)
	}
}

func TestAIExplicitRegenerationReplacesCacheAndRequiresBoolean(t *testing.T) {
	for _, kind := range []string{"title", "refinement", "test-cases"} {
		t.Run(kind, func(t *testing.T) {
			a := aiApp(t)
			path, base, success, resultField := titlePath, titleBody(nil), 200, "title"
			if kind == "refinement" {
				path, resultField = "/api/ai/requirement-refine", "preview"
			} else if kind == "test-cases" {
				x, err := a.get(1)
				if err != nil {
					t.Fatal(err)
				}
				path, base, success, resultField = "/api/requirements/1/ai-test-cases", jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt}), 201, "cases"
			}
			calls := 0
			aiMock(a, func(*http.Request) (*http.Response, error) {
				calls++
				switch kind {
				case "refinement":
					return aiResponse(strings.Replace(refinementSample, "已有背景", fmt.Sprintf("已有背景版本%d", calls), 1)), nil
				case "test-cases":
					return aiResponse(strings.Replace(aiExampleJSON(), "正常提交", fmt.Sprintf("正常提交版本%d", calls), 1)), nil
				default:
					return titleResponse(fmt.Sprintf("客户筛选功能版本%d", calls)), nil
				}
			})
			var previous string
			for i, raw := range []string{"", "false", "true", ""} {
				body := base
				if raw != "" {
					body = strings.TrimSuffix(base, "}") + `,"forceNew":` + raw + `}`
				}
				w := apiRequest(a, "POST", path, "u_front", projectID, body)
				if w.Code != success || jsonMap(t, w)["reused"] != (i == 1 || i == 3) {
					t.Fatalf("regeneration %d: %d %s", i, w.Code, w.Body.String())
				}
				result := jsonText(jsonMap(t, w)[resultField])
				if i == 2 && result == previous || (i == 1 || i == 3) && result != previous {
					t.Fatal("explicit regeneration failed to replace the result recovered by subsequent retries")
				}
				previous = result
			}
			if calls != 2 {
				t.Fatalf("explicit regeneration expected exactly one new provider call: %d", calls)
			}
			for _, raw := range []string{`null`, `"true"`, `1`, `{}`, `[]`} {
				body := strings.TrimSuffix(base, "}") + `,"forceNew":` + raw + `}`
				w := apiRequest(a, "POST", path, "u_front", projectID, body)
				if w.Code != 422 || calls != 2 {
					t.Fatalf("non-boolean force option accepted: %s %d", raw, w.Code)
				}
			}
			body := strings.TrimSuffix(base, "}") + `,"forceNew":true}`
			w := apiRequest(a, "POST", path, "u_viewer", projectID, body)
			if w.Code != 403 || calls != 2 {
				t.Fatal("force regeneration bypassed permissions")
			}
		})
	}
}

func TestAIForcedRegenerationRetainsOriginalHourlyQuotas(t *testing.T) {
	for _, kind := range []string{"title", "refinement", "test-cases"} {
		t.Run(kind, func(t *testing.T) {
			a := aiApp(t)
			path, body, success, limit, table := titlePath, titleBody(nil), 200, 10, "ai_title_requests"
			if kind == "refinement" {
				path = "/api/ai/requirement-refine"
			} else if kind == "test-cases" {
				x, err := a.get(1)
				if err != nil {
					t.Fatal(err)
				}
				path, body, success, limit, table = "/api/requirements/1/ai-test-cases", jsonText(map[string]any{"confirmed": true, "requirementUpdatedAt": x.UpdatedAt}), 201, 5, "ai_test_case_drafts"
			}
			body = strings.TrimSuffix(body, "}") + `,"forceNew":true}`
			calls := 0
			aiMock(a, func(*http.Request) (*http.Response, error) {
				calls++
				switch kind {
				case "refinement":
					return aiResponse(refinementSample), nil
				case "test-cases":
					return aiResponse(aiExampleJSON()), nil
				default:
					return titleResponse(generatedTitle), nil
				}
			})
			for i := 0; i < limit; i++ {
				w := apiRequest(a, "POST", path, "u_front", projectID, body)
				if w.Code != success || jsonMap(t, w)["reused"] != false {
					t.Fatalf("explicit generation %d: %d %s", i, w.Code, w.Body.String())
				}
			}
			w := apiRequest(a, "POST", path, "u_front", projectID, body)
			if w.Code != 429 || calls != limit || tableCount(t, a, table) != limit {
				t.Fatalf("forceNew bypassed hourly quota: status=%d calls=%d limit=%d", w.Code, calls, limit)
			}
		})
	}
}
