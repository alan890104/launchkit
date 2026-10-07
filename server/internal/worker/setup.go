package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/billing"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// Config holds dependencies for the river worker setup.
type Config struct {
	DB      *pgxpool.Pool
	Builder provider.Builder
	// ExecuteDeployment runs a full deployment given planID and deploymentID.
	// Provided as a closure by server.go to avoid an import cycle
	// (deploy imports worker for BuildJobArgs, so worker cannot import deploy).
	ExecuteDeployment func(ctx context.Context, planID, deploymentID string) error

	// Provider references for CleanupWorker (all optional — nil disables cleanup for that type).
	Compute       provider.Compute
	Database      provider.Database
	Cache         provider.Cache
	ObjectStorage provider.ObjectStorage
	Static        provider.Static

	// BuildWorkerTimeout overrides the default 15-minute build job timeout.
	// Zero uses the default.
	BuildWorkerTimeout time.Duration

	// Metering dependencies (all optional — nil disables billing metering).
	ConsumptionFetchers []provider.ConsumptionFetcher
	Pricing             *billing.PricingTable

	// Domain registrar (optional — nil disables domain renewal worker).
	DomainRegistrar provider.DomainRegistrar
}

// Setup creates and starts a river client with all workers registered.
// Returns the client (for job insertion) and a stop function.
func Setup(ctx context.Context, cfg Config) (*river.Client[pgx.Tx], func() error) {
	// Run river schema migrations
	migrator, err := rivermigrate.New(riverpgxv5.New(cfg.DB), nil)
	if err != nil {
		panic(fmt.Sprintf("create river migrator: %v", err))
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		panic(fmt.Sprintf("run river migrations: %v", err))
	}
	slog.Info("river migrations applied")

	// Register workers
	workers := river.NewWorkers()
	river.AddWorker(workers, &BuildWorker{Builder: cfg.Builder, DB: cfg.DB, JobTimeout: cfg.BuildWorkerTimeout})
	river.AddWorker(workers, &DeployWorker{DB: cfg.DB, ExecuteDeployment: cfg.ExecuteDeployment})
	river.AddWorker(workers, &CleanupWorker{
		DB:            cfg.DB,
		Compute:       cfg.Compute,
		Database:      cfg.Database,
		Cache:         cfg.Cache,
		ObjectStorage: cfg.ObjectStorage,
		Static:        cfg.Static,
	})

	// Register metering worker if pricing is configured
	if cfg.Pricing != nil {
		river.AddWorker(workers, &MeteringWorker{
			DB:       cfg.DB,
			Fetchers: cfg.ConsumptionFetchers,
			Pricing:  cfg.Pricing,
			Compute:  cfg.Compute,
		})
	}

	// Register domain workers if registrar is configured
	if cfg.DomainRegistrar != nil {
		river.AddWorker(workers, &DomainRenewalWorker{
			DB:              cfg.DB,
			DomainRegistrar: cfg.DomainRegistrar,
		})
		river.AddWorker(workers, &DomainReconcileWorker{
			DB:              cfg.DB,
			DomainRegistrar: cfg.DomainRegistrar,
		})
	}

	// Build periodic jobs list
	var periodicJobs []*river.PeriodicJob

	if cfg.DomainRegistrar != nil {
		// Domain renewal: daily check for expiring domains
		periodicJobs = append(periodicJobs, river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return DomainRenewalJobArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		))
		// Domain reconciliation: daily integrity check against registrar
		periodicJobs = append(periodicJobs, river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return DomainReconcileJobArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		))
	}

	if cfg.Pricing != nil {
		periodicJobs = append(periodicJobs, river.NewPeriodicJob(
			river.PeriodicInterval(1*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				now := time.Now().UTC().Truncate(time.Hour)
				return MeteringJobArgs{
					PeriodStart: now.Add(-65 * time.Minute),
					PeriodEnd:   now.Add(-5 * time.Minute),
				}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: false},
		))
	}

	// Create river client with queue config
	client, err := river.NewClient(riverpgxv5.New(cfg.DB), &river.Config{
		Queues: map[string]river.QueueConfig{
			"build":            {MaxWorkers: 3},
			river.QueueDefault: {MaxWorkers: 10},
			"cleanup":          {MaxWorkers: 5},
			"metering":         {MaxWorkers: 1},
		},
		Workers:      workers,
		PeriodicJobs: periodicJobs,
	})
	if err != nil {
		panic(fmt.Sprintf("create river client: %v", err))
	}

	// Start processing jobs
	if err := client.Start(ctx); err != nil {
		slog.Error("start river client", "error", err)
	}

	stop := func() error {
		slog.Info("stopping river workers")
		return client.Stop(ctx)
	}

	slog.Info("river workers started", "build_max_workers", 3)
	return client, stop
}

// DeployWorker handles deployments enqueued via river (both new deploys and crash recovery).
type DeployWorker struct {
	river.WorkerDefaults[DeployJobArgs]
	DB                *pgxpool.Pool
	ExecuteDeployment func(ctx context.Context, planID, deploymentID string) error
}

// Timeout caps each deployment at 15 minutes. Builds (separate BuildWorker) also have
// their own 15-minute cap. Without this, a stuck deploy hangs indefinitely.
func (w *DeployWorker) Timeout(job *river.Job[DeployJobArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *DeployWorker) Work(ctx context.Context, job *river.Job[DeployJobArgs]) error {
	args := job.Args
	slog.Info("deploy worker processing",
		"deployment_id", args.DeploymentID,
		"plan_id", args.PlanID,
		"is_recovery", args.IsRecovery,
		"attempt", job.Attempt,
	)

	if !args.IsRecovery {
		// New deployment — run full orchestration via the injected closure.
		if w.ExecuteDeployment != nil {
			return w.ExecuteDeployment(ctx, args.PlanID, args.DeploymentID)
		}
		slog.Warn("ExecuteDeployment not configured, skipping", "deployment_id", args.DeploymentID)
		return nil
	}

	// Recovery flow: check if all steps are actually completed
	var stepCount int
	var failedCount int
	err := w.DB.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE status = 'failed')
		FROM deployment_steps
		WHERE deployment_id = $1
	`, args.DeploymentID).Scan(&stepCount, &failedCount)
	if err != nil {
		return fmt.Errorf("check deployment steps: %w", err)
	}

	if failedCount > 0 {
		// Steps failed — mark deployment as failed
		if _, execErr := w.DB.Exec(ctx,
			"UPDATE deployments SET status = 'failed', updated_at = NOW() WHERE id = $1",
			args.DeploymentID,
		); execErr != nil {
			slog.Error("deploy worker: failed to mark deployment as failed",
				"deployment_id", args.DeploymentID, "error", execErr)
		}
		return fmt.Errorf("deployment has %d failed steps, cannot recover", failedCount)
	}

	if stepCount > 0 {
		// Steps completed during recovery — mark deployment as live
		if _, execErr := w.DB.Exec(ctx,
			"UPDATE deployments SET status = 'live', finished_at = NOW(), updated_at = NOW() WHERE id = $1",
			args.DeploymentID,
		); execErr != nil {
			slog.Error("deploy worker: failed to mark deployment as live",
				"deployment_id", args.DeploymentID, "error", execErr)
		}
		slog.Info("recovery: deployment completed during retry", "deployment_id", args.DeploymentID)
		return nil
	}

	// No steps completed — deployment genuinely needs re-trigger from scratch.
	// Mark as needs_recovery for manual intervention or re-deploy via API.
	slog.Warn("recovered deployment has no steps; marking needs_recovery",
		"deployment_id", args.DeploymentID)
	if _, execErr := w.DB.Exec(ctx,
		"UPDATE deployments SET status = 'needs_recovery', updated_at = NOW() WHERE id = $1",
		args.DeploymentID,
	); execErr != nil {
		slog.Error("deploy worker: failed to mark deployment needs_recovery",
			"deployment_id", args.DeploymentID, "error", execErr)
	}

	return nil
}
