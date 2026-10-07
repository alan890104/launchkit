-- +goose Up
-- Subscription plans, monthly credit cycles, and build time tracking.

-- ═══════════════════════════════════════════════
-- SUBSCRIPTIONS
-- ═══════════════════════════════════════════════

CREATE TABLE subscriptions (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  plan            TEXT NOT NULL CHECK (plan IN ('starter', 'pro')),
  monthly_credit  NUMERIC NOT NULL,              -- $5 for starter, $20 for pro (set by app)
  status          TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active', 'cancelled', 'past_due')),
  current_period_start TIMESTAMPTZ NOT NULL DEFAULT date_trunc('month', NOW()),
  current_period_end   TIMESTAMPTZ NOT NULL DEFAULT (date_trunc('month', NOW()) + INTERVAL '1 month'),
  credit_remaining     NUMERIC NOT NULL,          -- set by app on creation (= monthly_credit + bonus)
  stripe_subscription_id TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(team_id)
);

-- ═══════════════════════════════════════════════
-- PLAN DEFINITIONS (reference, not queried at runtime)
-- ═══════════════════════════════════════════════

COMMENT ON TABLE subscriptions IS
  'Plan details: starter=$5/mo ($5 credit, 10GB egress/project, 100min build), '
  'pro=$20/mo ($20 credit, 50GB egress/project, 500min build).';

-- ═══════════════════════════════════════════════
-- BUILD TIME TRACKING
-- ═══════════════════════════════════════════════

ALTER TABLE deployments ADD COLUMN build_duration_ms INTEGER;

-- Monthly build usage per team (aggregated for billing).
-- step_name enables dedup across River job retries (same step won't be recorded twice).
CREATE TABLE build_usage (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
  deployment_id   TEXT REFERENCES deployments(id) ON DELETE SET NULL,
  step_name       TEXT NOT NULL DEFAULT '',
  duration_ms     BIGINT NOT NULL,
  cost            NUMERIC NOT NULL DEFAULT 0,        -- $0.005/sec after free tier
  billed_at       TIMESTAMPTZ,                       -- NULL = not yet billed
  period_month    DATE NOT NULL DEFAULT date_trunc('month', NOW())::DATE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(deployment_id, step_name)
);

CREATE INDEX idx_build_usage_team_month
  ON build_usage(team_id, period_month);

CREATE INDEX idx_build_usage_unbilled
  ON build_usage(team_id, period_month) WHERE billed_at IS NULL;

-- ═══════════════════════════════════════════════
-- EGRESS TRACKING (per project per month)
-- ═══════════════════════════════════════════════

CREATE TABLE egress_usage (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
  bytes           BIGINT NOT NULL DEFAULT 0,
  period_month    DATE NOT NULL DEFAULT date_trunc('month', NOW())::DATE,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(team_id, project_id, period_month)
);

-- +goose Down

DROP TABLE IF EXISTS egress_usage;
DROP TABLE IF EXISTS build_usage;
ALTER TABLE deployments DROP COLUMN IF EXISTS build_duration_ms;
DROP TABLE IF EXISTS subscriptions;
