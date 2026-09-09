package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func integrationAPIToken(t *testing.T, a *App, extra ...string) string {
	t.Helper()
	scopes := append(append([]string{}, integrationReadScopes...), extra...)
	w := apiRequest(a, "POST", "/api/integrations/tokens", "u_admin", projectID, jsonText(map[string]any{"name": "API regression", "scopes": scopes, "expiresInDays": 7}))
	if w.Code != 201 {
		t.Fatalf("create credential: %d %s", w.Code, w.Body.String())
	}
	return jsonMap(t, w)["token"].(string)
}

func integrationAPIRequest(a *App, token, method, path, body, key, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, r)
	return w
}

func TestIntegrationAPIReadContextMetadataAndPaging(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a, "executions:read")
	for _, resource := range []string{"requirements", "iterations", "defects", "test-cases", "executions"} {
		w := integrationAPIRequest(a, token, "GET", "/api/open/v1/"+resource+"?pageSize=1", "", "", "")
		if w.Code != 200 {
			t.Fatalf("list %s: %d %s", resource, w.Code, w.Body.String())
		}
		items := jsonMap(t, w)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("unbounded/empty list %s: %s", resource, w.Body.String())
		}
		id := int64(items[0].(map[string]any)["id"].(float64))
		w = integrationAPIRequest(a, token, "GET", fmt.Sprintf("/api/open/v1/%s/%d", resource, id), "", "", "")
		if w.Code != 200 || w.Header().Get("ETag") == "" || jsonMap(t, w)["_etag"] != w.Header().Get("ETag") {
			t.Fatalf("get %s: %d %s", resource, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/open/v1/requirements?pageSize=101", "/api/open/v1/requirements?projectId=foreign", "/api/open/v1/requirements?q=a&q=b", "/api/open/v1/context?requirementId=1&sprintId=1", "/api/open/v1/context?requirementId=bad"} {
		w := integrationAPIRequest(a, token, "GET", path, "", "", "")
		if w.Code != 400 {
			t.Fatalf("invalid filter %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := integrationAPIRequest(a, token, "GET", "/api/open/v1/metadata", "", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "u_front") || strings.Contains(w.Body.String(), "@devflow.local") || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("metadata unsafe/missing: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/open/v1/context", "/api/open/v1/context?requirementId=1", "/api/open/v1/context?sprintId=1"} {
		w = integrationAPIRequest(a, token, "GET", path, "", "", "")
		if w.Code != 200 {
			t.Fatalf("context %s: %d %s", path, w.Code, w.Body.String())
		}
		data := jsonMap(t, w)
		if data["limits"].(map[string]any)["truncated"] != false {
			t.Fatal("small project unexpectedly truncated")
		}
		if strings.Contains(path, "requirementId") && len(data["requirements"].([]any)) != 1 {
			t.Fatal("requirement context not scoped")
		}
		if strings.Contains(path, "sprintId") && len(data["sprints"].([]any)) != 1 {
			t.Fatal("iteration context not scoped")
		}
		if strings.Contains(w.Body.String(), "洞察报告支持多模型对比") {
			t.Fatal("foreign project leaked")
		}
	}
	w = integrationAPIRequest(a, token, "GET", "/api/open/v1/context?requirementId=7", "", "", "")
	if w.Code != 404 {
		t.Fatalf("foreign context: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationAPICreateUpdateIdempotencyAndNativeWriteConflict(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a, "requirements:write", "iterations:write", "defects:write", "test-cases:write", "comments:write", "executions:read", "executions:write")
	for _, test := range []struct{ resource, create, update string }{
		{"requirements", `{"title":"API 需求"}`, `{"title":"API 需求更新"}`},
		{"iterations", `{"name":"API 迭代","startDate":"2026-09-10","endDate":"2026-09-20"}`, `{"goal":"AI 协作交付"}`},
		{"defects", `{"title":"API 缺陷","severity":"一般","priority":"P2"}`, `{"description":"补充复现信息"}`},
		{"test-cases", `{"title":"API 用例","preconditions":"已登录","steps":"打开页面","expected":"显示列表","requirementId":1}`, `{"title":"API 用例更新"}`},
	} {
		t.Run(test.resource, func(t *testing.T) {
			path := "/api/open/v1/" + test.resource
			key := "create-" + test.resource
			first := integrationAPIRequest(a, token, "POST", path, test.create, key, "")
			if first.Code != 201 {
				t.Fatalf("create: %d %s", first.Code, first.Body.String())
			}
			again := integrationAPIRequest(a, token, "POST", path, test.create, key, "")
			if again.Code != 201 || again.Body.String() != first.Body.String() || again.Header().Get("Idempotency-Replayed") != "true" {
				t.Fatalf("not replayed: %d %s", again.Code, again.Body.String())
			}
			conflict := integrationAPIRequest(a, token, "POST", path, test.update, key, "")
			if conflict.Code != 409 {
				t.Fatalf("key reused for changed content: %d %s", conflict.Code, conflict.Body.String())
			}
			id := int64(jsonMap(t, first)["id"].(float64))
			path += fmt.Sprint("/", id)
			read := integrationAPIRequest(a, token, "GET", path, "", "", "")
			etag := read.Header().Get("ETag")
			missing := integrationAPIRequest(a, token, "PATCH", path, test.update, "missing-"+test.resource, "")
			if missing.Code != 428 {
				t.Fatalf("missing version: %d %s", missing.Code, missing.Body.String())
			}
			updated := integrationAPIRequest(a, token, "PATCH", path, test.update, "update-"+test.resource, etag)
			if updated.Code != 200 {
				t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
			}
			stale := integrationAPIRequest(a, token, "PATCH", path, test.update, "stale-"+test.resource, etag)
			if stale.Code != 412 {
				t.Fatalf("stale overwrite: %d %s", stale.Code, stale.Body.String())
			}
			fresh := integrationAPIRequest(a, token, "GET", path, "", "", "")
			if fresh.Header().Get("ETag") == etag {
				t.Fatal("ETag unchanged")
			}
		})
	}
	read := integrationAPIRequest(a, token, "GET", "/api/open/v1/requirements/1", "", "", "")
	ui := apiRequest(a, "PATCH", "/api/requirements/1", "u_admin", projectID, `{"title":"人类在浏览器修改"}`)
	if ui.Code != 200 {
		t.Fatal(ui.Body.String())
	}
	w := integrationAPIRequest(a, token, "PATCH", "/api/open/v1/requirements/1", `{"title":"过时的 AI 标题"}`, "ui-conflict-key", read.Header().Get("ETag"))
	if w.Code != 412 {
		t.Fatalf("UI overwrite: %d %s", w.Code, w.Body.String())
	}
	read = integrationAPIRequest(a, token, "GET", "/api/open/v1/executions/1", "", "", "")
	w = integrationAPIRequest(a, token, "PATCH", "/api/open/v1/executions/1", `{"status":"通过","actualResult":"页面符合预期"}`, "execution-result-1", read.Header().Get("ETag"))
	if w.Code != 200 {
		t.Fatalf("execution: %d %s", w.Code, w.Body.String())
	}
}

func TestIntegrationAPICommentsReuseAndRejectUnknownFields(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a, "comments:write", "requirements:write")
	for _, resource := range []string{"requirements", "defects", "test-cases"} {
		path := "/api/open/v1/" + resource + "/1/comments"
		body := `{"body":"已完成分析，请 @林夏 复核","mentionUserIds":["u_admin"]}`
		w := integrationAPIRequest(a, token, "POST", path, body, "comment-"+resource, "")
		if w.Code != 201 {
			t.Fatalf("comment %s: %d %s", resource, w.Code, w.Body.String())
		}
		again := integrationAPIRequest(a, token, "POST", path, body, "comment-"+resource, "")
		if again.Code != 201 || again.Body.String() != w.Body.String() {
			t.Fatal("duplicate comment not replayed")
		}
		w = integrationAPIRequest(a, token, "GET", path, "", "", "")
		if w.Code != 200 || strings.Count(w.Body.String(), "已完成分析") != 1 {
			t.Fatalf("duplicated/missing comments: %d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"title":"x","projectId":"bad"}`, `{"title":"x","arbitraryField":true}`, `{"title":"x","authorUserId":"u_algo"}`} {
		w := integrationAPIRequest(a, token, "POST", "/api/open/v1/requirements", body, "bad-payload-0001", "")
		if w.Code != 422 {
			t.Fatalf("unsafe write accepted: %d %s", w.Code, w.Body.String())
		}
	}
	var logged, replayed int
	err := a.db.QueryRow(`SELECT COUNT(*),SUM(replayed) FROM integration_requests WHERE method='POST' AND status=201`).Scan(&logged, &replayed)
	if err != nil || logged != 6 || replayed != 3 {
		t.Fatalf("audit missing: %d %d %v", logged, replayed, err)
	}
	var secrets int
	a.db.QueryRow(`SELECT COUNT(*) FROM integration_requests WHERE path LIKE '%df_%'`).Scan(&secrets)
	if secrets != 0 {
		t.Fatal("credential leaked into audit")
	}
}

func TestIntegrationAPIContextLimitsAndIdempotencySurviveMigration(t *testing.T) {
	a := testApp(t)
	token := integrationAPIToken(t, a, "requirements:write")
	for i := 0; i < 103; i++ {
		_, err := a.db.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,type,status,priority,created_at,updated_at) VALUES(?,?,?,?,'产品需求','草稿','P2',?,?)`, tenantID, projectID, fmt.Sprintf("LIMIT-%d", i), "有界上下文", "2026-09-05T00:00:00Z", "2026-09-05T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	w := integrationAPIRequest(a, token, "GET", "/api/open/v1/context", "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	data := jsonMap(t, w)
	if len(data["requirements"].([]any)) != 100 || data["limits"].(map[string]any)["truncated"] != true {
		t.Fatal("context did not report truncation")
	}
	body := `{"title":"重启后幂等"}`
	w = integrationAPIRequest(a, token, "POST", "/api/open/v1/requirements", body, "durable-operation", "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if err := a.migrateIntegrations(); err != nil {
		t.Fatal(err)
	}
	again := integrationAPIRequest(a, token, "POST", "/api/open/v1/requirements", body, "durable-operation", "")
	if again.Body.String() != w.Body.String() || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("migration lost idempotency record")
	}
	var count int
	a.db.QueryRow(`SELECT COUNT(*) FROM requirements WHERE title='重启后幂等'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate persisted write")
	}
	// 持久化中的未知结果不可重新执行，即使之前的进程已经退出。
	raw := map[string]json.RawMessage{"title": json.RawMessage(`"未确认写入"`)}
	canonical, _ := json.Marshal(raw)
	var tokenID string
	a.db.QueryRow(`SELECT id FROM integration_tokens WHERE token_hash=?`, tokenDigest(token)).Scan(&tokenID)
	_, err := a.db.Exec(`INSERT INTO integration_idempotency(token_id,key_hash,fingerprint,created_at)VALUES(?,?,?,?)`, tokenID, tokenDigest("pending-write-key"), tokenDigest("POST\n/api/open/v1/requirements\n\n"+string(canonical)), "2026-09-05T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	w = integrationAPIRequest(a, token, "POST", "/api/open/v1/requirements", string(canonical), "pending-write-key", "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "operation_pending") {
		t.Fatal("pending write was rerun")
	}
}
