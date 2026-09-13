package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var integrationResources = map[string]string{"requirements": "requirements", "iterations": "sprints", "defects": "defects", "test-cases": "test-cases", "executions": "test-executions", "notifications": "notifications", "release-notes": "release-notes"}
var integrationTables = map[string]string{"requirements": "requirements", "iterations": "sprints", "defects": "defects", "test-cases": "test_cases", "executions": "test_executions"}

const integrationResponseLimit = 16 << 20

// 在复用业务处理器时缓冲响应，先持久化调用结果再向客户端确认成功。
// 不额外包裹业务事务，避免嵌套 SQLite 写事务死锁。
type integrationResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func newIntegrationResponse() *integrationResponse {
	return &integrationResponse{header: make(http.Header)}
}
func (w *integrationResponse) Header() http.Header { return w.header }
func (w *integrationResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *integrationResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.body.Len()+len(b) > integrationResponseLimit {
		w.overflow = true
		return len(b), nil
	}
	return w.body.Write(b)
}
func (w *integrationResponse) send(out http.ResponseWriter) {
	for key, values := range w.header {
		out.Header()[key] = values
	}
	status := w.status
	if status == 0 {
		status = 200
	}
	out.WriteHeader(status)
	_, _ = out.Write(w.body.Bytes())
}

type integrationRoute struct {
	resource, legacy, sub string
	id                    int64
}

func integrationResolveRoute(path, method string) (integrationRoute, bool) {
	var route integrationRoute
	if !strings.HasPrefix(path, "/api/open/v1/") {
		return route, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/open/v1/"), "/")
	if len(parts) < 1 || len(parts) > 3 {
		return route, false
	}
	route.resource = parts[0]
	route.legacy = integrationResources[route.resource]
	if route.legacy == "" {
		return route, false
	}
	// 通知是凭据所有者的个人收件箱，只开放列表和单条详情读取。已读状态
	// 仍只能由站内交互修改，避免轮询型集成改变用户的阅读状态。
	if route.resource == "notifications" {
		if method != http.MethodGet || len(parts) > 2 {
			return route, false
		}
		if len(parts) == 2 {
			var ok bool
			route.id, ok = integrationPositive(parts[1])
			if !ok {
				return route, false
			}
		}
		return route, true
	}
	// 升级日志是已保存快照的只读资源，不可被通用创建/更新转发逻辑误开放。
	if route.resource == "release-notes" {
		if method != http.MethodGet || len(parts) > 2 {
			return route, false
		}
		if len(parts) == 2 {
			var ok bool
			route.id, ok = integrationPositive(parts[1])
			if !ok {
				return route, false
			}
		}
		return route, true
	}
	if len(parts) > 1 {
		var ok bool
		route.id, ok = integrationPositive(parts[1])
		if !ok {
			return route, false
		}
	}
	if len(parts) > 2 {
		route.sub = parts[2]
		if route.sub == "comments" {
			return route, validChoice(route.resource, []string{"requirements", "defects", "test-cases"}) && validChoice(method, []string{"GET", "POST"})
		}
		return route, route.resource == "requirements" && validChoice(route.sub, []string{"transitions", "test-cases"}) && method == "GET"
	}
	if route.id == 0 {
		return route, method == "GET" || (method == "POST" && route.resource != "executions")
	}
	return route, method == "GET" || method == "PATCH"
}

func (a *App) integrationServeAuthorized(w http.ResponseWriter, r *http.Request, key integrationCredential) {
	w.Header().Set("Cache-Control", "private, no-store")
	// MCP dispatch 与普通 HTTP 一样逐次复查凭证，不能使用初始化时缓存的权限。
	fresh, verified, err := a.authenticateIntegration(r)
	if err != nil || fresh == nil || fresh.pid() != a.pid() || fresh.uid() != a.uid() || verified.ID != key.ID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "database_unavailable", "凭证服务暂时不可用")
		} else {
			fail(w, 401, "invalid_api_token", "凭证已失效，请重新授权")
		}
		return
	}
	key = verified
	if len(r.URL.Path) > 200 || len(r.URL.RawQuery) > 3000 {
		fail(w, 400, "invalid_request", "请求路径或查询过长")
		return
	}
	if r.URL.Path == "/api/open/v1/me" && r.Method == "GET" {
		write(w, 200, map[string]any{"projectId": a.pid(), "userId": a.uid(), "scopes": key.Scopes})
		return
	}
	requestID, ok := a.integrationBeginRequest(w, r, key)
	if !ok {
		return
	}
	response := newIntegrationResponse()
	replayed := false
	defer func() {
		integrationRedactFailure(response)
		if response.overflow {
			response = newIntegrationResponse()
			fail(response, 413, "response_too_large", "返回内容超过 16 MB，请缩小范围或分页读取")
		}
		status := response.status
		if status == 0 {
			status = 500
		}
		// 审计失败不回传“已完成”；保留占位记录供管理员核查，不自动重复写入。
		_, err := a.db.ExecContext(context.Background(), `UPDATE integration_requests SET status=?,replayed=? WHERE id=?`, status, replayed, requestID)
		if err != nil {
			fail(w, 503, "audit_unavailable", "操作结果待核实，请保留请求编号，勿使用新编号重复提交")
			return
		}
		_, _ = a.db.ExecContext(context.Background(), `UPDATE integration_tokens SET last_used_at=? WHERE id=? AND last_used_at<?`, time.Now().UTC().Format(time.RFC3339), key.ID, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339))
		response.send(w)
	}()
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/api/open/v1/context":
			a.integrationContext(response, r)
			return
		case "/api/open/v1/metadata":
			a.integrationMetadata(response, r)
			return
		case "/api/open/v1/openapi":
			write(response, 200, integrationOpenAPISpec())
			return
		}
	}
	route, ok := integrationResolveRoute(r.URL.Path, r.Method)
	if !ok {
		fail(response, 404, "endpoint_not_exposed", "此接口未开放给 AI 协作凭证")
		return
	}
	scope := route.resource + ":read"
	if r.Method != "GET" {
		scope = route.resource + ":write"
		if route.sub == "comments" {
			scope = "comments:write"
		}
	}
	if !integrationHas(key.Scopes, scope) {
		fail(response, 403, "insufficient_scope", "凭证未授权此操作")
		return
	}
	if route.resource == "release-notes" {
		a.integrationReleaseNotes(response, r, route)
		return
	}
	if route.resource == "notifications" {
		a.integrationNotifications(response, r, route)
		return
	}
	if r.Method == "GET" {
		if route.id == 0 {
			a.integrationList(response, r, route.resource)
			return
		}
		if route.sub == "test-cases" {
			if !a.requireEntity(response, "requirement", route.id) {
				return
			}
			q := r.URL.Query()
			q.Set("requirementId", strconv.FormatInt(route.id, 10))
			copy := r.Clone(r.Context())
			u := *r.URL
			u.RawQuery = q.Encode()
			copy.URL = &u
			a.integrationList(response, copy, "test-cases")
			return
		}
		if route.sub != "" {
			if route.sub == "comments" {
				a.integrationComments(response, r, route.resource, route.id)
				return
			}
			if len(r.URL.Query()) != 0 {
				fail(response, 400, "invalid_query", "此读取不接受筛选参数")
				return
			}
			a.integrationForward(response, r, route)
			return
		}
		data, etag, err := a.integrationReadEntity(r, route.resource, route.id)
		if err != nil {
			integrationReadError(response, err)
			return
		}
		response.Header().Set("ETag", etag)
		write(response, 200, data)
		return
	}
	if len(r.URL.Query()) != 0 {
		fail(response, 400, "invalid_query", "写入请求不接受查询参数")
		return
	}
	if route.id > 0 && route.sub == "" && !regexp.MustCompile(`^"[^"\r\n]{1,160}"$`).MatchString(r.Header.Get("If-Match")) {
		fail(response, 428, "precondition_required", "请先读取当前记录，并通过 If-Match 提交 ETag 版本")
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,127}$`).MatchString(r.Header.Get("Idempotency-Key")) {
		fail(response, 400, "idempotency_key_required", "写入须提供 8–128 位唯一 Idempotency-Key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		fail(response, 413, "request_too_large", "单次协作写入不能超过 2 MB")
		return
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || len(payload) == 0 {
		fail(response, 400, "invalid_json", "写入内容须为非空 JSON 对象")
		return
	}
	for _, protected := range []string{"tenantId", "projectId", "id", "code", "createdAt", "updatedAt", "createdBy"} {
		if _, exists := payload[protected]; exists {
			fail(response, 422, "readonly_field", "不能写入系统标识、时间或审计字段")
			return
		}
	}
	allowed := integrationOpenAPIWriteSchema(route.resource, route.id == 0)["properties"].(map[string]any)
	if route.sub == "comments" {
		allowed = map[string]any{"body": true, "mentionUserIds": true, "replyToId": true}
	}
	for field := range payload {
		if _, ok := allowed[field]; !ok {
			fail(response, 422, "unsupported_field", "开放接口不支持字段："+field)
			return
		}
	}
	// 规范化对象键序；相同幂等键对应不同目标、版本或正文必须冲突，绝不覆盖。
	canonical, _ := json.Marshal(payload)
	fingerprint := tokenDigest(r.Method + "\n" + r.URL.Path + "\n" + r.Header.Get("If-Match") + "\n" + string(canonical))
	keyHash := tokenDigest(r.Header.Get("Idempotency-Key"))
	reserved, err := a.db.ExecContext(r.Context(), `INSERT OR IGNORE INTO integration_idempotency(token_id,key_hash,fingerprint,created_at)VALUES(?,?,?,?)`, key.ID, keyHash, fingerprint, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		fail(response, 503, "database_unavailable", "请求保护记录暂时不可用")
		return
	}
	n, _ := reserved.RowsAffected()
	if n == 0 {
		var storedFingerprint, etag string
		var status int
		var data []byte
		if err := a.db.QueryRowContext(r.Context(), `SELECT fingerprint,status,response_json,etag FROM integration_idempotency WHERE token_id=? AND key_hash=?`, key.ID, keyHash).Scan(&storedFingerprint, &status, &data, &etag); err != nil {
			fail(response, 503, "database_unavailable", "请求结果暂时无法读取")
			return
		}
		if storedFingerprint != fingerprint {
			fail(response, 409, "idempotency_conflict", "相同请求编号已用于不同内容，请核查后使用新编号")
			return
		}
		if status == 0 {
			fail(response, 409, "operation_pending", "请求正在处理或结果尚待核实，不可使用新编号重复写入")
			return
		}
		response.Header().Set("Idempotency-Replayed", "true")
		if etag != "" {
			response.Header().Set("ETag", etag)
		}
		response.WriteHeader(status)
		_, _ = response.Write(data)
		replayed = true
		return
	}
	copy := r.Clone(r.Context())
	copy.Body = io.NopCloser(bytes.NewReader(canonical))
	copy.ContentLength = int64(len(canonical))
	if route.id > 0 && route.sub == "" {
		copy = withIntegrationPrecondition(copy, route.legacy, route.id, r.Header.Get("If-Match"))
	}
	a.integrationForward(response, copy, route)
	integrationRedactFailure(response)
	// 5xx 也保留原结果：某些业务处理器可能已提交但在回读时失败，不能自动清空重试。
	if !response.overflow {
		if _, err := a.db.ExecContext(context.Background(), `UPDATE integration_idempotency SET status=?,response_json=?,etag=? WHERE token_id=? AND key_hash=?`, response.status, response.body.Bytes(), response.Header().Get("ETag"), key.ID, keyHash); err != nil {
			response = newIntegrationResponse()
			fail(response, 503, "result_unconfirmed", "结果尚待核实，请保留请求编号，勿重复提交")
		}
	}
}

// 原业务接口的老错误可能带 SQL 细节；外部协作边界统一脱敏，但保留
// 明确的结果待确认语义。错误也要缓存，不能把回读失败误当成没有写入。
func integrationRedactFailure(response *integrationResponse) {
	if response.status < 500 {
		return
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(response.body.Bytes(), &result)
	if result.Error.Code != "db_error" && result.Error.Code != "internal_error" {
		return
	}
	response.body.Reset()
	response.status = 0
	fail(response, 503, "service_unavailable", "服务暂时不可用；若这是写入请求，请保留请求编号并核查结果")
}

func (a *App) integrationForward(w http.ResponseWriter, r *http.Request, route integrationRoute) {
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/api/" + route.legacy
	if route.id > 0 {
		u.Path += "/" + strconv.FormatInt(route.id, 10)
	}
	if route.sub != "" {
		u.Path += "/" + route.sub
	}
	u.RawPath = ""
	copy.URL = &u
	copy.RequestURI = u.RequestURI()
	// 必须在重写成原业务路径之后鉴权，保留迭代管理、状态流转及通知规则。
	a.authorize(a.apiMux()).ServeHTTP(w, copy)
}

var errIntegrationReadConflict = errors.New("integration read changed")

func integrationReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "记录不存在或不属于当前项目")
	} else if errors.Is(err, errIntegrationReadConflict) {
		fail(w, 409, "read_conflict", "记录正在被更新，请重新读取")
	} else {
		fail(w, 503, "database_unavailable", "研发数据暂时无法读取")
	}
}

func (a *App) integrationReadEntity(r *http.Request, resource string, id int64) (map[string]any, string, error) {
	legacy := integrationResources[resource]
	before, err := a.integrationEntityETag(r.Context(), a.db, legacy, id)
	if err != nil {
		return nil, "", err
	}
	var data any
	switch resource {
	case "requirements":
		var requirement Requirement
		requirement, err = a.get(id)
		if err == nil {
			requirement.CustomFields, err = a.customFieldsUsing(a.db, "requirement", id)
		}
		data = requirement
	case "defects":
		data, err = a.getDefect(id)
	case "test-cases":
		data, err = a.getTestCase(id)
	case "iterations":
		var s Sprint
		err = a.db.QueryRowContext(r.Context(), `SELECT id,code,name,goal,start_date,end_date,status,capacity,updated_at FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&s.ID, &s.Code, &s.Name, &s.Goal, &s.StartDate, &s.EndDate, &s.Status, &s.Capacity, &s.UpdatedAt)
		data = s
	case "executions":
		var ex TestExecution
		err = a.db.QueryRowContext(r.Context(), `SELECT e.id,e.plan_id,e.case_id,e.status,e.executor,e.executed_at,e.note,e.defect_id,c.title,p.name,e.actual_result,e.executor_user_id FROM test_executions e JOIN test_cases c ON c.id=e.case_id AND c.project_id=e.project_id AND c.tenant_id=e.tenant_id JOIN test_plans p ON p.id=e.plan_id AND p.project_id=e.project_id AND p.tenant_id=e.tenant_id WHERE e.tenant_id=? AND e.project_id=? AND e.id=?`, tenantID, a.pid(), id).Scan(&ex.ID, &ex.PlanID, &ex.CaseID, &ex.Status, &ex.Executor, &ex.ExecutedAt, &ex.Note, &ex.DefectID, &ex.CaseTitle, &ex.PlanName, &ex.ActualResult, &ex.ExecutorUserID)
		data = ex
	default:
		return nil, "", sql.ErrNoRows
	}
	if err != nil {
		return nil, "", err
	}
	after, err := a.integrationEntityETag(r.Context(), a.db, legacy, id)
	if err != nil {
		return nil, "", err
	}
	if before != after {
		return nil, "", errIntegrationReadConflict
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, "", err
	}
	var object map[string]any
	if err = json.Unmarshal(b, &object); err != nil {
		return nil, "", err
	}
	object["_etag"] = after
	return object, after, nil
}

func integrationListQuery(resource string, values url.Values) (string, []any, int, int, error) {
	page, size := 1, 25
	if raw := values.Get("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 1000000 {
			return "", nil, 0, 0, errors.New("页码须为 1–1000000")
		}
		page = v
	}
	raw := values.Get("pageSize")
	if raw == "" {
		raw = values.Get("limit")
	}
	if raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 100 {
			return "", nil, 0, 0, errors.New("每页数量须为 1–100")
		}
		size = v
	}
	fields := map[string]string{"status": "status"}
	if resource != "executions" && resource != "iterations" {
		fields["priority"] = "priority"
	}
	if resource == "requirements" || resource == "defects" {
		fields["sprint"] = "sprint"
		fields["assigneeUserId"] = "assignee_user_id"
	}
	if resource == "defects" {
		fields["verifierUserId"] = "verifier_user_id"
		fields["requirementId"] = "requirement_id"
	}
	if resource == "test-cases" {
		fields["ownerUserId"] = "owner_user_id"
		fields["requirementId"] = "requirement_id"
		fields["caseType"] = "type"
		fields["category"] = "category"
	}
	if resource == "requirements" {
		fields["ownerUserId"] = "owner_user_id"
		fields["category"] = "category"
	}
	if resource == "executions" {
		fields["planId"] = "plan_id"
	}
	where := ""
	args := []any{}
	for key, vs := range values {
		if len(vs) != 1 || len(vs[0]) > 500 {
			return "", nil, 0, 0, errors.New("筛选值过长或重复")
		}
		value := vs[0]
		switch key {
		case "page", "pageSize", "limit":
			continue
		case "q":
			if resource == "executions" {
				return "", nil, 0, 0, errors.New("执行记录不支持关键词筛选")
			}
			if len([]rune(value)) > 200 {
				return "", nil, 0, 0, errors.New("关键词最多 200 字")
			}
			title := "title"
			if resource == "iterations" {
				title = "name"
			}
			where += " AND (" + title + " LIKE ? OR code LIKE ?)"
			args = append(args, "%"+value+"%", "%"+value+"%")
		case "updatedSince":
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return "", nil, 0, 0, errors.New("更新时间须使用 RFC3339")
			}
			where += " AND julianday(updated_at)>=julianday(?)"
			args = append(args, value)
		default:
			column := fields[key]
			if column == "" {
				return "", nil, 0, 0, fmt.Errorf("不支持筛选 %s", key)
			}
			if key == "requirementId" || key == "planId" {
				if _, ok := integrationPositive(value); !ok {
					return "", nil, 0, 0, errors.New("关联编号无效")
				}
			}
			where += " AND " + column + "=?"
			args = append(args, value)
		}
	}
	return where, args, page, size, nil
}

func (a *App) integrationListData(r *http.Request, resource string) (map[string]any, error) {
	where, args, page, size, err := integrationListQuery(resource, r.URL.Query())
	if err != nil {
		return nil, err
	}
	base := " FROM " + integrationTables[resource] + " WHERE tenant_id=? AND project_id=?" + where
	args = append([]any{tenantID, a.pid()}, args...)
	var total int
	if err = a.db.QueryRowContext(r.Context(), "SELECT COUNT(*)"+base, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(r.Context(), "SELECT id"+base+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, id := range ids {
		item, _, err := a.integrationReadEntity(r, resource, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "total": total, "page": page, "pageSize": size}, nil
}

func (a *App) integrationList(w http.ResponseWriter, r *http.Request, resource string) {
	if _, _, _, _, err := integrationListQuery(resource, r.URL.Query()); err != nil {
		fail(w, 400, "invalid_query", err.Error())
		return
	}
	data, err := a.integrationListData(r, resource)
	if err != nil {
		integrationReadError(w, err)
		return
	}
	write(w, 200, data)
}
