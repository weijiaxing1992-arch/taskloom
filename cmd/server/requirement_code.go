package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Requirement identifiers are intentionally independent from the database
// primary key in the public API.  The primary key remains the stable route and
// relationship key; this six-digit serial is the concise, human-facing number
// shown in lists, details, exports and notifications.
const requirementCodeWidth = 6
const requirementCodeMaximum int64 = 999999

var errRequirementCodeExhausted = errors.New("需求编号已达到 6 位上限，无法继续创建需求")

func requirementSerialCode(id int64) (string, error) {
	if id < 1 || id > requirementCodeMaximum {
		return "", errRequirementCodeExhausted
	}
	return fmt.Sprintf("%0*d", requirementCodeWidth, id), nil
}

// requirementDisplayCode translates historic REQ-xxxx storage values without
// rewriting production records.  IDs have always been the canonical identity,
// so deriving the display serial from the ID also removes inconsistent old
// prefixes safely.  If a legacy database has already exceeded the six-digit
// allocation range, keep its stored value visible rather than silently showing
// the wrong requirement number; new writes are blocked before that can happen.
func requirementDisplayCode(id int64, stored string) string {
	if code, err := requirementSerialCode(id); err == nil {
		return code
	}
	return strings.TrimSpace(stored)
}

// requirementCodeQueryID accepts both the new numeric form and historic
// REQ-000123 input.  It is deliberately only a search convenience: API routes
// continue to accept stable numeric IDs, never display codes.
func requirementCodeQueryID(raw string) int64 {
	value := strings.TrimSpace(strings.ToUpper(raw))
	value = strings.TrimPrefix(value, "REQ-")
	if value == "" || len(value) > requirementCodeWidth {
		return 0
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 || id > requirementCodeMaximum {
		return 0
	}
	return id
}

func normalizeRequirementCode(x *Requirement) {
	if x != nil {
		x.Code = requirementDisplayCode(x.ID, x.Code)
	}
}

// normalizeReleaseNoteSourceCodes preserves a frozen release-note snapshot
// while making its legacy requirement labels obey the current public contract.
// The snapshot's IDs remain the source of truth, so this is safe to apply on
// every read and never needs a destructive migration of stored draft JSON.
func normalizeReleaseNoteSourceCodes(source *releaseNoteSource) {
	if source == nil {
		return
	}
	for index := range source.Requirements {
		item := &source.Requirements[index]
		item.Code = requirementDisplayCode(item.ID, item.Code)
	}
}

func requirementCodeMapID(row map[string]any, key string) (int64, bool) {
	value, ok := row[key]
	if !ok {
		return 0, false
	}
	switch number := value.(type) {
	case int64:
		return number, number > 0
	case int:
		return int64(number), number > 0
	case float64:
		return int64(number), number > 0 && number == float64(int64(number))
	case json.Number:
		id, err := number.Int64()
		return id, err == nil && id > 0
	case string:
		id, err := strconv.ParseInt(number, 10, 64)
		return id, err == nil && id > 0
	default:
		return 0, false
	}
}

// normalizeRequirementExportCodes keeps generated JSON/Markdown exports in the
// same display contract as the UI, including historic linked requirements and
// dependency summaries that are read with direct SQL projections.
func normalizeRequirementExportCodes(data map[string][]map[string]any) {
	normalizeRows := func(rows []map[string]any, idKey, codeKey string) {
		for _, row := range rows {
			if id, ok := requirementCodeMapID(row, idKey); ok {
				row[codeKey] = requirementDisplayCode(id, fmt.Sprint(row[codeKey]))
			}
		}
	}
	normalizeRows(data["requirements"], "id", "code")
	normalizeRows(data["relatedRequirementSummaries"], "id", "code")
	normalizeRows(data["dependencies"], "sourceRequirementId", "sourceCode")
	normalizeRows(data["dependencies"], "targetRequirementId", "targetCode")

	// Related-requirement history snapshots are old user-visible data. They
	// contain a stable requirement ID, so normalizing only that nested summary
	// preserves history while avoiding a stale REQ-* leak in an export.
	normalizeChange := func(change *requirementHistoryChange) {
		if change == nil || change.Field != "relatedRequirement" {
			return
		}
		for _, side := range []*any{&change.Before, &change.After} {
			value, _ := (*side).(map[string]any)
			if id, ok := requirementCodeMapID(value, "id"); ok {
				value["code"] = requirementDisplayCode(id, fmt.Sprint(value["code"]))
			}
		}
	}
	for _, activity := range data["requirementActivities"] {
		switch changes := activity["changes"].(type) {
		case []requirementHistoryChange:
			for index := range changes {
				normalizeChange(&changes[index])
			}
		case []any:
			for _, raw := range changes {
				change, _ := raw.(map[string]any)
				if change["field"] != "relatedRequirement" {
					continue
				}
				for _, side := range []string{"before", "after"} {
					value, _ := change[side].(map[string]any)
					if id, ok := requirementCodeMapID(value, "id"); ok {
						value["code"] = requirementDisplayCode(id, fmt.Sprint(value["code"]))
					}
				}
			}
		}
	}
}
