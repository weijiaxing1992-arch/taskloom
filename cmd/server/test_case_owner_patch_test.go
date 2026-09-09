package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func createOwnerPatchCase(t *testing.T, a *App, title, owner string) TestCase {
	t.Helper()
	w := apiRequest(a, http.MethodPost, "/api/test-cases", "u_admin", projectID, jsonText(map[string]any{
		"title": title, "steps": "执行操作", "expected": "得到预期结果", "owner": owner,
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create test case: %d %s", w.Code, w.Body.String())
	}
	var item TestCase
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestTestCasePatchOwnerUserIDUsesStableScopedIdentity(t *testing.T) {
	a := testApp(t)
	frontName := peopleName(t, a, "u_front")
	historic := createOwnerPatchCase(t, a, "历史负责人仍可保存", frontName)
	target := createOwnerPatchCase(t, a, "稳定负责人更新", frontName)
	ambiguousLegacy := createOwnerPatchCase(t, a, "旧客户端同名歧义", peopleName(t, a, "u_pm"))
	legacyWithoutID := createOwnerPatchCase(t, a, "旧导入负责人无 ID", frontName)
	if _, err := a.db.Exec(`UPDATE test_cases SET owner_user_id='' WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, projectID, legacyWithoutID.ID); err != nil {
		t.Fatal(err)
	}
	// 旧导入数据可能只保存显示名。新版整包保存原 owner + 空 ownerUserId 时
	// 不能把它误判成清空；只有 ID-only 空值或变更后的空二元组才清空。
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", legacyWithoutID.ID), "u_admin", projectID, fmt.Sprintf(`{"title":"旧导入负责人可编辑","owner":%q,"ownerUserId":""}`, frontName))
	if w.Code != http.StatusOK {
		t.Fatalf("legacy owner without ID blocked unrelated edit: %d %s", w.Code, w.Body.String())
	}
	legacySaved, err := a.getTestCase(legacyWithoutID.ID)
	if err != nil || legacySaved.Owner != frontName || legacySaved.OwnerUserID != "" || legacySaved.Title != "旧导入负责人可编辑" {
		t.Fatalf("legacy owner without ID was cleared: %+v err=%v", legacySaved, err)
	}

	// 两个仍有效的同名成员使旧 name-only 编辑无法安全猜测；稳定 ID 则必须
	// 精确落到指定用户，并以服务端的规范化显示名覆盖客户端陈旧文本。
	const sharedName = "测试同名负责人"
	if _, err := a.db.Exec(`UPDATE users SET name=? WHERE tenant_id=? AND id IN ('u_front','u_back')`, sharedName, tenantID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"owner":"过期显示名","ownerUserId":"u_back"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stable owner ID update: %d %s", w.Code, w.Body.String())
	}
	changed, err := a.getTestCase(target.ID)
	if err != nil || changed.OwnerUserID != "u_back" || changed.Owner != sharedName {
		t.Fatalf("stable owner ID was not canonicalized: %+v err=%v", changed, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", target.ID, "u_back"); got != 1 {
		t.Fatalf("stable ID owner change notification = %d, want 1", got)
	}
	// 完全不带负责人字段的普通编辑也必须以事务内快照为准，不能把请求刚
	// 进入时读到的旧负责人反向写回。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"title":"无负责人字段的正常编辑"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch without owner fields: %d %s", w.Code, w.Body.String())
	}
	withoutOwnerFields, err := a.getTestCase(target.ID)
	if err != nil || withoutOwnerFields.OwnerUserID != "u_back" || withoutOwnerFields.Owner != sharedName || withoutOwnerFields.Title != "无负责人字段的正常编辑" {
		t.Fatalf("patch without owner fields rewrote owner: %+v err=%v", withoutOwnerFields, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", target.ID, "u_back"); got != 1 {
		t.Fatalf("patch without owner fields emitted notification = %d", got)
	}

	// 新版表单会整包回传 owner + ownerUserId。ID 没有变化时不能因为缓存的
	// 姓名不同而改绑、改名或重复发通知。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"title":"稳定 ID 无重复通知","owner":"伪造或缓存姓名","ownerUserId":"u_back"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("same stable owner ID: %d %s", w.Code, w.Body.String())
	}
	unchanged, err := a.getTestCase(target.ID)
	if err != nil || unchanged.OwnerUserID != "u_back" || unchanged.Owner != sharedName || unchanged.Title != "稳定 ID 无重复通知" {
		t.Fatalf("same stable ID was not preserved: %+v err=%v", unchanged, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", target.ID, "u_back"); got != 1 {
		t.Fatalf("same stable ID repeated notification = %d, want 1", got)
	}
	// name-only 的同名 no-op 仍应兼容；真正切换到歧义姓名时才拒绝，不能借
	// 用 next 中带来的旧 ID 绕过唯一性判断。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, fmt.Sprintf(`{"owner":%q}`, sharedName))
	if w.Code != http.StatusOK {
		t.Fatalf("same-name legacy no-op rejected: %d %s", w.Code, w.Body.String())
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", ambiguousLegacy.ID), "u_admin", projectID, fmt.Sprintf(`{"owner":%q}`, sharedName))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ambiguous legacy name was accepted: %d %s", w.Code, w.Body.String())
	}
	ambiguousSaved, err := a.getTestCase(ambiguousLegacy.ID)
	if err != nil || ambiguousSaved.OwnerUserID != "u_pm" || ambiguousSaved.Owner != peopleName(t, a, "u_pm") {
		t.Fatalf("ambiguous legacy name changed current owner: %+v err=%v", ambiguousSaved, err)
	}

	// 已被业务停用的历史负责人仍可保存在用例上。表单回传同一个稳定 ID 时，
	// 无关字段编辑不得重新校验失败、更不得把历史绑定悄悄清空。
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", historic.ID), "u_admin", projectID, `{"title":"历史负责人允许编辑","owner":"过期显示名","ownerUserId":"u_front"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("historic disabled owner blocked unrelated edit: %d %s", w.Code, w.Body.String())
	}
	historicSaved, err := a.getTestCase(historic.ID)
	if err != nil || historicSaved.OwnerUserID != "u_front" || historicSaved.Owner != frontName || historicSaved.Title != "历史负责人允许编辑" {
		t.Fatalf("historic binding changed unexpectedly: %+v err=%v", historicSaved, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", historic.ID, "u_front"); got != 0 {
		t.Fatalf("historic no-op owner update emitted notification = %d", got)
	}

	// 新分配始终必须是当前项目的 active、未被业务停用成员。先覆盖停用账号，
	// 再覆盖只属于另一个项目的账号；两次失败都不能修改原负责人。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"ownerUserId":"u_front"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled owner accepted: %d %s", w.Code, w.Body.String())
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.db.Exec(`INSERT INTO users(id,tenant_id,name,email,active)VALUES('case-owner-foreign',?,?,?,1)`, tenantID, "外部项目负责人", "case-owner-foreign@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status,created_at,updated_at)VALUES(?,?,?,'active',?,?)`, tenantID, "case-owner-foreign", "member", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'qa',?,?)`, tenantID, insightProjectID, "case-owner-foreign", now, now); err != nil {
		t.Fatal(err)
	}
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"ownerUserId":"case-owner-foreign"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-project owner accepted: %d %s", w.Code, w.Body.String())
	}
	unchanged, err = a.getTestCase(target.ID)
	if err != nil || unchanged.OwnerUserID != "u_back" || unchanged.Owner != sharedName {
		t.Fatalf("rejected owner update changed case: %+v err=%v", unchanged, err)
	}

	// null 与空字符串都表示显式清空；清空不应给旧负责人再写一条变更通知。
	w = apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", target.ID), "u_admin", projectID, `{"owner":"不可信显示名","ownerUserId":null}`)
	if w.Code != http.StatusOK {
		t.Fatalf("clear stable owner: %d %s", w.Code, w.Body.String())
	}
	cleared, err := a.getTestCase(target.ID)
	if err != nil || cleared.Owner != "" || cleared.OwnerUserID != "" {
		t.Fatalf("owner was not cleared: %+v err=%v", cleared, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", target.ID, "u_back"); got != 1 {
		t.Fatalf("owner clear emitted duplicate notification = %d", got)
	}
}

func TestTestCasePatchOwnerNotificationFailureRollsBack(t *testing.T) {
	a := testApp(t)
	owner := peopleName(t, a, "u_pm")
	item := createOwnerPatchCase(t, a, "负责人通知事务回滚", owner)
	if _, err := a.db.Exec(`CREATE TRIGGER reject_test_case_owner_changed BEFORE INSERT ON user_notifications WHEN NEW.event_type='test_case.owner_changed' BEGIN SELECT RAISE(ABORT,'owner notification rejected'); END`); err != nil {
		t.Fatal(err)
	}
	w := apiRequest(a, http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", item.ID), "u_admin", projectID, `{"ownerUserId":"u_qa"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("notification failure response: %d %s", w.Code, w.Body.String())
	}
	saved, err := a.getTestCase(item.ID)
	if err != nil || saved.OwnerUserID != "u_pm" || saved.Owner != owner {
		t.Fatalf("notification failure did not roll back owner: %+v err=%v", saved, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", item.ID, "u_qa"); got != 0 {
		t.Fatalf("failed notification left persisted event = %d", got)
	}
}

func TestTestCasePatchOwnerConcurrentSameStableIDDoesNotDuplicateNotice(t *testing.T) {
	// 真实文件型 SQLite 使用生产同款 _txlock=immediate。多请求可同时在事务
	// 外读到旧模型，但进入写事务后必须按最新 owner 快照判断，最终只产生一条
	// owner_changed；内存库不具备这个锁语义，不能替代该回归。
	a := fileSQLiteTestApp(t)
	item := createOwnerPatchCase(t, a, "并发稳定负责人", peopleName(t, a, "u_front"))
	cookie := fileSQLiteLogin(t, a)
	handler := a.scopedAPI()
	const requests = 8
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, requests)
	var group sync.WaitGroup
	for i := 0; i < requests; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			r := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/test-cases/%d", item.ID), bytes.NewBufferString(`{"owner":"并发缓存名","ownerUserId":"u_back"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-DevFlow-Project", projectID)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			responses <- w
		}()
	}
	close(start)
	group.Wait()
	close(responses)
	for response := range responses {
		if response.Code != http.StatusOK {
			t.Fatalf("concurrent stable owner patch: %d %s", response.Code, response.Body.String())
		}
	}
	saved, err := a.getTestCase(item.ID)
	if err != nil || saved.OwnerUserID != "u_back" || saved.Owner != peopleName(t, a, "u_back") {
		t.Fatalf("concurrent stable owner result: %+v err=%v", saved, err)
	}
	if got := assignmentNoticeCount(t, a, "test_case.owner_changed", "test_case", item.ID, "u_back"); got != 1 {
		t.Fatalf("concurrent same owner emitted %d notifications, want 1", got)
	}
}
