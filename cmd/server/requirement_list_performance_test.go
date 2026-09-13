package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"testing"
)

func assertRequirementPageMatchesUnpaged(t *testing.T, a *App, query string) {
	t.Helper()
	full := apiRequest(a, http.MethodGet, "/api/requirements?"+query, "u_admin", a.pid(), "")
	if full.Code != http.StatusOK {
		t.Fatalf("unpaged %s: %d %s", query, full.Code, full.Body.String())
	}
	legacy := jsonMap(t, full)
	items := legacy["items"].([]any)
	for _, requestedPage := range []int{1, 2, 100000} {
		const size = 2
		page := min(requestedPage, max(1, (len(items)+size-1)/size))
		start := (page - 1) * size
		want := map[string]any{"items": items[start:min(start+size, len(items))], "total": float64(len(items)), "page": float64(page), "pageSize": float64(size)}
		w := apiRequest(a, http.MethodGet, fmt.Sprintf("/api/requirements?%s&page=%d&pageSize=%d", query, requestedPage, size), "u_admin", a.pid(), "")
		if w.Code != http.StatusOK {
			t.Fatalf("page %d %s: %d %s", requestedPage, query, w.Code, w.Body.String())
		}
		if got := jsonMap(t, w); !reflect.DeepEqual(got, want) {
			t.Fatalf("page %d differs from complete-result filtering/order for %s\ngot: %#v\nwant: %#v", requestedPage, query, got, want)
		}
	}
}

func TestRequirementSQLPageMatchesLegacySortsFiltersAndProjections(t *testing.T) {
	a := testApp(t)
	// Include ties, numeric zero, empty dates, an off-page parent and legacy
	// codes whose lexical order differs from canonical numeric IDs.
	scaleExec(t, a.db, `UPDATE requirements SET updated_at='2026-09-03T08:00:00Z',created_at='2026-09-02T08:00:00Z',assignee_user_ids_json='["u_front","u_back"]',assignee_user_id='u_front',owner_user_ids_json='["u_pm"]',owner_user_id='u_pm' WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
	for index, id := range []int64{20, 101, 1001} {
		var parent any
		if index > 0 {
			parent = int64(20)
		}
		scaleExec(t, a.db, `INSERT INTO requirements(id,tenant_id,project_id,code,title,parent_id,category,sprint,status,priority,progress,estimated_hours,actual_hours,iteration_delay_count,sensitive,auth_impact,start_date,end_date,created_at,updated_at,assignee_user_ids_json,owner_user_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, tenantID, a.pid(), []string{"REQ-Z", "REQ-A", "REQ-B"}[index], "分页验证 "+strconv.Itoa(index), parent, "客户端", "V1.0", "规划中", []string{"P2", "P0", "P1"}[index], index*10, []float64{0, 10.5, 2}[index], []float64{10, 0, 2.5}[index], index, index%2, (index+1)%2, []string{"", "2026-09-01", "2026-09-02"}[index], []string{"2026-09-30", "", "2026-09-15"}[index], []string{"", "2026-09-02t08:00:00z", "2026-09-03T08:00:00Z"}[index], []string{"2026-09-03T08:00:00Z", "", "2026-09-03t08:00:00z"}[index], `["u_back"]`, "u_pm")
	}
	for _, field := range []string{"code", "updatedAt", "createdAt", "startDate", "endDate", "priority", "parentId", "progress", "estimatedHours", "actualHours", "iterationDelayCount", "sensitive", "authImpact"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(field+"/"+order, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "/api/requirements?page=1&sort="+field+"&order="+order, nil)
				parsed, err := a.parseRequirementListQuery(r)
				if err != nil {
					t.Fatal(err)
				}
				page, err := parseRequirementPage(r)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, ok := requirementListSQLSort(r, page, parsed); !ok {
					t.Fatal("expected SQL pagination")
				}
				assertRequirementPageMatchesUnpaged(t, a, "sort="+field+"&order="+order)
			})
		}
	}
	for _, query := range []string{
		"", "projection=list", "projection=reference&sort=code&order=asc",
		"status=" + url.QueryEscape("规划中"), "statuses=" + url.QueryEscape(`["规划中","开发中"]`), "statusCategory=done",
		"category=" + url.QueryEscape("客户端"), "priority=P1", "sprint=V1.0", "sprint=missing",
		"assignee=" + url.QueryEscape("周屿"), "assigneeUserId=u_back", "mine=1",
		"q=" + url.QueryEscape("分页验证"), "q=REQ-000020", "q=000020", "q=absent-no-matches",
		"category=" + url.QueryEscape("客户端") + "&priority=P1&sprint=V1.0&assigneeUserId=u_back",
	} {
		t.Run("query/"+query, func(t *testing.T) { assertRequirementPageMatchesUnpaged(t, a, query) })
	}
}

func TestRequirementSQLPageComplexQueriesKeepCompleteResultFallback(t *testing.T) {
	a := testApp(t)
	for _, query := range []string{
		"sort=title", "sort=owner", "sort=assignee", "sort=tags", "sort=weightTotal", "sort=role.frontend.value", "sort=dependencyState",
		"sort=cf.business_value", "cf.business_value=" + url.QueryEscape("高"),
		"filters=" + url.QueryEscape(`[{"field":"progress","operator":"eq","value":0}]`),
		"filters=" + url.QueryEscape(`[{"field":"createdAt","operator":"eq","value":"2026-09-03"}]`),
		"filters=" + url.QueryEscape(`[{"field":"dependencyState","operator":"is_empty"}]`),
	} {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/requirements?page=1&"+query, nil)
			parsed, err := a.parseRequirementListQuery(r)
			if err != nil {
				t.Fatal(err)
			}
			page, err := parseRequirementPage(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, ok := requirementListSQLSort(r, page, parsed); ok {
				t.Fatal("complex query must retain complete-result semantics")
			}
			assertRequirementPageMatchesUnpaged(t, a, query)
		})
	}
	// Unicode folding differs in Go and SQLite. Unexpected legacy dates or
	// priorities must keep the old comparator, including case-insensitive ties.
	scaleExec(t, a.db, `UPDATE requirements SET priority=CASE id WHEN 1 THEN 'Ä' WHEN 2 THEN 'ä' ELSE priority END,updated_at=CASE id WHEN 1 THEN 'Ä' WHEN 2 THEN 'ä' ELSE updated_at END WHERE tenant_id=? AND project_id=?`, tenantID, a.pid())
	for _, field := range []string{"priority", "updatedAt"} {
		r := httptest.NewRequest(http.MethodGet, "/api/requirements?page=1&sort="+field, nil)
		parsed, err := a.parseRequirementListQuery(r)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := parseRequirementPage(r)
		result, err := a.readRequirementSQLPage(r.Context(), r, page, parsed, ` FROM requirements WHERE tenant_id=? AND project_id=?`, []any{tenantID, a.pid()})
		if err != nil || result != nil {
			t.Fatalf("Unicode values need fallback: result=%#v error=%v", result, err)
		}
		assertRequirementPageMatchesUnpaged(t, a, "sort="+field)
	}
}

func TestRequirementSQLPageOnlyScansAndHydratesRequestedRows(t *testing.T) {
	a := testApp(t)
	scaleExec(t, a.db, `WITH RECURSIVE series(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM series WHERE n<1200) INSERT INTO requirements(tenant_id,project_id,code,title,assignee_user_ids_json,created_at,updated_at) SELECT ?,?,'legacy','分页性能样本 '||n,'["u_front"]','2026-09-03T08:00:00Z','2026-09-03T08:00:00Z' FROM series`, tenantID, a.pid())
	var lastID int64
	var total int
	if err := a.db.QueryRow(`SELECT MAX(id),COUNT(*) FROM requirements WHERE tenant_id=? AND project_id=?`, tenantID, a.pid()).Scan(&lastID, &total); err != nil {
		t.Fatal(err)
	}
	// Invalid associated JSON is a read error if hydrated. A valid first page
	// must succeed while the final page still reports its own invalid record.
	scaleExec(t, a.db, `INSERT INTO field_values(tenant_id,project_id,object_type,object_id,field_definition_id,value_json,updated_at) SELECT tenant_id,project_id,'requirement',?,id,'invalid JSON','x' FROM field_definitions WHERE tenant_id=? AND project_id=? AND object_type='requirement' AND key='business_value'`, lastID, tenantID, a.pid())
	first := "/api/requirements?page=1&pageSize=30&projection=list&sort=code&order=asc"
	w := apiRequest(a, http.MethodGet, first, "u_admin", a.pid(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("first page hydrated an off-page record: %d %s", w.Code, w.Body.String())
	}
	response := jsonMap(t, w)
	if response["total"] != float64(total) || len(response["items"].([]any)) != 30 {
		t.Fatal("SQL count or page size changed", response)
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements?page=100000&pageSize=30&sort=code&order=asc", "u_admin", a.pid(), "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("on-page invalid custom field must fail hydration", w.Code)
	}
	// Malformed base-row JSON outside the page proves SELECT is limited before
	// scanRequirement as well as before association hydration.
	scaleExec(t, a.db, `UPDATE requirements SET role_weights_json='invalid JSON' WHERE id=?`, lastID)
	w = apiRequest(a, http.MethodGet, first, "u_admin", a.pid(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("first page scanned an off-page record: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodGet, "/api/requirements?sort=code&order=asc", "u_admin", a.pid(), "")
	if w.Code != http.StatusInternalServerError {
		t.Fatal("unpaged list must still load its complete result", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, first, nil)
	parsed, err := a.parseRequirementListQuery(r)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := parseRequirementPage(r)
	result, err := a.readRequirementSQLPage(context.Background(), r, page, parsed, ` FROM requirements WHERE tenant_id=? AND project_id=?`, []any{tenantID, a.pid()})
	if err != nil || result == nil || len(result.items) != 30 || result.total != total {
		t.Fatalf("SQL page returned more than requested: result=%#v err=%v", result, err)
	}
	counter := &scaleQueryCounter{db: a.db}
	if err := a.hydrateRequirementBatch(r.Context(), counter, result.items); err != nil {
		t.Fatal(err)
	}
	if counter.maxArgs > 32 || counter.queries > 2 {
		t.Fatalf("hydration exceeded page bounds: queries=%d args=%d", counter.queries, counter.maxArgs)
	}
	t.Logf("scoped_rows=%d scanned_rows=%d hydration_queries=%d max_query_arguments=%d", total, len(result.items), counter.queries, counter.maxArgs)
}

func TestRequirementSQLPagePreservesScopeAndCancellation(t *testing.T) {
	a := testApp(t)
	foreign := planningRequirement(t, a, `{"title":"计数不能包含其他租户"}`)
	scaleExec(t, a.db, `UPDATE requirements SET tenant_id='foreign-tenant' WHERE id=?`, foreign.ID)
	otherProject := *a
	otherProject.project = insightProjectID
	planningRequirement(t, &otherProject, `{"title":"计数不能包含其他项目"}`)
	assertRequirementPageMatchesUnpaged(t, a, "sort=code")
	w := apiRequest(a, http.MethodGet, "/api/requirements?page=1&pageSize=2&sort=code", "u_front", insightProjectID, "")
	if w.Code != http.StatusForbidden {
		t.Fatal("SQL paging bypassed project access", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/requirements?page=1", nil)
	parsed, err := a.parseRequirementListQuery(r)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := parseRequirementPage(r)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	if _, err := a.readRequirementSQLPage(ctx, r, page, parsed, ` FROM requirements WHERE tenant_id=? AND project_id=?`, []any{tenantID, a.pid()}); err == nil {
		t.Fatal("cancelled request continued database reads")
	}
}
