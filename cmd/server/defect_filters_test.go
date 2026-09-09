package main

import (
	"net/http"
	"sort"
	"testing"
)

func defectFilterCodes(t *testing.T, wBody map[string]any) []string {
	t.Helper()
	items, ok := wBody["items"].([]any)
	if !ok {
		t.Fatalf("missing defect items: %#v", wBody)
	}
	codes := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("invalid defect item: %#v", raw)
		}
		code, ok := item["code"].(string)
		if !ok {
			t.Fatalf("missing defect code: %#v", item)
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func TestDefectListFiltersUseStablePeopleIDsAndCurrentProject(t *testing.T) {
	a := testApp(t)
	insert := func(project, code, assigneeID, verifierID string) {
		t.Helper()
		_, err := a.db.Exec(`INSERT INTO defects(tenant_id,project_id,code,title,status,severity,assignee_user_id,verifier_user_id,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?)`, tenantID, project, code, "defect-filter "+code, "新建", "一般", assigneeID, verifierID, "2026-09-04T00:00:00Z", "2026-09-04T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(projectID, "FILTER-OWN", "u_front", "u_qa")
	insert(projectID, "FILTER-VERIFY", "u_qa", "u_front")
	insert(projectID, "FILTER-OTHER", "u_qa", "u_back")
	// The same member IDs in another project must never enter the active-project list.
	insert(insightProjectID, "FILTER-FOREIGN", "u_front", "u_front")

	query := func(path string) map[string]any {
		w := apiRequest(a, http.MethodGet, path, "u_front", projectID, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return jsonMap(t, w)
	}

	owned := query("/api/defects?q=defect-filter&assigneeUserId=u_front")
	if got := defectFilterCodes(t, owned); len(got) != 1 || got[0] != "FILTER-OWN" {
		t.Fatalf("assignee ID filter = %v", got)
	}
	ownedItem := owned["items"].([]any)[0].(map[string]any)
	if ownedItem["assigneeUserId"] != "u_front" || ownedItem["verifierUserId"] != "u_qa" {
		t.Fatalf("list must return stable person IDs: %#v", ownedItem)
	}

	verified := query("/api/defects?q=defect-filter&verifierUserId=u_front")
	if got := defectFilterCodes(t, verified); len(got) != 1 || got[0] != "FILTER-VERIFY" {
		t.Fatalf("verifier ID filter = %v", got)
	}

	mine := query("/api/defects?q=defect-filter&mine=1")
	if got := defectFilterCodes(t, mine); len(got) != 2 || got[0] != "FILTER-OWN" || got[1] != "FILTER-VERIFY" {
		t.Fatalf("current-user defect filter = %v", got)
	}

	invalid := apiRequest(a, http.MethodGet, "/api/defects?mine=somebody-else", "u_front", projectID, "")
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid mine filter: %d %s", invalid.Code, invalid.Body.String())
	}
	englishInvalid := languageRequest(t, a, http.MethodGet, "/api/defects?mine=somebody-else", "u_front", projectID, "en-US", "")
	if englishInvalid.Code != http.StatusUnprocessableEntity || jsonMap(t, englishInvalid)["error"].(map[string]any)["message"] != "The related-to-me filter is invalid." {
		t.Fatalf("invalid mine filter must localize: %d %s", englishInvalid.Code, englishInvalid.Body.String())
	}
}
