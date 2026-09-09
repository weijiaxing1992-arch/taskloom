package main

import (
	"strings"
	"testing"
)

func TestMyWorkIncludesSecondaryRequirementOwners(t *testing.T) {
	a := testApp(t)
	planningRequirement(t, a, `{"title":"secondary-owner-requirement-qa","ownerUserIds":["u_admin","u_member"],"assigneeUserIds":[]}`)
	w := apiRequest(a, "GET", "/api/my-work", "u_member", projectID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "secondary-owner-requirement-qa") {
		t.Fatalf("secondary owners missing: %d %s", w.Code, w.Body)
	}
	// A distinct account with the same display name must not inherit an ID-backed assignment.
	if _, err := a.db.Exec(`UPDATE users SET name='林夏' WHERE id='u_ui'`); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, "GET", "/api/my-work", "u_ui", projectID, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secondary-owner-requirement-qa") {
		t.Fatal("same-name account inherited assignment", w.Code, w.Body)
	}
}
