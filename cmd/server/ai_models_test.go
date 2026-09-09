package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// 使用模拟上游检查每个型号的实际请求，不使用客户密钥，也不产生模型费用。
func TestLatestAIModelInterfaces(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-cyber"} {
		t.Run(model, func(t *testing.T) {
			a := aiApp(t)
			w := apiRequest(a, "PATCH", "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"model": model}))
			if w.Code != 200 {
				t.Fatalf("save model: %d %s", w.Code, w.Body.String())
			}
			calls := 0
			aiMock(a, func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if r.URL.String() != aiEndpoint || body["model"] != model || body["store"] != false || body["tools"] != nil {
					t.Fatal("unsafe request", body["model"])
				}
				if model == "gpt-5.6-cyber" {
					if body["reasoning"] != nil {
						t.Fatal("unverified cyber effort")
					}
				} else if body["reasoning"].(map[string]any)["effort"] != "low" {
					t.Fatal("unexpected effort")
				}
				if body["temperature"] != nil {
					t.Fatal("unexpected sampling parameter")
				}
				if calls == 1 {
					return titleResponse("新增客户筛选功能"), nil
				}
				if calls == 3 {
					return aiResponse(aiReviewJSON()), nil
				}
				return aiResponse(aiExampleJSON()), nil
			})
			if _, err := a.generateAIRequirementTitle(context.Background(), aiTestSecret, model, titleDescription); err != nil {
				t.Fatal(err)
			}
			if _, err := a.generateAITestCases(context.Background(), aiTestSecret, model, Requirement{Title: "筛选", Description: titleDescription}); err != nil {
				t.Fatal(err)
			}
			if _, err := a.reviewAITestCase(context.Background(), aiTestSecret, model, aiTestCaseReviewInput{Case: TestCase{Title: "测试"}, Mode: "standard"}); err != nil {
				t.Fatal(err)
			}
			if calls != 3 {
				t.Fatal(calls)
			}
		})
	}
}
