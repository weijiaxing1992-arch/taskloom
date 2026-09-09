package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Matches the field whitelist and bn() role-weight serializer in the deployed
// system-i18n-20260903 browser bundle. It deliberately knows no new arrays.
func legacyBrowserRequirementPayload(t *testing.T, x Requirement) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonText(x)), &raw); err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	for _, key := range []string{"type", "title", "description", "acceptance", "parentId", "category", "sprint", "status", "priority", "owner", "assignee", "tags", "tagColors", "remarks", "roleWeights", "startDate", "endDate", "sensitive", "authImpact", "customFields"} {
		fields[key] = raw[key]
	}
	weights := map[string]any{}
	for _, role := range requirementWeightRoles {
		weight := x.RoleWeights[role]
		weights[role] = map[string]any{"userId": weight.UserID, "value": weight.Value}
	}
	fields["roleWeights"] = weights
	return fields
}

func TestLegacyBrowserFullSavePreservesMultiPeopleAndMentions(t *testing.T) {
	a := testApp(t)
	description := "请 @" + peopleName(t, a, "u_front") + " 确认正文"
	remarks := "请 @" + peopleName(t, a, "u_pm") + " 确认备注"
	x := createPeopleRequirement(t, a, map[string]any{"description": description, "descriptionMentionUserIds": []string{"u_front"}, "remarks": remarks, "remarksMentionUserIds": []string{"u_pm"}, "ownerUserIds": []string{"u_pm", "u_admin"}, "assigneeUserIds": []string{"u_front", "u_back"}, "tags": "兼容草稿,生产验证", "tagColors": map[string]string{"兼容草稿": "#123ABC", "生产验证": "#ABC123"}, "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front", "u_front_lead"}, "value": 12.5}, "product": map[string]any{"userIds": []string{"u_pm", "u_admin"}, "value": 2.5}}})
	legacy := legacyBrowserRequirementPayload(t, x)
	legacy["title"] = "旧版浏览器中未保存的草稿"
	legacy["remarks"] = remarks + "，保留旧的提及"
	for i := 0; i < 2; i++ {
		got := patchPeopleRequirement(t, a, x.ID, legacy)
		if got.Title != legacy["title"] || got.Remarks != legacy["remarks"] || got.CreatedAt != x.CreatedAt || got.Sprint != x.Sprint || got.Category != x.Category || got.Tags != x.Tags || !reflect.DeepEqual(got.TagColors, x.TagColors) {
			t.Fatalf("legacy saved data mismatch %+v", got)
		}
		if !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) || !reflect.DeepEqual(got.AssigneeUserIDs, x.AssigneeUserIDs) || !reflect.DeepEqual(got.RoleWeights, x.RoleWeights) || got.WeightTotal != 15 {
			t.Fatalf("legacy save collapsed multi-person state %+v", got)
		}
		if !reflect.DeepEqual(got.DescriptionMentionNames, x.DescriptionMentionNames) || !reflect.DeepEqual(got.RemarksMentionNames, x.RemarksMentionNames) {
			t.Fatal("omitted new mention fields lost existing metadata")
		}
	}
	for _, expect := range []struct{ event, user string }{{"requirement.assigned", "u_front"}, {"requirement.assigned", "u_back"}, {"requirement.description_mentioned", "u_front"}, {"requirement.remarks_mentioned", "u_pm"}} {
		if n := peopleNoticeCount(t, a, x.ID, expect.event, expect.user); n != 1 {
			t.Fatalf("legacy save duplicated %s/%s notice=%d", expect.event, expect.user, n)
		}
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id IN ('u_pm','u_front')`); err != nil {
		t.Fatal(err)
	}
	got := patchPeopleRequirement(t, a, x.ID, legacy)
	if !reflect.DeepEqual(got.AssigneeUserIDs, x.AssigneeUserIDs) || !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) {
		t.Fatal("legacy unchanged inactive primary rejected/lost")
	}
}

func TestLegacyBrowserAssessmentSaveAndExplicitReplacement(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"ownerUserIds": []string{"u_pm", "u_admin"}, "assigneeUserIds": []string{"u_front", "u_back"}, "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front", "u_front_lead"}, "value": 12.5}}})
	legacy := legacyBrowserRequirementPayload(t, x)
	weights := legacy["roleWeights"].(map[string]any)
	weights["frontend"].(map[string]any)["value"] = 30
	got := patchPeopleRequirement(t, a, x.ID, map[string]any{"roleWeights": weights, "remarks": "旧版详情评估保存", "tags": "草稿", "tagColors": map[string]string{"草稿": "#AA11BB"}, "customFields": map[string]any{}})
	if got.WeightTotal != 30 || !reflect.DeepEqual(got.RoleWeights["frontend"].UserIDs, x.RoleWeights["frontend"].UserIDs) || !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) {
		t.Fatalf("legacy assessment update %+v", got)
	}
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"owner": peopleName(t, a, "u_back"), "assignee": peopleName(t, a, "u_qa"), "roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front_lead", "value": 7}}})
	if !reflect.DeepEqual(got.OwnerUserIDs, []string{"u_back"}) || !reflect.DeepEqual(got.AssigneeUserIDs, []string{"u_qa"}) || !reflect.DeepEqual(got.RoleWeights["frontend"].UserIDs, []string{"u_front_lead"}) {
		t.Fatal("changed legacy primary did not replace relation")
	}
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"owner": "", "assignee": "", "roleWeights": map[string]any{"frontend": map[string]any{"userId": "", "value": 7}}})
	if len(got.OwnerUserIDs) != 0 || len(got.AssigneeUserIDs) != 0 || len(got.RoleWeights["frontend"].UserIDs) != 0 {
		t.Fatal("empty legacy value did not clear relation")
	}
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"ownerUserIds": []string{"u_pm", "u_admin"}, "assigneeUserIds": []string{"u_front", "u_back"}, "roleWeights": x.RoleWeights})
	got = patchPeopleRequirement(t, a, x.ID, map[string]any{"owner": got.Owner, "ownerUserIds": []string{}, "assignee": got.Assignee, "assigneeUserIds": []string{}, "roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front", "userIds": []string{}, "value": 0}}})
	if len(got.OwnerUserIDs) != 0 || len(got.AssigneeUserIDs) != 0 || len(got.RoleWeights["frontend"].UserIDs) != 0 {
		t.Fatal("explicit empty arrays lost authority to legacy values")
	}
}

func TestRequirementPeopleShallowCopyCannotGrantInactiveMembers(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"ownerUserIds": []string{"u_pm"}, "assigneeUserIds": []string{"u_front"}, "roleWeights": map[string]any{"frontend": map[string]any{"userIds": []string{"u_front"}, "value": 3}}})
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_back'`); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"ownerUserIds":["u_back"]}`, `{"assigneeUserIds":["u_back"]}`, `{"roleWeights":{"frontend":{"userIds":["u_back"],"value":3}}}`} {
		w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, payload)
		if w.Code != 422 {
			t.Fatalf("inactive newcomer bypassed prior-slice validation: %d %s", w.Code, w.Body.String())
		}
	}
	got, err := a.get(x.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.OwnerUserIDs, x.OwnerUserIDs) || !reflect.DeepEqual(got.AssigneeUserIDs, x.AssigneeUserIDs) || !reflect.DeepEqual(got.RoleWeights, x.RoleWeights) {
		t.Fatal("rejected new member changed persisted state")
	}
}

func TestLegacyWeightCompatibilityRechecksCurrentPrimary(t *testing.T) {
	patch := map[string]json.RawMessage{"roleWeights": json.RawMessage(`{"frontend":{"userId":"u_front","value":5}}`)}
	initial := Requirement{RoleWeights: map[string]RoleWeight{"frontend": {UserID: "u_front", UserIDs: []string{"u_front", "u_front_lead"}}}}
	incoming := Requirement{RoleWeights: map[string]RoleWeight{"frontend": {UserID: "u_front"}}}
	preserveLegacyRequirementWeightMembers(&incoming, &initial, patch)
	if len(incoming.RoleWeights["frontend"].UserIDs) != 2 {
		t.Fatal("unchanged primary did not preserve multiple members")
	}
	latest := Requirement{RoleWeights: map[string]RoleWeight{"frontend": {UserID: "u_back", UserIDs: []string{"u_back", "u_admin"}}}}
	preserveLegacyRequirementWeightMembers(&incoming, &latest, patch)
	if !reflect.DeepEqual(incoming.RoleWeights["frontend"].UserIDs, []string{"u_front"}) {
		t.Fatal("transaction recheck reused an earlier expanded array after primary changed")
	}
}

func TestLegacyBrowserUpgradePreservesUserRequirementFields(t *testing.T) {
	a := fileSQLiteTestApp(t)
	w := apiRequest(a, "POST", "/api/requirement-categories", "u_admin", projectID, `{"name":"用户创建的分类"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(a, "POST", "/api/sprints", "u_admin", projectID, `{"name":"123","startDate":"2026-09-01","endDate":"2026-09-10"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	x := createPeopleRequirement(t, a, map[string]any{"title": "待保存草稿原需求", "sprint": "123", "category": "用户创建的分类", "tags": "独立标签,发布", "tagColors": map[string]string{"独立标签": "#12ABCD"}, "remarks": "不应重写的备注", "owner": peopleName(t, a, "u_pm"), "assignee": peopleName(t, a, "u_front"), "roleWeights": map[string]any{"frontend": map[string]any{"userId": "u_front", "value": 4.25}}})
	read := func() string {
		t.Helper()
		var fields string
		err := a.db.QueryRow(`SELECT json_array(title,sprint,category,tags,tag_colors_json,remarks,description,role_weights_json,owner,owner_user_id,assignee,assignee_user_id,created_at,updated_at,progress,estimated_hours,actual_hours) FROM requirements WHERE id=?`, x.ID).Scan(&fields)
		if err != nil {
			t.Fatal(err)
		}
		return fields
	}
	before := read()
	// Recreate the previous schema shape for the additive compatibility columns.
	for _, column := range []string{"owner_user_ids_json", "assignee_user_ids_json", "text_mentions_json"} {
		if _, err := a.db.Exec(`ALTER TABLE requirements DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := a.migrate(); err != nil {
			t.Fatal(err)
		}
	}
	if after := read(); after != before {
		t.Fatalf("upgrade rewrote user fields\nbefore %s\nafter %s", before, after)
	}
	legacy := legacyBrowserRequirementPayload(t, x)
	legacy["remarks"] = "用户在升级前编辑、升级后保存的草稿"
	got := patchPeopleRequirement(t, a, x.ID, legacy)
	if !strings.Contains(got.Remarks, "升级后保存") || got.Sprint != "123" || got.Category != "用户创建的分类" || got.Tags != x.Tags || got.WeightTotal != 4.25 {
		t.Fatalf("old draft failed after migration %+v", got)
	}
}
