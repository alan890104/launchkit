package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/db"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// E2E deploy poc-counter backend to Cloud Run.
//
// Usage:
//   cd server && set -a && source .env && set +a && go run ./cmd/e2e-deploy
//
// This script:
//   1. Loads .env via config.Load()
//   2. Creates DB pool + starts river workers
//   3. Generates presigned upload URL via Engine.Generate()
//   4. Uploads backend source code as source.archive to GCS
//   5. Calls Orchestrator.Execute() (build → deploy)
//   6. Prints service URL(s)

func main() {
	// Structured JSON logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "cloud", cfg.Cloud, "env", cfg.Env, "project", cfg.GCPProjectID)

	// 1. Database
	slog.Info("connecting to database")
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	slog.Info("running migrations")
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// 2. GCS (build storage)
	slog.Info("initializing GCS", "bucket", cfg.GCSBuildBucket)
	gcs, err := provider.NewGCS(ctx, cfg.GCSBuildBucket)
	if err != nil {
		slog.Error("init GCS failed", "error", err)
		os.Exit(1)
	}
	_ = gcs

	// 3. Cloud Build (fallback)
	slog.Info("initializing Cloud Build", "project", cfg.GCPProjectID, "region", cfg.GCPRegion)
	cloudBuild, err := provider.NewCloudBuild(ctx, cfg.GCPProjectID, cfg.GCPRegion, cfg.CloudBuildTimeout)
	if err != nil {
		slog.Error("init Cloud Build failed", "error", err)
		os.Exit(1)
	}

	// 4. BuildKit VM (primary builder) or fallback to Cloud Build
	var builder provider.Builder
	if cfg.BuildKitAddr != "" {
		bk, err := provider.NewBuildKit(ctx, provider.BuildKitConfig{
			Addr:         cfg.BuildKitAddr,
			ProjectID:    cfg.GCPProjectID,
			Zone:         cfg.BuildKitZone,
			Instance:     cfg.BuildKitInstance,
			Fallback:     cloudBuild,
			BuildStorage: gcs,
		})
		if err != nil {
			slog.Warn("BuildKit init failed, falling back to Cloud Build", "error", err)
			builder = cloudBuild
		} else {
			builder = bk
			slog.Info("BuildKit VM configured as primary builder", "addr", cfg.BuildKitAddr)
		}
	} else {
		builder = cloudBuild
		slog.Info("BUILDKIT_ADDR not set, using Cloud Build only")
	}

	// 5. Cloud Run (compute provider)
	slog.Info("initializing Cloud Run")
	cloudRun, err := provider.NewCloudRun(ctx, cfg.GCPProjectID)
	if err != nil {
		slog.Error("init Cloud Run failed", "error", err)
		os.Exit(1)
	}

	// 6. Neon (optional)
	var database provider.Database
	if cfg.NeonAPIKey != "" {
		database = provider.NewNeon(cfg.NeonAPIKey)
	}

	// 7. Cloudflare Pages (optional, for frontends)
	var static provider.Static
	if cfg.CloudflareAccountID != "" && cfg.CloudflareAPIToken != "" {
		static = provider.NewCloudflarePages(cfg.CloudflareAccountID, cfg.CloudflareAPIToken)
	}

	// 8. River workers (need these for build jobs)
	slog.Info("starting river workers")
	riverClient, stopWorkers := worker.Setup(ctx, worker.Config{
		DB:      pool,
		Builder: builder,
	})
	defer func() {
		if err := stopWorkers(); err != nil {
			slog.Warn("workers didn't stop cleanly", "error", err)
		}
	}()

	// 9. Engine + Orchestrator
	engine := deploy.NewEngine(pool, gcs, cfg.GCPRegion, deploy.CloudGCP)

	registryRepo := fmt.Sprintf("us-east4-docker.pkg.dev/%s/user-images", cfg.GCPProjectID)
	orchestrator := deploy.NewOrchestrator(deploy.OrchestratorConfig{
		Engine:       engine,
		DB:           pool,
		BuildStorage: gcs,
		Compute:      cloudRun,
		Database:     database,
		Static:       static,
		RiverClient:  riverClient,
		Region:       cfg.GCPRegion,
		BuildBucket:  cfg.GCSBuildBucket,
		BaseURL:      cfg.BaseURL,
		ImageTagFn: func(serviceName, planID string) string {
			return fmt.Sprintf("%s/%s:%s", registryRepo, serviceName, planID)
		},
	})

	// 10. Seed test data + Generate Plan with Hints
	projectUUID, err := seedTestData(ctx, pool)
	if err != nil {
		slog.Error("failed to seed test data", "error", err)
		os.Exit(1)
	}

	hints := deploy.Hints{
		ProjectName: "poc-counter",
		ProjectID:   projectUUID,
		Cloud:       "gcp",
		Services: []deploy.ServiceHint{
			{
				Name:      "counter",
				Type:      "backend",
				Framework: "fastapi",
				SourceDir: "poc-counter",
				Port:      8080,
			},
		},
		Resources: []deploy.ResourceHint{
			{
				Name:   "postgres",
				Type:   "postgres",
				Reason: "Counter data persistence",
			},
		},
		EnvHints: []deploy.EnvHint{
			{
				Key:            "DATABASE_URL",
				Service:        "counter",
				Classification: "auto_inject",
				InjectFrom:     "postgres",
				Description:    "PostgreSQL connection string from Neon",
			},
		},
	}

	slog.Info("generating plan", "project", hints.ProjectName)
	plan, err := engine.Generate(ctx, hints)
	if err != nil {
		slog.Error("plan generation failed", "error", err)
		os.Exit(1)
	}
	slog.Info("plan created", "plan_id", plan.ID, "upload_url", plan.UploadURL != "")

	// 11. Upload source archive
	backendDir := "../poc/poc-counter"
	absPath, err := filepath.Abs(backendDir)
	if err != nil {
		slog.Error("failed to resolve backend path", "error", err)
		os.Exit(1)
	}
	slog.Info("backend directory", "path", absPath)
	archivePath, err := createSourceArchive(absPath)
	if err != nil {
		slog.Error("failed to create source archive", "error", err)
		os.Exit(1)
	}
	slog.Info("source archive created", "path", archivePath)

	// Upload to GCS
	sourceKey := fmt.Sprintf("sources/%s/source.archive", plan.ID)
	f, err := os.Open(archivePath)
	if err != nil {
		slog.Error("failed to open archive", "error", err)
		os.Exit(1)
	}
	defer f.Close()

	err = gcs.Upload(ctx, sourceKey, f)
	if err != nil {
		slog.Error("failed to upload to GCS", "error", err)
		os.Exit(1)
	}
	slog.Info("source uploaded to GCS", "bucket", cfg.GCSBuildBucket, "key", sourceKey)
	_ = os.Remove(archivePath)

	// 12. Create deployment record
	deploymentID := uuid.NewString()
	_, err = pool.Exec(ctx,
		`INSERT INTO deployments (id, service_id, status, plan_id, trigger, started_at)
		VALUES ($1, NULL, $2, $3, $4, NOW())`,
		deploymentID, "pending", plan.ID, "deploy",
	)
	if err != nil {
		slog.Error("failed to create deployment record", "error", err)
		os.Exit(1)
	}
	slog.Info("deployment created", "deployment_id", deploymentID)

	// 13. Execute the full deploy
	slog.Info("executing deployment pipeline", "deployment_id", deploymentID)

	progress := func(progress int, total int, message string) {
		slog.Info("deploy progress", "step", fmt.Sprintf("%d/%d", progress, total), "message", message)
	}

	err = orchestrator.Execute(ctx, plan, deploymentID, progress)
	if err != nil {
		slog.Error("deployment failed", "error", err)

		// Update failure status
		_, _ = pool.Exec(ctx,
			`UPDATE deployments SET status = 'failed', error = $1, finished_at = NOW() WHERE id = $2`,
			err.Error(), deploymentID,
		)
		os.Exit(1)
	}

	// 14. Get final service URLs
	var urls []map[string]string
	rows, err := pool.Query(ctx,
		`SELECT ds.service_name, ds.step_data->>'url' as url
		FROM deployment_steps ds
		WHERE ds.deployment_id = $1 AND ds.step_name LIKE 'deploy_%' AND ds.status = 'completed'`,
		deploymentID,
	)
	if err != nil {
		slog.Error("failed to query service URLs", "error", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var name, url string
			if err := rows.Scan(&name, &url); err == nil {
				urls = append(urls, map[string]string{"name": name, "url": url})
			}
		}
	}

	slog.Info("✅ E2E deployment completed successfully!", "deployment_id", deploymentID)
	for _, u := range urls {
		slog.Info("service live", "name", u["name"], "url", u["url"])
		fmt.Printf("\n🌐 %s: %s\n", u["name"], u["url"])
	}

	// Also output JSON for scripts
	output, _ := json.Marshal(map[string]any{
		"deployment_id": deploymentID,
		"plan_id":       plan.ID,
		"services":      urls,
	})
	fmt.Printf("\n%s\n", output)
}

// createSourceArchive creates a tar.gz file of the source directory and returns its path.
//
// The archive preserves the directory name as a prefix:
//
//	srcDir = /path/to/poc-counter/backend
//	-> archive contains: backend/main.py, backend/requirements.txt, ...
//
// This matches the `source_dir` field in the deploy Plan.
func createSourceArchive(srcDir string) (string, error) {
	srcDir, err := filepath.Abs(srcDir)
	if err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp("", "source-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	prefix := filepath.Base(srcDir)
	parentDir := filepath.Dir(srcDir)

	err = createTarWithPrefix(parentDir, prefix, tmpFile)
	if err != nil {
		return "", err
	}
	return tmpFile.Name(), nil
}

// createTarWithPrefix creates a gzipped tar archive rooted at parentDir,
// with all paths prefixed by the given prefix.
func createTarWithPrefix(parentDir, prefix string, w *os.File) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	srcFS := os.DirFS(parentDir)
	walkErr := fs.WalkDir(srcFS, prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(p)

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !d.IsDir() {
			f, err := srcFS.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// seedTestData creates a test user, team, and project if they don't already exist.
// Returns the project UUID for use in plan generation.
func seedTestData(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO users (id, email, name)
		VALUES ('e2e-test-user', 'e2e@launchkit.dev', 'E2E Test User')
		ON CONFLICT (id) DO NOTHING
	`)

	var teamID string
	err := pool.QueryRow(ctx, `
		INSERT INTO teams (id, name, owner_id)
		VALUES ('e2e-test-team', 'E2E Test Team', 'e2e-test-user')
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`).Scan(&teamID)
	if err != nil {
		// Team might already exist — fetch it
		err = pool.QueryRow(ctx, `
			SELECT id FROM teams WHERE id = 'e2e-test-team'
		`).Scan(&teamID)
		if err != nil {
			return "", fmt.Errorf("seed team: %w", err)
		}
	}

	var projID string
	err = pool.QueryRow(ctx, `
		INSERT INTO projects (name, team_id, region)
		VALUES ('poc-counter', 'e2e-test-team', 'us-east4')
		ON CONFLICT (team_id, name) DO NOTHING
		RETURNING id
	`).Scan(&projID)
	if err != nil {
		// Project might already exist — fetch it
		err = pool.QueryRow(ctx, `
			SELECT id FROM projects WHERE name = 'poc-counter' AND team_id = 'e2e-test-team'
		`).Scan(&projID)
		if err != nil {
			return "", fmt.Errorf("seed project: %w", err)
		}
	}

	slog.Info("seed data ready", "project_id", projID)
	return projID, nil
}
