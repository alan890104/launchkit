-- +goose Up
-- 005_cancel.sql: add river_job_id to deployments for cancellation support

ALTER TABLE deployments ADD COLUMN IF NOT EXISTS river_job_id BIGINT;

COMMENT ON COLUMN deployments.river_job_id IS
  'River job queue ID — used by cancel_deployment to cancel in-flight deploys via river.JobCancel';
