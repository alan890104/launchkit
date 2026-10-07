-- +goose Up
-- ═══════════════════════════════════════════════
-- API Keys
-- ═══════════════════════════════════════════════
-- Allows Claude Desktop / Claude Code to authenticate via Bearer token
-- instead of Auth0 OAuth2 redirect flow (which is impossible in MCP context).
--
-- Key format: lk_<32 random hex bytes>  (e.g. lk_a3f9...)
-- Stored as SHA-256 hash — plaintext never persisted after creation.

CREATE TABLE api_keys (
  id          TEXT PRIMARY KEY DEFAULT gen_random_uuid()::TEXT,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,                          -- user-defined label, e.g. "Claude Desktop"
  key_hash    TEXT NOT NULL UNIQUE,                   -- SHA-256(plaintext_key), hex-encoded
  key_prefix  TEXT NOT NULL,                          -- first 8 chars of plaintext for display ("lk_a3f9...")
  last_used_at TIMESTAMPTZ,
  expires_at  TIMESTAMPTZ,                            -- NULL = never expires
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at  TIMESTAMPTZ                             -- NULL = active
);

CREATE INDEX idx_api_keys_user  ON api_keys(user_id);
CREATE INDEX idx_api_keys_hash  ON api_keys(key_hash) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS api_keys;
