package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MeteringConfig holds all dependencies for a metering cycle.
type MeteringConfig struct {
	DB       *pgxpool.Pool
	Fetchers []provider.ConsumptionFetcher
	Pricing  *PricingTable
	Compute  provider.Compute // needed for zero-balance suspension (scale to zero)
}

// RunMeteringCycle executes one hourly metering cycle.
func RunMeteringCycle(ctx context.Context, cfg MeteringConfig, periodStart, periodEnd time.Time) error {
	logger := slog.With("job", "metering",
		"period_start", periodStart.Format(time.RFC3339),
		"period_end", periodEnd.Format(time.RFC3339),
	)

	// Step 1: Create metering run (dedup by UNIQUE constraint on period).
	// This MUST happen before any side effects so duplicate runs are blocked.
	runID, err := createMeteringRun(ctx, cfg.DB, periodStart, periodEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		logger.Info("metering run already exists for this period, skipping")
		return nil // idempotent — UNIQUE conflict, not a real error
	}
	if err != nil {
		return fmt.Errorf("create metering run: %w", err)
	}
	logger = logger.With("run_id", runID)
	logger.Info("metering cycle started")

	// Step 1.5: Reset subscription credits for expired periods.
	// Runs AFTER dedup check so multi-replica races can't double-reset.
	if err := resetExpiredCredits(ctx, cfg.DB, logger); err != nil {
		finalizeMeteringRun(ctx, cfg.DB, runID, "failed", 0, 0, err)
		return fmt.Errorf("reset expired credits: %w", err)
	}

	// Step 2: Load active resources
	resources, err := loadActiveResources(ctx, cfg.DB)
	if err != nil {
		finalizeMeteringRun(ctx, cfg.DB, runID, "failed", 0, 0, err)
		return fmt.Errorf("load active resources: %w", err)
	}
	if len(resources) == 0 {
		finalizeMeteringRun(ctx, cfg.DB, runID, "completed", 0, 0, nil)
		logger.Info("no active resources, skipping")
		return nil
	}

	// Step 3: Group by provider
	byProvider := groupByProvider(resources)

	// Step 4-8: Fetch, price, store for each provider
	providerResults := make(map[string]string)
	var totalRecords int
	var totalCost float64
	anyFailed := false

	for _, fetcher := range cfg.Fetchers {
		pName := fetcher.ProviderName()
		providerResources, ok := byProvider[pName]
		if !ok || len(providerResources) == 0 {
			providerResults[pName] = "no_resources"
			continue
		}

		// Filter out resources already metered past periodEnd
		filtered := filterByWatermark(ctx, cfg.DB, providerResources, pName, periodEnd)
		if len(filtered) == 0 {
			providerResults[pName] = "already_metered"
			continue
		}

		records, fetchErr := fetcher.FetchConsumption(ctx, filtered, periodStart, periodEnd)
		if fetchErr != nil {
			logger.Error("provider fetch failed", "provider", pName, "error", fetchErr)
			providerResults[pName] = fmt.Sprintf("failed: %s", fetchErr)
			anyFailed = true
			continue // graceful degradation
		}

		for _, record := range records {
			// Resolve team_id for this project
			teamID, resolveErr := teamIDForProject(ctx, cfg.DB, record.ProjectID)
			if resolveErr != nil {
				logger.Error("resolve team for project", "project_id", record.ProjectID, "error", resolveErr)
				continue
			}

			// Calculate compute cost (excludes egress — egress is handled atomically
			// inside recordAndDeduct to prevent the quota ledger and financial ledger
			// from diverging on partial failure).
			computeCost := cfg.Pricing.CalculateCost(record)
			egressBytes := record.Metrics["network_egress_bytes"]

			if computeCost <= 0 && egressBytes <= 0 {
				continue
			}

			// Insert usage_record + deduct credit/balance (including egress) in one transaction.
			inserted, charged, txErr := recordAndDeduct(ctx, cfg.DB, record, computeCost, egressBytes, teamID, runID, logger)
			if txErr != nil {
				logger.Error("record and deduct failed", "urn", record.ResourceURN, "error", txErr)
				continue
			}
			if !inserted {
				continue // already existed (dedup)
			}

			totalRecords++
			totalCost += charged
		}

		// Update checkpoints for this provider
		updateCheckpoints(ctx, cfg.DB, pName, records, periodEnd)
		providerResults[pName] = "ok"
		logger.Info("provider metered", "provider", pName, "records", len(records))
	}

	// Step 9a: Calculate build time costs
	buildCost := CalculateBuildCosts(ctx, cfg.DB, logger)
	totalCost += buildCost

	// Step 9b: Check low balance / suspension
	checkBalanceAlerts(ctx, cfg.DB, cfg.Compute, logger)

	// Step 10: Finalize run
	status := "completed"
	if anyFailed {
		status = "partial"
	}
	finalizeMeteringRun(ctx, cfg.DB, runID, status, totalRecords, totalCost, nil)

	logger.Info("metering cycle complete",
		"status", status,
		"records", totalRecords,
		"total_cost_usd", fmt.Sprintf("%.6f", totalCost),
	)
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// resetExpiredCredits advances subscription periods and resets monthly credits.
// Called at the start of each metering cycle. Idempotent — only updates subscriptions
// whose current_period_end has passed.
func resetExpiredCredits(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) error {
	// Use date_trunc to always align period boundaries to month starts,
	// preventing drift when adding INTERVAL '1 month' across months with different lengths.
	tag, err := db.Exec(ctx, `
		UPDATE subscriptions
		SET credit_remaining = monthly_credit,
		    current_period_start = date_trunc('month', NOW()),
		    current_period_end = date_trunc('month', NOW()) + INTERVAL '1 month',
		    updated_at = NOW()
		WHERE status = 'active'
		  AND current_period_end <= NOW()
	`)
	if err != nil {
		return fmt.Errorf("reset expired credits: %w", err)
	}
	if tag.RowsAffected() > 0 {
		logger.Info("subscription credits reset", "count", tag.RowsAffected())
	}
	return nil
}

func createMeteringRun(ctx context.Context, db *pgxpool.Pool, start, end time.Time) (string, error) {
	var id string
	err := db.QueryRow(ctx, `
		INSERT INTO metering_runs (period_start, period_end)
		VALUES ($1, $2)
		ON CONFLICT (period_start, period_end) DO NOTHING
		RETURNING id
	`, start, end).Scan(&id)
	// pgx.ErrNoRows means ON CONFLICT DO NOTHING fired (duplicate period).
	// All other errors are real DB failures and must be propagated.
	return id, err
}

func finalizeMeteringRun(ctx context.Context, db *pgxpool.Pool, runID, status string, records int, cost float64, runErr error) {
	var errMsg *string
	if runErr != nil {
		s := runErr.Error()
		errMsg = &s
	}
	_, err := db.Exec(ctx, `
		UPDATE metering_runs
		SET status = $1, records_count = $2, total_cost = $3, finished_at = NOW(), error = $4
		WHERE id = $5
	`, status, records, cost, errMsg, runID)
	if err != nil {
		slog.Error("finalize metering run failed", "run_id", runID, "error", err)
	}
}

func loadActiveResources(ctx context.Context, db *pgxpool.Pool) ([]provider.ActiveResource, error) {
	rows, err := db.Query(ctx, `
		SELECT urn, project_id, resource_type, provider, outputs::text
		FROM resource_states
		WHERE status IN ('active', 'deleting')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []provider.ActiveResource
	for rows.Next() {
		var r provider.ActiveResource
		var outputsRaw string
		if err := rows.Scan(&r.URN, &r.ProjectID, &r.ResourceType, &r.Provider, &outputsRaw); err != nil {
			return nil, fmt.Errorf("scan resource_states: %w", err)
		}
		if err := json.Unmarshal([]byte(outputsRaw), &r.Outputs); err != nil {
			slog.Warn("corrupt resource_states outputs, skipping",
				"urn", r.URN, "error", err)
			continue
		}
		resources = append(resources, r)
	}
	return resources, rows.Err()
}

func groupByProvider(resources []provider.ActiveResource) map[string][]provider.ActiveResource {
	m := make(map[string][]provider.ActiveResource)
	for _, r := range resources {
		m[r.Provider] = append(m[r.Provider], r)
	}
	return m
}

func filterByWatermark(ctx context.Context, db *pgxpool.Pool, resources []provider.ActiveResource, providerName string, periodEnd time.Time) []provider.ActiveResource {
	// Build URN list
	urns := make([]string, len(resources))
	for i, r := range resources {
		urns[i] = r.URN
	}

	// Query existing checkpoints
	rows, err := db.Query(ctx, `
		SELECT resource_urn, period_end
		FROM metering_checkpoints
		WHERE resource_urn = ANY($1) AND provider = $2
	`, urns, providerName)
	if err != nil {
		// On error, skip filtering — process all
		return resources
	}
	defer rows.Close()

	watermarks := make(map[string]time.Time)
	for rows.Next() {
		var urn string
		var pe time.Time
		if err := rows.Scan(&urn, &pe); err != nil {
			continue
		}
		watermarks[urn] = pe
	}

	// Keep resources whose watermark is before periodEnd
	var filtered []provider.ActiveResource
	for _, r := range resources {
		if wm, ok := watermarks[r.URN]; ok && !wm.Before(periodEnd) {
			continue // already metered past this period
		}
		filtered = append(filtered, r)
	}
	return filtered
}

// recordAndDeduct wraps insertUsageRecord + egress accounting + deductFromCredit in a
// single transaction. Returns (inserted, totalCost, error).
// inserted=false and err=nil means the record already existed (idempotent dedup).
// egressBytes is the raw network egress for this record; the egress free-tier
// and quota are evaluated inside the same tx so they are atomic with the usage insert.
func recordAndDeduct(ctx context.Context, db *pgxpool.Pool, record provider.ConsumptionRecord, computeCost, egressBytes float64, teamID, runID string, logger *slog.Logger) (bool, float64, error) {
	// Fetch the team's plan before opening the tx (read-only; plan rarely changes).
	plan := getTeamPlan(ctx, db, teamID)
	limits := PlanConfig[plan]

	tx, err := db.Begin(ctx)
	if err != nil {
		return false, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Compute egress cost inside the tx so egress_usage and usage_records commit atomically.
	// EgressCostInTx always advances egress_usage (free-tier quota tracking) even when the
	// returned cost is $0. We must commit the tx if egressBytes > 0 so the quota counter
	// advances regardless of whether a billable charge is produced.
	egressCost := EgressCostInTx(ctx, tx, teamID, record.ProjectID, egressBytes, limits)
	totalCost := computeCost + egressCost

	if totalCost <= 0 {
		// No charge, but egress quota tracking still needs to commit if there was egress.
		if egressBytes > 0 {
			if err := tx.Commit(ctx); err != nil {
				logger.Error("commit egress quota tx failed", "team_id", teamID, "error", err)
			}
		}
		return false, 0, nil
	}

	// Insert usage record
	quantity, unit := primaryMetric(record)
	tag, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			team_id, project_id, resource_type, resource_id, quantity, unit, cost,
			recorded_at, period_start, period_end, provider, resource_urn, metering_run_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), $8, $9, $10, $11, $12)
		ON CONFLICT (resource_urn, period_start, period_end) WHERE resource_urn != '' DO NOTHING
	`, teamID, record.ProjectID, record.ResourceType, record.ProviderID,
		quantity, unit, totalCost,
		record.PeriodStart, record.PeriodEnd, record.Provider, record.ResourceURN, runID,
	)
	if err != nil {
		return false, 0, fmt.Errorf("insert usage record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Dedup: record already exists. Still commit the egress quota advancement.
		if egressBytes > 0 {
			if err := tx.Commit(ctx); err != nil {
				logger.Error("commit egress quota tx (dedup) failed", "team_id", teamID, "error", err)
			}
		}
		return false, 0, nil
	}

	// Deduct from credit/balance within same transaction
	deductFromCreditTx(ctx, tx, teamID, totalCost, logger)

	if err := tx.Commit(ctx); err != nil {
		return false, 0, fmt.Errorf("commit tx: %w", err)
	}
	return true, totalCost, nil
}

// deductFromCreditTx is the transaction-aware version of deductFromCredit.
// Uses SELECT FOR UPDATE to serialize concurrent credit deductions for the same team.
func deductFromCreditTx(ctx context.Context, tx pgx.Tx, teamID string, amount float64, logger *slog.Logger) {
	if amount <= 0 {
		return
	}

	// Lock the subscription row and read credit BEFORE update.
	// FOR UPDATE ensures concurrent metering goroutines serialize here.
	var oldCredit float64
	err := tx.QueryRow(ctx, `
		SELECT credit_remaining
		FROM subscriptions
		WHERE team_id = $1 AND status = 'active'
		FOR UPDATE
	`, teamID).Scan(&oldCredit)

	if err != nil {
		// No active subscription — deduct from balance directly
		_, balErr := tx.Exec(ctx, "UPDATE teams SET balance = balance - $1 WHERE id = $2", amount, teamID)
		if balErr != nil {
			logger.Error("deduct balance (tx) failed", "team_id", teamID, "error", balErr)
		}
		return
	}

	// Deduct from credit
	newCredit := oldCredit - amount
	if newCredit < 0 {
		newCredit = 0
	}
	_, err = tx.Exec(ctx, `
		UPDATE subscriptions
		SET credit_remaining = $1, updated_at = NOW()
		WHERE team_id = $2 AND status = 'active'
	`, newCredit, teamID)
	if err != nil {
		logger.Error("deduct credit (tx) failed", "team_id", teamID, "error", err)
		return
	}

	if oldCredit >= amount {
		return // credit covered everything
	}

	// Credit didn't fully cover — deduct the overflow from balance
	overflow := amount - oldCredit
	_, balErr := tx.Exec(ctx, "UPDATE teams SET balance = balance - $1 WHERE id = $2", overflow, teamID)
	if balErr != nil {
		logger.Error("deduct overflow (tx) failed", "team_id", teamID, "error", balErr)
	}
}

func teamIDForProject(ctx context.Context, db *pgxpool.Pool, projectID string) (string, error) {
	var teamID string
	err := db.QueryRow(ctx, "SELECT team_id FROM projects WHERE id = $1", projectID).Scan(&teamID)
	return teamID, err
}

func primaryMetric(record provider.ConsumptionRecord) (float64, string) {
	switch record.Provider {
	case "cloud_run":
		return record.Metrics["cpu_seconds"], "cpu-seconds"
	case "ecs_fargate":
		return record.Metrics["vcpu_hours"], "vcpu-hours"
	case "neon":
		return record.Metrics["compute_unit_seconds"], "cu-seconds"
	case "upstash":
		if record.DirectCost != nil {
			return *record.DirectCost, "usd"
		}
		return record.Metrics["monthly_requests"], "requests"
	case "gcs", "s3":
		return record.Metrics["storage_bytes"], "bytes"
	case "artifact_registry":
		return record.Metrics["storage_bytes"], "bytes"
	default:
		return 0, "unknown"
	}
}

// DeductFromCreditInTx deducts cost from subscription credit first, overflow goes to team balance.
// Must be called within a transaction. Exported for use by build billing.
func DeductFromCreditInTx(ctx context.Context, tx pgx.Tx, teamID string, amount float64, logger *slog.Logger) {
	deductFromCreditTx(ctx, tx, teamID, amount, logger)
}

func updateCheckpoints(ctx context.Context, db *pgxpool.Pool, providerName string, records []provider.ConsumptionRecord, periodEnd time.Time) {
	for _, r := range records {
		cumulativeJSON, _ := json.Marshal(r.Metrics)
		_, err := db.Exec(ctx, `
			INSERT INTO metering_checkpoints (resource_urn, provider, period_end, cumulative, updated_at)
			VALUES ($1, $2, $3, $4, NOW())
			ON CONFLICT (resource_urn, provider) DO UPDATE SET
				period_end = GREATEST(metering_checkpoints.period_end, EXCLUDED.period_end),
				cumulative = EXCLUDED.cumulative,
				updated_at = NOW()
		`, r.ResourceURN, providerName, periodEnd, cumulativeJSON)
		if err != nil {
			slog.Error("update checkpoint failed", "urn", r.ResourceURN, "error", err)
		}
	}
}

// GetCheckpointCumulative retrieves the last cumulative metrics for a resource+provider.
// Used by fetchers that need delta calculation (e.g. Upstash).
func GetCheckpointCumulative(ctx context.Context, db *pgxpool.Pool, resourceURN, providerName string) (map[string]float64, error) {
	var raw string
	err := db.QueryRow(ctx, `
		SELECT cumulative::text FROM metering_checkpoints
		WHERE resource_urn = $1 AND provider = $2
	`, resourceURN, providerName).Scan(&raw)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil // no prior checkpoint
		}
		return nil, err
	}
	var m map[string]float64
	json.Unmarshal([]byte(raw), &m)
	return m, nil
}
