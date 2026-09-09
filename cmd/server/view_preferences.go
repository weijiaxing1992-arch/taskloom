package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *App) requirementListPreferences(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view == "" {
		view = "requirement-list"
	}
	if view != "requirement-list" && view != "sprint-list" {
		fail(w, 422, "validation_error", "列表视图无效")
		return
	}
	if r.Method == http.MethodGet {
		var raw string
		err := a.db.QueryRow(`SELECT columns_json FROM user_view_preferences WHERE tenant_id=? AND project_id=? AND user_id=? AND view_key=?`, tenantID, a.pid(), a.uid(), view).Scan(&raw)
		if err == sql.ErrNoRows {
			write(w, 200, map[string]any{"columns": nil})
			return
		}
		var columns []string
		if err != nil || json.Unmarshal([]byte(raw), &columns) != nil {
			fail(w, 500, "db_error", "无法读取列表显示设置")
			return
		}
		write(w, 200, map[string]any{"columns": columns})
		return
	}
	if r.Method != http.MethodPatch {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	var body struct {
		Columns *[]string `json:"columns"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Columns == nil {
		fail(w, 422, "validation_error", "columns 须为列表字段数组")
		return
	}
	columns := *body.Columns
	if len(columns) > 100 {
		fail(w, 422, "validation_error", "最多配置 100 个列表字段")
		return
	}
	seen := map[string]bool{}
	for _, key := range columns {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key || utf8.RuneCountInString(key) > 128 || seen[key] {
			fail(w, 422, "validation_error", "列表字段须唯一且名称有效")
			return
		}
		seen[key] = true
	}
	if _, err := a.db.Exec(`INSERT INTO user_view_preferences(tenant_id,project_id,user_id,view_key,columns_json,updated_at)VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,project_id,user_id,view_key) DO UPDATE SET columns_json=excluded.columns_json,updated_at=excluded.updated_at`, tenantID, a.pid(), a.uid(), view, jsonText(columns), time.Now().UTC().Format(time.RFC3339)); err != nil {
		fail(w, 500, "db_error", "无法保存列表显示设置")
		return
	}
	write(w, 200, map[string]any{"columns": columns})
}
