package main

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
)

// Only database-backed sorts with identical read-model semantics are eligible.
// Calculated weights, hydrated people, custom fields and dependency state keep
// the complete-result query path. Code sorting has always meant numeric ID.
func requirementListSQLSort(r *http.Request, page requirementPage, query *workItemQuery) (orderBy, unsupported string, ok bool) {
	if !page.enabled || len(query.Filters) != 0 || (query.Order != "asc" && query.Order != "desc") {
		return "", "", false
	}
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "cf.") && len(values) > 0 {
			return "", "", false
		}
	}
	column := ""
	switch query.Sort {
	case "code":
		// Canonical IDs are non-null integers, so preserve the usable ID index.
		// Extremely large legacy IDs need the old JSON float64 comparator.
		return "requirements.id " + query.Order, "CASE WHEN requirements.id>9007199254740991 OR requirements.id< -9007199254740991 THEN 1 ELSE 0 END", true
	case "parentId":
		column = "parent_id"
	case "progress":
		column = "progress"
	case "estimatedHours":
		column = "estimated_hours"
	case "actualHours":
		column = "actual_hours"
	case "iterationDelayCount":
		column = "iteration_delay_count"
	case "sensitive":
		column = "sensitive"
	case "authImpact":
		column = "auth_impact"
	case "startDate":
		column = "start_date"
	case "endDate":
		column = "end_date"
	case "createdAt":
		column = "created_at"
	case "updatedAt":
		column = "updated_at"
	case "priority":
		column = "priority"
	default:
		return "", "", false
	}
	column = "requirements." + column
	empty := column + " IS NULL"
	value := "CAST(" + column + " AS REAL)"
	unsupported = "0"
	if query.Fields[query.Sort] == "date" || query.Sort == "priority" {
		empty = "(" + column + " IS NULL OR " + column + "='')"
		value = "lower(" + column + ")"
		// SQLite lower() folds ASCII; Go's existing comparator folds Unicode.
		// Dates/priority normally contain ASCII, but legacy non-ASCII values must
		// fall back instead of silently changing ordering. COUNT checks only the
		// sort column, never loads rich documents or associated field values.
		unsupported = "CASE WHEN length(CAST(" + column + " AS BLOB))<>length(" + column + ") THEN 1 ELSE 0 END"
	}
	return empty + " ASC," + value + " " + query.Order + ",requirements.id ASC", unsupported, true
}

type requirementSQLPage struct {
	page  requirementPage
	total int
	items []Requirement
}

// scopeSQL is the existing, parameterized FROM/WHERE expression from the list
// handler, including its tenant/project and permission-related predicates.
// Count and page rows share a read snapshot so a concurrent insertion/deletion
// cannot produce inconsistent totals or an incorrectly clamped final page.
func (a *App) readRequirementSQLPage(ctx context.Context, r *http.Request, page requirementPage, query *workItemQuery, scopeSQL string, args []any) (*requirementSQLPage, error) {
	orderBy, unsupported, ok := requirementListSQLSort(r, page, query)
	if !ok {
		return nil, nil
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result := &requirementSQLPage{page: page, items: []Requirement{}}
	var needsFallback int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MAX("+unsupported+"),0)"+scopeSQL, args...).Scan(&result.total, &needsFallback); err != nil {
		return nil, err
	}
	if needsFallback != 0 {
		return nil, nil
	}
	result.page.number = min(page.number, max(1, (result.total+page.size-1)/page.size))
	if result.total > 0 {
		pageArgs := append(append([]any{}, args...), page.size, (result.page.number-1)*page.size)
		rows, err := tx.QueryContext(ctx, "SELECT "+page.selectColumns()+scopeSQL+" ORDER BY "+orderBy+" LIMIT ? OFFSET ?", pageArgs...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item Requirement
			if err := scanRequirement(rows, &item); err != nil {
				rows.Close()
				return nil, err
			}
			result.items = append(result.items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *requirementSQLPage) response(items []Requirement) map[string]any {
	// Keep the existing projection contract, but do not paginate an already
	// limited slice again. Parent IDs and unpaged export/reference APIs retain
	// their existing behavior.
	projection := p.page
	projection.enabled = false
	response := projection.response(items)
	response["total"], response["page"], response["pageSize"] = p.total, p.page.number, p.page.size
	return response
}
