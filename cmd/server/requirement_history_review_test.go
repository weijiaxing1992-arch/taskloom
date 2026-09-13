package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func historyChange(t *testing.T, item requirementHistoryItem, field string) requirementHistoryChange {
	t.Helper()
	for _, change := range item.Changes {
		if change.Field == field {
			return change
		}
	}
	t.Fatalf("missing %s in %+v", field, item)
	return requirementHistoryChange{}
}

func TestRequirementHistoryReviewRichFormattingAndBulkMutationShape(t *testing.T) {
	a := testApp(t)
	planningSprint(t, a, "Review A", "规划中")
	planningSprint(t, a, "Review B", "规划中")
	w := apiRequest(a, "POST", "/api/field-definitions", "u_admin", projectID, `{"objectType":"requirement","key":"history_review","name":"History review","type":"text","enabled":true}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	plain := rtDoc(rtParagraph(rtText("unchanged text")))
	bold := rtDoc(rtParagraph(map[string]any{"type": "text", "text": "unchanged text", "marks": []any{map[string]any{"type": "bold"}}}))
	// Bulk edit/move use one normal PATCH per selected requirement, not a
	// separate write endpoint. Exercise that exact request shape for two items.
	for i := 0; i < 2; i++ {
		x := planningRequirement(t, a, jsonText(map[string]any{"title": fmt.Sprint("review ", i), "sprint": "Review A", "descriptionDoc": plain}))
		patchPlanningRequirement(t, a, x.ID, `{"sprint":"Review B"}`)
		patchPlanningRequirement(t, a, x.ID, `{"tags":"tag-one,tag-two","tagColors":{"tag-one":"#ff0000"},"customFields":{"history_review":"recorded value"}}`)
		tagEntry := readRequirementHistory(t, a, x.ID, "u_viewer")[0]
		for _, field := range []string{"tags", "tagColors", "customFields.history_review"} {
			historyChange(t, tagEntry, field)
		}
		patchPlanningRequirement(t, a, x.ID, jsonText(map[string]any{"descriptionDoc": bold}))
		items := readRequirementHistory(t, a, x.ID, "u_viewer")
		change := historyChange(t, items[0], "descriptionDoc")
		if reflect.DeepEqual(change.Before, change.After) || !strings.Contains(jsonText(change.After), `"bold"`) || !strings.Contains(jsonText(change.Before), "unchanged text") || items[0].IterationDelayCount != 1 {
			t.Fatalf("format-only history lost: %+v", items[0])
		}
		patchPlanningRequirement(t, a, x.ID, jsonText(map[string]any{"descriptionDoc": bold}))
		if len(readRequirementHistory(t, a, x.ID, "u_viewer")) != len(items) {
			t.Fatal("identical rich document created a duplicate change")
		}
	}
}

func TestRequirementHistoryReviewTimeInstantsAndDuplicateEvidence(t *testing.T) {
	items := []requirementHistoryItem{{ID: 1, CreatedAt: "2026-09-10T00:00:00Z", IterationDelay: true}, {ID: 2, CreatedAt: "2026-09-10T00:00:00.5Z", IterationDelay: true}, {ID: 3, CreatedAt: "2026-09-10T08:00:01+08:00"}}
	if orderRequirementHistory(items) != 2 || items[0].ID != 3 || items[1].ID != 2 || items[2].ID != 1 || items[1].IterationDelayCount != 2 || items[2].IterationDelayCount != 1 {
		t.Fatalf("history is not ordered by real instants: %+v", items)
	}
	a := testApp(t)
	planningSprint(t, a, "Evidence A", "规划中")
	planningSprint(t, a, "Evidence B", "规划中")
	x := planningRequirement(t, a, `{"title":"same evidence","sprint":"Evidence A"}`)
	patchPlanningRequirement(t, a, x.ID, `{"sprint":"Evidence B"}`)
	before := len(readRequirementHistory(t, a, x.ID, "u_viewer"))
	// An imported audit duplicates the already recorded live event twice.
	for i := 0; i < 2; i++ {
		bulkFixtureExec(t, a, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,before_json,after_json,created_at) SELECT h.tenant_id,h.project_id,h.actor_id,'requirement',CAST(h.requirement_id AS TEXT),'updated',h.before_json,h.after_json,a.created_at FROM requirement_activity_history h JOIN activities a ON a.id=h.activity_id WHERE h.requirement_id=? AND h.iteration_delay=1`, x.ID)
	}
	for i := 0; i < 2; i++ {
		if err := a.migrateRequirementHistory(); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := a.get(x.ID)
	if after.IterationDelayCount != 1 || len(readRequirementHistory(t, a, x.ID, "u_viewer")) != before {
		t.Fatal("duplicate evidence fabricated additional iteration moves")
	}
}

func TestRequirementHistoryReviewChecklistResourcesAtomicAndScoped(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"related history"}`)
	path := fmt.Sprintf("/api/requirements/%d/checklist", x.ID)
	w := apiRequest(a, "POST", path, "u_admin", projectID, `{"text":"Must remain complete"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	cid := jsonMap(t, w)["id"]
	entry := readRequirementHistory(t, a, x.ID, "u_viewer")[0]
	change := historyChange(t, entry, "checklist")
	if change.Before != nil || !strings.Contains(jsonText(change.After), "Must remain complete") || entry.Sprint == nil || entry.Status == nil || entry.ActorID != "u_admin" {
		t.Fatal("checklist lost exact value or context")
	}
	body := jsonText(map[string]any{"id": cid, "done": true})
	for i := 0; i < 2; i++ {
		w = apiRequest(a, "PATCH", path, "u_admin", projectID, body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(readRequirementHistory(t, a, x.ID, "u_viewer")) != 3 {
		t.Fatal("checklist no-op duplicated history")
	}
	w = apiRequest(a, "PATCH", path, "u_viewer", projectID, body)
	if w.Code != 403 {
		t.Fatal("viewer changed checklist")
	}
	other := planningRequirement(t, a, `{"title":"other checklist owner"}`)
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d/checklist", other.ID), "u_admin", projectID, body)
	if w.Code != 404 {
		t.Fatal("cross-requirement checklist modification accepted")
	}
	linkPath := fmt.Sprintf("/api/requirements/%d/design-links", x.ID)
	w = apiRequest(a, "POST", linkPath, "u_admin", projectID, `{"title":"Recorded design","url":"https://www.figma.com/design/history"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	entry = readRequirementHistory(t, a, x.ID, "u_viewer")[0]
	if !strings.Contains(jsonText(historyChange(t, entry, "designLink").After), "Recorded design") || entry.Actor == "u_admin" || entry.IterationDelay {
		t.Fatal("design history did not capture resource metadata and actor")
	}
	counts := tableCount(t, a, "checklist_items")
	bulkFixtureExec(t, a, `CREATE TRIGGER reject_related_history BEFORE INSERT ON requirement_activity_history BEGIN SELECT RAISE(ABORT,'history unavailable'); END`)
	w = apiRequest(a, "POST", path, "u_admin", projectID, `{"text":"Must roll back"}`)
	if w.Code < 500 || tableCount(t, a, "checklist_items") != counts {
		t.Fatal("checklist committed without history")
	}
}

func TestRequirementHistoryReviewExportMatchesPublicTimeline(t *testing.T) {
	a := testApp(t)
	planningSprint(t, a, "Export history A", "规划中")
	planningSprint(t, a, "Export history B", "规划中")
	x := planningRequirement(t, a, `{"title":"export detailed history","sprint":"Export history A"}`)
	patchPlanningRequirement(t, a, x.ID, `{"sprint":"Export history B","remarks":"full exported change"}`)
	public := readRequirementHistory(t, a, x.ID, "u_viewer")
	path := fmt.Sprintf("/api/requirements/%d/export?format=json", x.ID)
	bulkFixtureExec(t, a, `INSERT INTO activities(tenant_id,project_id,requirement_id,actor,event,detail,created_at)VALUES('foreign',?,?,'hidden','updated','FOREIGN_ACTIVITY_SECRET','2026-09-10T00:00:00Z')`, projectID, x.ID)
	w := apiRequest(a, "GET", path, "u_viewer", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	exported := decodeRequirementExport(t, w.Body.Bytes())
	if len(exported.Data["auditHistory"]) != 0 || strings.Contains(w.Body.String(), "FOREIGN_ACTIVITY_SECRET") || exported.Data["requirements"][0]["iterationDelayCount"] != float64(1) {
		t.Fatal("export counts or access boundary changed")
	}
	if len(exported.Data["requirementActivities"]) != len(public) {
		t.Fatal("export dropped public history rows")
	}
	for _, expected := range public {
		found := false
		for _, row := range exported.Data["requirementActivities"] {
			if row["id"] != float64(expected.ID) {
				continue
			}
			var actual requirementHistoryItem
			if err := json.Unmarshal([]byte(jsonText(row)), &actual); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("export differs from public history: got %+v want %+v", actual, expected)
			}
			found = true
		}
		if !found {
			t.Fatal("missing history anchor")
		}
	}
	w = apiRequest(a, "GET", fmt.Sprintf("/api/requirements/%d/export?format=markdown", x.ID), "u_viewer", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "full exported change") || !strings.Contains(w.Body.String(), `"iterationDelayCount": 1`) {
		t.Fatal("Markdown export did not retain complete history")
	}
}

func TestRequirementHistoryReviewRenameDeleteAndCompletionBoundaries(t *testing.T) {
	a := testApp(t)
	source := planningSprint(t, a, "Boundary A", "进行中")
	target := planningSprint(t, a, "Boundary B", "规划中")
	third := planningSprint(t, a, "Boundary C", "规划中")
	w := apiRequest(a, "POST", "/api/requirement-categories", "u_admin", projectID, `{"name":"Delete history category"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	category := jsonMap(t, w)["id"]
	x := planningRequirement(t, a, `{"title":"completion boundary","sprint":"Boundary A","category":"Delete history category"}`)
	completion := fmt.Sprintf("/api/sprints/%d/complete", source.ID)
	for _, request := range []struct {
		actor, project string
		code           int
	}{{"u_viewer", projectID, 403}, {"u_admin", insightProjectID, 404}} {
		w = apiRequest(a, "POST", completion, request.actor, request.project, `{"targetSprint":"Boundary B"}`)
		if w.Code != request.code {
			t.Fatal("unauthorized completion", w.Code, w.Body.String())
		}
	}
	w = apiRequest(a, "POST", completion, "u_admin", projectID, `{"targetSprint":"Boundary B"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", fmt.Sprintf("/api/sprints/%d", target.ID), "u_admin", projectID, `{"name":"Boundary renamed"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Sprint deletion has no supported endpoint; a guessed DELETE must not
	// erase the stored move or sever the persisted historical sprint names.
	w = apiRequest(a, "DELETE", fmt.Sprintf("/api/sprints/%d", source.ID), "u_admin", projectID, "")
	if w.Code != 405 {
		t.Fatal("unexpected sprint deletion behavior", w.Code)
	}
	w = apiRequest(a, "DELETE", fmt.Sprintf("/api/requirement-categories/%v", category), "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	latest := readRequirementHistory(t, a, x.ID, "u_viewer")[0]
	change := historyChange(t, latest, "category")
	if change.Before != "Delete history category" || change.After != "未分类" || latest.IterationDelayCount != 1 {
		t.Fatal("category removal damaged transfer history")
	}
	x = patchPlanningRequirement(t, a, x.ID, jsonText(map[string]any{"sprint": third.Name}))
	if x.IterationDelayCount != 2 {
		t.Fatal("rename/delete changed linear transfer count")
	}
	for _, entry := range readRequirementHistory(t, a, x.ID, "u_viewer") {
		if entry.Event == "sprint_transferred" {
			change := historyChange(t, entry, "sprint")
			if change.Before != "Boundary A" || change.After != "Boundary B" {
				t.Fatal("rename rewrote original transfer evidence")
			}
		}
	}
}

func TestRequirementHistoryReviewCommentsAndSameProjectLinks(t *testing.T) {
	a := testApp(t)
	x := planningRequirement(t, a, `{"title":"discussion history"}`)
	other := planningRequirement(t, a, `{"title":"historically linked title"}`)
	w := apiRequest(a, "POST", fmt.Sprintf("/api/requirements/%d/comments", x.ID), "u_admin", projectID, `{"body":"The complete historical comment"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	entry := readRequirementHistory(t, a, x.ID, "u_viewer")[0]
	if !strings.Contains(jsonText(historyChange(t, entry, "comment").After), "The complete historical comment") || entry.Sprint == nil || entry.Status == nil {
		t.Fatal("comment body or historical context omitted")
	}
	path := fmt.Sprintf("/api/requirements/%d/links", x.ID)
	for i := 0; i < 2; i++ {
		w = apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"requirementId": other.ID}))
		if w.Code != 200 && w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if len(readRequirementHistory(t, a, x.ID, "u_viewer")) != 3 || len(readRequirementHistory(t, a, other.ID, "u_viewer")) != 2 {
		t.Fatal("relation history not anchored once on both local requirements")
	}
	entry = readRequirementHistory(t, a, x.ID, "u_viewer")[0]
	if !strings.Contains(jsonText(historyChange(t, entry, "relatedRequirement").After), "historically linked title") {
		t.Fatal("related requirement snapshot omitted")
	}
	w = apiRequest(a, "DELETE", fmt.Sprintf("%s/%d", path, other.ID), "u_admin", projectID, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	change := historyChange(t, readRequirementHistory(t, a, x.ID, "u_viewer")[0], "relatedRequirement")
	if change.Before == nil || change.After != nil {
		t.Fatal("unlink history lost deleted relation")
	}
	bulkFixtureExec(t, a, `CREATE TRIGGER reject_relation_history BEFORE INSERT ON requirement_activity_history BEGIN SELECT RAISE(ABORT,'history unavailable'); END`)
	w = apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"requirementId": other.ID}))
	if w.Code < 500 {
		t.Fatal("relation saved without history")
	}
	var linked int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM work_item_relations WHERE source_id=? AND target_id=?`, x.ID, other.ID).Scan(&linked); err != nil || linked != 0 {
		t.Fatal("relation write was not rolled back", err)
	}
}
