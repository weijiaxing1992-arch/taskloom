package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type scaleQueryCounter struct {
	db               *sql.DB
	queries, maxArgs int
}

func (q *scaleQueryCounter) QueryContext(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	q.queries++
	q.maxArgs = max(q.maxArgs, len(args))
	return q.db.QueryContext(ctx, statement, args...)
}
func scaleRows(t *testing.T, a *App) []Requirement {
	t.Helper()
	rows, err := a.db.Query(`SELECT `+requirementSelectColumns+` FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY id`, tenantID, a.pid())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	items := []Requirement{}
	for rows.Next() {
		var x Requirement
		if err := scanRequirement(rows, &x); err != nil {
			t.Fatal(err)
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return items
}
func scaleExec(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, query string, args ...any) sql.Result {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestDatabaseScaleBatchMatchesLegacyAndPreservesHistory(t *testing.T) {
	a := testApp(t)
	scaleExec(t, a.db, `UPDATE users SET active=0 WHERE id='u_front'`)
	scaleExec(t, a.db, `UPDATE requirements SET assignee_user_ids_json='["u_front","missing","u_front"]',assignee_user_id='u_front',owner_user_ids_json='[]',owner_user_id='u_pm' WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
	scaleExec(t, a.db, `UPDATE field_definitions SET enabled=0 WHERE tenant_id=? AND project_id=? AND key='business_value'`, tenantID, a.pid())
	old := scaleRows(t, a)
	batch := scaleRows(t, a)
	for i := range old {
		if err := a.loadRequirementAssignees(a.db, &old[i]); err != nil {
			t.Fatal(err)
		}
		values, err := a.checkedWorkCustomFields(context.Background(), "requirement", old[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		old[i].CustomFields = values
	}
	q := &scaleQueryCounter{db: a.db}
	if err := a.hydrateRequirementBatch(context.Background(), q, batch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old, batch) {
		t.Fatal("batch changed legacy bindings, order, names or custom fields")
	}
	if q.queries != 2 {
		t.Fatalf("queries=%d, want one name and one field batch", q.queries)
	}
	q = &scaleQueryCounter{db: a.db}
	if err := a.hydrateRequirementBatch(context.Background(), q, nil); err != nil || q.queries != 0 {
		t.Fatal("empty result queried database", err)
	}
}
func TestDatabaseScaleBatchScopeTombstonesAndInvalidJSON(t *testing.T) {
	a := testApp(t)
	items := scaleRows(t, a)
	id := items[0].ID
	foreign := scaleExec(t, a.db, `INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,created_at,updated_at) VALUES('other','other','requirement','secret','secret','text','x','x')`)
	fid, _ := foreign.LastInsertId()
	scaleExec(t, a.db, `INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,'requirement',?,?,?, 'x')`, tenantID, a.pid(), id, fid, `"secret"`)
	valid := scaleExec(t, a.db, `INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,created_at,updated_at) VALUES(?,?,'requirement','scale_deleted','deleted','text','x','x')`, tenantID, a.pid())
	deletedID, _ := valid.LastInsertId()
	scaleExec(t, a.db, `INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at)VALUES(?,?,'requirement',?,?,?, 'x')`, tenantID, a.pid(), id, deletedID, `"retained on disk"`)
	scaleExec(t, a.db, `UPDATE field_definitions SET deleted_at='deleted' WHERE id=?`, deletedID)
	if err := a.hydrateRequirementBatch(context.Background(), a.db, items); err != nil {
		t.Fatal(err)
	}
	if items[0].CustomFields["secret"] != nil || items[0].CustomFields["scale_deleted"] != nil {
		t.Fatal("cross-scope or deleted field leaked")
	}
	scaleExec(t, a.db, `UPDATE field_definitions SET deleted_at='' WHERE id=?`, deletedID)
	scaleExec(t, a.db, `UPDATE field_values SET value_json='not JSON' WHERE field_definition_id=?`, deletedID)
	if err := a.hydrateRequirementBatch(context.Background(), a.db, items); err == nil {
		t.Fatal("corrupted stored data silently ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.hydrateRequirementBatch(ctx, a.db, items); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}
func TestDatabaseScaleBatchPlaceholderBound(t *testing.T) {
	a := testApp(t)
	items := make([]Requirement, requirementHydrationBatch*2+1)
	for i := range items {
		items[i] = Requirement{ID: int64(100000 + i), AssigneeUserIDs: []string{fmt.Sprint("old-", i)}}
	}
	q := &scaleQueryCounter{db: a.db}
	if err := a.hydrateRequirementBatch(context.Background(), q, items); err != nil {
		t.Fatal(err)
	}
	if q.queries != 6 || q.maxArgs > requirementHydrationBatch+2 {
		t.Fatalf("unbounded query: %+v", q)
	}
	for _, x := range items {
		if len(x.Assignees) != 1 || x.Assignees[0].Name != x.AssigneeUserIDs[0] || x.CustomFields == nil {
			t.Fatal("lost unknown historical ID or empty map")
		}
	}
}
func scalePlan(t *testing.T, a *App, statement string, args ...any) string {
	t.Helper()
	rows, err := a.db.Query(`EXPLAIN QUERY PLAN `+statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "; ")
}
func TestDatabaseScaleIndexesAreIdempotentAndUsed(t *testing.T) {
	a := testApp(t)
	// 极小样本可能合理选择旧覆盖索引；用真实量级校验时间线排序规划。
	scaleExec(t, a.db, `WITH RECURSIVE counter(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM counter WHERE n<2000) INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key) SELECT ?,?,'u_admin','u_front','requirement.assigned','requirement',1,'synthetic','synthetic','2026-09-04T00:00:00Z','index-check-'||n FROM counter`, tenantID, a.pid())
	for i := 0; i < 2; i++ {
		if err := a.migrateDatabaseScale(); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		statement, index string
		args             []any
	}{
		{`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? AND sprint=? ORDER BY id`, "idx_req_scope_sprint_id", []any{tenantID, a.pid(), "V1.0"}},
		{`SELECT id FROM requirements WHERE tenant_id=? AND project_id=? AND category=? ORDER BY id`, "idx_req_scope_category_id", []any{tenantID, a.pid(), "待定"}},
		{`SELECT id FROM defects WHERE tenant_id=? AND project_id=? AND sprint=? ORDER BY id`, "idx_defect_scope_sprint_id", []any{tenantID, a.pid(), "V1.0"}},
		{`SELECT id FROM user_notifications WHERE tenant_id=? AND recipient_user_id=? ORDER BY created_at DESC,id DESC LIMIT 40`, "idx_notification_recipient_timeline", []any{tenantID, "u_admin"}},
	} {
		plan := scalePlan(t, a, test.statement, test.args...)
		t.Log(plan)
		if !strings.Contains(plan, test.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("index not used: %s", plan)
		}
	}
}

func TestDatabaseScaleQueryProjectionMatchesEveryRegisteredField(t *testing.T) {
	a := testApp(t)
	items := scaleRows(t, a)
	if err := a.hydrateRequirementBatch(context.Background(), a.db, items); err != nil {
		t.Fatal(err)
	}
	fields := baseWorkQueryFields()
	fields["cf.sample"] = "multi"
	for _, x := range items {
		x.CustomFields["sample"] = []any{"a", "b"}
		legacy, err := workRecord(x)
		if err != nil {
			t.Fatal(err)
		}
		for key, kind := range fields {
			query := &workItemQuery{Fields: fields, Sort: key}
			record, err := requirementQueryRecord(x, query)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(workValue(legacy, key), workValue(record, key)) || !reflect.DeepEqual(workSortValue(legacy, key, nil), workSortValue(record, key, nil)) {
				t.Fatalf("changed query semantics: %s (%s)", key, kind)
			}
		}
	}
}

func TestDatabaseScalePagingPreservesFullFilteredOrderAndIsolation(t *testing.T) {
	a := testApp(t)
	filter := url.QueryEscape(`[{"field":"title","operator":"not_contains","value":"不会匹配的词"}]`)
	base := "/api/requirements?sort=title&order=asc&filters=" + filter
	w := apiRequest(a, "GET", base, "u_admin", a.pid(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var full struct {
		Items []Requirement
		Total int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &full); err != nil {
		t.Fatal(err)
	}
	collected := []Requirement{}
	for page := 1; len(collected) < full.Total; page++ {
		w := apiRequest(a, "GET", fmt.Sprintf("%s&page=%d&pageSize=2", base, page), "u_admin", a.pid(), "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var result struct {
			Items                 []Requirement
			Total, Page, PageSize int
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Total != full.Total || result.Page != page || result.PageSize != 2 {
			t.Fatal("incorrect page metadata", w.Body.String())
		}
		collected = append(collected, result.Items...)
	}
	if !reflect.DeepEqual(full.Items, collected) {
		t.Fatal("paged filtering/order differs from legacy full list")
	}
	w = apiRequest(a, "GET", base+"&page=100000&pageSize=2", "u_admin", a.pid(), "")
	if jsonMap(t, w)["page"].(float64) != float64((full.Total+1)/2) {
		t.Fatal("out-of-range page not clamped")
	}
	w = apiRequest(a, "GET", base+"&page=1&pageSize=2", "u_front", insightProjectID, "")
	if w.Code != 403 {
		t.Fatal("paging bypassed project permission", w.Code)
	}
	for _, query := range []string{"page=0", "page=-1", "page=1.5", "page=100001", "pageSize=0", "pageSize=201", "pageSize=abc", "projection=other"} {
		w := apiRequest(a, "GET", "/api/requirements?"+query, "u_admin", a.pid(), "")
		if w.Code != 422 {
			t.Fatal("invalid paging", query, w.Code)
		}
	}
}

func TestDatabaseScaleProjectionOmitsDocumentButNotPlaintextOrDetails(t *testing.T) {
	a := testApp(t)
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Large rich document"}]}]}`
	scaleExec(t, a.db, `UPDATE requirements SET description_doc_json=?,description='Plain searchable text' WHERE tenant_id=? AND project_id=?`, doc, tenantID, a.pid())
	w := apiRequest(a, "GET", "/api/requirements?page=1&pageSize=1&projection=list", "u_admin", a.pid(), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	item := jsonMap(t, w)["items"].([]any)[0].(map[string]any)
	if item["descriptionDoc"] != nil || item["description"] != "Plain searchable text" {
		t.Fatal("incorrect projection", item)
	}
	w = apiRequest(a, "GET", fmt.Sprint("/api/requirements/", item["id"]), "u_admin", a.pid(), "")
	if jsonMap(t, w)["descriptionDoc"] == nil {
		t.Fatal("detail lost rich document")
	}
	w = apiRequest(a, "GET", "/api/requirements?projection=reference", "u_admin", a.pid(), "")
	items := jsonMap(t, w)["items"].([]any)
	if len(items) < 2 {
		t.Fatal("reference silently truncated")
	}
	for _, raw := range items {
		row := raw.(map[string]any)
		if len(row) != 7 || row["id"] == nil || row["title"] == nil || row["description"] != nil || row["descriptionDoc"] != nil {
			t.Fatal("wrong reference projection", row)
		}
	}
}

// 显式开启才运行；固定使用 t.TempDir() 和真实 WAL/连接池配置。
// 不接受外部 URL 或业务库路径，防止误用基准脚本污染真实数据。
func TestDatabaseScale20K(t *testing.T) {
	if os.Getenv("DEVFLOW_SCALE_BENCH") != "1" {
		t.Skip("set DEVFLOW_SCALE_BENCH=1 to run the isolated 20,000-item benchmark")
	}
	a := fileSQLiteTestApp(t)
	w := apiRequest(a, "POST", "/api/projects", "u_admin", projectID, `{"name":"Scale fixture","code":"SCALE"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.project = "prj_scale"
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stamp := "2026-09-04T08:00:00Z"
	var firstSprint int64
	for i := 0; i < 100; i++ {
		result := scaleExec(t, tx, `INSERT INTO sprints(tenant_id,project_id,code,name,start_date,end_date,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, a.pid(), fmt.Sprint("SC-", i), fmt.Sprintf("Scale iteration %03d", i), "2026-09-01", "2026-09-30", "进行中", stamp, stamp)
		if i == 0 {
			firstSprint, _ = result.LastInsertId()
		}
	}
	field := scaleExec(t, tx, `INSERT INTO field_definitions(tenant_id,project_id,object_type,key,name,type,filterable,created_at,updated_at) VALUES(?,?,'requirement','scale_score','Scale score','number',1,?,?)`, tenantID, a.pid(), stamp, stamp)
	fid, _ := field.LastInsertId()
	statement, err := tx.Prepare(`INSERT INTO requirements(tenant_id,project_id,code,title,description,description_doc_json,category,sprint,status,assignee_user_id,assignee_user_ids_json,owner_user_id,owner_user_ids_json,role_weights_json,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	text := strings.Repeat("开发规范测试描述。", 40)
	doc := jsonText(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}})
	for i := 0; i < 20000; i++ {
		result, err := statement.Exec(tenantID, a.pid(), fmt.Sprintf("SCALE-%05d", i), fmt.Sprintf("基准需求 %05d", i), text, doc, []string{"客户端", "管理端", "全球化", "定制开发", "待定"}[i%5], fmt.Sprintf("Scale iteration %03d", i%100), "规划中", "u_admin", `["u_admin","u_front"]`, "u_pm", `["u_pm"]`, `{"frontend":{"userIds":["u_front"],"value":20},"backend":{"userIds":["u_back"],"value":50}}`, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		scaleExec(t, tx, `INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at) VALUES(?,?,'requirement',?,?,?,?)`, tenantID, a.pid(), id, fid, fmt.Sprint(i%101), stamp)
		scaleExec(t, tx, `INSERT INTO user_notifications(tenant_id,project_id,recipient_user_id,actor_user_id,event_type,subject_type,subject_id,title,body,created_at,dedupe_key)VALUES(?,?,'u_admin','u_front','requirement.assigned','requirement',?,'Scale notification','synthetic fixture',?,?)`, tenantID, a.pid(), id, stamp, fmt.Sprint("scale-", i))
		if i%10 == 0 {
			scaleExec(t, tx, `INSERT INTO defects(tenant_id,project_id,code,title,sprint,status,assignee_user_id,created_at,updated_at)VALUES(?,?,?,?,?,'新建','u_front',?,?)`, tenantID, a.pid(), fmt.Sprint("SC-BUG-", i), "Synthetic defect", fmt.Sprintf("Scale iteration %03d", i%100), stamp, stamp)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	items := scaleRows(t, a)
	if len(items) != 20000 {
		t.Fatal("wrong fixture size", len(items))
	}
	var sqliteVersion string
	a.db.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion)
	t.Logf("ENV go=%s os=%s arch=%s cpu=%d sqlite=%s rows=%d description_bytes=%d document_bytes=%d", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), sqliteVersion, len(items), len(text), len(doc))
	measure := func(name string, fn func() int) {
		durations := []float64{}
		bytes := 0
		var allocated uint64
		for i := 0; i < 3; i++ {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			bytes = fn()
			durations = append(durations, float64(time.Since(start).Microseconds())/1000)
			runtime.ReadMemStats(&after)
			allocated += after.TotalAlloc - before.TotalAlloc
		}
		sort.Float64s(durations)
		t.Logf("MEASURE %s median_ms=%.3f min_ms=%.3f max_ms=%.3f response_bytes=%d average_alloc_bytes=%d samples=3", name, durations[1], durations[0], durations[2], bytes, allocated/3)
	}
	measure("legacy_hydration", func() int {
		copyItems := append([]Requirement(nil), items...)
		for i := range copyItems {
			if err := a.loadRequirementAssignees(a.db, &copyItems[i]); err != nil {
				t.Fatal(err)
			}
			values, err := a.checkedWorkCustomFields(context.Background(), "requirement", copyItems[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			copyItems[i].CustomFields = values
		}
		return 0
	})
	measure("batch_hydration", func() int {
		copyItems := append([]Requirement(nil), items...)
		q := &scaleQueryCounter{db: a.db}
		if err := a.hydrateRequirementBatch(context.Background(), q, copyItems); err != nil {
			t.Fatal(err)
		}
		if q.queries != 51 {
			t.Fatal("unexpected batch count", q.queries)
		}
		return 0
	})
	measure("legacy_filter_records", func() int {
		for _, item := range items {
			if _, err := workRecord(item); err != nil {
				t.Fatal(err)
			}
		}
		return 0
	})
	measure("projected_filter_records", func() int {
		for _, item := range items {
			if _, err := requirementQueryRecord(item, &workItemQuery{Sort: "updatedAt"}); err != nil {
				t.Fatal(err)
			}
		}
		return 0
	})
	t.Log("ASSOCIATIONS legacy_queries=60000 batch_queries=51 max_batch_ids=400")
	cookieWriter := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/requirements", nil)
	cookie, err := a.issueSession(cookieWriter, request, "u_admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, path string }{
		{"requirements_full", "/api/requirements"},
		{"requirements_first_page", "/api/requirements?page=1&pageSize=30&projection=list"},
		{"requirements_category", "/api/requirements?category=客户端&page=1&pageSize=30&projection=list"},
		{"search_first_page", "/api/search?q=基准需求&type=需求&project=prj_scale&limit=30"},
		{"sprint_200_items", fmt.Sprint("/api/sprints/", firstSprint)},
		{"my_work", "/api/my-work?project=prj_scale"},
		{"notifications_first_page", "/api/notifications?limit=30"},
		{"workload_month", "/api/reports/workload?month=2026-09"},
	} {
		measure(entry.name, func() int {
			request := httptest.NewRequest("GET", entry.path, nil)
			request.Header.Set("X-DevFlow-Project", a.pid())
			request.AddCookie(cookie)
			w := httptest.NewRecorder()
			a.scopedAPI().ServeHTTP(w, request)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", entry.name, w.Code, w.Body.String())
			}
			return w.Body.Len()
		})
	}
	var check string
	if err := a.db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		t.Fatal("integrity", check, err)
	}
}
