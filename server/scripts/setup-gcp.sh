#!/usr/bin/env bash
set -euo pipefail

# LaunchKit GCP Infrastructure Setup
# Run once per GCP project to create required resources.
#
# Prerequisites:
#   - gcloud CLI authenticated: gcloud auth login
#   - Project selected: gcloud config set project YOUR_PROJECT
#
# Usage:
#   ./scripts/setup-gcp.sh

REGION="${GCP_REGION:-us-east4}"
PROJECT=$(gcloud config get-value project 2>/dev/null)

if [[ -z "$PROJECT" ]]; then
  echo "ERROR: No GCP project set. Run: gcloud config set project YOUR_PROJECT"
  exit 1
fi

echo "=== LaunchKit GCP Setup ==="
echo "Project: $PROJECT"
echo "Region:  $REGION"
echo ""

# 1. Enable required APIs
echo "→ Enabling APIs..."
gcloud services enable \
  cloudbuild.googleapis.com \
  run.googleapis.com \
  artifactregistry.googleapis.com \
  storage.googleapis.com \
  logging.googleapis.com \
  iam.googleapis.com \
  --quiet

echo "  APIs enabled ✓"

# 2. Create GCS bucket for build artifacts (24h lifecycle)
BUCKET="${PROJECT}-launchkit-builds"
echo "→ Creating GCS bucket: $BUCKET ..."

if gsutil ls -b "gs://${BUCKET}" &>/dev/null; then
  echo "  Bucket already exists ✓"
else
  gsutil mb -l "$REGION" "gs://${BUCKET}"
  echo "  Bucket created ✓"
fi

# Set lifecycle: delete objects after 24 hours
cat > /tmp/lk-lifecycle.json <<'EOF'
{
  "rule": [
    {
      "action": {"type": "Delete"},
      "condition": {"age": 1}
    }
  ]
}
EOF
gsutil lifecycle set /tmp/lk-lifecycle.json "gs://${BUCKET}"
rm /tmp/lk-lifecycle.json
echo "  Lifecycle policy set (24h delete) ✓"

# 3. Create Artifact Registry repo for user images
REPO="user-images"
echo "→ Creating Artifact Registry repo: $REPO ..."

if gcloud artifacts repositories describe "$REPO" --location="$REGION" &>/dev/null; then
  echo "  Repository already exists ✓"
else
  gcloud artifacts repositories create "$REPO" \
    --repository-format=docker \
    --location="$REGION" \
    --description="LaunchKit user application images" \
    --quiet
  echo "  Repository created ✓"
fi

# 4. Create service account for the LaunchKit API
SA_NAME="launchkit-api"
SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
echo "→ Creating service account: $SA_NAME ..."

if gcloud iam service-accounts describe "$SA_EMAIL" &>/dev/null; then
  echo "  Service account already exists ✓"
else
  gcloud iam service-accounts create "$SA_NAME" \
    --display-name="LaunchKit API" \
    --description="Service account for LaunchKit API server" \
    --quiet
  echo "  Service account created ✓"
fi

# 5. Grant required roles
echo "→ Granting IAM roles..."
ROLES=(
  "roles/storage.admin"
  "roles/cloudbuild.builds.editor"
  "roles/run.admin"
  "roles/artifactregistry.writer"
  "roles/logging.viewer"
  "roles/iam.serviceAccountUser"
)

for ROLE in "${ROLES[@]}"; do
  gcloud projects add-iam-policy-binding "$PROJECT" \
    --member="serviceAccount:${SA_EMAIL}" \
    --role="$ROLE" \
    --quiet \
    --condition=None \
    >/dev/null 2>&1
  echo "  $ROLE ✓"
done

# 6. Create key for local development
KEY_FILE="$HOME/.config/launchkit/sa-key.json"
echo "→ Creating service account key for local dev..."
mkdir -p "$(dirname "$KEY_FILE")"

if [[ -f "$KEY_FILE" ]]; then
  echo "  Key file already exists at $KEY_FILE ✓"
else
  gcloud iam service-accounts keys create "$KEY_FILE" \
    --iam-account="$SA_EMAIL" \
    --quiet
  echo "  Key created at $KEY_FILE ✓"
fi

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Add these to your .env file:"
echo ""
echo "  GCP_PROJECT_ID=$PROJECT"
echo "  GCP_REGION=$REGION"
echo "  GCS_BUILD_BUCKET=$BUCKET"
echo "  ARTIFACT_REGISTRY_REPO=${REGION}-docker.pkg.dev/${PROJECT}/${REPO}"
echo "  GOOGLE_APPLICATION_CREDENTIALS=$KEY_FILE"
echo ""
echo "You also need:"
echo "  NEON_API_KEY=          (from https://console.neon.tech → API Keys)"
echo "  CLOUDFLARE_ACCOUNT_ID= (from https://dash.cloudflare.com → account ID)"
echo "  CLOUDFLARE_API_TOKEN=  (from https://dash.cloudflare.com → API Tokens, Pages permissions)"
