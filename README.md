# LaunchKit

**Deploy from Claude to Cloud Run, Neon, Upstash and Cloudflare.**

LaunchKit is a remote MCP server. Tell Claude to ship a project and it reads the code, plans the infrastructure, builds the image and puts it on the platforms you would have picked yourself, with one login instead of six dashboards.

```
You:    deploy my FastAPI app in ./backend

Claude: Python 3.11, FastAPI, asyncpg
        PostgreSQL connection found, provisioning a Neon database
        Plan: Cloud Run (us-east4), Neon (us-east-1), Artifact Registry
        Image pushed in 47s
        Live at https://backend-abc123.launchkit.app
```

## How it works

1. **Plan.** `plan_deployment` reads the repo, detects the runtime and what it needs (Postgres, Redis, a static frontend) and returns a plan.
2. **Build.** `deploy_project` uploads the source and builds a container image, on the local Docker daemon or on an isolated BuildKit VM, then pushes it to Artifact Registry.
3. **Provision and ship.** The backend runs on Cloud Run, data goes to Neon and Upstash, the frontend to Cloudflare Pages. Secrets are injected as environment variables, and Claude gets back an HTTPS URL.

After that, Claude manages the project through the same server: logs, status, rollbacks, scaling, domains and alerts.

## Tools

| Area | MCP tools |
| --- | --- |
| Deploy | `plan_deployment` `deploy_project` `redeploy` `deploy_status` `deploy_logs` `cancel_deployment` `rollback` `restart` `scale` `destroy` `list_projects` |
| Environments | `create_environment` `list_environments` `promote_environment` `destroy_environment` `update_env` |
| Domains and email | `search_domain` `purchase_domain` `list_purchased_domains` `add_domain` `verify_domain` `remove_domain` `manage_dns` `setup_email` `verify_email` `get_email_config` |
| Monitoring | `get_metrics` `get_uptime` `get_incidents` `set_alert` `list_alerts` `delete_alert` `get_audit_logs` `usage` |
| Teams and GitHub | `invite_member` `list_members` `update_member_role` `remove_member` `transfer_project` `connect_github` `disconnect_github` `get_registry_credentials` |
| Scheduled jobs | `create_cron_job` `list_cron_jobs` `delete_cron_job` |

Examples for each are in [docs/usage/MCP_TOOLS_USAGE_EXAMPLES.md](docs/usage/MCP_TOOLS_USAGE_EXAMPLES.md).

## Run it locally

The server is Go and needs Docker for its local Postgres.

```bash
cd server
cp .env.example .env        # set LAUNCHKIT_DEV_KEY to any string
set -a && source .env && set +a
make dev                    # starts Postgres, then the API on :8080
```

With `BUILD_MODE=local`, images are built by the Docker daemon on the same machine, so no remote build infrastructure is needed. Real deploys need the provider keys listed in [`.env.example`](server/.env.example): a GCP project for Cloud Run and Artifact Registry, plus Neon, Upstash, Cloudflare, Resend and Name.com as you use them.

Then add the server to Claude Code:

```bash
claude mcp add --transport http launchkit http://localhost:8080/mcp \
  --header "Authorization: Bearer $LAUNCHKIT_DEV_KEY"
```

The dashboard is Next.js. It talks to the server at `NEXT_PUBLIC_API_URL` and signs users in with Firebase:

```bash
cd dashboard
pnpm install
pnpm dev
```

Server tests: `cd server && make test`.

## Build isolation

Builds run untrusted code, so each one is isolated in four layers:

1. **gVisor** (`runsc`): syscalls are intercepted by a user-space kernel.
2. **A network namespace per build**, on a CNI bridge.
3. **iron-proxy**: DNS interception and a default-deny egress allowlist that admits only package registries.
4. **iptables raw PREROUTING**: dangerous destinations are dropped before the CNI firewall can be bypassed.

A build can `pip install` and `npm install`. It cannot reach the GCP metadata server, scan the VPC or send secrets anywhere off the allowlist. The VM image is built with Packer from [`server/infra/packer`](server/infra/packer).

## Repository

```
server/              Go API and MCP server
  cmd/api/           HTTP entry point (MCP at /mcp)
  cmd/e2e-deploy/    End-to-end deploy test
  internal/          auth, billing, deploy, mcp, provider, worker
  infra/packer/      BuildKit VM image
dashboard/           Web dashboard (Next.js 16, React 19, Tailwind 4, shadcn/ui)
poc/                 Sample apps for end-to-end tests: FastAPI + Postgres,
                     Rust (Axum), FastAPI + React + Redis, Next.js + Prisma,
                     a landing page and a Telegram bot
docs/                Architecture, user flow and tool examples
```

Start with [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/USER_FLOW.md](docs/USER_FLOW.md) for the flow from sign-up to first deploy.

## Services

| Service | Used for |
| --- | --- |
| Cloud Run | Running user apps |
| Cloud Build, BuildKit VM | Building images |
| Artifact Registry | Storing images |
| Cloud Storage | Build artifacts and file storage |
| Neon | Postgres for user projects |
| Upstash | Redis for user projects |
| Cloudflare Pages | Static frontends |
| Name.com | Domain registration and DNS |
| Firebase | Dashboard sign-in |
| Resend | Transactional email |

## License

[MIT](LICENSE)
