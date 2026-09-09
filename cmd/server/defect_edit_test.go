package main

import (
	"fmt"
	"testing"
)

func TestDefectFullEditorRoundTripAndProtectedFields(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/defects", "u_admin", projectID, `{"title":"editable defect"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	id := int64(jsonMap(t, w)["id"].(float64))
	path := fmt.Sprintf("/api/defects/%d", id)
	var sprint string
	if err := a.db.QueryRow(`SELECT name FROM sprints WHERE tenant_id=? AND project_id=? AND status IN ('规划中','进行中') LIMIT 1`, tenantID, projectID).Scan(&sprint); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"title": "full editor", "description": "Description", "steps": "step1", "actual": "actual", "expected": "expected", "environment": "Chrome", "foundVersion": "v1", "fixVersion": "v2", "severity": "严重", "priority": "P1", "assigneeUserId": "u_front", "verifierUserId": "u_qa", "sprint": sprint, "discipline": "frontend", "progress": 21, "estimatedHours": 2.5, "actualHours": 1.25, "requirementId": 1, "tags": "回归", "customFields": map[string]any{"escape_stage": "生产"}}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, jsonText(body))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	d, err := a.getDefect(id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "full editor" || d.Description != "Description" || d.Steps != "step1" || d.Actual != "actual" || d.Expected != "expected" || d.Environment != "Chrome" || d.FoundVersion != "v1" || d.FixVersion != "v2" || d.AssigneeUserID != "u_front" || d.VerifierUserID != "u_qa" || d.Progress != 21 || d.EstimatedHours != 2.5 || d.ActualHours != 1.25 || d.RequirementID == nil || *d.RequirementID != 1 || d.CustomFields["escape_stage"] != "生产" {
		t.Fatalf("lost edited fields: %+v", d)
	}
	if _, err := a.db.Exec(`UPDATE users SET active=0 WHERE id='u_front'; UPDATE sprints SET status='已完成' WHERE tenant_id=? AND project_id=? AND name=?`, tenantID, projectID, sprint); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"description":"unrelated change"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"id":1}`, `{"code":"BUG-999"}`, `{"createdAt":"fake"}`, `{"sourceExecutionId":1}`, `{"unknown":"x"}`, `{"startDate":"2026-09-03"}`, `{"title":"invalid partial","customFields":{"escape_stage":12}}`} {
		before, _ := a.getDefect(id)
		w = apiRequest(a, "PATCH", path, "u_admin", projectID, body)
		after, _ := a.getDefect(id)
		if w.Code != 422 || jsonText(before) != jsonText(after) {
			t.Fatalf("invalid editor write %s: %d %s", body, w.Code, w.Body.String())
		}
	}
}
