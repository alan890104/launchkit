package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiBillingHandler struct {
	db *pgxpool.Pool
}

type UsageLineItem struct {
	ResourceType string  `json:"resource_type"`
	TotalCost    float64 `json:"total_cost"`
	TotalQty     float64 `json:"total_qty"`
	Unit         string  `json:"unit"`
}

func (h *apiBillingHandler) summary(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var teamID string
	if err := h.db.QueryRow(r.Context(), `
		SELECT team_id
		FROM team_members
		WHERE user_id = $1
		LIMIT 1
	`, userID).Scan(&teamID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "team not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	periodEnd := periodStart.AddDate(0, 1, 0)

	resp := struct {
		Plan            string          `json:"plan"`
		Status          string          `json:"status"`
		CreditRemaining float64         `json:"credit_remaining"`
		MonthlyCredit   float64         `json:"monthly_credit"`
		PeriodStart     time.Time       `json:"period_start"`
		PeriodEnd       time.Time       `json:"period_end"`
		Usage           []UsageLineItem `json:"usage"`
		TotalSpend      float64         `json:"total_spend"`
	}{
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
		Usage:       []UsageLineItem{},
	}

	err := h.db.QueryRow(r.Context(), `
		SELECT plan, status, credit_remaining, monthly_credit, current_period_start, current_period_end
		FROM subscriptions
		WHERE team_id = $1
	`, teamID).Scan(
		&resp.Plan,
		&resp.Status,
		&resp.CreditRemaining,
		&resp.MonthlyCredit,
		&resp.PeriodStart,
		&resp.PeriodEnd,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT resource_type, SUM(cost) AS total_cost, SUM(quantity) AS total_qty, MIN(unit) AS unit
		FROM usage_records
		WHERE team_id = $1 AND recorded_at >= $2 AND recorded_at < $3
		GROUP BY resource_type
		ORDER BY total_cost DESC
	`, teamID, resp.PeriodStart, resp.PeriodEnd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var item UsageLineItem
		if err := rows.Scan(&item.ResourceType, &item.TotalCost, &item.TotalQty, &item.Unit); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		resp.Usage = append(resp.Usage, item)
		resp.TotalSpend += item.TotalCost
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
