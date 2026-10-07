# LaunchKit — Technical Architecture

> **Positioning**: An automatic publishing platform for vibe coders — tell the AI "publish this", and 3 minutes later the app is live.
>
> **User experience goals**:
> - Users do not need to know what a server, database or environment variable is
> - Users do not need to choose a plan, compare pricing or configure DNS
> - Users only need to say "publish this", and the AI handles the rest
>
> **Long-term vision**: An automatic publishing protocol for the Agent-to-Agent era
> **Phase 1 focus**: Indie developers building SaaS with AI, going live at light speed and starting to earn
> **Monetization core**: GPU training/inference (50%+ margin) + cloud cost markup (30-50%)
>
> **Division of labor**: The AI agent (such as Claude) is the brain — it reads the code, understands requirements and makes decisions. LaunchKit is the hands and feet — it executes infrastructure operations and handles unified billing. LaunchKit does not build its own AI.
>
> **Phase 1 strategy**: GCP-first (Cloud Run + BuildKit VM + Cloud Build fallback + Neon + Upstash + GCS build staging + Cloudflare R2 user storage), ship fast to validate the market. Multi-cloud / BYOC is a Phase 2+ option.
>
> **Why not use the official GCP MCP directly?** The GCP MCP is fragmented — each product has its own separate MCP endpoint (Cloud Run, Cloud SQL, Cloud Storage...), and there is no wiring mechanism between them. LaunchKit does everything in one MCP, and resources are wired together automatically through the ref mechanism (provision → ref → deploy with automatic injection), so the AI never touches plaintext passwords.
>
> **Why not use the Zeabur/Railway MCP?** They are single-platform operation tools (remote controls) and do not do full-stack orchestration. They have no GPU, no cross-service ref wiring and no cost control. LaunchKit is the butler.

---

## 📚 Related Documents

| Document | Description |
|------|------|
| [USER_FLOW.md](USER_FLOW.md) | User flow design |
| [docs/README.md](docs/README.md) | Full documentation index |
| [docs/architecture/DATABASE_ISOLATION_ARCHITECTURE.md](docs/architecture/DATABASE_ISOLATION_ARCHITECTURE.md) | **Database isolation design** (important) |

---

## 1. System Overview

```
Claude Code
    │
    │  HTTPS (MCP over Streamable HTTP)
    │  Bearer Token (OAuth 2.1)
    ▼
┌──────────────────────────────────────────────────────────┐
│                  LaunchKit API Server                     │
│                  (Cloud Run, always-on)                   │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │ MCP Endpoint │  │ REST API     │  │ Upload Handler │  │
│  │ POST /mcp    │  │ (Dashboard)  │  │ (Presigned URL)│  │
│  └──────┬──────┘  └──────┬───────┘  └────────────────┘  │
│         │                │                               │
│  ┌──────┴────────────────┴───────────────────────────┐  │
│  │              Shared Service Layer                   │  │
│  │                                                     │  │
│  │  Auth · RBAC · Audit · Rate Limit · Quota          │  │
│  │                                                     │  │
│  │  Tool Handlers:                                     │  │
│  │  provision_* · deploy_* · manage_* · monitor_*     │  │
│  │  team_* · billing_* · env_* · cron_* · db_ops_*   │  │
│  └──────┬──────────────────────────────────────────┘   │
│         │                                               │
│  ┌──────┴──────┐  ┌──────────────┐  ┌──────────────┐  │
│  │  Task Queue │  │ Event Bus    │  │ Scheduler    │  │
│  │  (river)    │  │ (SSE/Redis)  │  │ (Cron)       │  │
│  └──────┬──────┘  └──────────────┘  └──────────────┘  │
└─────────┼────────────────────────────────────────────────┘
          │
          │  Async workers
          │
    ┌─────┼──────────────────────────────────────┐
    │     │                                      │
    ▼     ▼                                      ▼
┌───────────────┐  ┌──────────────────┐  ┌──────────────┐
│ Build Handler │  │ Provision Worker │  │ GPU Worker   │
│ (API Server   │  │ (direct API      │  │ (SkyPilot    │
│  runs inside) │  │  calls)          │  │  Python SDK) │
│               │  │ Neon API         │  │              │
│ 1.Fetch source│  │ Upstash API      │  │ Runs on its  │
│ 2.railpack    │  │ Cloudflare R2    │  │ own VM/K8s   │
│   prepare     │  │ API              │  │              │
│ 3.BuildKit VM │  │                  │  │              │
│  (TCP direct) │  └──────────────────┘  └──────────────┘
│   fallback:   │
│   Cloud Build │
│ 4.Deploy to   │
│   Cloud Run   │
└───────────────┘

    ┌──────────────────────────────────────┐
    │          LaunchKit Database           │
    │          (Neon Postgres)              │
    │                                      │
    │  users · teams · team_members        │
    │  projects · environments · services  │
    │  deployments · resources · domains   │
    │  secrets · env_bindings              │
    │  alerts · incidents · cron_jobs      │
    │  audit_logs · invoices · usage       │
    │  budgets · backups                   │
    └──────────────────────────────────────┘

    ┌──────────────────────────────────────┐
    │          LaunchKit Dashboard          │
    │          (Cloudflare Pages)           │
    │                                      │
    │  Next.js SSR/SSG                     │
    │  Calls the LaunchKit REST API        │
    │  Auth: the same Auth0                │
    └──────────────────────────────────────┘
```

---

## 2. Technology Choices

| Component | Choice | Rationale |
|------|------|------|
| Language | **Go** | Tier 1 official MCP SDK support (`mark3labs/mcp-go`); native goroutine concurrency; small binary, fast cold start; the best fit for infrastructure orchestration |
| HTTP framework | **`net/http` (standard library)** | Go 1.22+ native method routing + path params; zero external dependencies, permanently maintained by the Go team |
| MCP SDK | **`mark3labs/mcp-go`** | Official Tier 1 SDK, tool registration, Streamable HTTP transport |
| OAuth | **`golang-jwt/jwt/v5`** + **Auth0 as IdP** | A standard `http.Handler` middleware validates the Auth0 JWT; Auth0 handles Google/GitHub login and Dynamic Client Registration |
| Task queue | **river** + **Postgres** | Go-native Postgres-based job queue; no Redis dependency, fewer external dependencies |
| Database | **Neon Postgres** | Dogfooding our own provider; serverless, created in milliseconds |
| ORM / Driver | **pgx/v5** (raw SQL) | pgxpool runs SQL directly; the Go driver Neon officially recommends; no ORM |
| Deploy target | **GCP Cloud Run** (API server + user apps) | scale-to-zero, serverless with no VMs to manage, GCP CUD discount |
| Container Registry | **GCP Artifact Registry** | Same GCP ecosystem, fastest push/pull |
| Packaging + detection | **Railpack** | Railway's official next-generation builder (automatic detection of 30+ frameworks across 11 languages); `railpack prepare` produces a BuildKit LLB plan that the BuildKit VM executes; the Plan Engine uses Railpack for deterministic framework detection and to validate Claude's hints |
| Image build | **Persistent BuildKit VM** (primary) + **Cloud Build** (fallback) | e2-standard-4 + 100GB pd-ssd with buildkitd always running on TCP :1234; **gVisor (runsc) multi-tenant isolation + CNI network isolation**; the BuildKit VM reads source code straight from GCS (a `gs://` URI as the build context, zero latency in the same region), so the API Server does not need to download and re-upload it; cached build 7-58s (measured); when the VM is unhealthy it automatically degrades to Cloud Build (cold build 77-197s, with the free allowance as a safety net) |
| Build staging | **GCS** (same region as the BuildKit VM) | Temporary storage for build source tarballs; zero latency in the same region, GCP IAM authentication, 24h lifecycle rule for automatic cleanup |
| Secret encryption | **Google Tink + AWS KMS** | `KMSEnvelopeAEAD2` envelope encryption; AAD bound to the Tenant ID for tenant isolation; misuse-resistant API; $1/month |
| GPU orchestration | **SkyPilot Python SDK** | Runs in a separate Python sidecar, called by the main Go program via subprocess |
| Dashboard | **Next.js on Cloudflare Pages** | SSR + global edge, dogfooding Cloudflare; shares Auth0 authentication |
| Real-time events | **SSE via Redis Pub/Sub** | Pushes deployment progress to Claude Code and the Dashboard in real time (`redis/go-redis/v9`) |
| Rate Limiting | **Redis sliding window** | A standard `http.Handler` middleware + go-redis, per-user rate limiting |
| Scheduling | **river periodic jobs** | User crons and system schedules (backups, cleanup) all use river |

---

## 3. MCP Server Implementation

### 3.1 Project Structure

```
launchkit/
├── cmd/
│   └── api/
│       └── main.go                  # HTTP server entry point (net/http ServeMux + MCP endpoint)
├── internal/
│   ├── mcp/
│   │   ├── server.go                # McpServer initialization + tool registration
│   │   └── tools/
│   │       ├── plan.go              # plan_deployment (Claude hints → Plan Engine → deployment plan)
│   │       ├── deploy.go            # deploy_project (whole-package deploy, orchestrated by the backend), deploy_inference, launch_training
│   │       ├── provision.go         # provision_database, provision_cache, provision_storage (internal use, called by the orchestrator)
│   │       ├── secrets.go           # generate_secret, request_secrets, check_secrets (internal use, called by the orchestrator)
│   │       ├── manage.go            # status, logs, update, scale, destroy, migrate, rollback, restart
│   │       ├── domains.go           # add_domain, check_domain, remove_domain
│   │       ├── monitor.go           # get_metrics, set_alert, list_alerts, delete_alert, get_incidents, get_uptime
│   │       ├── environments.go      # create_environment, list_environments, promote_environment, destroy_environment
│   │       ├── team.go              # create_team, invite_member, list_members, set_role, remove_member, get_audit_log
│   │       ├── billing.go           # top_up, get_balance, get_usage, get_invoice, set_budget, get_cost_forecast
│   │       ├── database.go          # backup_database, list_backups, restore_database
│   │       ├── cron.go              # create_cron, list_crons, delete_cron
│   │       └── account.go           # whoami, usage
│   ├── api/                         # REST API for Dashboard
│   │   ├── router.go                # http.ServeMux routes → shared service layer
│   │   └── middleware.go            # Auth0 JWT verification for REST
│   ├── auth/
│   │   ├── provider.go              # OAuthServerProvider implementation (proxy to Auth0)
│   │   ├── middleware.go            # Bearer token validation
│   │   └── rbac.go                  # Role-based access control
│   ├── provider/                    # Cloud provider adapters
│   │   ├── interfaces.go            # DatabaseProvider, CacheProvider etc. interface definitions
│   │   ├── registry.go              # Provider registry (Sprint 2)
│   │   ├── selector.go              # Managed vs BYOC selector (Sprint 2)
│   │   ├── neon.go                  # Neon Postgres API
│   │   ├── turso.go                 # Turso SQLite API
│   │   ├── upstash.go               # Upstash Redis API
│   │   ├── cloudflare_r2.go         # R2 bucket + credentials (user object storage, provision_storage)
│   │   ├── gcs_build.go             # GCS build staging (presigned URL generation, upload validation, download)
│   │   ├── cloudflare_pages.go      # Pages deployment
│   │   ├── cloud_run.go             # Cloud Run deployment
│   │   └── skypilot.go              # SkyPilot GPU operations (calls a Python subprocess)
│   ├── deploy/
│   │   ├── engine.go                # Plan generation (Railpack detection + Claude hints merge + provider selection)
│   │   ├── plan.go                  # Plan schema definition (first-class data structure, persisted to Postgres)
│   │   ├── classify.go              # env var classification (rules > Claude hint > safe default user_required)
│   │   ├── validate.go              # Plan validation (dependency graph cycles, ref integrity, URL uniqueness)
│   │   ├── hints.go                 # Claude hints type definitions and parsing
│   │   ├── orchestrator.go          # Deployment executor: runs the Plan, makes no judgments
│   │   └── step.go                  # Deployment step state tracking (checkpoint + crash recovery)
│   ├── build/
│   │   ├── railpack.go              # Railpack CLI wrapper (railpack prepare --plan-out)
│   │   ├── handler.go               # Build pipeline (BuildKit VM + Cloud Build fallback)
│   │   ├── validate.go              # Scans for suspected secret files
│   │   └── resolve_env.go           # Resolves env refs → plaintext at deploy time
│   ├── worker/
│   │   ├── scheduler.go             # river job scheduling (PostgreSQL-based job queue)
│   │   ├── build.go                 # build job worker (MaxWorkers: 3, controls BuildKit VM concurrency)
│   │   ├── deploy.go                # deploy job worker (Cloud Run + Cloudflare Pages)
│   │   ├── provision.go             # resource provisioning worker
│   │   ├── recovery.go              # Recovers stuck deployments when the API Server starts
│   │   └── cron.go                  # scheduled job worker
│   ├── event/
│   │   ├── bus.go                   # Redis Pub/Sub event bus
│   │   └── sse.go                   # SSE endpoint for real-time updates
│   ├── monitor/
│   │   ├── metrics.go               # Cloud Run metrics API wrapper
│   │   ├── health.go                # User service health check scheduler
│   │   ├── alerts.go                # Alert evaluation + notification
│   │   ├── selfcheck.go             # LaunchKit's own health checks (Postgres/Redis/BuildKit/KMS)
│   │   ├── orphan.go                # Orphaned resource detection + automatic reclamation (river periodic job)
│   │   ├── buildkit.go              # BuildKit VM idle management (stop/start + build concurrency monitoring + gVisor health)
│   │   └── retention.go             # Data retention cleanup (river periodic job)
│   ├── infra/
│   │   └── circuitbreaker.go        # Circuit breaker (degradation protection for external dependencies)
│   ├── middleware/
│   │   ├── logging.go               # Structured logging + trace_id injection
│   │   └── ratelimit_fallback.go    # Rate limiting (falls back to in-memory when Redis is down)
│   ├── billing/
│   │   ├── credits.go               # Top-up rules + usage pricing definitions
│   │   ├── metering.go              # Usage metering (async, non-blocking)
│   │   ├── topup.go                 # Top-up flow + Stripe payment
│   │   ├── invoicing.go             # Monthly invoice generation
│   │   └── stripe.go                # Stripe integration
│   ├── db/
│   │   ├── models.go                # DB model struct definitions
│   │   └── db.go                    # pgx connection pool
│   └── secrets/
│       └── kms.go                   # Tink + AWS KMS envelope encryption (AAD tenant isolation)
├── dashboard/                       # Next.js Dashboard (separate deploy, Cloudflare Pages)
│   ├── src/app/
│   │   ├── page.tsx                 # Projects overview
│   │   ├── projects/[name]/
│   │   │   ├── page.tsx             # Project detail
│   │   │   ├── metrics/page.tsx     # Metrics charts
│   │   │   ├── logs/page.tsx        # Log viewer
│   │   │   └── settings/page.tsx    # Project settings
│   │   ├── team/page.tsx            # Team management
│   │   ├── billing/page.tsx         # Billing overview
│   │   └── settings/page.tsx        # Account settings
│   └── package.json
├── go.mod
├── go.sum
├── Dockerfile
└── .github/workflows/deploy.yml
```

### 3.2 MCP Server Initialization

```go
// cmd/api/main.go
package main

import (
	"log"
	"net/http"

	"github.com/mark3labs/mcp-go/server"

	"launchkit/internal/api"
	"launchkit/internal/auth"
	"launchkit/internal/mcp"
	mw "launchkit/internal/middleware"
)

func main() {
	mcpSrv := mcp.NewServer()

	// MCP endpoint (requires a Bearer token)
	// ⚠️ Note: sessions cannot be kept only in memory!
	// With multiple Cloud Run instances, later requests of the same session may hit a different instance,
	// so the sessionId → transport mapping must be stored in Redis to ensure session affinity.
	// The in-memory map below is only for single-instance development in sprint 1; it must be replaced with a Redis-backed one before going HA.
	streamHandler := server.NewStreamableHTTPServer(mcpSrv)
	mcpHandler := mw.Chain(streamHandler, auth.RequireBearerToken, mw.RateLimit)

	mux := http.NewServeMux()

	// MCP endpoint
	mux.Handle("POST /mcp", mcpHandler)
	mux.Handle("GET /mcp", mcpHandler)
	mux.Handle("DELETE /mcp", mcpHandler)

	// OAuth endpoints (proxy to Auth0)
	mux.Handle("/.well-known/", auth.OAuthDiscoveryHandler())
	mux.Handle("/authorize", auth.OAuthProxyHandler())
	mux.Handle("/token", auth.OAuthProxyHandler())

	// REST API for Dashboard
	mux.Handle("/api/v1/", api.Handler())

	// Source code upload endpoint
	// ─── Source upload (the presigned URL returned by the MCP tool points here) ───
	mux.HandleFunc("PUT /upload/{deploymentID}", func(w http.ResponseWriter, r *http.Request) {
		// Validate the presigned token, receive the tarball/zip, and store it in GCS (build staging)
		// Supports Content-Type: application/gzip | application/zip
	})

	// ─── Public Upload API (for the Dashboard / CLI / third-party integrations) ───
	// Three-stage presigned URL flow (similar to Zeabur /v2/upload)
	uploadHandler := auth.RequireBearerToken(api.UploadHandler())
	mux.Handle("POST /v1/upload", uploadHandler)                        // Stage 1: get a presigned URL
	mux.Handle("POST /v1/upload/{uploadID}/deploy", uploadHandler)      // Stage 3: confirm the upload and trigger the deploy

	// Secret web form
	mux.HandleFunc("GET /s/{secretRequestID}", func(w http.ResponseWriter, r *http.Request) {
		// Render the secure form page
	})
	mux.HandleFunc("POST /s/{secretRequestID}", func(w http.ResponseWriter, r *http.Request) {
		// Receive the secret, encrypt it with Tink and store it in Postgres
	})

	// SSE endpoint for real-time events (project level)
	mux.Handle("GET /events/{projectID}", auth.RequireBearerToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Subscribe to project events via Redis Pub/Sub, stream as SSE
	})))

	// SSE endpoint for deployment progress (used by the Public Upload API)
	mux.Handle("GET /v1/deployments/{deploymentID}/events", auth.RequireBearerToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Subscribe to deployment progress via Redis Pub/Sub, stream as SSE
	})))

	// Global middleware: logging + panic recovery
	handler := mw.Chain(mux, mw.Logger, mw.Recoverer)

	log.Println("LaunchKit API listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
```

### 3.3 Tool Registration

```go
// internal/mcp/server.go
package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"launchkit/internal/mcp/tools"
)

func NewServer() *server.MCPServer {
	srv := server.NewMCPServer(
		"launchkit", "0.1.0",
		server.WithLogging(),
	)

	// ─── Deployment core (called directly by Claude) ───
	tools.RegisterPlan(srv)          // plan_deployment
	tools.RegisterDeploy(srv)        // deploy_project, deploy_inference, launch_training

	// ─── Management & queries (called directly by Claude) ───
	tools.RegisterManage(srv)        // status, logs, update, scale, destroy, migrate, rollback, restart
	tools.RegisterDomains(srv)       // add_domain, check_domain, remove_domain
	tools.RegisterMonitor(srv)       // get_metrics, set_alert, list_alerts, ...
	tools.RegisterEnvironments(srv)  // create_environment, list_environments, promote_environment, ...
	tools.RegisterTeam(srv)          // create_team, invite_member, ...
	tools.RegisterBilling(srv)       // top_up, get_balance, get_usage, ...
	tools.RegisterDatabase(srv)      // backup_database, list_backups, restore_database
	tools.RegisterCron(srv)          // create_cron, list_crons, delete_cron
	tools.RegisterAccount(srv)       // whoami, usage

	// ─── Internal use (called by the orchestrator, not directly by Claude) ───
	// provision_database, provision_cache, provision_storage → orchestrator.go
	// generate_secret, request_secrets → orchestrator.go

	return srv
}
```

---

## 4. OAuth Authentication Architecture

```
Claude Code               LaunchKit MCP Server              Auth0
    │                           │                              │
    │  POST /mcp (no token)     │                              │
    │─────────────────────────>│                              │
    │  401 + resource_metadata │                              │
    │<─────────────────────────│                              │
    │                           │                              │
    │  GET /.well-known/        │                              │
    │  oauth-protected-resource │                              │
    │─────────────────────────>│                              │
    │  {auth_servers:[auth0]}  │                              │
    │<─────────────────────────│                              │
    │                           │                              │
    │  GET /.well-known/oauth-authorization-server             │
    │─────────────────────────────────────────────────────────>│
    │  {endpoints...}                                          │
    │<─────────────────────────────────────────────────────────│
    │                           │                              │
    │  POST /oidc/register (DCR)                               │
    │─────────────────────────────────────────────────────────>│
    │  {client_id}                                             │
    │<─────────────────────────────────────────────────────────│
    │                           │                              │
    │  Browser → /authorize                                    │
    │─────────────────────────────────────────────────────────>│
    │          Google/GitHub login                              │
    │          ← redirect with code                            │
    │                           │                              │
    │  POST /oauth/token (code + PKCE verifier)                │
    │─────────────────────────────────────────────────────────>│
    │  {access_token, refresh_token}                           │
    │<─────────────────────────────────────────────────────────│
    │                           │                              │
    │  POST /mcp (Bearer token) │                              │
    │─────────────────────────>│                              │
    │                   verify JWT                             │
    │                   (Auth0 JWKS)                            │
    │                           │                              │
    │  {tool results}           │                              │
    │<─────────────────────────│                              │
```

### Implementation

Use a standard `http.Handler` middleware to proxy the OAuth flow to Auth0, and validate tokens with `golang-jwt`:

```go
// internal/auth/provider.go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/MicahParks/keyfunc/v3"
)

const auth0Domain = "launchkit.auth0.com"

var jwks *keyfunc.JWKS

func init() {
	var err error
	jwks, err = keyfunc.NewDefault([]string{
		fmt.Sprintf("https://%s/.well-known/jwks.json", auth0Domain),
	})
	if err != nil {
		panic(fmt.Sprintf("failed to init JWKS: %v", err))
	}
}

type AuthInfo struct {
	UserID   string
	ClientID string
	Scopes   []string
}

type ctxKey string
const authKey ctxKey = "auth"

func RequireBearerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		token, err := jwt.Parse(tokenStr, jwks.Keyfunc,
			jwt.WithAudience("https://mcp.launchkit.dev"),
			jwt.WithIssuer(fmt.Sprintf("https://%s/", auth0Domain)),
		)
		if err != nil || !token.Valid {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		claims := token.Claims.(jwt.MapClaims)
		info := AuthInfo{
			UserID:   claims["sub"].(string),
			ClientID: claims["azp"].(string),
		}
		if scope, ok := claims["scope"].(string); ok {
			info.Scopes = strings.Split(scope, " ")
		}

		ctx := context.WithValue(r.Context(), authKey, info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) AuthInfo {
	return ctx.Value(authKey).(AuthInfo)
}
```

### RBAC Middleware

```go
// internal/auth/rbac.go
package auth

import (
	"context"
	"fmt"
	"slices"

	"gorm.io/gorm"
)

type Role string

const (
	RoleOwner    Role = "owner"
	RoleAdmin    Role = "admin"
	RoleDeployer Role = "deployer"
	RoleViewer   Role = "viewer"
)

type Permission string

const (
	PermDeploy  Permission = "deploy"
	PermManage  Permission = "manage"
	PermView    Permission = "view"
	PermSecrets Permission = "secrets"
	PermBilling Permission = "billing"
	PermTeam    Permission = "team"
	PermDestroy Permission = "destroy"
)

var rolePermissions = map[Role][]Permission{
	RoleOwner:    {PermDeploy, PermManage, PermView, PermSecrets, PermBilling, PermTeam, PermDestroy},
	RoleAdmin:    {PermDeploy, PermManage, PermView, PermSecrets, PermBilling},
	RoleDeployer: {PermDeploy, PermManage, PermView},
	RoleViewer:   {PermView},
}

func RequirePermission(ctx context.Context, db *gorm.DB, userID, projectID string, perm Permission) error {
	var member struct{ Role string }
	err := db.WithContext(ctx).
		Table("team_members").
		Select("role").
		Where("user_id = ? AND project_id = ?", userID, projectID).
		First(&member).Error

	role := RoleViewer
	if err == nil {
		role = Role(member.Role)
	}

	perms := rolePermissions[role]
	if !slices.Contains(perms, perm) {
		return fmt.Errorf("insufficient permissions: requires '%s'", perm)
	}
	return nil
}
```

---

## 5. Data Model

```sql
-- ═══════════════════════════════════════════════
-- USERS & TEAMS
-- ═══════════════════════════════════════════════

CREATE TABLE users (
  id          TEXT PRIMARY KEY,           -- Auth0 user ID
  email       TEXT NOT NULL UNIQUE,
  name        TEXT,
  avatar_url  TEXT,
  card_fingerprint TEXT,                       -- credit card dedup (anti-abuse)
  first_top_up_claimed BOOLEAN DEFAULT false,  -- whether the first top-up bonus has been claimed (anti-abuse)
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE teams (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  name        TEXT NOT NULL,
  owner_id    TEXT NOT NULL REFERENCES users(id),
  balance          NUMERIC NOT NULL DEFAULT 0,     -- current balance (USD)
  lifetime_top_up  NUMERIC NOT NULL DEFAULT 0,     -- cumulative top-up amount (used to decide the first top-up bonus)
  low_balance_alert_at NUMERIC NOT NULL DEFAULT 2, -- warn when the balance falls below this value
  auto_top_up      BOOLEAN NOT NULL DEFAULT false, -- whether auto top-up is enabled
  auto_top_up_amount    NUMERIC,                   -- auto top-up amount
  auto_top_up_threshold NUMERIC,                   -- trigger auto top-up when the balance falls below this value
  stripe_customer_id TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE top_ups (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  amount      NUMERIC NOT NULL,                    -- top-up amount
  bonus       NUMERIC NOT NULL DEFAULT 0,          -- bonus amount (100% on the first top-up, capped at $5)
  stripe_payment_id TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  CONSTRAINT unique_stripe_payment UNIQUE (team_id, stripe_payment_id)  -- anti-abuse: the same card cannot claim the bonus twice
);

-- Anti-abuse rules:
-- 1. Each credit card (card_fingerprint) can claim the bonus only once
-- 2. First bonus = 100% of amount (capped at $5)
-- 3. After that, bonus = 0
-- 4. Check users.first_top_up_claimed to see whether it has been claimed

CREATE TABLE team_members (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id     TEXT NOT NULL REFERENCES users(id),
  role        TEXT NOT NULL DEFAULT 'viewer',  -- owner / admin / deployer / viewer
  projects    JSONB,                            -- null = all, or ["project_id", ...]
  invited_by  TEXT REFERENCES users(id),
  joined_at   TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(team_id, user_id)
);

-- ═══════════════════════════════════════════════
-- PROJECTS & ENVIRONMENTS
-- ═══════════════════════════════════════════════

CREATE TABLE projects (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     TEXT NOT NULL REFERENCES teams(id),
  name        TEXT NOT NULL,
  region      TEXT,                             -- default region
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(team_id, name)
);

CREATE TABLE environments (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,                    -- "production" | "staging" | custom
  status      TEXT NOT NULL DEFAULT 'active',   -- active / destroyed
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(project_id, name)
);

-- ═══════════════════════════════════════════════
-- SERVICES & DEPLOYMENTS
-- ═══════════════════════════════════════════════

CREATE TABLE services (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  type        TEXT NOT NULL,                    -- web / static / inference / training
  provider    TEXT NOT NULL,                    -- cloud_run / cloudflare_pages / skypilot / aws_ecs / ...
  region      TEXT,
  url         TEXT,
  status      TEXT NOT NULL DEFAULT 'pending',  -- pending / deploying / live / stopped / failed
  config      JSONB,                            -- cpu, memory, min/max instances
  cloud_account_id TEXT REFERENCES cloud_accounts(id),
  -- NULL = LaunchKit managed (all services are NULL in v1)
  -- NOT NULL = BYOC, compute runs in the team's own cloud account
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(environment_id, name)
);

CREATE TABLE always_on_addons (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE UNIQUE,
  size        TEXT NOT NULL,                    -- small / medium / large / xl
  vcpu        NUMERIC NOT NULL,                -- 0.25 / 0.5 / 1 / 2
  memory_mb   INTEGER NOT NULL,                -- 256 / 512 / 1024 / 2048
  monthly_fee NUMERIC NOT NULL,                -- $6 / $12 / $22 / $40
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE deployments (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',
  -- State machine (valid transitions):
  --   pending → provisioning → building → waiting_secrets → deploying → live
  --   any non-terminal state → failed
  --   live → stopped (manual pause)
  --   stopped → deploying (resume)
  -- Terminal: live / failed / stopped
  revision    TEXT,
  image_uri   TEXT,
  upload_url  TEXT,
  build_log   TEXT,
  error       TEXT,
  trigger     TEXT NOT NULL DEFAULT 'deploy',   -- deploy / update / rollback / promote_environment
  created_by  TEXT REFERENCES users(id),
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW(),        -- updated on every state change, used by crash recovery
  finished_at TIMESTAMPTZ
);

-- ── Deployment Steps (fine-grained progress tracking + crash recovery checkpoint) ──
-- Borrowed from SkyPilot's job_events_table: each parallel step tracks its own status independently.
-- The Orchestrator writes/updates the corresponding row after finishing each step.
-- Crash recovery reads this table to decide which steps are done and which need to be rerun.
-- The status tool reads this table and returns per-step progress to the user.

CREATE TABLE deployment_steps (
  id            TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  step          TEXT NOT NULL,
  -- step naming rule: {action}_{target}
  --   provision_postgres / provision_redis / provision_storage
  --   build_api / build_web / build_worker
  --   secrets (waiting for the user to fill them in)
  --   deploy_api / deploy_web / deploy_worker
  status        TEXT NOT NULL DEFAULT 'pending',
  -- pending → running → completed / failed / skipped
  provider_id   TEXT,             -- record the provider-side ID after the provision step succeeds (for idempotent retry + rollback)
  started_at    TIMESTAMPTZ,
  finished_at   TIMESTAMPTZ,
  error         TEXT,
  UNIQUE(deployment_id, step)
);

CREATE INDEX idx_deployment_steps_deployment ON deployment_steps(deployment_id);

-- ═══════════════════════════════════════════════
-- RESOURCES (DB, Cache, Storage)
-- ═══════════════════════════════════════════════

-- ═══════════════════════════════════════════════
-- CLOUD ACCOUNTS (BYOC — Bring Your Own Cloud)
-- ═══════════════════════════════════════════════
-- v1: this table exists but is empty; all resources go through LaunchKit managed (cloud_account_id = NULL)
-- When BYOC goes live: users connect GCP / AWS / Azure accounts in the Dashboard, which are written to this table
-- The matching GCP Service Account JSON / AWS IAM keys / Azure SP are stored with Tink envelope encryption
-- The Tink ciphertext contains the wrapped DEK, so a single BYTEA column is enough

CREATE TABLE cloud_accounts (
  id           TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id      TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,                   -- user-defined display name: "Production GCP"
  cloud        TEXT NOT NULL,                   -- 'gcp' | 'aws' | 'azure'
  encrypted_credentials BYTEA NOT NULL,         -- Tink AEAD ciphertext (contains the wrapped DEK + AES-256-GCM payload, AAD = team_id)
  status       TEXT NOT NULL DEFAULT 'active',  -- active / invalid / revoked
  created_at   TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(team_id, name)
);

CREATE TABLE resources (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  type        TEXT NOT NULL,                    -- database / cache / storage
  provider    TEXT NOT NULL,                    -- neon / turso / upstash / cloudflare_r2 / cloud_sql / rds / ...
  region      TEXT,
  provider_id TEXT,
  encrypted_credentials BYTEA NOT NULL,       -- Tink AEAD ciphertext (AAD = project_id)
  status      TEXT NOT NULL DEFAULT 'active',
  cloud_account_id TEXT REFERENCES cloud_accounts(id),
  -- NULL = LaunchKit managed (all resources are NULL in v1)
  -- NOT NULL = BYOC, uses the team's own cloud account
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- SECRETS & ENV BINDINGS
-- ═══════════════════════════════════════════════

CREATE TABLE user_secrets (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  encrypted_value BYTEA,                        -- Tink AEAD ciphertext (AAD = project_id, contains the wrapped DEK)
  status      TEXT NOT NULL DEFAULT 'pending',  -- pending / set
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(project_id, name)
);

CREATE TABLE secret_requests (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  keys        JSONB NOT NULL,
  status      TEXT NOT NULL DEFAULT 'pending',
  expires_at  TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE env_bindings (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  key         TEXT NOT NULL,
  value_type  TEXT NOT NULL,                    -- ref / secret / literal
  value       TEXT NOT NULL,
  UNIQUE(service_id, key)
);

-- ═══════════════════════════════════════════════
-- DOMAINS
-- ═══════════════════════════════════════════════

CREATE TABLE domains (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  domain      TEXT NOT NULL UNIQUE,
  dns_status  TEXT NOT NULL DEFAULT 'pending',  -- pending / configured / error
  ssl_status  TEXT NOT NULL DEFAULT 'pending',  -- pending / provisioning / active / error
  dns_records JSONB,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  verified_at TIMESTAMPTZ
);

-- ═══════════════════════════════════════════════
-- MONITORING & ALERTS
-- ═══════════════════════════════════════════════

CREATE TABLE alerts (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service_id  TEXT REFERENCES services(id) ON DELETE CASCADE,
  rules       JSONB NOT NULL,                   -- [{ metric, operator, threshold, window, severity }]
  channels    JSONB NOT NULL,                   -- [{ type, target }]
  status      TEXT NOT NULL DEFAULT 'active',   -- active / firing / paused
  last_triggered TIMESTAMPTZ,
  last_resolved TIMESTAMPTZ,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE incidents (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service_id  TEXT REFERENCES services(id) ON DELETE CASCADE,
  alert_id    TEXT REFERENCES alerts(id),
  description TEXT NOT NULL,
  impact      TEXT NOT NULL DEFAULT 'minor',    -- none / minor / major / critical
  started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at TIMESTAMPTZ,
  root_cause  TEXT
);

-- ═══════════════════════════════════════════════
-- CRON JOBS
-- ═══════════════════════════════════════════════

CREATE TABLE cron_jobs (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service_id  TEXT NOT NULL REFERENCES services(id),
  name        TEXT NOT NULL,
  schedule    TEXT NOT NULL,                    -- cron expression
  command     TEXT NOT NULL,
  timeout     TEXT NOT NULL DEFAULT '5m',
  status      TEXT NOT NULL DEFAULT 'active',   -- active / paused
  last_run    TIMESTAMPTZ,
  last_status TEXT,                             -- success / failed
  next_run    TIMESTAMPTZ,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(project_id, name)
);

-- ═══════════════════════════════════════════════
-- AUDIT & BILLING
-- ═══════════════════════════════════════════════

CREATE TABLE audit_logs (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     TEXT NOT NULL REFERENCES teams(id),
  actor_id    TEXT NOT NULL REFERENCES users(id),
  action      TEXT NOT NULL,                    -- deploy_project / provision_database / destroy / ...
  project_id  TEXT REFERENCES projects(id),
  service_id  TEXT REFERENCES services(id),
  details     JSONB,
  ip          TEXT,
  user_agent  TEXT,
  source      TEXT NOT NULL DEFAULT 'mcp',      -- mcp / dashboard / api
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_logs_team_time ON audit_logs(team_id, created_at DESC);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_id, created_at DESC);

CREATE TABLE usage_records (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id),
  service_id  TEXT REFERENCES services(id),
  resource_id TEXT REFERENCES resources(id),
  category    TEXT NOT NULL,                    -- compute / database / cache / storage / bandwidth / gpu
  quantity    NUMERIC NOT NULL,
  unit        TEXT NOT NULL,                    -- vcpu_seconds / gib_seconds / bytes / commands / gpu_seconds
  period_start TIMESTAMPTZ NOT NULL,
  period_end  TIMESTAMPTZ NOT NULL,
  cost_usd    NUMERIC NOT NULL,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_usage_project_period ON usage_records(project_id, period_start);

CREATE TABLE invoices (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id     TEXT NOT NULL REFERENCES teams(id),
  period      TEXT NOT NULL,                    -- "2026-03"
  status      TEXT NOT NULL DEFAULT 'draft',    -- draft / finalized / paid
  compute     NUMERIC NOT NULL DEFAULT 0,       -- Cloud Run compute cost
  database    NUMERIC NOT NULL DEFAULT 0,       -- Neon DB cost
  cache       NUMERIC NOT NULL DEFAULT 0,       -- Upstash Redis cost
  storage     NUMERIC NOT NULL DEFAULT 0,       -- R2 storage cost
  bandwidth   NUMERIC NOT NULL DEFAULT 0,       -- egress traffic cost
  always_on   NUMERIC NOT NULL DEFAULT 0,       -- Always-on fixed instance cost
  gpu_charges NUMERIC NOT NULL DEFAULT 0,       -- GPU cost
  total       NUMERIC NOT NULL DEFAULT 0,       -- sum of all items (= total deducted from the balance that month)
  pdf_url     TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  finalized_at TIMESTAMPTZ,
  UNIQUE(team_id, period)
);

CREATE TABLE budgets (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE UNIQUE,
  monthly_limit NUMERIC NOT NULL,
  alert_at    NUMERIC,
  action_at_limit TEXT NOT NULL DEFAULT 'alert_only', -- alert_only / scale_to_zero
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE backups (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  type        TEXT NOT NULL DEFAULT 'manual',   -- manual / automatic
  size_bytes  BIGINT,
  provider_backup_id TEXT,
  retention_until TIMESTAMPTZ,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);
```

---

## 6. Deployment Orchestration System

### 6.1 Three-Layer Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  Layer 1: Claude (perception + communication)                   │
│                                                                 │
│  ✓ Reads local files (only Claude has local file access)        │
│  ✓ Understands intent ("deploy this", "frontend only")          │
│  ✓ Judges project topology (which monorepo dirs are services)   │
│  ✓ Classifies env vars by meaning (STRIPE_KEY = user_required)  │
│  ✓ Produces hints (suggestions, not final decisions)            │
│                                                                 │
│  ✗ Makes no final decisions                                     │
│  ✗ Does not decide provider / resource sizing                   │
└────────────────────────┬────────────────────────────────────────┘
                         │ hints + source code upload
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│  Layer 2: Plan Engine (decision) ← core asset                   │
│                                                                 │
│  ✓ Detects the framework with Railpack (deterministic, no AI)   │
│  ✓ Parses package.json / go.mod / requirements.txt              │
│  ✓ Parses docker-compose.yml (if present)                       │
│  ✓ Validates Claude hints (framework: Railpack wins)            │
│  ✓ Adopts Claude hints (topology: what machines do poorly)      │
│  ✓ Produces an authoritative Plan (deterministic, testable)     │
│  ✓ Runs without Claude too (Dashboard / REST API)               │
│                                                                 │
│  100% deterministic · 100% testable · 100% reproducible         │
└────────────────────────┬────────────────────────────────────────┘
                         │ validated Plan
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│  Layer 3: Orchestrator (execution)                              │
│                                                                 │
│  ✓ Executes the Plan and makes no judgments                     │
│  ✓ In parallel: provision + build + secret → gate → deploy      │
│  ✓ Reports progress throughout (MCP progress / SSE)             │
│  ✓ Automatic rollback on failure                                │
└─────────────────────────────────────────────────────────────────┘
```

**Core principles:**

- **Claude is the perception layer, not the decision layer.** Claude's hints are suggestions; they take effect only after the Plan Engine validates them.
- **The Plan Engine is the core asset.** Deterministic Go code: testable, reproducible, independent of AI.
- **The Plan is first-class.** Persisted to Postgres, versioned, diffable, reusable.
- **It runs without Claude.** A Dashboard user uploads a zip → the Plan Engine analyzes it on its own → the Orchestrator executes.
- **Secrets never pass through Claude.** auto_inject / auto_generate / user_required are all handled by the Orchestrator.

### 6.2 `plan_deployment` Tool

After Claude scans the local project, it sends **hints** (suggestions, not commands) to the MCP. The backend Plan Engine validates the hints, runs Railpack, and produces the authoritative Plan.

```go
// internal/mcp/tools/plan.go

s.AddTool(mcp.NewTool("plan_deployment",
	mcp.WithDescription(`Submit project hints and receive a validated deployment plan.

Scan the local project and provide hints (suggestions — Plan Engine will verify):
1. Source topology: which directories are services, which are libraries
2. Framework hints (Plan Engine will verify with Railpack)
3. .env.example variables with your classification (auto_inject/auto_generate/user_required)
4. Monorepo info if applicable (turbo.json, pnpm-workspace.yaml)
5. build_context if different from service directory (e.g. monorepo root "/")

Plan Engine will verify your framework hints against Railpack detection,
validate paths, and may override your suggestions when deterministic analysis disagrees.`),
	mcp.WithObject("hints",
		mcp.Description("Structured project hints from local scan"),
		mcp.Required(),
	),
), planDeploymentHandler)
```

**Request (Claude Hints → MCP):**

```json
{
  "hints": {
    "project_name": "my-app",
    "sources": [
      {
        "id": "local_main",
        "type": "local",
        "local_path": "/Users/dev/projects/my-app",
        "description": "Turborepo monorepo with frontend and backend"
      }
    ],
    "services": [
      {
        "name": "api",
        "source_id": "local_main",
        "path": "apps/api/",
        "build_context": "/",
        "build_command": "turbo run build --filter=api",
        "framework_hint": "fastapi",
        "type": "backend"
      },
      {
        "name": "web",
        "source_id": "local_main",
        "path": "apps/web/",
        "build_context": "/",
        "build_command": "turbo run build --filter=web",
        "framework_hint": "nextjs",
        "type": "frontend"
      }
    ],
    "resources": [
      { "type": "postgres" }
    ],
    "env_hints": [
      { "name": "DATABASE_URL",        "classification": "auto_inject",  "reason": "prisma schema uses postgresql" },
      { "name": "STRIPE_SECRET_KEY",   "classification": "user_required","reason": "imports stripe SDK" },
      { "name": "SESSION_SECRET",      "classification": "auto_generate","reason": "random string for session signing" },
      { "name": "NEXT_PUBLIC_API_URL", "classification": "service_url",  "reason": "frontend needs backend URL at build time" }
    ],
    "source_size_bytes": 4200000
  }
}
```

**Source topology supports five modes:**

| Topology | Description | source.type | Phase |
|------|------|-------------|-------|
| Single App | One directory = one service | `local` (one) | 1 |
| Monorepo | One repo, several service subdirectories | `local` (one), services distinguished by path | 1 |
| Polyrepo | Several independent directories | `local` (several), each with its own upload URL | 2 |
| Mixed | Local + GitHub | `local` + `git` | 3 |
| Docker Compose | docker-compose.yml describes the topology | `local`, the Plan Engine parses the compose file itself | 2 |

Phase 1 supports local (single directory + monorepo); Phase 2+ extends it.

#### 6.2.1 Plan Engine (Deterministic Backend Analysis)

The Plan Engine is the core decision layer: deterministic Go code that does not depend on AI and can run on its own.

```go
// internal/deploy/engine.go
package deploy

type Engine struct {
	railpack    *railpack.Client
	providers   *provider.Registry
	storage     *storage.Client
	db          *gorm.DB
}

func (e *Engine) Generate(ctx context.Context, hints *Hints, uploadID string) (*Plan, error) {
	plan := &Plan{
		Version:   "v1",
		ID:        "plan_" + generateID(),
		ProjectID: hints.ProjectName,
		URLs:      make(map[string]string),
		CreatedAt: time.Now(),
	}

	// ── 1. Framework detection: Railpack is authoritative, the Claude hint is the fallback ───────
	for _, svcHint := range hints.Services {
		srcDir := e.storage.ExtractPath(uploadID, svcHint.Path)

		detected := e.railpack.Detect(srcDir) // deterministic detection

		svc := ServicePlan{
			Name:         svcHint.Name,
			Type:         svcHint.Type,
			SourceID:     svcHint.SourceID,
			Path:         svcHint.Path,
			BuildContext: svcHint.BuildContext, // topology judgment trusts Claude
			BuildCommand: svcHint.BuildCommand,
		}

		if detected.Framework != "" {
			svc.Framework = detected.Framework
			svc.DetectedBy = "railpack"
			if svcHint.FrameworkHint != detected.Framework {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"%s: Claude hint %q, Railpack detected %q — using Railpack",
					svc.Name, svcHint.FrameworkHint, detected.Framework))
			}
		} else if svcHint.FrameworkHint != "" {
			svc.Framework = svcHint.FrameworkHint
			svc.DetectedBy = "claude_hint"
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"%s: Railpack could not detect it, using Claude hint %q (unverified)",
				svc.Name, svcHint.FrameworkHint))
		}

		plan.Services = append(plan.Services, svc)
	}

	// ── 2. Env var classification: rules > Claude hint > safe default ────────────
	for _, envHint := range hints.EnvHints {
		classified := e.classifyEnv(envHint, plan)
		addToSecretPlan(&plan.Secrets, classified)
	}

	// ── 3. Provider selection + URL pre-decision ────────────────────────────
	for i, res := range hints.Resources {
		plan.Resources = append(plan.Resources, ResourcePlan{
			Type:     res.Type,
			Provider: e.providers.SelectBest(res.Type, res.Region),
			Region:   e.providers.ResolveRegion(res.Region),
		})
		_ = i
	}
	for i := range plan.Services {
		plan.Services[i].Target = e.selectTarget(plan.Services[i])
		plan.Services[i].URL = e.generateURL(plan.ProjectID, plan.Services[i].Name)
		plan.URLs[plan.Services[i].Name] = plan.Services[i].URL
	}

	// ── 4. Inject build args (inter-service URLs) ─────────────────────────
	for i := range plan.Services {
		if plan.Services[i].Type == "frontend" || plan.Services[i].Type == "static" {
			e.injectBuildArgs(&plan.Services[i], plan)
		}
	}

	// ── 5. Validate Plan completeness ─────────────────────────────────────
	plan.Validation = e.validate(plan)

	// ── 6. Persist the Plan (first-class) ───────────────────────────────
	e.db.WithContext(ctx).Create(plan)

	return plan, nil
}
```

```go
// internal/deploy/classify.go

// Env var classification: strict dependency matching first, the Claude hint as a backstop, a safe default when there is no information
func ClassifyEnv(key, hint, description, injectFrom string, availableResourceNames map[string]bool) (EnvVar, error) {
	entry := EnvVar{Key: key, Description: description}

	// Rule 1: InjectFrom explicitly specified + resource exists → auto_inject
	// Note: injectFrom points to a Resource Name (such as "main_db"), not a Type
	if injectFrom != "" {
		if !availableResourceNames[injectFrom] {
			// MCP feedback loop: refuse silent degradation and return an error so Claude can correct itself
			return entry, fmt.Errorf("invalid inject_from: resource '%s' not found in plan", injectFrom)
		}
		entry.Classification = "auto_inject"
		entry.Source = "provision_" + injectFrom
		return entry, nil
	}

	// Rule 2: VITE_* / NEXT_PUBLIC_* → build_arg
	if isBuildArg(key) {
		entry.Classification = "build_arg"
		return entry, nil
	}

	// Rule 3: the Claude hint specifies auto_generate and the name is not protected by the blocklist → auto_generate
	// We no longer guess blindly on _SECRET, to avoid accidentally overwriting external API keys (such as STRIPE_SECRET_KEY)
	if hint == "auto_generate" && isSecretKey(key) {
		entry.Classification = "auto_generate"
		entry.Strategy = "random_hex_32"
		return entry, nil
	}

	// Rule 4: the Claude hint as a backstop
	if hint != "" && hint != "auto_generate" {
		entry.Classification = hint
		return entry, nil
	}

	// Nothing matched → safe default: user_required (better to ask the user too often than to guess wrong)
	entry.Classification = "user_required"
	return entry, nil
}
```

**Trust boundary between Claude hints and the Plan Engine:**

| Item | Decided by | Reason |
|------|--------|------|
| Framework detection | **Railpack** (Plan Engine) | Deterministic scanning > AI guessing; Railpack supports 30+ frameworks across 11 languages |
| Project topology (which dirs are services) | **Claude hint** | Requires understanding of intent + semantics, hard for machines to infer |
| `build_context` (monorepo root) | **Claude hint** | Requires understanding of workspace config + import relationships |
| `build_command` override | **Claude hint** | Turborepo/Nx need special build commands |
| env var classification | **Rules > Claude hint > safe default** | Use a rule if there is one; otherwise trust Claude; if neither, user_required |
| Provider selection | **Plan Engine** | Pricing API + availability + region, no AI needed |
| URL pre-decision | **Plan Engine** | `{project}-{service}.launchkit.app`, deterministic |
| Cost estimation | **Plan Engine** | Historical data + provider pricing |

> **Railpack capability scope** (verified): Detects 11 languages in total, namely Node.js / Python / Go / PHP / Java / Ruby / Deno / Rust / Elixir / .NET / Shell, and automatically recognizes 30+ frameworks such as Next.js / FastAPI / Django / Rails / Spring Boot. Supports npm/yarn/pnpm/bun workspace detection, but **does not natively support Turborepo/Nx** (a Claude hint must supply build_command). The output is a BuildKit LLB build plan (not a Dockerfile).

#### 6.2.2 Plan Schema

The Plan is the system's core data structure: persisted to Postgres, versioned, diffable, reusable.

```go
// internal/deploy/plan.go
package deploy

type Plan struct {
	Version    string            `json:"version" gorm:"not null"`   // "v1"
	ID         string            `json:"id" gorm:"primaryKey"`      // "plan_xxx"
	ProjectID  string            `json:"project_id" gorm:"index"`
	Sources    []Source          `json:"sources" gorm:"serializer:json"`
	Services   []ServicePlan     `json:"services" gorm:"serializer:json"`
	Resources  []ResourcePlan    `json:"resources" gorm:"serializer:json"`
	Secrets    SecretPlan        `json:"secrets" gorm:"serializer:json"`
	URLs       map[string]string `json:"urls" gorm:"serializer:json"`

	CreatedAt  time.Time         `json:"created_at"`
	CreatedBy  string            `json:"created_by"`     // "mcp" | "dashboard" | "api"
	EstCost    string            `json:"estimated_cost"`
	Validation PlanValidation    `json:"validation" gorm:"serializer:json"`
	Warnings   []string          `json:"warnings,omitempty" gorm:"serializer:json"`
}

// Source — where the source code comes from (polymorphic: upload / git / image)
type Source struct {
	ID        string `json:"id"`        // "src_1"
	Type      string `json:"type"`      // "upload" | "git" | "image"
	UploadURL string `json:"upload_url,omitempty"` // presigned GCS URL (build staging)
	Repo      string `json:"repo,omitempty"`       // "github.com/user/repo"
	Branch    string `json:"branch,omitempty"`
	Image     string `json:"image,omitempty"`       // "myregistry/app:v1"
}

// ServicePlan — deployment spec of a single service
type ServicePlan struct {
	Name         string            `json:"name"`
	Type         string            `json:"type"`           // "web" | "worker" | "static" | "inference"
	Framework    string            `json:"framework"`      // "nextjs" | "fastapi" | "gin" ...
	DetectedBy   string            `json:"detected_by"`    // "railpack" | "claude_hint" | "dockerfile"

	SourceID     string            `json:"source_id"`      // → Source.ID
	Path         string            `json:"path"`           // service subdirectory ("apps/web/")
	BuildContext string            `json:"build_context"`  // build root directory (monorepo: "/")
	BuildCommand string            `json:"build_command,omitempty"`

	Target       string            `json:"target"`         // "cloud_run" | "cloudflare_pages"
	URL          string            `json:"url"`            // pre-decided URL
	DependsOn    []string          `json:"depends_on,omitempty"`
	BuildArgs    map[string]string `json:"build_args,omitempty"`
}

type ResourcePlan struct {
	Type     string `json:"type"`     // "postgres" | "redis" | "storage"
	Provider string `json:"provider"` // "neon" | "upstash" | "cloudflare_r2"
	Region   string `json:"region"`
}

type SecretPlan struct {
	AutoInject   []SecretEntry `json:"auto_inject"`
	AutoGenerate []SecretEntry `json:"auto_generate"`
	UserRequired []SecretEntry `json:"user_required"`
}

type SecretEntry struct {
	Name         string `json:"name"`
	Target       string `json:"target"`                // service name
	Hint         string `json:"hint,omitempty"`         // UI hint
	From         string `json:"from,omitempty"`         // auto_inject: "provision_postgres"
	Strategy     string `json:"strategy,omitempty"`     // auto_generate: "random_hex_32"
	ClassifiedBy string `json:"classified_by"`          // "rule" | "claude_hint" | "default"
}

type PlanValidation struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`   // blocking (the Plan Engine refuses to produce a Plan)
	Warnings []string `json:"warnings,omitempty"` // non-blocking (flagged but continues)
}
```

**Plan lifecycle:**

```
plan_deployment → Plan Engine produces → saved to Postgres → Claude shows it to the user
→ user confirms → Claude uploads the source code → deploy_project references the Plan ID
→ Orchestrator reads the Plan → executes

Afterwards you can use:
  plan diff: compare the Plans of two deployments
  plan reuse: "deploy the same as last time" → reuse a historical Plan
  plan rollback: roll back to an old Plan and redeploy
```

**Response (Plan Engine → Claude):**

```json
{
  "plan": {
    "version": "v1",
    "id": "plan_abc123",
    "project_id": "my-app",
    "sources": [
      { "id": "src_1", "type": "upload", "upload_url": "https://storage.googleapis.com/launchkit-builds/..." }
    ],
    "services": [
      {
        "name": "api",
        "type": "backend",
        "framework": "fastapi",
        "detected_by": "railpack",
        "source_id": "src_1",
        "path": "apps/api/",
        "build_context": "/",
        "build_command": "turbo run build --filter=api",
        "target": "cloud_run",
        "url": "https://my-app-api.launchkit.app"
      },
      {
        "name": "web",
        "type": "frontend",
        "framework": "nextjs",
        "detected_by": "railpack",
        "source_id": "src_1",
        "path": "apps/web/",
        "build_context": "/",
        "build_command": "turbo run build --filter=web",
        "target": "cloudflare_pages",
        "url": "https://my-app.launchkit.app",
        "build_args": { "NEXT_PUBLIC_API_URL": "https://my-app-api.launchkit.app" }
      }
    ],
    "resources": [
      { "name": "main_db", "type": "postgres", "provider": "neon", "region": "us-east-1" }
    ],
    "secrets": {
      "auto_inject": [
        { "name": "DATABASE_URL", "from": "provision_main_db", "target": "api" }
      ],
      "auto_generate": [
        { "name": "SESSION_SECRET", "strategy": "random_hex_32", "target": "api" }
      ],
      "user_required": [
        { "name": "STRIPE_SECRET_KEY", "target": "api", "hint": "Stripe Dashboard → Developers → API Keys" }
      ]
    },
    "estimated_cost": "$1.50/month (light usage)",
    "validation": { "valid": true },
    "warnings": ["api: Claude hint fastapi, Railpack confirmed ✓"]
  }
}
```

After Claude gets the validated Plan it shows it to the user (including warnings); once the user confirms, Claude uploads the source code to `sources[].upload_url`, then calls `deploy_project`.

### 6.3 `deploy_project` Tool

Whole-package deploy. The backend orchestrator handles provision, build and secret in parallel, then deploys once they are all ready.
This is a long-running tool call that reports progress throughout through MCP progress notifications.

```go
// internal/mcp/tools/deploy.go

s.AddTool(mcp.NewTool("deploy_project",
	mcp.WithDescription("Deploy a project based on a confirmed plan. Upload source code before calling. Returns when all services are live or failed."),
	mcp.WithString("plan_id", mcp.Description("Plan ID returned by plan_deployment. The plan is fetched from DB by plan_id; source tarball is at sources/{plan_id}/source.tar.gz on GCS."), mcp.Required()),
), deployProjectHandler)
```

### 6.4 Orchestrator Parallel Model

```
                ┌──────────────┐
           ┌────│ Upload code  │────┐
           │    └──────────────┘    │
           ▼         ▼         ▼    ▼
     ┌──────────┐ ┌────────┐ ┌────────┐ ┌──────────────┐
     │ Provision │ │Backend │ │Frontend│ │ Secret entry │
     │ Postgres  │ │ build  │ │ build  │ │(user browser)│
     └─────┬────┘ └───┬────┘ │+build  │ └──────┬───────┘
           │          │      │ arg    │        │
           │          │      └───┬────┘        │
           ▼          ▼          ▼             ▼
         ┌──────────────────────────────────────┐
         │     Dependency gate (all ready)      │
         └───────────────┬──────────────────────┘
               ┌─────────┴─────────┐
               ▼                   ▼
      ┌─────────────────┐ ┌─────────────────┐
      │ Backend deploy  │ │ Frontend deploy │
      │ Cloud Run       │ │ CF Pages        │
      └─────────────────┘ └─────────────────┘
```

**There is only one hard dependency: deploy must wait for all builds + secrets + provisioning to finish.** Everything else runs in parallel.

The frontend build does not need to wait for the backend deployment, because the URLs are decided in advance at `plan_deployment` time (we control the `*.launchkit.app` DNS).

```go
// internal/deploy/orchestrator.go
package deploy

import (
	"context"
	"sync"
	"time"
)

type DeployResult struct {
	Services []ServiceResult `json:"services"`
}

type ServiceResult struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"` // "live" | "failed"
	Error  string `json:"error,omitempty"`
}

// ─── Step Tracking ───────────────────────────────────
// Each parallel step tracks its own status independently and writes it to the deployment_steps table.
// Purpose: (1) when the user checks status they see per-step progress (2) crash recovery knows where to resume.
// Borrowed from SkyPilot job_events_table — every state transition is persisted.

func markStep(ctx context.Context, db *gorm.DB, deploymentID, step, status string, err error) {
	now := time.Now()
	updates := map[string]any{"status": status}
	if status == "running" {
		updates["started_at"] = now
	}
	if status == "completed" || status == "failed" {
		updates["finished_at"] = now
	}
	if err != nil {
		updates["error"] = err.Error()
	}

	// Upsert: INSERT the first time, UPDATE on retry (idempotent)
	db.WithContext(ctx).
		Where("deployment_id = ? AND step = ?", deploymentID, step).
		Assign(updates).
		FirstOrCreate(&DeploymentStep{DeploymentID: deploymentID, Step: step})

	// Also update deployments.updated_at (crash recovery uses it to decide staleness)
	db.WithContext(ctx).Model(&Deployment{}).
		Where("id = ?", deploymentID).
		Update("updated_at", now)
}

func markStepProviderID(ctx context.Context, db *gorm.DB, deploymentID, step, providerID string) {
	db.WithContext(ctx).Model(&DeploymentStep{}).
		Where("deployment_id = ? AND step = ?", deploymentID, step).
		Update("provider_id", providerID)
}

// isStepCompleted checks whether a step has completed (used by crash recovery to skip finished steps)
func isStepCompleted(ctx context.Context, db *gorm.DB, deploymentID, step string) bool {
	var s DeploymentStep
	err := db.WithContext(ctx).
		Where("deployment_id = ? AND step = ? AND status = 'completed'", deploymentID, step).
		First(&s).Error
	return err == nil
}

// ─── Execute: main orchestration logic ─────────────────────────────
// Write a checkpoint to deployment_steps after each step completes.
// Build steps are queued through the river job queue to control BuildKit VM concurrency (MaxWorkers: 3).

func Execute(ctx context.Context, db *gorm.DB, plan Plan, uploadID string, progress func(pct int, msg string)) (*DeployResult, error) {
	var wg sync.WaitGroup
	gate := NewGate(plan)
	deploymentID := plan.DeploymentID

	// Update deployment state
	db.WithContext(ctx).Model(&Deployment{}).Where("id = ?", deploymentID).
		Update("status", "provisioning")

	// ─── Parallel phase ─────────────────────────────────────

	// A. Provision resources (idempotent: look up first, then create; see the provider layer)
	for _, res := range plan.Resources {
		stepName := "provision_" + res.Type
		if isStepCompleted(ctx, db, deploymentID, stepName) {
			continue // crash recovery: skip the ones already completed
		}
		wg.Add(1)
		go func(res Resource, stepName string) {
			defer wg.Done()
			markStep(ctx, db, deploymentID, stepName, "running", nil)
			ref, err := provisionResource(ctx, res) // idempotent operation
			if err != nil {
				markStep(ctx, db, deploymentID, stepName, "failed", err)
				gate.Fail(stepName, err)
				return
			}
			markStepProviderID(ctx, db, deploymentID, stepName, ref.ProviderID)
			markStep(ctx, db, deploymentID, stepName, "completed", nil)
			gate.Complete(stepName, ref)
			progress(10, "Provisioned "+res.Type)
		}(res, stepName)
	}

	// B. Auto-generate secrets
	for _, sec := range plan.Secrets.AutoGenerate {
		stepName := "secret_" + sec.Name
		if isStepCompleted(ctx, db, deploymentID, stepName) {
			continue
		}
		wg.Add(1)
		go func(sec Secret, stepName string) {
			defer wg.Done()
			markStep(ctx, db, deploymentID, stepName, "running", nil)
			ref, _ := generateAndStoreSecret(ctx, sec)
			markStep(ctx, db, deploymentID, stepName, "completed", nil)
			gate.Complete(stepName, ref)
		}(sec, stepName)
	}

	// C. Create the user_required secret request
	if len(plan.Secrets.UserRequired) > 0 {
		stepName := "secrets"
		if !isStepCompleted(ctx, db, deploymentID, stepName) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				markStep(ctx, db, deploymentID, stepName, "running", nil)
				reqURL, _ := createSecretRequest(ctx, plan.Secrets.UserRequired)
				progress(5, "Waiting for secrets: "+reqURL)
				waitForSecretCompletion(ctx, reqURL)
				markStep(ctx, db, deploymentID, stepName, "completed", nil)
				gate.Complete(stepName, nil)
				progress(15, "Secrets received")
			}()
		}
	}

	// D. Build all services — queued through the river job queue
	// The river build worker is configured with MaxWorkers: 3 to control BuildKit VM concurrency.
	// Builds beyond that wait in the queue, so they never overwhelm the VM at the same time.
	db.WithContext(ctx).Model(&Deployment{}).Where("id = ?", deploymentID).
		Update("status", "building")

	for _, svc := range plan.Services {
		stepName := "build_" + svc.Name
		if isStepCompleted(ctx, db, deploymentID, stepName) {
			continue
		}
		wg.Add(1)
		go func(svc Service, stepName string) {
			defer wg.Done()
			markStep(ctx, db, deploymentID, stepName, "running", nil)
			progress(20, "Building "+svc.Name+"...")

			// Enqueue into the river queue and wait for a build worker to handle it (MaxWorkers: 3)
			artifact, err := enqueueBuildAndWait(ctx, uploadID, svc)
			if err != nil {
				markStep(ctx, db, deploymentID, stepName, "failed", err)
				gate.Fail(stepName, err)
				return
			}
			markStep(ctx, db, deploymentID, stepName, "completed", nil)
			gate.Complete(stepName, artifact)
			progress(60, "Built "+svc.Name)
		}(svc, stepName)
	}

	wg.Wait()

	// ─── Gate check ────────────────────────────────────
	if err := gate.CheckAll(); err != nil {
		db.WithContext(ctx).Model(&Deployment{}).Where("id = ?", deploymentID).
			Updates(map[string]any{"status": "failed", "error": err.Error(), "finished_at": time.Now()})
		rollbackProvisioned(ctx, gate)
		return nil, err
	}

	// ─── Parallel deploy ─────────────────────────────────
	db.WithContext(ctx).Model(&Deployment{}).Where("id = ?", deploymentID).
		Update("status", "deploying")
	progress(80, "Deploying all services...")
	results := deployAllServices(ctx, db, deploymentID, plan, gate) // internally markStep as well
	progress(100, "Deployment complete")

	db.WithContext(ctx).Model(&Deployment{}).Where("id = ?", deploymentID).
		Updates(map[string]any{"status": "live", "finished_at": time.Now()})

	return results, nil
}
```

#### 6.4.1 Crash Recovery (Recovering Stuck Deployments When the API Server Starts)

> **Problem**: Cloud Run can restart an instance at any time (scaling, OOM, a new version deploying). Once the in-flight orchestrator goroutine is gone, the deployment stays stuck in a non-terminal state forever.
>
> **Borrowed from**: SkyPilot `reset_jobs_for_recovery()` (`sky/jobs/state.py:2679`) — when the server starts it scans all non-terminal jobs and clears the PID so the scheduler picks them up again. SkyPilot `_recover_replica_operations()` (`sky/serve/replica_managers.py:771`) — reads the replica state and resumes interrupted provision/termination.
>
> **Precondition**: all provision/build operations must be **idempotent** (see 7.6 Provider Idempotency Design), so a rerun never creates duplicate resources.

```go
// internal/worker/recovery.go
package worker

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// RecoverStaleDeployments is called when the API Server starts.
// It scans all deployments stuck in a non-terminal state for more than 5 minutes,
// reads deployment_steps to determine progress, and re-enqueues the unfinished steps into the river queue.
//
// When it is called: in main() of cmd/api/main.go, immediately after the river workers start.
func RecoverStaleDeployments(ctx context.Context, db *gorm.DB, riverClient *river.Client) error {
	logger := slog.With("job", "crash_recovery")

	var stale []Deployment
	err := db.WithContext(ctx).
		Where("status NOT IN ? AND updated_at < ?",
			[]string{"live", "failed", "stopped"},
			time.Now().Add(-5*time.Minute)).
		Find(&stale).Error
	if err != nil {
		return err
	}

	if len(stale) == 0 {
		return nil
	}
	logger.Info("found stale deployments", "count", len(stale))

	for _, dep := range stale {
		// Read deployment_steps to decide which steps have completed
		var steps []DeploymentStep
		db.WithContext(ctx).Where("deployment_id = ?", dep.ID).Find(&steps)

		completedSteps := make(map[string]bool)
		hasFailure := false
		for _, s := range steps {
			if s.Status == "completed" {
				completedSteps[s.Step] = true
			}
			if s.Status == "failed" {
				hasFailure = true
			}
		}

		if hasFailure {
			// A step has failed → mark the whole deployment as failed and trigger a rollback
			logger.Warn("stale deployment has failed steps, marking failed",
				"deployment_id", dep.ID)
			db.WithContext(ctx).Model(&dep).
				Updates(map[string]any{"status": "failed", "finished_at": time.Now()})
			// Roll back the resources already created (read from deployment_steps.provider_id)
			rollbackFromSteps(ctx, db, dep.ID, steps)
			continue
		}

		// Re-enqueue into the river queue; the orchestrator skips completed steps automatically
		logger.Info("re-enqueuing stale deployment",
			"deployment_id", dep.ID,
			"completed_steps", len(completedSteps))

		_, err := riverClient.Insert(ctx, &DeployJobArgs{
			DeploymentID: dep.ID,
			PlanID:       dep.PlanID,
			IsRecovery:   true, // marked as recovery, the orchestrator skips completed steps with isStepCompleted
		}, nil)
		if err != nil {
			logger.Error("failed to re-enqueue deployment",
				"deployment_id", dep.ID, "error", err)
		}
	}
	return nil
}
```

```go
// cmd/api/main.go — call recovery at startup (after the river workers start)
func main() {
	// ... server setup ...

	// Start the river workers
	riverClient.Start(ctx)

	// Crash recovery: recover stuck deployments
	if err := worker.RecoverStaleDeployments(ctx, db, riverClient); err != nil {
		slog.Error("crash recovery failed", "error", err)
		// Not fatal — a recovery failure should not stop the server from starting
	}

	// ... http.ListenAndServe ...
}
```

### 6.5 The Three Types of Secrets

| Type | Example | Handling | When |
|------|------|---------|------|
| **auto_inject** | `DATABASE_URL`, `REDIS_URL` | Generated automatically after the resource is provisioned, stored encrypted with Tink AEAD | After provisioning completes |
| **auto_generate** | `NEXTAUTH_SECRET`, `JWT_SECRET` | Generated with `crypto/rand`, stored encrypted with Tink AEAD | When the orchestrator starts |
| **user_required** | `STRIPE_SECRET_KEY`, `GOOGLE_CLIENT_ID` | A secure entry page is generated and the user fills it in in the browser | Wait until the user finishes |

**Secrets never pass through Claude.** Claude only sees refs; the values exist only along: user's browser → HTTPS → LaunchKit API → Tink encryption (AAD = project_id) → Postgres ciphertext.

### 6.6 Secure Secret Entry Flow

```
The user opens https://launchkit.dev/s/{secret_request_id}

┌─────────────────────────────────────────────────────────────┐
│  LaunchKit · Secure Secret Entry                            │
│                                                             │
│  Project: my-app                                            │
│  Requested by: alan@company.com                             │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ STRIPE_SECRET_KEY                                   │    │
│  │ Stripe Dashboard → Developers → API Keys            │    │
│  │ ┌───────────────────────────────────────────────┐   │    │
│  │ │ sk_live_<your-stripe-secret-key>              │   │    │
│  │ └───────────────────────────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐    │
│  │ GOOGLE_CLIENT_ID                                    │    │
│  │ Google Cloud Console → OAuth                        │    │
│  │ ┌───────────────────────────────────────────────┐   │    │
│  │ │ 123456789.apps.googleusercontent...           │   │    │
│  │ └───────────────────────────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                             │
│              [ ✓ Submit and store encrypted ]               │
│                                                             │
│  🔒 AES-256-GCM encrypted, cannot be viewed once submitted  │
└─────────────────────────────────────────────────────────────┘

After submission:
  → HTTPS POST to the LaunchKit API
  → Each value is encrypted with Tink KMSEnvelopeAEAD2 (AAD = project_id, DEK + AES-256-GCM generated automatically)
  → Stored in Postgres (a single BYTEA ciphertext containing the wrapped DEK)
  → The original value never touches disk and is never logged
  → Redis pub/sub notifies the orchestrator: secret ready
  → The orchestrator gate opens and the deployment continues
```

### 6.7 Full Flow (Separate Frontend/Backend Example)

```
═══ User's computer (Claude Code) ════════════════════════════════════

User: "Help me deploy this project"

① Claude scans locally and produces hints
   → "I observed FastAPI + React Vite + Postgres"
   → "DATABASE_URL = auto_inject, STRIPE_SECRET_KEY = user_required"
   → hints are suggestions, not final decisions

② Claude calls plan_deployment({ hints })

═══ LaunchKit cloud ══════════════════════════════════════════════════

③ Plan Engine processing:
   → Railpack detects frameworks: FastAPI ✓, Vite-React ✓ (validates the Claude hint)
   → Rule-based env classification: DATABASE_URL → auto_inject (matched the prisma rule ✓)
   → Claude hint classification: STRIPE_SECRET_KEY → user_required (no rule matched, the hint is adopted)
   → Provider selection: Postgres → Neon, Frontend → Cloudflare Pages
   → URL pre-decision: my-app-api.launchkit.app, my-app.launchkit.app
   → Validate Plan → valid ✓
   → Persist the Plan to Postgres
   → Return the validated Plan + presigned upload URL

═══ User's computer ══════════════════════════════════════════════════

④ Claude shows the plan to the user, and the user confirms

⑤ Claude uploads the source code to GCS (the user's computer takes part for the last time)
   git archive HEAD | gzip | curl -XPUT '<presigned_url>'
   or
   curl -XPUT --data-binary @project.zip '<presigned_url>'

⑥ Claude calls deploy_project({ plan_id })

═══ LaunchKit cloud (fully automatic from here) ══════════════════════

⑦ The Orchestrator starts four things in parallel:

   A. Provision Neon Postgres ──────→ connection string → Tink encryption (AAD = project_id) → ref:DATABASE_URL
   B. Create Secret Request ────────→ https://launchkit.dev/s/sr_xxx
   C. Backend build (FastAPI) ──────→ railpack → BuildKit → image ready
   D. Frontend build (Vite React) ──→ railpack → BuildKit → dist/ ready
      build arg: VITE_API_URL = https://my-app-api.launchkit.app (URL is known in advance)

⑧ Claude receives the secure link and tells the user:
   "Please go to https://launchkit.dev/s/sr_xxx and fill in STRIPE_SECRET_KEY"

═══ User's browser ═══════════════════════════════════════════════════

⑨ The user opens the link, fills it in → submits → Tink encrypts it into Postgres

═══ LaunchKit cloud ══════════════════════════════════════════════════

⑩ Gate: A + B + C + D all ready → continue

⑪ Parallel deploy:
   Backend: Tink decrypts env → Cloud Run deploy → DNS → health check
   Frontend: dist/ → Cloudflare Pages API → DNS

⑫ Return the results:
   Backend https://my-app-api.launchkit.app
   Frontend https://my-app.launchkit.app
```

### 6.8 Build Pipeline

The build pipeline is integrated into the API Server process: **railpack prepare runs inside the API Server (a lightweight 2-3s operation), and the image build is done by the BuildKit VM.**

The frontend and backend use the same build pipeline; the only difference is the deploy target:

| | Backend service | Frontend static |
|---|---|---|
| Build tool | Railpack → BuildKit → container image | Railpack → BuildKit → static output (dist/) |
| Build-time env | Not needed (injected at runtime) | `VITE_*` / `NEXT_PUBLIC_*` via build args |
| Deploy target | Cloud Run | Cloudflare Pages |
| Runtime env | Cloud Run env vars (injected after Tink decryption) | None (static files) |

**The build needs no runtime secrets.** The image is generic, and secrets are injected into Cloud Run env vars only at the deploy stage. So the build and secret entry can run in parallel.

### 6.9 Public Upload API (Dashboard / CLI / Third-Party Integrations)

In addition to the MCP tools, a REST API lets the Dashboard, the CLI or third-party tools upload an archive directly and deploy.
**This path has no Claude — the Plan Engine analyzes the source code on its own and produces the Plan itself.**

```
Stage 1: POST /v1/upload (request a presigned URL)
  → { upload_id, presign_url, presign_method, presign_header, expires_in }

Stage 2: PUT <presign_url> (upload directly to GCS, bypassing the API Server)
  → Supports application/zip | application/gzip

Stage 3: POST /v1/upload/{upload_id}/plan (the Plan Engine analyzes the source code)
  → The Plan Engine automatically does: Railpack detection + dependency parsing + docker-compose parsing
  → No Claude hints → rule-based env classification (classified if a rule matches, otherwise user_required)
  → { plan } (the user confirms the Plan in the Dashboard)

Stage 4: POST /v1/upload/{upload_id}/deploy (confirm the Plan, trigger the deploy)
  → { deployment_id, status, watch_url: "/v1/deployments/{id}/events" }
```

**Progress tracking (SSE):**
```
GET /v1/deployments/{deployment_id}/events
Authorization: Bearer <token>
Accept: text/event-stream

← data: {"pct": 10, "message": "Provisioning database..."}
← data: {"pct": 20, "message": "Building api..."}
← data: {"pct": 60, "message": "Building web..."}
← data: {"pct": 80, "message": "Deploying all services..."}
← data: {"pct": 100, "status": "live", "urls": {...}}
```

**Comparison of the two paths:**

| | MCP path (Claude) | REST path (Dashboard) |
|---|---|---|
| hints source | Produced by Claude reading local files | None (the Plan Engine analyzes on its own) |
| Plan Engine input | hints + source code | Source code only |
| Framework detection accuracy | Railpack + Claude hint fallback | Railpack only |
| env var classification | Rules > Claude hint > user_required | Rules > user_required (no hint layer) |
| User experience | Natural language, Claude explains the Plan | The Dashboard UI shows the Plan |
| Progress reporting | MCP progress notifications | SSE |

**Shared core: Plan Engine + Orchestrator.** The differences are only the hints source and the UI layer.

### 6.10 BuildKit VM Specification

The build pipeline is integrated into the API Server process: **railpack prepare runs inside the API Server (a lightweight 2-3s operation), and the image build is done by the BuildKit VM. No separate Build Worker Cloud Run Job is needed.**

```
BuildKit VM (primary, enabled as soon as the beta goes live):
  Machine:    e2-standard-4 (4 vCPU, 16 GiB)
  Disk:       100GB pd-ssd (BuildKit cache)
  Zone:       us-east4-c
  Service:    buildkitd always running, TCP :1234
  Network:    API Server connects directly over the VPC internal network
  Cache:      Local SSD, managed automatically by BuildKit GC (keepBytes=80GB, keepDuration=720h)
  Pre-warm:   Pre-build common Python/Node/Rust/Go framework caches before the beta goes live
  Measured:   Python cached 7.4s, Next.js cached 58.5s (vs Cloud Build 85-197s)

  ── Concurrency control (river build worker) ──
  MaxWorkers: 3 (4 vCPU ÷ ~1.3 vCPU/build, leaving 0.1 vCPU for the buildkitd daemon)
  Builds beyond that wait in the river queue, and the user sees "build queued, position N"
  Why not 4: heavy builds (Rust, large monorepos) use 2+ vCPU, and 3 in parallel leaves headroom
  Phase 2: VM pool + QueueLengthAutoscaler (see 6.10.2 below)

  ── Idle management (automatic stop/start) ──
  30 min with no build → stop the VM (the pd-ssd is kept, stopped-disk cost $17/month)
  A build request arrives → start the VM + wait for TCP :1234 to be ready (~30s cold start)
  The timer keeps resetting while a build is running, so the VM never stops mid-build
  Estimated 40-60% VM cost savings (depending on the team's time zone distribution)

Cloud Build (fallback, automatic degradation when the VM is unhealthy):
  Uses docker:27 + the docker-container driver (verified 2026-04-01)
  Cold build 77-197s (no cache, but functionally correct)
  Cloud Build free allowance of 2,500 min/month as a safety net
  Needs a GCS relay (source + plan uploaded to a GCS bucket)
```

#### 6.10.1 Build Concurrency Control

> **Problem**: 8 users build at the same time → a single e2-standard-4 (4 vCPU) → every build fights for CPU → cached build balloons from 7s to 50s+ → heavy builds (Rust/Go monorepo) OOM outright → buildkitd crashes → all builds fail.
>
> **Borrowed from**: SkyPilot `sky/jobs/scheduler.py` uses `LAUNCHES_PER_WORKER` to limit the number of jobs launching at the same time; the rest queue and wait.

```go
// internal/worker/build.go — river build worker
package worker

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"
)

type BuildJobArgs struct {
	DeploymentID string `json:"deployment_id"`
	ServiceName  string `json:"service_name"`
	UploadID     string `json:"upload_id"`
	ImageTag     string `json:"image_tag"`
}

func (BuildJobArgs) Kind() string { return "build" }

type BuildWorker struct {
	river.WorkerDefaults[BuildJobArgs]
	buildkit *BuildKitManager
}

func (w *BuildWorker) Work(ctx context.Context, job *river.Job[BuildJobArgs]) error {
	// BuildKit VM idle management: make sure the VM is running, and start it if it is stopped
	if err := w.buildkit.EnsureRunning(ctx); err != nil {
		// VM failed to start → degrade to Cloud Build
		return buildWithCloudBuild(ctx, job.Args)
	}

	return buildWithBuildKitVM(ctx, job.Args)
}

// Set MaxWorkers: 3 at registration
// river.AddWorker(workers, &BuildWorker{buildkit: bkm}, &river.AddWorkerOpts{
//     MaxWorkers: 3, // at most 3 concurrent builds on a 4 vCPU VM
// })
```

```go
// Build step in the orchestrator: enqueue into the river queue instead of using a bare goroutine
func enqueueBuildAndWait(ctx context.Context, riverClient *river.Client, uploadID string, svc ServicePlan) (*BuildArtifact, error) {
	job, err := riverClient.Insert(ctx, &BuildJobArgs{
		DeploymentID: svc.DeploymentID,
		ServiceName:  svc.Name,
		UploadID:     uploadID,
		ImageTag:     fmt.Sprintf("%s/%s:%s", artifactRegistry, svc.Name, svc.DeploymentID),
	}, nil)
	if err != nil {
		return nil, err
	}

	// Wait for the river job to finish (polling job status)
	return waitForBuildJob(ctx, riverClient, job.ID)
}
```

#### 6.10.2 BuildKit VM Idle Management

> **Problem**: An always-on e2-standard-4 = $115/month. Between 10pm and 8am (10 hours) nobody builds, so ~300 hours a month sit idle = ~$40 burned for nothing. The loss multiplies with multiple VMs.
>
> **Borrowed from**: SkyPilot `AutostopEvent` (`sky/skylet/events.py:161-222`) — checks `is_cluster_idle()` every 60 seconds, and stops or terminates the cluster once it has been idle longer than `autostop_idle_minutes`. Key design: the timer resets while a job is running, so the cluster never stops mid-job.

```go
// internal/monitor/buildkit.go
package monitor

import (
	"context"
	"log/slog"
	"net"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
)

const (
	buildKitIdleTimeout = 30 * time.Minute // no build for 30 min → stop
	buildKitCheckInterval = 5 * time.Minute // check once every 5 min
	buildKitStartTimeout = 60 * time.Second // VM start timeout
)

type BuildKitManager struct {
	project  string
	zone     string
	instance string
	vmState  string // "running" / "stopped" / "unknown"
	client   *compute.InstancesClient
}

// EnsureRunning makes sure the VM is running. The build worker calls it before every build.
// Idempotent: returns immediately if it is already running. If it is stopped, it starts it and waits for TCP :1234 to be ready.
func (m *BuildKitManager) EnsureRunning(ctx context.Context) error {
	if m.vmState == "running" && m.isTCPReady(ctx) {
		return nil
	}

	slog.Info("starting BuildKit VM", "instance", m.instance)
	op, err := m.client.Start(ctx, &computepb.StartInstanceRequest{
		Project: m.project, Zone: m.zone, Instance: m.instance,
	})
	if err != nil {
		return err
	}
	if err := op.Wait(ctx); err != nil {
		return err
	}

	// Wait for buildkitd TCP :1234 to be ready
	deadline := time.Now().Add(buildKitStartTimeout)
	for time.Now().Before(deadline) {
		if m.isTCPReady(ctx) {
			m.vmState = "running"
			slog.Info("BuildKit VM ready", "instance", m.instance)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("BuildKit VM start timeout after %s", buildKitStartTimeout)
}

func (m *BuildKitManager) isTCPReady(ctx context.Context) bool {
	conn, err := net.DialTimeout("tcp", m.instance+":1234", 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// StartIdleMonitor runs as a river periodic job, checking once every 5 minutes.
// Borrows the idle detection of SkyPilot AutostopEvent:
// - A build is running → reset the timer, do not stop
// - Idle for more than 30 min → stop the VM (keep the pd-ssd cache)
func (m *BuildKitManager) CheckIdle(ctx context.Context, db *gorm.DB) error {
	if m.vmState != "running" {
		return nil // already stopped, no need to check again
	}

	// Check whether a build is running (river job state = 'running' AND kind = 'build')
	var runningBuilds int64
	db.WithContext(ctx).Raw(`
		SELECT count(*) FROM river_job
		WHERE kind = 'build' AND state = 'running'
	`).Scan(&runningBuilds)

	if runningBuilds > 0 {
		return nil // a build is running, do not stop
	}

	// Look up when the last build finished
	var lastFinished time.Time
	db.WithContext(ctx).Raw(`
		SELECT COALESCE(MAX(finished_at), '1970-01-01')
		FROM deployment_steps
		WHERE step LIKE 'build_%' AND status = 'completed'
	`).Scan(&lastFinished)

	idle := time.Since(lastFinished)
	if idle < buildKitIdleTimeout {
		return nil // has not yet passed the idle threshold
	}

	slog.Info("BuildKit VM idle, stopping",
		"idle_minutes", int(idle.Minutes()),
		"threshold_minutes", int(buildKitIdleTimeout.Minutes()))

	op, err := m.client.Stop(ctx, &computepb.StopInstanceRequest{
		Project: m.project, Zone: m.zone, Instance: m.instance,
	})
	if err != nil {
		return err
	}
	if err := op.Wait(ctx); err != nil {
		return err
	}

	m.vmState = "stopped"
	// pd-ssd costs only $0.17/GB/month while stopped (100GB = $17/month), and the build cache is fully preserved
	return nil
}
```

#### 6.10.3 BuildKit Multi-Tenant Isolation (Required in Phase 1)

> **Problem**: BuildKit was not designed for multi-tenancy ([moby/buildkit#5796](https://github.com/moby/buildkit/discussions/5796)). Users' build steps such as `npm install` and `pip install` execute arbitrary code from package.json / setup.py. On a shared VM, a malicious user may attempt container escape, cache poisoning, or scanning of the VPC internal network.
>
> **Background**: The 2024 "Leaky Vessels" incident (CVE-2024-23652 CVSS 10.0 — arbitrary host file deletion, CVE-2024-23653 CVSS 9.8 — privilege escalation creating a privileged container) proved that the risk of runc in multi-tenant scenarios is real. runc has 1-3 container escape CVEs every year, while gVisor has historically had zero confirmed escape-to-host.
>
> **Decision**: Adopt gVisor (runsc) as the BuildKit containerd worker runtime + CNI network isolation. **Cache sharing is not sacrificed** — gVisor isolates the "execution layer" (RUN steps run in the gVisor sandbox), not the "storage layer" (the content-addressed cache is managed by the buildkitd daemon, outside gVisor).

**Five-layer defense architecture:**

```
┌──────────────────────────────────────────────────────────────────────────────────────────┐
│  Tier 1: gVisor execution isolation                                                      │
│  buildkitd containerd worker → runsc runtime                                             │
│  Each RUN step runs in a gVisor sandbox (syscalls intercepted by a user-space kernel)    │
│  On a GCE VM it uses the systrap platform (no nested virtualization needed)              │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│  Tier 2: CNI network isolation                                                           │
│  Each build gets its own network namespace (bridge + veth pair)                          │
│  iptables: block metadata 169.254.169.254 + the VPC internal network 10.128.0.0/9        │
│  Only outbound to package registries (npm, PyPI, crates.io, etc.) is allowed             │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│  Tier 3: Cache hygiene                                                                   │
│  named cache mount IDs get a project prefix (prevents cross-tenant cache reads/writes)   │
│  content-addressed layer cache is still shared as usual (safe by design)                 │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│  Tier 4: BuildKit hardening                                                              │
│  security.insecure entitlement is forbidden                                              │
│  network.host entitlement is forbidden                                                   │
│  max-parallelism = 4                                                                     │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│  Tier 5: Host hardening                                                                  │
│  The VPC internal network has no public IP                                               │
│  firewall only allows API Server → TCP :1234                                             │
│  Ubuntu unattended-upgrades applies security updates automatically                       │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

**Tier 1: gVisor execution isolation (core)**

The VM uses Ubuntu 22.04/24.04 LTS, with containerd + runsc + containerd-shim-runsc-v1 installed.

```toml
# /etc/containerd/config.toml
version = 2
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
  runtime_type = "io.containerd.runsc.v1"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc.options]
  TypeUrl = "io.containerd.runsc.v1.options"
```

```toml
# /etc/buildkit/buildkitd.toml
[worker.oci]
  enabled = false

[worker.containerd]
  enabled = true
  namespace = "buildkit"

  # gVisor runtime — each RUN step runs in a runsc sandbox
  [worker.containerd.runtime]
    name = "io.containerd.runsc.v2"

  [[worker.containerd.gcpolicy]]
    keepBytes = 85899345920   # 80GB
    keepDuration = 2592000    # 720h

  # CNI network isolation
  [worker.containerd.cni]
    binaryDir = "/opt/cni/bin"
    confDir = "/etc/buildkit/cni"
    poolSize = 16

[registry."us-east4-docker.pkg.dev"]
  # Artifact Registry — used by buildkitd to push images
  # Uses the GCE VM's default service account, no extra credentials needed
```

**Tier 2: CNI network isolation**

```json
// /etc/buildkit/cni/10-buildkit.conflist
{
  "cniVersion": "1.0.0",
  "name": "buildkit",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "buildkit0",
      "isDefaultGateway": true,
      "ipMasq": true,
      "ipam": { "type": "host-local", "subnet": "10.222.0.0/16" }
    },
    {
      "type": "firewall",
      "ingressPolicy": "same-bridge"
    }
  ]
}
```

```bash
# Host-level iptables (set in the VM startup script)
# Block the GCE metadata endpoint (prevents stealing the service account token)
iptables -I FORWARD -s 10.222.0.0/16 -d 169.254.169.254 -j DROP
# Block the VPC internal network (prevents scanning the API Server, DB, etc.)
iptables -I FORWARD -s 10.222.0.0/16 -d 10.128.0.0/9 -j DROP
# Block link-local (except the metadata address already blocked)
iptables -I FORWARD -s 10.222.0.0/16 -d 169.254.0.0/16 -j DROP
```

**Tier 3: Cache hygiene**

BuildKit's content-addressed layer cache is inherently safe: the cache key is made of the instruction hash + the input layer checksum, so different source code cannot collide. **It is safe to share and needs no isolation** — this is the core advantage of the persistent VM approach.

But `--mount=type=cache` (named cache mounts) is shared by mount ID and needs protection:

```
# When Railpack generates the LLB, add a project prefix
--mount=type=cache,id=proj_abc123-npm,target=/root/.npm
--mount=type=cache,id=proj_abc123-pip,target=/root/.cache/pip
```

The Plan Engine injects `--cache-id-prefix=proj_{project_id}-` at the `railpack prepare` stage.

**Performance estimate (gVisor overhead):**

| Scenario | Current (runc) | Estimated (gVisor) | Overhead | Reason |
|------|------------|--------------|------|------|
| Python cached build | 7.4s | ~9-12s | +20-60% | I/O-heavy (cache restore), gVisor syscall interception overhead |
| Next.js cached build | 58.5s | ~65-75s | +10-25% | CPU-heavy (transpile), gVisor overhead is a small share |
| Cold build | 70-181s | +5-10% | — | Network download dominates, the sandbox overhead is diluted |

> For users 9s vs 7s is barely noticeable, and 65s vs 58s is also within a reasonable range. Google internally considers 10-30% overhead an acceptable trade-off.

**Competitive positioning:**

| | LaunchKit | Railway | Vercel | Cloud Build |
|---|---|---|---|---|
| Execution isolation | gVisor (user-space kernel) | None (shared BuildKit) | Firecracker (hardware VM) | Temporary VM |
| Cache sharing | Full, across tenants | Full, across tenants | Same account only | None |
| Cached build | ~9-75s | 7-58s | Not published | 77-197s (cold) |

Safer than Railway (gVisor vs no isolation), faster than Vercel (shared cache vs a separate cache per account), and much faster than Cloud Build.

**VM setup script (added to `scripts/setup-buildkit-vm.sh`):**

```bash
#!/usr/bin/env bash
set -euo pipefail

# 1. Install containerd
apt-get update && apt-get install -y containerd

# 2. Install gVisor (runsc + containerd shim)
curl -fsSL https://gvisor.dev/archive.key | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" \
  > /etc/apt/sources.list.d/gvisor.list
apt-get update && apt-get install -y runsc

# 3. Install CNI plugins
CNI_VERSION="v1.6.2"
mkdir -p /opt/cni/bin
curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz" \
  | tar -xz -C /opt/cni/bin

# 4. Install buildkitd
BUILDKIT_VERSION="v0.20.2"
curl -fsSL "https://github.com/moby/buildkit/releases/download/${BUILDKIT_VERSION}/buildkit-${BUILDKIT_VERSION}.linux-amd64.tar.gz" \
  | tar -xz -C /usr/local

# 5. Apply containerd + buildkitd configs (from /etc/containerd/ and /etc/buildkit/)
systemctl restart containerd
systemctl enable --now buildkitd

# 6. iptables rules for network isolation
iptables -I FORWARD -s 10.222.0.0/16 -d 169.254.169.254 -j DROP
iptables -I FORWARD -s 10.222.0.0/16 -d 10.128.0.0/9 -j DROP
iptables -I FORWARD -s 10.222.0.0/16 -d 169.254.0.0/16 -j DROP
iptables-save > /etc/iptables/rules.v4
```

> **On the Phase 2 GKE migration**: add `runtimeClassName: gvisor` to the buildkitd Pod; GKE Sandbox supports it natively and Google handles gVisor security updates. The config barely changes.

#### 6.10.4 Phase 2: BuildKit VM Pool Autoscaling (Design Reserved)

> At 100+ users, a single VM + MaxWorkers: 3 becomes the bottleneck.
> Borrows SkyPilot `QueueLengthAutoscaler` (`sky/serve/autoscalers.py:926-938`) and `_AutoscalerWithHysteresis` (`sky/serve/autoscalers.py:372-456`).

```
Phase 2 design direction (not implemented in Phase 1):

  VM Pool:
    min_vms:  1 (always-on primary, or 0 when idle)
    max_vms:  10
    target_builds_per_vm: 3 (MaxWorkers)

  Autoscaler (hysteresis against oscillation):
    scale_up_delay:   60s (builds are latency-sensitive, so scale out quickly)
    scale_down_delay: 600s (avoid repeated cold starts)
    Decision interval: 20s
    → scale_up_threshold: 3 consecutive decisions
    → scale_down_threshold: 30 consecutive decisions

  New VMs use spot/preemptible (saves 70%):
    build failed → automatically retried on an on-demand VM
    builds are stateless, which makes them a natural fit for spot

  The Phase 1 design does not block Phase 2:
    - the river queue already supports multiple workers
    - BuildKitManager can be extended into a pool manager
    - EnsureRunning() changes to pick the least busy VM
```

```go
// internal/build/handler.go (runs inside the API Server, not a separate Cloud Run Job)
package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1"
	cloudbuildpb "cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

var (
	buildkitVM       = os.Getenv("BUILDKIT_VM_ADDR") // e.g. tcp://10.128.0.x:1234
	gcpProject       = os.Getenv("GCP_PROJECT_ID")
	gcsBuildBucket   = os.Getenv("GCS_BUILD_BUCKET")
	artifactRegistry = fmt.Sprintf("us-east4-docker.pkg.dev/%s/user-images", gcpProject)
)

func BuildImage(ctx context.Context, planID, imageTag string) error {
	// 1. GCS URI (from plan.sources[].upload_url, the presigned tarball that was already uploaded)
	// The BuildKit VM is in the same region as GCS and reads it directly, so the API Server does not need to download and re-upload it.
	gcsSrc := fmt.Sprintf("gs://%s/sources/%s/source.tar.gz", gcsBuildBucket, planID)

	// 2. Railpack static analysis (2-3s, runs inside the API Server)
	// First download from GCS to local disk to run railpack prepare and produce the build plan
	if err := downloadFromGCS(ctx, fmt.Sprintf("sources/%s/source.tar.gz", planID), "/tmp/src.tar.gz"); err != nil {
		return fmt.Errorf("download source: %w", err)
	}
	if err := exec.CommandContext(ctx, "tar", "xzf", "/tmp/src.tar.gz", "-C", "/tmp/src").Run(); err != nil {
		return fmt.Errorf("extract source: %w", err)
	}
	if err := exec.CommandContext(ctx, "railpack", "prepare", "/tmp/src",
		"--plan-out", "/tmp/railpack-plan.json").Run(); err != nil {
		return fmt.Errorf("railpack prepare: %w", err)
	}

	// 3. Build image — primary path: BuildKit VM, fallback path: Cloud Build
	if isVMHealthy(ctx, buildkitVM) {
		// Primary path: the BuildKit VM reads the build context straight from GCS (same region, zero latency)
		cmd := exec.CommandContext(ctx, "docker", "buildx", "build",
			"--builder", "vm-builder", // docker buildx create --driver remote tcp://...
			"--build-arg", "BUILDKIT_SYNTAX=ghcr.io/railwayapp/railpack-frontend",
			"-f", "/tmp/railpack-plan.json",
			"--push", "-t", imageTag,
			gcsSrc) // gs:// URI — BuildKit reads directly from GCS, bypassing the API Server
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("buildkit build: %w", err)
		}
		// cached build: 7-58s (local SSD cache)
	} else {
		// Fallback path: Cloud Build (cold build 77-197s)
		// Cloud Build references the same GCS object directly, no re-upload needed
		client, err := cloudbuild.NewClient(ctx)
		if err != nil {
			return fmt.Errorf("cloudbuild client: %w", err)
		}
		defer client.Close()

		buildScript := `docker buildx create --name rb --driver docker-container --use && ` +
			`docker buildx inspect --bootstrap && ` +
			fmt.Sprintf(`docker buildx build --build-arg BUILDKIT_SYNTAX="ghcr.io/railwayapp/railpack-frontend" -f railpack-plan.json --push -t %s .`, imageTag)

		op, err := client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
			ProjectId: gcpProject,
			Build: &cloudbuildpb.Build{
				Source: &cloudbuildpb.Source{Source: &cloudbuildpb.Source_StorageSource{
					StorageSource: &cloudbuildpb.StorageSource{
						Bucket: gcsBuildBucket,
						Object: fmt.Sprintf("sources/%s/source.tar.gz", planID),
					},
				}},
				Steps: []*cloudbuildpb.BuildStep{{
					Name: "docker:27", Entrypoint: "sh",
					Args: []string{"-c", buildScript},
				}},
				Timeout: &durationpb.Duration{Seconds: 1800},
			},
		})
		if err != nil {
			return fmt.Errorf("create build: %w", err)
		}
		result, err := op.Wait(ctx)
		if err != nil || result.GetStatus() != cloudbuildpb.Build_SUCCESS {
			return fmt.Errorf("build failed: %v", result.GetStatusDetail())
		}
	}
	return nil
}
```

**Cloud Build measured data (launchkit-spike project, us-central1, 2026-04-01):**

Test app: Node.js + express + 5 deps, Railpack frontend v0.23.0

| | Run 1 Cold | Run 2 Same app, code changed | Run 3 Different app, no cache | Run 4 Different app, with cache-from | Run 5 e2-standard-2 |
|---|---|---|---|---|---|
| Queue + VM start | 49.6s | 58.0s | 49.6s | 48.4s | **0.9s** |
| Fetch source | 2.4s | 2.5s | 2.3s | 2.1s | 2.9s |
| Build step | 45.2s | 52.0s | 55.3s | 47.6s | 77.4s |
| Cleanup | 11.7s | 0.4s | 0.1s | 0.1s | 1.1s |
| **Total** | **111.2s** | **114.9s** | **109.3s** | **100.2s** | **82.8s** |
| Machine | E2_HIGHCPU_8 | E2_HIGHCPU_8 | E2_HIGHCPU_8 | E2_HIGHCPU_8 | e2-standard-2 |
| CACHED layers | 0 | 0 | 0 | 0 | 0 |

**Key findings:**

1. **VM cold start**: E2_HIGHCPU_8 is a fixed ~50s; e2-standard-2 starts in < 1s because the pool has more resources
2. **Registry cache is completely ineffective for Railpack (verified by measurement on 2026-04-01).** After adding `--cache-from/--cache-to type=registry,mode=max` to Cloud Build, redeploying the same service still had 0 cache hits (Run 1 = 86.5s, Run 2 = 89.2s). Reason: the cache key of a Railpack LLB cannot match across builds on a brand-new BuildKit daemon. `--cache-to` instead costs ~11s extra for the upload each time, and has been removed from build.sh.
3. **Build step ~45-77s**, depending on the machine type. Cloud Build is a cold build every time and cannot be optimized with a cache.

**Cloud Build cost comparison (based on measured data):**

| Machine | VM start | Build time | Total | 1000 builds/month | Monthly cost |
|---------|--------|-----------|------|---------------|------|
| e2-standard-2 | < 1s | ~77s | **~83s** | 1,383 min | **$0 (within the 2,500 free min)** |
| E2_HIGHCPU_8 | ~50s | ~45s | **~110s** | 1,833 min | **$28.6 ($0.0156/min)** |

> Cloud Build is only the fallback when the BuildKit VM is unhealthy; the normal path goes through the BuildKit VM (fixed cost $115/month).

**Persistent BuildKit VM measured data (2026-04-01, e2-standard-4 + pd-ssd, us-east4-c):**

| Project | Cloud Build (cold) | VM Cold | VM Cached (code change only) |
|------|-------------------|---------|----------------------|
| Python Telegram Bot (light) | 85s | 70s | **7.4s** |
| Next.js + Prisma + Tailwind (heavy) | 197s | 181s | **58.5s** |

> SSD vs pd-balanced: Python cached builds are 2.3x faster (7.4s vs 17.2s), Next.js only 7% faster (CPU-bound).
> e2-standard-4 + 100GB pd-ssd = ~$115/month, which breaks even at 12 Launch users.
> **Included in Phase 1 (beta launch):** persistent BuildKit VM + SSD, and the API Server connects to it with `docker buildx create --driver remote`. Cloud Build serves as the fallback.

---

## 7. Provider Adapter Layer

### Design Principles

The Provider layer has three tiers:

```
Tool Handler
    │
    ▼
Provider Selector          ← decides which provider to use (LaunchKit managed vs. BYOC)
    │
    ▼
Provider Registry          ← instances of all providers, with BYOC ones stubbed first
    │
    ▼
Provider Adapter           ← each provider's implementation, behind a unified interface
```

The v1 tool handlers are **unaware** of provider switching logic. When BYOC goes live, you only add logic in the selector and fill in implementations in the registry; the tool handlers do not change at all.

---

### 7.1 Provider Interfaces

```go
// internal/provider/interfaces.go
package provider

import "context"

type CloudCredentials map[string]string

// ─── Database ────────────────────────────────────────────────
type DatabaseProvider interface {
	Key() string
	Create(ctx context.Context, opts DatabaseCreateOpts) (*DatabaseResult, error)
	Delete(ctx context.Context, providerID string, creds CloudCredentials) error
}

type DatabaseCreateOpts struct {
	Name        string           // naming rule: launchkit-{projectID}-{type} (guarantees idempotency)
	Region      string
	Credentials CloudCredentials
}
// ⚠️ Idempotency requirement: every provider's Create() must "look up first, then create".
// If a resource with the same name already exists → return the existing resource, do not create it again.
// This is the precondition for crash recovery (re-running the orchestrator never produces orphans).

type DatabaseResult struct {
	ProviderID    string
	ConnectionURL string // stored encrypted in the DB, never returned to Claude
	Region        string
}

// ─── Cache ───────────────────────────────────────────────────
type CacheProvider interface {
	Key() string
	Create(ctx context.Context, opts CacheCreateOpts) (*CacheResult, error)
	Delete(ctx context.Context, providerID string, creds CloudCredentials) error
}

// ─── Storage ─────────────────────────────────────────────────
type StorageProvider interface {
	Key() string
	Create(ctx context.Context, opts StorageCreateOpts) (*StorageResult, error)
	Delete(ctx context.Context, providerID string, creds CloudCredentials) error
}

// ─── Compute ─────────────────────────────────────────────────
type ComputeProvider interface {
	Key() string
	Deploy(ctx context.Context, opts DeployOpts) (*DeployResult, error)
	Undeploy(ctx context.Context, providerID string, creds CloudCredentials) error
	GetLogs(ctx context.Context, providerID string, opts LogOpts, creds CloudCredentials) ([]LogEntry, error)
	GetMetrics(ctx context.Context, providerID string, opts MetricOpts, creds CloudCredentials) (*MetricsData, error)
	Scale(ctx context.Context, providerID string, config ScaleConfig, creds CloudCredentials) error
}

type DeployOpts struct {
	Name         string
	Image        string
	Region       string
	Env          map[string]string
	CPU          string // default "1"
	Memory       string // default "512Mi"
	MinInstances int
	MaxInstances int
	Credentials  CloudCredentials
}

type DeployResult struct {
	ProviderID string
	URL        string
	RevisionID string
}

// ─── Reconcilable (for orphan reconciliation) ─────────────────
// Every provider must implement this interface so that the orphan reconciliation job can enumerate resources
type Reconcilable interface {
	ListAll(ctx context.Context) ([]ProviderResource, error)
}

type ProviderResource struct {
	ProviderID string
	Name       string
	CreatedAt  time.Time
}

// Neon:       GET /projects (filter tag = launchkit)
// Upstash:    GET /v2/redis/databases
// Cloud Run:  services.list (filter label launchkit-managed=true)
// R2:         ListBuckets (filter prefix launchkit-)
```

---

### 7.2 Provider Registry

```go
// internal/provider/registry.go
package provider

// ─── Provider key constants ──────────────────────────────────
// Database
const (
	DbNeon         = "neon"           // Postgres  — LaunchKit managed ✅ v1
	DbTurso        = "turso"          // SQLite    — LaunchKit managed ✅ v1
	DbPlanetScale  = "planetscale"    // MySQL     — LaunchKit managed ✅ v1
	DbMongoAtlas   = "mongodb_atlas"  // MongoDB   — LaunchKit managed ✅ v1
	DbCloudSQL     = "cloud_sql"      // BYOC GCP     🔜
	DbRDS          = "rds"            // BYOC AWS     🔜
	DbAzureDB      = "azure_db"       // BYOC Azure   🔜
)

// Cache
const (
	CacheUpstash     = "upstash"       // LaunchKit managed ✅ v1
	CacheMemorystore = "memorystore"   // BYOC GCP 🔜
	CacheElastiCache = "elasticache"   // BYOC AWS 🔜
	CacheAzure       = "azure_cache"   // BYOC Azure 🔜
)

// Compute
const (
	ComputeCloudRun  = "cloud_run"         // LaunchKit default ✅ v1
	ComputePages     = "cloudflare_pages"  // Static sites      ✅ v1
	ComputeFly       = "fly"               // Alternative       ✅ v1
	ComputeECS       = "aws_ecs"           // BYOC AWS  🔜
	ComputeACA       = "azure_aca"         // BYOC Azure 🔜
)

// ─── Registry ────────────────────────────────────────────────
var DatabaseRegistry = map[string]DatabaseProvider{
	DbNeon:        &NeonProvider{},
	DbTurso:       &TursoProvider{},
	DbPlanetScale: &PlanetScaleProvider{},
	DbMongoAtlas:  &MongoAtlasProvider{},
	DbCloudSQL:    newBYOCStub("cloud_sql", "GCP Cloud SQL"),
	DbRDS:         newBYOCStub("rds", "AWS RDS"),
	DbAzureDB:     newBYOCStub("azure_db", "Azure Database"),
}

var ComputeRegistry = map[string]ComputeProvider{
	ComputeCloudRun: &CloudRunProvider{},
	ComputePages:    &CloudflarePagesProvider{},
	ComputeFly:      &FlyProvider{},
	ComputeECS:      newBYOCComputeStub("aws_ecs", "AWS ECS"),
	ComputeACA:      newBYOCComputeStub("azure_aca", "Azure Container Apps"),
}

// Same for the Cache and Storage registries
```

```go
// internal/provider/byoc_stub.go
package provider

import (
	"context"
	"fmt"
)

type BYOCNotImplementedError struct {
	Provider string
}

func (e *BYOCNotImplementedError) Error() string {
	return fmt.Sprintf("%s is not yet supported. See https://docs.launchkit.dev/byoc", e.Provider)
}

type byocDatabaseStub struct {
	key         string
	displayName string
}

func newBYOCStub(key, displayName string) DatabaseProvider {
	return &byocDatabaseStub{key: key, displayName: displayName}
}

func (s *byocDatabaseStub) Key() string { return s.key }

func (s *byocDatabaseStub) Create(_ context.Context, _ DatabaseCreateOpts) (*DatabaseResult, error) {
	return nil, &BYOCNotImplementedError{Provider: s.displayName}
}

func (s *byocDatabaseStub) Delete(_ context.Context, _ string, _ CloudCredentials) error {
	return &BYOCNotImplementedError{Provider: s.displayName}
}

// byocComputeStub is the same, omitted
```

---

### 7.3 Provider Selector

Tool handlers call the selector instead of importing providers directly. The selector decides whether to go LaunchKit managed or BYOC.

```go
// internal/provider/selector.go
package provider

import "context"

// LaunchKit v1 default choices (when the user does not specify)
var defaultDBProviders = map[string]string{
	"postgres": DbNeon,
	"sqlite":   DbTurso,
	"mysql":    DbPlanetScale,
	"mongodb":  DbMongoAtlas,
}

// BYOC mapping table (when the user provides a cloud account)
var byocDBProviders = map[string]map[string]string{
	"gcp":   {"postgres": DbCloudSQL, "mysql": DbCloudSQL},
	"aws":   {"postgres": DbRDS, "mysql": DbRDS},
	"azure": {"postgres": DbAzureDB, "mysql": DbAzureDB},
}

type SelectedProvider struct {
	Provider    DatabaseProvider
	Credentials CloudCredentials
}

func SelectDatabaseProvider(ctx context.Context, dbType, teamID, accountID string) (*SelectedProvider, error) {
	if accountID != "" {
		// BYOC path: the user provides their own cloud account
		account, err := getCloudAccount(ctx, accountID, teamID)
		if err != nil {
			return nil, err
		}
		creds, err := decryptCloudCredentials(ctx, account)
		if err != nil {
			return nil, err
		}
		key := defaultDBProviders[dbType]
		if byoc, ok := byocDBProviders[account.Cloud]; ok {
			if k, ok := byoc[dbType]; ok {
				key = k
			}
		}
		return &SelectedProvider{Provider: DatabaseRegistry[key], Credentials: creds}, nil
	}

	// LaunchKit managed path (all v1 users go through here)
	key := defaultDBProviders[dbType]
	return &SelectedProvider{Provider: DatabaseRegistry[key]}, nil
}

// Cache / Storage / Compute selectors are the same, omitted
```

---

### 7.4 How Tool Handlers Call It

```go
// internal/mcp/tools/provision.go
// The tool handler only calls the selector and is unaware of provider details

func handleProvisionDatabase(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := parseProvisionParams(req)

	sel, err := provider.SelectDatabaseProvider(ctx, params.Type, params.TeamID, params.AccountID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result, err := sel.Provider.Create(ctx, provider.DatabaseCreateOpts{
		Name:        params.Project,
		Region:      params.Region,
		Credentials: sel.Credentials,
	})
	if err != nil {
		var byocErr *provider.BYOCNotImplementedError
		if errors.As(err, &byocErr) {
			return mcp.NewToolResultError(byocErr.Error()), nil // Claude sees a clear error message
		}
		return nil, err
	}

	// Encrypt connectionURL and store it in the DB ...
	return mcp.NewToolResultText(formatResult(result)), nil
}
```

---

### 7.5 Steps to Add a BYOC Provider

To add Cloud SQL support in the future, you only need to:

```
1. Implement internal/provider/cloud_sql.go (implements DatabaseProvider)
2. In DatabaseRegistry, replace the byocStub of DbCloudSQL with &CloudSQLProvider{}
3. Add the "Connect GCP Account" UI to the Dashboard
4. The cloud_accounts table already exists, use it directly
Done. The tool handlers, selector logic and DB schema are all untouched.
```

---

### 7.6 Existing Provider Implementations (v1)

```go
// internal/provider/neon.go — implements the DatabaseProvider interface
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

type NeonProvider struct{}

func (n *NeonProvider) Key() string { return DbNeon }

// ── Provider idempotency design ─────────────────────────────────────────
// Problem: the Neon API POST /projects succeeds → the HTTP response is lost on the network → retry → a second one is created → orphan.
// Borrowed from: SkyPilot sky.launch first checks whether the cluster already exists, and reuses it if so.
//
// Solution: naming convention + look up first, then create.
//   Naming: launchkit-{projectID}-{type} (e.g. launchkit-abc123-postgres)
//   Create() first does GET /projects to look for one with the same name; if it exists, return it, and only POST if it does not.
//   Applies to all providers: Neon, Upstash, R2.
//
// This makes crash recovery safe: when the orchestrator re-runs a provision step that already completed,
// it does not create duplicate resources but gets the existing connection string.

func (n *NeonProvider) Create(ctx context.Context, opts DatabaseCreateOpts) (*DatabaseResult, error) {
	apiKey := os.Getenv("NEON_API_KEY")

	// ── Step 1: idempotency check — if a project with the same name already exists, return it directly ──
	existing, err := n.findByName(ctx, apiKey, opts.Name)
	if err == nil && existing != nil {
		return existing, nil
	}

	// ── Step 2: create only if it does not exist ──
	body := fmt.Sprintf(`{"project":{"name":"%s"}}`, opts.Name)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://console.neon.tech/api/v2/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neon create project: %w", err)
	}
	defer resp.Body.Close()

	var result neonCreateResponse
	json.NewDecoder(resp.Body).Decode(&result)

	return &DatabaseResult{
		ProviderID:    result.Project.ID,
		ConnectionURL: result.ConnectionURIs[0].ConnectionURI,
		Region:        result.Project.RegionID,
	}, nil
}

// findByName looks up a Neon project with the same name (idempotency guarantee)
func (n *NeonProvider) findByName(ctx context.Context, apiKey, name string) (*DatabaseResult, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://console.neon.tech/api/v2/projects", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var listResult struct {
		Projects []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RegionID string `json:"region_id"`
		} `json:"projects"`
	}
	json.NewDecoder(resp.Body).Decode(&listResult)

	for _, p := range listResult.Projects {
		if p.Name == name {
			// Already exists → look up the connection URI
			connURL, _ := n.getConnectionURL(ctx, apiKey, p.ID)
			return &DatabaseResult{
				ProviderID:    p.ID,
				ConnectionURL: connURL,
				Region:        p.RegionID,
			}, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (n *NeonProvider) Delete(ctx context.Context, providerID string, _ CloudCredentials) error {
	// DELETE /projects/{providerID}
	// ...
	return nil
}
```

```go
// internal/provider/cloud_run.go — implements the ComputeProvider interface
package provider

import (
	"context"
	"fmt"
	"os"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type CloudRunProvider struct {
	client  *run.ServicesClient
	project string
}

func NewCloudRunProvider() *CloudRunProvider {
	client, _ := run.NewServicesClient(context.Background())
	return &CloudRunProvider{client: client, project: os.Getenv("GCP_PROJECT_ID")}
}

func (cr *CloudRunProvider) Key() string { return ComputeCloudRun }

func (cr *CloudRunProvider) Deploy(ctx context.Context, opts DeployOpts) (*DeployResult, error) {
	parent := fmt.Sprintf("projects/%s/locations/%s", cr.project, opts.Region)
	svcName := fmt.Sprintf("%s/services/%s", parent, opts.Name)

	cpu := opts.CPU
	if cpu == "" { cpu = "1" }
	mem := opts.Memory
	if mem == "" { mem = "512Mi" }

	envVars := make([]*runpb.EnvVar, 0, len(opts.Env))
	for k, v := range opts.Env {
		envVars = append(envVars, &runpb.EnvVar{Name: k, Values: &runpb.EnvVar_Value{Value: v}})
	}

	svc := &runpb.Service{
		Name: svcName,
		Template: &runpb.RevisionTemplate{
			Containers: []*runpb.Container{{
				Image: opts.Image,
				Ports: []*runpb.ContainerPort{{ContainerPort: 8080}},
				Env:   envVars,
				Resources: &runpb.ResourceRequirements{
					Limits: map[string]string{"cpu": cpu, "memory": mem},
				},
			}},
			Scaling: &runpb.RevisionScaling{
				MinInstanceCount: int32(opts.MinInstances),
				MaxInstanceCount: int32(opts.MaxInstances),
			},
		},
	}

	// try update first, create if not found
	op, err := cr.client.UpdateService(ctx, &runpb.UpdateServiceRequest{Service: svc})
	if status.Code(err) == codes.NotFound {
		op, err = cr.client.CreateService(ctx, &runpb.CreateServiceRequest{
			Parent: parent, ServiceId: opts.Name, Service: svc,
		})
		if err != nil {
			return nil, fmt.Errorf("create service: %w", err)
		}
		result, err := op.Wait(ctx)
		if err != nil {
			return nil, err
		}

		// allow unauthenticated access
		cr.client.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{
			Resource: svcName,
			Policy:   &iampb.Policy{Bindings: []*iampb.Binding{{Role: "roles/run.invoker", Members: []string{"allUsers"}}}},
		})

		return &DeployResult{ProviderID: opts.Name, URL: result.Uri, RevisionID: result.LatestReadyRevision}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}
	result, err := op.Wait(ctx)
	if err != nil {
		return nil, err
	}
	return &DeployResult{ProviderID: opts.Name, URL: result.Uri, RevisionID: result.LatestReadyRevision}, nil
}

func (cr *CloudRunProvider) Undeploy(ctx context.Context, providerID string, _ CloudCredentials) error { /* ... */ return nil }
func (cr *CloudRunProvider) GetLogs(ctx context.Context, providerID string, opts LogOpts, _ CloudCredentials) ([]LogEntry, error) { /* ... */ return nil, nil }
func (cr *CloudRunProvider) GetMetrics(ctx context.Context, providerID string, opts MetricOpts, _ CloudCredentials) (*MetricsData, error) { /* ... */ return nil, nil }
func (cr *CloudRunProvider) Scale(ctx context.Context, providerID string, config ScaleConfig, _ CloudCredentials) error { /* ... */ return nil }
```

---

## 8. Secret Management Implementation

### Tink + AWS KMS Envelope Encryption

**Why Tink instead of hand-written envelope encryption?**

| Aspect | Hand-written (crypto/aes + KMS SDK) | Google Tink |
|------|---------------------------|-------------|
| Nonce management | You must ensure it never repeats yourself (a mistake = disaster) | Handled internally by Tink, misuse-resistant |
| AAD tenant isolation | You must add it to the GCM additionalData yourself | One line: `Encrypt(plaintext, tenantID)` |
| Key rotation | You must write the migration yourself | Built into the Tink keyset |
| Multiple KMS providers | Locked to the AWS SDK | Switching to GCP/Azure for BYOC only needs a different URI |
| Amount of code | ~200 lines of crypto code | ~50 lines |
| Storage format | Two columns (encrypted_value + encrypted_dek) | A single BYTEA (the ciphertext contains the wrapped DEK) |

```
  AWS KMS ($1/month, multi-region key)
    │  Does only one thing: manages the Master Key (never leaves the HSM hardware)
    │  Tink calls KMS automatically through KMSEnvelopeAEAD2
    ▼
  Tink KMSEnvelopeAEAD2
    │  On encrypt: generates a random DEK → AES-256-GCM encryption (AAD = tenant/project ID) → KMS wraps the DEK
    │  Output: a single ciphertext blob = [4B DEK length][wrapped DEK][AES-256-GCM payload]
    ▼
  LaunchKit's Neon Postgres
    │  Each secret needs only one BYTEA column: encrypted_value
    ▼
  Decryption flow at deploy time:
    1. Get encrypted_value (the Tink ciphertext) from the DB
    2. Call Tink Decrypt (AAD = project_id) → internally KMS unwraps the DEK + AES-256-GCM decrypts
    3. Inject as container environment variables
    4. The plaintext exists only in memory and is GC'd as soon as the deploy finishes
```

### Key Security Design Points

**AAD (Additional Authenticated Data) tenant isolation:**
- On encryption it is bound to `project_id` (or `team_id`), and decryption must supply the same AAD
- If any program bug causes a cross-tenant read → Tink decryption fails → no plaintext is leaked
- The AAD must be stable: `projects.id` / `teams.id` use UUIDs and never change once generated

**KMS key protection strategy:**
- Use an AWS KMS multi-region key (`mrk-xxx`); the primary and replicas span regions, and the cost is unchanged ($1/month)
- Enable deletion protection in the key policy, and separate the admin and encrypt/decrypt roles
- Tink passes the ARN straight to the AWS SDK, so MRKs are naturally compatible (verified: Tink does not modify the keyID)

**Known limitations and mitigations:**

| Limitation | Impact | Mitigation |
|------|------|------|
| Envelope AEAD malleability (the wrapped DEK header is not authenticated) | An attacker can replace the wrapped DEK → decryption fails (DoS), but no plaintext is leaked | The secrets are in Postgres, and if they can modify the DB it is already game over |
| Each decryption needs one KMS API call | Low frequency at deploy time, not a bottleneck | A DEK cache (TTL 5 min) can be added to the hot path later |
| Tink's own ciphertext format | Can only be decrypted with Tink | A reasonable trade-off: a standard library format beats a custom format |
| AAD is not forwarded to the KMS EncryptionContext | The DEK wrapping layer has no AAD | The local AES-256-GCM layer has the AAD, so tenant isolation still holds |

```go
// internal/secrets/kms.go
package secrets

import (
	"context"
	"fmt"

	"github.com/tink-crypto/tink-go-awskms/v2/integration/awskms"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/tink"
	"gorm.io/gorm"
)

// SecretStore wraps Tink envelope encryption.
// All encryption and decryption automatically go through AWS KMS, with the AAD bound to the tenant identity.
type SecretStore struct {
	envelope tink.AEAD
}

// NewSecretStore creates the Tink envelope AEAD.
// kmsKeyURI format: aws-kms://arn:aws:kms:us-east-1:123456789:key/mrk-xxx
func NewSecretStore(kmsKeyURI string) (*SecretStore, error) {
	// Create the AWS KMS client (using default credentials: env vars / IAM role)
	client, err := awskms.NewClientWithOptions(kmsKeyURI)
	if err != nil {
		return nil, fmt.Errorf("create KMS client: %w", err)
	}

	// Get the KEK AEAD (corresponds to the KMS master key)
	kekAEAD, err := client.GetAEAD(kmsKeyURI)
	if err != nil {
		return nil, fmt.Errorf("get KEK AEAD: %w", err)
	}

	// Create the envelope AEAD: every encryption automatically generates a random DEK (AES-256-GCM)
	envelope := aead.NewKMSEnvelopeAEAD2(aead.AES256GCMKeyTemplate(), kekAEAD)

	return &SecretStore{envelope: envelope}, nil
}

// Encrypt encrypts a secret value, with the AAD bound to ownerID (project_id or team_id).
// The returned ciphertext contains the wrapped DEK, so only a single BYTEA column is needed to store it.
func (s *SecretStore) Encrypt(plaintext []byte, ownerID string) ([]byte, error) {
	return s.envelope.Encrypt(plaintext, []byte(ownerID))
}

// Decrypt decrypts a secret value; ownerID must be the same as at encryption time, otherwise decryption fails.
func (s *SecretStore) Decrypt(ciphertext []byte, ownerID string) ([]byte, error) {
	return s.envelope.Decrypt(ciphertext, []byte(ownerID))
}

// Store encrypts and saves to Postgres.
func (s *SecretStore) Store(ctx context.Context, db *gorm.DB, projectID, name, value string) (string, error) {
	encrypted, err := s.Encrypt([]byte(value), projectID)
	if err != nil {
		return "", fmt.Errorf("encrypt secret %s: %w", name, err)
	}

	secret := UserSecret{ProjectID: projectID, Name: name, EncryptedValue: encrypted, Status: "set"}
	if err := db.WithContext(ctx).Create(&secret).Error; err != nil {
		return "", err
	}
	return secret.ID, nil
}

// RetrieveAll decrypts all secrets under a project.
// Each decryption needs one KMS call (to unwrap the DEK); called infrequently at deploy time, not a bottleneck.
func (s *SecretStore) RetrieveAll(ctx context.Context, db *gorm.DB, projectID string) (map[string]string, error) {
	var rows []UserSecret
	if err := db.WithContext(ctx).Where("project_id = ? AND status = ?", projectID, "set").Find(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string, len(rows))
	for _, row := range rows {
		plaintext, err := s.Decrypt(row.EncryptedValue, projectID)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %s: %w", row.Name, err)
		}
		result[row.Name] = string(plaintext)
	}
	return result, nil
}
```

> **Dependency versions:** `tink-go v2.6.0`, `tink-go-awskms v2.1.0` (uses aws-sdk-go v1).
> If you need aws-sdk-go-v2, you can switch to `tink-go-awskms v3.0.0` (released 2026-03), with the same API.

---

## 9. Real-Time Event System

### Architecture

```
Tool Handler (deploy/provision/...)
    │
    ├── update DB status
    │
    └── eventBus.publish(projectId, event)
            │
            ▼
        Redis Pub/Sub
            │
     ┌──────┴──────┐
     ▼              ▼
SSE endpoint    Dashboard WebSocket
(Claude Code)   (Browser)
```

### Implementation

```go
// internal/event/bus.go
package event

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client

func init() {
	opt, _ := redis.ParseURL(os.Getenv("UPSTASH_REDIS_URL"))
	rdb = redis.NewClient(opt)
}

type Event struct {
	Type      string `json:"type"` // build_started | build_completed | deploy_completed | alert_fired | ...
	Service   string `json:"service,omitempty"`
	Data      any    `json:"data,omitempty"`
	Timestamp string `json:"timestamp"`
}

func Publish(ctx context.Context, projectID string, evt Event) error {
	evt.Timestamp = time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal(evt)
	return rdb.Publish(ctx, fmt.Sprintf("project:%s", projectID), data).Err()
}

func Subscribe(ctx context.Context, projectID string, callback func(Event)) func() {
	channel := fmt.Sprintf("project:%s", projectID)
	sub := rdb.Subscribe(ctx, channel)

	go func() {
		ch := sub.Channel()
		for msg := range ch {
			var evt Event
			json.Unmarshal([]byte(msg.Payload), &evt)
			callback(evt)
		}
	}()

	return func() { sub.Close() }
}
```

---

## 10. Monitoring Infrastructure

> **Principle**: LaunchKit is a PaaS — if we go down ourselves, users' services go down with us.
> Monitoring has two layers: (1) LaunchKit's own health (2) the health of users' services. The two must not share the same set of alerts.

### 10.1 Structured Logging Pipeline

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Request arrives                                                              │
│   middleware generates trace_id (X-Trace-Id header, or freshly generated)    │
│   slog.With("trace_id", traceID) injects it into the context                 │
│   ↓                                                                          │
│   every later log automatically carries trace_id                             │
│   ↓                                                                          │
│   Tool Handler → Provider → Orchestrator → Build → Deploy                    │
│   every log at every step carries the same trace_id                          │
│   ↓                                                                          │
│   Cloud Run stdout → Cloud Logging → auto-ingested, searchable by trace_id   │
└──────────────────────────────────────────────────────────────────────────────┘
```

```go
// internal/middleware/logging.go
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ctxKey string

const traceKey ctxKey = "trace_id"

func TraceID(ctx context.Context) string {
	if id, ok := ctx.Value(traceKey).(string); ok {
		return id
	}
	return "unknown"
}

func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Inherit from the header or generate a new trace_id
		traceID := r.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = uuid.NewString()
		}

		ctx := context.WithValue(r.Context(), traceKey, traceID)
		logger := slog.With("trace_id", traceID, "method", r.Method, "path", r.URL.Path)

		// Inject the logger into the context (downstream uses slog.InfoContext to carry trace_id automatically)
		ctx = WithLogger(ctx, logger)
		w.Header().Set("X-Trace-Id", traceID)

		rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rec, r.WithContext(ctx))

		logger.Info("request",
			"status", rec.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"user_id", UserIDFromContext(ctx),
		)
	})
}
```

**Log level conventions:**

| Level | Purpose | Example |
|-------|------|------|
| `slog.Debug` | Development debugging, off in production | Railpack detection details, SQL queries |
| `slog.Info` | Normal operation records | "deployment started", "provision completed" |
| `slog.Warn` | Abnormal but self-healing | circuit breaker half-open, retrying, resource orphan detected |
| `slog.Error` | Needs human intervention | provider API failure, KMS unavailable, metering write failure |

**Log output format:** JSON (Cloud Run stdout → Cloud Logging parses structured JSON automatically).
**Log retention:** Cloud Logging retains 90 days (the GCP default is 30 days, adjusted to 90). Beyond 90 days, export to BigQuery on demand.

### 10.2 LaunchKit's Own Monitoring (Who Monitors Us?)

> The health checks of users' services are our feature. But if LaunchKit itself goes down, the feature no longer exists.
> So LaunchKit's own monitoring **cannot depend on LaunchKit itself**.

```
┌────────────────────────────────────────────────────────┐
│  GCP Cloud Monitoring (native, bypasses LaunchKit)     │
│                                                        │
│  Alert rules (set in the GCP console / Terraform):     │
│                                                        │
│  1. API Server 5xx rate > 5% for 2 min     → PagerDuty │
│  2. API Server p99 latency > 5s for 5 min  → PagerDuty │
│  3. API Server instance count = 0          → PagerDuty │
│  4. BuildKit VM health check fails         → Slack     │
│  5. Cloud Run autoscaled to max            → Slack     │
│  6. Neon connections near the limit        → Slack     │
│                                                        │
│  Notification channels:                                │
│  Critical → PagerDuty → on-call engineer's phone       │
│  Warning  → Slack #ops-alerts                          │
└────────────────────────────────────────────────────────┘
```

```go
// internal/monitor/selfcheck.go
// LaunchKit's own health check — runs in a goroutine inside the API Server
// Note: these check results are written to Cloud Monitoring custom metrics, and alerting is handled by GCP (not by ourselves)

package monitor

import (
	"context"
	"log/slog"
	"time"
)

type SystemHealth struct {
	Postgres    bool          // whether the Neon connection pool is healthy
	Redis       bool          // Upstash availability
	BuildKit    bool          // BuildKit VM TCP :1234 is reachable
	KMS         bool          // AWS KMS Encrypt test call succeeded
	RiverQueue  int64         // number of queued river jobs
}

// StartSelfCheck checks all internal dependencies every 30 seconds
func StartSelfCheck(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				health := checkAll(ctx)
				emitCustomMetrics(ctx, health) // → Cloud Monitoring custom metric

				if !health.Postgres {
					slog.Error("self-check: postgres unreachable")
				}
				if !health.Redis {
					slog.Warn("self-check: redis unreachable, SSE/rate-limit degraded")
				}
				if !health.BuildKit {
					slog.Warn("self-check: buildkit VM unreachable, using Cloud Build fallback")
				}
				if !health.KMS {
					slog.Error("self-check: KMS unreachable, secret operations will fail")
				}
				if health.RiverQueue > 1000 {
					slog.Warn("self-check: river queue depth high", "depth", health.RiverQueue)
				}
			}
		}
	}()
}

func checkAll(ctx context.Context) SystemHealth {
	return SystemHealth{
		Postgres:   pingPostgres(ctx),          // db.Exec("SELECT 1")
		Redis:      pingRedis(ctx),             // rdb.Ping(ctx)
		BuildKit:   pingBuildKit(ctx),          // net.DialTimeout("tcp", buildkitAddr, 5s)
		KMS:        pingKMS(ctx),               // tink Encrypt/Decrypt roundtrip with test data
		RiverQueue: countPendingRiverJobs(ctx), // SELECT count(*) FROM river_job WHERE state = 'available'
	}
}
```

**GCP Cloud Monitoring custom metrics naming conventions:**

| Metric | Type | Description |
|--------|------|------|
| `custom.googleapis.com/launchkit/self/postgres_up` | gauge (0/1) | Postgres availability |
| `custom.googleapis.com/launchkit/self/redis_up` | gauge (0/1) | Redis availability |
| `custom.googleapis.com/launchkit/self/buildkit_up` | gauge (0/1) | BuildKit VM availability |
| `custom.googleapis.com/launchkit/self/kms_up` | gauge (0/1) | AWS KMS availability |
| `custom.googleapis.com/launchkit/self/river_queue_depth` | gauge | number of queued river jobs |
| `custom.googleapis.com/launchkit/self/orphan_count` | gauge | number of orphan resources detected |

### 10.3 User Service Health Check

```go
// internal/monitor/health.go
// goroutine ticker: every 30 seconds per active service

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func CheckHealth(ctx context.Context, svc Service) {
	traceID := fmt.Sprintf("healthcheck-%s-%d", svc.ID, time.Now().Unix())
	logger := slog.With("trace_id", traceID, "service_id", svc.ID)

	url := svc.URL + "/health"
	client := &http.Client{Timeout: 10 * time.Second}

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		logger.Warn("health check failed", "error", err.Error())
		markUnhealthy(ctx, svc, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 400 {
		markHealthy(ctx, svc)
	} else {
		logger.Warn("health check unhealthy", "status", resp.StatusCode)
		markUnhealthy(ctx, svc, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

func markUnhealthy(ctx context.Context, svc Service, reason string) {
	consecutive := incrementFailCount(ctx, svc.ID)
	if consecutive >= 4 { // 4 × 30s = 2 minutes of consecutive failures
		createIncident(ctx, svc, reason)
		notifyAlertChannels(ctx, svc)
	}
}
```

### 10.4 Alert Evaluation

```go
// internal/monitor/alerts.go
// goroutine ticker: every 1 minute

package monitor

import (
	"context"
	"time"

	"gorm.io/gorm"
)

func EvaluateAlerts(ctx context.Context, db *gorm.DB) {
	var activeAlerts []Alert
	db.WithContext(ctx).Where("status IN ?", []string{"active", "firing"}).Find(&activeAlerts)

	for _, alert := range activeAlerts {
		for _, rule := range alert.Rules {
			metric := getMetric(ctx, alert.ServiceID, rule.Metric, rule.Window)
			firing := evaluate(metric, rule.Operator, rule.Threshold)

			if firing && alert.Status != "firing" {
				db.Model(&alert).Updates(map[string]any{"status": "firing", "last_triggered": time.Now()})
				createAlertIncident(ctx, db, alert)
				notify(ctx, alert.Channels, alert, metric, rule)
			} else if !firing && alert.Status == "firing" {
				db.Model(&alert).Updates(map[string]any{"status": "active", "last_resolved": time.Now()})
				resolveAlertIncident(ctx, db, alert)
			}
		}
	}
}
```

### 10.5 Metrics Collection

| Source | Metric | Method | Retention |
|------|------|------|------|
| Cloud Run | CPU, memory, requests, latency, 5xx rate | Cloud Monitoring API (polling every 1 min) | Cloud Monitoring default 6 weeks |
| Neon | connections, storage, query time | Neon API | Queried on demand |
| Upstash | commands, memory | Upstash API | Queried on demand |
| Cloudflare | bandwidth, requests | Cloudflare Analytics API | Queried on demand |
| SkyPilot | GPU utilization, cost | SkyPilot status API | Queried on demand |
| LaunchKit itself | postgres/redis/buildkit/kms health, river depth | Self-built custom metrics | Cloud Monitoring 6 weeks |

> **Phase 1 strategy**: metrics are not stored by us; we query the provider APIs directly. Cloud Monitoring's 6 weeks of history is enough.
> **If Phase 2+ needs long-term trends**: export Cloud Monitoring to BigQuery (a native GCP feature, just switch it on).

---

## 11. Billing Engine

### 11.1 Prepaid Balance & Usage Pricing

There is no monthly plan. Users top up a balance, and all resources are charged against it in real time by usage.

```go
// internal/billing/credits.go
package billing

// ─── Top-up rules ───
var Credits = struct {
	MinimumTopUp     float64 // minimum top-up $5 USD
	FirstTopUpBonus  float64 // first top-up bonus $5 (triggered when lifetime_top_up == 0)
	LowBalanceAlert  float64 // send a warning when the balance < $2
	SuspendThreshold float64 // balance ≤ $0 → all services scale to zero, charging stops
}{
	MinimumTopUp:     5,
	FirstTopUpBonus:  5,
	LowBalanceAlert:  2,
	SuspendThreshold: 0,
}

// ─── Usage pricing (markup ~30-50% on cost) ───
var UsageRates = struct {
	// Static frontend (Cloudflare Pages) → free, not metered
	ComputeCPU      float64 // $/vCPU-sec    (cost $0.000024)
	ComputeMemory   float64 // $/GiB-sec     (cost $0.0000025)
	ComputeRequests float64 // $/1M requests (cost $0.40/M)
	DatabaseCompute float64 // $/compute-hour (cost $0.016)
	DatabaseStorage float64 // $/GiB-month   (cost $0.35)
	RedisCommands   float64 // $/100K commands (cost ~$0.25)
	Storage         float64 // $/GiB-month   (cost $0.015)
	Bandwidth       float64 // $/GiB         (cost $0.08)
}{
	ComputeCPU:      0.000030,
	ComputeMemory:   0.0000035,
	ComputeRequests: 0.60,
	DatabaseCompute: 0.020,
	DatabaseStorage: 0.50,
	RedisCommands:   0.40,
	Storage:         0.025,
	Bandwidth:       0.10,
}

// ─── Always-On fixed instances (billed per hour) ───
type AlwaysOnSpec struct {
	VCPU     float64
	MemoryMB int
	Hourly   float64
	Monthly  float64
}

var AlwaysOnRates = map[string]AlwaysOnSpec{
	"small":  {VCPU: 0.25, MemoryMB: 256, Hourly: 0.008, Monthly: 5.76},
	"medium": {VCPU: 0.5, MemoryMB: 512, Hourly: 0.016, Monthly: 11.52},
	"large":  {VCPU: 1.0, MemoryMB: 1024, Hourly: 0.030, Monthly: 21.60},
	"xl":     {VCPU: 2.0, MemoryMB: 2048, Hourly: 0.055, Monthly: 39.60},
}

// ─── Account safety limits (anti-abuse, not for billing) ───
var AccountLimits = struct {
	MaxProjects          int
	MaxServicesPerProject int
	MaxInstancesPerService int // injected into Cloud Run max-instances, a hard cost boundary
	MaxDatabases         int
	MaxDatabaseGB        int
	MaxCustomDomains     int
	SourceSizeLimitMB    int
}{
	MaxProjects:            10,
	MaxServicesPerProject:  10,
	MaxInstancesPerService: 10,
	MaxDatabases:           5,
	MaxDatabaseGB:          10,
	MaxCustomDomains:       5,
	SourceSizeLimitMB:      500,
}
```

### 11.2 Usage Metering & Balance Deduction

Non-blocking metering that does not affect the hot path. Metering runs once an hour and deducts from the balance in real time.

**Scheduler: Postgres `FOR UPDATE SKIP LOCKED`, not river delayed jobs.**

river's delayed jobs rely on Postgres notify/listen under the hood, which can lose notifications under high concurrency. Billing data cannot be best-effort.

Postgres `FOR UPDATE SKIP LOCKED` is a row-level distributed lock, suited to the case where several API server instances run at the same time:
- Only one instance can lock a given project row
- Another instance that meets a locked row simply skips it (SKIP LOCKED)
- The whole metering action is wrapped in a transaction: `next_meter_at` is updated only on success, and a failure rolls back automatically and continues next time

```sql
-- schema (GORM AutoMigrate or a manual migration)
CREATE TABLE billing_checkpoints (
  project_id    TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
  last_metered_at TIMESTAMPTZ,
  next_meter_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- INSERT a checkpoint row when the project is created
```

```go
// internal/billing/metering.go
package billing

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

func StartMeteringLoop(ctx context.Context, db *gorm.DB) {
	ticker := time.NewTicker(1 * time.Minute) // scan once every minute
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				if err := runMeteringBatch(ctx, db); err != nil {
					slog.Error("metering batch failed", "error", err)
				}
			}
		}
	}()
}

func runMeteringBatch(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var projectIDs []string
		if err := tx.Raw(`
			SELECT project_id FROM billing_checkpoints
			WHERE next_meter_at <= NOW()
			ORDER BY next_meter_at
			LIMIT 10
			FOR UPDATE SKIP LOCKED
		`).Scan(&projectIDs).Error; err != nil {
			return err
		}

		for _, projectID := range projectIDs {
			cost, err := meterAndCalculateCost(ctx, tx, projectID)
			if err != nil {
				slog.Error("meter failed", "project", projectID, "error", err)
				continue
			}
			if cost > 0 {
				if err := deductBalance(ctx, tx, projectID, cost); err != nil {
					slog.Error("deduct failed", "project", projectID, "error", err)
					continue
				}
			}
			tx.Exec(`
				UPDATE billing_checkpoints
				SET last_metered_at = NOW(),
				    next_meter_at   = NOW() + INTERVAL '1 hour'
				WHERE project_id = ?
			`, projectID)
		}
		return nil
	})
}

func meterAndCalculateCost(ctx context.Context, tx *gorm.DB, projectID string) (float64, error) {
	var services []Service
	if err := tx.Where("project_id = ? AND provider = ?", projectID, "cloud_run").Find(&services).Error; err != nil {
		return 0, err
	}

	var totalCost float64

	for _, svc := range services {
		metrics, err := getCloudRunMetrics(ctx, svc.ProviderID, []string{
			"run.googleapis.com/container/request_count",
			"run.googleapis.com/container/cpu/utilizations",
			"run.googleapis.com/container/memory/utilizations",
		}, "last_1h")
		if err != nil {
			return 0, err
		}

		cost := (metrics.RequestCount/1_000_000)*UsageRates.ComputeRequests +
			metrics.VCPUSeconds*UsageRates.ComputeCPU +
			metrics.GiBSeconds*UsageRates.ComputeMemory

		tx.Create(&UsageRecord{
			ProjectID:   projectID,
			ServiceID:   svc.ID,
			Category:    "compute",
			Quantity:    metrics.RequestCount,
			Unit:        "requests",
			CostUSD:     cost,
			PeriodStart: metrics.Start,
			PeriodEnd:   metrics.End,
		})

		totalCost += cost
	}

	// ... same for metering Neon DB, Upstash Redis, R2 storage, bandwidth, always-on
	return totalCost, nil
}

func deductBalance(ctx context.Context, tx *gorm.DB, projectID string, cost float64) error {
	var project Project
	if err := tx.First(&project, "id = ?", projectID).Error; err != nil {
		return err
	}

	var team Team
	if err := tx.Raw(`
		UPDATE teams SET balance = balance - ?
		WHERE id = ?
		RETURNING balance, auto_top_up, auto_top_up_amount, auto_top_up_threshold, stripe_customer_id
	`, cost, project.TeamID).Scan(&team).Error; err != nil {
		return err
	}

	// Insufficient balance → auto top-up or suspend the service
	if team.Balance <= 0 {
		if team.AutoTopUp && team.AutoTopUpAmount > 0 {
			return processAutoTopUp(ctx, tx, project.TeamID, team.AutoTopUpAmount)
		}
		suspendAllServices(ctx, tx, project.TeamID) // scale to zero
		sendNotification(ctx, project.TeamID, "balance_depleted", nil)
	} else if team.Balance < Credits.LowBalanceAlert {
		sendNotification(ctx, project.TeamID, "low_balance", map[string]any{"balance": team.Balance})
	}
	return nil
}
```

### 11.3 Top-Up Flow

```go
// internal/billing/topup.go
package billing

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type TopUpResult struct {
	Credited   float64 `json:"credited"`
	Bonus      float64 `json:"bonus"`
	NewBalance float64 `json:"new_balance"`
}

func TopUp(ctx context.Context, db *gorm.DB, teamID string, amount float64, stripePaymentID string) (*TopUpResult, error) {
	if amount < Credits.MinimumTopUp {
		return nil, fmt.Errorf("minimum top-up amount is $%.0f", Credits.MinimumTopUp)
	}

	var result TopUpResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var team Team
		if err := tx.First(&team, "id = ?", teamID).Error; err != nil {
			return err
		}

		// First top-up bonus $5
		bonus := 0.0
		if team.LifetimeTopUp == 0 {
			bonus = Credits.FirstTopUpBonus
		}
		totalCredit := amount + bonus

		// Update the balance
		tx.Exec(`
			UPDATE teams
			SET balance = balance + ?,
			    lifetime_top_up = lifetime_top_up + ?
			WHERE id = ?
		`, totalCredit, amount, teamID)

		// Record the top-up
		tx.Create(&TopUpRecord{
			TeamID:          teamID,
			Amount:          amount,
			Bonus:           bonus,
			StripePaymentID: stripePaymentID,
		})

		// If the service was suspended earlier because of insufficient balance, resume it
		if team.Balance <= 0 {
			resumeSuspendedServices(ctx, tx, teamID)
		}

		result = TopUpResult{Credited: totalCredit, Bonus: bonus, NewBalance: team.Balance + totalCredit}
		return nil
	})
	return &result, err
}
```

### 11.4 Monthly Statement (for Records Only, No Charge)

Under the prepaid model, deductions happen in real time (11.2). The monthly statement is only a usage summary for users to check their account.

```go
// internal/billing/invoice.go
// river periodic job: 00:00 UTC on the 1st of every month

package billing

import (
	"context"
	"time"

	"gorm.io/gorm"
)

func GenerateMonthlyInvoices(ctx context.Context, db *gorm.DB) error {
	var allTeams []Team
	if err := db.WithContext(ctx).Find(&allTeams).Error; err != nil {
		return err
	}

	lastMonth := time.Now().AddDate(0, -1, 0)
	period := lastMonth.Format("2006-01")

	for _, team := range allTeams {
		usage, err := aggregateMonthlyUsage(ctx, db, team.ID)
		if err != nil {
			continue
		}

		db.WithContext(ctx).Create(&Invoice{
			TeamID:     team.ID,
			Period:     period,
			Compute:    usage.Compute,
			Database:   usage.Database,
			Cache:      usage.Cache,
			Storage:    usage.Storage,
			Bandwidth:  usage.Bandwidth,
			AlwaysOn:   usage.AlwaysOn,
			GPUCharges: usage.GPU,
			Total:      usage.Total,
			Status:     "finalized",
		})
	}
	return nil
}
```

---

## 12. Rate Limiting

```go
// internal/auth/ratelimit.go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client

func init() {
	opt, _ := redis.ParseURL(os.Getenv("UPSTASH_REDIS_URL"))
	rdb = redis.NewClient(opt)
}

type RateLimitConfig struct {
	Window time.Duration
	Max    int
}

var rateLimits = map[string]RateLimitConfig{
	"global":    {Window: 1 * time.Minute, Max: 100}, // 100 requests per minute per user
	"deploy":    {Window: 1 * time.Minute, Max: 10},  // 10 deploys per minute per user
	"provision": {Window: 1 * time.Minute, Max: 20},  // 20 provisions per minute per user
}

func checkRateLimit(ctx context.Context, key string, cfg RateLimitConfig) (remaining int, reset time.Time, allowed bool) {
	now := time.Now()
	windowKey := fmt.Sprintf("rl:%s:%d", key, now.UnixMilli()/cfg.Window.Milliseconds())

	count, _ := rdb.Incr(ctx, windowKey).Result()
	if count == 1 {
		rdb.Expire(ctx, windowKey, cfg.Window)
	}

	remaining = cfg.Max - int(count)
	if remaining < 0 {
		remaining = 0
	}
	reset = now.Truncate(cfg.Window).Add(cfg.Window)
	return remaining, reset, int(count) <= cfg.Max
}

func RateLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.Context().Value(ctxUserIDKey).(string)
		cfg := rateLimits["global"]

		remaining, reset, allowed := checkRateLimit(r.Context(), "global:"+userID, cfg)

		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))

		if !allowed {
			http.Error(w, `{"error":"Rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

---

## 13. SkyPilot Integration

SkyPilot is a Python SDK. The main Go program calls it through a subprocess:

```go
// internal/provider/skypilot.go
package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type SkyPilotOpts struct {
	Name        string
	SourcePath  string
	GPU         string
	Env         map[string]string
	MinReplicas int
	MaxReplicas int
	UseSpot     bool
}

func DeployInference(ctx context.Context, opts SkyPilotOpts) (string, error) {
	if opts.MaxReplicas == 0 {
		opts.MaxReplicas = 3
	}

	yaml := generateSkyYAML(opts)
	yamlPath := fmt.Sprintf("/tmp/sky-%s.yaml", opts.Name)
	if err := os.WriteFile(yamlPath, []byte(yaml), 0644); err != nil {
		return "", fmt.Errorf("write sky yaml: %w", err)
	}

	script := fmt.Sprintf(`
import sky
task = sky.Task.from_yaml('%s')
request_id = sky.serve.up(task, service_name='%s')
name, endpoint = sky.get(request_id)
print(endpoint)
`, yamlPath, opts.Name)

	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("skypilot deploy: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func generateSkyYAML(opts SkyPilotOpts) string {
	envLines := ""
	for k, v := range opts.Env {
		envLines += fmt.Sprintf("  %s: \"%s\"\n", k, v)
	}

	return fmt.Sprintf(`
service:
  readiness_probe: /health
  replica_policy:
    min_replicas: %d
    max_replicas: %d
    target_qps_per_replica: 5
    use_spot: %t
resources:
  accelerators: %s
  ports: 8080
envs:
%ssetup: |
  pip install -r requirements.txt
run: |
  python serve.py --port 8080
`, opts.MinReplicas, opts.MaxReplicas, opts.UseSpot, opts.GPU, envLines)
}
```

---

## 14. LaunchKit's Own Deployment Architecture

### Production Deployment

```
GCP Project: launchkit-prod
│
├── Cloud Run Service: launchkit-api (primary)
│   ├── Image: us-east4-docker.pkg.dev/launchkit-prod/launchkit/api:latest
│   ├── CPU: 2 vCPU, always allocated
│   ├── Memory: 2 GiB
│   ├── Min instances: 2 (HA + avoid cold starts)
│   ├── Max instances: 20
│   └── Handles: MCP endpoint, REST API, upload, secret forms, SSE
│
├── Compute Engine VM: launchkit-buildkit
│   ├── Machine: e2-standard-4 (4 vCPU, 16 GiB)
│   ├── Disk: 100GB pd-ssd (BuildKit cache, GC auto-managed)
│   ├── Zone: us-east4-c
│   ├── OS: Ubuntu 22.04 LTS + containerd + gVisor (runsc)
│   ├── buildkitd TCP :1234 (VPC internal network, not exposed to the public internet)
│   │   containerd worker + runsc runtime (gVisor multi-tenant isolation)
│   │   CNI bridge network isolation (blocks metadata + VPC internal network)
│   ├── Concurrency control: river MaxWorkers: 3 (excess builds queue)
│   ├── Idle management: 30 min with no build → auto stop (SSD cache kept)
│   │   build request → auto start (~30s cold start)
│   ├── The API Server connects directly through a river job handler (not a separate Cloud Run Job)
│   ├── Pre-warm: pre-build common framework caches before the beta goes live
│   └── Fallback: when the VM is unhealthy the API Server degrades to Cloud Build automatically
│
├── Artifact Registry: launchkit-prod/user-images
│   ├── Users' container images
│   ├── Tagging strategy: at deploy time the deployment_id is used as the tag (immutable)
│   │   After the new revision is ready, the Deploy Worker moves the live-{serviceId} tag to the new image
│   └── Cleanup policy (GCP native, configured in terraform):
│       keepCount: 5 tagged versions per repository
│       deleteUntagged: true(untagged layers older than 1 day are removed automatically)
│       // Keeping 5 versions provides rollback capability; the cost scales linearly with the number of services, not with the number of deploys
│
├── GCS Bucket: launchkit-builds(us-east4, same region as the BuildKit VM)
│   ├── Temporary storage for build source tarballs (uploaded via presigned URL)
│   ├── Lifecycle rule: deleted automatically after 24 hours (not needed once the deploy is done)
│   ├── IAM: the API Server SA has signBlob + objectAdmin permissions
│   └── Advantages: zero latency in the same region, native GCP IAM authentication, no cross-cloud egress
│
├── Neon Postgres: launchkit-db
│   ├── LaunchKit's own database
│   ├── Read replica for analytics queries
│   └── Daily auto backup
│
├── Upstash Redis: launchkit-queue
│   ├── river job queue (Postgres-based)
│   ├── SSE event bus (Pub/Sub)
│   ├── Rate limiting
│   └── Session cache
│
├── Cloudflare:
│   ├── Zone: launchkit.dev
│   ├── Zone: launchkit.app (user apps)
│   ├── WAF rules (DDoS, bot protection)
│   ├── DNS: mcp.launchkit.dev → Cloud Run
│   ├── DNS: dashboard.launchkit.dev → Cloudflare Pages
│   ├── DNS: *.launchkit.app → Cloud Run (user apps)
│   ├── R2 Bucket: launchkit-user-storage(user object storage, provision_storage)
│   └── Pages: LaunchKit Dashboard (Next.js)
│
├── AWS KMS:
│   └── Multi-region Key(mrk-xxx, $1/month), Tink envelope encryption
│
├── Auth0:
│   ├── Tenant: launchkit
│   ├── Social connections: Google (primary), GitHub
│   ├── Enterprise connections: SAML/OIDC (Enterprise discussion)
│   └── Dynamic Client Registration enabled
│
└── Stripe:
    ├── Payment processing(top-up)
    ├── Auto top-up
    └── Invoice records(monthly statement records)
```

### HA / DR Strategy

| Component | HA mechanism | RTO | RPO |
|------|--------|-----|-----|
| API Server | Cloud Run min 2 instances, multi-zone | 0 | 0 |
| Database | Neon auto-failover + daily backup | <1 min | <1 min |
| Redis | Upstash multi-zone replication | <1 min | <1 min |
| DNS | Cloudflare anycast (multi-PoP) | 0 | 0 |
| KMS | AWS multi-AZ HSM | 0 | 0 |

### Future: Multi-Region Active-Active

```
Phase 1:
  US: Cloud Run us-east4 (Virginia) ← same geographic region as Neon aws-us-east-1, latency ~5ms
  Neon: aws-us-east-1 (Virginia)
  Upstash: GCP us-east4

Phase 2 (Multi-Region):
  US: Cloud Run us-east4 (primary)
  EU: Cloud Run europe-west1 (for GDPR users)
  Neon: aws-eu-central-1 (Frankfurt) for EU users

  Cloudflare routes users to the nearest API server by region
  Neon read replica in EU
  Writes go back to the US primary

Phase 3 (once Neon's GCP region is available):
  LaunchKit migrates all Neon projects to the GCP region in the background
  Users notice nothing, latency goes from ~5ms → < 1ms
  Migration steps: Neon region migration API → update the Tink-encrypted connection string → injected automatically on the next deploy
```

### Orphan Reconciliation (Reclaiming Orphaned Resources)

> **Problem**: The Orchestrator crashes after the provider API call succeeds but before the row is written to the `resources` table → a resource is running on the provider side with no record in the DB → nobody knows it exists → it keeps burning money.
> The reverse is the same: when a project is deleted and the provider API times out → the DB record is deleted while the provider-side resource is still there.
>
> **This is a core reliability problem for a PaaS. Left unhandled = a cost black hole that grows linearly with the user base.**

```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│  River Periodic Job: runs once every hour                                            │
│                                                                                      │
│  1. List all active resources on the provider side (Neon projects, Upstash DBs,      │
│     Cloud Run services, R2 buckets)                                                  │
│  2. Compare against the LaunchKit resources table                                    │
│  3. Provider has it, DB does not → orphan candidate                                  │
│  4. Mark it as an orphan + wait out a grace period (1 hour)                          │
│  5. Still an orphan on the second scan → clean up automatically + write an audit log │
│  6. Write the result to a Cloud Monitoring custom metric                             │
└──────────────────────────────────────────────────────────────────────────────────────┘
```

```go
// internal/monitor/orphan.go
package monitor

import (
	"context"
	"log/slog"
	"time"
)

// OrphanCandidate marks an orphaned resource awaiting confirmation
type OrphanCandidate struct {
	Provider    string    // neon / upstash / cloud_run / r2
	ProviderID  string    // resource ID on the provider side
	DetectedAt  time.Time // time of first detection
	Confirmed   bool      // set to true after the second confirmation
}

// ReconcileOrphans is called every hour by a river periodic job
func ReconcileOrphans(ctx context.Context, deps ReconcileDeps) error {
	logger := slog.With("job", "orphan_reconciliation")
	var totalOrphans int

	for _, provider := range deps.Providers {
		// 1. List all resources on the provider side that carry the launchkit tag
		providerResources, err := provider.ListAll(ctx)
		if err != nil {
			logger.Error("failed to list provider resources",
				"provider", provider.Key(), "error", err)
			continue // one provider failing does not block the others
		}

		// 2. Batch-query the provider_ids known in the DB
		knownIDs, err := deps.DB.GetKnownProviderIDs(ctx, provider.Key())
		if err != nil {
			logger.Error("failed to query known resources", "error", err)
			continue
		}

		// 3. Compare: in the provider but not in the DB = orphan candidate
		for _, res := range providerResources {
			if _, known := knownIDs[res.ProviderID]; known {
				continue
			}

			candidate, exists := deps.OrphanCache.Get(res.ProviderID)
			if !exists {
				// First detection: mark it but do not delete (grace period)
				deps.OrphanCache.Set(res.ProviderID, OrphanCandidate{
					Provider:   provider.Key(),
					ProviderID: res.ProviderID,
					DetectedAt: time.Now(),
				})
				logger.Warn("orphan candidate detected",
					"provider", provider.Key(), "provider_id", res.ProviderID)
				continue
			}

			// Second confirmation (grace period has passed)
			if time.Since(candidate.DetectedAt) >= 1*time.Hour {
				logger.Warn("confirmed orphan, destroying",
					"provider", provider.Key(), "provider_id", res.ProviderID)
				if err := provider.Delete(ctx, res.ProviderID); err != nil {
					logger.Error("failed to destroy orphan",
						"provider", provider.Key(), "provider_id", res.ProviderID, "error", err)
				} else {
					deps.OrphanCache.Delete(res.ProviderID)
					deps.AuditLog.Record(ctx, "orphan_destroyed", map[string]any{
						"provider":    provider.Key(),
						"provider_id": res.ProviderID,
					})
					totalOrphans++
				}
			}
		}
	}

	// 4. Write a custom metric (for GCP Cloud Monitoring alerts)
	emitOrphanMetric(ctx, totalOrphans)
	return nil
}
```

**Providers must support a `ListAll()` method** (added in `interfaces.go`):

```go
// Every provider must support resource enumeration (for orphan reconciliation)
type Reconcilable interface {
	ListAll(ctx context.Context) ([]ProviderResource, error)
}

// Neon: GET /projects (filter tag = launchkit)
// Upstash: GET /v2/redis/databases
// Cloud Run: services.list (filter label launchkit-managed=true)
// R2: ListBuckets (filter prefix launchkit-)
```

**Key design decisions:**
- **1-hour grace period**: avoids deleting resources that are "still being created" (the orchestrator's normal flow may take a few minutes)
- **Second confirmation**: the first scan only marks, the second deletes, which protects against transient provider API inconsistency
- **Tag/Label filtering**: only reclaim resources that carry the LaunchKit tag, never touching other resources in the user's account (BYOC scenario)
- **One provider failing does not block the rest**: a Neon API outage does not affect Upstash reconciliation

### External Dependency Failure Matrix

> What happens when each external dependency goes down? How should LaunchKit react?
> The bar for a $55M product: **no single external service outage may make LaunchKit completely unavailable.**

| Dependency | Impact when down | Degraded behavior | Retry strategy |
|------|-----------|---------|---------|
| **Neon** (Postgres) | ❌ All writes fail, user provisioning fails | Reads go to the read replica; writes are queued in river (when Postgres is down river is down too, see below) | Exponential backoff, 3 attempts |
| **Upstash** (Redis) | ⚠️ SSE push is interrupted, rate limiting stops working, the event bus is unavailable | Rate limiting degrades to in-memory (leaky bucket, resets on restart but acceptable); SSE falls back to polling; the event bus queues until recovery | Exponential backoff, 3 attempts |
| **Cloudflare** (DNS/R2/Pages) | ⚠️ User object storage fails, frontend deploys fail, the Dashboard is unavailable | Backend deploys are unaffected (build uploads go through GCS, and Cloud Run does not depend on Cloudflare); when user storage fails → tell the user clearly to retry later | No retry (infrastructure-level failure) |
| **AWS KMS** | ❌ All secret encryption/decryption fails → new deploys cannot inject env | Deployed services are unaffected (env already injected); new deploys block without crashing and wait for KMS to recover | Exponential backoff, 5 attempts, 1s-30s apart |
| **Auth0** | ❌ New users cannot log in, new tokens cannot be issued | Existing JWTs can still be validated (the public key is cached); a new login fails → 503 + a clear error message | No retry (the user just logs in again) |
| **Stripe** | ⚠️ Top-ups and auto top-up fail | Deployed services are unaffected; a low balance does not suspend for now (24h grace period) | Stripe webhook retry (Stripe retries on its own) |
| **Neon (user DB)** | ⚠️ The user's provisioning fails | The orchestrator rolls back resources already created; the user can retry later | Exponential backoff, 3 attempts |
| **BuildKit VM** | ⚠️ Builds are slower | Automatically degrades to Cloud Build (already designed) | Automatic failover, no retry needed |

**Circuit Breaker implementation:**

```go
// internal/infra/circuitbreaker.go
package infra

import (
	"errors"
	"sync"
	"time"
)

type State int

const (
	Closed   State = iota // normal
	Open                  // open: reject immediately, do not call downstream
	HalfOpen              // half-open: let one request through as a probe
)

type CircuitBreaker struct {
	mu            sync.Mutex
	state         State
	failures      int
	threshold     int           // number of consecutive failures before switching to Open
	resetTimeout  time.Duration // how long Open lasts before switching to HalfOpen
	lastFailureAt time.Time
}

var ErrCircuitOpen = errors.New("circuit breaker is open")

func NewCircuitBreaker(threshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{threshold: threshold, resetTimeout: resetTimeout}
}

func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	switch cb.state {
	case Open:
		if time.Since(cb.lastFailureAt) > cb.resetTimeout {
			cb.state = HalfOpen
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	}
	cb.mu.Unlock()

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err != nil {
		cb.failures++
		cb.lastFailureAt = time.Now()
		if cb.failures >= cb.threshold {
			cb.state = Open
		}
		return err
	}
	cb.failures = 0
	cb.state = Closed
	return nil
}

// One circuit breaker instance per external dependency
var (
	NeonCB       = NewCircuitBreaker(5, 30*time.Second)
	UpstashCB    = NewCircuitBreaker(5, 15*time.Second)
	CloudflareCB = NewCircuitBreaker(3, 60*time.Second)
	KMSCB        = NewCircuitBreaker(3, 10*time.Second) // KMS recovers quickly, so the timeout is short
)
```

**Degrading to an in-memory rate limiter when Redis is down:**

```go
// internal/middleware/ratelimit_fallback.go
// When Redis is unavailable, degrade to sync.Map + leaky bucket (resets on restart but is never completely unlimited)

func checkRateLimitWithFallback(ctx context.Context, key string, cfg RateLimitConfig) (int, time.Time, bool) {
	err := UpstashCB.Execute(func() error {
		// Normal: Redis sliding window
		remaining, reset, allowed = checkRateLimit(ctx, key, cfg)
		return nil
	})
	if err != nil {
		// Degraded: in-memory rate limiting (loose but not unlimited)
		return checkInMemoryRateLimit(key, cfg)
	}
	return remaining, reset, allowed
}
```

### Data Retention Policy (Retention and Cleanup)

> Not defining a retention policy = unbounded data growth = a ticking time bomb.
> Retaining too briefly = history cannot be queried. Retaining too long = cost and performance problems.

| Data | Retention | Cleanup method | Reason |
|------|---------|---------|------|
| `audit_logs` | **365 days** | river periodic job (daily 02:00 UTC) | SOC 2 requires a minimum of 1 year |
| `usage_records` | **90 days** (detail), summaries kept permanently | On the 1st of each month: detail older than 90 days is deleted, and the monthly summary is written to `invoices` | The monthly statement is a permanent record |
| `deployments.build_log` | **30 days** | river periodic job: build_log older than 30 days is set to NULL | Build logs run tens of KB to MB, high volume |
| `secret_requests` (expired) | **7 days** | river periodic job: `expires_at < now() - 7 days` → delete | Expired ones have no value |
| `incidents` (resolved) | **180 days** | river periodic job | Half a year is enough for lookback |
| `backups` | **Per `retention_until`** | river periodic job: `retention_until < now()` → delete the record + call the provider to delete the backup | User-configurable |
| Cloud Logging | **90 days** | GCP setting (Log Router sink retention) | Beyond 90 days, export to BigQuery |
| Artifact Registry images | **5 versions** | GCP cleanup policy (already set) | Rollback capability |
| GCS build staging (source uploads) | **24 hours** | GCS lifecycle rule (Age: 1 day, action: Delete) | Not needed once the deploy is done |

```go
// internal/monitor/retention.go
// river periodic job: daily 02:00 UTC

package monitor

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

func RunRetentionCleanup(ctx context.Context, db *gorm.DB) error {
	logger := slog.With("job", "retention_cleanup")
	now := time.Now()

	// 1. Audit logs: 365 days
	res := db.WithContext(ctx).
		Where("created_at < ?", now.AddDate(-1, 0, 0)).
		Delete(&AuditLog{})
	logger.Info("audit_logs cleaned", "deleted", res.RowsAffected)

	// 2. Usage records: 90 days (detail)
	res = db.WithContext(ctx).
		Where("created_at < ?", now.AddDate(0, -3, 0)).
		Delete(&UsageRecord{})
	logger.Info("usage_records cleaned", "deleted", res.RowsAffected)

	// 3. Build logs: 30 days (clear only the log content, do not delete the deployment record)
	res = db.WithContext(ctx).
		Model(&Deployment{}).
		Where("created_at < ? AND build_log IS NOT NULL", now.AddDate(0, -1, 0)).
		Update("build_log", nil)
	logger.Info("build_logs cleaned", "cleared", res.RowsAffected)

	// 4. Expired secret requests: 7 days
	res = db.WithContext(ctx).
		Where("expires_at < ?", now.AddDate(0, 0, -7)).
		Delete(&SecretRequest{})
	logger.Info("expired_secret_requests cleaned", "deleted", res.RowsAffected)

	// 5. Resolved incidents: 180 days
	res = db.WithContext(ctx).
		Where("resolved_at IS NOT NULL AND resolved_at < ?", now.AddDate(0, -6, 0)).
		Delete(&Incident{})
	logger.Info("resolved_incidents cleaned", "deleted", res.RowsAffected)

	// 6. Expired backups
	res = db.WithContext(ctx).
		Where("retention_until < ?", now).
		Delete(&Backup{})
	logger.Info("expired_backups cleaned", "deleted", res.RowsAffected)

	return nil
}
```

---

## 15. Cost Model and Profit & Loss Analysis

> GCP Cloud Run pricing baseline (us-east4 Tier 1, verified 2026 Q2):
> - Instance-based (CPU always allocated): CPU $0.000018/vCPU-sec, Memory $0.000002/GiB-sec, no request fee
> - Request-based (CPU only during requests): CPU $0.000024/vCPU-sec, Memory $0.0000025/GiB-sec, $0.40/M requests
> - Cloud Run Jobs: CPU $0.000024/vCPU-sec, Memory $0.0000025/GiB-sec
> - Free tier: 180K vCPU-sec + 360K GiB-sec + 2M requests/month

### 15.1 Core Cost Control Principles

**Prepaid balance: deduct only what is used, and pause when the balance reaches zero. LaunchKit never absorbs costs on behalf of users.**

| Control mechanism | Description |
|---------|------|
| Balance reaches zero → pause | All services scale to zero and stop incurring charges |
| Low balance warning | A notification is sent when the balance < $2 |
| Auto top-up | Users can enable it; the credit card is charged automatically when the balance falls below a threshold |
| Budget cap | Users can set a monthly budget, and services scale to zero automatically once it is reached |
| Hard account limits | max 10 projects, max 10 instances/service, max 10 GiB DB (anti-abuse) |

### 15.2 LaunchKit Operating Side (Fixed Costs, Independent of User Count)

| Service | Spec | Billing method | Monthly cost | Description |
|------|------|---------|------|------|
| Cloud Run `launchkit-api` | 2 instance × 1 vCPU × 1 GiB, always-on | instance-based | **~$100** | CPU: 2×1×2,592,000×$0.000018=$93 + Mem: $10 − free $4 |
| Neon `launchkit-db` | LaunchKit's own DB | pay-as-you-go | **~$3** | ~1 GiB storage × $0.35 + minimal compute |
| Upstash `launchkit-queue` | event bus + rate limit | Free or $10 fixed | **$0–10** | At the 100-user stage it may sit near the edge of the Free tier's 500K cmd/month |
| Cloudflare Pro | DNS + CDN + WAF | Monthly fee | **$20** | |
| Auth0 Essentials | OAuth login | Monthly fee | **$23** | |
| AWS KMS | 1 multi-region key (Tink envelope encryption) | Monthly fee | **$1** | |
| Compute Engine `launchkit-buildkit` | e2-standard-4 + 100GB pd-ssd, always on | instance-based | **~$115** | VM $98 + SSD $17; cached build 7-58s |
| GCS bucket | Cloud Build fallback staging | Pay per use | **~$0** | Used only for the fallback; the normal path does not go through GCS |
| Artifact Registry `launchkit/api` | LaunchKit's own image | $0.10/GB | **~$0.10** | |
| Domain | launchkit.dev + launchkit.app | Annual fee spread monthly | **$2** | |
| **Total fixed cost** | | | **~$265–275** | |

### 15.3 User-Side Costs (Variable Costs, Itemized)

#### Cost per Deployment

| Step | Network cost | BuildKit VM (primary) | Cloud Build (fallback) | Description |
|------|---------|-------------------|----------------------|------|
| Upload source to GCS | $0 | ~$0 | ~$0 | GCS same-region ingress is free |
| railpack prepare (inside the API Server) | $0 | $0 | $0 | 2-3s, in-process, marginal CPU negligible |
| Image build | $0 | **$0 (the VM is a fixed cost)** | **$0 (free allowance) / $0.029** | VM cached 7-58s; CB cold 77-197s |
| Artifact Registry push | $0 | $0 | $0 | Same region |
| Cloud Run deploy | $0 | $0 | $0 | |
| Cloudflare DNS | $0 | $0 | $0 | |
| **Total per deploy** | **$0** | **~$0** | **~$0–0.03** | VM path: zero marginal cost |

> The BuildKit VM is a fixed cost ($115/month) that does not grow with the number of builds. Cloud Build is only the fallback when the VM is unhealthy.
> Measured performance: Python cached 7.4s, Next.js cached 58.5s, Rust ~15s (vs Cloud Build cold 85-197s).

#### Cost vs Revenue Under the Prepaid Model (by Usage Scenario)

| User scenario | Our cost | Charged to user | Margin | How long the user's $10 balance lasts |
|---------|-----------|---------|--------|-------------------|
| Very light (static site + occasional API, 50K req/month) | ~$0.40 | ~$0.60 | 50% | **~16 months** |
| Light (API + DB, 100K req/month) | ~$0.80 | ~$1.20 | 50% | **~8 months** |
| Medium (3 svc, 500K req, 2 DB) | ~$4.00 | ~$6.00 | 50% | **~7 weeks** |
| Heavy (1M req, 5 DB, Redis, always-on) | ~$12.00 | ~$18.00 | 50% | **~2 weeks** |

> Very light and light users are the majority; $5 top-up + $5 bonus = $10 lasts a long time, and conversion and retention are expected to beat a $9/month subscription.
> Heavy users consume quickly, but margin is steady, and their willingness to keep topping up is high (they have already committed).

#### Revenue Model Comparison: Prepaid vs the Old Subscription Model

| | Old subscription ($9/month and up) | New prepaid ($5 and up) |
|---|---|---|
| Monthly revenue from a light user | $9 (far above actual usage) | ~$0.60 (matches usage) |
| Light user retention | Low (feels not worth it) | High (pay for what you use) |
| Monthly revenue from a heavy user | $9-27 (including the overage cap) | $18+ (no cap, reflects real usage) |
| Customer acquisition cost | High (the $9 barrier scares people off) | Low ($5 entry, plus $5 free) |
| Unit economics | Light users have high margin but high churn | Margin steady at ~50%, large user base |

### 15.4 Cross-Cloud Boundary Costs

| Boundary | Direction | Cost | Description |
|------|------|------|------|
| GCS → BuildKit VM (build download) | GCS us-east4 → VM us-east4 | **$0** | Same-region internal traffic is free |
| GCP → Cloudflare (user API responses) | GCP → Cloudflare | **$0.08/GB** | CDN Interconnect partner rate |
| GCP → Neon (DB queries) | GCP us-east4 → AWS us-east-1 | **~5ms latency, negligible cost** | Same geographic region (Virginia), query payloads of a few KB |
| GCP → Upstash (Redis) | GCP → GCP | **$0** | Choose the Upstash GCP us-east4 region (same region as Cloud Run) |
| Frontend (Cloudflare Pages) | Cloudflare edge | **$0** | Zero egress fee |
| File downloads (R2) | Cloudflare edge | **$0** | R2 zero egress |

**⚠️ The GCP CDN Interconnect rate goes up from $0.04 to $0.08/GB starting 2026-05-01.** Cloudflare proxied mode (orange cloud) gets this rate automatically, but traffic must go over IPv4 (IPv6 does not qualify).

**⚠️ Neon cross-cloud latency (a mitigation strategy that has been verified):**
Neon currently has only AWS + Azure regions; the GCP region was originally planned for the end of 2025 but has slipped with no new ETA.
Mitigation: run Cloud Run in `us-east4` (Virginia) and Neon in `aws-us-east-1` (Virginia); the same geographic region gives ~5ms/query latency (not 20-40ms).
The Cloud SQL alternative has been ruled out: creating a Cloud SQL instance takes over 3 minutes, which does not meet the product requirement that "provision_database responds in seconds".
**All users use Neon; account limits only cap the number and size of DBs (anti-abuse) and do not affect pricing.**
When Neon ships a GCP region LaunchKit migrates everyone in the background; users notice nothing, and latency drops from ~5ms to < 1ms.
Upstash is recommended in the GCP us-east4 region (same region as Cloud Run, latency < 1ms).

### 15.5 Egress Traffic Analysis

**SPA + separate API architecture (recommended):**
- Frontend (React/Vue) → Cloudflare Pages → **$0**
- API (JSON responses) → Cloud Run → Cloudflare → **$0.08/GB**

| Monthly API requests | JSON egress (avg 5KB) | GCP egress |
|----------------|--------------------|-----------| 
| 100K | 500 MB | $0.04 |
| 1M | 5 GB | $0.40 |
| 10M | 50 GB | $4.00 |

**Next.js SSR (the whole app on Cloud Run):**
- `_next/static/*` → Cloudflare cache (~99% hit) → **$0**
- SSR HTML → Cloud Run egress → **$0.08/GB**

| Monthly page views | SSR HTML (avg 30KB) | GCP egress |
|--------------|--------------------|-----------| 
| 100K | 3 GB | $0.24 |
| 1M | 30 GB | $2.40 |

**Conclusion: egress traffic is not a cost problem.** The frontend on Cloudflare Pages is entirely free; API JSON payloads are tiny; SSR static assets are cached.

### 15.6 100-Customer P&L Model (Phase 1 Target)

**Assumption: 100 prepaid users, with usage distributed as (very light 30% / light 40% / medium 20% / heavy 10%)**

| | Amount | Calculation |
|--|------|---------|
| **Revenue (user spend = our revenue)** | | |
| 30 very light × $0.60/month | $18 | Static site + a little API |
| 40 light × $1.20/month | $48 | API + DB, 100K req |
| 20 medium × $6.00/month | $120 | 3 svc, 500K req, 2 DB |
| 10 heavy × $18.00/month | $180 | 1M req, DB, Redis, always-on |
| **Monthly revenue** | **$366** | |
| | | |
| **Fixed operating costs** | | |
| LaunchKit servers (see 15.2) | -$270 | Cloud Run $100 + BuildKit VM $115 + Neon $3 + Upstash $10 + CF $20 + Auth0 $23, etc. |
| | | |
| **User-side variable costs (~67% of revenue)** | | |
| Underlying cloud resources for all users | -$244 | Markup ~50%, so cost ≈ revenue × 0.67 |
| Cloud Build fallback | -$2 | Used only when the VM is unhealthy |
| Artifact Registry shared storage | -$15 | ~150 GB × $0.10 |
| GCP → Cloudflare egress | -$5 | CDN Interconnect $0.08/GB |
| | | |
| **Other** | | |
| Stripe fee 3% | -$11 | 3% of the top-up amount |
| First top-up bonus (new-user CAC) | -$50 | Assuming 10 new users this month × $5 bonus |
| | | |
| **Monthly net profit** | **-$231 (loss)** | Early fixed cost of $270 is too large a share |

> **Still a loss at 100 users**, which is characteristic of the early prepaid model: the fixed cost of $270/month needs more users to spread it out.
> The break-even point is about **250-300 users** (monthly revenue must reach ≥ $550 to cover the fixed costs).
> The subscription model looks more profitable at 100 users ($1,410 vs $366), but those light users renew at a low rate, so the actual MRR would keep declining.

### 15.7 Cost Driver Ranking (Based on Measured Data)

| Rank | Item | Monthly cost at 100 users | Share | Controllability |
|------|------|------------|------|--------|
| 1 | LaunchKit fixed costs (API + BuildKit + DB + queue, etc.) | $270 | 50% | CUD discounts can lower it to ~$200 |
| 2 | Cloud Run user apps (compute) | ~$130 | 24% | Account limits cap max instances |
| 3 | Neon user DBs (storage + compute) | ~$50 | 9% | Account limits cap GiB + DB count |
| 4 | First top-up bonus | $50 | 9% | Determined by the number of new users |
| 5 | Upstash user Redis | ~$15 | 3% | Account limits cap commands |
| 6 | Artifact Registry | $15 | 3% | The cleanup policy caps the number of versions |
| 7 | Stripe fees | $11 | 2% | Not controllable |
| 8 | Other | ~$5 | 1% | |

### 15.8 Growth Stage Projection

| Users | Monthly revenue | Fixed operating cost (incl. VM) | User variable | First top-up bonus | Stripe | **Monthly net profit (gross margin)** |
|--------|-------|-----------------|---------|---------|--------|-------------------|
| 100 | $366 | $270 | $244 | $50 | $11 | **-$209 (loss)** |
| 300 | $1,098 | $270 | $732 | $75 | $33 | **-$12 (near break-even)** |
| 500 | $1,830 | $365 | $1,220 | $75 | $55 | **$115 (6%)** |
| 1,000 | $3,660 | $515 | $2,440 | $100 | $110 | **$495 (14%)** |
| 5,000 | $18,300 | $800 | $12,200 | $250 | $549 | **$4,501 (25%)** |

> Early losses under the prepaid model are normal — a low barrier trades for a user base.
> Break-even arrives at around 300 users; after that the fixed costs are diluted and margin rises gradually.
> At 5,000 users margin stabilizes at ~25%, below the old subscription model's ~45%, but the user base is expected to be 3-5 times larger.
> At 500 users the BuildKit VM may need a second one (~$230 total).
> At 500 users the LaunchKit API server needs to auto-scale to 4-6 instances (~$200-300).

---

## 16. Development Roadmap

### Phase 1: Core MVP (6 Weeks)

**Sprint 1: get one deployment running end to end (2 weeks)**
- [ ] net/http + mcp-go server
- [ ] `plan_deployment` tool (takes Claude hints → Plan Engine validates → returns the validated Plan)
- [ ] Basic Plan Engine (Railpack detection + env rule classification + hints merge)
- [ ] `deploy_project` tool (whole-package deploy: orchestrator + build + deploy)
- [ ] Basic orchestrator (single service, no parallelism)
- [ ] `deployment_steps` table + orchestrator step tracking (write a checkpoint at every step)
- [ ] Crash recovery: `RecoverStaleDeployments()` scans and recovers when the API Server starts
- [ ] Build concurrency control: river build worker `MaxWorkers: 3`, the rest queue
- [ ] `status`, `logs` tools (status returns per-step progress)
- [ ] Hardcoded test token (no OAuth yet)
- [ ] Structured logging pipeline: trace_id middleware + slog JSON output
- [ ] Manual test: Claude Code → plan → upload → deploy → live URL

**Sprint 2: resource provisioning + secrets (1.5 weeks)**
- [ ] `provision_database` (Neon), `provision_cache` (Upstash), `provision_storage` (R2)
- [ ] Provider idempotency: every Create() looks up first, then creates (naming: `launchkit-{projectID}-{type}`)
- [ ] Tink + AWS KMS envelope encryption (AAD tenant isolation)
- [ ] `generate_secret`, `request_secrets`, `check_secrets`
- [ ] Secret web form (/s/:id)
- [ ] Reference-based env vars
- [ ] Circuit breaker (Neon / Upstash / Cloudflare / KMS)
- [ ] Provider `ListAll()` interface (foundation for orphan reconciliation)
- [ ] BuildKit VM idle management: river periodic job + auto stop/start

**Sprint 3: OAuth + static sites (1.5 weeks)**
- [ ] Auth0 tenant + Google social login + GitHub social login
- [ ] `ProxyOAuthServerProvider` + `mcpAuthRouter()` + `requireBearerAuth()`
- [ ] Orchestrator supports the frontend (Cloudflare Pages target)
- [ ] End-to-end test of the full OAuth flow

**Sprint 4: Management tools (1 week)**
- [ ] `list_projects`, `get_deployments`, `rollback`, `restart`
- [ ] `update`, `scale`, `set_env`, `destroy`
- [ ] Error handling + retry logic
- [ ] Integration tests

### Phase 2: Enterprise Foundation (6 Weeks)

**Sprint 5: Team + RBAC (2 weeks)**
- [ ] Team / team_members DB schema
- [ ] `create_team`, `invite_member`, `list_members`, `set_role`
- [ ] RBAC middleware (permission checks on all tools)
- [ ] Audit logging (every tool call)
- [ ] `get_audit_log`

**Sprint 6: Environments + Domains (2 weeks)**
- [ ] Environment DB schema
- [ ] `create_environment`, `promote_environment`, `destroy_environment`
- [ ] Staging URL format (`--staging.launchkit.app`)
- [ ] `add_domain`, `check_domain`, `remove_domain`
- [ ] Let's Encrypt auto TLS via Cloudflare API

**Sprint 7: Monitoring + Alerts + Operational Integrity (2 weeks)**
- [ ] Cloud Monitoring API integration
- [ ] Health check scheduler (goroutine ticker)
- [ ] `get_metrics`, `set_alert`, `list_alerts`, `get_incidents`, `get_uptime`
- [ ] Alert evaluation engine
- [ ] Notification channels (email, Slack, webhook)
- [ ] LaunchKit self-monitoring (selfcheck goroutine + custom metrics + GCP alert policies)
- [ ] Orphan reconciliation river periodic job (hourly scan + grace period + automatic cleanup)
- [ ] Data retention cleanup river periodic job (daily 02:00 UTC)
- [ ] PagerDuty integration (Critical alerts → on-call push notifications)

### Phase 3: Billing + Dashboard (4 Weeks)

**Sprint 8: Billing (2 weeks)**
- [ ] Stripe integration (customers, top-up payments, auto top-up)
- [ ] Usage metering (hourly, async) + real-time balance deduction
- [ ] `top_up`, `get_balance`, `set_auto_top_up`, `get_usage`, `get_invoice`, `set_budget`, `get_cost_forecast`
- [ ] Monthly invoice generation (for records)
- [ ] Low balance alerts + budget enforcement + suspend on $0

**Sprint 9: Dashboard v1 (2 weeks)**
- [ ] Next.js on Cloudflare Pages
- [ ] Auth0 login (same tenant as MCP)
- [ ] REST API endpoints (shared service layer)
- [ ] Pages: Projects overview, service status, logs viewer, metrics charts
- [ ] Pages: Billing, team management, settings

### Phase 4: GPU + Advanced (4 Weeks)

**Sprint 10: GPU (2 weeks)**
- [ ] SkyPilot integration (Python subprocess)
- [ ] `deploy_inference`, `launch_training`
- [ ] GPU cost tracking + billing

**Sprint 11: Advanced Ops (2 weeks)**
- [ ] `migrate` (cross-region)
- [ ] `switch_provider` (cross-cloud)
- [ ] `backup_database`, `restore_database`
- [ ] `create_cron`, `list_crons`, `delete_cron`
- [ ] Auto-repair (Phase 2 auto-scaling, auto-rollback)

### Phase 5: Enterprise (Ongoing)

- [ ] SSO/SAML integration (Auth0 Enterprise)
- [ ] VPC Peering
- [ ] IP allowlisting
- [ ] SOC 2 Type II audit
- [ ] Multi-region active-active
- [ ] Advanced analytics + cost optimization AI

### Phase 6: Bring Your Own Cloud (BYOC)

> **Precondition**: Phase 1–4 are complete, and this starts only after enough Pro/Team users ask for it.
> The DB schema (`cloud_accounts`, the `cloud_account_id` FK) and the provider registry stubs are already in place in v1,
> so the actual work is only in the provider layer and the Dashboard UI.

**Sprint A: GCP BYOC (highest priority, because LaunchKit itself runs on GCP)**
- [ ] Dashboard: "Connect GCP Account" UI
  - The user uploads a Service Account JSON or authorizes via OAuth (Workload Identity Federation)
  - Validate the credentials and write them to `cloud_accounts` (Tink encrypted, AAD = team_id)
- [ ] Implement `internal/provider/cloudsql.go` (implements `DatabaseProvider`)
  - Create a Cloud SQL instance in the user's GCP project
  - Support Cloud SQL Auth Proxy connections
- [ ] Implement `internal/provider/gcs.go` (implements `StorageProvider`)
- [ ] Implement `internal/provider/memorystore.go` (implements `CacheProvider`)
- [ ] Add an optional `account_id` parameter to the `provision_database` / `provision_cache` / `provision_storage` tools
- [ ] Replace the registry's `BYOCStub("cloud_sql", ...)` with `cloudSQLProvider`

**Sprint B: AWS BYOC**
- [ ] Dashboard: "Connect AWS Account" UI (IAM Role ARN + ExternalId cross-account assume role)
- [ ] Implement `internal/provider/rds.go` (implements `DatabaseProvider`)
- [ ] Implement `internal/provider/s3.go` (implements `StorageProvider`)
- [ ] Implement `internal/provider/elasticache.go` (implements `CacheProvider`)
- [ ] Implement `internal/provider/awsecs.go` (implements `ComputeProvider`, optional)

**Sprint C: Azure BYOC**
- [ ] Dashboard: "Connect Azure Account" UI (Service Principal)
- [ ] Implement `internal/provider/azuredb.go`, `azureblob.go`, `azurecache.go`

**BYOC architecture invariants** (held throughout Phase 6):
```
- Tool handlers do not change (only an optional account_id parameter is added)
- Selector logic does not change (accountId = null → LaunchKit managed, otherwise BYOC)
- The DB schema does not change (cloud_accounts and the FK were built in v1)
- The only additions: provider implementations + Dashboard UI + replacing registry stubs
```

---

### Target Milestones

| Milestone | Timeline | Deliverable |
|--------|------|------|
| **Internal Alpha** | Week 6 | Core deploy + resources + OAuth |
| **Private Beta** | Week 12 | + Team + Environments + Monitoring |
| **Public Beta** | Week 16 | + Billing + Dashboard |
| **GA v1.0** | Week 20 | + GPU + Advanced Ops |
| **Enterprise GA** | Week 24+ | + SSO + Compliance |
| **BYOC GA** | Week 32+ | + GCP / AWS / Azure BYOC |

---

## 17. Deployment Progress Design (MCP Progress Notifications)

### Design Principles

Deployment is LaunchKit's longest operation (3–10 minutes). While the user waits inside Claude Code they must be able to see live progress, otherwise the experience is a black box.

**Use the official MCP `notifications/progress`**, because:
- It is part of the MCP Protocol spec, and every compliant client must support it
- It does not depend on any particular client implementation (works with Claude Code, Cursor, Windsurf, etc.)
- No extra SSE endpoint or WebSocket is needed; it goes straight over the response stream of MCP Streamable HTTP
- Claude can keep seeing progress during the tool call and update the user in the conversation as it goes

---

### 17.1 Tool Call Flow

Deployment has three steps, of which only two are MCP tool calls:

```
plan_deployment()         ← Tool call 1: Claude sends hints → the Plan Engine validates → returns the validated Plan + upload URL
  ↓
Claude shows the Plan     ← Claude does this itself (including warnings, such as Railpack overriding a Claude hint)
User confirms
Claude uploads via Bash   ← Claude runs it itself (tar + curl to the presigned URL)
  ↓
deploy_project()          ← Tool call 2: long-running (3–10 min), progress reported throughout
  ↓                         The Orchestrator provisions + builds + deploys in parallel per the Plan
{ urls, status: "live" }  ← Final result
```

Internally, `plan_deployment` is handled by the Plan Engine: Railpack detects the framework → rules classify env → merge Claude hints → validate the Plan → persist to Postgres → return. Claude uploads the source code between the two tool calls. `deploy_project` is a long-running tool call: the Orchestrator handles everything in parallel per the validated Plan and reports through progress notifications throughout.

---

### 17.2 `deploy_project` Progress Reporting Implementation

The `deploy_project` handler calls the orchestrator, the orchestrator reports progress through a callback, and the handler converts it into MCP progress notifications:

```go
// internal/mcp/tools/deploy.go

func deployProjectHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	plan := parsePlan(request.Params.Arguments["plan"])
	uploadID := request.Params.Arguments["upload_id"].(string)

	// Confirm GCS has received the source code
	if !gcsObjectExists(ctx, "uploads/"+uploadID) {
		return mcp.NewToolResultError("Source not uploaded yet. Please upload first."), nil
	}

	progressToken := request.Params.Meta.ProgressToken

	// The orchestrator runs and reports progress through a callback
	result, err := orchestrator.Execute(ctx, plan, uploadID, func(pct int, msg string) {
		if progressToken != nil {
			mcpServer.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
				"progressToken": progressToken,
				"progress":      pct,
				"total":         100,
				"message":       msg,
			})
		}
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}
```

---

### 17.3 Orchestrator Event Format

The goroutines inside the Orchestrator report through a progress callback; Redis pub/sub is no longer used as a relay (progress is passed directly inside the orchestrator):

```go
// The orchestrator calls the progress callback internally:
progress(5,   "Provisioning database...")
progress(10,  "Provisioned postgres ✓")
progress(15,  "Waiting for secrets: https://launchkit.dev/s/sr_xxx")
progress(20,  "Building api...")
progress(25,  "Building web...")
progress(55,  "Secrets received ✓")
progress(60,  "Built api ✓")
progress(65,  "Built web ✓")
progress(80,  "Deploying all services...")
progress(90,  "Backend live ✓")
progress(95,  "Frontend live ✓")
progress(100, "Deployment complete")
```

---

### 17.4 What the User Sees in Claude Code

```
User: "Help me deploy this project"

Claude: (scans locally: package.json, .env.example, prisma/schema → produces hints)
Claude: (calls plan_deployment, the Plan Engine validates the hints) "Deployment plan:

  Backend FastAPI → Cloud Run   https://my-app-api.launchkit.app
  Frontend React  → Cloudflare  https://my-app.launchkit.app
  Database PostgreSQL → Neon (created automatically)
  STRIPE_SECRET_KEY → you need to provide it
  Estimated monthly cost ~$1.50

  Confirm the deployment?"

User: "OK"

Claude: (runs git archive | curl to upload the source code)
Claude: (calls deploy_project, the terminal shows progress)

  ██░░░░░░░░░░░░░░░   5%  Provisioning database...
  ███░░░░░░░░░░░░░░  15%  Waiting for secrets...

Claude: "Please fill in the secrets at this link: https://launchkit.dev/s/sr_xxx"

  [The user fills in STRIPE_SECRET_KEY in the browser]

  █████████░░░░░░░░  55%  Secrets received ✓
  ██████████░░░░░░░  60%  Built api ✓
  ███████████░░░░░░  65%  Built web ✓
  █████████████████  90%  Backend live ✓
  █████████████████  95%  Frontend live ✓
  █████████████████ 100%  Deployment complete

Claude: "Deployment complete!
  Backend https://my-app-api.launchkit.app
  Frontend https://my-app.launchkit.app
  Console https://dashboard.launchkit.dev/projects/my-app"
```

If it fails midway:

```
  ██████████░░░░░░░  60%  Building api...
  ✗ Build failed: could not find start command

  Last 5 log lines:
  > railpack prepare /tmp/src --plan-out /tmp/railpack-plan.json
  error: No start command detected for Python project.
  hint: Add a Procfile with: web: uvicorn main:app --port 8080

Claude: "The build failed. Your project is missing a start command.
  Want me to add a Procfile for you?"

  [The Neon database that was already created is rolled back (deleted) automatically]
```

---

### 17.5 Cloud Run Timeout Setting

The `deploy_project` tool call may run for up to 10–15 minutes (including waiting for the user to fill in secrets). You need to make sure Cloud Run does not cut the connection early:

```yaml
# launchkit-api Cloud Run service setting
timeout: 3600s   # maximum (1 hour), ensures a long-running MCP stream is not interrupted
```

The response of MCP Streamable HTTP is one long-lived SSE stream, and the progress notifications are JSON lines sent continuously over that stream. As long as the timeout is long enough, the connection will not drop.

**Note**: the build handler's timeout is 30 minutes (the upper limit of the build job itself),
which is an independent Job timeout, unrelated to launchkit-api's 3600s. The two values do not affect each other.

---

## 18. POC Scenario Design

The first-phase POC validates four core scenarios, covering LaunchKit's main deployment paths.

### Scenario A: Next.js Full Stack (deploy_project)

```
my-nextjs-app/
├── package.json          # next, prisma
├── prisma/schema.prisma  # provider = "postgresql"
└── src/app/              # Next.js App Router (SSR, not a static export)
```

**Validation focus**:
- Railpack detects Next.js + prisma → correctly produces the BuildKit LLB plan
- Cloud Build successfully builds + pushes the image (Railpack as the BuildKit frontend)
- `env: { DATABASE_URL: { ref: "db_xxx" } }` is injected into Cloud Run correctly
- plan_deployment + deploy_project whole-package deploy, end to end

**Claude call sequence**:
```
plan_deployment({ hints: {
  sources: [{ id: "src_1", type: "local", local_path: "/path/to/app" }],
  services: [{ name: "web", source_id: "src_1", path: "/", framework_hint: "nextjs", type: "frontend" }],
  resources: [{ type: "postgres" }],
  env_hints: [{ name: "DATABASE_URL", classification: "auto_inject" }]
} })
  → { plan: { services: [{ name: "web", framework: "nextjs", detected_by: "railpack",
              target: "cloud_run", url: "https://my-nextjs-app.launchkit.app" }],
              resources: [{ type: "postgres", provider: "neon" }],
              secrets: { auto_inject: [{ name: "DATABASE_URL", classified_by: "rule" }] },
              sources: [{ id: "src_1", type: "upload", upload_url: "..." }] } }

[Claude shows the plan, the user confirms]
[Claude runs the Bash upload of the source code]

deploy_project({ plan_id })
  → the orchestrator automatically provisions postgres + builds + deploys
  → { services: [{ name: "web", status: "live", url: "https://my-nextjs-app.launchkit.app" }] }
```

---

### Scenario B: Python API + React Frontend (Separate Frontend/Backend)

```
python-react-app/
├── backend/              # FastAPI, requirements.txt
│   └── main.py           # PORT=8080, CORS for frontend URL
└── frontend/             # React + Vite
    └── src/              # VITE_API_URL=backend URL
```

**Validation focus**:
- Two services build in parallel (URLs are decided in advance, no need to wait for the backend to deploy first)
- `VITE_API_URL` is baked into the React bundle as a build arg
- Backend → Cloud Run, frontend → Cloudflare Pages
- The orchestrator parallelizes correctly and deploys after the gate waits for everything

**Claude call sequence**:
```
plan_deployment({ hints: {
  services: [
    { name: "api", path: "backend/", framework_hint: "fastapi", type: "backend" },
    { name: "web", path: "frontend/", framework_hint: "vite-react", type: "frontend" }
  ], ...
} })
  → { plan: { services: [
        { name: "api", target: "cloud_run", url: "https://python-react-api.launchkit.app" },
        { name: "web", target: "cloudflare_pages", url: "https://python-react.launchkit.app",
          build_args: { VITE_API_URL: "https://python-react-api.launchkit.app" } }
     ] }, ... }

[Claude uploads the source code]

deploy_project({ plan_id })
  → the orchestrator builds the two services in parallel + provisions + deploys
  → { services: [
       { name: "api", status: "live", url: "https://python-react-api.launchkit.app" },
       { name: "web", status: "live", url: "https://python-react.launchkit.app" }
     ] }
```

---

### Scenario C: Telegram Bot (Webhook Mode, deploy_project)

```
telegram-bot/
├── main.py               # python-telegram-bot, webhook mode
└── requirements.txt
```

**What is special about a Telegram Bot**:
- Webhook mode: Telegram POSTs to the bot URL proactively → a perfect fit for Cloud Run scale-to-zero
- No always_on needed (only long polling needs it)
- After deployment Claude automatically calls the Telegram API to set the webhook

**Claude runs this automatically after deployment** (Bash tool):
```bash
curl -s -XPOST https://api.telegram.org/bot${BOT_TOKEN}/setWebhook \
  -d "url=https://telegram-bot.launchkit.app/webhook"
# → { "ok": true, "description": "Webhook was set" }
```

**Validation focus**:
- Railpack detects a Python project → correctly produces a Dockerfile
- Cloud Run scale-to-zero, zero cost when there is no traffic
- Claude completes the webhook setup automatically (using the Bash tool)
- No always_on addon needed

---

### Scenario D: Pure Frontend Landing Page (Simplest)

```
landing-page/
├── package.json          # next.js (output: 'export')
└── src/app/              # static export, no API
```

**Validation focus**:
- Pure frontend: no database, no backend, no secrets
- The orchestrator only runs build + deploy to Cloudflare Pages
- The whole flow should finish within 1 minute

**Claude call sequence**:
```
plan_deployment({ hints: {
  services: [{ name: "web", path: "/", framework_hint: "nextjs-static", type: "frontend" }],
  resources: [], env_hints: []
} })
  → { plan: { services: [{ name: "web", target: "cloudflare_pages",
       url: "https://my-landing.launchkit.app" }] }, ... }

[Claude uploads the source code]

deploy_project({ plan_id })
  → { services: [{ name: "web", status: "live", url: "https://my-landing.launchkit.app" }] }
```

---

### POC Validation Matrix

| Scenario | plan_deployment | deploy_project | orchestrator parallelism | build arg | Secret entry | Telegram webhook |
|------|----------------|---------------|-------------------|-----------|------------|-----------------|
| A. Next.js fullstack | ✅ | ✅ | provision + build | — | — | — |
| B. Python+React separate | ✅ | ✅ | provision + 2x build + secret | ✅ VITE_API_URL | ✅ | — |
| C. Telegram Bot | ✅ | ✅ | build only | — | ✅ BOT_TOKEN | ✅ |
| D. Landing Page | ✅ | ✅ | build only | — | — | — |
