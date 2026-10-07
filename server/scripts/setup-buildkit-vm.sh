#!/usr/bin/env bash
set -euo pipefail

# LaunchKit BuildKit VM Setup
# Creates a GCE VM from the pre-baked Packer image (launchkit-buildkit family).
#
# The image includes: containerd 1.7.x, gVisor (runsc), buildkitd, CNI plugins,
# docker-credential-gcr, iptables rules, and pre-cached base images.
#
# Prerequisites:
#   - gcloud CLI authenticated with compute.admin permissions
#   - Packer image built: cd infra/packer && make image
#   - VPC firewall rule allowing API Server → BuildKit VM TCP :1234
#
# Usage:
#   ./scripts/setup-buildkit-vm.sh

REGION="${GCP_REGION:-us-east4}"
ZONE="${BUILDKIT_ZONE:-${REGION}-c}"
PROJECT=$(gcloud config get-value project 2>/dev/null)
INSTANCE="${BUILDKIT_INSTANCE:-launchkit-buildkit}"
MACHINE_TYPE="e2-standard-4"

if [[ -z "$PROJECT" ]]; then
  echo "ERROR: No GCP project set. Run: gcloud config set project YOUR_PROJECT"
  exit 1
fi

echo "=== LaunchKit BuildKit VM Setup ==="
echo "Project:  $PROJECT"
echo "Zone:     $ZONE"
echo "Instance: $INSTANCE"
echo "Machine:  $MACHINE_TYPE"
echo "Image:    launchkit-buildkit (latest from family)"
echo ""

# ──────────────────────────────────────────────────────────
# 1. Create GCE VM from pre-baked Packer image
# ──────────────────────────────────────────────────────────
echo "→ Creating GCE VM from Packer image..."

if gcloud compute instances describe "$INSTANCE" --zone="$ZONE" &>/dev/null; then
  echo "  VM already exists, skipping creation"
else
  gcloud compute instances create "$INSTANCE" \
    --zone="$ZONE" \
    --machine-type="$MACHINE_TYPE" \
    --image-family=launchkit-buildkit \
    --image-project="$PROJECT" \
    --no-address \
    --tags=buildkit \
    --metadata=enable-oslogin=TRUE \
    --scopes=cloud-platform \
    --quiet
  echo "  VM created"
fi

# Get internal IP for BUILDKIT_ADDR
INTERNAL_IP=$(gcloud compute instances describe "$INSTANCE" --zone="$ZONE" \
  --format='get(networkInterfaces[0].networkIP)')
echo "  Internal IP: $INTERNAL_IP"

# ──────────────────────────────────────────────────────────
# 2. Create firewall rule (API Server → BuildKit TCP :1234)
# ──────────────────────────────────────────────────────────
echo "→ Creating firewall rule..."

RULE_NAME="allow-buildkit-internal"
if gcloud compute firewall-rules describe "$RULE_NAME" &>/dev/null; then
  echo "  Firewall rule already exists"
else
  gcloud compute firewall-rules create "$RULE_NAME" \
    --direction=INGRESS \
    --action=ALLOW \
    --rules=tcp:1234 \
    --target-tags=buildkit \
    --source-ranges=10.128.0.0/9 \
    --description="Allow API Server to reach BuildKit VM on TCP 1234 (VPC internal only)" \
    --quiet
  echo "  Firewall rule created"
fi

# ──────────────────────────────────────────────────────────
# 3. Wait for buildkitd to be ready
# ──────────────────────────────────────────────────────────
echo "→ Waiting for buildkitd to start (services are pre-installed in image)..."

for i in $(seq 1 30); do
  if gcloud compute ssh "$INSTANCE" --zone="$ZONE" --tunnel-through-iap \
    --command="systemctl is-active buildkitd" 2>/dev/null | grep -q active; then
    echo "  buildkitd is active"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "ERROR: buildkitd did not start within 60 seconds"
    gcloud compute ssh "$INSTANCE" --zone="$ZONE" --tunnel-through-iap \
      --command="journalctl -u buildkitd --no-pager -n 20" 2>/dev/null || true
    exit 1
  fi
  sleep 2
done

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Add to your .env:"
echo ""
echo "  BUILDKIT_ADDR=tcp://${INTERNAL_IP}:1234"
echo "  BUILDKIT_INSTANCE=${INSTANCE}"
echo "  BUILDKIT_ZONE=${ZONE}"
echo ""
echo "Isolation layers active (pre-baked in image):"
echo "  Tier 1: gVisor (runsc) — every RUN step sandboxed"
echo "  Tier 2: CNI bridge — per-build network namespace"
echo "  Tier 3: iron-proxy — egress firewall + DNS audit (default-deny allowlist)"
echo "  Tier 4: iptables — metadata + VPC internal blocked"
echo "  Tier 5: entitlements — security.insecure + network.host disabled"
echo "  Tier 6: no public IP — VPC internal only"
