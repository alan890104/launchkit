-- +goose Up
-- Billing security hardening:
-- 1. UNIQUE(owner_id) on teams — prevents concurrent provision race from giving
--    the same user multiple $10 signup bonuses via separate team rows.
-- 2. No changes to subscriptions (UNIQUE(team_id) already prevents double billing).

ALTER TABLE teams ADD CONSTRAINT unique_owner_team UNIQUE (owner_id);

-- +goose Down

ALTER TABLE teams DROP CONSTRAINT IF EXISTS unique_owner_team;
