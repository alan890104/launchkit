package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// CleanupJobArgs describes a cloud resource to be deleted.
// Enqueued by ResourceManager.RollbackAll() when a delete fails;
// CleanupWorker retries up to 10 times with River's exponential backoff.
type CleanupJobArgs struct {
	URN          string `json:"urn"`           // launchkit:{project_id}:{type}:{name}
	ResourceType string `json:"resource_type"` // compute / database / cache / storage / static
	ProviderID   string `json:"provider_id"`   // neon project ID, upstash database_id, GCS bucket name, etc.
	ServiceName  string `json:"service_name"`  // compute: Cloud Run service name / ECS service name
	Region       string `json:"region"`        // compute: AWS/GCP region
	ProjectName  string `json:"project_name"`  // static: Cloudflare Pages project name
	BucketName   string `json:"bucket_name"`   // storage: GCS/S3 bucket name
}

func (CleanupJobArgs) Kind() string { return "cleanup" }

// InsertOpts gives cleanup jobs 10 retries — deletion should eventually succeed.
// River uses exponential backoff, so failures don't hammer the API immediately.
func (CleanupJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 10}
}

// CleanupWorker deletes orphaned cloud resources and updates resource_states.
// It holds direct provider references so it can make the actual cloud API calls.
type CleanupWorker struct {
	river.WorkerDefaults[CleanupJobArgs]
	DB            *pgxpool.Pool
	Compute       provider.Compute
	Database      provider.Database
	Cache         provider.Cache
	ObjectStorage provider.ObjectStorage
	Static        provider.Static
}

// Timeout caps each cleanup attempt at 2 minutes.
func (w *CleanupWorker) Timeout(_ *river.Job[CleanupJobArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *CleanupWorker) Work(ctx context.Context, job *river.Job[CleanupJobArgs]) error {
	args := job.Args
	slog.Info("cleanup worker: deleting orphaned resource",
		"urn", args.URN,
		"resource_type", args.ResourceType,
		"attempt", job.Attempt,
	)

	var deleteErr error

	switch args.ResourceType {
	case "compute":
		if w.Compute == nil {
			return fmt.Errorf("compute provider not configured for cleanup")
		}
		deleteErr = w.Compute.DeleteService(ctx, args.ServiceName, args.Region)

	case "database":
		if w.Database == nil {
			return fmt.Errorf("database provider not configured for cleanup")
		}
		deleteErr = w.Database.Delete(ctx, args.ProviderID)

	case "cache":
		if w.Cache == nil {
			return fmt.Errorf("cache provider not configured for cleanup")
		}
		deleteErr = w.Cache.Delete(ctx, args.ProviderID)

	case "storage":
		if w.ObjectStorage == nil {
			return fmt.Errorf("object storage provider not configured for cleanup")
		}
		bucketName := args.BucketName
		if bucketName == "" {
			bucketName = args.ProviderID
		}
		deleteErr = w.ObjectStorage.DeleteBucket(ctx, bucketName)

	case "static":
		if w.Static == nil {
			return fmt.Errorf("static provider not configured for cleanup")
		}
		deleteErr = w.Static.DeleteProject(ctx, args.ProjectName)

	default:
		slog.Warn("cleanup worker: unknown resource type, marking deleted", "resource_type", args.ResourceType, "urn", args.URN)
		w.markDeleted(ctx, args.URN)
		return nil
	}

	if deleteErr != nil {
		// River will retry — update last_error so operators can see what's happening.
		w.updateLastError(ctx, args.URN, deleteErr)
		return fmt.Errorf("delete %s %q: %w", args.ResourceType, args.URN, deleteErr)
	}

	// Success — mark as deleted in resource_states.
	w.markDeleted(ctx, args.URN)
	slog.Info("cleanup worker: resource deleted", "urn", args.URN)
	return nil
}

func (w *CleanupWorker) markDeleted(ctx context.Context, urn string) {
	if _, err := w.DB.Exec(ctx,
		"UPDATE resource_states SET status = 'deleted', updated_at = NOW() WHERE urn = $1",
		urn,
	); err != nil {
		slog.Error("cleanup worker: mark deleted failed", "urn", urn, "error", err)
	}
}

func (w *CleanupWorker) updateLastError(ctx context.Context, urn string, deleteErr error) {
	if _, err := w.DB.Exec(ctx,
		"UPDATE resource_states SET last_error = $1, updated_at = NOW() WHERE urn = $2",
		deleteErr.Error(), urn,
	); err != nil {
		slog.Error("cleanup worker: update last_error failed", "urn", urn, "error", err)
	}
}
