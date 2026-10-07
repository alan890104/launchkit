package provider

import (
	"context"
	"io"
	"time"
)

// BuildStorage handles temporary source code storage for builds.
type BuildStorage interface {
	// GenerateUploadURL returns a presigned URL the client uses to upload a source tarball.
	GenerateUploadURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error)

	// Download returns a reader for the object at the given key.
	Download(ctx context.Context, objectKey string) (io.ReadCloser, error)

	// Upload writes data to the given object key.
	Upload(ctx context.Context, objectKey string, r io.Reader) error

	// Exists checks whether an object exists.
	Exists(ctx context.Context, objectKey string) (bool, error)
}

// BuildOpts configures a build job.
type BuildOpts struct {
	SourceBucket  string            // bucket (GCS or S3) with source tarball (cloud mode)
	SourceKey     string            // object key within bucket (cloud mode)
	ImageTag      string            // full container registry tag for the output image (backend only)
	SourceDir     string            // subdirectory within tarball to build
	BuildArgs     map[string]string // build-time env vars (e.g. VITE_API_URL)
	DistOutputKey string            // object key for dist tarball output (frontend/cloud mode)
	ProjectID     string            // LaunchKit project ID — used as cache mount prefix for tenant isolation
	// Local build mode: source is already on disk, no download needed.
	LocalSourcePath string // absolute path to source root on local filesystem (local mode)
}

// BuildResult is returned when a build completes.
type BuildResult struct {
	ImageURI      string // pushed image URI (for backends)
	LogsURL       string // link to build logs
	LocalDistPath string // local directory with built frontend assets (local mode, frontend only)
}

// Builder submits and monitors build jobs.
type Builder interface {
	// Build submits a build job and blocks until it completes or fails.
	Build(ctx context.Context, opts BuildOpts) (*BuildResult, error)
}

// DeployOpts configures a service deployment.
type DeployOpts struct {
	ServiceName     string
	Region          string
	ImageURI        string
	EnvVars         map[string]string
	Port            int
	MinScale        int // 0 = scale to zero
	MaxScale        int
	Memory          string // e.g. "512Mi"
	CPU             string // e.g. "1"
	SessionAffinity bool   // enable session affinity (required for WebSocket)
	RequestTimeout  int    // request timeout in seconds (default 300, max 3600 for WebSocket)
	Concurrency     int    // max concurrent requests per instance (0 = default 80)
}

// ServiceStatus represents the current state of a deployed service.
type ServiceStatus struct {
	Name   string
	URL    string
	Status string // "ready", "deploying", "failed"
}

// LogEntry is a single log line from a running service.
type LogEntry struct {
	Timestamp time.Time
	Severity  string
	Message   string
}

// Compute manages container-based deployments (Cloud Run, ECS Fargate, etc.).
type Compute interface {
	// CreateService creates a new service (possibly with no active revision) and returns its URL.
	CreateService(ctx context.Context, opts DeployOpts) (url string, err error)

	// Deploy updates an existing service with a new revision.
	Deploy(ctx context.Context, opts DeployOpts) (url string, err error)

	// GetStatus returns the current status of a service.
	GetStatus(ctx context.Context, serviceName, region string) (*ServiceStatus, error)

	// GetLogs returns recent log entries for a service.
	GetLogs(ctx context.Context, serviceName, region string, limit int) ([]LogEntry, error)

	// DeleteService removes a service entirely (used for rollback).
	DeleteService(ctx context.Context, serviceName, region string) error

	// GetMetrics returns real metrics for Cloud Run services.
	GetMetrics(ctx context.Context, params MetricsQueryParams) ([]MetricsResponse, error)

	// GetUptime returns uptime statistics for a Cloud Run service.
	GetUptime(ctx context.Context, serviceName, region string, period string) (UptimeInfo, error)
}

// CreateDBOpts configures a new managed database.
type CreateDBOpts struct {
	Name   string // project-scoped name, e.g. "lk-myapp-prod"
	Region string
}

// CreateDBResult is returned after provisioning a database.
type CreateDBResult struct {
	ProviderID    string // external ID for cleanup
	ConnectionURL string // full connection string
	Host          string
	Database      string
	User          string
	Password      string
}

// Database provisions and manages databases.
type Database interface {
	// Create provisions a new database and returns connection info.
	Create(ctx context.Context, opts CreateDBOpts) (*CreateDBResult, error)

	// Delete tears down a provisioned database (used for rollback).
	Delete(ctx context.Context, providerID string) error
}

// CreateStorageOpts configures a new object storage bucket.
type CreateStorageOpts struct {
	Name   string // bucket name (e.g. "lk-foodie-uploads")
	Region string
}

// CreateStorageResult is returned after provisioning a storage bucket.
type CreateStorageResult struct {
	ProviderID string // full bucket name
	BucketURL  string // e.g. "gs://lk-foodie-uploads" or "s3://lk-foodie-uploads"
	PublicURL  string // e.g. "https://storage.googleapis.com/lk-foodie-uploads"
}

// ObjectStorage provisions and manages object storage (GCS buckets, S3 buckets).
type ObjectStorage interface {
	// CreateBucket provisions a new storage bucket. Returns the bucket URL.
	CreateBucket(ctx context.Context, opts CreateStorageOpts) (*CreateStorageResult, error)

	// DeleteBucket tears down a provisioned bucket.
	DeleteBucket(ctx context.Context, bucketName string) error
}

// ─── Custom Domain ────────────────────────────────────────────────

// DNSRecord represents a DNS record the user needs to add at their registrar.
type DNSRecord struct {
	Type     string `json:"type"`               // "CNAME", "TXT", "MX"
	Name     string `json:"name"`               // e.g. "api", "@", "resend._domainkey"
	Value    string `json:"value"`              // e.g. "ghs.googlehosted.com"
	Priority int    `json:"priority,omitempty"` // for MX records
	TTL      int    `json:"ttl,omitempty"`
}

// DomainMapper maps custom domains to deployed services.
// Implemented optionally by CloudRun and CloudflarePages — use type assertion.
type DomainMapper interface {
	MapDomain(ctx context.Context, opts MapDomainOpts) (*MapDomainResult, error)
	UnmapDomain(ctx context.Context, opts UnmapDomainOpts) error
	VerifyDomain(ctx context.Context, opts VerifyDomainOpts) (*DomainStatus, error)
}

type MapDomainOpts struct {
	ServiceName string // Cloud Run service name or Cloudflare Pages project name
	Domain      string
	Region      string
}

type MapDomainResult struct {
	Records []DNSRecord
}

type UnmapDomainOpts struct {
	ServiceName string
	Domain      string
	Region      string
}

type VerifyDomainOpts struct {
	ServiceName string
	Domain      string
	Region      string
}

// DomainStatus represents the verification state of a custom domain.
type DomainStatus struct {
	DNSVerified   bool   `json:"dns_verified"`
	SSLStatus     string `json:"ssl_status"` // "pending" | "provisioning" | "active"
	OwnerVerified bool   `json:"owner_verified"`
}

// ─── Email ────────────────────────────────────────────────────────

// Email manages email domain verification and sending credentials.
// Implemented by Resend.
type Email interface {
	CreateDomain(ctx context.Context, domain string) (*EmailDomainResult, error)
	VerifyDomain(ctx context.Context, domainID string) (*EmailDomainStatus, error)
	CreateAPIKey(ctx context.Context, name, domainID string) (string, error)
	DeleteDomain(ctx context.Context, domainID string) error
}

type EmailDomainResult struct {
	ProviderID string      `json:"provider_id"`
	Records    []DNSRecord `json:"records"`
	Status     string      `json:"status"`
}

type EmailDomainStatus struct {
	Status  string           `json:"status"` // "pending" | "verified" | "failed"
	Records []DNSRecordCheck `json:"records"`
}

type DNSRecordCheck struct {
	DNSRecord
	Verified bool `json:"verified"`
}

// ─── Domain Registration ─────────────────────────────────────────

// DomainRegistrar provisions and manages domains via a registrar API.
// Implemented by NameCom.
type DomainRegistrar interface {
	// SearchDomains checks availability for up to 50 domains at once.
	SearchDomains(ctx context.Context, domains []string) ([]DomainSearchResult, error)

	// PurchaseDomain registers a new domain.
	PurchaseDomain(ctx context.Context, opts DomainPurchaseOpts) (*DomainPurchaseResult, error)

	// RenewDomain renews an existing domain for the given number of years.
	RenewDomain(ctx context.Context, domain string, years int) (*DomainPurchaseResult, error)

	// CreateDNSRecord adds a DNS record at the registrar.
	CreateDNSRecord(ctx context.Context, domain string, rec RegistrarDNSRecord) (*RegistrarDNSRecord, error)

	// ListDNSRecords returns all DNS records for a domain.
	ListDNSRecords(ctx context.Context, domain string) ([]RegistrarDNSRecord, error)

	// DeleteDNSRecord removes a DNS record by ID.
	DeleteDNSRecord(ctx context.Context, domain string, recordID int) error

	// LockDomain enables registrar lock to prevent unauthorized transfers.
	LockDomain(ctx context.Context, domain string) error

	// UnlockDomain disables registrar lock (required before transfer).
	UnlockDomain(ctx context.Context, domain string) error

	// ListDomains returns all domains owned by the account.
	ListDomains(ctx context.Context) ([]DomainInfo, error)
}

// DomainSearchResult represents availability and pricing for a domain.
type DomainSearchResult struct {
	Domain     string  `json:"domain"`
	Available  bool    `json:"available"`
	Premium    bool    `json:"premium"`
	PriceUSD   float64 `json:"price_usd"`   // registration price per year
	RenewalUSD float64 `json:"renewal_usd"` // annual renewal price
}

// DomainPurchaseOpts configures a domain purchase.
type DomainPurchaseOpts struct {
	Domain    string
	Years     int // 1-10
	Contacts  DomainContacts
	AutoRenew bool
}

// DomainContacts represents WHOIS contact info for domain registration.
type DomainContacts struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`        // E.164: +1.5551234567
	Organization string `json:"organization"` // optional
	Address1     string `json:"address1"`
	City         string `json:"city"`
	State        string `json:"state"`
	PostalCode   string `json:"postal_code"`
	Country      string `json:"country"` // ISO 3166-1 alpha-2
}

// DomainPurchaseResult is returned after a domain registration or renewal.
type DomainPurchaseResult struct {
	Domain      string    `json:"domain"`
	ExpiresAt   time.Time `json:"expires_at"`
	ProviderID  string    `json:"provider_id"`
	Nameservers []string  `json:"nameservers"`
}

// RegistrarDNSRecord represents a DNS record managed at the registrar.
type RegistrarDNSRecord struct {
	ID    int    `json:"id,omitempty"`
	Type  string `json:"type"` // A, AAAA, CNAME, TXT, MX, SRV, NS
	Host  string `json:"host"` // subdomain or "" for apex
	Value string `json:"value"`
	TTL   int    `json:"ttl,omitempty"`
	Prio  int    `json:"priority,omitempty"` // for MX/SRV
}

// DomainInfo represents a domain in the registrar account.
type DomainInfo struct {
	Domain    string    `json:"domain"`
	ExpiresAt time.Time `json:"expires_at"`
	AutoRenew bool      `json:"auto_renew"`
	Locked    bool      `json:"locked"`
}

// DeployStaticOpts configures a static site deployment.
type DeployStaticOpts struct {
	ProjectName string
	DistDir     string // local directory with built assets
}

// Static deploys static frontends (Cloudflare Pages, S3+CloudFront, etc.).
type Static interface {
	// CreateProject creates a new Pages project (idempotent).
	CreateProject(ctx context.Context, projectName string) error

	// Deploy uploads dist/ assets and triggers a deployment. Returns the live URL.
	Deploy(ctx context.Context, opts DeployStaticOpts) (url string, err error)

	// DeleteProject removes a Pages project (used for rollback).
	DeleteProject(ctx context.Context, projectName string) error
}
