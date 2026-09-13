package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func releaseNotesAPI(t *testing.T, a *App, method string, sprint int64, suffix, body string) *httpResponse {
	t.Helper()
	w := apiRequest(a, method, fmt.Sprintf("/api/sprints/%d/release-notes%s", sprint, suffix), "u_admin", projectID, body)
	return &httpResponse{Code: w.Code, Header: w.Header(), Body: w.Body.Bytes()}
}

// Small value wrapper keeps assertions independent from httptest internals.
type httpResponse struct {
	Code   int
	Header http.Header
	Body   []byte
}

func (r *httpResponse) text() string { return string(r.Body) }
func releaseResponseMap(t *testing.T, response *httpResponse) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response.Body, &value); err != nil {
		t.Fatalf("invalid response: %s", response.text())
	}
	return value
}
func releaseSettings(t *testing.T, a *App) aiSettings {
	t.Helper()
	value, err := a.readAISettings(context.Background(), a.db)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func releaseJob(t *testing.T, a *App, sprint int64) releaseNotesJob {
	t.Helper()
	scoped := *a
	scoped.project = projectID
	value, err := scoped.readReleaseNotesJob(context.Background(), a.db, sprint)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReleaseNotesManualLifecycleBundleAndChangedSource(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V9.2.0")
	requirement := releaseContentRequirement(t, a, "V9.2.0", "已完成", "产品需求", "智能体")
	imageID := releaseContentImage(t, a, requirement, "能力配置.png", "design", releaseContentPNG(t))
	view := releaseNotesAPI(t, a, http.MethodGet, sprint, "", "")
	if view.Code != 200 {
		t.Fatal(view.Code, view.text())
	}
	initial := releaseResponseMap(t, view)
	if initial["state"] != "none" || initial["sourceChanged"] != false || initial["sourceCount"] != float64(1) || len(initial["categories"].([]any)) != 7 {
		t.Fatal(initial)
	}
	settings := releaseSettings(t, a)
	missing := releaseNotesAPI(t, a, http.MethodPost, sprint, "", `{"confirmed":false}`)
	if missing.Code != 422 {
		t.Fatal("missing consent accepted", missing.Code, missing.text())
	}
	queued := releaseNotesAPI(t, a, http.MethodPost, sprint, "", jsonText(map[string]any{"confirmed": true, "expectedRevision": 0, "expectedSettingsVersion": settings.Version}))
	if queued.Code != 202 || releaseJob(t, a, sprint).State != "queued" {
		t.Fatal(queued.Code, queued.text())
	}
	var calls atomic.Int32
	aiMock(a, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		_, batch := releaseProviderInput(t, request)
		entries := releaseContentEntries(batch)
		entries[0].Category = "智能体类型"
		return aiResponse(jsonText(map[string]any{"entries": entries})), nil
	})
	processed, err := a.processReleaseNotesJob(context.Background())
	if err != nil || !processed || calls.Load() != 1 || releaseJob(t, a, sprint).State != "draft" {
		t.Fatal("job did not finish", processed, calls.Load(), err)
	}
	draft := releaseResponseMap(t, releaseNotesAPI(t, a, http.MethodGet, sprint, "", ""))
	revision := int64(draft["revision"].(float64))
	entries := draft["entries"].([]any)
	entry := entries[0].(map[string]any)
	entry["imageIds"] = []any{float64(imageID)}
	entry["imageCaptions"] = map[string]any{fmt.Sprint(imageID): "智能体配置页面"}
	edited := releaseNotesAPI(t, a, http.MethodPatch, sprint, "", jsonText(map[string]any{"expectedRevision": revision, "entries": entries}))
	if edited.Code != 200 {
		t.Fatal(edited.Code, edited.text())
	}
	draft = releaseResponseMap(t, edited)
	revision = int64(draft["revision"].(float64))
	bundle := releaseNotesAPI(t, a, http.MethodGet, sprint, fmt.Sprintf("/bundle?expectedRevision=%d", revision), "")
	if bundle.Code != 200 || bundle.Header.Get("Content-Type") != "application/zip" || bundle.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(bundle.Code, bundle.Header, bundle.text())
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle.Body), int64(len(bundle.Body)))
	if err != nil || len(reader.File) != 4 {
		t.Fatal("invalid portable bundle", err)
	}
	if replay := releaseNotesAPI(t, a, http.MethodGet, sprint, fmt.Sprintf("/bundle?expectedRevision=%d", revision-1), ""); replay.Code != 409 {
		t.Fatal("stale revision exported", replay.Code)
	}
	if response := releaseNotesAPI(t, a, http.MethodGet, sprint, "/unknown", ""); response.Code != 404 {
		t.Fatal("unknown nested resource accepted", response.Code)
	}
}

func TestReleaseNotesChangedSourceBlocksEditAndEveryExport(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V9.2.1")
	id := releaseContentRequirement(t, a, "V9.2.1", "已完成", "产品需求", "API")
	settings := releaseSettings(t, a)
	response := releaseNotesAPI(t, a, http.MethodPost, sprint, "", jsonText(map[string]any{"confirmed": true, "expectedRevision": 0, "expectedSettingsVersion": settings.Version}))
	if response.Code != 202 {
		t.Fatal(response.text())
	}
	aiMock(a, func(request *http.Request) (*http.Response, error) {
		_, batch := releaseProviderInput(t, request)
		return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(batch)})), nil
	})
	if ok, err := a.processReleaseNotesJob(context.Background()); err != nil || !ok {
		t.Fatal(err)
	}
	before := releaseResponseMap(t, releaseNotesAPI(t, a, http.MethodGet, sprint, "", ""))
	revision := int64(before["revision"].(float64))
	bulkFixtureExec(t, a, `UPDATE requirements SET description='source changed after draft' WHERE id=?`, id)
	afterResponse := releaseNotesAPI(t, a, http.MethodGet, sprint, "", "")
	after := releaseResponseMap(t, afterResponse)
	if after["sourceChanged"] != true || after["canEdit"] != false || after["markdown"] != "" || after["canGenerate"] != true {
		t.Fatal(after)
	}
	patch := releaseNotesAPI(t, a, http.MethodPatch, sprint, "", jsonText(map[string]any{"expectedRevision": revision, "entries": before["entries"]}))
	if patch.Code != 409 || !strings.Contains(patch.text(), "release_notes_source_changed") {
		t.Fatal(patch.Code, patch.text())
	}
	bundle := releaseNotesAPI(t, a, http.MethodGet, sprint, fmt.Sprintf("/bundle?expectedRevision=%d", revision), "")
	if bundle.Code != 409 || strings.HasPrefix(bundle.Header.Get("Content-Type"), "application/zip") {
		t.Fatal("changed source exported", bundle.Code, bundle.Header)
	}
	regenerate := releaseNotesAPI(t, a, http.MethodPost, sprint, "", jsonText(map[string]any{"confirmed": true, "expectedRevision": revision, "expectedSettingsVersion": settings.Version, "replaceDraft": true}))
	if regenerate.Code != 202 || releaseJob(t, a, sprint).State != "queued" {
		t.Fatal(regenerate.Code, regenerate.text())
	}
}

func TestReleaseNotesAutomaticCompletionIsDurableAndDoesNotCallProviderInline(t *testing.T) {
	a := aiApp(t)
	settings := releaseSettings(t, a)
	w := apiRequest(a, http.MethodPatch, "/api/organization/ai-settings", "u_admin", projectID, jsonText(map[string]any{"autoReleaseNotes": true, "expectedVersion": settings.Version, "confirmAutomatic": true}))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	result, err := a.db.Exec(`INSERT INTO sprints(tenant_id,project_id,code,name,goal,start_date,end_date,status,capacity,created_at,updated_at)VALUES(?,?,'AUTO','V9.3.0','','2026-09-01','2026-09-10','进行中',40,?,?)`, tenantID, projectID, orgNow(), orgNow())
	if err != nil {
		t.Fatal(err)
	}
	sprint, _ := result.LastInsertId()
	var doneStatus string
	if err = a.db.QueryRow(`SELECT key FROM requirement_statuses WHERE tenant_id=? AND project_id=? AND category='done' ORDER BY id LIMIT 1`, tenantID, projectID).Scan(&doneStatus); err != nil {
		t.Fatal(err)
	}
	releaseContentRequirement(t, a, "V9.3.0", doneStatus, "产品需求", "大模型")
	var calls atomic.Int32
	aiMock(a, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		_, batch := releaseProviderInput(t, request)
		return aiResponse(jsonText(map[string]any{"entries": releaseContentEntries(batch)})), nil
	})
	completion := apiRequest(a, http.MethodPost, fmt.Sprintf("/api/sprints/%d/complete", sprint), "u_admin", projectID, `{"targetSprint":"待规划"}`)
	if completion.Code != 200 || calls.Load() != 0 {
		t.Fatal("completion invoked AI inline", completion.Code, calls.Load(), completion.Body.String())
	}
	job := releaseJob(t, a, sprint)
	if !job.Automatic || job.State != "queued" || job.Session != "" {
		t.Fatal("automatic job not durable", job)
	}
	if ok, err := a.processReleaseNotesJob(context.Background()); err != nil || !ok || calls.Load() != 1 || releaseJob(t, a, sprint).State != "draft" {
		t.Fatal("automatic worker failed", err, calls.Load())
	}
}

func TestReleaseNotesWorkerRejectsChangedConfigurationWithoutCallingProvider(t *testing.T) {
	a := aiApp(t)
	sprint := releaseContentSprint(t, a, "V9.4.0")
	releaseContentRequirement(t, a, "V9.4.0", "已完成", "产品需求", "其他")
	settings := releaseSettings(t, a)
	queued := releaseNotesAPI(t, a, http.MethodPost, sprint, "", jsonText(map[string]any{"confirmed": true, "expectedRevision": 0, "expectedSettingsVersion": settings.Version}))
	if queued.Code != 202 {
		t.Fatal(queued.text())
	}
	bulkFixtureExec(t, a, `UPDATE organization_ai_settings SET version=version+1 WHERE tenant_id=?`, tenantID)
	var calls atomic.Int32
	aiMock(a, func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, fmt.Errorf("must not call") })
	if processed, err := a.processReleaseNotesJob(context.Background()); err != nil || processed {
		t.Fatal("invalid job reported provider processing", processed, err)
	}
	job := releaseJob(t, a, sprint)
	if calls.Load() != 0 || job.State != "failed" || !strings.Contains(job.Error, "AI 配置已变化") {
		t.Fatal("stale settings reached provider", calls.Load(), job)
	}
}

func TestReleaseNotesCompletionCannotBeBypassedByCreatingTerminalSprint(t *testing.T) {
	a := testApp(t)
	for _, status := range []string{"已完成", "已取消"} {
		response := apiRequest(a, http.MethodPost, "/api/sprints", "u_admin", projectID, jsonText(map[string]any{"name": "旁路-" + status, "status": status, "startDate": "2026-09-01", "endDate": "2026-09-10"}))
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "新建迭代只能选择") {
			t.Fatalf("terminal sprint creation bypassed lifecycle for %s: %d %s", status, response.Code, response.Body.String())
		}
	}
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM sprints WHERE tenant_id=? AND project_id=? AND name LIKE '旁路-%'`, tenantID, projectID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected terminal sprint was persisted: %d %v", count, err)
	}
}
