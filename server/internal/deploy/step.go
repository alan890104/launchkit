package deploy

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// markStep upserts a deployment step's status.
// First call inserts; retries update (idempotent).
func markStep(ctx context.Context, db *pgxpool.Pool, deploymentID, step, status string, stepErr error) {
	now := time.Now().UTC()

	var errText *string
	if stepErr != nil {
		s := stepErr.Error()
		errText = &s
	}

	var startedAt, finishedAt *time.Time
	if status == "running" {
		startedAt = &now
	}
	if status == "completed" || status == "failed" {
		finishedAt = &now
	}

	// Upsert: INSERT on first call, UPDATE on retry (idempotent)
	if _, err := db.Exec(ctx, `
		INSERT INTO deployment_steps (deployment_id, step, status, started_at, finished_at, error)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (deployment_id, step) DO UPDATE SET
			status = EXCLUDED.status,
			started_at = COALESCE(EXCLUDED.started_at, deployment_steps.started_at),
			finished_at = EXCLUDED.finished_at,
			error = EXCLUDED.error
	`, deploymentID, step, status, startedAt, finishedAt, errText); err != nil {
		slog.Error("markStep: upsert deployment_steps failed", "deployment_id", deploymentID, "step", step, "error", err)
	}

	// Keep deployments.updated_at fresh for crash recovery
	if _, err := db.Exec(ctx,
		"UPDATE deployments SET updated_at = $1 WHERE id = $2",
		now, deploymentID,
	); err != nil {
		slog.Error("markStep: update deployments.updated_at failed", "deployment_id", deploymentID, "error", err)
	}
}

// markStepProviderID records the provider-side ID on a step (for idempotent retry + rollback).
func markStepProviderID(ctx context.Context, db *pgxpool.Pool, deploymentID, step, providerID string) {
	if _, err := db.Exec(ctx,
		"UPDATE deployment_steps SET provider_id = $1 WHERE deployment_id = $2 AND step = $3",
		providerID, deploymentID, step,
	); err != nil {
		slog.Error("markStepProviderID failed", "deployment_id", deploymentID, "step", step, "error", err)
	}
}

// isStepCompleted checks if a step already finished (crash recovery uses this to skip completed steps).
func isStepCompleted(ctx context.Context, db *pgxpool.Pool, deploymentID, step string) bool {
	var exists int
	err := db.QueryRow(ctx,
		"SELECT 1 FROM deployment_steps WHERE deployment_id = $1 AND step = $2 AND status = 'completed' LIMIT 1",
		deploymentID, step,
	).Scan(&exists)
	return err == nil
}

// updateDeploymentStatus updates the deployment's status and updated_at.
func updateDeploymentStatus(ctx context.Context, db *pgxpool.Pool, deploymentID, status string) {
	if _, err := db.Exec(ctx,
		"UPDATE deployments SET status = $1, updated_at = NOW() WHERE id = $2",
		status, deploymentID,
	); err != nil {
		slog.Error("updateDeploymentStatus failed", "deployment_id", deploymentID, "status", status, "error", err)
	}
}

// failDeployment marks a deployment as failed with an error message.
func failDeployment(ctx context.Context, db *pgxpool.Pool, deploymentID string, deployErr error) {
	errMsg := ""
	if deployErr != nil {
		errMsg = deployErr.Error()
	}
	if _, err := db.Exec(ctx,
		"UPDATE deployments SET status = 'failed', error = $1, updated_at = NOW(), finished_at = NOW() WHERE id = $2",
		errMsg, deploymentID,
	); err != nil {
		slog.Error("failDeployment failed", "deployment_id", deploymentID, "error", err)
	}
}

// cancelDeployment marks a deployment as cancelled (user-initiated via cancel_deployment tool).
func cancelDeployment(ctx context.Context, db *pgxpool.Pool, deploymentID string) {
	if _, err := db.Exec(ctx,
		"UPDATE deployments SET status = 'cancelled', updated_at = NOW(), finished_at = NOW() WHERE id = $1",
		deploymentID,
	); err != nil {
		slog.Error("cancelDeployment failed", "deployment_id", deploymentID, "error", err)
	}
}

// completeDeployment marks a deployment as live.
func completeDeployment(ctx context.Context, db *pgxpool.Pool, deploymentID string) {
	if _, err := db.Exec(ctx,
		"UPDATE deployments SET status = 'live', updated_at = NOW(), finished_at = NOW() WHERE id = $1",
		deploymentID,
	); err != nil {
		slog.Error("completeDeployment failed", "deployment_id", deploymentID, "error", err)
	}
}

// getStepProviderID reads the provider_id stored on a completed step.
func getStepProviderID(ctx context.Context, db *pgxpool.Pool, deploymentID, step string) string {
	var pid string
	if err := db.QueryRow(ctx,
		"SELECT COALESCE(provider_id, '') FROM deployment_steps WHERE deployment_id = $1 AND step = $2",
		deploymentID, step,
	).Scan(&pid); err != nil {
		slog.Error("getStepProviderID failed", "deployment_id", deploymentID, "step", step, "error", err)
	}
	return pid
}
