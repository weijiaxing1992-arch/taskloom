package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func releaseNotesRequest(t *testing.T, a *App, user, method string, sprint int64, body string) *httpResponse {
	t.Helper()
	w := apiRequest(a, method, fmt.Sprintf("/api/sprints/%d/release-notes", sprint), user, projectID, body)
	return &httpResponse{Code: w.Code, Header: w.Header(), Body: w.Body.Bytes()}
}

func releaseNotesQueueBody(settings aiSettings, revision int64, replace bool) string {
	return jsonText(map[string]any{
		"confirmed":               true,
		"expectedRevision":        revision,
		"expectedSettingsVersion": settings.Version,
		"replaceDraft":            replace,
	})
}

// 生成权限和读取权限必须分开；重复提交同一队列请求不能产生第二个付费任务。
func TestReleaseNotesPermissionBoundaryAndIdempotentQueueReplay(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V10.0 permission")
	releaseContentRequirement(t, a, "V10.0 permission", "已完成", "产品需求", "其他")
	settings := releaseSettings(t, a)

	if response := releaseNotesRequest(t, a, "u_viewer", http.MethodGet, sprint, ""); response.Code != http.StatusOK {
		t.Fatalf("project viewer cannot read a release-note draft: %d %s", response.Code, response.text())
	}
	for _, user := range []string{"u_viewer", "u_front"} {
		response := releaseNotesRequest(t, a, user, http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s queued a paid release-note job: %d %s", user, response.Code, response.text())
		}
	}

	first := releaseNotesRequest(t, a, "u_pm", http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
	if first.Code != http.StatusAccepted {
		t.Fatal(first.Code, first.text())
	}
	before := releaseJob(t, a, sprint)
	replay := releaseNotesRequest(t, a, "u_pm", http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
	after := releaseJob(t, a, sprint)
	if replay.Code != http.StatusAccepted || before.ID != after.ID || before.Revision != after.Revision || before.Attempts != 0 || after.Attempts != 0 || before.Session != after.Session {
		t.Fatalf("queue replay created or replaced work: before=%+v after=%+v response=%d %s", before, after, replay.Code, replay.text())
	}
	var audits int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND project_id=? AND object_type='sprint' AND object_id=? AND action='release_notes_generate'`, tenantID, projectID, fmt.Sprint(sprint)).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("queue replay wrote %d generation audits: %v", audits, err)
	}
}

// 手工任务绑定发起会话；排队后会话被撤销时，不得继续产生外部 AI 调用。
func TestReleaseNotesWorkerRechecksRevokedSessionBeforeProvider(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V10.1 revoked")
	releaseContentRequirement(t, a, "V10.1 revoked", "已完成", "产品需求", "API接口")
	settings := releaseSettings(t, a)
	response := releaseNotesRequest(t, a, "u_pm", http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Code, response.text())
	}
	job := releaseJob(t, a, sprint)
	if job.Session == "" {
		t.Fatal("manual job was not bound to its authenticated session")
	}
	if _, err := a.db.Exec(`UPDATE auth_sessions SET revoked_at=? WHERE tenant_id=? AND token_hash=?`, orgNow(), tenantID, job.Session); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	aiMock(a, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("provider must not be called")
	})
	processed, err := a.processReleaseNotesJob(context.Background())
	job = releaseJob(t, a, sprint)
	if err != nil || processed || calls.Load() != 0 || job.State != "failed" || !strings.Contains(job.Error, "权限或会话已失效") {
		t.Fatalf("revoked job crossed the provider boundary: processed=%v calls=%d job=%+v err=%v", processed, calls.Load(), job, err)
	}
	var drafts int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, projectID, sprint).Scan(&drafts); err != nil || drafts != 0 {
		t.Fatalf("revoked task left a partial draft: %d %v", drafts, err)
	}
}

// 临时网关错误最多重试三次，期间不写半成品；人工重试可从失败状态恢复。
func TestReleaseNotesRetryExhaustionLeavesNoPartialDraftAndCanRecover(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V10.2 retry")
	releaseContentRequirement(t, a, "V10.2 retry", "已完成", "产品需求", "智能体类型")
	settings := releaseSettings(t, a)
	response := releaseNotesRequest(t, a, "u_admin", http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Code, response.text())
	}
	var calls atomic.Int32
	aiMock(a, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("temporary upstream failure containing " + aiTestSecret)
	})
	for attempt := 1; attempt <= releaseNotesMaxAttempts; attempt++ {
		processed, err := a.processReleaseNotesJob(context.Background())
		if err != nil || !processed {
			t.Fatalf("attempt %d was not durably finalized: processed=%v err=%v", attempt, processed, err)
		}
		job := releaseJob(t, a, sprint)
		wantState := "queued"
		if attempt == releaseNotesMaxAttempts {
			wantState = "failed"
		}
		if job.State != wantState || job.Attempts != attempt || strings.Contains(job.Error, aiTestSecret) {
			t.Fatalf("attempt %d has unsafe retry state: %+v", attempt, job)
		}
		var drafts int
		if err = a.db.QueryRow(`SELECT COUNT(*) FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, projectID, sprint).Scan(&drafts); err != nil || drafts != 0 {
			t.Fatalf("attempt %d wrote a partial draft: %d %v", attempt, drafts, err)
		}
		if attempt < releaseNotesMaxAttempts {
			if _, err = a.db.Exec(`UPDATE release_note_jobs SET next_attempt=0 WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, projectID, sprint); err != nil {
				t.Fatal(err)
			}
		}
	}
	if calls.Load() != releaseNotesMaxAttempts {
		t.Fatalf("unexpected provider attempts: %d", calls.Load())
	}

	failed := releaseJob(t, a, sprint)
	response = releaseNotesRequest(t, a, "u_admin", http.MethodPost, sprint, releaseNotesQueueBody(settings, failed.Revision, false))
	if response.Code != http.StatusAccepted {
		t.Fatalf("manual recovery was rejected: %d %s", response.Code, response.text())
	}
	if reset := releaseJob(t, a, sprint); reset.State != "queued" || reset.Attempts != 0 {
		t.Fatalf("manual recovery did not reset retry state: %+v", reset)
	}
	aiMock(a, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		_, source := releaseProviderInput(t, request)
		return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(source)})), nil
	})
	if processed, err := a.processReleaseNotesJob(context.Background()); err != nil || !processed || releaseJob(t, a, sprint).State != "draft" {
		t.Fatalf("manual recovery did not produce a draft: processed=%v err=%v", processed, err)
	}
}

// 租约令牌是后台并发的 fencing token；过期 worker 的晚到结果必须被丢弃。
func TestReleaseNotesExpiredWorkerCannotOverwriteRecoveredJob(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V10.3 fencing")
	releaseContentRequirement(t, a, "V10.3 fencing", "已完成", "产品需求", "其他")
	settings := releaseSettings(t, a)
	response := releaseNotesRequest(t, a, "u_admin", http.MethodPost, sprint, releaseNotesQueueBody(settings, 0, false))
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Code, response.text())
	}
	stale, err := a.claimReleaseNotesJob(context.Background())
	if err != nil || stale == nil {
		t.Fatal("first worker did not claim job", err)
	}
	if _, err = a.db.Exec(`UPDATE release_note_jobs SET lease_until=? WHERE tenant_id=? AND id=? AND lease_token=?`, time.Now().Add(-time.Minute).Unix(), tenantID, stale.Job.ID, stale.Job.Lease); err != nil {
		t.Fatal(err)
	}
	current, err := a.claimReleaseNotesJob(context.Background())
	if err != nil || current == nil || current.Job.Lease == stale.Job.Lease {
		t.Fatal("expired job was not recovered with a new fencing token", err)
	}
	staleEntries := releaseContentEntries(stale.Source)
	staleEntries[0].Title = "过期 worker 输出"
	if err = a.finishReleaseNotesJob(context.Background(), stale, staleEntries, nil); err != nil {
		t.Fatal(err)
	}
	var drafts int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, projectID, sprint).Scan(&drafts); err != nil || drafts != 0 {
		t.Fatalf("stale worker wrote a draft: %d %v", drafts, err)
	}
	currentEntries := releaseContentEntries(current.Source)
	currentEntries[0].Title = "当前 worker 输出"
	if err = a.finishReleaseNotesJob(context.Background(), current, currentEntries, nil); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = a.db.QueryRow(`SELECT entries_json FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, projectID, sprint).Scan(&stored); err != nil || !strings.Contains(stored, "当前 worker 输出") || strings.Contains(stored, "过期 worker 输出") {
		t.Fatalf("fencing did not preserve the current output: %s %v", stored, err)
	}
}
