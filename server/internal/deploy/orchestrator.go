package deploy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/alan890104/launchkit/server/internal/tarutil"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"golang.org/x/sync/errgroup"
)

// Orchestrator executes a validated Plan: provision → build → deploy.
// Each step writes checkpoints to deployment_steps for crash recovery.
// Builds are enqueued via river job queue (MaxWorkers: 3).
// ImageTagFunc builds a full container registry tag from a service name and plan ID.
// For GCP: us-east4-docker.pkg.dev/project/user-images/svc:planID
// For AWS: 123456789.dkr.ecr.us-east-1.amazonaws.com/base/svc:planID
type ImageTagFunc func(serviceName, planID string) string

type Orchestrator struct {
	engine        *Engine
	db            *pgxpool.Pool
	buildStorage  provider.BuildStorage
	compute       provider.Compute
	database      provider.Database
	static        provider.Static
	objectStorage provider.ObjectStorage
	cache         provider.Cache
	riverClient   *river.Client[pgx.Tx]
	region        string
	buildBucket   string
	imageTagFn    ImageTagFunc
	baseURL       string
	enc           *Encryptor
	rm            *ResourceManager
}

// OrchestratorConfig holds the dependencies for creating an Orchestrator.
type OrchestratorConfig struct {
	Engine          *Engine
	DB              *pgxpool.Pool
	BuildStorage    provider.BuildStorage
	Compute         provider.Compute
	Database        provider.Database
	Static          provider.Static
	ObjectStorage   provider.ObjectStorage
	Cache           provider.Cache
	RiverClient     *river.Client[pgx.Tx]
	Region          string
	BuildBucket     string
	ImageTagFn      ImageTagFunc
	BaseURL         string
	Enc             *Encryptor
	ResourceManager *ResourceManager
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	return &Orchestrator{
		engine:        cfg.Engine,
		db:            cfg.DB,
		buildStorage:  cfg.BuildStorage,
		compute:       cfg.Compute,
		database:      cfg.Database,
		static:        cfg.Static,
		objectStorage: cfg.ObjectStorage,
		cache:         cfg.Cache,
		riverClient:   cfg.RiverClient,
		region:        cfg.Region,
		buildBucket:   cfg.BuildBucket,
		imageTagFn:    cfg.ImageTagFn,
		baseURL:       cfg.BaseURL,
		enc:           cfg.Enc,
		rm:            cfg.ResourceManager,
	}
}

// Execute runs the full deployment pipeline for a confirmed Plan.
// Each step is checkpointed to deployment_steps for crash recovery.
func (o *Orchestrator) Execute(ctx context.Context, plan *Plan, deploymentID string, progress ProgressFunc) error {
	if err := o.engine.UpdateStatus(ctx, plan.ID, PlanExecuting); err != nil {
		return fmt.Errorf("update plan status: %w", err)
	}

	err := o.execute(ctx, plan, deploymentID, progress)
	if err != nil {
		// Use a fresh context for cleanup — the original context may be cancelled.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		// Distinguish user-initiated cancellation from a real failure.
		// Either way, roll back provisioned cloud resources so nothing is orphaned.
		if ctx.Err() != nil {
			o.engine.UpdateStatus(cleanupCtx, plan.ID, PlanFailed)
			cancelDeployment(cleanupCtx, o.db, deploymentID)
			o.rollback(cleanupCtx, plan, deploymentID) // clean up any partially-created resources
			return err
		}

		o.engine.UpdateStatus(cleanupCtx, plan.ID, PlanFailed)
		failDeployment(cleanupCtx, o.db, deploymentID, err)
		o.rollback(cleanupCtx, plan, deploymentID)
		return err
	}

	o.engine.UpdateStatus(ctx, plan.ID, PlanCompleted)
	completeDeployment(ctx, o.db, deploymentID)
	return nil
}

func (o *Orchestrator) execute(ctx context.Context, plan *Plan, deploymentID string, progress ProgressFunc) error {
	// Phase 0: Create compute service placeholders to get URLs early.
	progress(1, 12, "Creating service placeholders...")
	updateDeploymentStatus(ctx, o.db, deploymentID, "provisioning")

	backendURLs := make(map[string]string)
	for i, svc := range plan.Services {
		if !IsComputeTarget(svc.Target) {
			continue
		}
		stepName := "create_" + svc.Name
		if isStepCompleted(ctx, o.db, deploymentID, stepName) {
			// Crash recovery: read URL from step provider_id
			plan.Services[i].URL = getStepProviderID(ctx, o.db, deploymentID, stepName)
			backendURLs[svc.Name] = plan.Services[i].URL
			continue
		}

		markStep(ctx, o.db, deploymentID, stepName, "running", nil)
		opts := provider.DeployOpts{
			ServiceName:     scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name),
			Region:          o.region,
			Port:            svc.Port,
			SessionAffinity: svc.SessionAffinity,
			RequestTimeout:  svc.RequestTimeout,
		}
		var url string
		var err error
		if o.rm != nil {
			url, err = o.rm.CreateCompute(ctx, plan.ProjectID, deploymentID, opts)
		} else {
			url, err = o.compute.CreateService(ctx, opts)
		}
		if err != nil {
			markStep(ctx, o.db, deploymentID, stepName, "failed", err)
			return fmt.Errorf("create placeholder for %s: %w", svc.Name, err)
		}
		markStep(ctx, o.db, deploymentID, stepName, "completed", nil)
		markStepProviderID(ctx, o.db, deploymentID, stepName, url)
		backendURLs[svc.Name] = url
		plan.Services[i].URL = url
		slog.Info("service placeholder created", "service", svc.Name, "url", url)
	}

	// Phase 1: Provision resources + build all services (parallel)
	progress(2, 12, "Provisioning resources and building services...")
	updateDeploymentStatus(ctx, o.db, deploymentID, "building")

	g, gctx := errgroup.WithContext(ctx)

	// Fix #2: protect resourceResults with a mutex — goroutines write concurrently.
	var resourceMu sync.Mutex
	resourceResults := make(map[string]*provider.CreateDBResult)
	storageResults := make(map[string]*provider.CreateStorageResult)
	redisResults := make(map[string]*provider.CreateRedisResult)

	// Provision resources (databases, storage buckets)
	for _, res := range plan.Resources {
		res := res

		// Storage buckets (GCS/S3)
		if res.Type == ResourceStorage || res.Type == ResourceGCS || res.Type == ResourceS3 {
			stepName := "provision_" + res.Name
			if isStepCompleted(ctx, o.db, deploymentID, stepName) {
				continue
			}
			if o.objectStorage == nil {
				slog.Warn("object storage provider not configured, skipping", "resource", res.Name)
				continue
			}
			g.Go(func() error {
				markStep(gctx, o.db, deploymentID, stepName, "running", nil)
				progress(3, 12, "Provisioning storage bucket...")
				scopedName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, res.Name)
				storOpts := provider.CreateStorageOpts{Name: scopedName, Region: res.Region}
				var result *provider.CreateStorageResult
				var err error
				if o.rm != nil {
					result, err = o.rm.CreateStorage(gctx, plan.ProjectID, deploymentID, storOpts)
				} else {
					result, err = o.objectStorage.CreateBucket(gctx, storOpts)
				}
				if err != nil {
					markStep(gctx, o.db, deploymentID, stepName, "failed", err)
					return fmt.Errorf("provision storage %s: %w", res.Name, err)
				}
				markStepProviderID(gctx, o.db, deploymentID, stepName, result.ProviderID)
				markStep(gctx, o.db, deploymentID, stepName, "completed", nil)
				resourceMu.Lock()
				storageResults[res.Name] = result
				resourceMu.Unlock()
				progress(4, 12, "Storage bucket ready")
				slog.Info("storage provisioned", "bucket", result.BucketURL)
				return nil
			})
		}

		if res.Type == ResourcePostgres {
			stepName := "provision_" + res.Name
			if isStepCompleted(ctx, o.db, deploymentID, stepName) {
				continue // Crash recovery: skip completed
			}
			if o.database == nil {
				return fmt.Errorf("database provider not configured (set NEON_API_KEY)")
			}
			g.Go(func() error {
				markStep(gctx, o.db, deploymentID, stepName, "running", nil)
				progress(3, 12, "Provisioning database...")
				// Scope resource name to project to prevent cross-tenant collisions.
				// Convention: lk-{projectID prefix}-{resource name}
				scopedName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, res.Name)
				dbOpts := provider.CreateDBOpts{Name: scopedName, Region: res.Region}
				var result *provider.CreateDBResult
				var err error
				if o.rm != nil {
					result, err = o.rm.CreateDatabase(gctx, plan.ProjectID, deploymentID, dbOpts)
				} else {
					result, err = o.database.Create(gctx, dbOpts)
				}
				if err != nil {
					markStep(gctx, o.db, deploymentID, stepName, "failed", err)
					return fmt.Errorf("provision %s: %w", res.Type, err)
				}
				markStepProviderID(gctx, o.db, deploymentID, stepName, result.ProviderID)
				markStep(gctx, o.db, deploymentID, stepName, "completed", nil)
				resourceMu.Lock()
				resourceResults[res.Name] = result
				resourceMu.Unlock()
				progress(4, 12, "Database ready")
				slog.Info("database provisioned", "type", res.Type, "provider_id", result.ProviderID)
				return nil
			})
		}

		if res.Type == ResourceRedis {
			stepName := "provision_" + res.Name
			if isStepCompleted(ctx, o.db, deploymentID, stepName) {
				continue
			}
			if o.cache == nil {
				slog.Warn("cache provider not configured, skipping", "resource", res.Name)
				continue
			}
			g.Go(func() error {
				markStep(gctx, o.db, deploymentID, stepName, "running", nil)
				progress(3, 12, "Provisioning Redis cache...")
				scopedName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, res.Name)
				cacheOpts := provider.CreateRedisOpts{Name: scopedName, Region: res.Region}
				var result *provider.CreateRedisResult
				var err error
				if o.rm != nil {
					result, err = o.rm.CreateCache(gctx, plan.ProjectID, deploymentID, cacheOpts)
				} else {
					result, err = o.cache.Create(gctx, cacheOpts)
				}
				if err != nil {
					markStep(gctx, o.db, deploymentID, stepName, "failed", err)
					return fmt.Errorf("provision redis %s: %w", res.Name, err)
				}
				markStepProviderID(gctx, o.db, deploymentID, stepName, result.ProviderID)
				markStep(gctx, o.db, deploymentID, stepName, "completed", nil)
				resourceMu.Lock()
				redisResults[res.Name] = result
				resourceMu.Unlock()
				progress(4, 12, "Redis cache ready")
				slog.Info("redis provisioned", "endpoint", result.Endpoint)
				return nil
			})
		}
	}

	// Build services via river queue (MaxWorkers: 3 controls concurrency)
	buildJobIDs := make(map[string]int64) // step name → river job ID
	for i := range plan.Services {
		svc := &plan.Services[i]
		stepName := "build_" + svc.Name

		if isStepCompleted(ctx, o.db, deploymentID, stepName) {
			// Crash recovery: read build result from step
			resultJSON := getStepProviderID(ctx, o.db, deploymentID, stepName)
			if resultJSON != "" {
				var br worker.BuildResult
				if json.Unmarshal([]byte(resultJSON), &br) == nil {
					svc.ImageURI = br.ImageURI
					svc.LocalDistPath = br.LocalDistPath
				}
			}
			continue
		}

		buildArgs := envVarsToMap(svc.BuildArgs)

		// Fix #1: Inject VITE_API_URL deterministically — pick the first compute
		// service (in plan.Services order) instead of ranging a map.
		if !IsComputeTarget(svc.Target) {
			for _, candidate := range plan.Services {
				if IsComputeTarget(candidate.Target) {
					if u, ok := backendURLs[candidate.Name]; ok {
						buildArgs["VITE_API_URL"] = u
					}
					break
				}
			}
		}

		imageTag := ""
		distOutputKey := ""
		if IsComputeTarget(svc.Target) {
			// Use the scoped service name (not the raw display name) so that two
			// different tenants with a service both named "api" push to different
			// Artifact Registry image paths.  The scoped name is the same hash
			// used for the Cloud Run service name, keeping naming consistent.
			imageTag = o.imageTagFn(scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name), plan.ID)
		} else if plan.BuildMode != "local" {
			distOutputKey = fmt.Sprintf("builds/%s/%s/dist.tar.gz", plan.ID, svc.Name)
		}

		// Local mode: source on disk; cloud mode: source in GCS/S3.
		localSourcePath := ""
		sourceBucket := ""
		sourceKey := ""
		if plan.BuildMode == "local" {
			localSourcePath = filepath.Join(plan.SourcePath, svc.SourceDir)
		} else {
			sourceBucket = o.buildBucket
			sourceKey = fmt.Sprintf("sources/%s/source.archive", plan.ID)
		}

		// Enqueue build via river (queued in "build" queue, MaxWorkers: 3)
		result, err := o.riverClient.Insert(ctx, worker.BuildJobArgs{
			DeploymentID:    deploymentID,
			ServiceName:     svc.Name,
			UploadID:        plan.ID,
			SourceBucket:    sourceBucket,
			SourceKey:       sourceKey,
			LocalSourcePath: localSourcePath,
			SourceDir:       svc.SourceDir,
			ImageTag:        imageTag,
			DistOutputKey:   distOutputKey,
			BuildArgs:       buildArgs,
			StepName:        stepName,
			ProjectID:       plan.ProjectID,
		}, &river.InsertOpts{Queue: "build"})
		if err != nil {
			return fmt.Errorf("enqueue build for %s: %w", svc.Name, err)
		}
		buildJobIDs[stepName] = result.Job.ID
		progress(5, 12, fmt.Sprintf("Build queued: %s", svc.Name))
	}

	// Create secret request (parallel with provision + build)
	userRequiredKeys := collectUserRequiredVars(plan)
	var secretRequestID string
	secretsAlreadyDone := false

	if len(userRequiredKeys) > 0 {
		stepName := "secrets"
		if isStepCompleted(ctx, o.db, deploymentID, stepName) {
			secretsAlreadyDone = true
			secretRequestID = getStepProviderID(ctx, o.db, deploymentID, stepName)
		} else {
			var err error
			secretRequestID, err = createSecretRequest(ctx, o.db, plan.ProjectID, deploymentID, userRequiredKeys)
			if err != nil {
				return fmt.Errorf("create secret request: %w", err)
			}
			markStep(ctx, o.db, deploymentID, stepName, "running", nil)
			markStepProviderID(ctx, o.db, deploymentID, stepName, secretRequestID)
			formURL := fmt.Sprintf("%s/s/%s", o.baseURL, secretRequestID)
			updateDeploymentStatus(ctx, o.db, deploymentID, "waiting_secrets")
			progress(6, 12, "ACTION_REQUIRED: Fill secrets at "+formURL)
		}
	}

	// Wait for provision goroutines
	if err := g.Wait(); err != nil {
		return err
	}

	// Fix #5: Wait for all build jobs concurrently with errgroup.
	bg, bctx := errgroup.WithContext(ctx)
	for stepName := range buildJobIDs {
		stepName := stepName
		bg.Go(func() error {
			return o.waitForStep(bctx, deploymentID, stepName, 10*time.Minute)
		})
	}
	if err := bg.Wait(); err != nil {
		return err
	}

	// Read build results from deployment_steps
	for i := range plan.Services {
		svc := &plan.Services[i]
		if svc.ImageURI != "" || svc.LocalDistPath != "" {
			continue // Already set from crash recovery
		}
		stepName := "build_" + svc.Name
		resultJSON := getStepProviderID(ctx, o.db, deploymentID, stepName)
		if resultJSON != "" {
			var br worker.BuildResult
			if json.Unmarshal([]byte(resultJSON), &br) == nil {
				svc.ImageURI = br.ImageURI
				svc.LocalDistPath = br.LocalDistPath
			}
		}
	}

	progress(7, 12, "All builds complete")

	// Wait for user secrets if needed
	if len(userRequiredKeys) > 0 && !secretsAlreadyDone {
		keyNames := make([]string, len(userRequiredKeys))
		for i, k := range userRequiredKeys {
			keyNames[i] = k.Name
		}

		if !allSecretsSet(ctx, o.db, plan.ProjectID, keyNames) {
			progress(8, 12, "Builds complete. Waiting for secrets...")
			if err := waitForSecrets(ctx, o.db, plan.ProjectID, keyNames, 1*time.Hour); err != nil {
				markStep(ctx, o.db, deploymentID, "secrets", "failed", err)
				return fmt.Errorf("waiting for secrets: %w", err)
			}
		}
		markStep(ctx, o.db, deploymentID, "secrets", "completed", nil)
		if secretRequestID != "" {
			MarkSecretRequestCompleted(ctx, o.db, secretRequestID)
		}
		progress(9, 12, "All secrets received")
	}

	// Resolve env vars from provisioned resources
	for i, svc := range plan.Services {
		for j, ev := range svc.EnvVars {
			switch ev.Classification {
			case EnvAutoInject:
				resName := strings.TrimPrefix(ev.Source, "provision_")
				resourceMu.Lock()
				dbResult, dbOK := resourceResults[resName]
				storageResult, storageOK := storageResults[resName]
				redisResult, redisOK := redisResults[resName]
				resourceMu.Unlock()
				if dbOK {
					plan.Services[i].EnvVars[j].Value = dbResult.ConnectionURL
				} else if storageOK {
					// For storage, inject the bucket URL by default.
					// Key naming convention: STORAGE_BUCKET → BucketURL, STORAGE_URL → PublicURL
					if strings.HasSuffix(ev.Key, "_URL") || strings.HasSuffix(ev.Key, "_PUBLIC_URL") {
						plan.Services[i].EnvVars[j].Value = storageResult.PublicURL
					} else {
						plan.Services[i].EnvVars[j].Value = storageResult.BucketURL
					}
				} else if redisOK {
					plan.Services[i].EnvVars[j].Value = redisResult.ConnectionURL
				} else {
					slog.Error("auto_inject failed: provisioned resource not found",
						"key", ev.Key, "source", ev.Source, "resource_name", resName)
				}
			case EnvAutoGenerate:
				plan.Services[i].EnvVars[j].Value = generateSecret(ev.Strategy)
			}
		}
	}

	// Resolve user_required env vars from user_secrets table
	if len(userRequiredKeys) > 0 {
		secretValues, err := ResolveUserSecrets(ctx, o.db, plan.ProjectID, o.enc)
		if err != nil {
			return fmt.Errorf("resolve user secrets: %w", err)
		}
		for i, svc := range plan.Services {
			for j, ev := range svc.EnvVars {
				if ev.Classification == EnvUserRequired {
					if val, ok := secretValues[ev.Key]; ok {
						plan.Services[i].EnvVars[j].Value = val
					}
				}
			}
		}
	}

	// Phase 2: Deploy all services concurrently (Fix #6 + #7)
	progress(10, 12, "Deploying services...")
	updateDeploymentStatus(ctx, o.db, deploymentID, "deploying")

	// We need to update plan.Services[i].URL after each deploy; guard that with a mutex.
	var deployMu sync.Mutex

	dg, dctx := errgroup.WithContext(ctx)
	for i := range plan.Services {
		i := i
		svc := plan.Services[i]
		stepName := "deploy_" + svc.Name

		if isStepCompleted(ctx, o.db, deploymentID, stepName) {
			continue
		}

		dg.Go(func() error {
			if IsComputeTarget(svc.Target) {
				markStep(dctx, o.db, deploymentID, stepName, "running", nil)
				progress(11, 12, fmt.Sprintf("Deploying %s...", svc.Name))
				deployOpts := provider.DeployOpts{
					ServiceName:     scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name),
					Region:          o.region,
					ImageURI:        svc.ImageURI,
					EnvVars:         envVarsToMap(svc.EnvVars),
					Port:            svc.Port,
					SessionAffinity: svc.SessionAffinity,
					RequestTimeout:  svc.RequestTimeout,
				}
				var url string
				var err error
				if o.rm != nil {
					url, err = o.rm.DeployCompute(dctx, plan.ProjectID, deployOpts)
				} else {
					url, err = o.compute.Deploy(dctx, deployOpts)
				}
				if err != nil {
					markStep(dctx, o.db, deploymentID, stepName, "failed", err)
					return fmt.Errorf("deploy %s: %w", svc.Name, err)
				}
				markStep(dctx, o.db, deploymentID, stepName, "completed", nil)
				markStepProviderID(dctx, o.db, deploymentID, stepName, url)
				deployMu.Lock()
				plan.Services[i].URL = url
				deployMu.Unlock()
				slog.Info("service deployed", "service", svc.Name, "url", url)
			} else {
				if o.static == nil && o.rm == nil {
					markStep(dctx, o.db, deploymentID, stepName, "failed", fmt.Errorf("static provider not configured"))
					return fmt.Errorf("static provider not configured")
				}
				markStep(dctx, o.db, deploymentID, stepName, "running", nil)
				progress(11, 12, fmt.Sprintf("Deploying %s...", svc.Name))
				projectName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name)

				var createErr error
				if o.rm != nil {
					createErr = o.rm.CreateStaticProject(dctx, plan.ProjectID, deploymentID, projectName)
				} else {
					createErr = o.static.CreateProject(dctx, projectName)
				}
				if createErr != nil {
					markStep(dctx, o.db, deploymentID, stepName, "failed", createErr)
					return fmt.Errorf("create static project %s: %w", svc.Name, createErr)
				}

				// Local mode: dist is already on disk from LocalDocker.Build().
				// Cloud mode: download dist tarball from GCS/S3 into a temp dir.
				var distDir string
				var cleanup func()
				if svc.LocalDistPath != "" {
					distDir = svc.LocalDistPath
					cleanup = func() {} // nothing to clean up — it's the user's source tree
				} else {
					// Fix #7: call cleanup() explicitly instead of defer.
					var err error
					distDir, cleanup, err = o.downloadDist(dctx, plan.ID, svc.Name)
					if err != nil {
						markStep(dctx, o.db, deploymentID, stepName, "failed", err)
						return fmt.Errorf("download dist for %s: %w", svc.Name, err)
					}
				}

				staticOpts := provider.DeployStaticOpts{ProjectName: projectName, DistDir: distDir}
				var url string
				var err error
				if o.rm != nil {
					url, err = o.rm.DeployStatic(dctx, plan.ProjectID, staticOpts)
				} else {
					url, err = o.static.Deploy(dctx, staticOpts)
				}
				cleanup() // explicit, not deferred
				if err != nil {
					markStep(dctx, o.db, deploymentID, stepName, "failed", err)
					return fmt.Errorf("deploy %s: %w", svc.Name, err)
				}
				markStep(dctx, o.db, deploymentID, stepName, "completed", nil)
				markStepProviderID(dctx, o.db, deploymentID, stepName, url)
				deployMu.Lock()
				plan.Services[i].URL = url
				deployMu.Unlock()
				slog.Info("frontend deployed", "service", svc.Name, "url", url)
			}
			return nil
		})
	}

	if err := dg.Wait(); err != nil {
		return err
	}

	progress(12, 12, "All services live")
	return nil
}

// waitForStep polls deployment_steps until a step is completed or failed.
func (o *Orchestrator) waitForStep(ctx context.Context, deploymentID, stepName string, timeout time.Duration) error {
	// Fix #4: use time.NewTimer so the timer is stopped when we return (no goroutine leak).
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timeout waiting for step %s", stepName)
		case <-ticker.C:
			var status string
			var stepErr *string
			err := o.db.QueryRow(ctx,
				"SELECT status, error FROM deployment_steps WHERE deployment_id = $1 AND step = $2",
				deploymentID, stepName,
			).Scan(&status, &stepErr)
			if err != nil {
				continue // Step not yet created
			}
			switch status {
			case "completed":
				return nil
			case "failed":
				msg := "unknown error"
				if stepErr != nil {
					msg = *stepErr
				}
				return fmt.Errorf("step %s failed: %s", stepName, msg)
			}
		}
	}
}

// rollback cleans up partially provisioned resources on failure.
// If ResourceManager is configured, it uses resource_states as the source of truth —
// enqueuing CleanupWorker jobs for any deletions that fail.
// Falls back to legacy deployment_steps scanning when ResourceManager is not set.
func (o *Orchestrator) rollback(ctx context.Context, plan *Plan, deploymentID string) {
	slog.Warn("rolling back failed deployment", "plan_id", plan.ID, "deployment_id", deploymentID)

	if o.rm != nil {
		o.rm.RollbackAll(ctx, plan.ProjectID, deploymentID)
		return
	}

	// Legacy fallback (no ResourceManager configured).
	for _, svc := range plan.Services {
		if IsComputeTarget(svc.Target) {
			serviceName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name)
			if err := o.compute.DeleteService(ctx, serviceName, o.region); err != nil {
				slog.Error("rollback: failed to delete compute service", "service", serviceName, "error", err)
			}
		}
		if !IsComputeTarget(svc.Target) && o.static != nil {
			projectName := scopeResourceNameWithEnv(plan.ProjectID, plan.EnvironmentID, svc.Name)
			if err := o.static.DeleteProject(ctx, projectName); err != nil {
				slog.Error("rollback: failed to delete Pages project", "project", projectName, "error", err)
			}
		}
	}

	rows, _ := o.db.Query(ctx,
		"SELECT step, provider_id FROM deployment_steps WHERE deployment_id = $1 AND step LIKE 'provision_%' AND status = 'completed' AND provider_id IS NOT NULL",
		deploymentID,
	)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var step, providerID string
			rows.Scan(&step, &providerID)
			if strings.HasPrefix(step, "provision_") {
				resName := strings.TrimPrefix(step, "provision_")
				switch {
				case resName == "postgres" && o.database != nil:
					if err := o.database.Delete(ctx, providerID); err != nil {
						slog.Error("rollback: failed to delete database", "provider_id", providerID, "error", err)
					}
				case resName == "redis" && o.cache != nil:
					if err := o.cache.Delete(ctx, providerID); err != nil {
						slog.Error("rollback: failed to delete redis", "provider_id", providerID, "error", err)
					}
				case o.objectStorage != nil:
					if err := o.objectStorage.DeleteBucket(ctx, providerID); err != nil {
						slog.Error("rollback: failed to delete storage bucket", "provider_id", providerID, "error", err)
					}
				}
			}
		}
		if err := rows.Err(); err != nil {
			slog.Error("rollback: error iterating deployment_steps rows", "deployment_id", deploymentID, "error", err)
		}
	}
}

// downloadDist retrieves the built frontend dist from GCS to a temp directory.
func (o *Orchestrator) downloadDist(ctx context.Context, planID, serviceName string) (string, func(), error) {
	objectKey := fmt.Sprintf("builds/%s/%s/dist.tar.gz", planID, serviceName)

	r, err := o.buildStorage.Download(ctx, objectKey)
	if err != nil {
		return "", nil, fmt.Errorf("download dist: %w", err)
	}
	defer r.Close()

	tmpDir, err := os.MkdirTemp("", "lk-dist-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	tarPath := filepath.Join(tmpDir, "dist.tar.gz")
	f, err := os.Create(tarPath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("write dist tarball: %w", err)
	}
	f.Close()

	distDir := filepath.Join(tmpDir, "dist")
	if err := tarutil.ExtractTarGz(tarPath, distDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract dist: %w", err)
	}

	return distDir, cleanup, nil
}

func envVarsToMap(vars []EnvVar) map[string]string {
	m := make(map[string]string, len(vars))
	for _, v := range vars {
		if v.Value != "" {
			m[v.Key] = v.Value
		}
	}
	return m
}

// scopeResourceName generates a deterministic, project-and-environment-scoped
// name for an external resource (Cloud Run service, Neon project, Upstash
// database, GCS bucket, Cloudflare Pages project, etc.).
//
// The user's resource label (e.g. "postgres", "my-db") is only stored in
// LaunchKit's resources table as a display name. The external provider
// receives this scoped ID instead, preventing:
//   - Cross-tenant name collisions (two users both naming their DB "postgres")
//   - Cross-environment clobbering (staging deploy overwriting production)
//   - Name-based enumeration attacks
//
// Format: lk-{sha256(projectID+"/"+envID+"/"+resourceName)[:16]}
// Uses 8 bytes (16 hex chars) = 64-bit collision space, satisfying NIST SP 800-107
// minimum for second-preimage resistance in resource-name contexts.
//
// envID is the environment UUID ("production", "staging", etc.) or empty string
// for legacy single-environment deployments (treated as "production").
//
// Deterministic so that crash-recovery idempotency (Neon's findByName) works:
// same (project, environment, resource) triple always produces the same scoped name.
func scopeResourceName(projectID, resourceName string) string {
	h := sha256.Sum256([]byte(projectID + "/" + resourceName))
	return fmt.Sprintf("lk-%s", hex.EncodeToString(h[:8]))
}

// scopeResourceNameWithEnv is the environment-aware variant of scopeResourceName.
// Use this for all new resource provisioning so that staging and production
// environments of the same project get distinct cloud resource names.
// Falls back to scopeResourceName when envID is empty (legacy / single-env plans).
func scopeResourceNameWithEnv(projectID, envID, resourceName string) string {
	if envID == "" {
		// Legacy path: no environment scoping (single-environment plan).
		return scopeResourceName(projectID, resourceName)
	}
	h := sha256.Sum256([]byte(projectID + "/" + envID + "/" + resourceName))
	return fmt.Sprintf("lk-%s", hex.EncodeToString(h[:8]))
}

// ScopeServiceName is the exported version of scopeResourceName for use by
// packages outside the deploy package (e.g. mcp/tools, server).
//
// Use this to derive the globally-unique cloud service name from a project UUID
// and a human-readable service name.  Never compute the cloud name from the
// project's display name — display names are only unique per team, not globally.
func ScopeServiceName(projectID, serviceName string) string {
	return scopeResourceName(projectID, serviceName)
}

// ScopeServiceNameWithEnv is the environment-aware exported version.
// Prefer this over ScopeServiceName for multi-environment projects so that
// staging and production environments never share a Cloud Run service name.
func ScopeServiceNameWithEnv(projectID, envID, serviceName string) string {
	return scopeResourceNameWithEnv(projectID, envID, serviceName)
}

func generateSecret(strategy string) string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
