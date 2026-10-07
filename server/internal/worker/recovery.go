package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// RecoverStaleDeployments runs on API server startup.
// Scans all deployments stuck in non-terminal state for >5 minutes,
// reads deployment_steps to determine progress, and re-enqueues or fails them.
//
// Inspired by SkyPilot reset_jobs_for_recovery() and _recover_replica_operations().
// Prerequisite: all provision/build operations must be idempotent.
func RecoverStaleDeployments(ctx context.Context, db *pgxpool.Pool, riverClient *river.Client[pgx.Tx]) error {
	logger := slog.With("job", "crash_recovery")

	staleThreshold := time.Now().Add(-5 * time.Minute)
	secretsThreshold := time.Now().Add(-1 * time.Hour)

	rows, err := db.Query(ctx, `
		SELECT id, status, plan_id
		FROM deployments
		WHERE (
			(status NOT IN ('live', 'failed', 'stopped', 'waiting_secrets') AND updated_at < $1)
			OR (status = 'waiting_secrets' AND updated_at < $2)
		)
	`, staleThreshold, secretsThreshold)
	if err != nil {
		return err
	}
	defer rows.Close()

	type staleDeployment struct {
		ID     string
		Status string
		PlanID *string
	}

	var stale []staleDeployment
	for rows.Next() {
		var d staleDeployment
		if err := rows.Scan(&d.ID, &d.Status, &d.PlanID); err != nil {
			return err
		}
		stale = append(stale, d)
	}

	if len(stale) == 0 {
		return nil
	}
	logger.Info("found stale deployments", "count", len(stale))

	for _, dep := range stale {
		// Read deployment_steps to assess progress
		stepRows, err := db.Query(ctx,
			"SELECT step, status FROM deployment_steps WHERE deployment_id = $1",
			dep.ID,
		)
		if err != nil {
			logger.Error("failed to read steps", "deployment_id", dep.ID, "error", err)
			continue
		}

		hasFailure := false
		completedCount := 0
		for stepRows.Next() {
			var step, status string
			if scanErr := stepRows.Scan(&step, &status); scanErr != nil {
				logger.Error("failed to scan step row", "deployment_id", dep.ID, "error", scanErr)
				continue
			}
			if status == "failed" {
				hasFailure = true
			}
			if status == "completed" {
				completedCount++
			}
		}
		stepRows.Close()

		if hasFailure {
			// Steps already failed → mark entire deployment as failed
			logger.Warn("stale deployment has failed steps, marking failed",
				"deployment_id", dep.ID)
			if _, execErr := db.Exec(ctx,
				"UPDATE deployments SET status = 'failed', error = 'crash recovery: step failure detected', updated_at = NOW(), finished_at = NOW() WHERE id = $1",
				dep.ID,
			); execErr != nil {
				logger.Error("failed to mark deployment as failed", "deployment_id", dep.ID, "error", execErr)
			}
			continue
		}

		// Re-enqueue: the orchestrator uses isStepCompleted() to skip completed steps
		if dep.PlanID != nil {
			logger.Info("re-enqueuing stale deployment",
				"deployment_id", dep.ID,
				"completed_steps", completedCount,
			)
			_, err := riverClient.Insert(ctx, &DeployJobArgs{
				DeploymentID: dep.ID,
				PlanID:       *dep.PlanID,
				IsRecovery:   true,
			}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{ByArgs: true},
			})
			if err != nil {
				logger.Error("failed to re-enqueue deployment",
					"deployment_id", dep.ID, "error", err)
			}
		} else {
			// No plan reference — can't re-enqueue, mark as failed
			logger.Warn("stale deployment has no plan_id, marking failed",
				"deployment_id", dep.ID)
			if _, execErr := db.Exec(ctx,
				"UPDATE deployments SET status = 'failed', error = 'crash recovery: no plan reference for re-enqueue', updated_at = NOW(), finished_at = NOW() WHERE id = $1",
				dep.ID,
			); execErr != nil {
				logger.Error("failed to mark deployment as failed", "deployment_id", dep.ID, "error", execErr)
			}
		}
	}

	return nil
}

// DeployJobArgs is the payload for re-enqueuing a deployment via river.
// Used by crash recovery and potentially by the MCP deploy_project tool.
type DeployJobArgs struct {
	DeploymentID string `json:"deployment_id"`
	PlanID       string `json:"plan_id"`
	IsRecovery   bool   `json:"is_recovery"`
}

func (DeployJobArgs) Kind() string { return "deploy" }
