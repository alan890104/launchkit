-- +goose Up
-- Billing metering infrastructure: checkpoints for deduplication,
-- run audit log, and indexes for efficient metering queries.

-- ═══════════════════════════════════════════════
-- METERING CHECKPOINTS
-- ═══════════════════════════════════════════════
-- Tracks the high-water mark per provider per resource.
-- Used for deduplication (don't double-count) and for
-- cumulative-to-delta conversion (Upstash monthly totals).

CREATE TABLE metering_checkpoints (
  resource_urn  TEXT NOT NULL,
  provider      TEXT NOT NULL,
  period_end    TIMESTAMPTZ NOT NULL,
  cumulative    JSONB NOT NULL DEFAULT '{}',
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (resource_urn, provider)
);

-- ═══════════════════════════════════════════════
-- METERING RUNS
-- ═══════════════════════════════════════════════
-- One row per hourly metering cycle. Enables observability
-- and retry of failed runs.

CREATE TABLE metering_runs (
  id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  period_start  TIMESTAMPTZ NOT NULL,
  period_end    TIMESTAMPTZ NOT NULL,
  status        TEXT NOT NULL DEFAULT 'running'
                CHECK (status IN ('running', 'completed', 'partial', 'failed')),
  providers     JSONB NOT NULL DEFAULT '{}',
  records_count INTEGER NOT NULL DEFAULT 0,
  total_cost    NUMERIC NOT NULL DEFAULT 0,
  started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at   TIMESTAMPTZ,
  error         TEXT,
  UNIQUE(period_start, period_end)
);

-- ═══════════════════════════════════════════════
-- EXTEND USAGE_RECORDS
-- ═══════════════════════════════════════════════
-- Add period tracking and dedup columns.

ALTER TABLE usage_records ADD COLUMN period_start    TIMESTAMPTZ;
ALTER TABLE usage_records ADD COLUMN period_end      TIMESTAMPTZ;
ALTER TABLE usage_records ADD COLUMN provider        TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN resource_urn    TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_records ADD COLUMN metering_run_id TEXT REFERENCES metering_runs(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_usage_records_dedup
  ON usage_records(resource_urn, period_start, period_end)
  WHERE resource_urn != '';

-- NOTE: idx_usage_team (team_id, recorded_at DESC) already exists in 001_schema.sql.
-- Not re-creating it here to avoid duplicate indexes.

-- ═══════════════════════════════════════════════
-- EXTEND TEAMS
-- ═══════════════════════════════════════════════
-- Track zero-balance suspension state + alert debounce.

ALTER TABLE teams ADD COLUMN suspended_at         TIMESTAMPTZ;
ALTER TABLE teams ADD COLUMN suspension_reason    TEXT;
ALTER TABLE teams ADD COLUMN low_balance_alerted_at TIMESTAMPTZ;

-- +goose Down

DROP INDEX IF EXISTS idx_usage_records_dedup;

ALTER TABLE usage_records DROP COLUMN IF EXISTS metering_run_id;
ALTER TABLE usage_records DROP COLUMN IF EXISTS resource_urn;
ALTER TABLE usage_records DROP COLUMN IF EXISTS provider;
ALTER TABLE usage_records DROP COLUMN IF EXISTS period_end;
ALTER TABLE usage_records DROP COLUMN IF EXISTS period_start;

ALTER TABLE teams DROP COLUMN IF EXISTS low_balance_alerted_at;
ALTER TABLE teams DROP COLUMN IF EXISTS suspension_reason;
ALTER TABLE teams DROP COLUMN IF EXISTS suspended_at;

DROP TABLE IF EXISTS metering_runs;
DROP TABLE IF EXISTS metering_checkpoints;
