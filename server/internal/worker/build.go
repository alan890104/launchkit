package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// BuildJobArgs defines the payload for a build job.
type BuildJobArgs struct {
	DeploymentID    string            `json:"deployment_id"`
	ServiceName     string            `json:"service_name"`
	UploadID        string            `json:"upload_id"`
	SourceBucket    string            `json:"source_bucket"`
	SourceKey       string            `json:"source_key"`
	SourceDir       string            `json:"source_dir"`
	ImageTag        string            `json:"image_tag"`
	DistOutputKey   string            `json:"dist_output_key,omitempty"` // frontend, cloud mode
	BuildArgs       map[string]string `json:"build_args"`
	StepName        string            `json:"step_name"`                   // e.g. "build_api"
	ProjectID       string            `json:"project_id"`                  // for BuildKit cache mount tenant isolation
	LocalSourcePath string            `json:"local_source_path,omitempty"` // absolute path (local mode)
}

func (BuildJobArgs) Kind() string { return "build" }

// InsertOpts caps build job retries at 3. Compilation errors are permanent
// failures and don't benefit from the default 25 retry attempts.
func (BuildJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3}
}

// BuildResult is stored in deployment_steps.provider_id as JSON after a successful build.
type BuildResult struct {
	ImageURI      string `json:"image_uri"`
	LogsURL       string `json:"logs_url"`
	LocalDistPath string `json:"local_dist_path,omitempty"` // frontend local mode only
}

// BuildWorker processes build jobs via river.
// MaxWorkers is set at queue level to control BuildKit VM concurrency.
type BuildWorker struct {
	river.WorkerDefaults[BuildJobArgs]
	Builder    provider.Builder
	DB         *pgxpool.Pool
	JobTimeout time.Duration
}

// Timeout gives Cloud Build enough time to complete (default 60s is too short).
func (w *BuildWorker) Timeout(job *river.Job[BuildJobArgs]) time.Duration {
	if w.JobTimeout > 0 {
		return w.JobTimeout
	}
	return 15 * time.Minute
}

func (w *BuildWorker) Work(ctx context.Context, job *river.Job[BuildJobArgs]) error {
	args := job.Args
	slog.Info("build worker starting",
		"deployment_id", args.DeploymentID,
		"service", args.ServiceName,
		"step", args.StepName,
	)

	// Mark step as running
	markStepRunning(ctx, w.DB, args.DeploymentID, args.StepName)

	buildStart := time.Now()

	result, err := w.Builder.Build(ctx, provider.BuildOpts{
		SourceBucket:    args.SourceBucket,
		SourceKey:       args.SourceKey,
		LocalSourcePath: args.LocalSourcePath,
		ImageTag:        args.ImageTag,
		DistOutputKey:   args.DistOutputKey,
		SourceDir:       args.SourceDir,
		BuildArgs:       args.BuildArgs,
		ProjectID:       args.ProjectID,
	})

	buildDurationMs := int(time.Since(buildStart).Milliseconds())

	if err != nil {
		// Record build duration even on failure (still used VM time)
		recordBuildDuration(ctx, w.DB, args.DeploymentID, args.ProjectID, args.StepName, buildDurationMs)
		markStepFailed(ctx, w.DB, args.DeploymentID, args.StepName, err)
		return fmt.Errorf("build %s: %w", args.ServiceName, err)
	}

	// Record build duration for billing
	recordBuildDuration(ctx, w.DB, args.DeploymentID, args.ProjectID, args.StepName, buildDurationMs)

	// Store build result as JSON in provider_id for orchestrator to read
	resultJSON, err := json.Marshal(BuildResult{
		ImageURI:      result.ImageURI,
		LogsURL:       result.LogsURL,
		LocalDistPath: result.LocalDistPath,
	})
	if err != nil {
		slog.Error("build worker: failed to marshal build result",
			"deployment_id", args.DeploymentID,
			"service", args.ServiceName,
			"error", err,
		)
		resultJSON = []byte("{}")
	}
	markStepCompleted(ctx, w.DB, args.DeploymentID, args.StepName, string(resultJSON))

	slog.Info("build worker completed",
		"deployment_id", args.DeploymentID,
		"service", args.ServiceName,
		"image", result.ImageURI,
		"build_duration_ms", buildDurationMs,
	)
	return nil
}

// Step tracking helpers (writes to deployment_steps table).

func markStepRunning(ctx context.Context, db *pgxpool.Pool, deploymentID, step string) {
	if _, execErr := db.Exec(ctx, `
		INSERT INTO deployment_steps (deployment_id, step, status, started_at)
		VALUES ($1, $2, 'running', NOW())
		ON CONFLICT (deployment_id, step) DO UPDATE SET
			status = 'running',
			started_at = COALESCE(deployment_steps.started_at, NOW())
	`, deploymentID, step); execErr != nil {
		slog.Error("markStepRunning: failed to upsert deployment_steps",
			"deployment_id", deploymentID, "step", step, "error", execErr)
	}

	if _, execErr := db.Exec(ctx, "UPDATE deployments SET updated_at = NOW() WHERE id = $1", deploymentID); execErr != nil {
		slog.Error("markStepRunning: failed to update deployments",
			"deployment_id", deploymentID, "error", execErr)
	}
}

func markStepCompleted(ctx context.Context, db *pgxpool.Pool, deploymentID, step, providerID string) {
	if _, execErr := db.Exec(ctx, `
		UPDATE deployment_steps
		SET status = 'completed', finished_at = NOW(), provider_id = $1
		WHERE deployment_id = $2 AND step = $3
	`, providerID, deploymentID, step); execErr != nil {
		slog.Error("markStepCompleted: failed to update deployment_steps",
			"deployment_id", deploymentID, "step", step, "error", execErr)
	}

	if _, execErr := db.Exec(ctx, "UPDATE deployments SET updated_at = NOW() WHERE id = $1", deploymentID); execErr != nil {
		slog.Error("markStepCompleted: failed to update deployments",
			"deployment_id", deploymentID, "error", execErr)
	}
}

// recordBuildDuration stores build time for billing and updates the deployment record.
// Uses ON CONFLICT to handle River job retries idempotently — same (deployment_id, step_name)
// won't be inserted twice.
func recordBuildDuration(ctx context.Context, db *pgxpool.Pool, deploymentID, projectID, stepName string, durationMs int) {
	// Update deployment record
	_, _ = db.Exec(ctx,
		"UPDATE deployments SET build_duration_ms = $1 WHERE id = $2",
		durationMs, deploymentID,
	)

	// Resolve team_id
	var teamID string
	if err := db.QueryRow(ctx,
		"SELECT team_id FROM projects WHERE id = $1", projectID,
	).Scan(&teamID); err != nil {
		slog.Error("recordBuildDuration: resolve team_id", "project_id", projectID, "error", err)
		return
	}

	// Insert build_usage record — ON CONFLICT handles retries (same deployment+step = no-op).
	_, _ = db.Exec(ctx, `
		INSERT INTO build_usage (team_id, project_id, deployment_id, step_name, duration_ms, cost, period_month)
		VALUES ($1, $2, $3, $4, $5, 0, date_trunc('month', NOW())::DATE)
		ON CONFLICT (deployment_id, step_name) DO NOTHING
	`, teamID, projectID, deploymentID, stepName, durationMs)

	slog.Debug("build duration recorded",
		"deployment_id", deploymentID,
		"step", stepName,
		"duration_ms", durationMs,
	)
}

func markStepFailed(ctx context.Context, db *pgxpool.Pool, deploymentID, step string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if _, execErr := db.Exec(ctx, `
		UPDATE deployment_steps
		SET status = 'failed', finished_at = NOW(), error = $1
		WHERE deployment_id = $2 AND step = $3
	`, errMsg, deploymentID, step); execErr != nil {
		slog.Error("markStepFailed: failed to update deployment_steps",
			"deployment_id", deploymentID, "step", step, "error", execErr)
	}

	if _, execErr := db.Exec(ctx, "UPDATE deployments SET updated_at = NOW() WHERE id = $1", deploymentID); execErr != nil {
		slog.Error("markStepFailed: failed to update deployments",
			"deployment_id", deploymentID, "error", execErr)
	}
}
