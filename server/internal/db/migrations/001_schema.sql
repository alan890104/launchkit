-- +goose Up
-- ═══════════════════════════════════════════════
-- LaunchKit Database Schema
-- ═══════════════════════════════════════════════
-- Single source of truth for database schema.
-- Run this once on fresh database setup.

-- ═══════════════════════════════════════════════
-- USERS & TEAMS
-- ═══════════════════════════════════════════════

CREATE TABLE users (
  id          TEXT PRIMARY KEY,           -- Auth0 user ID
  email       TEXT NOT NULL UNIQUE,
  name        TEXT,
  avatar_url  TEXT,
  card_fingerprint TEXT,                  -- credit card dedup (abuse prevention)
  first_top_up_claimed BOOLEAN DEFAULT false,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE teams (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  name        TEXT NOT NULL,
  owner_id    TEXT NOT NULL REFERENCES users(id),
  balance          NUMERIC NOT NULL DEFAULT 0,
  lifetime_top_up  NUMERIC NOT NULL DEFAULT 0,
  low_balance_alert_at NUMERIC NOT NULL DEFAULT 2,
  auto_top_up      BOOLEAN NOT NULL DEFAULT false,
  auto_top_up_amount    NUMERIC,
  auto_top_up_threshold NUMERIC,
  stripe_customer_id TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE top_ups (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  amount      NUMERIC NOT NULL,
  bonus       NUMERIC NOT NULL DEFAULT 0,
  stripe_payment_id TEXT,
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  CONSTRAINT unique_stripe_payment UNIQUE (team_id, stripe_payment_id)
);

CREATE TABLE team_members (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
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
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id     TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  region      TEXT NOT NULL DEFAULT 'us-east4',
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  updated_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(team_id, name)
);

CREATE TABLE environments (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,                    -- "production" | "staging" | "dev" | "preview-*"
  status      TEXT NOT NULL DEFAULT 'active',   -- active / destroyed
  clone_from  TEXT REFERENCES environments(id), -- source environment for cloning
  created_at  TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(project_id, name)
);

CREATE TABLE services (
  id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  type           TEXT NOT NULL,    -- backend / frontend / worker / inference
  target         TEXT NOT NULL,    -- cloud_run / cloudflare_pages
  framework      TEXT NOT NULL DEFAULT '',
  url            TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'pending',  -- pending / deploying / live / failed / stopped
  config         JSONB NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(environment_id, name)
);

CREATE TABLE deployments (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  service_id  TEXT REFERENCES services(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',
  -- Status machine:
  --   pending → provisioning → building → waiting_secrets → deploying → live
  --   live → stopped (manual)
  --   stopped → deploying (resume)
  -- Terminal: live / failed / stopped
  image_uri   TEXT NOT NULL DEFAULT '',
  build_log   TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  plan_id     TEXT,
  trigger     TEXT NOT NULL DEFAULT 'deploy',  -- deploy / update / rollback / promote_environment
  started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE deployment_steps (
  id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  step          TEXT NOT NULL,              -- {action}_{target}
  status        TEXT NOT NULL DEFAULT 'pending',  -- pending / running / completed / failed / skipped
  provider_id   TEXT,
  started_at    TIMESTAMPTZ,
  finished_at   TIMESTAMPTZ,
  error         TEXT,
  UNIQUE(deployment_id, step)
);

CREATE INDEX idx_deployment_steps_deployment ON deployment_steps(deployment_id);

CREATE TABLE resources (
  id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
  type           TEXT NOT NULL,    -- postgres / redis / storage
  provider       TEXT NOT NULL,    -- neon / upstash / cloudflare_r2
  provider_id    TEXT NOT NULL DEFAULT '',
  connection_url TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT 'pending',
  config         JSONB NOT NULL DEFAULT '{}',
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(environment_id, type)
);

-- ═══════════════════════════════════════════════
-- PLANS (deployment plans from Plan Engine)
-- ═══════════════════════════════════════════════

CREATE TABLE plans (
  id              TEXT PRIMARY KEY,           -- 'plan_' + uuid
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  environment_id  TEXT REFERENCES environments(id),
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending / confirmed / executing / completed / failed
  plan_data       JSONB NOT NULL,
  upload_url      TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- SECRETS & ENV VARS
-- ═══════════════════════════════════════════════

CREATE TABLE secret_requests (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  deployment_id   TEXT REFERENCES deployments(id),
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending / completed / expired
  keys            JSONB NOT NULL,               -- [{name, description}, ...]
  expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '1 hour',
  csrf_token      TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at    TIMESTAMPTZ
);

CREATE TABLE secrets (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  value_enc   BYTEA,                          -- Tink encrypted value; NULL until user fills form
  nonce       BYTEA,                          -- encryption nonce; NULL until encrypted
  status      TEXT NOT NULL DEFAULT 'pending', -- pending / set
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(project_id, name)
);

CREATE TABLE env_bindings (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  key             TEXT NOT NULL,
  value           TEXT,                       -- plaintext for auto-generated/injected
  secret_id       TEXT REFERENCES secrets(id),  -- or reference to secret
  source          TEXT,                       -- user_input / auto_generate / auto_inject
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(service_id, key)
);

-- ═══════════════════════════════════════════════
-- DOMAINS
-- ═══════════════════════════════════════════════

CREATE TABLE domains (
  id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  service_id    TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  domain        TEXT NOT NULL,
  status        TEXT NOT NULL DEFAULT 'pending_dns',  -- pending_dns / configured / error
  ssl_status    TEXT NOT NULL DEFAULT 'pending',      -- pending / provisioning / active / error
  dns_records   JSONB NOT NULL DEFAULT '[]',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(service_id, domain)
);

-- ═══════════════════════════════════════════════
-- MONITORING & ALERTING
-- ═══════════════════════════════════════════════

CREATE TABLE alert_rules (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service         TEXT,                              -- null = all services
  name            TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  metric          TEXT NOT NULL,
  operator        TEXT NOT NULL,                     -- > / < / >= / <= / ==
  threshold       NUMERIC NOT NULL,
  "window"        TEXT NOT NULL,                     -- 1m / 5m / 15m / 1h
  severity        TEXT NOT NULL DEFAULT 'warning',   -- warning / critical
  channels        JSONB NOT NULL DEFAULT '[]',       -- [{type, target}, ...]
  status          TEXT NOT NULL DEFAULT 'active',    -- active / disabled
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE alert_incidents (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service         TEXT NOT NULL,
  rule_id         TEXT REFERENCES alert_rules(id),
  title           TEXT NOT NULL,
  description     TEXT NOT NULL,
  severity        TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active',    -- active / acknowledged / resolved
  started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  resolved_at     TIMESTAMPTZ,
  root_cause      TEXT,
  acknowledged_by TEXT REFERENCES users(id),
  resolved_by     TEXT REFERENCES users(id)
);

CREATE TABLE maintenance_windows (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  starts_at       TIMESTAMPTZ NOT NULL,
  ends_at         TIMESTAMPTZ NOT NULL,
  suppress_severities TEXT[] NOT NULL DEFAULT '{}',  -- e.g. {warning,error}
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE uptime_checks (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service         TEXT NOT NULL,
  url             TEXT NOT NULL,
  interval_sec    INTEGER NOT NULL DEFAULT 60,       -- check interval
  timeout_sec     INTEGER NOT NULL DEFAULT 10,
  status          TEXT NOT NULL DEFAULT 'active',    -- active / paused
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE uptime_results (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  check_id        TEXT NOT NULL REFERENCES uptime_checks(id) ON DELETE CASCADE,
  status          TEXT NOT NULL,                     -- up / down
  response_time_ms INTEGER,
  error           TEXT,
  checked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE service_slos (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  service         TEXT NOT NULL,
  target          NUMERIC NOT NULL,                  -- e.g. 99.95 for 99.95%
  window_days     INTEGER NOT NULL DEFAULT 30,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sla_credits (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  incident_id     TEXT REFERENCES alert_incidents(id),
  amount          NUMERIC NOT NULL,
  reason          TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending',   -- pending / issued
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- BILLING & USAGE
-- ═══════════════════════════════════════════════

CREATE TABLE usage_records (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id),
  resource_type   TEXT NOT NULL,                 -- compute / database / storage / gpu
  resource_id     TEXT,
  quantity        NUMERIC NOT NULL,
  unit            TEXT NOT NULL,                 -- cpu-seconds / memory-gb-seconds / gb-storage / gpu-hours
  cost            NUMERIC NOT NULL,
  recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE invoices (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  amount          NUMERIC NOT NULL,
  period_start    TIMESTAMPTZ NOT NULL,
  period_end      TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending',  -- pending / paid / overdue
  stripe_invoice_id TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE budgets (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id),
  amount          NUMERIC NOT NULL,
  period          TEXT NOT NULL DEFAULT 'monthly',  -- monthly / quarterly / yearly
  alert_at        NUMERIC NOT NULL DEFAULT 0.8,     -- alert when 80% used
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- CRON JOBS
-- ═══════════════════════════════════════════════

CREATE TABLE cron_jobs (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  schedule        TEXT NOT NULL,                 -- cron expression
  command         TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active',  -- active / paused
  last_run_at     TIMESTAMPTZ,
  next_run_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- AUDIT LOGS
-- ═══════════════════════════════════════════════

CREATE TABLE audit_logs (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id         TEXT REFERENCES users(id),
  action          TEXT NOT NULL,                 -- e.g. "deploy", "provision", "delete"
  resource_type   TEXT,
  resource_id     TEXT,
  details         JSONB,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ═══════════════════════════════════════════════
-- INDEXES
-- ═══════════════════════════════════════════════

CREATE INDEX idx_deployments_service ON deployments(service_id, created_at DESC);
CREATE INDEX idx_deployments_status  ON deployments(status, updated_at);
CREATE INDEX idx_services_env        ON services(environment_id);
CREATE INDEX idx_resources_env       ON resources(environment_id);
CREATE INDEX idx_plans_project       ON plans(project_id, created_at DESC);
CREATE INDEX idx_alert_rules_project ON alert_rules(project_id);
CREATE INDEX idx_alert_incidents_project ON alert_incidents(project_id, status);
CREATE INDEX idx_usage_team ON usage_records(team_id, recorded_at DESC);
