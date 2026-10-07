#!/usr/bin/env bash
# LaunchKit BuildKit VM provisioner
# Run by Packer as root — installs all software and bakes config into the image.
set -euo pipefail

BUILDKIT_VERSION="v0.20.2"
CNI_VERSION="v1.6.2"
CREDENTIAL_GCR_VERSION="2.1.25"
IRON_PROXY_VERSION="v0.5.1"
ARTIFACT_REGISTRY_REGION="${ARTIFACT_REGISTRY_REGION:-us-east4}"

echo "=== LaunchKit BuildKit Image Provisioner ==="

# ──────────────────────────────────────────────────────────
# 1. containerd 1.7.x from Docker repo (NOT Ubuntu default 2.x)
#    gVisor's containerd-shim-runsc-v1 is incompatible with containerd 2.x.
# ──────────────────────────────────────────────────────────
echo "[1/10] Installing containerd 1.7.x from Docker repo..."

apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg lsb-release

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg

echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
  > /etc/apt/sources.list.d/docker.list

apt-get update -qq
# Pin to 1.7.x — gVisor shim v1 requires containerd 1.x
apt-get install -y -qq containerd.io=1.7.*
apt-mark hold containerd.io

echo "  containerd version: $(containerd --version)"

# ──────────────────────────────────────────────────────────
# 2. gVisor (runsc + containerd-shim-runsc-v1)
# ──────────────────────────────────────────────────────────
echo "[2/10] Installing gVisor..."

curl -fsSL https://gvisor.dev/archive.key \
  | gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" \
  > /etc/apt/sources.list.d/gvisor.list

apt-get update -qq
apt-get install -y -qq runsc

echo "  runsc version: $(runsc --version 2>&1 | head -1)"
echo "  shim (stock): $(which containerd-shim-runsc-v1)"

# Replace stock shim with patched version that fixes non-CRI deadlock.
# See: https://github.com/google/gvisor/issues/12198
# Source: https://github.com/alan890104/gvisor/tree/minimal-fix
SHIM_RELEASE="https://github.com/alan890104/gvisor/releases/download/v0.1.0-shim-fix/containerd-shim-runsc-v1"
echo "  Downloading patched shim from GitHub Release..."
curl -fsSL -o /usr/bin/containerd-shim-runsc-v1 "$SHIM_RELEASE"
chmod +x /usr/bin/containerd-shim-runsc-v1
echo "  shim (patched): installed from $SHIM_RELEASE"

# ──────────────────────────────────────────────────────────
# 3. CNI plugins
# ──────────────────────────────────────────────────────────
echo "[3/10] Installing CNI plugins ${CNI_VERSION}..."

mkdir -p /opt/cni/bin
curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz" \
  | tar -xz -C /opt/cni/bin

echo "  CNI plugins installed: $(ls /opt/cni/bin | wc -w) binaries"

# ──────────────────────────────────────────────────────────
# 4. buildkitd
# ──────────────────────────────────────────────────────────
echo "[4/10] Installing buildkitd ${BUILDKIT_VERSION}..."

curl -fsSL "https://github.com/moby/buildkit/releases/download/${BUILDKIT_VERSION}/buildkit-${BUILDKIT_VERSION}.linux-amd64.tar.gz" \
  | tar -xz -C /usr/local

echo "  buildkitd: $(buildkitd --version 2>&1 || echo 'installed')"
echo "  buildctl:  $(buildctl --version 2>&1 || echo 'installed')"

# ──────────────────────────────────────────────────────────
# 5. docker-credential-gcr (Artifact Registry auth)
# ──────────────────────────────────────────────────────────
echo "[5/10] Installing docker-credential-gcr..."

curl -fsSL "https://github.com/GoogleCloudPlatform/docker-credential-gcr/releases/download/v${CREDENTIAL_GCR_VERSION}/docker-credential-gcr_linux_amd64-${CREDENTIAL_GCR_VERSION}.tar.gz" \
  | tar -xz -C /usr/local/bin

# Configure for Artifact Registry — buildkitd runs as root
mkdir -p /root/.docker
docker-credential-gcr configure-docker --registries="${ARTIFACT_REGISTRY_REGION}-docker.pkg.dev"

echo "  docker-credential-gcr configured for ${ARTIFACT_REGISTRY_REGION}-docker.pkg.dev"

# ──────────────────────────────────────────────────────────
# 6. iron-proxy (egress firewall for build containers)
# ──────────────────────────────────────────────────────────
echo "[6/10] Installing iron-proxy ${IRON_PROXY_VERSION}..."

# Tarball naming: iron-proxy_<ver>_linux_amd64.tar.gz (version without v prefix)
curl -fsSL "https://github.com/ironsh/iron-proxy/releases/download/${IRON_PROXY_VERSION}/iron-proxy_${IRON_PROXY_VERSION#v}_linux_amd64.tar.gz" \
  | tar -xz -C /usr/local/bin iron-proxy

mkdir -p /etc/iron-proxy /var/log/iron-proxy

# Generate MITM CA cert (baked into image).
# Phase 1: passthrough everything, CA unused.
# Phase 2+: non-passthrough domains resolve to proxy_ip, CA used for MITM interception.
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -days 3650 -nodes \
  -keyout /etc/iron-proxy/ca.key \
  -out /etc/iron-proxy/ca.crt \
  -subj "/CN=LaunchKit Build Proxy CA/O=LaunchKit" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign"
chmod 600 /etc/iron-proxy/ca.key
chmod 644 /etc/iron-proxy/ca.crt

echo "  iron-proxy installed, CA cert generated"

# ──────────────────────────────────────────────────────────
# 6b. Google Cloud Ops Agent (ships journal to Cloud Logging)
# ──────────────────────────────────────────────────────────
echo "[6b/10] Installing Ops Agent..."

# Ops Agent default config only tails flat files. Our custom config adds
# systemd_journald receiver so iron-proxy structured JSON logs are ingested
# into Cloud Logging with jsonPayload intact.
curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh
bash add-google-cloud-ops-agent-repo.sh --also-install
cp /tmp/configs/ops-agent.yaml /etc/google-cloud-ops-agent/config.yaml
systemctl restart google-cloud-ops-agent

echo "  Ops Agent installed, journald → Cloud Logging"

# ──────────────────────────────────────────────────────────
# 7. iptables-persistent (for network isolation rules)
# ──────────────────────────────────────────────────────────
echo "[7/10] Installing iptables + iptables-persistent..."

# iptables (nftables compat layer) must be explicit on Ubuntu 24.04.
# Used by CNI bridge plugin (ipMasq, firewall) and our FORWARD/INPUT security rules.
apt-get install -y -qq iptables

echo iptables-persistent iptables-persistent/autosave_v4 boolean true | debconf-set-selections
echo iptables-persistent iptables-persistent/autosave_v6 boolean true | debconf-set-selections
apt-get install -y -qq iptables-persistent

# ──────────────────────────────────────────────────────────
# 8. Copy config files to final locations
# ──────────────────────────────────────────────────────────
echo "[8/10] Installing config files..."

mkdir -p /etc/containerd /etc/buildkit

cp /tmp/configs/containerd.toml          /etc/containerd/config.toml
cp /tmp/configs/runsc.toml               /etc/containerd/runsc.toml
cp /tmp/configs/buildkitd.toml           /etc/buildkit/buildkitd.toml
cp /tmp/configs/buildkitd.service        /etc/systemd/system/buildkitd.service
cp /tmp/configs/iron-proxy.yaml          /etc/iron-proxy/iron-proxy.yaml
cp /tmp/configs/iron-proxy.service       /etc/systemd/system/iron-proxy.service

# Disable systemd-resolved: BuildKit detects 127.0.0.53 in /etc/resolv.conf
# and overrides buildkitd.toml [dns] with /run/systemd/resolve/resolv.conf
# (which points to 169.254.169.254 on GCE). Without this, build containers
# bypass iron-proxy and use the GCE metadata DNS directly.
systemctl disable systemd-resolved
systemctl stop systemd-resolved
rm -f /etc/resolv.conf
cat > /etc/resolv.conf << 'RESOLV'
nameserver 8.8.8.8
nameserver 8.8.4.4
RESOLV

# Enable IP forwarding so build containers can reach the internet
sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf

# Disable IPv6 on buildkit0 bridge to prevent IPv6 bypass of IPv4 firewall rules.
# Without this, containers could use IPv6 link-local to reach the host or other
# containers, completely bypassing our IPv4-only raw PREROUTING DROP rules.
cat >> /etc/sysctl.conf << 'SYSCTL'
net.ipv6.conf.buildkit0.disable_ipv6=1
net.ipv6.conf.buildkit0.autoconf=0
net.ipv6.conf.buildkit0.accept_ra=0
SYSCTL

# Network security for CNI bridge mode (buildkit0).
# BuildKit uses networkMode=bridge → each build container gets its own netns
# with a veth pair on the buildkit0 bridge. CNI ipMasq handles NAT automatically.
#
# The CNI firewall plugin inserts per-container ACCEPT rules into the filter
# FORWARD chain at runtime, which would bypass filter-table DROP rules.
# Solution: use the raw table's PREROUTING chain. The raw table is processed
# FIRST in the iptables pipeline (before conntrack, mangle, nat, filter).
# CNI plugins only operate in the filter table — they cannot override raw DROP.
#
# Pipeline: raw PREROUTING → conntrack → nat PREROUTING ��� routing → filter FORWARD

# raw PREROUTING: block build containers from reaching dangerous destinations.
# Packets are dropped before they enter conntrack or any filter chain.
#
# IMPORTANT: The bridge subnet (10.222.0.0/16) falls within the VPC block
# (10.128.0.0/9 = 10.128.0.0–10.255.255.255). We must ACCEPT bridge-local
# traffic BEFORE the VPC DROP, otherwise build containers cannot reach
# iron-proxy (10.222.0.1) for DNS/proxy services.
iptables -t raw -A PREROUTING -i buildkit0 -d 10.222.0.0/16 -j ACCEPT   # bridge-local (iron-proxy)
iptables -t raw -A PREROUTING -i buildkit0 -d 169.254.169.254 -j DROP   # GCE metadata (SA token theft)
iptables -t raw -A PREROUTING -i buildkit0 -d 10.128.0.0/9 -j DROP      # GCE VPC internal network
iptables -t raw -A PREROUTING -i buildkit0 -d 169.254.0.0/16 -j DROP    # All link-local addresses

# IPv6: drop ALL traffic from bridge. Build containers only need IPv4.
# Even though we disable IPv6 via sysctl, belt-and-suspenders with ip6tables.
ip6tables -t raw -A PREROUTING -i buildkit0 -j DROP

# filter INPUT: allow build containers to reach iron-proxy on the bridge gateway
# (10.222.0.1), then block all other host service access. iron-proxy handles DNS
# resolution (port 53) and egress proxy (ports 80, 443) for build containers.
# ARP is L2 (unaffected by iptables).
iptables -A INPUT -i buildkit0 -d 10.222.0.1 -p udp --dport 53 -j ACCEPT
iptables -A INPUT -i buildkit0 -d 10.222.0.1 -p tcp --dport 53 -j ACCEPT
iptables -A INPUT -i buildkit0 -d 10.222.0.1 -p tcp --dport 80 -j ACCEPT
iptables -A INPUT -i buildkit0 -d 10.222.0.1 -p tcp --dport 443 -j ACCEPT
iptables -A INPUT -i buildkit0 -j DROP
ip6tables -A INPUT -i buildkit0 -j DROP
netfilter-persistent save

systemctl daemon-reload
systemctl enable containerd
systemctl enable iron-proxy
systemctl enable buildkitd

echo "  Config files installed, services enabled"

# ──────────────────────────────────────────────────────────
# 9. Pre-cache Railpack images + common base images
# ──────────────────────────────────────────────────────────
echo "[9/10] Pre-caching images..."

systemctl start containerd
sleep 2

# Railpack images (used by every build without a Dockerfile)
for img in \
  ghcr.io/railwayapp/railpack-frontend:latest \
  ghcr.io/railwayapp/railpack-builder:mise-2026.3.17 \
  ghcr.io/railwayapp/railpack-runtime:mise-2026.3.17; do
  echo "  Pulling $img ..."
  ctr -n buildkit images pull "$img" >/dev/null 2>&1 || echo "  WARN: failed to pull $img (non-fatal)"
done

# Common base images (for users who bring their own Dockerfile)
for img in \
  docker.io/library/node:22-slim \
  docker.io/library/python:3.13-slim \
  docker.io/library/golang:1.23-alpine \
  docker.io/library/rust:1-slim; do
  echo "  Pulling $img ..."
  ctr -n buildkit images pull "$img" >/dev/null 2>&1 || echo "  WARN: failed to pull $img (non-fatal)"
done

echo "  Cached images: $(ctr -n buildkit images ls -q | wc -l)"

# ──────────────────────────────────────────────────────────
# 10. Validate: start services, check they work, then stop
# ──────────────────────────────────────────────────────────
echo "[10/10] Validating iron-proxy + buildkitd with gVisor (runsc) runtime..."

systemctl start iron-proxy
sleep 2

if systemctl is-active --quiet iron-proxy; then
  echo "  iron-proxy is running on 10.222.0.1:53"
else
  echo "WARNING: iron-proxy failed to start"
  journalctl -u iron-proxy --no-pager -n 20
fi

systemctl start buildkitd
sleep 5

if systemctl is-active --quiet buildkitd; then
  echo "  buildkitd is running"
  # Confirm gVisor (runsc) is the active runtime — must NOT show runc
  WORKERS=$(buildctl --addr tcp://127.0.0.1:1234 debug workers 2>&1)
  echo "$WORKERS" | head -20
  if echo "$WORKERS" | grep -q "runsc"; then
    echo "  gVisor runtime confirmed"
  else
    echo "WARNING: gVisor runtime not detected in workers output — check buildkitd.toml"
    echo "$WORKERS"
  fi
else
  echo "ERROR: buildkitd failed to start"
  journalctl -u buildkitd --no-pager -n 30
  exit 1
fi

# Stop services — they'll start on first boot via systemd enable
systemctl stop buildkitd
systemctl stop iron-proxy
systemctl stop containerd

# ──────────────────────────────────────────────────────────
# Cleanup: minimize image size
# ──────────────────────────────────────────────────────────
echo "Cleaning up..."
apt-get clean
rm -rf /var/lib/apt/lists/* /tmp/configs

echo ""
echo "=== BuildKit image provisioning complete ==="
echo "  containerd:  1.7.x (Docker repo, held)"
echo "  gVisor:      runsc + containerd-shim-runsc-v1"
echo "  buildkitd:   ${BUILDKIT_VERSION}"
echo "  CNI:         ${CNI_VERSION}"
echo "  iron-proxy:  ${IRON_PROXY_VERSION} (egress firewall)"
echo "  Runtime:     io.containerd.runsc.v1"
echo "  Cached:      railpack-frontend/builder/runtime + node:22, python:3.13, golang:1.23, rust:1"
