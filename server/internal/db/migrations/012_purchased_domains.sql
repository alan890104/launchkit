-- +goose Up
-- Domain registration tracking. Separate from the `domains` table which tracks
-- domain-to-service mappings (custom domains). This table tracks domains
-- *purchased* through LaunchKit via registrar APIs (Name.com, etc.).

CREATE TABLE purchased_domains (
  id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  team_id         TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
  domain          TEXT NOT NULL UNIQUE,                -- globally unique
  registrar       TEXT NOT NULL DEFAULT 'namecom',     -- future: dynadot, etc.
  provider_id     TEXT NOT NULL,                       -- registrar-specific ID
  status          TEXT NOT NULL DEFAULT 'active'
                  CHECK (status IN ('pending', 'active', 'expired', 'transfer_locked', 'renewal_failed')),
  purchase_price  NUMERIC NOT NULL,                    -- USD total at time of purchase
  renewal_price   NUMERIC NOT NULL,                    -- USD per year at time of purchase
  years           INTEGER NOT NULL DEFAULT 1,
  auto_renew      BOOLEAN NOT NULL DEFAULT true,
  purchased_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at      TIMESTAMPTZ NOT NULL,
  renewed_at      TIMESTAMPTZ,                         -- last successful renewal
  dns_configured  BOOLEAN NOT NULL DEFAULT false,      -- true after auto-DNS completes
  contacts        JSONB NOT NULL DEFAULT '{}',         -- WHOIS contacts (redacted in API responses)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_purchased_domains_team ON purchased_domains(team_id);
CREATE INDEX idx_purchased_domains_expires ON purchased_domains(expires_at)
  WHERE status = 'active' AND auto_renew = true;
CREATE INDEX idx_purchased_domains_project ON purchased_domains(project_id)
  WHERE project_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS purchased_domains;
