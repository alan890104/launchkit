package deploy

import "time"

// PlanStatus tracks the lifecycle of a deployment plan.
type PlanStatus string

const (
	PlanPending   PlanStatus = "pending"
	PlanConfirmed PlanStatus = "confirmed"
	PlanExecuting PlanStatus = "executing"
	PlanCompleted PlanStatus = "completed"
	PlanFailed    PlanStatus = "failed"
)

// Cloud identifies which cloud provider to use.
type Cloud string

const (
	CloudGCP Cloud = "gcp"
	CloudAWS Cloud = "aws"
)

// ServiceTarget is the compute platform for a service.
type ServiceTarget string

const (
	TargetCloudRun        ServiceTarget = "cloud_run"
	TargetECSFargate      ServiceTarget = "ecs_fargate"
	TargetCloudflarePages ServiceTarget = "cloudflare_pages"
)

// IsComputeTarget returns true if the target is a container compute platform
// (Cloud Run, ECS Fargate, etc.) as opposed to a static hosting platform.
func IsComputeTarget(t ServiceTarget) bool {
	return t == TargetCloudRun || t == TargetECSFargate
}

// ServiceType classifies what the service does.
type ServiceType string

const (
	ServiceBackend  ServiceType = "backend"
	ServiceFrontend ServiceType = "frontend"
	ServiceWorker   ServiceType = "worker"
)

// ResourceType classifies provisioned infrastructure.
type ResourceType string

const (
	ResourcePostgres ResourceType = "postgres"
	ResourceRedis    ResourceType = "redis"   // Sprint 2
	ResourceStorage  ResourceType = "storage" // GCS or S3, depending on cloud
	ResourceS3       ResourceType = "s3"      // explicit S3
	ResourceGCS      ResourceType = "gcs"     // explicit GCS
)

// EnvClassification determines how an env var gets its value.
type EnvClassification string

const (
	EnvAutoInject   EnvClassification = "auto_inject"   // from provisioned resource (e.g. DATABASE_URL)
	EnvAutoGenerate EnvClassification = "auto_generate" // server generates (e.g. SECRET_KEY)
	EnvBuildArg     EnvClassification = "build_arg"     // compile-time only (e.g. VITE_API_URL)
	EnvUserRequired EnvClassification = "user_required" // user must supply (Sprint 2)
)

// Plan is the central deployment blueprint.
// Persisted as JSONB in the plans table.
type Plan struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	// EnvironmentID scopes cloud resource names to a specific environment
	// (production, staging, dev, etc.).  Without this field, all environments
	// of the same project share the same Cloud Run service name, causing staging
	// deploys to overwrite production.  Empty string = "production" (legacy plans).
	EnvironmentID string     `json:"environment_id,omitempty"`
	Cloud         Cloud      `json:"cloud"`
	Status        PlanStatus `json:"status"`

	Services  []ServicePlan  `json:"services"`
	Resources []ResourcePlan `json:"resources"`

	// Build mode: "local" = Docker socket on MCP server machine; "cloud" = BuildKit VM / Cloud Build.
	BuildMode  string `json:"build_mode"`            // "local" | "cloud"
	SourcePath string `json:"source_path,omitempty"` // absolute local path (local mode only)

	UploadURL        string `json:"upload_url,omitempty"`         // presigned URL for source upload (cloud mode only)
	UploadCommand    string `json:"upload_command,omitempty"`     // shell command for tar.gz upload (cloud mode only)
	UploadCommandZip string `json:"upload_command_zip,omitempty"` // shell command for zip upload (cloud mode only)

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ServicePlan describes how to build and deploy one service.
type ServicePlan struct {
	Name       string        `json:"name"`
	Type       ServiceType   `json:"type"`
	Target     ServiceTarget `json:"target"`
	Framework  string        `json:"framework"`             // e.g. "fastapi", "vite-react"
	DetectedBy string        `json:"detected_by,omitempty"` // "claude_hint" or "railpack" (Sprint 2)
	SourceDir  string        `json:"source_dir"`            // relative path in tarball, e.g. "backend/"

	BuildCmd string `json:"build_cmd,omitempty"` // override if non-standard
	StartCmd string `json:"start_cmd,omitempty"` // override if non-standard

	Port int `json:"port,omitempty"` // application port (default varies by framework)

	SessionAffinity bool `json:"session_affinity,omitempty"` // enable session affinity (WebSocket support)
	RequestTimeout  int  `json:"request_timeout,omitempty"`  // request timeout in seconds (extended for WebSocket)

	EnvVars   []EnvVar `json:"env_vars"`
	BuildArgs []EnvVar `json:"build_args"`

	// Populated during execution
	ImageURI      string `json:"image_uri,omitempty"`       // Artifact Registry URI (backend only)
	LocalDistPath string `json:"local_dist_path,omitempty"` // local dist dir (frontend, local mode only)
	URL           string `json:"url,omitempty"`             // live URL after deploy
}

// ResourcePlan describes infrastructure to provision.
type ResourcePlan struct {
	Name     string       `json:"name"`
	Type     ResourceType `json:"type"`
	Provider string       `json:"provider"` // e.g. "neon"
	Region   string       `json:"region"`
}

// EnvVar represents a classified environment variable.
type EnvVar struct {
	Key            string            `json:"key"`
	Classification EnvClassification `json:"classification"`
	Value          string            `json:"value,omitempty"`       // populated after resolve
	Source         string            `json:"source,omitempty"`      // e.g. "provision_postgres"
	Strategy       string            `json:"strategy,omitempty"`    // e.g. "random_hex_32" for auto_generate
	Description    string            `json:"description,omitempty"` // Claude's hint text, shown on secret form
}

// ProgressFunc is a callback for reporting deploy progress.
// It sends progress notifications via MCP protocol.
// progress: current step number (1-based)
// total: total number of steps
// message: human-readable status message
type ProgressFunc func(progress int, total int, message string)
