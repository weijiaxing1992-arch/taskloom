package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func aiReviewJSON() string {
	return `{"summary":"用例覆盖主流程，但需补充异常分支。","issues":[{"severity":"medium","field":"步骤 2","message":"没有验证失败后的恢复状态","suggestion":"增加无权限与服务异常后的可重试预期"}]}`
}

func aiReviewCase(t *testing.T, a *App) (Requirement, TestCase) {
	t.Helper()
	if _, err := a.readTestingSettings(context.Background(), a.db); err != nil {
		t.Fatalf("AI 审查依赖的测试设置不可读取: %v", err)
	}
	requirement := createPeopleRequirement(t, a, map[string]any{"title": "AI 审查关联需求", "description": "支持账号登录并在失败后提示原因"})
	return requirement, createTraceabilityCase(t, a, requirement.ID, "登录主流程", "草稿")
}

func TestAITestCaseReviewScopesProviderInputAndReplaysCurrentVersion(t *testing.T) {
	a := aiApp(t)
	requirement, item := aiReviewCase(t, a)
	var calls atomic.Int32
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.String() != aiEndpoint || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+aiTestSecret {
			t.Error("unsafe AI review endpoint or credential")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["store"] != false || body["tools"] != nil || body["max_output_tokens"] != float64(5000) {
			t.Error("unsafe AI review request configuration")
		}
		format := body["text"].(map[string]any)["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true {
			t.Error("review does not require strict JSON")
		}
		var input map[string]any
		if err := json.Unmarshal([]byte(body["input"].(string)), &input); err != nil {
			t.Fatal(err)
		}
		linked, ok := input["linkedRequirement"].(map[string]any)
		if !ok || linked["id"] != float64(requirement.ID) || (linked["title"] != requirement.Title && linked["title"] != "AI 审查关联需求（已更新）") || len(linked) != 4 {
			t.Fatalf("linked requirement scope leaked or missing: %#v", input)
		}
		caseInput, ok := input["testCase"].(map[string]any)
		if !ok || caseInput["title"] != item.Title || caseInput["requirementId"] != nil || strings.Contains(body["input"].(string), "PRIVATE") {
			t.Fatalf("unexpected case review input: %#v", input)
		}
		return aiResponse(aiReviewJSON()), nil
	})

	capability := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, "")
	if capability.Code != http.StatusOK || jsonMap(t, capability)["canReview"] != true {
		t.Fatalf("review availability: %d %s", capability.Code, capability.Body.String())
	}
	body := jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "logic"})
	first := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, body)
	if first.Code != http.StatusOK || jsonMap(t, first)["replayed"] != false || calls.Load() != 1 {
		t.Fatalf("review failed: %d %s calls=%d", first.Code, first.Body.String(), calls.Load())
	}
	if got, err := a.getTestCase(item.ID); err != nil || got.UpdatedAt != item.UpdatedAt || got.Title != item.Title || got.RequirementID == nil || *got.RequirementID != requirement.ID {
		t.Fatalf("AI review modified the case: %+v %v", got, err)
	}
	second := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, body)
	if second.Code != http.StatusOK || jsonMap(t, second)["replayed"] != true || calls.Load() != 1 {
		t.Fatalf("same-version review was not safely replayed: %d %s calls=%d", second.Code, second.Body.String(), calls.Load())
	}
	var status, result string
	if err := a.db.QueryRow(`SELECT status,result_json FROM ai_test_case_reviews WHERE tenant_id=? AND project_id=? AND case_id=?`, tenantID, projectID, item.ID).Scan(&status, &result); err != nil || status != "completed" || strings.Contains(result, aiTestSecret) {
		t.Fatalf("review reservation was not safely persisted: %q %q %v", status, result, err)
	}
	var auditCount int
	if err := a.db.QueryRow(`SELECT count(*) FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='test_case' AND object_id=? AND action='ai_reviewed'`, tenantID, projectID, fmt.Sprint(item.ID)).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("review audit missing: %d %v", auditCount, err)
	}
	// 关联需求摘要属于审查输入的一部分。即使测试用例自身版本未变，也不能
	// 把旧需求上下文的缓存结果重放给用户。
	if _, err := a.db.Exec(`UPDATE requirements SET title='AI 审查关联需求（已更新）' WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, requirement.ID); err != nil {
		t.Fatal(err)
	}
	third := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, body)
	if third.Code != http.StatusOK || jsonMap(t, third)["replayed"] != false || calls.Load() != 2 {
		t.Fatalf("linked requirement change reused a stale review: %d %s calls=%d", third.Code, third.Body.String(), calls.Load())
	}
}

func TestAITestCaseReviewFailsClosedForInvalidVersionPermissionsAndStaleRules(t *testing.T) {
	a := aiApp(t)
	_, item := aiReviewCase(t, a)
	var calls atomic.Int32
	aiMock(a, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if _, err := a.db.Exec(`UPDATE testing_settings SET version=version+1 WHERE tenant_id=? AND project_id=?`, tenantID, projectID); err != nil {
			t.Fatal(err)
		}
		return aiResponse(aiReviewJSON()), nil
	})
	path := fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID)
	for _, test := range []struct {
		user, project, body string
		want                int
	}{
		{"u_admin", projectID, `{}`, http.StatusUnprocessableEntity},
		{"u_admin", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": "old", "mode": "standard"}), http.StatusConflict},
		{"u_admin", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "invalid"}), http.StatusUnprocessableEntity},
		{"u_viewer", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"}), http.StatusForbidden},
		{"u_admin", insightProjectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"}), http.StatusNotFound},
	} {
		w := apiRequest(a, http.MethodPost, path, test.user, test.project, test.body)
		if w.Code != test.want {
			t.Fatalf("%+v got %d %s", test, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid review called provider")
	}
	viewer := apiRequest(a, http.MethodGet, path, "u_viewer", projectID, "")
	if viewer.Code != http.StatusOK || jsonMap(t, viewer)["canReview"] != false {
		t.Fatalf("viewer capability leaked write access: %d %s", viewer.Code, viewer.Body.String())
	}
	stale := apiRequest(a, http.MethodPost, path, "u_admin", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"}))
	if stale.Code != http.StatusConflict || calls.Load() != 1 || strings.Contains(stale.Body.String(), aiTestSecret) {
		t.Fatalf("settings change during AI review was accepted: %d %s calls=%d", stale.Code, stale.Body.String(), calls.Load())
	}
	var status string
	if err := a.db.QueryRow(`SELECT status FROM ai_test_case_reviews WHERE tenant_id=? AND project_id=? AND case_id=?`, tenantID, projectID, item.ID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("stale completion should not remain generating: %q %v", status, err)
	}
}

func TestAITestCaseReviewRejectsUnsafeProviderOutput(t *testing.T) {
	for _, raw := range []string{
		`{"summary":"ok","issues":[{"severity":"critical","field":"步骤","message":"x","suggestion":"y"}]}`,
		`{"summary":"ok","issues":[],"extra":"x"}`,
		`{"summary":"ok","issues":[]} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			a := aiApp(t)
			_, item := aiReviewCase(t, a)
			aiMock(a, func(*http.Request) (*http.Response, error) { return aiResponse(raw), nil })
			w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"}))
			if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), aiTestSecret) {
				t.Fatalf("unsafe AI output leaked or succeeded: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAITestCaseReviewHonorsHourlyReservationLimit(t *testing.T) {
	a := aiApp(t)
	_, item := aiReviewCase(t, a)
	now := orgNow()
	for index := 0; index < aiTestCaseReviewUserHourlyLimit; index++ {
		if _, err := a.db.Exec(`INSERT INTO ai_test_case_reviews(id,tenant_id,project_id,case_id,user_id,source_hash,mode,settings_version,ai_settings_version,model,status,result_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,'failed','',?,?)`, fmt.Sprintf("rate-%d", index), tenantID, projectID, 10000+index, "u_admin", fmt.Sprintf("hash-%d", index), "standard", 1, 1, "gpt-5-mini", now, now); err != nil {
			t.Fatal(err)
		}
	}
	aiMock(a, func(*http.Request) (*http.Response, error) {
		t.Fatal("rate-limited AI review must not call provider")
		return nil, nil
	})
	w := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/test-cases/%d/ai-review", item.ID), "u_admin", projectID, jsonText(map[string]any{"confirmed": true, "caseUpdatedAt": item.UpdatedAt, "mode": "standard"}))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("hourly AI review limit was bypassed: %d %s", w.Code, w.Body.String())
	}
}
