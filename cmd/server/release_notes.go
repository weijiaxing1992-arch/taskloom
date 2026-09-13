package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const releaseNotesMaxAttempts = 3
const releaseNotesLeaseSeconds = 120

type releaseNotesJob struct {
	ID, SprintID, Revision                                                          int64
	Project, Actor, Session, State, SourceJSON, SourceHash, Lease, Error, UpdatedAt string
	Automatic                                                                       bool
	SettingsVersion, Attempts                                                       int
	LeaseUntil, NextAttempt                                                         int64
}

type releaseNotesWork struct {
	Job      releaseNotesJob
	Source   releaseNoteSource
	Settings aiSettings
	Key      string // In-memory only. Never persisted, included in errors, or returned through HTTP.
}

func (a *App) migrateReleaseNotes() error {
	_, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS release_note_jobs(
 id INTEGER PRIMARY KEY AUTOINCREMENT,tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,sprint_id INTEGER NOT NULL,
 actor_id TEXT NOT NULL,session_hash TEXT NOT NULL DEFAULT '',automatic INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('queued','generating','draft','failed')),revision INTEGER NOT NULL DEFAULT 1,
 settings_version INTEGER NOT NULL,source_json TEXT NOT NULL,source_hash TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,next_attempt INTEGER NOT NULL DEFAULT 0,lease_token TEXT NOT NULL DEFAULT '',lease_until INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(tenant_id,project_id,sprint_id));
CREATE INDEX IF NOT EXISTS idx_release_note_jobs_pending ON release_note_jobs(tenant_id,state,next_attempt,lease_until);
CREATE TABLE IF NOT EXISTS release_note_drafts(
 tenant_id TEXT NOT NULL,project_id TEXT NOT NULL,sprint_id INTEGER NOT NULL,entries_json TEXT NOT NULL,
 source_json TEXT NOT NULL,source_hash TEXT NOT NULL,created_by TEXT NOT NULL,edited_by TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 PRIMARY KEY(tenant_id,project_id,sprint_id));`)
	return err // No historical enqueue/backfill: enabling automatic generation is not consent to process old sprints.
}

func releaseNotesConflict() error {
	return aiFailure(409, "release_notes_conflict", "升级日志已变化，请刷新后重试")
}
func releaseNotesSourceChanged() error {
	return aiFailure(409, "release_notes_source_changed", "完成需求已变化，请重新生成升级日志")
}
func releaseNotesSettingsChanged() error {
	return aiFailure(409, "release_notes_settings_changed", "AI 配置已变化，请重新确认后生成升级日志")
}

func (a *App) releaseNotesAccess(ctx context.Context, q stateStore, manage bool) error {
	if err := a.requireOperationAccess(ctx, q); err != nil {
		return err
	}
	var active int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE tenant_id=? AND id=? AND status='active'`, tenantID, a.pid()).Scan(&active); err != nil {
		return err
	}
	if active != 1 {
		return aiFailure(403, "project_forbidden", "无权访问该项目")
	}
	role, err := a.requirementStateRole(ctx, q)
	if err != nil {
		return err
	}
	if manage {
		if a.impersonation != nil {
			return aiFailure(403, "impersonation_restricted", "代访问期间不能生成或编辑升级日志")
		}
		if !validChoice(role, []string{"tenant_admin", "project_admin", "product", "frontend_lead", "backend_lead"}) {
			return aiFailure(403, "sprint_manager_required", "当前角色不可管理迭代")
		}
	}
	return nil
}

func (a *App) beginReleaseNotesWrite(ctx context.Context, session bool) (*sql.Tx, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err == nil {
		err = a.releaseNotesAccess(ctx, tx, true)
	}
	if err == nil && session {
		err = a.requireAdministrationSession(ctx, tx)
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

const releaseNotesJobColumns = `id,project_id,sprint_id,actor_id,session_hash,automatic,state,revision,settings_version,source_json,source_hash,attempts,next_attempt,lease_token,lease_until,last_error,updated_at`

func scanReleaseNotesJob(row interface{ Scan(...any) error }) (releaseNotesJob, error) {
	var j releaseNotesJob
	err := row.Scan(&j.ID, &j.Project, &j.SprintID, &j.Actor, &j.Session, &j.Automatic, &j.State, &j.Revision, &j.SettingsVersion, &j.SourceJSON, &j.SourceHash, &j.Attempts, &j.NextAttempt, &j.Lease, &j.LeaseUntil, &j.Error, &j.UpdatedAt)
	return j, err
}
func (a *App) readReleaseNotesJob(ctx context.Context, q stateStore, sprint int64) (releaseNotesJob, error) {
	return scanReleaseNotesJob(q.QueryRowContext(ctx, `SELECT `+releaseNotesJobColumns+` FROM release_note_jobs WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, a.pid(), sprint))
}

func (a *App) releaseNotesSprint(ctx context.Context, q stateStore, id int64) (string, string, error) {
	var name, status string
	err := q.QueryRowContext(ctx, `SELECT name,status FROM sprints WHERE tenant_id=? AND project_id=? AND id=?`, tenantID, a.pid(), id).Scan(&name, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", aiFailure(404, "not_found", "迭代不存在")
	}
	return name, status, err
}

// Error strings are deliberately bounded, public, and unrelated to provider response text.
func releaseNotesPublicError(err error) string {
	var problem *organizationError
	var state stateError
	if errors.As(err, &problem) {
		switch problem.Code {
		case "release_notes_settings_changed":
			return "AI 配置已变化，请重新确认后生成升级日志"
		case "release_notes_source_changed":
			return "完成需求已变化，请重新生成升级日志"
		}
		if problem.Status == 401 || problem.Status == 403 {
			return "任务发起人的权限或会话已失效，请重新生成"
		}
		if problem.Status == 422 {
			return "完成需求来源无效、为空或超过生成限制，请检查后重试"
		}
	}
	if errors.As(err, &state) && state.status == 403 {
		return "任务发起人的权限或会话已失效，请重新生成"
	}
	return "升级日志生成失败，请稍后重试或联系管理员"
}
func releaseNotesRetryable(err error) bool {
	var problem *organizationError
	var state stateError
	if errors.As(err, &problem) {
		return problem.Status == 429 || problem.Status >= 500
	}
	if errors.As(err, &state) {
		return state.status >= 500
	}
	return true
}

func (a *App) auditReleaseNotes(ctx context.Context, q stateStore, sprint int64, action string, revision int64, automatic bool, count int) error {
	_, err := q.ExecContext(ctx, `INSERT INTO audit_logs(tenant_id,project_id,actor_id,object_type,object_id,action,after_json,created_at)VALUES(?,?,?,?,?,?,?,?)`, tenantID, a.pid(), a.uid(), "sprint", fmt.Sprint(sprint), action, jsonText(map[string]any{"revision": revision, "automatic": automatic, "requirementCount": count, "published": false}), orgNow())
	return err
}

// Called inside completeSprint's transaction, after unfinished work is moved and
// the completion activity is written. Queuing never invokes a provider/worker.
func (a *App) enqueueAutomaticReleaseNotes(ctx context.Context, tx *sql.Tx, sprint int64) error {
	s, err := a.readAISettings(ctx, tx)
	if err != nil {
		return err
	}
	if !s.AutoReleaseNotes || !s.Enabled || len(s.Encrypted) == 0 || a.impersonation != nil {
		return nil
	}
	if _, err = a.readReleaseNotesJob(ctx, tx, sprint); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	source, hash, sourceErr := a.releaseNotesSource(ctx, tx, a.pid(), sprint)
	if sourceErr == nil {
		sourceErr = a.releaseNotesAccess(ctx, tx, true)
	}
	state, message := "queued", ""
	if sourceErr != nil || len(source.Requirements) == 0 {
		state = "failed"
		if sourceErr == nil {
			sourceErr = aiFailure(422, "release_notes_empty", "没有符合条件的完成需求")
		}
		message = releaseNotesPublicError(sourceErr)
	}
	now := orgNow()
	_, err = tx.ExecContext(ctx, `INSERT INTO release_note_jobs(tenant_id,project_id,sprint_id,actor_id,automatic,state,settings_version,source_json,source_hash,last_error,created_at,updated_at)VALUES(?,?,?,?,1,?,?,?,?,?,?,?)`, tenantID, a.pid(), sprint, a.uid(), state, s.Version, jsonText(source), hash, message, now, now)
	if err != nil {
		return err
	}
	return a.auditReleaseNotes(ctx, tx, sprint, "release_notes_queued", 1, true, len(source.Requirements))
}

func (a *App) sprintReleaseNotes(w http.ResponseWriter, r *http.Request, sprint int64, tail []string) {
	if len(tail) == 1 && tail[0] == "bundle" {
		if r.Method != http.MethodGet {
			fail(w, 405, "method_not_allowed", "不支持的方法")
			return
		}
		a.downloadReleaseNotesBundle(w, r, sprint)
		return
	}
	if len(tail) != 0 {
		fail(w, 404, "not_found", "升级日志资源不存在")
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.writeReleaseNotesView(w, r, sprint, 200)
	case http.MethodPost:
		a.queueManualReleaseNotes(w, r, sprint)
	case http.MethodPatch:
		a.editReleaseNotes(w, r, sprint)
	default:
		fail(w, 405, "method_not_allowed", "不支持的方法")
	}
}

func (a *App) writeReleaseNotesView(w http.ResponseWriter, r *http.Request, sprint int64, status int) {
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.releaseNotesAccess(r.Context(), tx, false); err != nil {
		failAI(w, err)
		return
	}
	name, sprintStatus, err := a.releaseNotesSprint(r.Context(), tx, sprint)
	if err != nil {
		failAI(w, err)
		return
	}
	s, err := a.readAISettings(r.Context(), tx)
	if err != nil {
		failAI(w, err)
		return
	}
	j, err := a.readReleaseNotesJob(r.Context(), tx, sprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failAI(w, err)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		j.State = "none"
	}
	managerErr := a.releaseNotesAccess(r.Context(), tx, true)
	var problem *organizationError
	var permission stateError
	if managerErr != nil && !errors.As(managerErr, &problem) && !errors.As(managerErr, &permission) {
		failAI(w, managerErr)
		return
	}
	liveSource, liveHash, sourceErr := a.releaseNotesSource(r.Context(), tx, a.pid(), sprint)
	if sourceErr != nil && !errors.As(sourceErr, &problem) && !errors.As(sourceErr, &permission) {
		failAI(w, sourceErr)
		return
	}
	source := liveSource
	entries := []releaseNoteEntry{}
	var draftJSON, draftSourceJSON, draftHash, updated string
	err = tx.QueryRowContext(r.Context(), `SELECT entries_json,source_json,source_hash,updated_at FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, a.pid(), sprint).Scan(&draftJSON, &draftSourceJSON, &draftHash, &updated)
	hasDraft := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failAI(w, err)
		return
	}
	if hasDraft {
		if json.Unmarshal([]byte(draftJSON), &entries) != nil || json.Unmarshal([]byte(draftSourceJSON), &source) != nil {
			failAI(w, errors.New("invalid stored release notes"))
			return
		}
		normalizeReleaseNoteSourceCodes(&source)
		if validateReleaseNotesEntries(source, entries) != nil {
			failAI(w, errors.New("invalid stored release notes"))
			return
		}
		entries = sortReleaseNotesEntries(entries)
	}
	sourceChanged := hasDraft && (sourceErr != nil || draftHash == "" || draftHash != liveHash)
	sources := []map[string]any{}
	images := []map[string]any{}
	for _, requirement := range source.Requirements {
		sources = append(sources, map[string]any{"id": requirement.ID, "code": requirement.Code, "title": requirement.Title})
		for _, image := range requirement.Images {
			images = append(images, map[string]any{"id": image.ID, "name": image.Name, "url": image.URL, "requirementId": requirement.ID})
		}
	}
	busy := j.State == "queued" || j.State == "generating"
	message := j.Error
	if message == "" && sourceChanged {
		message = "完成需求已变化，请重新生成升级日志"
	}
	if message == "" && sourceErr != nil {
		message = releaseNotesPublicError(sourceErr)
	}
	message = localizedError(w.Header().Get("Content-Language"), 422, "release_notes_error", message)
	if j.Error == "" && sourceErr == nil && !sourceChanged {
		message = ""
	}
	markdown := ""
	if hasDraft && !sourceChanged {
		markdown = renderReleaseNotesMarkdown(source, entries)
	}
	result := map[string]any{"state": j.State, "revision": j.Revision, "entries": entries, "categories": releaseNoteCategories, "markdown": markdown, "canGenerate": managerErr == nil && sprintStatus == "已完成" && s.Enabled && len(s.Encrypted) > 0 && sourceErr == nil && len(liveSource.Requirements) > 0 && !busy, "canEdit": managerErr == nil && hasDraft && !sourceChanged && !busy, "sourceChanged": sourceChanged, "configured": len(s.Encrypted) > 0, "enabled": s.Enabled, "autoEnabled": s.AutoReleaseNotes, "model": s.Model, "baseUrl": s.BaseURL, "settingsVersion": s.Version, "sourceCount": len(liveSource.Requirements), "sources": sources, "images": images, "error": message, "updatedAt": j.UpdatedAt, "draftUpdatedAt": updated, "sprintName": name, "completedAt": source.ReleaseDate, "attempts": j.Attempts, "maxAttempts": releaseNotesMaxAttempts, "automatic": j.Automatic}
	if err = tx.Commit(); err != nil {
		failAI(w, err)
		return
	}
	write(w, status, result)
}

func (a *App) queueManualReleaseNotes(w http.ResponseWriter, r *http.Request, sprint int64) {
	var input struct {
		Confirmed               bool   `json:"confirmed"`
		ExpectedRevision        *int64 `json:"expectedRevision"`
		ExpectedSettingsVersion *int   `json:"expectedSettingsVersion"`
		ReplaceDraft            bool   `json:"replaceDraft"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	if !input.Confirmed || input.ExpectedRevision == nil || *input.ExpectedRevision < 0 || input.ExpectedSettingsVersion == nil {
		failAI(w, aiFailure(422, "release_notes_confirmation_required", "请确认将完成需求发送到企业配置的 AI 服务，并刷新配置后生成"))
		return
	}
	tx, err := a.beginReleaseNotesWrite(r.Context(), true)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	_, status, err := a.releaseNotesSprint(r.Context(), tx, sprint)
	if err != nil {
		failAI(w, err)
		return
	}
	if status != "已完成" {
		failAI(w, aiFailure(409, "sprint_not_completed", "仅已完成的迭代可生成升级日志"))
		return
	}
	s, err := a.readAISettings(r.Context(), tx)
	if err != nil {
		failAI(w, err)
		return
	}
	if s.Version != *input.ExpectedSettingsVersion {
		failAI(w, releaseNotesSettingsChanged())
		return
	}
	if !s.Enabled || len(s.Encrypted) == 0 || len(a.wecomKey) != 32 {
		failAI(w, aiFailure(422, "ai_not_configured", "AI 服务尚未启用或密钥不可用"))
		return
	}
	j, err := a.readReleaseNotesJob(r.Context(), tx, sprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		failAI(w, err)
		return
	}
	exists := err == nil
	if exists && (j.State == "queued" || j.State == "generating") {
		// Repeated clicks/replayed requests refer to the existing durable job.
		if err = tx.Commit(); err != nil {
			failAI(w, err)
			return
		}
		a.writeReleaseNotesView(w, r, sprint, 202)
		return
	}
	if j.Revision != *input.ExpectedRevision {
		failAI(w, releaseNotesConflict())
		return
	}
	var drafts int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, a.pid(), sprint).Scan(&drafts); err != nil {
		failAI(w, err)
		return
	}
	if drafts > 0 && !input.ReplaceDraft {
		failAI(w, aiFailure(409, "release_notes_replace_confirmation", "重新生成会替换已有草稿，请先明确确认"))
		return
	}
	var attempts int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM audit_logs WHERE tenant_id=? AND actor_id=? AND action='release_notes_generate' AND created_at>=?`, tenantID, a.uid(), time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)).Scan(&attempts); err != nil {
		failAI(w, err)
		return
	}
	if attempts >= 10 {
		failAI(w, aiFailure(429, "ai_rate_limited", "AI 请求过于频繁，请稍后重试"))
		return
	}
	source, hash, err := a.releaseNotesSource(r.Context(), tx, a.pid(), sprint)
	if err != nil {
		failAI(w, err)
		return
	}
	if len(source.Requirements) == 0 {
		failAI(w, aiFailure(422, "release_notes_empty", "没有符合条件的完成需求"))
		return
	}
	now := orgNow()
	revision := j.Revision + 1
	if exists {
		_, err = tx.ExecContext(r.Context(), `UPDATE release_note_jobs SET actor_id=?,session_hash=?,automatic=0,state='queued',revision=?,settings_version=?,source_json=?,source_hash=?,attempts=0,next_attempt=0,lease_token='',lease_until=0,last_error='',updated_at=? WHERE tenant_id=? AND project_id=? AND sprint_id=? AND revision=?`, a.uid(), a.sessionToken, revision, s.Version, jsonText(source), hash, now, tenantID, a.pid(), sprint, j.Revision)
	} else {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO release_note_jobs(tenant_id,project_id,sprint_id,actor_id,session_hash,automatic,state,revision,settings_version,source_json,source_hash,created_at,updated_at)VALUES(?,?,?,?,?,0,'queued',?,?,?,?,?,?)`, tenantID, a.pid(), sprint, a.uid(), a.sessionToken, revision, s.Version, jsonText(source), hash, now, now)
	}
	if err == nil {
		err = a.auditReleaseNotes(r.Context(), tx, sprint, "release_notes_generate", revision, false, len(source.Requirements))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	a.writeReleaseNotesView(w, r, sprint, 202)
}

func (a *App) editReleaseNotes(w http.ResponseWriter, r *http.Request, sprint int64) {
	var input struct {
		ExpectedRevision *int64             `json:"expectedRevision"`
		Entries          []releaseNoteEntry `json:"entries"`
	}
	if err := decodeOrganizationJSON(w, r, &input); err != nil {
		failAI(w, err)
		return
	}
	if input.ExpectedRevision == nil {
		failAI(w, releaseNotesConflict())
		return
	}
	tx, err := a.beginReleaseNotesWrite(r.Context(), true)
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	if _, _, err = a.releaseNotesSprint(r.Context(), tx, sprint); err != nil {
		failAI(w, err)
		return
	}
	j, err := a.readReleaseNotesJob(r.Context(), tx, sprint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = aiFailure(404, "not_found", "升级日志草稿不存在")
		}
		failAI(w, err)
		return
	}
	if j.Revision != *input.ExpectedRevision || j.State == "queued" || j.State == "generating" {
		failAI(w, releaseNotesConflict())
		return
	}
	var sourceJSON, sourceHash string
	if err = tx.QueryRowContext(r.Context(), `SELECT source_json,source_hash FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, a.pid(), sprint).Scan(&sourceJSON, &sourceHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = aiFailure(404, "not_found", "升级日志草稿不存在")
		}
		failAI(w, err)
		return
	}
	var source releaseNoteSource
	if json.Unmarshal([]byte(sourceJSON), &source) != nil {
		failAI(w, errors.New("invalid stored release source"))
		return
	}
	_, liveHash, sourceErr := a.releaseNotesSource(r.Context(), tx, a.pid(), sprint)
	if sourceErr != nil || liveHash != sourceHash {
		failAI(w, releaseNotesSourceChanged())
		return
	}
	if err = validateReleaseNotesEntries(source, input.Entries); err != nil {
		failAI(w, err)
		return
	}
	input.Entries = sortReleaseNotesEntries(input.Entries)
	now := orgNow()
	_, err = tx.ExecContext(r.Context(), `UPDATE release_note_drafts SET entries_json=?,edited_by=?,updated_at=? WHERE tenant_id=? AND project_id=? AND sprint_id=?`, jsonText(input.Entries), a.uid(), now, tenantID, a.pid(), sprint)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE release_note_jobs SET revision=revision+1,state='draft',last_error='',updated_at=? WHERE tenant_id=? AND project_id=? AND sprint_id=? AND revision=?`, now, tenantID, a.pid(), sprint, j.Revision)
	}
	if err == nil {
		err = a.auditReleaseNotes(r.Context(), tx, sprint, "release_notes_edit", j.Revision+1, j.Automatic, len(source.Requirements))
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		failAI(w, err)
		return
	}
	a.writeReleaseNotesView(w, r, sprint, 200)
}

func (a *App) downloadReleaseNotesBundle(w http.ResponseWriter, r *http.Request, sprint int64) {
	values := r.URL.Query()
	raw := values.Get("expectedRevision")
	expected, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || expected < 1 || len(values) != 1 || len(values["expectedRevision"]) != 1 {
		failAI(w, releaseNotesConflict())
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		failAI(w, err)
		return
	}
	defer tx.Rollback()
	if err = a.releaseNotesAccess(r.Context(), tx, false); err != nil {
		failAI(w, err)
		return
	}
	if _, _, err = a.releaseNotesSprint(r.Context(), tx, sprint); err != nil {
		failAI(w, err)
		return
	}
	j, err := a.readReleaseNotesJob(r.Context(), tx, sprint)
	if err != nil || j.State != "draft" || j.Revision != expected {
		if errors.Is(err, sql.ErrNoRows) {
			failAI(w, aiFailure(404, "not_found", "升级日志草稿不存在"))
		} else if err != nil {
			failAI(w, err)
		} else {
			failAI(w, releaseNotesConflict())
		}
		return
	}
	var entriesJSON, sourceJSON, sourceHash string
	if err = tx.QueryRowContext(r.Context(), `SELECT entries_json,source_json,source_hash FROM release_note_drafts WHERE tenant_id=? AND project_id=? AND sprint_id=?`, tenantID, a.pid(), sprint).Scan(&entriesJSON, &sourceJSON, &sourceHash); err != nil {
		failAI(w, err)
		return
	}
	var source releaseNoteSource
	var entries []releaseNoteEntry
	if json.Unmarshal([]byte(sourceJSON), &source) != nil || json.Unmarshal([]byte(entriesJSON), &entries) != nil {
		failAI(w, errors.New("invalid stored release notes"))
		return
	}
	_, liveHash, err := a.releaseNotesSource(r.Context(), tx, a.pid(), sprint)
	if err != nil || liveHash != sourceHash || j.SourceHash != sourceHash {
		failAI(w, releaseNotesSourceChanged())
		return
	}
	bundle, err := buildReleaseNotesBundle(r.Context(), tx, source, entries, int(j.Revision))
	if err != nil {
		failAI(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		failAI(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="devflow-upgrade-notes.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(bundle)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle)
}

func (a *App) releaseNotesJobAuthority(ctx context.Context, q stateStore, j releaseNotesJob) (aiSettings, releaseNoteSource, error) {
	scoped := *a
	scoped.project, scoped.user, scoped.sessionToken, scoped.impersonation = j.Project, j.Actor, j.Session, nil
	var s aiSettings
	var source releaseNoteSource
	if err := scoped.releaseNotesAccess(ctx, q, true); err != nil {
		return s, source, err
	}
	if !j.Automatic {
		if err := scoped.requireAdministrationSession(ctx, q); err != nil {
			return s, source, err
		}
	}
	_, status, err := scoped.releaseNotesSprint(ctx, q, j.SprintID)
	if err != nil {
		return s, source, err
	}
	if status != "已完成" {
		return s, source, releaseNotesSourceChanged()
	}
	s, err = scoped.readAISettings(ctx, q)
	if err != nil {
		return s, source, err
	}
	if s.Version != j.SettingsVersion || !s.Enabled || len(s.Encrypted) == 0 || j.Automatic && !s.AutoReleaseNotes {
		return s, source, releaseNotesSettingsChanged()
	}
	source, hash, err := scoped.releaseNotesSource(ctx, q, j.Project, j.SprintID)
	if err != nil {
		return s, source, err
	}
	if len(source.Requirements) == 0 || hash != j.SourceHash {
		return s, source, releaseNotesSourceChanged()
	}
	return s, source, nil
}

func (a *App) claimReleaseNotesJob(ctx context.Context) (*releaseNotesWork, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	j, err := scanReleaseNotesJob(tx.QueryRowContext(ctx, `SELECT `+releaseNotesJobColumns+` FROM release_note_jobs WHERE tenant_id=? AND ((state='queued' AND next_attempt<=?) OR (state='generating' AND lease_until<=?)) ORDER BY updated_at,id LIMIT 1`, tenantID, now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s, source, authorityErr := a.releaseNotesJobAuthority(ctx, tx, j)
	if authorityErr == nil && j.Attempts >= releaseNotesMaxAttempts {
		authorityErr = aiFailure(422, "release_notes_retry_limit", "已达到升级日志自动重试上限")
	}
	key := ""
	if authorityErr == nil {
		key, authorityErr = openAIKey(a.wecomKey, s.Encrypted)
	}
	if authorityErr != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE release_note_jobs SET state='failed',revision=revision+1,lease_token='',lease_until=0,last_error=?,updated_at=? WHERE tenant_id=? AND id=?`, releaseNotesPublicError(authorityErr), orgNow(), tenantID, j.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	lease, err := organizationID("release")
	if err != nil {
		return nil, err
	}
	j.Attempts++
	j.Revision++
	j.State = "generating"
	j.Lease = lease
	j.LeaseUntil = now + releaseNotesLeaseSeconds
	_, err = tx.ExecContext(ctx, `UPDATE release_note_jobs SET state='generating',revision=?,attempts=?,lease_token=?,lease_until=?,last_error='',updated_at=? WHERE tenant_id=? AND id=?`, j.Revision, j.Attempts, j.Lease, j.LeaseUntil, orgNow(), tenantID, j.ID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &releaseNotesWork{Job: j, Source: source, Settings: s, Key: key}, nil
}

func (a *App) releaseNotesHeartbeat(ctx context.Context, cancel context.CancelFunc, j releaseNotesJob, done chan<- struct{}) {
	defer close(done)
	timer := time.NewTicker(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			res, err := a.db.ExecContext(ctx, `UPDATE release_note_jobs SET lease_until=? WHERE tenant_id=? AND id=? AND state='generating' AND lease_token=?`, time.Now().Unix()+releaseNotesLeaseSeconds, tenantID, j.ID, j.Lease)
			if err != nil {
				cancel()
				return
			}
			changed, err := res.RowsAffected()
			if err != nil || changed != 1 {
				cancel()
				return
			}
		}
	}
}

func (a *App) finishReleaseNotesJob(ctx context.Context, work *releaseNotesWork, entries []releaseNoteEntry, generationErr error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE organization_write_locks SET revision=revision+1 WHERE tenant_id=?`, tenantID); err != nil {
		return err
	}
	j, err := scanReleaseNotesJob(tx.QueryRowContext(ctx, `SELECT `+releaseNotesJobColumns+` FROM release_note_jobs WHERE tenant_id=? AND id=?`, tenantID, work.Job.ID))
	if err != nil {
		return err
	}
	// A recovered job or later request owns a different fencing token: old output is discarded.
	if j.State != "generating" || j.Lease != work.Job.Lease {
		return nil
	}
	_, _, authorityErr := a.releaseNotesJobAuthority(ctx, tx, j)
	if authorityErr != nil {
		generationErr = authorityErr
	}
	if generationErr == nil {
		generationErr = validateReleaseNotesEntries(work.Source, entries)
	}
	if generationErr != nil {
		state, next := "failed", int64(0)
		if authorityErr == nil && releaseNotesRetryable(generationErr) && j.Attempts < releaseNotesMaxAttempts {
			state = "queued"
			next = time.Now().Unix() + int64(15*j.Attempts)
		}
		_, err = tx.ExecContext(ctx, `UPDATE release_note_jobs SET state=?,revision=revision+1,next_attempt=?,lease_token='',lease_until=0,last_error=?,updated_at=? WHERE tenant_id=? AND id=?`, state, next, releaseNotesPublicError(generationErr), orgNow(), tenantID, j.ID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	now := orgNow()
	_, err = tx.ExecContext(ctx, `INSERT INTO release_note_drafts(tenant_id,project_id,sprint_id,entries_json,source_json,source_hash,created_by,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,sprint_id)DO UPDATE SET entries_json=excluded.entries_json,source_json=excluded.source_json,source_hash=excluded.source_hash,created_by=excluded.created_by,edited_by='',created_at=excluded.created_at,updated_at=excluded.updated_at`, tenantID, j.Project, j.SprintID, jsonText(entries), jsonText(work.Source), j.SourceHash, j.Actor, now, now)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE release_note_jobs SET state='draft',revision=revision+1,next_attempt=0,lease_token='',lease_until=0,last_error='',updated_at=? WHERE tenant_id=? AND id=?`, now, tenantID, j.ID)
	}
	scoped := *a
	scoped.project, scoped.user = j.Project, j.Actor
	if err == nil {
		err = scoped.auditReleaseNotes(ctx, tx, j.SprintID, "release_notes_drafted", j.Revision+1, j.Automatic, len(work.Source.Requirements))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) processReleaseNotesJob(ctx context.Context) (bool, error) {
	work, err := a.claimReleaseNotesJob(ctx)
	if err != nil || work == nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	done := make(chan struct{})
	go a.releaseNotesHeartbeat(callCtx, cancel, work.Job, done)
	entries, generationErr := a.generateReleaseNotes(callCtx, work.Key, work.Settings.Model, work.Settings.BaseURL, work.Source)
	work.Key = ""
	cancel()
	<-done
	// A shutdown/cancelled provider must still leave a durable retry state when possible.
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	return true, a.finishReleaseNotesJob(finishCtx, work, entries, generationErr)
}

func (a *App) runReleaseNotes(ctx context.Context) {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := a.processReleaseNotesJob(ctx)
		if err == nil && processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
