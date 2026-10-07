package billing

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

type alertRows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
}

type balanceAlertStore interface {
	Query(ctx context.Context, sql string, args ...any) (alertRows, error)
	Exec(ctx context.Context, sql string, args ...any) error
}

type pgxBalanceAlertStore struct {
	db *pgxpool.Pool
}

func (s pgxBalanceAlertStore) Query(ctx context.Context, sql string, args ...any) (alertRows, error) {
	return s.db.Query(ctx, sql, args...)
}

func (s pgxBalanceAlertStore) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := s.db.Exec(ctx, sql, args...)
	return err
}

// checkBalanceAlerts scans for teams with low or zero balance and takes action.
// - Low balance: insert audit_log entry for notification.
// - Zero balance: scale all compute services to min_instances=0, set suspended_at.
func checkBalanceAlerts(ctx context.Context, db *pgxpool.Pool, compute provider.Compute, logger *slog.Logger) {
	checkLowBalance(ctx, db, logger)
	checkZeroBalance(ctx, db, compute, logger)
}

func checkLowBalance(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) {
	rows, err := db.Query(ctx, `
		SELECT id, name, balance, low_balance_alert_at
		FROM teams
		WHERE balance > 0
		  AND balance <= low_balance_alert_at
		  AND suspended_at IS NULL
		  AND (low_balance_alerted_at IS NULL OR low_balance_alerted_at < NOW() - INTERVAL '24 hours')
	`)
	if err != nil {
		logger.Error("low balance query failed", "error", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var teamID, name string
		var balance, threshold float64
		if err := rows.Scan(&teamID, &name, &balance, &threshold); err != nil {
			continue
		}

		logger.Warn("low balance alert",
			"team_id", teamID,
			"team_name", name,
			"balance", balance,
			"threshold", threshold,
		)

		// Record audit log and update debounce timestamp
		_, _ = db.Exec(ctx, `
			INSERT INTO audit_logs (team_id, action, resource_type, details)
			VALUES ($1, 'low_balance_alert', 'billing', jsonb_build_object('balance', $2::text, 'threshold', $3::text))
		`, teamID, balance, threshold)
		_, _ = db.Exec(ctx, `UPDATE teams SET low_balance_alerted_at = NOW() WHERE id = $1`, teamID)
	}
}

func checkZeroBalance(ctx context.Context, db *pgxpool.Pool, compute provider.Compute, logger *slog.Logger) {
	checkZeroBalanceWithStore(ctx, pgxBalanceAlertStore{db: db}, compute, logger, suspendTeamComputeWithStore)
}

func checkZeroBalanceWithStore(ctx context.Context, store balanceAlertStore, compute provider.Compute, logger *slog.Logger, suspend func(context.Context, balanceAlertStore, provider.Compute, string, *slog.Logger) bool) {
	rows, err := store.Query(ctx, `
		SELECT id, name, balance
		FROM teams
		WHERE balance <= 0
		  AND suspended_at IS NULL
	`)
	if err != nil {
		logger.Error("zero balance query failed", "error", err)
		return
	}
	defer rows.Close()

	type team struct {
		id, name string
		balance  float64
	}
	var teams []team
	for rows.Next() {
		var t team
		if err := rows.Scan(&t.id, &t.name, &t.balance); err != nil {
			continue
		}
		teams = append(teams, t)
	}
	rows.Close()

	for _, t := range teams {
		logger.Warn("zero balance — suspending team",
			"team_id", t.id,
			"team_name", t.name,
			"balance", t.balance,
		)

		// Scale all compute services to zero. If any shutdown fails, skip marking
		// the team suspended so metering keeps running and doesn't under-bill.
		if compute == nil {
			logger.Error("CRITICAL: compute provider is nil, cannot suspend services — manual intervention required",
				"team_id", t.id)
			// Don't mark as suspended: services keep running → metering must continue.
			continue
		}
		if !suspend(ctx, store, compute, t.id, logger) {
			logger.Error("partial suspension failure — skipping suspended_at to keep metering active",
				"team_id", t.id)
			_ = store.Exec(ctx, `
				INSERT INTO audit_logs (team_id, action, resource_type, details)
				VALUES ($1, 'suspension_failed', 'billing', jsonb_build_object('reason', 'partial_shutdown_failure', 'balance', $2::text))
			`, t.id, t.balance)
			continue
		}

		// All services confirmed stopped — safe to mark suspended.
		_ = store.Exec(ctx, `
			UPDATE teams
			SET suspended_at = NOW(), suspension_reason = 'zero_balance'
			WHERE id = $1
		`, t.id)

		// Audit log
		_ = store.Exec(ctx, `
			INSERT INTO audit_logs (team_id, action, resource_type, details)
			VALUES ($1, 'team_suspended', 'billing', jsonb_build_object('reason', 'zero_balance', 'balance', $2::text))
		`, t.id, t.balance)
	}
}

// suspendTeamCompute scales all active compute services for a team to min_instances=0.
// Returns true only when every service was successfully stopped.
// On any failure it logs the error and returns false so the caller can skip
// marking the team suspended and keep metering active.
func suspendTeamCompute(ctx context.Context, db *pgxpool.Pool, compute provider.Compute, teamID string, logger *slog.Logger) bool {
	return suspendTeamComputeWithStore(ctx, pgxBalanceAlertStore{db: db}, compute, teamID, logger)
}

func suspendTeamComputeWithStore(ctx context.Context, store balanceAlertStore, compute provider.Compute, teamID string, logger *slog.Logger) bool {
	rows, err := store.Query(ctx, `
		SELECT rs.outputs::text
		FROM resource_states rs
		JOIN projects p ON p.id = rs.project_id
		WHERE p.team_id = $1
		  AND rs.resource_type = 'compute'
		  AND rs.status = 'active'
	`, teamID)
	if err != nil {
		logger.Error("query compute resources for suspension", "team_id", teamID, "error", err)
		return false
	}
	defer rows.Close()

	allStopped := true
	for rows.Next() {
		var outputsRaw string
		if err := rows.Scan(&outputsRaw); err != nil {
			allStopped = false
			continue
		}

		serviceName := provider.OutputStr(jsonToMap(outputsRaw), "service_name")
		region := provider.OutputStr(jsonToMap(outputsRaw), "region")
		if serviceName == "" {
			continue
		}

		// Scale to zero: Deploy with MinScale=0 effectively lets Cloud Run / Fargate
		// scale down completely when there's no traffic.
		_, err := compute.Deploy(ctx, provider.DeployOpts{
			ServiceName: serviceName,
			Region:      region,
			MinScale:    0,
		})
		if err != nil {
			logger.Error("scale to zero failed",
				"team_id", teamID,
				"service", serviceName,
				"error", err,
			)
			allStopped = false
		} else {
			logger.Info("service scaled to zero",
				"team_id", teamID,
				"service", serviceName,
			)
		}
	}
	return allStopped
}

func jsonToMap(raw string) map[string]any {
	var m map[string]any
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}
