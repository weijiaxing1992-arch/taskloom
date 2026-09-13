package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
)

// Weights are dimensionless manual estimates, not estimated or actual hours.
type SprintWeightItem struct {
	ID          int64               `json:"id"`
	Code        string              `json:"code"`
	Title       string              `json:"title"`
	Status      string              `json:"status"`
	ParentID    *int64              `json:"parentId"`
	Weights     map[string]*float64 `json:"weights"`
	TotalWeight float64             `json:"totalWeight"`
	Estimated   bool                `json:"estimated"`
}

type SprintWeightSummary struct {
	TotalWeight         float64            `json:"totalWeight"`
	RequirementCount    int                `json:"requirementCount"`
	EstimatedCount      int                `json:"estimatedCount"`
	UnestimatedCount    int                `json:"unestimatedCount"`
	RoleTotals          map[string]float64 `json:"roleTotals"`
	RoleEstimatedCounts map[string]int     `json:"roleEstimatedCounts"`
	Items               []SprintWeightItem `json:"items"`
	Precision           int                `json:"precision"`
}

// Round only after decimal accumulation. This prevents binary floating-point
// drift when many fractional weights (e.g. 0.1) are summed across a large pool.
func roundedSprintWeight(value *big.Rat) float64 {
	scaled := new(big.Rat).Mul(value, big.NewRat(1_000_000, 1))
	scaled.Add(scaled, big.NewRat(1, 2))
	units := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	result, _ := new(big.Rat).SetFrac(units, big.NewInt(1_000_000)).Float64()
	return result
}

func (a *App) sprintWeightSummary(ctx context.Context, name string) (SprintWeightSummary, error) {
	out := SprintWeightSummary{RoleTotals: map[string]float64{}, RoleEstimatedCounts: map[string]int{}, Items: []SprintWeightItem{}, Precision: 6}
	roleSums := map[string]*big.Rat{}
	for _, role := range requirementWeightRoles {
		out.RoleTotals[role] = 0
		out.RoleEstimatedCounts[role] = 0
		roleSums[role] = new(big.Rat)
	}
	a1, a2 := "待规划", ""
	if name != "" && name != "待规划" {
		a1, a2 = a.scopedSprintAliases(name)
	}
	// No LIMIT, status filter, joins or hierarchy expansion: each requirement ID
	// contributes its own manual fields once, including completed requirements.
	rows, err := a.db.QueryContext(ctx, `SELECT id,code,title,status,parent_id,role_weights_json FROM requirements WHERE tenant_id=? AND project_id=? AND sprint IN (?,?) ORDER BY id`, tenantID, a.pid(), a1, a2)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	grandTotal := new(big.Rat)
	for rows.Next() {
		var item SprintWeightItem
		var parent sql.NullInt64
		var raw string
		if err := rows.Scan(&item.ID, &item.Code, &item.Title, &item.Status, &parent, &raw); err != nil {
			return out, err
		}
		item.Code = requirementDisplayCode(item.ID, item.Code)
		if parent.Valid {
			id := parent.Int64
			item.ParentID = &id
		}
		var weights map[string]struct {
			Value *json.Number `json:"value"`
		}
		if err := json.Unmarshal([]byte(raw), &weights); err != nil {
			return out, fmt.Errorf("invalid requirement weight data: %w", err)
		}
		for role := range weights {
			if !validChoice(role, requirementWeightRoles) {
				return out, fmt.Errorf("unknown requirement weight dimension")
			}
		}
		item.Weights = map[string]*float64{}
		itemTotal := new(big.Rat)
		for _, role := range requirementWeightRoles {
			item.Weights[role] = nil
			if weights[role].Value == nil {
				continue
			}
			value, ok := new(big.Rat).SetString(weights[role].Value.String())
			if !ok || value.Sign() < 0 || value.Cmp(big.NewRat(1_000_000, 1)) > 0 {
				return out, fmt.Errorf("invalid requirement weight value")
			}
			asNumber, _ := value.Float64()
			item.Weights[role] = &asNumber
			item.Estimated = true // Explicit zero is an estimate; null is not.
			out.RoleEstimatedCounts[role]++
			roleSums[role].Add(roleSums[role], value)
			itemTotal.Add(itemTotal, value)
		}
		item.TotalWeight = roundedSprintWeight(itemTotal)
		grandTotal.Add(grandTotal, itemTotal)
		out.RequirementCount++
		if item.Estimated {
			out.EstimatedCount++
		} else {
			out.UnestimatedCount++
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, role := range requirementWeightRoles {
		out.RoleTotals[role] = roundedSprintWeight(roleSums[role])
	}
	out.TotalWeight = roundedSprintWeight(grandTotal)
	return out, nil
}

func (a *App) backlogWeights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "不支持的方法")
		return
	}
	summary, err := a.sprintWeightSummary(r.Context(), "待规划")
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "database_unavailable", "迭代统计暂时无法读取，请稍后重试")
		return
	}
	write(w, http.StatusOK, map[string]any{"weightSummary": summary})
}
