package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetailPreferencesPartialPatchPreservesOmittedGroups(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	path := "/api/preferences/requirement-detail"
	patch := func(body string) detailPreferences {
		t.Helper()
		w := impersonationRequest(a, member, "PATCH", path, body, projectID)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var value detailPreferences
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	patch(`{"basicFields":["status","owner"],"customFieldKeys":[]}`)
	value := patch(`{"basicFields":["priority"]}`)
	if value.CustomFieldKeys == nil || len(value.CustomFieldKeys) != 0 {
		t.Fatal("omitted custom field preferences were reset", value)
	}
	value = patch(`{"customFieldKeys":null}`)
	if len(value.BasicFields) != 1 || value.BasicFields[0] != "priority" || value.CustomFieldKeys != nil {
		t.Fatal("partial default reset changed the other group", value)
	}
	for _, body := range []string{`null`, `{}`, `{"basicFields":12}`, `{"customFieldKeys":[null]}`} {
		w := impersonationRequest(a, member, "PATCH", path, body, projectID)
		if w.Code != 422 {
			t.Fatalf("accepted invalid patch %s: %d %s", body, w.Code, w.Body)
		}
	}
	w := impersonationRequest(a, member, "GET", path, "", projectID)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"basicFields":["priority"]`) {
		t.Fatal("rejected patch mutated saved preferences", w.Code, w.Body)
	}
}

func TestDisplayPreferencesPersonalScopeAndValidation(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	path := "/api/preferences/display"
	w := impersonationRequest(a, member, "GET", path, "", "stale-project")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"fontSize":"standard"`) {
		t.Fatal(w.Code, w.Body)
	}
	w = impersonationRequest(a, member, "PATCH", path, `{"fontSize":"extraLarge"}`, "stale-project")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	w = impersonationRequest(a, member, "GET", path, "", projectID)
	if !strings.Contains(w.Body.String(), `"fontSize":"extraLarge"`) {
		t.Fatal(w.Body)
	}
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	w = impersonationRequest(a, admin, "GET", path, "", projectID)
	if !strings.Contains(w.Body.String(), `"fontSize":"standard"`) {
		t.Fatal("cross-user preference leak", w.Body)
	}
	for _, body := range []string{`{"fontSize":"invalid"}`, `{"fontSize":null}`, `{"fontSize":123}`, `{"fontSize":"large","userId":"u_admin"}`, `{"fontSize":"large"} {}`} {
		w = impersonationRequest(a, member, "PATCH", path, body, projectID)
		if w.Code != 422 {
			t.Fatalf("accepted %s: %d %s", body, w.Code, w.Body)
		}
	}
	w = impersonationRequest(a, nil, "GET", path, "", projectID)
	if w.Code != 401 {
		t.Fatal("anonymous preference access", w.Code)
	}
}

func TestDetailPreferencesIsolationAndNonDestructiveVisibility(t *testing.T) {
	a := testApp(t)
	_, member := loginRequest(a, "zhouyu@devflow.local", seedPassword)
	path := "/api/preferences/requirement-detail"
	w := impersonationRequest(a, member, "PATCH", path, `{"basicFields":["status","owner"],"customFieldKeys":[]}`, projectID)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var roleCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM field_definitions`).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	w = impersonationRequest(a, member, "GET", path, "", projectID)
	if !strings.Contains(w.Body.String(), `"basicFields":["status","owner"]`) || !strings.Contains(w.Body.String(), `"customFieldKeys":[]`) {
		t.Fatal(w.Body)
	}
	_, admin := loginRequest(a, "linxia@devflow.local", seedPassword)
	for _, project := range []string{projectID, insightProjectID} {
		w = impersonationRequest(a, admin, "GET", path, "", project)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"basicFields":null`) {
			t.Fatal("preference scope leak", w.Body)
		}
	}
	for _, body := range []string{`{"basicFields":["bogus"]}`, `{"basicFields":["status","status"]}`, `{"customFieldKeys":["not-a-field"]}`, `{"basicFields":12}`} {
		w = impersonationRequest(a, member, "PATCH", path, body, projectID)
		if w.Code != 422 {
			t.Fatalf("accepted invalid fields: %d %s", w.Code, w.Body)
		}
	}
	w = impersonationRequest(a, member, "PATCH", path, `{"basicFields":null,"customFieldKeys":null}`, projectID)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"basicFields":null`) {
		t.Fatal(w.Body)
	}
	var after int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM field_definitions`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != roleCount {
		t.Fatal("hiding fields changed definitions")
	}
}
