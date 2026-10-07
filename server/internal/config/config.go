package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// parseOrigins splits a comma-separated origins string into a slice.
// An empty string returns ["*"] (wildcard) as the safe default.
func parseOrigins(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{"*"}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type Config struct {
	Port    string
	Version string
	Env     string // development | staging | production

	DatabaseURL string
	BaseURL     string

	// Cloud provider selection: "gcp" or "aws" (default "gcp")
	Cloud string

	// GCP (used when Cloud == "gcp")
	GCPProjectID         string
	GCPRegion            string
	GCSBuildBucket       string
	ArtifactRegistryRepo string

	// GCP BuildKit VM (primary builder, gVisor-isolated)
	BuildKitAddr     string // e.g. tcp://10.128.0.x:1234 (empty = Cloud Build only)
	BuildKitInstance string // GCE instance name for auto start/stop
	BuildKitZone     string // GCE zone for the BuildKit VM

	// AWS (used when Cloud == "aws")
	AWSRegion             string
	AWSAccountID          string
	S3BuildBucket         string
	ECRRepositoryBase     string
	AWSBuildKitAddr       string // e.g. tcp://10.0.1.x:1234 (empty = CodeBuild only)
	AWSBuildKitInstanceID string
	AWSECSCluster         string
	AWSECSSubnets         string // comma-separated subnet IDs
	AWSECSSecurityGroups  string // comma-separated security group IDs
	AWSECSExecutionRole   string // ECS task execution role ARN
	AWSECSTaskRole        string // ECS task role ARN
	AWSALBListenerARN     string // ALB HTTPS listener ARN

	// Neon (managed Postgres for user projects — cloud-agnostic)
	NeonAPIKey string

	// Upstash (managed Redis for user projects — cloud-agnostic)
	UpstashEmail  string
	UpstashAPIKey string

	// Cloudflare (Pages for static frontends — cloud-agnostic)
	CloudflareAccountID string
	CloudflareAPIToken  string

	// Resend (transactional email for user projects — cloud-agnostic)
	ResendAPIKey string

	// Name.com (domain registration — cloud-agnostic)
	NameComUser    string
	NameComToken   string
	NameComSandbox bool // use sandbox API for development

	// Firebase (authentication for web dashboard)
	FirebaseProjectID string // GCP project ID with Firebase enabled

	// Build mode: "local" uses Docker socket on same machine; "cloud" uses BuildKit VM / Cloud Build.
	// Default "local" for open-source tier.
	BuildMode        string // "local" | "cloud"
	DockerSocketAddr string // default "unix:///var/run/docker.sock"

	// SecretEncryptionKey is a 32-byte hex-encoded key for AES-256-GCM secret encryption.
	// Generate with: openssl rand -hex 32
	// Required in production. If empty, secrets are stored as plaintext (dev only).
	SecretEncryptionKey string

	// CORS — allowed browser origins.
	// Comma-separated list (e.g. "https://app.launchkit.dev,https://launchkit.dev").
	// Defaults to "*" (wildcard). Set explicitly in production to restrict to dashboard domains.
	AllowedOrigins []string

	// GitHub (push-to-deploy webhooks)
	GitHubToken string // PAT or GitHub App token for downloading private repo tarballs

	// LaunchKit-internal Redis (Upstash REST) for distributed API key caching.
	// Optional — if empty, auth falls through to DB lookup every request.
	LaunchKitRedisURL   string // LAUNCHKIT_REDIS_URL   (Upstash REST endpoint)
	LaunchKitRedisToken string // LAUNCHKIT_REDIS_TOKEN (Upstash REST token)

	// DevKey is an optional static API key for development (LAUNCHKIT_DEV_KEY).
	// When set, any request with this exact Bearer token is authenticated as "dev-user".
	// Never set in production.
	DevKey string

	// Tunable operation timeouts (all have sensible defaults; rarely need changing)
	BuildKitStartTimeout  time.Duration // BUILDKIT_START_TIMEOUT  default 90s
	BuildKitIdleTimeout   time.Duration // BUILDKIT_IDLE_TIMEOUT   default 30m
	BuildKitCheckInterval time.Duration // BUILDKIT_CHECK_INTERVAL default 5m
	CloudBuildTimeout     time.Duration // CLOUD_BUILD_TIMEOUT     default 10m
	DeployWorkerTimeout   time.Duration // DEPLOY_WORKER_TIMEOUT   default 15m
	ECSStabilizeTimeout   time.Duration // ECS_STABILIZE_TIMEOUT   default 5m
}

// Region returns the region for the configured cloud provider.
func (c *Config) Region() string {
	if c.Cloud == "aws" {
		return c.AWSRegion
	}
	return c.GCPRegion
}

func Load() (*Config, error) {
	cloud := envOr("CLOUD", "gcp")

	// GCP defaults
	gcpRegion := envOr("GCP_REGION", "us-east4")
	gcpProject := os.Getenv("GCP_PROJECT_ID")

	// Build derived defaults only when gcpProject is non-empty to avoid
	// bucket names that start with "-" (invalid GCS bucket names).
	gcsBuildBucket := os.Getenv("GCS_BUILD_BUCKET")
	if gcsBuildBucket == "" && gcpProject != "" {
		gcsBuildBucket = gcpProject + "-launchkit-builds"
	}

	artifactRepo := os.Getenv("ARTIFACT_REGISTRY_REPO")
	if artifactRepo == "" && gcpProject != "" {
		artifactRepo = gcpRegion + "-docker.pkg.dev/" + gcpProject + "/user-images"
	}

	// AWS defaults
	awsRegion := envOr("AWS_REGION", "us-east-1")

	cfg := &Config{
		Port:        envOr("PORT", "8080"),
		Version:     envOr("VERSION", "0.1.0"),
		Env:         envOr("ENV", "development"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		BaseURL:     envOr("BASE_URL", "http://localhost:8080"),

		Cloud: cloud,

		GCPProjectID:         gcpProject,
		GCPRegion:            gcpRegion,
		GCSBuildBucket:       gcsBuildBucket,
		ArtifactRegistryRepo: artifactRepo,

		BuildKitAddr:     os.Getenv("BUILDKIT_ADDR"),
		BuildKitInstance: envOr("BUILDKIT_INSTANCE", "launchkit-buildkit"),
		BuildKitZone:     envOr("BUILDKIT_ZONE", gcpRegion+"-c"),

		AWSRegion:             awsRegion,
		AWSAccountID:          os.Getenv("AWS_ACCOUNT_ID"),
		S3BuildBucket:         os.Getenv("S3_BUILD_BUCKET"),
		ECRRepositoryBase:     envOr("ECR_REPOSITORY_BASE", "launchkit"),
		AWSBuildKitAddr:       os.Getenv("AWS_BUILDKIT_ADDR"),
		AWSBuildKitInstanceID: os.Getenv("AWS_BUILDKIT_INSTANCE_ID"),
		AWSECSCluster:         envOr("AWS_ECS_CLUSTER", "launchkit"),
		AWSECSSubnets:         os.Getenv("AWS_ECS_SUBNETS"),
		AWSECSSecurityGroups:  os.Getenv("AWS_ECS_SECURITY_GROUPS"),
		AWSECSExecutionRole:   os.Getenv("AWS_ECS_EXECUTION_ROLE"),
		AWSECSTaskRole:        os.Getenv("AWS_ECS_TASK_ROLE"),
		AWSALBListenerARN:     os.Getenv("AWS_ALB_LISTENER_ARN"),

		NeonAPIKey: os.Getenv("NEON_API_KEY"),

		UpstashEmail:  os.Getenv("UPSTASH_EMAIL"),
		UpstashAPIKey: os.Getenv("UPSTASH_API_KEY"),

		CloudflareAccountID: os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		CloudflareAPIToken:  os.Getenv("CLOUDFLARE_API_TOKEN"),

		ResendAPIKey: os.Getenv("RESEND_API_KEY"),

		NameComUser:    os.Getenv("NAMECOM_API_USER"),
		NameComToken:   os.Getenv("NAMECOM_API_TOKEN"),
		NameComSandbox: os.Getenv("NAMECOM_SANDBOX") == "true",

		FirebaseProjectID: os.Getenv("FIREBASE_PROJECT_ID"),

		BuildMode:        envOr("BUILD_MODE", "local"),
		DockerSocketAddr: envOr("DOCKER_SOCKET", "unix:///var/run/docker.sock"),

		SecretEncryptionKey: os.Getenv("SECRET_ENCRYPTION_KEY"),

		AllowedOrigins: parseOrigins(os.Getenv("ALLOWED_ORIGINS")),

		GitHubToken: os.Getenv("GITHUB_TOKEN"),

		LaunchKitRedisURL:   os.Getenv("LAUNCHKIT_REDIS_URL"),
		LaunchKitRedisToken: os.Getenv("LAUNCHKIT_REDIS_TOKEN"),

		DevKey: os.Getenv("LAUNCHKIT_DEV_KEY"),

		BuildKitStartTimeout:  parseDuration(os.Getenv("BUILDKIT_START_TIMEOUT"), 90*time.Second),
		BuildKitIdleTimeout:   parseDuration(os.Getenv("BUILDKIT_IDLE_TIMEOUT"), 30*time.Minute),
		BuildKitCheckInterval: parseDuration(os.Getenv("BUILDKIT_CHECK_INTERVAL"), 5*time.Minute),
		CloudBuildTimeout:     parseDuration(os.Getenv("CLOUD_BUILD_TIMEOUT"), 10*time.Minute),
		DeployWorkerTimeout:   parseDuration(os.Getenv("DEPLOY_WORKER_TIMEOUT"), 15*time.Minute),
		ECSStabilizeTimeout:   parseDuration(os.Getenv("ECS_STABILIZE_TIMEOUT"), 5*time.Minute),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all critical configuration fields are populated.
func (c *Config) Validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	switch c.Cloud {
	case "gcp":
		if c.GCPProjectID == "" {
			errs = append(errs, "GCP_PROJECT_ID is required when CLOUD=gcp")
		}
		// GCS bucket only required in cloud build mode
		if c.BuildMode == "cloud" && strings.HasPrefix(c.GCSBuildBucket, "-") {
			errs = append(errs, fmt.Sprintf("GCS bucket name %q is invalid (starts with '-'); set GCS_BUILD_BUCKET explicitly", c.GCSBuildBucket))
		}
	case "aws":
		if c.AWSAccountID == "" {
			errs = append(errs, "AWS_ACCOUNT_ID is required when CLOUD=aws")
		}
		// S3 bucket only required in cloud build mode
		if c.BuildMode == "cloud" && c.S3BuildBucket == "" {
			errs = append(errs, "S3_BUILD_BUCKET is required when CLOUD=aws and BUILD_MODE=cloud")
		}
		if c.AWSECSSubnets == "" {
			errs = append(errs, "AWS_ECS_SUBNETS is required when CLOUD=aws")
		}
		if c.AWSECSExecutionRole == "" {
			errs = append(errs, "AWS_ECS_EXECUTION_ROLE is required when CLOUD=aws")
		}
	default:
		errs = append(errs, fmt.Sprintf("CLOUD must be 'gcp' or 'aws' (got %q)", c.Cloud))
	}

	// Firebase — required in production/staging, optional in development
	if !c.IsDev() && c.FirebaseProjectID == "" {
		errs = append(errs, "FIREBASE_PROJECT_ID is required in production/staging")
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (c *Config) IsDev() bool {
	return c.Env == "development"
}

func (c *Config) IsLocalBuild() bool {
	return c.BuildMode == "local"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseDuration parses a duration string (e.g. "90s", "30m") from an env var value.
// Returns def if the string is blank or cannot be parsed.
func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}
