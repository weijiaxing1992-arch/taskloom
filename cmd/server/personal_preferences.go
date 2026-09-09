package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type detailPreferences struct {
	BasicFields     []string `json:"basicFields"`
	CustomFieldKeys []string `json:"customFieldKeys"`
}

const savePersonalPreferenceSQL = `INSERT INTO user_view_preferences(tenant_id,project_id,user_id,view_key,columns_json,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id,view_key) DO UPDATE SET columns_json=excluded.columns_json,updated_at=excluded.updated_at`

func decodePreference(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		fail(w, 422, "validation_error", "请求字段格式不正确")
		return false
	}
	return true
}

func (a *App) readPersonalPreference(project, key string, value any) error {
	var raw string
	err := a.db.QueryRow(`SELECT columns_json FROM user_view_preferences WHERE tenant_id=? AND project_id=? AND user_id=? AND view_key=?`, tenantID, project, a.uid(), key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), value)
}

func (a *App) savePersonalPreference(project, key string, value any) error {
	_, err := a.db.Exec(savePersonalPreferenceSQL, tenantID, project, a.uid(), key, jsonText(value), time.Now().UTC().Format(time.RFC3339))
	return err
}

type displayPreference struct {
	FontSize  string `json:"fontSize"`
	ThemeMode string `json:"themeMode"`
}

func (a *App) displayPreferences(w http.ResponseWriter, r *http.Request) {
	value := displayPreference{FontSize: "standard", ThemeMode: "light"}
	switch r.Method {
	case http.MethodGet:
		if err := a.readPersonalPreference("", "display", &value); err != nil {
			fail(w, 500, "db_error", "无法读取显示设置")
			return
		}
	case http.MethodPatch:
		var patch *struct {
			FontSize  json.RawMessage `json:"fontSize"`
			ThemeMode json.RawMessage `json:"themeMode"`
		}
		if !decodePreference(w, r, &patch) {
			return
		}
		if patch == nil || len(patch.FontSize) == 0 && len(patch.ThemeMode) == 0 {
			fail(w, 422, "validation_error", "请求字段格式不正确")
			return
		}
		var font, theme string
		if len(patch.FontSize) != 0 && (json.Unmarshal(patch.FontSize, &font) != nil || !validChoice(font, []string{"small", "standard", "large", "extraLarge"})) {
			fail(w, 422, "validation_error", "字号设置无效")
			return
		}
		if len(patch.ThemeMode) != 0 && (json.Unmarshal(patch.ThemeMode, &theme) != nil || !validChoice(theme, []string{"light", "dark", "auto"})) {
			fail(w, 422, "validation_error", "请求字段格式不正确")
			return
		}
		// Merge in one transaction so the separate font and theme controls cannot
		// reset one another. Preferences belong to the authenticated account.
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
		defer tx.Rollback()
		var raw string
		err = tx.QueryRowContext(r.Context(), `SELECT columns_json FROM user_view_preferences WHERE tenant_id=? AND project_id='' AND user_id=? AND view_key='display'`, tenantID, a.uid()).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) || err == nil && json.Unmarshal([]byte(raw), &value) != nil {
			fail(w, 500, "db_error", "无法读取显示设置")
			return
		}
		if len(patch.FontSize) != 0 {
			value.FontSize = font
		}
		if len(patch.ThemeMode) != 0 {
			value.ThemeMode = theme
		}
		if _, err = tx.ExecContext(r.Context(), savePersonalPreferenceSQL, tenantID, "", a.uid(), "display", jsonText(value), time.Now().UTC().Format(time.RFC3339)); err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
		if err = tx.Commit(); err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
	default:
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	write(w, 200, value)
}

func (a *App) requirementDetailPreferences(w http.ResponseWriter, r *http.Request) {
	value := detailPreferences{}
	switch r.Method {
	case http.MethodGet:
		if err := a.readPersonalPreference(a.pid(), "requirement-detail", &value); err != nil {
			fail(w, 500, "db_error", "无法读取显示设置")
			return
		}
	case http.MethodPatch:
		var patch *struct {
			BasicFields     json.RawMessage `json:"basicFields"`
			CustomFieldKeys json.RawMessage `json:"customFieldKeys"`
		}
		if !decodePreference(w, r, &patch) {
			return
		}
		if patch == nil || len(patch.BasicFields) == 0 && len(patch.CustomFieldKeys) == 0 {
			fail(w, 422, "validation_error", "请求字段格式不正确")
			return
		}
		if len(patch.BasicFields) != 0 && json.Unmarshal(patch.BasicFields, &value.BasicFields) != nil || len(patch.CustomFieldKeys) != 0 && json.Unmarshal(patch.CustomFieldKeys, &value.CustomFieldKeys) != nil {
			fail(w, 422, "validation_error", "请求字段格式不正确")
			return
		}
		allowed := map[string]bool{}
		for _, key := range []string{"status", "category", "sprint", "priority", "owner", "assignee", "createdAt", "updatedAt", "weightTotal"} {
			allowed[key] = true
		}
		if !validPreferenceKeys(value.BasicFields, allowed) {
			fail(w, 422, "validation_error", "详情字段配置无效")
			return
		}
		if len(patch.CustomFieldKeys) != 0 {
			defs, err := a.definitions("requirement", true)
			if err != nil {
				fail(w, 500, "db_error", "无法读取显示设置")
				return
			}
			allowed = map[string]bool{}
			for _, d := range defs {
				allowed[d.Key] = true
			}
			if !validPreferenceKeys(value.CustomFieldKeys, allowed) {
				fail(w, 422, "validation_error", "详情字段配置无效")
				return
			}
		}
		// Read and merge within the same transaction: an omitted group is not an
		// instruction to reset it, including when another request edits that group.
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
		defer tx.Rollback()
		var raw string
		var current detailPreferences
		err = tx.QueryRowContext(r.Context(), `SELECT columns_json FROM user_view_preferences WHERE tenant_id=? AND project_id=? AND user_id=? AND view_key=?`, tenantID, a.pid(), a.uid(), "requirement-detail").Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) || err == nil && json.Unmarshal([]byte(raw), &current) != nil {
			fail(w, 500, "db_error", "无法读取显示设置")
			return
		}
		if len(patch.BasicFields) == 0 {
			value.BasicFields = current.BasicFields
		}
		if len(patch.CustomFieldKeys) == 0 {
			value.CustomFieldKeys = current.CustomFieldKeys
		}
		if _, err = tx.ExecContext(r.Context(), savePersonalPreferenceSQL, tenantID, a.pid(), a.uid(), "requirement-detail", jsonText(value), time.Now().UTC().Format(time.RFC3339)); err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
		if err = tx.Commit(); err != nil {
			fail(w, 500, "db_error", "无法保存显示设置")
			return
		}
	default:
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	write(w, 200, value)
}

func validPreferenceKeys(keys []string, allowed map[string]bool) bool {
	if len(keys) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if !allowed[key] || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
