package main

import (
	"fmt"
	"testing"
)

// 不再开放手填排序，但更新名称和默认值不能破坏历史顺序。
func TestFieldOrderIsAutomaticAndHistoricalOrderIsPreserved(t *testing.T) {
	a := testApp(t)
	first := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"feedback_order_a","name":"自动排序甲","type":"text","sortOrder":999999}`)
	second := fieldTestDefinition(t, a, `{"objectType":"requirement","key":"feedback_order_b","name":"自动排序乙","type":"text","sortOrder":-999}`)
	if first.SortOrder == 999999 || second.SortOrder != first.SortOrder+10 {
		t.Fatalf("new field must append automatically: %d, %d", first.SortOrder, second.SortOrder)
	}
	if _, err := a.db.Exec(`UPDATE field_definitions SET sort_order=37 WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, "PATCH", fmt.Sprintf("/api/field-definitions/%d", first.ID), "u_admin", projectID, `{"name":"保留历史顺序","sortOrder":1,"defaultValue":"新默认值"}`)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var sort int
	var name, value string
	if err := a.db.QueryRow(`SELECT sort_order,name,default_value FROM field_definitions WHERE id=?`, first.ID).Scan(&sort, &name, &value); err != nil || sort != 37 || name != "保留历史顺序" || value != `"新默认值"` {
		t.Fatalf("historical field changed unexpectedly: %d %s %s %v", sort, name, value, err)
	}
}
