package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

var threadedEntities = []struct{ resource, object string }{
	{"requirements", "requirement"}, {"defects", "defect"}, {"test-cases", "test_case"}, {"test-plans", "test_plan"}, {"test-executions", "test_execution"},
}

func threadPost(t *testing.T, a *App, path, user string, body map[string]any) map[string]any {
	t.Helper()
	w := apiRequest(a, "POST", path, user, projectID, jsonText(body))
	if w.Code != 201 {
		t.Fatalf("%s %s: %d %s", user, path, w.Code, w.Body.String())
	}
	return jsonMap(t, w)
}

func TestCommentThreadsAllEntitiesRoundTripAndReplyMentionDedup(t *testing.T) {
	a := testApp(t)
	front, back := peopleName(t, a, "u_front"), peopleName(t, a, "u_back")
	for _, entity := range threadedEntities {
		t.Run(entity.object, func(t *testing.T) {
			path := "/api/" + entity.resource + "/1/comments"
			parent := threadPost(t, a, path, "u_front", map[string]any{"body": "父评论原文", "mentionUserIds": []string{}})
			if parent["replyToId"] != nil || parent["authorUserId"] != "u_front" {
				t.Fatalf("top-level author identity missing: %v", parent)
			}
			body := "@" + front + " 请确认，@" + back + " 协作"
			reply := threadPost(t, a, path, "u_admin", map[string]any{"body": body, "replyToId": parent["id"], "mentionUserIds": []string{"u_front", "u_front", "u_back"}})
			if reply["replyToId"] != parent["id"] || reply["replyToAuthorUserId"] != "u_front" || reply["replyToAuthor"] != front {
				t.Fatalf("parent relation missing: %v", reply)
			}
			var replies, mentions int
			if err := a.db.QueryRow(`SELECT count(*) FROM user_notifications WHERE subject_type=? AND subject_id=1 AND event_type=? AND recipient_user_id='u_front'`, entity.object, entity.object+".replied").Scan(&replies); err != nil {
				t.Fatal(err)
			}
			if err := a.db.QueryRow(`SELECT count(*) FROM user_notifications WHERE subject_type=? AND subject_id=1 AND event_type=? AND recipient_user_id='u_front'`, entity.object, entity.object+".mentioned").Scan(&mentions); err != nil {
				t.Fatal(err)
			}
			if replies != 1 || mentions != 0 {
				t.Fatalf("reply and @ were not deduplicated: replies=%d mentions=%d", replies, mentions)
			}
			w := apiRequest(a, "GET", "/api/notifications?eventType="+entity.object+".replied", "u_front", projectID, "")
			items := jsonMap(t, w)["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["body"] != body || items[0].(map[string]any)["url"] != notificationURL(entity.object, 1) {
				t.Fatalf("reply notification body/link lost: %s", w.Body.String())
			}
			child := threadPost(t, a, path, "u_back", map[string]any{"body": "继续回复", "replyToId": reply["id"]})
			if child["replyToId"] != reply["id"] || child["replyToAuthorUserId"] != "u_admin" {
				t.Fatalf("nested reply incorrectly attached to root: %v", child)
			}
			before := tableCount(t, a, "user_notifications")
			threadPost(t, a, path, "u_back", map[string]any{"body": "回复我自己", "replyToId": child["id"]})
			if tableCount(t, a, "user_notifications") != before {
				t.Fatal("self reply generated a notification")
			}
			w = apiRequest(a, "GET", path, "u_viewer", projectID, "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"replyToAuthorUserId":"u_admin"`) {
				t.Fatalf("viewer lost thread metadata: %d %s", w.Code, w.Body.String())
			}
			title, text := localizedNotification("en-US", entity.object+".replied", "你的评论收到回复", "你的评论收到回复：这是用户原文")
			if title != "Someone replied to your comment" || text != "你的评论收到回复：这是用户原文" {
				t.Fatalf("system localization changed user content: %q %q", title, text)
			}
		})
	}
}

func TestCommentThreadsNoArtificialDepthLimitOrMutableParent(t *testing.T) {
	a := testApp(t)
	path := "/api/requirements/1/comments"
	var parent any
	var first, last map[string]any
	for depth := 0; depth < 128; depth++ {
		last = threadPost(t, a, path, "u_admin", map[string]any{"body": fmt.Sprintf("第%d层", depth), "replyToId": parent})
		if depth == 0 {
			first = last
		}
		if last["replyToId"] != parent {
			t.Fatalf("depth %d: parent changed %v", depth, last)
		}
		parent = last["id"]
	}
	w := apiRequest(a, "GET", path, "u_admin", projectID, "")
	items := jsonMap(t, w)["items"].([]any)
	if len(items) < 128 || items[0].(map[string]any)["id"] != last["id"] {
		t.Fatalf("thread truncated or unstable order: %d", len(items))
	}
	for _, entity := range threadedEntities {
		w = apiRequest(a, "PATCH", "/api/"+entity.resource+"/1/comments", "u_admin", projectID, jsonText(map[string]any{"id": first["id"], "replyToId": last["id"], "body": "尝试制造环"}))
		if w.Code != 405 {
			t.Fatalf("existing comment parent was mutable: %s %d %s", entity.object, w.Code, w.Body.String())
		}
	}
}

func TestCommentThreadsRejectForeignParentsAndDisabledWriters(t *testing.T) {
	a := testApp(t)
	for _, entity := range threadedEntities {
		path := "/api/" + entity.resource + "/1/comments"
		for _, scope := range []struct {
			tenant, project, object string
			id                      int
		}{{tenantID, projectID, entity.object, 999}, {tenantID, insightProjectID, entity.object, 1}, {"foreign-tenant", projectID, entity.object, 1}, {tenantID, projectID, "unrelated_type", 1}} {
			if entity.object == "requirement" && scope.object == "unrelated_type" {
				continue
			}
			query := `INSERT INTO entity_comments(tenant_id,project_id,object_type,object_id,author,author_user_id,body,created_at)VALUES(?,?,?,?,?,'u_front','foreign','2026-09-04')`
			args := []any{scope.tenant, scope.project, scope.object, scope.id, "不可引用作者"}
			if entity.object == "requirement" {
				query = `INSERT INTO comments(tenant_id,project_id,requirement_id,author,author_user_id,body,created_at)VALUES(?,?,?,?,'u_front','foreign','2026-09-04')`
				args = []any{scope.tenant, scope.project, scope.id, "不可引用作者"}
			}
			res, err := a.db.Exec(query, args...)
			if err != nil {
				t.Fatal(err)
			}
			foreignID, _ := res.LastInsertId()
			before := tableCount(t, a, "user_notifications")
			w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"body": "越界回复", "replyToId": foreignID}))
			if w.Code != 422 || strings.Contains(w.Body.String(), "不可引用作者") || tableCount(t, a, "user_notifications") != before {
				t.Fatalf("foreign parent allowed or leaked: %s %d %s", entity.object, w.Code, w.Body.String())
			}
		}
		for _, parent := range []any{0, -1, 99999999, "1", 1.5, true} {
			w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(map[string]any{"body": "invalid", "replyToId": parent}))
			if w.Code != 400 && w.Code != 422 {
				t.Fatalf("invalid parent %v: %d %s", parent, w.Code, w.Body.String())
			}
		}
		for _, user := range []string{"u_viewer", "u_front"} {
			if user == "u_front" {
				if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='u_front'`); err != nil {
					t.Fatal(err)
				}
			}
			w := apiRequest(a, "POST", path, user, projectID, `{"body":"禁止的评论"}`)
			if w.Code != 403 {
				t.Fatalf("forbidden writer %s: %d %s", user, w.Code, w.Body.String())
			}
		}
		if _, err := a.db.Exec(`UPDATE users SET operation_disabled=0 WHERE id='u_front'`); err != nil {
			t.Fatal(err)
		}
	}
	// The transactional guard also rejects a stale scope already past middleware.
	b := *a
	b.user = "u_front"
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE id='u_front'`); err != nil {
		t.Fatal(err)
	}
	for _, entity := range threadedEntities {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/comments", strings.NewReader(`{"body":"stale scope"}`))
		if entity.object == "requirement" {
			b.requirementComments(w, r, 1)
		} else {
			b.qualityComments(w, r, entity.object, 1)
		}
		if w.Code != 403 {
			t.Fatalf("stale disabled writer: %s %d %s", entity.object, w.Code, w.Body.String())
		}
	}
}

func TestCommentThreadsStableAuthorsAndRevokedRecipient(t *testing.T) {
	a := testApp(t)
	for _, entity := range threadedEntities {
		path := "/api/" + entity.resource + "/1/comments"
		originalName := peopleName(t, a, "u_front")
		p := threadPost(t, a, path, "u_front", map[string]any{"body": "原作者"})
		if _, err := a.db.Exec(`UPDATE users SET name='已改名作者' WHERE id='u_front'; UPDATE users SET name=? WHERE id='u_back'`, originalName); err != nil {
			t.Fatal(err)
		}
		reply := threadPost(t, a, path, "u_admin", map[string]any{"body": "按ID回复", "replyToId": p["id"]})
		if reply["replyToAuthorUserId"] != "u_front" || reply["replyToAuthor"] != originalName {
			t.Fatalf("author rebound after rename: %v", reply)
		}
		before := tableCount(t, a, "user_notifications")
		if _, err := a.db.Exec(`DELETE FROM project_members WHERE tenant_id=? AND project_id=? AND user_id='u_front'`, tenantID, projectID); err != nil {
			t.Fatal(err)
		}
		threadPost(t, a, path, "u_admin", map[string]any{"body": "被移出项目不再通知", "replyToId": p["id"]})
		if tableCount(t, a, "user_notifications") != before {
			t.Fatal("revoked parent author notified")
		}
		if _, err := a.db.Exec(`INSERT INTO project_members(tenant_id,project_id,user_id,role,created_at,updated_at)VALUES(?,?,?,'frontend',?,?)`, tenantID, projectID, "u_front", orgNow(), orgNow()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommentThreadSkipsOperationDisabledReplyRecipient(t *testing.T) {
	a := testApp(t)
	path := "/api/defects/1/comments"
	parent := threadPost(t, a, path, "u_front", map[string]any{"body": "停用前的评论"})
	if _, err := a.db.Exec(`UPDATE users SET operation_disabled=1 WHERE tenant_id=? AND id='u_front'`, tenantID); err != nil {
		t.Fatal(err)
	}
	threadPost(t, a, path, "u_admin", map[string]any{"body": "不能投递给停用作者", "replyToId": parent["id"]})
	if count := assignmentNoticeCount(t, a, "defect.replied", "defect", 1, "u_front"); count != 0 {
		t.Fatalf("operation-disabled reply recipient received inbox row=%d", count)
	}
}

func TestCommentThreadsReplyRichAttachmentsAndTransactionalRollback(t *testing.T) {
	for _, target := range []string{"user_notifications", "notification_outbox", "audit_logs"} {
		t.Run(target, func(t *testing.T) {
			a := testApp(t)
			// Enable the trigger without a real endpoint or network call so a
			// rollback also proves the personal-WeCom delivery row is atomic.
			if _, err := a.db.Exec(`INSERT INTO user_wecom_webhooks(tenant_id,user_id,encrypted_url,enabled,version,updated_at)VALUES(?,'u_front',X'01',1,1,?)`, tenantID, orgNow()); err != nil {
				t.Fatal(err)
			}
			for _, entity := range threadedEntities {
				path := "/api/" + entity.resource + "/1/comments"
				p := threadPost(t, a, path, "u_front", map[string]any{"body": "待回复"})
				if _, err := a.db.Exec(`CREATE TRIGGER reject_thread BEFORE INSERT ON ` + target + ` BEGIN SELECT RAISE(ABORT,'private SQL sentinel');END`); err != nil {
					t.Fatal(err)
				}
				tables := []string{"comments", "entity_comments", "activities", "entity_activities", "audit_logs", "user_notifications", "notification_outbox", "requirement_attachments", "user_wecom_deliveries"}
				before := map[string]int{}
				for _, table := range tables {
					before[table] = tableCount(t, a, table)
				}
				body := map[string]any{"body": "不允许部分提交", "replyToId": p["id"]}
				if entity.object == "requirement" {
					body["contentDoc"] = rtDoc(rtParagraph(rtMention("u_front", peopleName(t, a, "u_front")), rtText("富文本回复")), rtPending("image", "reply.png", rtPNG(t)))
				}
				w := apiRequest(a, "POST", path, "u_admin", projectID, jsonText(body))
				if w.Code < 500 || strings.Contains(w.Body.String(), "private SQL sentinel") {
					t.Fatalf("rollback error leaked: %d %s", w.Code, w.Body.String())
				}
				for _, table := range tables {
					if tableCount(t, a, table) != before[table] {
						t.Fatalf("partial %s reply wrote %s", entity.object, table)
					}
				}
				if _, err := a.db.Exec(`DROP TRIGGER reject_thread`); err != nil {
					t.Fatal(err)
				}
				if entity.object == "requirement" {
					got := threadPost(t, a, path, "u_admin", body)
					raw, _ := json.Marshal(got["contentDoc"])
					if strings.Contains(string(raw), `"data":`) || !strings.Contains(string(raw), `"attachmentId":`) || got["replyToId"] != p["id"] {
						t.Fatalf("rich reply not normalized: %s", raw)
					}
				}
			}
		})
	}
}

func TestCommentThreadsMigrationPreservesDataWithoutGuessingNames(t *testing.T) {
	db, err := openSQLiteDatabase(filepath.Join(t.TempDir(), "legacy-comments.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &App{db: db}
	_, err = db.Exec(`CREATE TABLE users(id TEXT,tenant_id TEXT,name TEXT); CREATE TABLE project_members(tenant_id TEXT,project_id TEXT,user_id TEXT);
CREATE TABLE comments(id INTEGER PRIMARY KEY,tenant_id TEXT,project_id TEXT,requirement_id INTEGER,author TEXT,body TEXT,created_at TEXT);
CREATE TABLE entity_comments(id INTEGER PRIMARY KEY,tenant_id TEXT,project_id TEXT,object_type TEXT,object_id INTEGER,author TEXT,author_user_id TEXT NOT NULL DEFAULT '',body TEXT,created_at TEXT);
CREATE TABLE audit_logs(tenant_id TEXT,project_id TEXT,actor_id TEXT,object_type TEXT,object_id TEXT,action TEXT,after_json TEXT);
INSERT INTO users VALUES('one','t','唯一姓名'),('two','t','重复姓名'),('three','t','重复姓名'),('other','other','唯一姓名');
INSERT INTO project_members VALUES('t','p','one'),('t','p','two'),('t','p','three'),('other','p','other');
INSERT INTO comments VALUES(1,'t','p',1,'唯一姓名','保留正文','2020'),(2,'t','p',1,'重复姓名','保留二','2021'),(3,'t','p',1,'失踪作者','保留三','2022'),(4,'t','p',1,'唯一姓名','旧人已离职，新同名者不能被回填','2018');
INSERT INTO audit_logs VALUES('t','p','one','requirement','1','comment_created','{"commentId":1}'),('t','other','two','requirement','1','comment_created','{"commentId":4}'),('other','p','other','requirement','1','comment_created','{"commentId":4}'),('t','p','two','defect','1','comment_created','{"commentId":4}'),('t','p','two','requirement','2','comment_created','{"commentId":4}');
INSERT INTO entity_comments VALUES(1,'t','p','defect',1,'唯一姓名','','保留测试评论','2020'),(2,'t','p','test_case',1,'原姓名','stable-id','稳定ID不得改写','2019');`)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.migrateCommentThreads(); err != nil {
		t.Fatal(err)
	}
	var uid, body, created string
	var parent *int64
	if err = db.QueryRow(`SELECT author_user_id,body,created_at,reply_to_id FROM comments WHERE id=1`).Scan(&uid, &body, &created, &parent); err != nil || uid != "one" || body != "保留正文" || created != "2020" || parent != nil {
		t.Fatalf("legacy changed %s %s %s %v %v", uid, body, created, parent, err)
	}
	if _, err = db.Exec(`UPDATE users SET name='别的姓名' WHERE id='three'; INSERT INTO users VALUES('later','t','失踪作者'); INSERT INTO project_members VALUES('t','p','later')`); err != nil {
		t.Fatal(err)
	}
	if err = a.migrateCommentThreads(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{2, 3, 4} {
		if err = db.QueryRow(`SELECT author_user_id FROM comments WHERE id=?`, id).Scan(&uid); err != nil || uid != "" {
			t.Fatalf("ambiguous/missing author guessed on restart: %d %s %v", id, uid, err)
		}
	}
	if err = db.QueryRow(`SELECT author_user_id FROM entity_comments WHERE id=2`).Scan(&uid); err != nil || uid != "stable-id" {
		t.Fatalf("stable historical id overwritten: %s %v", uid, err)
	}
}
