-- +goose Up
ALTER TABLE api_keys
  ADD COLUMN IF NOT EXISTS scopes   TEXT NOT NULL DEFAULT '*',
  ADD COLUMN IF NOT EXISTS key_type TEXT NOT NULL DEFAULT 'dashboard';

COMMENT ON COLUMN api_keys.scopes   IS 'Space-separated permission scopes, "*" = full access';
COMMENT ON COLUMN api_keys.key_type IS 'dashboard | cli';

-- +goose Down
ALTER TABLE api_keys
  DROP COLUMN IF EXISTS scopes,
  DROP COLUMN IF EXISTS key_type;
