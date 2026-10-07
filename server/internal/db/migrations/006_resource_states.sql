-- +goose Up
-- 006_resource_states.sql
-- Pulumi-inspired resource state tracking.
-- Every cloud resource created by LaunchKit gets a row here.
-- Intent is written BEFORE the cloud API call, so crashed deploys leave
-- evidence of what might exist — enabling guaranteed cleanup.

CREATE TABLE resource_states (
  urn           TEXT PRIMARY KEY,       -- launchkit:{project_id}:{resource_type}:{name}
  project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  deployment_id TEXT REFERENCES deployments(id),
  resource_type TEXT NOT NULL,          -- compute / database / cache / storage / static
  provider      TEXT NOT NULL,          -- cloud_run / ecs_fargate / neon / upstash / cloudflare_pages / gcs / s3
  inputs        JSONB NOT NULL DEFAULT '{}',  -- what we asked for
  outputs       JSONB NOT NULL DEFAULT '{}',  -- what the provider returned (URLs, IDs, etc.)
  status        TEXT NOT NULL DEFAULT 'creating',
  -- Status machine:
  --   creating  → active    (provider API succeeded)
  --   creating  → failed    (provider API errored)
  --   active    → deleting  (delete intent written)
  --   deleting  → deleted   (provider API succeeded)
  --   deleting  → orphaned  (provider API failed — CleanupWorker will retry)
  last_error    TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- For listing all active resources per project (dashboard, billing)
CREATE INDEX idx_resource_states_project ON resource_states(project_id, status);

-- For rollback: find all resources for a given deployment
CREATE INDEX idx_resource_states_deployment ON resource_states(deployment_id)
  WHERE deployment_id IS NOT NULL;

-- For CleanupWorker: find all resources needing attention
CREATE INDEX idx_resource_states_needs_cleanup ON resource_states(status)
  WHERE status IN ('orphaned', 'failed', 'creating');

COMMENT ON TABLE resource_states IS
  'Pulumi-inspired resource state store. Source of truth for all cloud resources managed by LaunchKit. '
  'Written before every cloud API call (intent) and updated after (result).';

COMMENT ON COLUMN resource_states.urn IS
  'Deterministic resource identifier: launchkit:{project_id}:{resource_type}:{name}. '
  'Same URN across deployments = same resource (enables idempotent re-deploys).';
