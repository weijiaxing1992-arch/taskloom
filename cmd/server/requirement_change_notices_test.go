package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestRequirementRealEditsNotifyEveryAssigneeAndNoOpsDoNot(t *testing.T) {
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"title": "Title 原文", "description": "Body 原文", "assigneeUserIds": []string{"u_admin", "u_front", "u_back"}})
	path := fmt.Sprintf("/api/requirements/%d", x.ID)
	w := apiRequest(a, "PATCH", path, "u_admin", projectID, `{"remarks":"new remarks"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, uid := range []string{"u_admin", "u_front", "u_back"} {
		if n := peopleNoticeCount(t, a, x.ID, "requirement.updated", uid); n != 1 {
			t.Fatalf("missing assignee notice %s: %d", uid, n)
		}
	}
	var detail, created string
	a.db.QueryRow(`SELECT body,created_at FROM user_notifications WHERE subject_type='requirement' AND subject_id=? AND event_type='requirement.updated' LIMIT 1`, x.ID).Scan(&detail, &created)
	if !strings.Contains(detail, "Title 原文") || !strings.Contains(detail, "Body 原文") || created == "" {
		t.Fatalf("incomplete change notice %q", detail)
	}
	before, _ := a.get(x.ID)
	activities := tableCount(t, a, "activities")
	outbox := tableCount(t, a, "notification_outbox")
	for _, body := range []string{`{"remarks":"new remarks","title":"Title 原文"}`, `{"customFields":{}}`, jsonText(map[string]any{"roleWeights": before.RoleWeights, "tagColors": before.TagColors, "assigneeUserIds": before.AssigneeUserIDs})} {
		w = apiRequest(a, "PATCH", path, "u_admin", projectID, body)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	after, _ := a.get(x.ID)
	if before.UpdatedAt != after.UpdatedAt || tableCount(t, a, "activities") != activities || tableCount(t, a, "notification_outbox") != outbox {
		t.Fatal("no-op saved data or emitted audit/notification")
	}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"status":"开发中","remarks":"mixed change"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, uid := range []string{"u_admin", "u_front", "u_back"} {
		if peopleNoticeCount(t, a, x.ID, "requirement.status_changed", uid) != 1 || peopleNoticeCount(t, a, x.ID, "requirement.updated", uid) != 1 {
			t.Fatalf("mixed change duplicate or missing for %s", uid)
		}
	}
	w = languageRequest(t, a, "GET", "/api/notifications?eventType=requirement.status_changed", "u_front", projectID, "en-US", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Requirement status changed to In development") || !strings.Contains(w.Body.String(), "Title 原文") || !strings.Contains(w.Body.String(), fmt.Sprintf("/requirements?req=%d", x.ID)) {
		t.Fatal(w.Body.String())
	}
}

func TestRequirementEditNoticeRollbackAndRecipientScope(t *testing.T) {
	for _, target := range []string{"user_notifications", "notification_outbox", "activities"} {
		t.Run(target, func(t *testing.T) {
			a := testApp(t)
			x := createPeopleRequirement(t, a, map[string]any{"assigneeUserIds": []string{"u_front", "u_back"}})
			before, _ := a.get(x.ID)
			counts := map[string]int{}
			for _, table := range []string{"user_notifications", "notification_outbox", "activities", "audit_logs", "user_wecom_deliveries"} {
				counts[table] = tableCount(t, a, table)
			}
			if _, err := a.db.Exec(`CREATE TRIGGER reject_change BEFORE INSERT ON ` + target + ` BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
				t.Fatal(err)
			}
			w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"title":"must roll back","status":"开发中"}`)
			if w.Code < 500 {
				t.Fatalf("write unexpectedly succeeded: %d %s", w.Code, w.Body.String())
			}
			after, _ := a.get(x.ID)
			if after.Title != before.Title || after.Status != before.Status || after.UpdatedAt != before.UpdatedAt {
				t.Fatal("partial requirement saved")
			}
			for table, count := range counts {
				if tableCount(t, a, table) != count {
					t.Fatalf("partial %s", table)
				}
			}
		})
	}
	a := testApp(t)
	x := createPeopleRequirement(t, a, map[string]any{"assigneeUserIds": []string{"u_front", "u_back"}})
	if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'; UPDATE users SET active=0 WHERE id='u_back'`, tenantID, projectID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/requirements/%d", x.ID), "u_admin", projectID, `{"remarks":"no longer authorized recipients"}`)
	if w.Code != 200 || peopleNoticeCount(t, a, x.ID, "requirement.updated", "u_front") != 0 || peopleNoticeCount(t, a, x.ID, "requirement.updated", "u_back") != 0 {
		t.Fatal(w.Body.String())
	}
}
