package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// These tests use integrationAPIToken and testApp's isolated database. Requests
// enter the real external route with a persisted credential; no MCP dispatcher,
// authorization handler or business service is mocked.
func integrationMCPE2ERequest(t *testing.T, a *App, token string, id any, method string, params map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		message["id"] = id
	}
	if params != nil {
		message["params"] = params
	}
	request := httptest.NewRequest(http.MethodPost, "/api/open/mcp", strings.NewReader(jsonText(message)))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if method != "initialize" {
		request.Header.Set("MCP-Protocol-Version", "2025-06-18")
	}
	response := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(response, request)
	var decoded map[string]any
	if response.Body.Len() != 0 {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("External MCP response is not JSON: HTTP %d %s", response.Code, response.Body.String())
		}
	}
	return response, decoded
}

func integrationMCPE2ECall(t *testing.T, a *App, token string, id int, tool string, arguments map[string]any, wantStatus int) map[string]any {
	t.Helper()
	response, decoded := integrationMCPE2ERequest(t, a, token, id, "tools/call", map[string]any{"name": tool, "arguments": arguments})
	if response.Code != http.StatusOK || decoded["id"] != float64(id) || decoded["error"] != nil {
		t.Fatalf("External MCP %s failed: HTTP %d %s", tool, response.Code, response.Body.String())
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("Missing MCP tool result: %s", response.Body.String())
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["status"] != float64(wantStatus) || result["isError"] != (wantStatus >= 400) || structured["contentTrust"] != "untrusted_business_data" {
		t.Fatalf("Unexpected MCP business result: %s", response.Body.String())
	}
	return structured
}

func TestIntegrationMCPE2EInitializeToolsAndRequirementETag(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a)
	response, initialized := integrationMCPE2ERequest(t, a, token, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "devflow-e2e", "version": "1"},
	})
	if response.Code != http.StatusOK || initialized["error"] != nil || response.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("Authenticated initialize failed: HTTP %d %s", response.Code, response.Body.String())
	}
	result := initialized["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" || !strings.Contains(result["instructions"].(string), "untrusted task data") {
		t.Fatalf("Invalid initialization result: %#v", result)
	}
	response, _ = integrationMCPE2ERequest(t, a, token, nil, "notifications/initialized", nil)
	if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
		t.Fatalf("Initialized notification failed: HTTP %d %s", response.Code, response.Body.String())
	}
	response, listed := integrationMCPE2ERequest(t, a, token, 2, "tools/list", nil)
	if response.Code != http.StatusOK || listed["error"] != nil {
		t.Fatalf("Tool discovery failed: HTTP %d %s", response.Code, response.Body.String())
	}
	foundRead := false
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		foundRead = foundRead || tool["name"] == "devflow_requirements_get"
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatalf("Read credential advertised a writable tool: %v", tool["name"])
		}
	}
	if !foundRead {
		t.Fatal("Authorized requirement detail tool is missing")
	}
	read := integrationMCPE2ECall(t, a, token, 3, "devflow_requirements_get", map[string]any{"id": 1}, http.StatusOK)
	etag, ok := read["etag"].(string)
	data := read["data"].(map[string]any)
	if !ok || len(etag) < 3 || data["_etag"] != etag || data["id"] != float64(1) {
		t.Fatalf("Real business ETag was not preserved: %#v", read)
	}
	var title string
	if err := a.db.QueryRow(`SELECT title FROM requirements WHERE tenant_id=? AND project_id=? AND id=1`, tenantID, projectID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if data["title"] != title {
		t.Fatalf("MCP detail differs from persisted requirement: %#v", data)
	}
	rest := integrationAPIRequest(a, token, http.MethodGet, "/api/open/v1/requirements/1", "", "", "")
	if rest.Code != http.StatusOK || rest.Header().Get("ETag") != etag {
		t.Fatalf("MCP version differs from REST version: HTTP %d %s", rest.Code, rest.Body.String())
	}
}

func TestIntegrationMCPE2EAuthorizedCreateUpdateAndReplay(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a, "requirements:write")
	createArguments := map[string]any{"data": map[string]any{"title": "MCP 端到端创建"}, "idempotencyKey": "mcp-e2e-create-001"}
	created := integrationMCPE2ECall(t, a, token, 1, "devflow_requirements_create", createArguments, http.StatusCreated)
	createdID := int64(created["data"].(map[string]any)["id"].(float64))
	replayed := integrationMCPE2ECall(t, a, token, 2, "devflow_requirements_create", createArguments, http.StatusCreated)
	if !reflect.DeepEqual(created, replayed) {
		t.Fatalf("Repeated MCP creation did not return the original result: first=%#v replay=%#v", created, replayed)
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=? AND title=?`, tenantID, projectID, "MCP 端到端创建").Scan(&count); err != nil || count != 1 {
		t.Fatalf("Creation replay duplicated persisted work: count=%d err=%v", count, err)
	}
	read := integrationMCPE2ECall(t, a, token, 3, "devflow_requirements_get", map[string]any{"id": createdID}, http.StatusOK)
	etag := read["etag"].(string)
	updateArguments := map[string]any{
		"id": createdID, "data": map[string]any{"remarks": "已完成端到端验证，等待复核"},
		"ifMatch": etag, "idempotencyKey": "mcp-e2e-update-001",
	}
	updated := integrationMCPE2ECall(t, a, token, 4, "devflow_requirements_update", updateArguments, http.StatusOK)
	fresh := integrationMCPE2ECall(t, a, token, 5, "devflow_requirements_get", map[string]any{"id": createdID}, http.StatusOK)
	if fresh["etag"] == etag || fresh["data"].(map[string]any)["remarks"] != "已完成端到端验证，等待复核" {
		t.Fatalf("Authorized update did not persist or advance version: %#v", fresh)
	}
	// Reuse the original ETag and key: replay must return the saved response
	// without executing the write again, even though the object version changed.
	updateReplay := integrationMCPE2ECall(t, a, token, 6, "devflow_requirements_update", updateArguments, http.StatusOK)
	if !reflect.DeepEqual(updated, updateReplay) {
		t.Fatalf("Repeated update did not replay: first=%#v replay=%#v", updated, updateReplay)
	}
	afterReplay := integrationMCPE2ECall(t, a, token, 7, "devflow_requirements_get", map[string]any{"id": createdID}, http.StatusOK)
	if afterReplay["etag"] != fresh["etag"] {
		t.Fatal("Idempotent replay performed another database update")
	}
	var remarks string
	if err := a.db.QueryRow(`SELECT remarks FROM requirements WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, createdID).Scan(&remarks); err != nil || remarks != "已完成端到端验证，等待复核" {
		t.Fatalf("MCP write did not reach database: remarks=%q err=%v", remarks, err)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/open/v1/requirements"},
		{http.MethodPatch, fmt.Sprintf("/api/open/v1/requirements/%d", createdID)},
	} {
		var attempts, replays int
		if err := a.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(replayed),0) FROM integration_requests WHERE method=? AND path=? AND status<400`, request.method, request.path).Scan(&attempts, &replays); err != nil || attempts != 2 || replays != 1 {
			t.Fatalf("Gateway audit did not record one execution and replay for %s %s: attempts=%d replays=%d err=%v", request.method, request.path, attempts, replays, err)
		}
	}
}

func TestIntegrationMCPE2EReadOnlyCredentialCannotWrite(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a)
	before := integrationMCPE2ECall(t, a, token, 1, "devflow_requirements_get", map[string]any{"id": 1}, http.StatusOK)
	var rowCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=?`, tenantID, projectID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	for i, call := range []struct {
		tool string
		args map[string]any
	}{
		{"devflow_requirements_create", map[string]any{"data": map[string]any{"title": "不应创建"}, "idempotencyKey": "mcp-readonly-create"}},
		{"devflow_requirements_update", map[string]any{"id": 1, "data": map[string]any{"title": "不应修改"}, "ifMatch": before["etag"], "idempotencyKey": "mcp-readonly-update"}},
	} {
		response, decoded := integrationMCPE2ERequest(t, a, token, i+2, "tools/call", map[string]any{"name": call.tool, "arguments": call.args})
		if response.Code != http.StatusOK || decoded["error"] == nil || decoded["error"].(map[string]any)["code"] != float64(-32602) {
			t.Fatalf("Read-only token reached a write: HTTP %d %s", response.Code, response.Body.String())
		}
	}
	after := integrationMCPE2ECall(t, a, token, 4, "devflow_requirements_get", map[string]any{"id": 1}, http.StatusOK)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("Denied MCP writes changed requirement contents or revision")
	}
	var afterCount, writes int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=?`, tenantID, projectID).Scan(&afterCount); err != nil || afterCount != rowCount {
		t.Fatalf("Denied create changed database: before=%d after=%d err=%v", rowCount, afterCount, err)
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM integration_requests WHERE method IN ('POST','PATCH')`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("Unauthorized tool was dispatched as a business write: writes=%d err=%v", writes, err)
	}
}
