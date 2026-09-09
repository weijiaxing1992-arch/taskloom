package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCaseCreateUsesStableOwnerIDDespiteDuplicateOrStaleName(t *testing.T) {
	a := testApp(t)
	if _, err := a.db.Exec(`UPDATE users SET name='同名测试成员' WHERE tenant_id=? AND id IN ('u_front','u_qa')`, tenantID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, `{"title":"稳定负责人创建","stepsDetail":[{"action":"提交","expected":"成功"}],"ownerUserId":"u_qa","owner":"过期显示名"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("Stable owner ID rejected: %d %s", w.Code, w.Body.String())
	}
	var created TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	saved, err := a.getTestCase(created.ID)
	if err != nil || created.OwnerUserID != "u_qa" || created.Owner != "同名测试成员" || saved.OwnerUserID != "u_qa" || saved.Owner != created.Owner {
		t.Fatalf("Owner identity was lost: created=%+v saved=%+v err=%v", created, saved, err)
	}
	if count := assignmentNoticeCount(t, a, "test_case.owner_assigned", "test_case", created.ID, "u_qa"); count != 1 {
		t.Fatalf("Owner notification count=%d", count)
	}
	if count := assignmentNoticeCount(t, a, "test_case.owner_assigned", "test_case", created.ID, "u_front"); count != 0 {
		t.Fatalf("Notified another member with the same name: %d", count)
	}
	before := tableCount(t, a, "test_cases")
	w = apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, `{"title":"同名旧客户端","steps":"提交","expected":"成功","owner":"同名测试成员"}`)
	if w.Code != 422 || tableCount(t, a, "test_cases") != before {
		t.Fatalf("Ambiguous name-only assignment was accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestCaseCreateRejectsInvalidOwnerIDWithoutNameFallback(t *testing.T) {
	for _, tc := range []struct{ name, setup string }{
		{"missing", `DELETE FROM project_members WHERE tenant_id='tn_acme' AND project_id='prj_orbit' AND user_id='u_qa'`},
		{"inactive", `UPDATE users SET active=0 WHERE tenant_id='tn_acme' AND id='u_qa'`},
		{"disabled", `UPDATE users SET operation_disabled=1 WHERE tenant_id='tn_acme' AND id='u_qa'`},
		{"inactive membership", `UPDATE tenant_memberships SET status='inactive' WHERE tenant_id='tn_acme' AND user_id='u_qa'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			fallbackName := peopleName(t, a, "u_front")
			if _, err := a.db.Exec(tc.setup); err != nil {
				t.Fatal(err)
			}
			beforeCases := tableCount(t, a, "test_cases")
			beforeNotices := tableCount(t, a, "user_notifications")
			body := jsonText(map[string]any{"title": "无效负责人", "steps": "提交", "expected": "成功", "ownerUserId": "u_qa", "owner": fallbackName})
			w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, body)
			if w.Code != 422 || tableCount(t, a, "test_cases") != beforeCases || tableCount(t, a, "user_notifications") != beforeNotices {
				t.Fatalf("Invalid ID created a case or fallback notification: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCaseCreateRetainsUniqueLegacyNameAndUnassignedBehavior(t *testing.T) {
	a := testApp(t)
	for _, ownerID := range []string{"u_front", ""} {
		ownerName := ""
		if ownerID != "" {
			ownerName = peopleName(t, a, ownerID)
		}
		body := jsonText(map[string]any{"title": "旧客户端兼容", "steps": "提交", "expected": "成功", "owner": ownerName})
		w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("Legacy owner create failed: %d %s", w.Code, w.Body.String())
		}
		var created TestCase
		if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if created.OwnerUserID != ownerID || created.Owner != ownerName {
			t.Fatalf("Legacy assignment changed: %+v", created)
		}
	}
}
