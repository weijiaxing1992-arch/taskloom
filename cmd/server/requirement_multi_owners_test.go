package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRequirementMultipleOwnersAndRoleMembers(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"ownerUserIds": []string{"u_pm", "u_admin", "u_pm"}, "owner": "forged", "owners": []any{map[string]string{"id": "u_back", "name": "forged"}}, "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front", "u_front_lead", "u_front"}, "userId": "u_back", "value": 12.5}, "product": map[string]any{"userId": "u_pm", "value": 2.5}}})
	if !reflect.DeepEqual(x.OwnerUserIDs, []string{"u_pm", "u_admin"}) || len(x.Owners) != 2 || x.OwnerUserID != "u_pm" || x.Owner != peopleName(t, a, "u_pm") {
		t.Fatalf("owners canonicalization %+v", x)
	}
	if !reflect.DeepEqual(x.RoleWeights["frontend"].UserIDs, []string{"u_front", "u_front_lead"}) || x.RoleWeights["frontend"].UserID != "u_front" || x.WeightTotal != 15 {
		t.Fatalf("role member weight multiplied/incorrect %+v", x.RoleWeights)
	}
	if !reflect.DeepEqual(x.RoleWeights["product"].UserIDs, []string{"u_pm"}) {
		t.Fatal("legacy role single member not normalized")
	}
	got := patchPeopleRequirement(t, a, x.ID, map[string]any{"remarks": "unrelated edit"})
	if !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) || !reflect.DeepEqual(got.RoleWeights, x.RoleWeights) {
		t.Fatal("omitted multi-person data was lost")
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_pm'`); err != nil {
		t.Fatal(err)
	}
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"ownerUserIds": []string{"u_admin", "u_pm"}, "roleWeights": x.RoleWeights})
	if got.OwnerUserID != "u_admin" || len(got.Owners) != 2 || got.WeightTotal != 15 {
		t.Fatal("historic inactive people not preserved")
	}
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"ownerUserIds": []string{}, "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{}, "userId": "u_front", "value": 0}}})
	if len(got.OwnerUserIDs) != 0 || got.OwnerUserID != "" || got.Owner != "" || len(got.RoleWeights["frontend"].UserIDs) != 0 || got.RoleWeights["frontend"].UserID != "" || got.WeightTotal != 0 {
		t.Fatalf("explicit clear failed %+v", got)
	}
}

func TestRequirementOwnerAndWeightMembersValidateProject(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE user_id='u_qa' AND project_id=?`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_pm'`); err != nil {
		t.Fatal(err)
	}
	for _, body := range []map[string]any{
		{"ownerUserIds": []string{"unknown"}}, {"ownerUserIds": []string{"u_qa"}}, {"ownerUserIds": []string{"u_pm"}},
		{"owner": "unknown"},
		{"roleWeights": map[string]any{"ui": map[string]any{"userIds": []string{"u_front", "unknown"}, "value": 5}}},
		{"roleWeights": map[string]any{"ui": map[string]any{"userIds": []string{"u_qa"}, "value": 5}}},
		{"roleWeights": map[string]any{"ui": map[string]any{"userIds": []string{"u_pm"}, "value": 5}}},
	} {
		body["title"] = "invalid people"
		w := apiRequest(a, "POST", "/api/requirements", "u_admin", projectID, jsonText(body))
		if w.Code != 422 {
			t.Fatalf("invalid people accepted: %d %s", w.Code, w.Body.String())
		}
	}
	x := createPeopleRequirement(t, a, map[string]any{"ownerUserIds": []string{"u_admin"}})
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"ownerUserIds":null}`)
	if w.Code != 422 {
		t.Fatalf("null array accepted %d", w.Code)
	}
	if _, err := a.db.Exec(`CREATE TRIGGER reject_owner_write BEFORE INSERT ON activities BEGIN SELECT RAISE(ABORT,'rollback'); END`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"ownerUserIds":["u_back"]}`)
	if w.Code < 500 {
		t.Fatalf("transaction failure not surfaced %d", w.Code)
	}
	got, err := a.get(x.ID)
	if err != nil || !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) {
		t.Fatalf("owner transaction did not roll back %+v %v", got, err)
	}
}

func TestRequirementOwnerMigrationPreservesArrays(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"ownerUserIds": []string{"u_admin", "u_pm"}})
	for i := 0; i < 2; i++ {
		if err := a.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.get(x.ID)
	if err != nil || !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) {
		t.Fatalf("restart overwrote array %+v %v", got, err)
	}
	if _, err := a.db.Exec(`ALTER TABLE requirements DROP COLUMN owner_user_ids_json`); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateRequirementPeople(); err != nil {
		t.Fatal(err)
	}
	got, err = a.get(x.ID)
	if err != nil || !reflect.DeepEqual(got.OwnerUserIDs, []string{"u_admin"}) {
		t.Fatalf("legacy stable ID backfill %+v %v", got, err)
	}
}

func TestRequirementTagsProjectAggregation(t *testing.T) {
	a := testApp(t)
	createPeopleRequirement(t, a, map[string]any{"tags": "集合标签, 集合标签，旧标签\n换行标签", "tagColors": map[string]string{"集合标签": "#123456", "旧标签": "#123abc"}})
	createPeopleRequirement(t, a, map[string]any{"tags": "集合标签,集合标签", "tagColors": map[string]string{"集合标签": "#fedcba"}})
	createPeopleRequirement(t, a, map[string]any{"tags": "集合标签"})
	w := apiRequest(a, "POST", "/api/requirements", "u_admin", insightProjectID, `{"title":"other project","tags":"不应泄露,集合标签","tagColors":{"集合标签":"#000000"}}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "GET", "/api/requirement-tags?limit=1&q=nonexistent", "u_admin", projectID, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "不应泄露") {
		t.Fatalf("tags scope %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []requirementTag `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tag := range response.Items {
		if tag.Name == "集合标签" {
			found = true
			if tag.Count != 3 || tag.Color != "#FEDCBA" {
				t.Fatalf("aggregate incorrect %+v", tag)
			}
		}
	}
	if !found {
		t.Fatal("tag omitted by query/pagination")
	}
	w = apiRequest(a, "POST", "/api/requirement-tags", "u_admin", projectID, `{}`)
	if w.Code != 405 {
		t.Fatalf("tag endpoint allows mutation %d", w.Code)
	}
	if _, err := a.db.Exec(`UPDATE requirements SET tag_colors_json='broken' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/requirement-tags", "u_admin", projectID, "")
	if w.Code != 503 {
		t.Fatalf("broken data didn't fail closed %d", w.Code)
	}
}
