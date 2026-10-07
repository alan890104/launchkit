package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/billing"
	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/db"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/mcp"
	"github.com/alan890104/launchkit/server/internal/mcp/tools"
	mw "github.com/alan890104/launchkit/server/internal/middleware"
	"github.com/alan890104/launchkit/server/internal/provider"
	domainsvc "github.com/alan890104/launchkit/server/internal/service/domain"
	projectsvc "github.com/alan890104/launchkit/server/internal/service/project"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Server struct {
	cfg         *config.Config
	pool        *pgxpool.Pool
	srv         *http.Server
	riverClient *river.Client[pgx.Tx] // non-nil when deploy workers are active
	stopWorkers func() error          // stops river workers on shutdown
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Server {
	return &Server{cfg: cfg, pool: pool}
}

func (s *Server) Start(ctx context.Context) error {
	// SECURITY: refuse to start in dev mode if BASE_URL looks like production.
	// Dev mode bypasses auth checks in some code paths; running it against a
	// production database would be a critical vulnerability (VULN-006).
	if s.cfg.IsDev() && s.cfg.BaseURL != "" &&
		!strings.Contains(s.cfg.BaseURL, "localhost") &&
		!strings.Contains(s.cfg.BaseURL, "127.0.0.1") {
		return fmt.Errorf(
			"SECURITY: ENV=development but BASE_URL=%q looks like production — "+
				"refusing to start. Set ENV=production or use a localhost BASE_URL", s.cfg.BaseURL)
	}

	deps := &tools.Deps{
		Config: s.cfg,
		DB:     s.pool,
		Logger: slog.Default(),
	}

	// Secret encryption — optional in dev, required in production.
	// If key is unset, secrets are stored as plaintext (dev mode).
	var enc *deploy.Encryptor
	if s.cfg.SecretEncryptionKey != "" {
		var err error
		enc, err = deploy.NewEncryptor(s.cfg.SecretEncryptionKey)
		if err != nil {
			return fmt.Errorf("init secret encryptor: %w", err)
		}
		slog.Info("secret encryption enabled (AES-256-GCM)")
		deps.Enc = enc
	} else {
		slog.Warn("SECRET_ENCRYPTION_KEY not set — secrets stored as plaintext (dev mode only)")
	}

	// Initialize deploy providers based on configured cloud
	switch s.cfg.Cloud {
	case "gcp":
		if err := s.initGCPDeps(ctx, deps); err != nil {
			slog.Warn("GCP deploy providers not initialized — deploy tools will fail", "error", err)
		}
	case "aws":
		if err := s.initAWSDeps(ctx, deps); err != nil {
			slog.Warn("AWS deploy providers not initialized — deploy tools will fail", "error", err)
		}
	default:
		slog.Info("CLOUD not set to gcp or aws — deploy tools disabled (ping still works)")
	}

	// Cloud-agnostic providers
	if s.cfg.ResendAPIKey != "" {
		deps.Email = provider.NewResend(s.cfg.ResendAPIKey)
		slog.Info("email provider configured", "provider", "resend")
	}

	if s.cfg.NameComUser != "" && s.cfg.NameComToken != "" {
		deps.DomainRegistrar = provider.NewNameCom(s.cfg.NameComUser, s.cfg.NameComToken, s.cfg.NameComSandbox)
		slog.Info("domain registrar configured", "provider", "namecom", "sandbox", s.cfg.NameComSandbox)
	}

	// MCP server + Streamable HTTP transport
	mcpSrv := mcp.NewServer(deps)
	deps.MCPServer = mcpSrv // back-reference for progress notifications

	streamHandler := mcpserver.NewStreamableHTTPServer(mcpSrv,
		mcpserver.WithHeartbeatInterval(30*time.Second),
	)

	// Route registration
	mux := http.NewServeMux()

	// MCP endpoint
	mux.Handle("POST /mcp", streamHandler)
	mux.Handle("GET /mcp", streamHandler)
	mux.Handle("DELETE /mcp", streamHandler)

	// Landing page
	mux.HandleFunc("GET /", serveLanding)

	// Web dashboard — API key management UI
	dash := &webDashboardHandler{db: s.pool, baseURL: s.cfg.BaseURL}
	mux.HandleFunc("GET /dashboard", dash.serveGet)
	mux.HandleFunc("POST /dashboard/keys", dash.serveCreateKey)
	mux.HandleFunc("POST /dashboard/keys/{id}/revoke", dash.serveRevokeKey)

	// JSON API — API key management (for programmatic access)
	keyHandler := &apiKeyHandler{db: s.pool}
	mux.HandleFunc("POST /auth/keys", keyHandler.Create)
	mux.HandleFunc("GET /auth/keys", keyHandler.List)
	mux.HandleFunc("DELETE /auth/keys/{id}", keyHandler.Revoke)
	mux.HandleFunc("POST /auth/keys/{id}/rotate", keyHandler.Rotate)

	// JSON API — projects + deployments (for Next.js dashboard)
	projectSvc := projectsvc.New(s.pool, deps.Compute, s.cfg)
	projHandler := &apiProjectsHandler{db: s.pool, riverClient: s.riverClient, projectSvc: projectSvc}
	mux.HandleFunc("GET /api/projects", projHandler.listProjects)
	mux.HandleFunc("POST /api/projects", projHandler.createProject)
	mux.HandleFunc("GET /api/projects/{id}/deployments", projHandler.listDeployments)
	mux.HandleFunc("POST /api/projects/{id}/scale", projHandler.scaleService)
	mux.HandleFunc("POST /api/projects/{id}/restart", projHandler.restartService)
	mux.HandleFunc("POST /api/projects/{id}/rollback", projHandler.rollbackService)
	mux.HandleFunc("POST /api/projects/{id}/redeploy", projHandler.redeploy)
	mux.HandleFunc("DELETE /api/projects/{id}", projHandler.deleteProject)
	mux.HandleFunc("GET /api/deployments/{id}", projHandler.getDeployment)

	billingAPI := &apiBillingHandler{db: s.pool}
	mux.HandleFunc("GET /api/billing/summary", billingAPI.summary)

	// Secrets API
	secretsAPI := &apiSecretsHandler{db: s.pool, enc: enc}
	mux.HandleFunc("GET /api/projects/{id}/secrets", secretsAPI.list)
	mux.HandleFunc("PUT /api/projects/{id}/secrets/{key}", secretsAPI.set)
	mux.HandleFunc("DELETE /api/projects/{id}/secrets/{key}", secretsAPI.delete)

	// Domains API
	domainService := domainsvc.NewService(s.cfg, s.pool, slog.Default(), deps.Compute, deps.DomainRegistrar)
	domainsAPI := &apiDomainsHandler{db: s.pool, service: domainService}
	mux.HandleFunc("GET /api/projects/{id}/domains", domainsAPI.list)
	mux.HandleFunc("GET /api/domains/search", domainsAPI.search)
	mux.HandleFunc("POST /api/projects/{id}/domains/register", domainsAPI.register)
	mux.HandleFunc("POST /api/projects/{id}/domains/attach", domainsAPI.attach)
	mux.HandleFunc("GET /api/projects/{id}/dns", domainsAPI.listDNS)
	mux.HandleFunc("POST /api/projects/{id}/dns", domainsAPI.createDNS)
	mux.HandleFunc("DELETE /api/projects/{id}/dns", domainsAPI.deleteDNS)

	// GitHub connection API
	githubAPI := &apiGitHubHandler{db: s.pool}
	mux.HandleFunc("GET /api/projects/{id}/github", githubAPI.list)
	mux.HandleFunc("DELETE /api/projects/{id}/github/{connectionId}", githubAPI.disconnect)

	// Live log streaming
	logsAPI := &apiLogsHandler{db: s.pool}
	mux.HandleFunc("GET /api/deployments/{id}/logs/stream", logsAPI.stream)

	// JSON API — metrics + health (deps.Compute set by initGCPDeps/initAWSDeps above)
	mh := &metricsHandler{db: s.pool, compute: deps.Compute, region: s.cfg.Region()}
	mux.HandleFunc("GET /api/projects/{id}/health", mh.health)
	mux.HandleFunc("GET /api/projects/{id}/metrics", mh.metrics)
	mux.HandleFunc("GET /api/projects/{id}/errors/recent", mh.recentErrors)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Secret web form (no auth — accessed by link from Claude)
	secretsH := newSecretsHandler(s.pool, enc)
	mux.HandleFunc("GET /s/{secretRequestID}", secretsH.HandleGet)
	mux.HandleFunc("POST /s/{secretRequestID}", secretsH.HandlePost)

	// GitHub push-to-deploy webhook (auth-free, uses per-connection HMAC verification)
	mux.Handle("POST /webhooks/github", &githubWebhookHandler{
		db:          s.pool,
		buildStore:  deps.BuildStorage,
		planEngine:  deps.PlanEngine,
		riverClient: deps.RiverClient,
		githubToken: s.cfg.GitHubToken,
		baseURL:     s.cfg.BaseURL,
		isLocal:     s.cfg.IsLocalBuild(),
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(r.Context(), s.pool); err != nil {
			slog.Error("readyz: database check failed", "error", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "unavailable", "error": "database check failed"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})

	// Global middleware: outermost → innermost
	// 1. CORS (always outermost so panics + auth errors carry headers)
	// 2. Recovery (catch panics before CORS writes headers)
	// 3. RequestID (assign early for tracing)
	// 4. Logging (log with trace_id)
	// 5. Auth (verify JWT, inject user_id via context — skip for healthz/readyz/secrets)
	corsMiddleware := mw.NewCORS(s.cfg.AllowedOrigins)
	handler := mw.Chain(mux, corsMiddleware, mw.Recovery, mw.RequestID, mw.Logging)

	// API key cache (Upstash REST) — optional distributed cache for API key validation.
	// When configured, multiple server instances share a single cache to avoid hitting
	// the DB on every authenticated request.
	apiCache := provider.NewAPICache(s.cfg.LaunchKitRedisURL, s.cfg.LaunchKitRedisToken)
	if apiCache.Enabled() {
		slog.Info("API key cache enabled (Upstash REST)")
	} else {
		slog.Info("API key cache disabled (no LAUNCHKIT_REDIS_URL set)")
	}

	// Auth middleware — always active, supports three paths:
	//   1. LaunchKit API key (lk_xxx) — for Claude Desktop / Claude Code
	//   2. Firebase ID token — for web dashboard
	//   3. LAUNCHKIT_DEV_KEY env var — dev shortcut (no DB lookup)
	authMiddleware := &auth.AuthMiddleware{
		DB:                s.pool,
		FirebaseProjectID: s.cfg.FirebaseProjectID,
		SkipPrefixes:      []string{"/healthz", "/readyz", "/s/", "/auth/session", "/webhooks/"},
		IsDev:             s.cfg.IsDev(),
		DevKey:            s.cfg.DevKey,
		Cache:             apiCache,
	}

	if s.cfg.FirebaseProjectID != "" {
		firebaseKeys, err := auth.NewFirebaseKeys(ctx)
		if err != nil {
			return fmt.Errorf("init Firebase keys: %w", err)
		}
		authMiddleware.FirebaseKeys = firebaseKeys
		slog.Info("auth: Firebase enabled", "project", s.cfg.FirebaseProjectID)

		// Register session exchange endpoint (Firebase ID token → user info)
		mux.HandleFunc("POST /auth/session", auth.ExchangeHandler(s.pool, firebaseKeys, s.cfg.FirebaseProjectID))
	} else {
		slog.Warn("auth: FIREBASE_PROJECT_ID not set — Firebase auth disabled (API keys still work)")
	}

	if s.cfg.DevKey != "" {
		slog.Info("auth: LAUNCHKIT_DEV_KEY set — dev shortcut active")
	}

	// User provisioning middleware — runs after auth, creates user+team on first contact
	provisionMiddleware := provisionMiddlewareFor(s.pool)

	handler = mw.Chain(mux, corsMiddleware, mw.Recovery, mw.RequestID, mw.Logging, authMiddleware.Handler, provisionMiddleware)

	s.srv = &http.Server{
		Addr:         ":" + s.cfg.Port,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 3600 * time.Second, // long for MCP SSE streaming
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("launchkit api starting", "port", s.cfg.Port, "env", s.cfg.Env)
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		return fmt.Errorf("server exited: %w", err)
	}
}

// initGCPDeps initializes GCP cloud providers, river workers, and deploy engine/orchestrator.
func (s *Server) initGCPDeps(ctx context.Context, deps *tools.Deps) error {
	// Build storage (GCS) — only required in cloud build mode.
	var gcs provider.BuildStorage
	if !s.cfg.IsLocalBuild() {
		g, err := provider.NewGCS(ctx, s.cfg.GCSBuildBucket)
		if err != nil {
			return fmt.Errorf("init GCS: %w", err)
		}
		gcs = g
		deps.BuildStorage = gcs
	}

	// Builder selection: local Docker socket (open-source tier) or remote BuildKit VM / Cloud Build.
	var builder provider.Builder
	if s.cfg.IsLocalBuild() {
		builder = provider.NewLocalDocker(s.cfg.DockerSocketAddr)
		slog.Info("using local Docker builder", "socket", s.cfg.DockerSocketAddr)
	} else {
		// Cloud Build (fallback, or primary when BuildKit VM is not configured)
		cloudBuild, err := provider.NewCloudBuild(ctx, s.cfg.GCPProjectID, s.cfg.GCPRegion, s.cfg.CloudBuildTimeout)
		if err != nil {
			return fmt.Errorf("init Cloud Build: %w", err)
		}

		// BuildKit VM (primary builder with gVisor isolation, falls back to Cloud Build)
		if s.cfg.BuildKitAddr != "" {
			bk, err := provider.NewBuildKit(ctx, provider.BuildKitConfig{
				Addr:          s.cfg.BuildKitAddr,
				ProjectID:     s.cfg.GCPProjectID,
				Zone:          s.cfg.BuildKitZone,
				Instance:      s.cfg.BuildKitInstance,
				Fallback:      cloudBuild,
				BuildStorage:  gcs,
				StartTimeout:  s.cfg.BuildKitStartTimeout,
				IdleTimeout:   s.cfg.BuildKitIdleTimeout,
				CheckInterval: s.cfg.BuildKitCheckInterval,
			})
			if err != nil {
				slog.Warn("BuildKit provider init failed, using Cloud Build only", "error", err)
				builder = cloudBuild
			} else {
				builder = bk
				slog.Info("BuildKit VM configured as primary builder",
					"addr", s.cfg.BuildKitAddr,
					"instance", s.cfg.BuildKitInstance,
					"isolation", "gVisor (runsc) + CNI network",
				)
			}
		} else {
			builder = cloudBuild
			slog.Info("BUILDKIT_ADDR not set — using Cloud Build only")
		}
	}

	// Cloud Run + Cloud Logging
	compute, err := provider.NewCloudRun(ctx, s.cfg.GCPProjectID)
	if err != nil {
		return fmt.Errorf("init Cloud Run: %w", err)
	}
	deps.Compute = compute

	// Neon (managed Postgres)
	var database provider.Database
	if s.cfg.NeonAPIKey != "" {
		database = provider.NewNeon(s.cfg.NeonAPIKey)
	}

	// Upstash Redis (cloud-agnostic)
	var cache provider.Cache
	if s.cfg.UpstashAPIKey != "" {
		cache = provider.NewUpstash(s.cfg.UpstashEmail, s.cfg.UpstashAPIKey)
		slog.Info("cache provider configured", "provider", "upstash")
	}

	// Cloudflare Pages
	var static provider.Static
	if s.cfg.CloudflareAccountID != "" && s.cfg.CloudflareAPIToken != "" {
		static = provider.NewCloudflarePages(s.cfg.CloudflareAccountID, s.cfg.CloudflareAPIToken)
	}

	// Plan Engine
	engine := deploy.NewEngine(s.pool, gcs, s.cfg.GCPRegion, deploy.CloudGCP)
	deps.PlanEngine = engine

	// Orchestrator (uses river for build jobs)
	registryRepo := s.cfg.ArtifactRegistryRepo

	// ── Billing: consumption fetchers + dynamic pricing ──
	var fetchers []provider.ConsumptionFetcher
	fetchers = append(fetchers, provider.NewCloudRunFetcher(s.cfg.GCPProjectID, s.cfg.GCPRegion))
	if s.cfg.NeonAPIKey != "" {
		fetchers = append(fetchers, provider.NewNeonFetcher(s.cfg.NeonAPIKey))
	}
	if s.cfg.UpstashAPIKey != "" {
		fetchers = append(fetchers, provider.NewUpstashFetcher(s.cfg.UpstashEmail, s.cfg.UpstashAPIKey, s.pool))
	}
	fetchers = append(fetchers, provider.NewGCSStorageFetcher(s.cfg.GCPProjectID))
	fetchers = append(fetchers, provider.NewARFetcher(s.cfg.GCPProjectID))

	pricing := billing.NewPricingTable()
	pricing.StartRefreshLoop(ctx)

	// River job queue (build concurrency control: MaxWorkers=3)
	// ExecuteDeployment is a closure so worker doesn't need to import deploy (cycle prevention).
	riverClient, stopWorkers := worker.Setup(ctx, worker.Config{
		DB:                  s.pool,
		Builder:             builder,
		Compute:             compute,
		Database:            database,
		Cache:               cache,
		Static:              static,
		BuildWorkerTimeout:  s.cfg.DeployWorkerTimeout,
		ConsumptionFetchers: fetchers,
		Pricing:             pricing,
		DomainRegistrar:     deps.DomainRegistrar,
		ExecuteDeployment: func(ctx context.Context, planID, deploymentID string) error {
			plan, err := engine.GetPlan(ctx, planID)
			if err != nil {
				return fmt.Errorf("get plan %s: %w", planID, err)
			}
			return deps.Orchestrator.Execute(ctx, plan, deploymentID, func(step, total int, msg string) {
				slog.Info("deploy worker progress", "deployment_id", deploymentID, "step", step, "total", total, "msg", msg)
			})
		},
	})
	s.riverClient = riverClient
	s.stopWorkers = stopWorkers
	deps.RiverClient = riverClient

	// Crash recovery: re-enqueue stale deployments stuck in non-terminal state
	if err := worker.RecoverStaleDeployments(ctx, s.pool, riverClient); err != nil {
		slog.Error("crash recovery failed", "error", err)
		// Non-fatal: recovery failure shouldn't prevent server startup
	}

	// ResourceManager: Pulumi-inspired state tracking for all cloud resources.
	// Must be created after river client is ready (it enqueues cleanup jobs on delete failures).
	rm := deploy.NewResourceManager(deploy.ResourceManagerConfig{
		DB:                  s.pool,
		Compute:             compute,
		Database:            database,
		Cache:               cache,
		Static:              static,
		RiverClient:         riverClient,
		ComputeProviderName: "cloud_run",
		StorageProviderName: "gcs",
	})

	deps.Orchestrator = deploy.NewOrchestrator(deploy.OrchestratorConfig{
		Engine:          engine,
		DB:              s.pool,
		BuildStorage:    gcs,
		Compute:         compute,
		Database:        database,
		Static:          static,
		Cache:           cache,
		RiverClient:     riverClient,
		Region:          s.cfg.GCPRegion,
		BuildBucket:     s.cfg.GCSBuildBucket,
		BaseURL:         s.cfg.BaseURL,
		Enc:             deps.Enc,
		ResourceManager: rm,
		ImageTagFn: func(serviceName, planID string) string {
			return fmt.Sprintf("%s/%s:%s", registryRepo, serviceName, planID)
		},
	})

	slog.Info("deploy providers initialized",
		"cloud", "gcp",
		"project", s.cfg.GCPProjectID,
		"region", s.cfg.GCPRegion,
		"bucket", s.cfg.GCSBuildBucket,
	)
	return nil
}

// initAWSDeps initializes AWS cloud providers, river workers, and deploy engine/orchestrator.
func (s *Server) initAWSDeps(ctx context.Context, deps *tools.Deps) error {
	ecrRepoBase := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s",
		s.cfg.AWSAccountID, s.cfg.AWSRegion, s.cfg.ECRRepositoryBase)

	// Build storage (S3) — only required in cloud build mode.
	var s3Store provider.BuildStorage
	if !s.cfg.IsLocalBuild() {
		s, err := provider.NewS3(ctx, s.cfg.S3BuildBucket, s.cfg.AWSRegion)
		if err != nil {
			return fmt.Errorf("init S3: %w", err)
		}
		s3Store = s
		deps.BuildStorage = s3Store
	}

	// Builder selection: local Docker socket or remote BuildKit EC2 / CodeBuild.
	var builder provider.Builder
	if s.cfg.IsLocalBuild() {
		builder = provider.NewLocalDocker(s.cfg.DockerSocketAddr)
		slog.Info("using local Docker builder", "socket", s.cfg.DockerSocketAddr)
	} else {
		// AWS CodeBuild (fallback, or primary when BuildKit EC2 is not configured)
		codeBuild, err := provider.NewAWSCodeBuild(ctx, s.cfg.AWSRegion, ecrRepoBase)
		if err != nil {
			return fmt.Errorf("init AWS CodeBuild: %w", err)
		}

		// BuildKit on EC2 (primary builder, falls back to CodeBuild)
		if s.cfg.AWSBuildKitAddr != "" {
			bk, err := provider.NewBuildKitEC2(ctx, provider.BuildKitEC2Config{
				Addr:         s.cfg.AWSBuildKitAddr,
				InstanceID:   s.cfg.AWSBuildKitInstanceID,
				Region:       s.cfg.AWSRegion,
				Fallback:     codeBuild,
				BuildStorage: s3Store,
				StartTimeout: s.cfg.BuildKitStartTimeout,
				IdleTimeout:  s.cfg.BuildKitIdleTimeout,
			})
			if err != nil {
				slog.Warn("EC2 BuildKit provider init failed, using CodeBuild only", "error", err)
				builder = codeBuild
			} else {
				builder = bk
				slog.Info("EC2 BuildKit VM configured as primary builder",
					"addr", s.cfg.AWSBuildKitAddr,
					"instance", s.cfg.AWSBuildKitInstanceID,
				)
			}
		} else {
			builder = codeBuild
			slog.Info("AWS_BUILDKIT_ADDR not set — using CodeBuild only")
		}
	}

	// ECS Fargate for compute
	subnets := strings.Split(s.cfg.AWSECSSubnets, ",")
	var securityGroups []string
	if s.cfg.AWSECSSecurityGroups != "" {
		securityGroups = strings.Split(s.cfg.AWSECSSecurityGroups, ",")
	}

	compute, err := provider.NewECSFargate(ctx, provider.ECSFargateConfig{
		Region:           s.cfg.AWSRegion,
		ClusterName:      s.cfg.AWSECSCluster,
		SubnetIDs:        subnets,
		SecurityGroupIDs: securityGroups,
		ExecutionRoleARN: s.cfg.AWSECSExecutionRole,
		TaskRoleARN:      s.cfg.AWSECSTaskRole,
		ALBListenerARN:   s.cfg.AWSALBListenerARN,
		StabilizeTimeout: s.cfg.ECSStabilizeTimeout,
	})
	if err != nil {
		return fmt.Errorf("init ECS Fargate: %w", err)
	}
	deps.Compute = compute

	// Neon (managed Postgres — cloud-agnostic, same as GCP)
	var database provider.Database
	if s.cfg.NeonAPIKey != "" {
		database = provider.NewNeon(s.cfg.NeonAPIKey)
	}

	// Upstash Redis (cloud-agnostic)
	var awsCache provider.Cache
	if s.cfg.UpstashAPIKey != "" {
		awsCache = provider.NewUpstash(s.cfg.UpstashEmail, s.cfg.UpstashAPIKey)
	}

	// Cloudflare Pages (static frontends — cloud-agnostic, same as GCP)
	var static provider.Static
	if s.cfg.CloudflareAccountID != "" && s.cfg.CloudflareAPIToken != "" {
		static = provider.NewCloudflarePages(s.cfg.CloudflareAccountID, s.cfg.CloudflareAPIToken)
	}

	// Plan Engine
	engine := deploy.NewEngine(s.pool, s3Store, s.cfg.AWSRegion, deploy.CloudAWS)
	deps.PlanEngine = engine

	// ── Billing: consumption fetchers + dynamic pricing ──
	var fetchers []provider.ConsumptionFetcher
	if ecsFetcher, ecsFetchErr := provider.NewECSFargateFetcher(ctx, s.cfg.AWSRegion, s.cfg.AWSECSCluster); ecsFetchErr != nil {
		slog.Warn("failed to init ECS Fargate fetcher", "error", ecsFetchErr)
	} else {
		fetchers = append(fetchers, ecsFetcher)
	}
	if s.cfg.NeonAPIKey != "" {
		fetchers = append(fetchers, provider.NewNeonFetcher(s.cfg.NeonAPIKey))
	}
	if s.cfg.UpstashAPIKey != "" {
		fetchers = append(fetchers, provider.NewUpstashFetcher(s.cfg.UpstashEmail, s.cfg.UpstashAPIKey, s.pool))
	}
	fetchers = append(fetchers, provider.NewS3StorageFetcher(s.cfg.AWSRegion))

	pricing := billing.NewPricingTable()
	pricing.StartRefreshLoop(ctx)

	// River job queue (same concurrency control as GCP)
	riverClient, stopWorkers := worker.Setup(ctx, worker.Config{
		DB:                  s.pool,
		Builder:             builder,
		Compute:             compute,
		Database:            database,
		Cache:               awsCache,
		Static:              static,
		BuildWorkerTimeout:  s.cfg.DeployWorkerTimeout,
		ConsumptionFetchers: fetchers,
		Pricing:             pricing,
		DomainRegistrar:     deps.DomainRegistrar,
		ExecuteDeployment: func(ctx context.Context, planID, deploymentID string) error {
			plan, err := engine.GetPlan(ctx, planID)
			if err != nil {
				return fmt.Errorf("get plan %s: %w", planID, err)
			}
			return deps.Orchestrator.Execute(ctx, plan, deploymentID, func(step, total int, msg string) {
				slog.Info("deploy worker progress", "deployment_id", deploymentID, "step", step, "total", total, "msg", msg)
			})
		},
	})
	s.riverClient = riverClient
	s.stopWorkers = stopWorkers
	deps.RiverClient = riverClient

	// Crash recovery
	if err := worker.RecoverStaleDeployments(ctx, s.pool, riverClient); err != nil {
		slog.Error("crash recovery failed", "error", err)
	}

	// ResourceManager: Pulumi-inspired state tracking for all cloud resources.
	rm := deploy.NewResourceManager(deploy.ResourceManagerConfig{
		DB:                  s.pool,
		Compute:             compute,
		Database:            database,
		Cache:               awsCache,
		Static:              static,
		RiverClient:         riverClient,
		ComputeProviderName: "ecs_fargate",
		StorageProviderName: "s3",
	})

	// Orchestrator
	deps.Orchestrator = deploy.NewOrchestrator(deploy.OrchestratorConfig{
		Engine:          engine,
		DB:              s.pool,
		BuildStorage:    s3Store,
		Compute:         compute,
		Database:        database,
		Static:          static,
		Cache:           awsCache,
		RiverClient:     riverClient,
		Region:          s.cfg.AWSRegion,
		BuildBucket:     s.cfg.S3BuildBucket,
		BaseURL:         s.cfg.BaseURL,
		Enc:             deps.Enc,
		ResourceManager: rm,
		ImageTagFn: func(serviceName, planID string) string {
			return fmt.Sprintf("%s/%s:%s", ecrRepoBase, serviceName, planID)
		},
	})

	slog.Info("deploy providers initialized",
		"cloud", "aws",
		"region", s.cfg.AWSRegion,
		"bucket", s.cfg.S3BuildBucket,
		"ecr", ecrRepoBase,
	)
	return nil
}

func (s *Server) shutdown() error {
	slog.Info("shutting down gracefully")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop River workers before closing the HTTP server so in-flight jobs can
	// complete (or be returned to the queue) within the shutdown window.
	if s.stopWorkers != nil {
		if err := s.stopWorkers(); err != nil {
			slog.Warn("river workers did not stop cleanly", "error", err)
		}
	} else if s.riverClient != nil {
		if err := s.riverClient.Stop(ctx); err != nil {
			slog.Warn("river client did not stop cleanly", "error", err)
		}
	}

	return s.srv.Shutdown(ctx)
}
