package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func integrationMCPTestScopes(write bool) []string {
	result := []string{"requirements:read", "iterations:read", "defects:read", "test-cases:read", "executions:read"}
	if write {
		result = append(result, "requirements:write", "iterations:write", "defects:write", "test-cases:write", "executions:write", "comments:write")
	}
	return result
}

func integrationMCPTestCall(t *testing.T, payload string, scopes []string, dispatch http.Handler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/open/mcp", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Authorization", "Bearer test-only-credential")
	r.Header.Set("X-DevFlow-User", "untrusted-client-selector")
	r.Header.Set("Cookie", "untrusted=client-cookie")
	w := httptest.NewRecorder()
	serveIntegrationMCP(w, r, dispatch, scopes)
	var response map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("Invalid MCP JSON response: %s", w.Body.String())
		}
	}
	return w, response
}

func integrationMCPTestNoDispatch(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Unexpected business dispatch: %s %s", r.Method, r.URL)
	})
}

func TestIntegrationMCPInitializeNegotiatesStatelessProtocol(t *testing.T) {
	for _, version := range []string{"2025-03-26", "2025-06-18", "2099-01-01"} {
		t.Run(version, func(t *testing.T) {
			w, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, nil, integrationMCPTestNoDispatch(t))
			if w.Code != 200 || response["id"] != "init" || w.Header().Get("Mcp-Session-Id") != "" {
				t.Fatalf("Invalid stateless initialize: %d %s", w.Code, w.Body.String())
			}
			result := response["result"].(map[string]any)
			if result["protocolVersion"] != "2025-06-18" || !strings.Contains(result["instructions"].(string), "untrusted task data") {
				t.Fatalf("Missing version or trust boundary: %#v", result)
			}
		})
	}
	w, _ := integrationMCPTestCall(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil, integrationMCPTestNoDispatch(t))
	if w.Code != 202 || w.Body.Len() != 0 {
		t.Fatalf("Notification must return empty 202: %d %s", w.Code, w.Body.String())
	}
	_, ping := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, nil, integrationMCPTestNoDispatch(t))
	if _, ok := ping["result"].(map[string]any); !ok {
		t.Fatalf("Ping response: %#v", ping)
	}
}

func TestIntegrationMCPHTTPAndJSONBoundaries(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPatch} {
		r := httptest.NewRequest(method, "/api/open/mcp", nil)
		w := httptest.NewRecorder()
		serveIntegrationMCP(w, r, integrationMCPTestNoDispatch(t), nil)
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("Method %s accepted: %d", method, w.Code)
		}
	}
	for name, body := range map[string]string{
		"array":       `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		"two objects": `{"jsonrpc":"2.0","id":1,"method":"ping"} {}`,
		"null":        `null`, "invalid version": `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		"null id":       `{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		"fractional id": `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`,
		"oversized":     `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"padding":"` + strings.Repeat("x", integrationMCPMaxBody) + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			w, response := integrationMCPTestCall(t, body, nil, integrationMCPTestNoDispatch(t))
			if w.Code < 400 || response["error"] == nil {
				t.Fatalf("Accepted invalid JSON RPC: %d %#v", w.Code, response)
			}
			if name == "oversized" && w.Code != 413 {
				t.Fatalf("Oversize returned %d", w.Code)
			}
		})
	}
	for name, value := range map[string]string{"Content-Type": "text/plain", "Accept": "text/html", "MCP-Protocol-Version": "2099-01-01"} {
		r := httptest.NewRequest(http.MethodPost, "/api/open/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(name, value)
		w := httptest.NewRecorder()
		serveIntegrationMCP(w, r, integrationMCPTestNoDispatch(t), nil)
		if w.Code < 400 {
			t.Fatalf("Accepted unsupported %s: %s", name, value)
		}
	}
}

func TestIntegrationMCPToolCatalogueScopeAndAnnotations(t *testing.T) {
	readScopes := integrationMCPTestScopes(false)
	_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, readScopes, integrationMCPTestNoDispatch(t))
	tools := response["result"].(map[string]any)["tools"].([]any)
	if len(tools) < 10 {
		t.Fatalf("Missing read tools: %#v", tools)
	}
	for _, entry := range tools {
		tool := entry.(map[string]any)
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatalf("Read credential exposed write tool: %#v", tool)
		}
		if tool["inputSchema"].(map[string]any)["additionalProperties"] != false || !strings.Contains(tool["description"].(string), "untrusted data") {
			t.Fatalf("Missing schema/trust boundary: %#v", tool)
		}
	}
	for _, tool := range integrationMCPTools(integrationMCPTestScopes(true)) {
		if tool.action == "update" && (tool.Annotations["readOnlyHint"] != false || tool.Annotations["destructiveHint"] != true) {
			t.Fatalf("Update missing write annotation: %#v", tool)
		}
		if tool.action == "comment" && tool.Annotations["readOnlyHint"] != false {
			t.Fatalf("Comment missing write annotation: %#v", tool)
		}
	}
	if tools := integrationMCPTools([]string{"requirements:write", "comments:write"}); len(tools) != 1 || tools[0].Name != "devflow_me" {
		t.Fatalf("Write tools leaked without required read scopes: %#v", tools)
	}
}

func TestIntegrationMCPRejectsUnauthorizedToolsAndUnsafeArguments(t *testing.T) {
	cases := []struct{ name, tool, args string }{
		{"arbitrary URL", "fetch", `{"url":"http://127.0.0.1/admin"}`},
		{"path override", "devflow_requirements_get", `{"id":1,"path":"/api/admin"}`},
		{"string id", "devflow_requirements_get", `{"id":"../admin"}`},
		{"zero id", "devflow_requirements_get", `{"id":0}`},
		{"fractional id", "devflow_requirements_get", `{"id":1.5}`},
		{"too large id", "devflow_requirements_get", `{"id":9007199254740992}`},
		{"unknown query", "devflow_requirements_list", `{"query":{"projectId":"other"}}`},
		{"unbounded page", "devflow_requirements_list", `{"query":{"pageSize":1000}}`},
		{"bad query type", "devflow_requirements_list", `{"query":{"q":{}}}`},
		{"ambiguous context", "devflow_context", `{"requirementId":1,"sprintId":2}`},
		{"array body", "devflow_requirements_create", `{"data":[],"idempotencyKey":"abcdefgh"}`},
		{"unknown body field", "devflow_requirements_create", `{"data":{"title":"T","projectId":"other"},"idempotencyKey":"abcdefgh"}`},
		{"ignored requirement assignment field", "devflow_requirements_update", `{"id":1,"data":{"ownerUserId":"u_other"},"idempotencyKey":"abcdefgh","ifMatch":"\"v1\""}`},
		{"execution identity override", "devflow_executions_update", `{"id":1,"data":{"executorUserId":"u_other"},"idempotencyKey":"abcdefgh","ifMatch":"\"v1\""}`},
		{"execution plan override", "devflow_executions_update", `{"id":1,"data":{"planId":3},"idempotencyKey":"abcdefgh","ifMatch":"\"v1\""}`},
		{"invalid steps", "devflow_test_cases_create", `{"data":{"title":"T","stepsDetail":[{"action":"Check"}]},"idempotencyKey":"abcdefgh"}`},
		{"empty steps", "devflow_test_cases_create", `{"data":{"title":"T","stepsDetail":[]},"idempotencyKey":"abcdefgh"}`},
		{"bad relation", "devflow_test_cases_create", `{"data":{"title":"T","requirementId":"other"},"idempotencyKey":"abcdefgh"}`},
		{"negative relation", "devflow_test_cases_create", `{"data":{"title":"T","requirementId":-1},"idempotencyKey":"abcdefgh"}`},
		{"missing key", "devflow_requirements_create", `{"data":{"title":"T"}}`},
		{"header injection key", "devflow_requirements_create", `{"data":{"title":"T"},"idempotencyKey":"abcdefgh\r\nX-Test: bad"}`},
		{"missing etag", "devflow_requirements_update", `{"id":1,"data":{"remarks":"T"},"idempotencyKey":"abcdefgh"}`},
		{"wildcard etag", "devflow_requirements_update", `{"id":1,"data":{"remarks":"T"},"idempotencyKey":"abcdefgh","ifMatch":"*"}`},
		{"header injection etag", "devflow_requirements_update", `{"id":1,"data":{"remarks":"T"},"idempotencyKey":"abcdefgh","ifMatch":"\"v1\"\r\nX-Test: bad"}`},
		{"unexpected comment field", "devflow_requirements_comment", `{"id":1,"body":"T","author":"other","idempotencyKey":"abcdefgh"}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tt.tool+`","arguments":`+tt.args+`}}`, integrationMCPTestScopes(true), integrationMCPTestNoDispatch(t))
			if response["error"] == nil || response["error"].(map[string]any)["code"] != float64(-32602) {
				t.Fatalf("Unsafe call accepted: %#v", response)
			}
		})
	}
	_, denied := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_requirements_create","arguments":{"data":{"title":"T"},"idempotencyKey":"abcdefgh"}}}`, integrationMCPTestScopes(false), integrationMCPTestNoDispatch(t))
	if denied["error"] == nil {
		t.Fatalf("Read credential dispatched write: %#v", denied)
	}
}

func TestIntegrationMCPForwardsOnlyBoundedRequestsAndReturnsETag(t *testing.T) {
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/open/v1/requirements/42" || r.URL.Host != "" || r.URL.Scheme != "" {
			t.Fatalf("Unexpected target: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("If-Match") != `"revision-7"` || r.Header.Get("Idempotency-Key") != "change-123" || r.Header.Get("Authorization") != "Bearer test-only-credential" || r.Header.Get("X-DevFlow-User") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("Unsafe or missing forwarded headers: %#v", r.Header)
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["remarks"] != "实现完成，待复核" || len(payload) != 1 {
			t.Fatalf("Invalid native request body: %#v", payload)
		}
		w.Header().Set("ETag", `"revision-8"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42,"remarks":"实现完成，待复核"}`))
	})
	_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"devflow_requirements_update","arguments":{"id":42,"data":{"remarks":"实现完成，待复核"},"idempotencyKey":"change-123","ifMatch":"\"revision-7\""}}}`, integrationMCPTestScopes(true), dispatch)
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if result["isError"] != false || structured["etag"] != `"revision-8"` || structured["contentTrust"] != "untrusted_business_data" || structured["data"].(map[string]any)["id"] != float64(42) {
		t.Fatalf("Lost response data or ETag: %#v", result)
	}
	var textResult map[string]any
	if err := json.Unmarshal([]byte(result["content"].([]any)[0].(map[string]any)["text"].(string)), &textResult); err != nil || textResult["etag"] != structured["etag"] {
		t.Fatalf("Text and structured results disagree: %#v", result)
	}
}

func TestIntegrationMCPReadQueriesAndComments(t *testing.T) {
	cases := []struct{ tool, args, method, path, query string }{
		{"devflow_me", `{}`, "GET", "/api/open/v1/me", ""},
		{"devflow_metadata", `{}`, "GET", "/api/open/v1/metadata", ""},
		{"devflow_context", `{"sprintId":8}`, "GET", "/api/open/v1/context", "sprintId=8"},
		{"devflow_requirements_list", `{"query":{"q":"a&projectId=evil","page":2,"pageSize":10}}`, "GET", "/api/open/v1/requirements", "page=2&pageSize=10&q=a%26projectId%3Devil"},
		{"devflow_requirements_transitions", `{"id":4}`, "GET", "/api/open/v1/requirements/4/transitions", ""},
		{"devflow_requirements_test_cases", `{"id":4,"query":{"page":2}}`, "GET", "/api/open/v1/requirements/4/test-cases", "page=2"},
		{"devflow_defects_comments", `{"id":3}`, "GET", "/api/open/v1/defects/3/comments", ""},
		{"devflow_test_cases_create", `{"data":{"title":"T","requirementId":null},"idempotencyKey":"create-123"}`, "POST", "/api/open/v1/test-cases", ""},
		{"devflow_requirements_comment", `{"id":4,"body":"已验证","mentionUserIds":["u_member"],"idempotencyKey":"comment-123"}`, "POST", "/api/open/v1/requirements/4/comments", ""},
	}
	for _, tt := range cases {
		t.Run(tt.tool, func(t *testing.T) {
			called := false
			dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != tt.method || r.URL.Path != tt.path || r.URL.RawQuery != tt.query {
					t.Fatalf("Wrong native request: %s %s", r.Method, r.URL)
				}
				if tt.tool == "devflow_requirements_comment" {
					var comment map[string]any
					if json.NewDecoder(r.Body).Decode(&comment) != nil || comment["body"] != "已验证" || len(comment) != 2 || len(comment["mentionUserIds"].([]any)) != 1 {
						t.Fatalf("Comment body: %#v", comment)
					}
				}
				_, _ = w.Write([]byte(`{"items":[]}`))
			})
			_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tt.tool+`","arguments":`+tt.args+`}}`, integrationMCPTestScopes(true), dispatch)
			if !called || response["error"] != nil {
				t.Fatalf("Request not dispatched: %#v", response)
			}
		})
	}
}

func TestIntegrationMCPBusinessErrorsAndBoundedResults(t *testing.T) {
	for _, code := range []int{403, 404, 409, 412, 422, 429, 503} {
		dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"native_business_error"}`))
		})
		w, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_me","arguments":{}}}`, nil, dispatch)
		result := response["result"].(map[string]any)
		if w.Code != 200 || result["isError"] != true || result["structuredContent"].(map[string]any)["status"] != float64(code) {
			t.Fatalf("HTTP %d lost its tool error: %#v", code, response)
		}
	}
	_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_me","arguments":{}}}`, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", integrationMCPMaxResult+1)))
	}))
	result := response["result"].(map[string]any)
	if result["isError"] != true || result["structuredContent"].(map[string]any)["data"].(map[string]any)["error"] != "result_too_large" {
		t.Fatalf("Oversized business result not bounded: %#v", response)
	}
}

func TestIntegrationMCPOptionalArgumentsAndProtocolErrors(t *testing.T) {
	_, response := integrationMCPTestCall(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_me"}}`, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projectId":"test"}`))
	}))
	if response["error"] != nil || response["result"].(map[string]any)["isError"] != false {
		t.Fatalf("Omitted optional arguments rejected: %#v", response)
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_me","arguments":null}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"devflow_requirements_get"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file:///etc/passwd"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"unknown"}}`,
	} {
		_, response := integrationMCPTestCall(t, body, integrationMCPTestScopes(true), integrationMCPTestNoDispatch(t))
		if response["error"] == nil {
			t.Fatalf("Invalid protocol call accepted: %#v", response)
		}
	}
	w, _ := integrationMCPTestCall(t, `{"jsonrpc":"2.0","method":"notifications/initialized","params":[]}`, nil, integrationMCPTestNoDispatch(t))
	if w.Code != 400 || w.Body.Len() != 0 {
		t.Fatalf("Invalid notification must be rejected without an RPC response: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationOpenAPICoversBoundedInterface(t *testing.T) {
	spec := integrationOpenAPISpec()
	if spec["openapi"] != "3.1.0" {
		t.Fatal("Missing OpenAPI 3.1 declaration")
	}
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{"/me", "/context", "/metadata", "/openapi", "/requirements", "/iterations", "/defects", "/test-cases", "/executions", "/requirements/{id}/comments", "/requirements/{id}/transitions", "/requirements/{id}/test-cases"} {
		if paths[path] == nil {
			t.Fatalf("Missing documented path %s", path)
		}
	}
	if paths["/executions"].(map[string]any)["post"] != nil || paths["/requirements/{id}/transitions"].(map[string]any)["post"] != nil {
		t.Fatal("Spec advertises unsupported write methods")
	}
	patch := paths["/requirements/{id}"].(map[string]any)["patch"].(map[string]any)
	parameters := patch["parameters"].([]any)
	if len(parameters) != 3 || parameters[1].(map[string]any)["name"] != "Idempotency-Key" || parameters[2].(map[string]any)["name"] != "If-Match" {
		t.Fatalf("Missing documented write preconditions: %#v", parameters)
	}
	security := spec["components"].(map[string]any)["securitySchemes"].(map[string]any)["bearerAuth"].(map[string]any)
	if security["type"] != "http" || security["scheme"] != "bearer" {
		t.Fatalf("Incorrect authentication scheme: %#v", security)
	}
	if _, err := json.Marshal(spec); err != nil {
		t.Fatalf("OpenAPI is not serializable: %v", err)
	}
}
