package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const refinementSample = `{"background":"已有背景","rules":"规则说明","exceptions":"待确认","acceptance":"可以验证","questions":"待确认：目标用户？"}`

func TestAIDefectProfessionalPreview(t *testing.T) {
	a := aiApp(t)
	calls := 0
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "defect triage and debugging expert") || !strings.Contains(string(raw), "Unverified causes") {
			t.Fatal("missing defect grounding")
		}
		return aiResponse(refinementSample), nil
	})
	before := tableCount(t, a, "defects")
	for _, tc := range []struct {
		user, body string
		status     int
	}{{"u_viewer", titleBody(nil), 403}, {"u_admin", `{"description":"缺陷复现描述内容","confirmed":false}`, 422}, {"u_admin", `{"description":"缺陷复现描述内容","confirmed":true,"requirementId":1}`, 400}, {"u_admin", titleBody(nil), 200}} {
		w := apiRequest(a, "POST", "/api/ai/defect-refine", tc.user, projectID, tc.body)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 || tableCount(t, a, "defects") != before {
		t.Fatal("unexpected paid request or mutation")
	}
}

func TestAIRefinementValidation(t *testing.T) {
	if _, e := parseAIRefinement(refinementSample); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{}`, `null`, strings.Replace(refinementSample, `"rules":"规则说明"`, `"rules":null`, 1), strings.Replace(refinementSample, `"rules":"规则说明"`, `"rules":"<script>"`, 1), strings.Replace(refinementSample, `"rules":"规则说明"`, `"rules":"a","rules":"b"`, 1), refinementSample + `{}`} {
		if _, e := parseAIRefinement(raw); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestAIRefinementProvenanceInstructions(t *testing.T) {
	for name, prompt := range map[string]string{"requirement": aiRefinementInstructions, "defect": aiDefectRefinementInstructions} {
		for _, instruction := range []string{"[原文明确]", "[优化建议]", "[待确认]", "Source assertions are not verified facts", "If source statements conflict, preserve both", "Never fabricate a quote", "Do not ask again for information already supplied", "when the item is adopted alone"} {
			if !strings.Contains(prompt, instruction) {
				t.Fatalf("%s prompt lost handoff quality boundary %q", name, instruction)
			}
		}
	}
	// Provenance labels remain plain text in the existing strict five-field schema.
	preview := `{"background":"[原文明确] 仅管理员可导出。","rules":"[原文明确] 导出当前筛选结果。","exceptions":"[待确认] 两处权限规则冲突，尚未裁决。","acceptance":"[优化建议] 前置条件满足后执行导出，核对结果是否匹配筛选。","questions":"[待确认] 原文未提供导出字段，请确认以定义验收范围。"}`
	parsed, err := parseAIRefinement(preview)
	if err != nil || !strings.Contains(parsed["questions"], "[待确认]") {
		t.Fatal("provenance must survive validation and selective adoption", err)
	}
}
func TestAIRefinementPreviewAndPermissions(t *testing.T) {
	a := aiApp(t)
	calls := 0
	aiMock(a, func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "requirement_refinement") || !strings.Contains(string(raw), `"store":false`) {
			t.Fatal(string(raw))
		}
		return aiResponse(refinementSample), nil
	})
	before := tableCount(t, a, "requirements")
	for _, test := range []struct {
		user, body string
		status     int
	}{{"u_viewer", titleBody(nil), 403}, {"u_admin", `{"description":"补充需求说明测试","confirmed":false}`, 422}, {"u_admin", titleBody(nil), 200}} {
		w := apiRequest(a, "POST", "/api/ai/requirement-refine", test.user, projectID, test.body)
		if w.Code != test.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if calls != 1 || tableCount(t, a, "requirements") != before {
		t.Fatal("unexpected call or mutation")
	}
}
