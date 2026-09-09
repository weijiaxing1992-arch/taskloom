package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// 需求内编辑不能把已被其他人关联的用例抢回，也不能留下部分字段修改。
func TestCaseRequirementLinkPrecondition(t *testing.T) {
	a := testApp(t)
	w := apiRequest(a, "POST", "/api/test-cases", "u_admin", projectID, `{"title":"关联保护","steps":"操作","expected":"成功"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var item TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/test-cases/%d", item.ID)
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"requirementId":1,"expectedRequirementId":null}`)
	if w.Code != 200 {
		t.Fatalf("link: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{
		`{"requirementId":2,"expectedRequirementId":null,"title":"不得覆盖"}`,
		`{"requirementId":null,"expectedRequirementId":2,"title":"不得覆盖"}`,
	} {
		w = apiRequest(a, "PATCH", path, "u_admin", projectID, body)
		if w.Code != 409 {
			t.Fatalf("stale link: %d %s", w.Code, w.Body.String())
		}
		var title string
		var linked int64
		if err := a.db.QueryRow("SELECT title,requirement_id FROM test_cases WHERE id=?", item.ID).Scan(&title, &linked); err != nil {
			t.Fatal(err)
		}
		if title != "关联保护" || linked != 1 {
			t.Fatalf("partial write: %q %d", title, linked)
		}
	}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"expectedRequirementId":"invalid","title":"不得覆盖"}`)
	if w.Code != 422 {
		t.Fatalf("invalid condition: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, "PATCH", path, "u_admin", projectID, `{"expectedRequirementId":1,"title":"编辑成功"}`)
	if w.Code != 200 {
		t.Fatalf("matching condition: %d %s", w.Code, w.Body.String())
	}
}
