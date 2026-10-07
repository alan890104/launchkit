#!/bin/sh
# Apply Artifact Registry cleanup policies to the user-images repository.
# Run once (or whenever the policy file changes).
#
# Prerequisites:
#   gcloud auth login
#   gcloud config set project <PROJECT_ID>
#
# What the policies do:
#   1. delete-untagged-layers:  removes orphaned layer blobs older than 7 days
#   2. delete-stale-buildcache: removes :buildcache tags inactive for 30 days
#      (catches deleted/abandoned services whose cache is still sitting around)

set -e

REGION="${REGION:-us-east4}"
REPOSITORY="${REPOSITORY:-user-images}"
POLICY_FILE="$(dirname "$0")/cleanup-policy.json"

echo "[launchkit] Applying cleanup policies to ${REGION}/${REPOSITORY}..."

gcloud artifacts repositories set-cleanup-policies "$REPOSITORY" \
  --project="$(gcloud config get-value project)" \
  --location="$REGION" \
  --policy="$POLICY_FILE"

echo "[launchkit] Done. Policies applied:"
gcloud artifacts repositories describe "$REPOSITORY" \
  --project="$(gcloud config get-value project)" \
  --location="$REGION" \
  --format="value(cleanupPolicies)"
