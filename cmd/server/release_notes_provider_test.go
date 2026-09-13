package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func releaseProviderSource(count int) releaseNoteSource {
	source := releaseNoteSource{ProjectID: projectID, SprintID: 88, VersionName: "Release 2", ReleaseDate: "2026-09-10T08:00:00Z", Requirements: []releaseNoteRequirement{}}
	for i := 1; i <= count; i++ {
		source.Requirements = append(source.Requirements, releaseNoteRequirement{ID: int64(i), Code: fmt.Sprintf("REL-%d", i), Title: "新增客户筛选", Description: "支持按负责人筛选客户。", Acceptance: "筛选保留分页条件。", Images: []releaseNoteImage{}})
	}
	return source
}
func releaseProviderInput(t *testing.T, r *http.Request) (map[string]any, releaseNoteSource) {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	var input struct {
		PromptVersion string                   `json:"promptVersion"`
		Categories    []string                 `json:"categories"`
		Requirements  []releaseNoteRequirement `json:"requirements"`
	}
	if json.Unmarshal([]byte(payload["input"].(string)), &input) != nil || input.PromptVersion != releaseNotesPromptVersion || len(input.Categories) != 7 {
		t.Fatal("missing fixed source contract")
	}
	return payload, releaseNoteSource{Requirements: input.Requirements}
}

func TestReleaseNotesProviderUsesStrictSchemaFullBatchesAndOneConfigurationSnapshot(t *testing.T) {
	a := testApp(t)
	source := releaseProviderSource(43)
	source.Requirements[0].Type = "产品需求"
	source.Requirements[0].Category = "大模型类型"
	source.Requirements[0].Description = "完整开头 " + strings.Repeat("细节", 5000) + " 完整末尾 ![外部图](https://images.example.test/private.png) data:image/png;base64,AAAABBBB"
	source.Requirements[0].Images = []releaseNoteImage{{ID: 11, Name: "筛选.png", URL: "https://images.example.test/never-send", SHA256: "private-image-fingerprint", ContentType: "image/png"}}
	seen := map[int64]bool{}
	calls := 0
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://release-gateway.example.com/v1/responses" || r.Header.Get("Authorization") != "Bearer "+aiTestSecret || r.Method != "POST" {
			t.Fatal("snapshot address/key mismatch")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 60*time.Second {
			t.Fatal("unbounded provider call")
		}
		payload, batch := releaseProviderInput(t, r)
		if !strings.Contains(payload["instructions"].(string), "categoryHint") || !strings.Contains(payload["instructions"].(string), "冲突时只采用最先命中的一类") || !strings.Contains(payload["instructions"].(string), "缺陷、Bug、Defect 不得") {
			t.Fatal("prompt is missing the stable category and defect contract")
		}
		if calls == 1 && (!strings.Contains(payload["input"].(string), `"type":"产品需求"`) || !strings.Contains(payload["input"].(string), `"sourceCategory":"大模型类型"`) || !strings.Contains(payload["input"].(string), `"categoryHint":"大模型类型"`)) {
			t.Fatal("requirement type/category was not supplied with a stable category hint")
		}
		if payload["store"] != false || payload["truncation"] != "disabled" || payload["tools"] != nil || payload["model"] != "gpt-5-mini" || payload["reasoning"] == nil {
			t.Fatal("unsafe model payload")
		}
		if strings.Contains(payload["input"].(string), "AAAABBBB") || strings.Contains(payload["input"].(string), "images.example.test") || strings.Contains(payload["input"].(string), "private-image-fingerprint") {
			t.Fatal("image bytes, URL or unnecessary metadata left server")
		}
		if calls == 1 && (!strings.Contains(payload["input"].(string), "完整末尾") || !strings.Contains(payload["input"].(string), "完整开头")) {
			t.Fatal("source silently truncated")
		}
		format := payload["text"].(map[string]any)["format"].(map[string]any)
		schema := format["schema"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true || schema["additionalProperties"] != false {
			t.Fatal("not strict structured outputs")
		}
		if len(batch.Requirements) > releaseNotesBatchSize {
			t.Fatal("unbounded batch")
		}
		for _, item := range batch.Requirements {
			if seen[item.ID] {
				t.Fatal("repeated source")
			}
			seen[item.ID] = true
		}
		return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(batch)})), nil
	})
	entries, err := a.generateReleaseNotes(context.Background(), aiTestSecret, "gpt-5-mini", "https://release-gateway.example.com/v1", source)
	if err != nil || len(entries) != 43 || len(seen) != 43 || calls != 3 {
		t.Fatal("batch source omitted or failed", len(entries), calls, err)
	}
}

func TestReleaseNotesGeneratedClassificationIsStableAndCanonicallyOrdered(t *testing.T) {
	a := testApp(t)
	source := releaseNoteSource{ProjectID: projectID, SprintID: 88, VersionName: "Release taxonomy", ReleaseDate: "2026-09-10T08:00:00Z", Requirements: []releaseNoteRequirement{
		{ID: 7, Title: "优化导出体验", Type: "产品需求", Category: "协作"},
		{ID: 1, Title: "开放 Webhook", Type: "API接口", Category: "开放平台"},
		{ID: 6, Title: "成员权限配置", Type: "产品需求", Category: "企业管理"},
		{ID: 3, Title: "客户账单查询", Type: "产品需求", Category: "CRM"},
		{ID: 2, Title: "AI 外呼任务", Type: "产品需求", Category: "语音机器人"},
		{ID: 4, Title: "智能体工具调用", Type: "产品需求", Category: "Agent"},
		{ID: 5, Title: "RAG 知识库检索", Type: "大模型类型", Category: "API接口"},
	}}
	for i := range source.Requirements {
		source.Requirements[i].Description = "完成对应能力。"
		source.Requirements[i].Acceptance = "功能可用。"
		source.Requirements[i].Images = []releaseNoteImage{}
	}
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		_, batch := releaseProviderInput(t, r)
		entries := releaseContentEntries(batch)
		for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
			entries[left], entries[right] = entries[right], entries[left]
		}
		// Simulate a model that returned valid JSON but ignored the category hint.
		for i := range entries {
			entries[i].Category = "其他"
		}
		return aiResponse(jsonText(map[string]any{"entries": entries})), nil
	})
	entries, err := a.generateReleaseNotes(context.Background(), aiTestSecret, "gpt-5-mini", aiDefaultBaseURL, source)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []int64{5, 4, 2, 3, 6, 1, 7}
	wantCategories := releaseNoteCategories
	for i := range entries {
		if entries[i].RequirementIDs[0] != wantIDs[i] || entries[i].Category != wantCategories[i] {
			t.Fatalf("entry %d was not normalized: %#v", i, entries[i])
		}
	}
	if got := releaseNotesCanonicalCategory(releaseNoteRequirement{Title: "智能体 API", Type: "API接口", Category: "智能体类型"}); got != "智能体类型" {
		t.Fatalf("category precedence changed: %s", got)
	}
}

func TestReleaseNotesGeneratedOrderIsGlobalAcrossBatches(t *testing.T) {
	a := testApp(t)
	source := releaseProviderSource(releaseNotesBatchSize + 1)
	for i := range source.Requirements {
		source.Requirements[i].Category = "其他"
	}
	source.Requirements[len(source.Requirements)-1].Category = "大模型类型"
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		_, batch := releaseProviderInput(t, r)
		return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(batch)})), nil
	})
	entries, err := a.generateReleaseNotes(context.Background(), aiTestSecret, "gpt-5-mini", aiDefaultBaseURL, source)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].RequirementIDs[0] != int64(releaseNotesBatchSize+1) || entries[0].Category != "大模型类型" {
		t.Fatalf("categories were only sorted inside individual provider batches: %#v", entries)
	}
}

func TestReleaseNotesProviderNeverReturnsPartialDraftAndDoesNotSendOversizedSource(t *testing.T) {
	for _, scenario := range []string{"later-batch-failed", "too-many", "too-large", "unsafe-destination"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			source := releaseProviderSource(21)
			calls := 0
			base := aiDefaultBaseURL
			if scenario == "too-many" {
				source = releaseProviderSource(releaseNotesMaxRequirements + 1)
			}
			if scenario == "too-large" {
				source.Requirements[0].Description = strings.Repeat("x", releaseNotesMaxSourceBytes+1)
			}
			if scenario == "unsafe-destination" {
				base = "http://127.0.0.1/private"
			}
			aiMock(a, func(r *http.Request) (*http.Response, error) {
				calls++
				_, batch := releaseProviderInput(t, r)
				if calls == 2 {
					return nil, errors.New("synthetic gateway failure " + aiTestSecret)
				}
				return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(batch)})), nil
			})
			entries, err := a.generateReleaseNotes(context.Background(), aiTestSecret, "gpt-5-mini", base, source)
			if err == nil || entries != nil || strings.Contains(err.Error(), aiTestSecret) {
				t.Fatal("partial draft or unsafe provider failure")
			}
			if scenario == "later-batch-failed" && calls != 2 || scenario != "later-batch-failed" && calls != 0 {
				t.Fatal("wrong number of paid calls", calls)
			}
		})
	}
}

func TestReleaseNotesProviderRejectsMalformedStructuredContent(t *testing.T) {
	source := releaseProviderSource(1)
	entry := releaseContentEntries(source)[0]
	valid := jsonText(map[string]any{"entries": []releaseNoteEntry{entry}})
	for _, scenario := range []string{"missing", "unknown", "duplicate", "null", "extra-json", "caption-in-model", "unknown-image", "future-plan", "cancelled", "duplicate-id", "wrong-id", "null-image-list"} {
		t.Run(scenario, func(t *testing.T) {
			raw := valid
			switch scenario {
			case "missing":
				raw = `{}`
			case "unknown":
				raw = `{"entries":[],"other":1}`
			case "duplicate":
				raw = `{"entries":[],"entries":[]}`
			case "null":
				raw = `{"entries":null}`
			case "extra-json":
				raw += `{}`
			case "caption-in-model":
				raw = strings.Replace(valid, `"imageIds":[]`, `"imageIds":[],"imageCaptions":{}`, 1)
			case "unknown-image":
				raw = strings.Replace(valid, `"imageIds":[]`, `"imageIds":[99]`, 1)
			case "future-plan":
				raw = strings.Replace(valid, entry.Description, "下一迭代完成客户筛选功能", 1)
			case "cancelled":
				raw = strings.Replace(valid, entry.Description, "已取消该功能", 1)
			case "duplicate-id":
				raw = strings.Replace(valid, `"requirementIds":[1]`, `"requirementIds":[1,1]`, 1)
			case "wrong-id":
				raw = strings.Replace(valid, `"requirementIds":[1]`, `"requirementIds":[999]`, 1)
			case "null-image-list":
				raw = strings.Replace(valid, `"imageIds":[]`, `"imageIds":null`, 1)
			}
			if entries, err := parseReleaseNotesOutput(raw, source); err == nil || entries != nil {
				t.Fatal("invalid structured content accepted", scenario)
			}
		})
	}
	if entries, err := parseReleaseNotesOutput(valid, source); err != nil || len(entries) != 1 {
		t.Fatal("valid output rejected", err)
	}
	entry.Title = "新增已取消状态筛选"
	entry.Description = "支持在计划任务页面按已取消状态筛选记录。"
	if _, err := parseReleaseNotesOutput(jsonText(map[string]any{"entries": []releaseNoteEntry{entry}}), source); err != nil {
		t.Fatal("a delivered status-filter feature was confused with cancelled delivery", err)
	}
}

func TestReleaseNotesProviderRefusalIncompleteSecretReflectionAndResponseBounds(t *testing.T) {
	for _, scenario := range []string{"refusal", "incomplete", "secret", "escaped-secret", "two-output-chunks", "oversized", "http-failure", "cancelled-context"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			source := releaseProviderSource(1)
			entries := releaseContentEntries(source)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled-context" {
				cancel()
			}
			aiMock(a, func(r *http.Request) (*http.Response, error) {
				if scenario == "cancelled-context" {
					return nil, r.Context().Err()
				}
				if scenario == "secret" || scenario == "escaped-secret" {
					entries[0].Description = aiTestSecret
				}
				response := aiResponse(jsonText(map[string]any{"entries": entries}))
				switch scenario {
				case "refusal":
					response.Body = io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal"}]}]}`))
				case "incomplete":
					response.Body = io.NopCloser(strings.NewReader(`{"status":"incomplete","output":[]}`))
				case "two-output-chunks":
					response.Body = io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{}"},{"type":"output_text","text":"{}"}]}]}`))
				case "oversized":
					response.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", (1<<20)+1)))
				case "http-failure":
					response.StatusCode = 500
					response.Body = io.NopCloser(strings.NewReader(aiTestSecret))
				case "escaped-secret":
					raw := jsonText(map[string]any{"entries": entries})
					escaped := ""
					for _, ch := range aiTestSecret {
						escaped += fmt.Sprintf(`\u%04x`, ch)
					}
					raw = strings.ReplaceAll(raw, aiTestSecret, escaped)
					response = aiResponse(raw)
				}
				return response, nil
			})
			result, err := a.generateReleaseNotes(ctx, aiTestSecret, "gpt-4o", aiDefaultBaseURL, source)
			if err == nil || result != nil || strings.Contains(err.Error(), aiTestSecret) {
				t.Fatal("unsafe provider response accepted")
			}
		})
	}
}
