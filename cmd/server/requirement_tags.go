package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

type requirementTag struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int    `json:"count"`
}

// Suggestions cover the whole project, independent of list filters/pagination.
// When old requirements used different colors, the most recently updated
// explicit color wins; a missing color never masks an older explicit color.
func (a *App) requirementTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, 405, "method_not_allowed", "不支持的方法")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT tags,tag_colors_json FROM requirements WHERE tenant_id=? AND project_id=? ORDER BY updated_at DESC,id DESC`, tenantID, a.pid())
	if err != nil {
		failRequirementResource(w, err)
		return
	}
	defer rows.Close()
	byName := map[string]*requirementTag{}
	for rows.Next() {
		var raw, colorsJSON string
		if err := rows.Scan(&raw, &colorsJSON); err != nil {
			failRequirementResource(w, err)
			return
		}
		colors := map[string]string{}
		if err := json.Unmarshal([]byte(colorsJSON), &colors); err != nil {
			failRequirementResource(w, err)
			return
		}
		seen := map[string]bool{}
		for _, part := range strings.FieldsFunc(raw, func(c rune) bool { return c == ',' || c == '，' || c == '\n' }) {
			name := strings.TrimSpace(part)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			item := byName[name]
			if item == nil {
				item = &requirementTag{Name: name}
				byName[name] = item
			}
			item.Count++
			if item.Color == "" && tagColorPattern.MatchString(colors[name]) {
				item.Color = strings.ToUpper(colors[name])
			}
		}
	}
	if err := rows.Err(); err != nil {
		failRequirementResource(w, err)
		return
	}
	items := []requirementTag{}
	for _, item := range byName {
		if item.Color == "" {
			item.Color = "#665FE8"
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	write(w, 200, map[string]any{"items": items})
}
