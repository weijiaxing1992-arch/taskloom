package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const refinementSample = `{"background":"已有背景","rules":"规则说明","exceptions":"待确认","acceptance":"可以验证","questions":"待确认：目标用户？"}`

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
