-- +goose Up
-- ═══════════════════════════════════════════════
-- EMAIL DOMAINS (Resend integration)
-- ═══════════════════════════════════════════════

CREATE TABLE email_domains (
  id             TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  domain         TEXT NOT NULL,
  provider       TEXT NOT NULL DEFAULT 'resend',   -- email provider name
  provider_id    TEXT,                              -- Resend domain ID
  status         TEXT NOT NULL DEFAULT 'pending_dns', -- pending_dns / verified / failed
  dns_records    JSONB NOT NULL DEFAULT '[]',       -- DNS records user must add
  api_key_enc    TEXT,                              -- encrypted Resend API key (scoped to this domain)
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(project_id, domain)
);
