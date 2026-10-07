package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Engine generates validated deployment Plans from Claude's Hints.
type Engine struct {
	db           *pgxpool.Pool
	buildStorage provider.BuildStorage
	region       string
	cloud        Cloud
}

func NewEngine(db *pgxpool.Pool, buildStorage provider.BuildStorage, region string, cloud Cloud) *Engine {
	return &Engine{db: db, buildStorage: buildStorage, region: region, cloud: cloud}
}

// Generate turns Hints into a validated Plan with a presigned upload URL.
func (e *Engine) Generate(ctx context.Context, hints Hints) (*Plan, error) {
	// Use resolved project ID if available, fall back to project name
	projectID := hints.ProjectID
	if projectID == "" {
		projectID = hints.ProjectName
	}

	cloud := e.cloud
	if hints.Cloud != "" {
		cloud = Cloud(hints.Cloud)
	}

	now := time.Now().UTC()
	plan := &Plan{
		ID:          "plan_" + uuid.NewString(),
		ProjectID:   projectID,
		ProjectName: hints.ProjectName,
		Cloud:       cloud,
		Status:      PlanPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Build resource name set for validation
	availableResourceNames := make(map[string]bool)
	for _, rh := range hints.Resources {
		if rh.Name == "" {
			return nil, fmt.Errorf("resource name cannot be empty")
		}
		availableResourceNames[rh.Name] = true
	}

	// Build resource plans
	for _, rh := range hints.Resources {
		p, err := defaultProvider(rh.Type)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", rh.Type, err)
		}
		rp := ResourcePlan{
			Name:     rh.Name,
			Type:     ResourceType(rh.Type),
			Provider: p,
			Region:   e.region,
		}
		plan.Resources = append(plan.Resources, rp)
	}

	// Build service plans
	for _, sh := range hints.Services {
		sp := ServicePlan{
			Name:       sh.Name,
			Type:       ServiceType(sh.Type),
			Target:     selectTarget(sh.Type, cloud),
			Framework:  sh.Framework,
			DetectedBy: "claude_hint", // TODO(Sprint 2): run railpack.Detect() and use as authoritative source
			SourceDir:  sh.SourceDir,
			BuildCmd:   sh.BuildCmd,
			StartCmd:   sh.StartCmd,
			Port:       sh.Port,
		}

		// WebSocket support: enable session affinity and extended timeout
		if sh.WebSocket {
			sp.SessionAffinity = true
			sp.RequestTimeout = 3600 // max Cloud Run timeout for long-lived connections
		}

		// Classify env vars for this service
		for _, eh := range hints.EnvHints {
			if eh.Service != sh.Name {
				continue
			}
			ev, err := ClassifyEnv(eh.Key, eh.Classification, eh.Description, eh.InjectFrom, availableResourceNames)
			if err != nil {
				return nil, fmt.Errorf("service %q: %w", sh.Name, err)
			}
			if ev.Classification == EnvBuildArg {
				sp.BuildArgs = append(sp.BuildArgs, ev)
			} else {
				sp.EnvVars = append(sp.EnvVars, ev)
			}
		}

		plan.Services = append(plan.Services, sp)
	}

	// Validate before persisting
	if err := Validate(plan); err != nil {
		return nil, fmt.Errorf("plan validation failed: %w", err)
	}

	// Local mode: source is on disk — no upload needed.
	if hints.SourcePath != "" {
		plan.BuildMode = "local"
		plan.SourcePath = hints.SourcePath
	} else {
		// Cloud mode: generate presigned upload URL for source archive.
		// The object key is format-agnostic (no extension); the server auto-detects
		// tar.gz vs zip by inspecting magic bytes at extraction time.
		plan.BuildMode = "cloud"
		objectKey := fmt.Sprintf("sources/%s/source.archive", plan.ID)
		uploadURL, err := e.buildStorage.GenerateUploadURL(ctx, objectKey, 30*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("generate upload URL: %w", err)
		}
		plan.UploadURL = uploadURL
		plan.UploadCommand = fmt.Sprintf(
			`git archive HEAD | gzip | curl -sSf -XPUT -H 'Content-Type: application/gzip' --data-binary @- '%s'`,
			uploadURL,
		)
		plan.UploadCommandZip = fmt.Sprintf(
			`zip -r source.zip . -x '.git/*' && curl -sSf -XPUT -H 'Content-Type: application/zip' --data-binary @source.zip '%s' && rm source.zip`,
			uploadURL,
		)
	}

	// Persist plan to database
	if err := e.persistPlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("persist plan: %w", err)
	}

	slog.Info("plan generated", "plan_id", plan.ID, "services", len(plan.Services), "resources", len(plan.Resources))
	return plan, nil
}

// GetPlan retrieves a plan by ID.
func (e *Engine) GetPlan(ctx context.Context, planID string) (*Plan, error) {
	var planData []byte
	err := e.db.QueryRow(ctx,
		"SELECT plan_data FROM plans WHERE id = $1",
		planID,
	).Scan(&planData)
	if err != nil {
		return nil, fmt.Errorf("get plan %s: %w", planID, err)
	}

	var plan Plan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan %s: %w", planID, err)
	}
	return &plan, nil
}

// UpdateStatus updates a plan's status in the database.
func (e *Engine) UpdateStatus(ctx context.Context, planID string, status PlanStatus) error {
	_, err := e.db.Exec(ctx,
		"UPDATE plans SET status = $1, updated_at = NOW() WHERE id = $2",
		string(status), planID,
	)
	return err
}

func (e *Engine) persistPlan(ctx context.Context, plan *Plan) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}

	_, err = e.db.Exec(ctx,
		"INSERT INTO plans (id, project_id, status, plan_data) VALUES ($1, $2, $3, $4)",
		plan.ID, plan.ProjectID, string(plan.Status), data,
	)
	return err
}

// selectTarget decides where a service should be deployed based on its type
// and the target cloud. Frontends always go to Cloudflare Pages (cloud-agnostic).
func selectTarget(serviceType string, cloud Cloud) ServiceTarget {
	switch serviceType {
	case "frontend":
		return TargetCloudflarePages
	default:
		if cloud == CloudAWS {
			return TargetECSFargate
		}
		return TargetCloudRun
	}
}

// defaultProvider returns the default provider for a resource type,
// or an error if the resource type is not recognised.
func defaultProvider(resourceType string) (string, error) {
	switch resourceType {
	case "postgres":
		return "neon", nil
	case "redis":
		return "upstash", nil // Sprint 2
	case "storage", "gcs":
		return "gcs", nil
	case "s3":
		return "s3", nil
	default:
		return "", fmt.Errorf("unsupported resource type %q", resourceType)
	}
}
