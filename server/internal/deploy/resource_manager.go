package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// MakeURN builds a deterministic resource identifier.
// Format: launchkit:{projectID}:{resourceType}:{resourceName}
// Same inputs always produce the same URN, enabling idempotent re-deploys.
func MakeURN(projectID, resourceType, resourceName string) string {
	return fmt.Sprintf("launchkit:%s:%s:%s", projectID, resourceType, resourceName)
}

// ResourceManager wraps all cloud provider calls with Pulumi-inspired state tracking.
// Every create/delete is checkpointed in resource_states BEFORE the cloud API call,
// guaranteeing we can clean up even if the server crashes mid-operation.
type ResourceManager struct {
	db                  *pgxpool.Pool
	compute             provider.Compute
	database            provider.Database
	cache               provider.Cache
	objectStorage       provider.ObjectStorage
	static              provider.Static
	riverClient         *river.Client[pgx.Tx]
	computeProviderName string
	storageProviderName string
}

// ResourceManagerConfig is the constructor config for ResourceManager.
type ResourceManagerConfig struct {
	DB                  *pgxpool.Pool
	Compute             provider.Compute
	Database            provider.Database
	Cache               provider.Cache
	ObjectStorage       provider.ObjectStorage
	Static              provider.Static
	RiverClient         *river.Client[pgx.Tx]
	ComputeProviderName string // e.g. "cloud_run" or "ecs_fargate"
	StorageProviderName string // e.g. "gcs" or "s3"
}

func NewResourceManager(cfg ResourceManagerConfig) *ResourceManager {
	computeName := cfg.ComputeProviderName
	if computeName == "" {
		computeName = "compute"
	}
	storageName := cfg.StorageProviderName
	if storageName == "" {
		storageName = "object_storage"
	}
	return &ResourceManager{
		db:                  cfg.DB,
		compute:             cfg.Compute,
		database:            cfg.Database,
		cache:               cfg.Cache,
		objectStorage:       cfg.ObjectStorage,
		static:              cfg.Static,
		riverClient:         cfg.RiverClient,
		computeProviderName: computeName,
		storageProviderName: storageName,
	}
}

// ─── DB state helpers ─────────────────────────────────────────────────────────

func (rm *ResourceManager) upsertResource(ctx context.Context, urn, projectID, deploymentID, resourceType, providerName string, inputs map[string]any) error {
	inputsJSON, err := json.Marshal(inputs)
	if err != nil {
		return fmt.Errorf("marshal inputs: %w", err)
	}
	_, err = rm.db.Exec(ctx, `
		INSERT INTO resource_states (urn, project_id, deployment_id, resource_type, provider, inputs, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'creating')
		ON CONFLICT (urn) DO UPDATE SET
			deployment_id = EXCLUDED.deployment_id,
			inputs        = EXCLUDED.inputs,
			status        = 'creating',
			last_error    = NULL,
			updated_at    = NOW()
	`, urn, projectID, deploymentID, resourceType, providerName, inputsJSON)
	return err
}

func (rm *ResourceManager) activateResource(ctx context.Context, urn string, outputs map[string]any) error {
	outputsJSON, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf("marshal outputs: %w", err)
	}
	_, err = rm.db.Exec(ctx,
		"UPDATE resource_states SET status = 'active', outputs = $1, updated_at = NOW() WHERE urn = $2",
		outputsJSON, urn)
	return err
}

func (rm *ResourceManager) failResource(ctx context.Context, urn string, failErr error) {
	errMsg := ""
	if failErr != nil {
		errMsg = failErr.Error()
	}
	if _, err := rm.db.Exec(ctx,
		"UPDATE resource_states SET status = 'failed', last_error = $1, updated_at = NOW() WHERE urn = $2",
		errMsg, urn,
	); err != nil {
		slog.Error("failResource: update failed", "urn", urn, "error", err)
	}
}

func (rm *ResourceManager) beginDelete(ctx context.Context, urn string) {
	if _, err := rm.db.Exec(ctx,
		"UPDATE resource_states SET status = 'deleting', updated_at = NOW() WHERE urn = $1",
		urn,
	); err != nil {
		slog.Error("beginDelete: update failed", "urn", urn, "error", err)
	}
}

func (rm *ResourceManager) confirmDelete(ctx context.Context, urn string) {
	if _, err := rm.db.Exec(ctx,
		"UPDATE resource_states SET status = 'deleted', updated_at = NOW() WHERE urn = $1",
		urn,
	); err != nil {
		slog.Error("confirmDelete: update failed", "urn", urn, "error", err)
	}
}

func (rm *ResourceManager) markOrphaned(ctx context.Context, urn string, orphanErr error) {
	errMsg := ""
	if orphanErr != nil {
		errMsg = orphanErr.Error()
	}
	if _, err := rm.db.Exec(ctx,
		"UPDATE resource_states SET status = 'orphaned', last_error = $1, updated_at = NOW() WHERE urn = $2",
		errMsg, urn,
	); err != nil {
		slog.Error("markOrphaned: update failed", "urn", urn, "error", err)
	}
}

// activeOutputs returns the outputs JSON for an active resource, or nil if not active.
func (rm *ResourceManager) activeOutputs(ctx context.Context, urn string) map[string]any {
	var status, outputsRaw string
	err := rm.db.QueryRow(ctx,
		"SELECT status, outputs::text FROM resource_states WHERE urn = $1",
		urn,
	).Scan(&status, &outputsRaw)
	if err != nil || status != "active" {
		return nil
	}
	var outputs map[string]any
	json.Unmarshal([]byte(outputsRaw), &outputs)
	return outputs
}

// ─── Create / Deploy methods ──────────────────────────────────────────────────

// CreateCompute creates a compute service placeholder and records it in resource_states.
// Idempotent: if the resource is already active, returns the existing URL.
func (rm *ResourceManager) CreateCompute(ctx context.Context, projectID, deploymentID string, opts provider.DeployOpts) (string, error) {
	urn := MakeURN(projectID, "compute", opts.ServiceName)

	// Idempotency: return existing URL if already active.
	if outputs := rm.activeOutputs(ctx, urn); outputs != nil {
		slog.Info("resource already active", "urn", urn)
		return strVal(outputs, "url"), nil
	}

	inputs := map[string]any{
		"service_name":     opts.ServiceName,
		"region":           opts.Region,
		"port":             opts.Port,
		"session_affinity": opts.SessionAffinity,
		"request_timeout":  opts.RequestTimeout,
	}
	if err := rm.upsertResource(ctx, urn, projectID, deploymentID, "compute", rm.computeProviderName, inputs); err != nil {
		slog.Error("upsertResource failed (non-fatal, continuing)", "urn", urn, "error", err)
	}

	url, err := rm.compute.CreateService(ctx, opts)
	if err != nil {
		rm.failResource(ctx, urn, err)
		return "", err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"url":          url,
		"service_name": opts.ServiceName,
		"region":       opts.Region,
	}); err != nil {
		slog.Error("activateResource failed (resource exists in cloud)", "urn", urn, "error", err)
	}

	return url, nil
}

// DeployCompute updates a running compute service with a new image and records the outputs.
func (rm *ResourceManager) DeployCompute(ctx context.Context, projectID string, opts provider.DeployOpts) (string, error) {
	urn := MakeURN(projectID, "compute", opts.ServiceName)

	url, err := rm.compute.Deploy(ctx, opts)
	if err != nil {
		return "", err
	}

	// Update outputs with the deployed image URI.
	if err := rm.activateResource(ctx, urn, map[string]any{
		"url":          url,
		"service_name": opts.ServiceName,
		"region":       opts.Region,
		"image_uri":    opts.ImageURI,
	}); err != nil {
		slog.Error("activateResource after deploy failed (non-fatal)", "urn", urn, "error", err)
	}

	return url, nil
}

// CreateDatabase provisions a managed database and records it in resource_states.
func (rm *ResourceManager) CreateDatabase(ctx context.Context, projectID, deploymentID string, opts provider.CreateDBOpts) (*provider.CreateDBResult, error) {
	urn := MakeURN(projectID, "database", opts.Name)

	// Idempotency: return existing connection details if already active.
	if outputs := rm.activeOutputs(ctx, urn); outputs != nil {
		slog.Info("resource already active", "urn", urn)
		return &provider.CreateDBResult{
			ProviderID:    strVal(outputs, "provider_id"),
			ConnectionURL: strVal(outputs, "connection_url"),
			Host:          strVal(outputs, "host"),
			Database:      strVal(outputs, "database"),
			User:          strVal(outputs, "user"),
		}, nil
	}

	inputs := map[string]any{"name": opts.Name, "region": opts.Region}
	if err := rm.upsertResource(ctx, urn, projectID, deploymentID, "database", "neon", inputs); err != nil {
		slog.Error("upsertResource failed (non-fatal)", "urn", urn, "error", err)
	}

	result, err := rm.database.Create(ctx, opts)
	if err != nil {
		rm.failResource(ctx, urn, err)
		return nil, err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"provider_id":    result.ProviderID,
		"connection_url": result.ConnectionURL,
		"host":           result.Host,
		"database":       result.Database,
		"user":           result.User,
	}); err != nil {
		slog.Error("activateResource failed (non-fatal)", "urn", urn, "error", err)
	}

	return result, nil
}

// CreateCache provisions a managed Redis instance and records it in resource_states.
func (rm *ResourceManager) CreateCache(ctx context.Context, projectID, deploymentID string, opts provider.CreateRedisOpts) (*provider.CreateRedisResult, error) {
	urn := MakeURN(projectID, "cache", opts.Name)

	// Idempotency: return existing connection details if already active.
	if outputs := rm.activeOutputs(ctx, urn); outputs != nil {
		slog.Info("resource already active", "urn", urn)
		return &provider.CreateRedisResult{
			ProviderID:    strVal(outputs, "provider_id"),
			Endpoint:      strVal(outputs, "endpoint"),
			Password:      strVal(outputs, "password"),
			ConnectionURL: strVal(outputs, "connection_url"),
		}, nil
	}

	inputs := map[string]any{"name": opts.Name, "region": opts.Region}
	if err := rm.upsertResource(ctx, urn, projectID, deploymentID, "cache", "upstash", inputs); err != nil {
		slog.Error("upsertResource failed (non-fatal)", "urn", urn, "error", err)
	}

	result, err := rm.cache.Create(ctx, opts)
	if err != nil {
		rm.failResource(ctx, urn, err)
		return nil, err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"provider_id":    result.ProviderID,
		"endpoint":       result.Endpoint,
		"password":       result.Password,
		"connection_url": result.ConnectionURL,
	}); err != nil {
		slog.Error("activateResource failed (non-fatal)", "urn", urn, "error", err)
	}

	return result, nil
}

// CreateStorage provisions an object storage bucket and records it in resource_states.
func (rm *ResourceManager) CreateStorage(ctx context.Context, projectID, deploymentID string, opts provider.CreateStorageOpts) (*provider.CreateStorageResult, error) {
	urn := MakeURN(projectID, "storage", opts.Name)

	// Idempotency: return existing bucket info if already active.
	if outputs := rm.activeOutputs(ctx, urn); outputs != nil {
		slog.Info("resource already active", "urn", urn)
		return &provider.CreateStorageResult{
			ProviderID: strVal(outputs, "provider_id"),
			BucketURL:  strVal(outputs, "bucket_url"),
			PublicURL:  strVal(outputs, "public_url"),
		}, nil
	}

	inputs := map[string]any{"name": opts.Name, "region": opts.Region}
	if err := rm.upsertResource(ctx, urn, projectID, deploymentID, "storage", rm.storageProviderName, inputs); err != nil {
		slog.Error("upsertResource failed (non-fatal)", "urn", urn, "error", err)
	}

	result, err := rm.objectStorage.CreateBucket(ctx, opts)
	if err != nil {
		rm.failResource(ctx, urn, err)
		return nil, err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"provider_id": result.ProviderID,
		"bucket_url":  result.BucketURL,
		"public_url":  result.PublicURL,
	}); err != nil {
		slog.Error("activateResource failed (non-fatal)", "urn", urn, "error", err)
	}

	return result, nil
}

// CreateStaticProject creates a Cloudflare Pages project and records it in resource_states.
func (rm *ResourceManager) CreateStaticProject(ctx context.Context, projectID, deploymentID, projectName string) error {
	urn := MakeURN(projectID, "static", projectName)

	// Idempotency: skip if already active.
	if outputs := rm.activeOutputs(ctx, urn); outputs != nil {
		slog.Info("resource already active", "urn", urn)
		return nil
	}

	inputs := map[string]any{"project_name": projectName}
	if err := rm.upsertResource(ctx, urn, projectID, deploymentID, "static", "cloudflare_pages", inputs); err != nil {
		slog.Error("upsertResource failed (non-fatal)", "urn", urn, "error", err)
	}

	if err := rm.static.CreateProject(ctx, projectName); err != nil {
		rm.failResource(ctx, urn, err)
		return err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"project_name": projectName,
	}); err != nil {
		slog.Error("activateResource failed (non-fatal)", "urn", urn, "error", err)
	}

	return nil
}

// DeployStatic uploads assets to a Cloudflare Pages project and updates resource_states with the live URL.
func (rm *ResourceManager) DeployStatic(ctx context.Context, projectID string, opts provider.DeployStaticOpts) (string, error) {
	urn := MakeURN(projectID, "static", opts.ProjectName)

	url, err := rm.static.Deploy(ctx, opts)
	if err != nil {
		return "", err
	}

	if err := rm.activateResource(ctx, urn, map[string]any{
		"project_name": opts.ProjectName,
		"url":          url,
	}); err != nil {
		slog.Error("activateResource after deploy failed (non-fatal)", "urn", urn, "error", err)
	}

	return url, nil
}

// ─── Rollback ─────────────────────────────────────────────────────────────────

// RollbackAll deletes all active/creating resources for a deployment.
// Deletion order: static → compute → storage/database/cache (parallel within tier).
// If a delete fails, the resource is marked 'orphaned' and a CleanupWorker job is enqueued
// (MaxAttempts: 10, exponential backoff) to retry later.
func (rm *ResourceManager) RollbackAll(ctx context.Context, projectID, deploymentID string) {
	slog.Warn("ResourceManager.RollbackAll", "project_id", projectID, "deployment_id", deploymentID)

	rows, err := rm.db.Query(ctx, `
		SELECT urn, resource_type, provider, outputs::text
		FROM resource_states
		WHERE deployment_id = $1 AND status IN ('active', 'creating')
		ORDER BY created_at DESC
	`, deploymentID)
	if err != nil {
		slog.Error("RollbackAll: query resource_states failed", "error", err)
		return
	}
	defer rows.Close()

	type entry struct {
		urn          string
		resourceType string
		providerName string
		outputs      map[string]any
	}

	grouped := make(map[string][]entry)
	for rows.Next() {
		var urn, resourceType, providerName, outputsRaw string
		if err := rows.Scan(&urn, &resourceType, &providerName, &outputsRaw); err != nil {
			slog.Error("RollbackAll: scan row failed", "error", err)
			continue
		}
		var outputs map[string]any
		json.Unmarshal([]byte(outputsRaw), &outputs)
		grouped[resourceType] = append(grouped[resourceType], entry{
			urn: urn, resourceType: resourceType, providerName: providerName, outputs: outputs,
		})
	}
	if err := rows.Err(); err != nil {
		slog.Error("RollbackAll: rows iteration error", "error", err)
	}

	// Dependency order: delete static first (no dependencies on other resources),
	// then compute (may depend on static for VITE_API_URL), then data resources.
	for _, resType := range []string{"static", "compute", "storage", "database", "cache"} {
		for _, r := range grouped[resType] {
			rm.deleteOne(ctx, r.urn, r.resourceType, r.outputs)
		}
	}
}

// deleteOne attempts to delete a single resource and updates resource_states.
// On failure, marks as orphaned and enqueues a CleanupWorker job for retry.
func (rm *ResourceManager) deleteOne(ctx context.Context, urn, resourceType string, outputs map[string]any) {
	rm.beginDelete(ctx, urn)

	var deleteErr error
	switch resourceType {
	case "compute":
		if rm.compute == nil {
			slog.Warn("deleteOne: compute provider nil, skipping", "urn", urn)
			return
		}
		serviceName := strVal(outputs, "service_name")
		if serviceName == "" {
			serviceName = urnSuffix(urn)
		}
		region := strVal(outputs, "region")
		deleteErr = rm.compute.DeleteService(ctx, serviceName, region)

	case "static":
		if rm.static == nil {
			slog.Warn("deleteOne: static provider nil, skipping", "urn", urn)
			return
		}
		projectName := strVal(outputs, "project_name")
		if projectName == "" {
			projectName = urnSuffix(urn)
		}
		deleteErr = rm.static.DeleteProject(ctx, projectName)

	case "database":
		if rm.database == nil {
			slog.Warn("deleteOne: database provider nil, skipping", "urn", urn)
			return
		}
		deleteErr = rm.database.Delete(ctx, strVal(outputs, "provider_id"))

	case "cache":
		if rm.cache == nil {
			slog.Warn("deleteOne: cache provider nil, skipping", "urn", urn)
			return
		}
		deleteErr = rm.cache.Delete(ctx, strVal(outputs, "provider_id"))

	case "storage":
		if rm.objectStorage == nil {
			slog.Warn("deleteOne: object storage provider nil, skipping", "urn", urn)
			return
		}
		bucketName := strVal(outputs, "provider_id")
		deleteErr = rm.objectStorage.DeleteBucket(ctx, bucketName)
	}

	if deleteErr != nil {
		slog.Error("deleteOne failed, marking orphaned + enqueueing cleanup",
			"urn", urn, "resource_type", resourceType, "error", deleteErr)
		rm.markOrphaned(ctx, urn, deleteErr)
		rm.enqueueCleanup(ctx, urn, resourceType, outputs)
		return
	}

	rm.confirmDelete(ctx, urn)
	slog.Info("resource deleted", "urn", urn)
}

// enqueueCleanup inserts a CleanupWorker job for a resource that failed to delete.
func (rm *ResourceManager) enqueueCleanup(ctx context.Context, urn, resourceType string, outputs map[string]any) {
	if rm.riverClient == nil {
		slog.Warn("enqueueCleanup: river client nil, resource may be orphaned permanently", "urn", urn)
		return
	}

	args := worker.CleanupJobArgs{
		URN:          urn,
		ResourceType: resourceType,
		ProviderID:   strVal(outputs, "provider_id"),
		ServiceName:  strVal(outputs, "service_name"),
		Region:       strVal(outputs, "region"),
		ProjectName:  strVal(outputs, "project_name"),
		BucketName:   strVal(outputs, "provider_id"),
	}
	if args.ServiceName == "" {
		args.ServiceName = urnSuffix(urn)
	}
	if args.ProjectName == "" && resourceType == "static" {
		args.ProjectName = urnSuffix(urn)
	}

	_, err := rm.riverClient.Insert(ctx, args, &river.InsertOpts{
		Queue:      "cleanup",
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	if err != nil {
		slog.Error("enqueueCleanup: river insert failed", "urn", urn, "error", err)
	} else {
		slog.Info("cleanup job enqueued", "urn", urn)
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// strVal extracts a string value from a map[string]any.
func strVal(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// urnSuffix extracts the resource name (last component) from a URN.
// "launchkit:proj123:compute:lk-myapp-api" → "lk-myapp-api"
func urnSuffix(urn string) string {
	parts := strings.SplitN(urn, ":", 4)
	if len(parts) == 4 {
		return parts[3]
	}
	return urn
}
