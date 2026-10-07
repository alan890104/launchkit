-- +goose Up
ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_project_name_unique UNIQUE (project_id, name);

-- +goose Down
ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_project_name_unique;
