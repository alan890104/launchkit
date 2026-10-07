-- +goose Up
-- GitHub push-to-deploy connections
CREATE TABLE github_connections (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repo_full_name  TEXT NOT NULL,
  branch          TEXT NOT NULL DEFAULT 'main',
  webhook_secret  TEXT NOT NULL,
  last_push_sha   TEXT,
  status          TEXT NOT NULL DEFAULT 'active',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(project_id, repo_full_name)
);

CREATE INDEX idx_github_connections_repo ON github_connections(repo_full_name);
