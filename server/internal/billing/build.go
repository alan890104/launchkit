package billing

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CalculateBuildCosts processes unbilled build_usage records for the current month.
// Applies plan-aware free tier, then charges $0.005/sec for overages.
// Called at the end of each metering cycle.
func CalculateBuildCosts(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) float64 {
	// Find teams with unbilled build usage
	rows, err := db.Query(ctx, `
		SELECT team_id, SUM(duration_ms) as total_ms
		FROM build_usage
		WHERE billed_at IS NULL
		  AND period_month = date_trunc('month', NOW())::DATE
		GROUP BY team_id
	`)
	if err != nil {
		logger.Error("query unbilled build usage", "error", err)
		return 0
	}
	defer rows.Close()

	type teamBuild struct {
		teamID  string
		totalMs int64
	}
	var teams []teamBuild
	for rows.Next() {
		var t teamBuild
		if err := rows.Scan(&t.teamID, &t.totalMs); err != nil {
			continue
		}
		teams = append(teams, t)
	}
	// rows.Close() is handled by defer — don't call twice

	var totalCost float64

	for _, t := range teams {
		plan := getTeamPlan(ctx, db, t.teamID)
		limits := PlanConfig[plan]
		freeMs := int64(limits.BuildFreeMinutes) * 60 * 1000

		// Get total build time already billed this month
		var billedMs int64
		_ = db.QueryRow(ctx, `
			SELECT COALESCE(SUM(duration_ms), 0)
			FROM build_usage
			WHERE team_id = $1
			  AND period_month = date_trunc('month', NOW())::DATE
			  AND billed_at IS NOT NULL
		`, t.teamID).Scan(&billedMs)

		// Calculate billable overage
		totalUsedMs := billedMs + t.totalMs
		var billableMs int64
		var cost float64

		if totalUsedMs <= freeMs {
			// Still within free tier — mark as billed with $0 cost
			cost = 0
		} else if billedMs >= freeMs {
			// Already past free tier — all new builds are billable
			billableMs = t.totalMs
			cost = float64(billableMs) / 1000.0 * BuildRatePerSecond
		} else {
			// Partially in free tier
			billableMs = totalUsedMs - freeMs
			cost = float64(billableMs) / 1000.0 * BuildRatePerSecond
		}

		// Wrap mark-billed + insert-usage-record + deduct-credit in one transaction.
		// Without this, a crash between steps leaves inconsistent billing state.
		tx, txErr := db.Begin(ctx)
		if txErr != nil {
			logger.Error("build billing tx begin", "team_id", t.teamID, "error", txErr)
			continue
		}

		// Mark all unbilled records as billed (with prorated cost)
		_, markErr := tx.Exec(ctx, `
			UPDATE build_usage
			SET billed_at = NOW(),
			    cost = CASE
			        WHEN $2 > 0 THEN $2 * (duration_ms::numeric / NULLIF($3::numeric, 0))
			        ELSE 0
			    END
			WHERE team_id = $1
			  AND period_month = date_trunc('month', NOW())::DATE
			  AND billed_at IS NULL
		`, t.teamID, cost, t.totalMs)
		if markErr != nil {
			tx.Rollback(ctx)
			logger.Error("mark build billed", "team_id", t.teamID, "error", markErr)
			continue
		}

		if cost > 0 {
			// Build cost URN for dedup: one record per team per month
			buildURN := fmt.Sprintf("buildkit:%s:%s",
				t.teamID,
				"current-month", // dedup key — only one build cost record per team per metering cycle
			)
			periodStart := periodStartOfMonth(ctx, db)
			periodEnd := periodEndOfMonth(ctx, db)

			// Insert usage record with real project_id from the build_usage data
			_, insertErr := tx.Exec(ctx, `
				INSERT INTO usage_records (
					team_id, project_id, resource_type, resource_id, quantity, unit, cost,
					recorded_at, period_start, period_end, provider, resource_urn
				)
				SELECT $1, sub.project_id, 'build', '', $3, 'build-seconds', $4,
				       NOW(), $5, $6, 'buildkit', $7
				FROM (SELECT project_id FROM build_usage
				      WHERE team_id = $1 AND period_month = date_trunc('month', NOW())::DATE
				        AND billed_at IS NOT NULL AND project_id IS NOT NULL
				      LIMIT 1) sub
				ON CONFLICT (resource_urn, period_start, period_end) WHERE resource_urn != ''
				DO UPDATE SET cost = usage_records.cost + EXCLUDED.cost,
				             quantity = usage_records.quantity + EXCLUDED.quantity,
				             recorded_at = NOW()
			`, t.teamID, nil, float64(billableMs)/1000.0, cost,
				periodStart, periodEnd, buildURN)
			if insertErr != nil {
				tx.Rollback(ctx)
				logger.Error("insert build usage record", "team_id", t.teamID, "error", insertErr)
				continue
			}

			// Deduct from credit/balance within the same transaction
			DeductFromCreditInTx(ctx, tx, t.teamID, cost, logger)
		}

		if commitErr := tx.Commit(ctx); commitErr != nil {
			logger.Error("build billing tx commit", "team_id", t.teamID, "error", commitErr)
			continue
		}

		if cost > 0 {
			totalCost += cost
			logger.Info("build time billed",
				"team_id", t.teamID,
				"new_ms", t.totalMs,
				"billable_ms", billableMs,
				"cost", cost,
			)
		}
	}

	return totalCost
}

// periodStartOfMonth returns the start of the current month.
func periodStartOfMonth(ctx context.Context, db *pgxpool.Pool) interface{} {
	var t interface{}
	_ = db.QueryRow(ctx, "SELECT date_trunc('month', NOW())").Scan(&t)
	return t
}

// periodEndOfMonth returns the start of the next month.
func periodEndOfMonth(ctx context.Context, db *pgxpool.Pool) interface{} {
	var t interface{}
	_ = db.QueryRow(ctx, "SELECT date_trunc('month', NOW()) + INTERVAL '1 month'").Scan(&t)
	return t
}
