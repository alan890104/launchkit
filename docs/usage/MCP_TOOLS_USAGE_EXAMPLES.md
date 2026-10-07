# LaunchKit MCP Tools — Usage Examples

> Developer experience design modeled on Railway, Vercel, and Netlify

---

## Core Design Principles

### 1. The Essence of Deployment

**LaunchKit's deployment model**:
```
Build Once → Deploy Anywhere

Image: gcr.io/project/api:git-abc123
              ↓
      Staging: deploy git-abc123
              ↓ (push to main branch)
      Production: deploy git-abc123 (same image)
```

**It is not "promote environment"**, but **deploying the same code to different environments**.

### 2. Environment Management

| Environment type | Purpose | Lifecycle | Reference platform |
|---------|------|---------|---------|
| `production` | Live environment | Permanent | Vercel Production |
| `staging` | Pre-release validation | Permanent | Vercel Preview |
| `dev` | Development and testing | Permanent | Railway Dev |
| `preview-pr-{id}` | PR preview | Cleaned up automatically after the PR is merged | Railway Preview |

### 3. Automation

- **Scale-to-zero**: natively supported by Cloud Run, no manual sleep/wake needed
- **CI/CD**: GitHub push → automatic build → automatic deploy
- **PR Preview**: PR opened → a full environment (services + DB) is created automatically

---

## Scenario 1: Launching a New Product (from zero to production)

### Step 1: Deploy the production environment

**Natural-language instruction**:
```
Claude, deploy this SaaS to production for me
```

**MCP calls executed by Claude**:
```json
{
  "tool": "plan_deployment",
  "arguments": {
    "project_name": "my-saas",
    "services": [
      {"name": "api", "type": "backend", "source_dir": "backend/", "framework": "fastapi"},
      {"name": "web", "type": "frontend", "source_dir": "frontend/", "framework": "nextjs"}
    ],
    "resources": [
      {"name": "main-db", "type": "postgres"}
    ],
    "env_hints": [
      {"key": "DATABASE_URL", "classification": "auto_inject", "inject_from": "main-db"},
      {"key": "STRIPE_SECRET_KEY", "classification": "user_required", "description": "Stripe API key"}
    ]
  }
}
```

**Returns**:
```json
{
  "plan_id": "plan_abc123",
  "upload_url": "https://storage.googleapis.com/...",
  "upload_command": "git archive HEAD | gzip | curl -XPUT ..."
}
```

> Note: no cost estimate is provided, because the actual cost depends on usage (CPU-seconds, memory-seconds, request count).

### Step 2: Upload the source code and deploy

**Automatic upload**:
```bash
git archive HEAD | gzip | curl -XPUT -H 'Content-Type: application/gzip' --data-binary @- '<upload_url>'
```

**Trigger the deployment**:
```json
{
  "tool": "deploy_project",
  "arguments": {"plan_id": "plan_abc123"}
}
```

**Deployment status query** (asynchronous job):
```json
{
  "tool": "deploy_status",
  "arguments": {"deployment_id": "dep_xyz789"}
}
```

**Returns**:
```json
{
  "deployment_id": "dep_xyz789",
  "status": "deploying",
  "current_step": "Building api service",
  "steps": [
    {"name": "Upload", "status": "completed"},
    {"name": "Build", "status": "running"},
    {"name": "Deploy", "status": "pending"}
  ]
}
```

> Note: no percentage progress is provided, because it cannot be computed precisely. Instead the **current step** and **step status** are shown.

### Step 3: Enter Secret environment variables

**Secure form flow**:
1. `plan_deployment` returns `secret_request_id`
2. The user visits `https://launchkit.app/s/{secret_request_id}`
3. Enters sensitive information such as `STRIPE_SECRET_KEY`
4. After the form is submitted, it is encrypted with Tink and stored in Postgres

> Secrets are not exposed to the AI agent; they are handled through a separate secure form.

### Step 4: Create alert rules

```json
{
  "tool": "set_alert",
  "arguments": {
    "project": "my-saas",
    "name": "High Error Rate",
    "metric": "error_rate",
    "operator": ">",
    "threshold": 0.01,
    "window": "5m",
    "severity": "critical"
  }
}
```

> Note: alert channels (Slack, PagerDuty) are configured centrally in team settings, not repeated in each alert rule.

### Step 5: Query monitoring metrics

```json
{
  "tool": "get_metrics",
  "arguments": {
    "project": "my-saas",
    "metrics": ["requests", "latency_p95", "error_rate"],
    "period": "24h"
  }
}
```

**Returns**:
```json
{
  "services": [
    {
      "service": "api",
      "data": {
        "requests": {"current": 150, "avg": 120, "unit": "req/min"},
        "latency_p95": {"current": 250, "avg": 260, "unit": "ms"},
        "error_rate": {"current": 0.02, "avg": 0.03, "unit": "%"}
      }
    }
  ]
}
```

---

## Scenario 2: Multi-Environment Development Workflow

### Step 1: Create the Staging environment

```json
{
  "tool": "create_environment",
  "arguments": {
    "project": "my-saas",
    "name": "staging",
    "clone_from": "production",
    "isolation": "database"
  }
}
```

**Returns**:
```json
{
  "environment_id": "env_staging_123",
  "name": "staging",
  "services": [
    {"name": "api", "url": "https://api--staging.launchkit.app"},
    {"name": "web", "url": "https://web--staging.launchkit.app"}
  ],
  "resources": [
    {"type": "postgres", "note": "New database will be provisioned on first deploy"}
  ],
  "env_vars_status": "pending",
  "message": "Environment 'staging' created. Services cloned from production. You need to configure environment variables and deploy to complete setup."
}
```

> **Important notes**:
> - **Service config**: clones the production service definitions (framework, build command)
> - **Environment variables**: must be reconfigured (the `user_required` category must be re-entered)
> - **Database**: a new independent database is created (schema is migrated automatically on the first deploy)
> - **Data**: production data is not cloned (for security)

### Step 2: A developer pushes code

**GitHub Actions automatic deploy**:
```yaml
# .github/workflows/deploy-staging.yml
name: Deploy to Staging
on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Upload to LaunchKit
        run: |
          git archive HEAD | gzip | curl -XPUT -H 'Content-Type: application/gzip' \
            --data-binary @- '${{ secrets.LAUNCHKIT_UPLOAD_URL }}'
```

**Flow**:
```
git push origin main
    ↓
GitHub Actions triggers
    ↓
Upload the source code to LaunchKit
    ↓
Automatic build + deploy to staging
```

### Step 3: Deploy to Production

**It is not "promote from staging"**, but **deploying the same code to production**:

**Option A: Branch-based (recommended)**
```yaml
# .github/workflows/deploy-production.yml
name: Deploy to Production
on:
  push:
    branches: [production]  # protected branch, requires PR + review

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Deploy to Production
        run: |
          git archive HEAD | gzip | curl -XPUT ...
```

**Option B: Manual trigger**
```json
{
  "tool": "deploy_project",
  "arguments": {
    "project": "my-saas",
    "environment": "production",
    "git_ref": "abc123"  # same commit hash as staging
  }
}
```

> **Key point**: make sure staging and production deploy the **same git commit**, guaranteed through the image tag or git_ref.

---

## Scenario 3: PR Preview Environments

### Step 1: Create a preview environment automatically when a PR is opened

**GitHub Actions**:
```yaml
# .github/workflows/preview.yml
name: Preview Deployment
on:
  pull_request:
    types: [opened, synchronize]

jobs:
  preview:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Create Preview Environment
        run: |
          curl -X POST https://api.launchkit.app/v1/environments \
            -H "Authorization: Bearer $TOKEN" \
            -d '{"project":"my-saas","name":"preview-pr-${{ github.event.number }}","clone_from":"staging"}'
```

**Returns**:
```json
{
  "environment_id": "env_preview_42",
  "name": "preview-pr-42",
  "services": [
    {"name": "api", "url": "https://api--preview-pr-42.launchkit.app"},
    {"name": "web", "url": "https://web--preview-pr-42.launchkit.app"}
  ],
  "lifecycle": {
    "auto_destroy": true,
    "destroy_after_pr_merge": true
  }
}
```

### Step 2: Clean up automatically after the PR is merged

**GitHub Actions**:
```yaml
on:
  pull_request:
    types: [closed]

jobs:
  cleanup:
    runs-on: ubuntu-latest
    steps:
      - name: Destroy Preview Environment
        run: |
          curl -X DELETE https://api.launchkit.app/v1/environments/preview-pr-${{ github.event.number }} \
            -H "Authorization: Bearer $TOKEN"
```

> Modeled on Railway: the PR environment is created when the PR is opened and deleted automatically when the PR is merged.

---

## Scenario 4: Incident Response

### Step 1: Receive the alert notification

```
[Slack #alerts] 🔴 Critical Alert Fired
Service: api
Metric: error_rate
Current: 5.2%
Threshold: 1%
Time: 2026-04-02 14:30:00 UTC
```

### Step 2: Look up incident details

```json
{
  "tool": "get_incidents",
  "arguments": {
    "project": "my-saas",
    "status": "active"
  }
}
```

### Step 3: Query the logs to diagnose the problem

```json
{
  "tool": "logs",
  "arguments": {
    "project": "my-saas",
    "service": "api",
    "environment": "production",
    "lines": 50
  }
}
```

### Step 4: Restart the service (quick fix)

```json
{
  "tool": "restart",
  "arguments": {
    "project": "my-saas",
    "service": "api",
    "environment": "production"
  }
}
```

### Step 5: Verify recovery

```json
{
  "tool": "get_metrics",
  "arguments": {
    "project": "my-saas",
    "service": "api",
    "metrics": ["error_rate"],
    "period": "1h"
  }
}
```

---

## Scenario 5: SLA/SLO Review Meeting

### Step 1: Query last month's SLA attainment

```json
{
  "tool": "get_uptime",
  "arguments": {
    "project": "my-saas",
    "period": "30d"
  }
}
```

**Returns**:
```json
{
  "uptime": {
    "uptime_percentage": 99.92,
    "total_downtime": "34m 34s",
    "incident_count": 3
  },
  "sla_target": 99.95,
  "status": "at_risk"
}
```

### Step 2: Query incident history

```json
{
  "tool": "get_incidents",
  "arguments": {
    "project": "my-saas",
    "status": "resolved"
  }
}
```

---

## Scenario 6: Usage and Budget Management

### Step 1: Query current usage

```json
{
  "tool": "usage",
  "arguments": {}
}
```

**Returns**:
```json
{
  "usage": {
    "total_projects": 5,
    "total_services": 12,
    "total_deployments": 45,
    "active_databases": 3
  },
  "billing": {
    "current_month_cost": 127.50,
    "currency": "USD",
    "budget_remaining": 372.50
  }
}
```

> Note: `current_month_cost` is a **cumulative value**, not an estimate.

---

## Scenario 7: Maintenance Window (Alert Suppression)

### Step 1: Create a maintenance window

```json
{
  "tool": "create_maintenance_window",
  "arguments": {
    "project": "my-saas",
    "starts_at": "2026-04-10T02:00:00Z",
    "ends_at": "2026-04-10T04:00:00Z",
    "suppress_severities": ["warning", "error"],
    "title": "Database migration"
  }
}
```

### Step 2: Alerts are suppressed automatically during maintenance

During the maintenance window:
- `warning` and `error` alerts are suppressed
- `critical` alerts are still sent
- Alerting resumes automatically after maintenance ends

---

## Summary

The 7 scenarios above cover:

| # | Scenario | Reference platform | Status |
|---|------|---------|---------|
| 1 | **Launching a new product**: the full flow from zero to production | Railway Deploy | ✅ Sprint 1 |
| 2 | **Multi-environment development**: independent staging / production deploys | Vercel Environments | ✅ Sprint 2 |
| 3 | **PR previews**: automatically create/clean up preview environments | Railway Preview | ✅ Sprint 2 |
| 4 | **Incident response**: alert → diagnose → fix → verify | Industry standard | ✅ Sprint 2 |
| 5 | **SLA review**: usage review and improvement actions | Industry standard | ✅ Sprint 2 |
| 6 | **Budget management**: usage queries and budget tracking | Industry standard | ✅ Sprint 2 |
| 7 | **Maintenance window**: alert suppression mechanism | Industry standard | 🟡 Sprint 3 |

### Features Not Implemented

| Feature | Reason | Alternative |
|------|------|---------|
| `promote` (staging → production) | Violates immutable infrastructure | Deploy the same git_ref to a different environment |
| `sleep_environment` / `wake_environment` | Cloud Run has native scale-to-zero | Rely on the cloud provider's automatic scaling |
| Cost estimate | Cannot be computed precisely before deployment | Provide usage data; users estimate themselves |
| Percentage progress | Cannot be computed precisely | Show the current step and step status |

### Key Difference: LaunchKit vs Railway

| Feature | Railway | LaunchKit |
|------|---------|-----------|
| **Deployment model** | GitHub connection, push deploys automatically | MCP + REST API, supports multiple upload methods |
| **Environment management** | Copy environments manually | `create_environment` with `clone_from` |
| **Secret management** | Variables UI | A separate secure form (the AI never sees plaintext) |
| **Billing** | Usage-based (CPU-seconds, memory-seconds) | Usage-based + add-on services (GPU, management fee) |
| **Target users** | Developers | Vibe coders + AI agents |

---

## Appendix: Complete MCP Tools List

### Core Deployment
- `plan_deployment` — generate a deployment plan
- `deploy_project` — execute the deployment
- `deploy_status` — query deployment status
- `deploy_logs` — query deployment logs

### Environment Management
- `create_environment` — create an environment (staging, dev, preview)
- `list_environments` — list all environments
- `promote_environment` — deploy the same image to a different environment
- `destroy_environment` — delete an environment

### Monitoring and Alerting
- `get_metrics` — query monitoring metrics
- `set_alert` — create an alert rule
- `list_alerts` — list alert rules
- `delete_alert` — delete an alert rule
- `get_incidents` — query incidents
- `get_uptime` — query uptime

### Management Queries
- `list_projects` — list all projects
- `usage` — query usage
- `logs` — query service logs
- `restart` — restart the service
