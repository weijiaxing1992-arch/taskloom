package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func favoriteRequest(t *testing.T, a *App, method, user, project string, id int64) requirementFavoriteState {
	t.Helper()
	w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d/favorite", id), user, project, "")
	if w.Code != 200 {
		t.Fatalf("favorite %s: %d %s", method, w.Code, w.Body.String())
	}
	var state requirementFavoriteState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.RequirementID != id {
		t.Fatalf("wrong requirement: %+v", state)
	}
	return state
}
func favoriteList(t *testing.T, a *App, user, project, query string) []favoriteWorkItem {
	t.Helper()
	w := apiRequest(a, http.MethodGet, "/api/my-work?view=favorites&"+query, user, project, "")
	if w.Code != 200 {
		t.Fatalf("favorites list: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Items []favoriteWorkItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func TestRequirementFavoritesPersonalIdempotenceAndViewerAccess(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"不是我负责的收藏","assigneeUserIds":["u_back"]}`)
	if err := a.migrateRequirementFavorites(); err != nil {
		t.Fatal(err)
	}
	if state := favoriteRequest(t, a, http.MethodGet, "u_viewer", projectID, x.ID); state.Favorited || state.CreatedAt != "" {
		t.Fatalf("new state: %+v", state)
	}
	first := favoriteRequest(t, a, http.MethodPut, "u_viewer", projectID, x.ID)
	again := favoriteRequest(t, a, http.MethodPut, "u_viewer", projectID, x.ID)
	if !first.Favorited || first.CreatedAt == "" || first.CreatedAt != again.CreatedAt {
		t.Fatalf("PUT must be idempotent: %+v %+v", first, again)
	}
	if state := favoriteRequest(t, a, http.MethodGet, "u_admin", projectID, x.ID); state.Favorited {
		t.Fatal("another user's favorite leaked")
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM requirement_favorites WHERE requirement_id=?`, x.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("favorite duplicates: %d %v", count, err)
	}
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d", x.ID), "u_viewer", projectID, `{"title":"forbidden"}`)
		if w.Code != 403 {
			t.Fatalf("favorite bypass granted business write: %d %s", w.Code, w.Body.String())
		}
	}
	items := favoriteList(t, a, "u_viewer", projectID, "project=all")
	if len(items) != 1 || items[0].ID != x.ID || items[0].Role != "favorite" || !items[0].Favorited {
		t.Fatalf("other-assignee favorite absent: %+v", items)
	}
	w := apiRequest(a, http.MethodGet, "/api/my-work?project=all&q="+url.QueryEscape(x.Title), "u_viewer", projectID, "")
	if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 0 {
		t.Fatalf("assigned work was contaminated: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		if state := favoriteRequest(t, a, http.MethodDelete, "u_viewer", projectID, x.ID); state.Favorited || state.CreatedAt != "" {
			t.Fatalf("DELETE not idempotent: %+v", state)
		}
	}
	if len(favoriteList(t, a, "u_viewer", projectID, "project=all")) != 0 {
		t.Fatal("removed favorite still listed")
	}
}

func TestRequirementFavoritesProjectTenantAndRevokedAccessIsolation(t *testing.T) {
	a := testApp(t)
	first := planningRequirement(t, a, `{"title":"visible-main"}`)
	scoped := *a
	scoped.project = insightProjectID
	second := planningRequirement(t, &scoped, `{"title":"visible-insight"}`)
	favoriteRequest(t, a, http.MethodPut, "u_viewer", projectID, first.ID)
	favoriteRequest(t, a, http.MethodPut, "u_viewer", insightProjectID, second.ID)
	if items := favoriteList(t, a, "u_viewer", projectID, "project=all"); len(items) != 2 {
		t.Fatalf("accessible cross-project favorites: %+v", items)
	}
	if items := favoriteList(t, a, "u_viewer", projectID, ""); len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("default scope mismatch: %+v", items)
	}
	if items := favoriteList(t, a, "u_viewer", projectID, "project="+insightProjectID); len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("selected project mismatch: %+v", items)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d/favorite", first.ID), "u_viewer", insightProjectID, "")
		if w.Code != 404 {
			t.Fatalf("cross-project favorite: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_viewer'`, tenantID, insightProjectID); err != nil {
		t.Fatal(err)
	}
	if items := favoriteList(t, a, "u_viewer", projectID, "project=all"); len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("revoked metadata leaked: %+v", items)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d/favorite", second.ID), "u_viewer", insightProjectID, "")
		if w.Code != 403 {
			t.Fatalf("revoked project was accessible: %d %s", w.Code, w.Body.String())
		}
	}
	// Even stale/malformed favorite rows cannot cross tenant or project joins.
	if _, err := a.db.Exec(`INSERT INTO requirement_favorites(tenant_id,project_id,user_id,requirement_id,created_at)VALUES('other-tenant',?,'u_viewer',?,'2099-01-01'),(?,?,'u_viewer',?,'2099-01-01')`, projectID, first.ID, tenantID, projectID, second.ID); err != nil {
		t.Fatal(err)
	}
	if items := favoriteList(t, a, "u_viewer", projectID, "project=all"); len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("malformed association leaked: %+v", items)
	}
	if _, err := a.db.Exec(`UPDATE requirements SET tenant_id='other-tenant' WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	if items := favoriteList(t, a, "u_viewer", projectID, "project=all"); len(items) != 0 {
		t.Fatalf("foreign requirement leaked: %+v", items)
	}
}

func TestRequirementFavoritesDeletedAndInactiveRecordsFailClosed(t *testing.T) {
	for _, scenario := range []string{"deleted", "inactive-user", "inactive-membership"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			x := planningRequirement(t, a, `{"title":"private favorite"}`)
			favoriteRequest(t, a, http.MethodPut, "u_viewer", projectID, x.ID)
			query := `DELETE FROM requirements WHERE id=?`
			args := []any{x.ID}
			want := 404
			if scenario == "inactive-user" {
				query = `UPDATE users SET active=0 WHERE id='u_viewer'`
				args = nil
				want = 401
			}
			if scenario == "inactive-membership" {
				query = `UPDATE tenant_memberships SET status='disabled' WHERE user_id='u_viewer'`
				args = nil
				want = 401
			}
			if _, err := a.db.Exec(query, args...); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
				w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d/favorite", x.ID), "u_viewer", projectID, "")
				if w.Code != want {
					t.Fatalf("%s expected %d: %d %s", scenario, want, w.Code, w.Body.String())
				}
			}
			w := apiRequest(a, http.MethodGet, "/api/my-work?view=favorites&project=all", "u_viewer", projectID, "")
			if scenario == "deleted" {
				if w.Code != 200 || len(jsonMap(t, w)["items"].([]any)) != 0 {
					t.Fatalf("deleted requirement in list: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 401 {
				t.Fatalf("inactive personal data accessible: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRequirementFavoritesFilteringStatusSortingAndCounts(t *testing.T) {
	a := testApp(t)
	alpha := planningRequirement(t, a, `{"title":"Favorite Alpha","status":"草稿","priority":"P2"}`)
	beta := planningRequirement(t, a, `{"title":"Favorite Beta","priority":"P0","endDate":"2026-09-01"}`)
	beta = patchPlanningRequirement(t, a, beta.ID, `{"status":"已完成"}`)
	favoriteRequest(t, a, http.MethodPut, "u_front", projectID, alpha.ID)
	favoriteRequest(t, a, http.MethodPut, "u_front", projectID, beta.ID)
	if _, err := a.db.Exec(`UPDATE requirement_favorites SET created_at=CASE requirement_id WHEN ? THEN '2026-09-03T10:00:00Z' ELSE '2026-09-03T10:00:00.001Z' END WHERE user_id='u_front'`, alpha.ID); err != nil {
		t.Fatal(err)
	}
	if items := favoriteList(t, a, "u_front", projectID, ""); len(items) != 2 || items[0].ID != beta.ID {
		t.Fatalf("nanosecond favorite timestamps sorted lexically: %+v", items)
	}
	for _, tc := range []struct {
		query string
		ids   []int64
	}{{"sort=title&order=asc", []int64{alpha.ID, beta.ID}}, {"sort=title&order=desc", []int64{beta.ID, alpha.ID}}, {"sort=priority&order=asc", []int64{beta.ID, alpha.ID}}, {"sort=dueDate&order=desc", []int64{beta.ID, alpha.ID}}, {"sort=code&order=desc", []int64{beta.ID, alpha.ID}}, {"q=alpha", []int64{alpha.ID}}, {"status=" + url.QueryEscape("已完成"), []int64{beta.ID}}, {"category=completed", []int64{beta.ID}}, {"type=" + url.QueryEscape("缺陷"), nil}} {
		items := favoriteList(t, a, "u_front", projectID, tc.query)
		if len(items) != len(tc.ids) {
			t.Fatalf("%s got %+v", tc.query, items)
		}
		for i, id := range tc.ids {
			if items[i].ID != id {
				t.Fatalf("%s ordering mismatch %+v", tc.query, items)
			}
		}
	}
	w := apiRequest(a, http.MethodGet, "/api/my-work?view=favorites&category=completed", "u_front", projectID, "")
	counts := jsonMap(t, w)["counts"].(map[string]any)
	if counts["all"] != float64(2) || counts["completed"] != float64(1) || counts["todo"] != float64(1) {
		t.Fatalf("counts should precede category filter: %s", w.Body.String())
	}
	for _, q := range []string{"sort=" + url.QueryEscape("title;DROP TABLE requirements"), "order=sideways"} {
		w = apiRequest(a, http.MethodGet, "/api/my-work?view=favorites&"+q, "u_front", projectID, "")
		if w.Code != 422 {
			t.Fatalf("invalid sort accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRequirementFavoritesEntireAccessibleSetIsNotTruncated(t *testing.T) {
	a := testApp(t)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1205; i++ {
		result, err := tx.Exec(`INSERT INTO requirements(tenant_id,project_id,code,title,created_at,updated_at)VALUES(?,?,?,?,?,?)`, tenantID, projectID, fmt.Sprintf("FAV-%d", i), fmt.Sprintf("Bulk favorite %d", i), "2026-09-01T00:00:00Z", "2026-09-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO requirement_favorites(tenant_id,project_id,user_id,requirement_id,created_at)VALUES(?,?,'u_viewer',?,?)`, tenantID, projectID, id, "2026-09-03T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	items := favoriteList(t, a, "u_viewer", projectID, "project=all&sort=code&order=asc")
	if len(items) != 1205 {
		t.Fatalf("favorites truncated: %d", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i].ID <= items[i-1].ID {
			t.Fatal("numeric ordering not stable")
		}
	}
}

func TestRequirementFavoriteMutationWhitelistIsExact(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"route safety"}`)
	for _, path := range []string{fmt.Sprintf("/api/requirements/%d/favorite/extra", x.ID), fmt.Sprintf("/api/requirements/%d/comments", x.ID), fmt.Sprintf("/api/requirements/%d/favorites", x.ID), "/api/requirements/-1/favorite", "/api/requirements/NaN/favorite"} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			r := httptest.NewRequest(method, path, nil)
			if requirementFavoriteMutation(r) {
				t.Fatalf("broad preference bypass: %s %s", method, path)
			}
			w := apiRequest(a, method, path, "u_viewer", projectID, "")
			if w.Code != 403 {
				t.Fatalf("viewer adjacent write: %d %s", w.Code, w.Body.String())
			}
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		w := apiRequest(a, method, fmt.Sprintf("/api/requirements/%d/favorite", x.ID), "u_admin", projectID, "")
		if w.Code != 405 {
			t.Fatalf("unsupported method accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.scopedAPI().ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/requirements/%d/favorite", x.ID), nil))
	if w.Code != 401 {
		t.Fatalf("anonymous favorite state leaked: %d %s", w.Code, w.Body.String())
	}
}

func TestRequirementFavoritesDatabaseFailureIsNotAnEmptySuccess(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"failure safety"}`)
	if _, err := a.db.Exec(`DROP TABLE requirement_favorites`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fmt.Sprintf("/api/requirements/%d/favorite", x.ID), "/api/my-work?view=favorites&project=all"} {
		w := apiRequest(a, http.MethodGet, path, "u_front", projectID, "")
		if w.Code != 503 || strings.Contains(w.Body.String(), "SQL") {
			t.Fatalf("failure leaked/internal or success: %d %s", w.Code, w.Body.String())
		}
	}
}
