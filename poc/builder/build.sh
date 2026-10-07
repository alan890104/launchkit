#!/bin/sh
set -e

export DOCKER_API_VERSION=1.41

echo "[launchkit] Loading pre-cached images..."
docker load < /opt/launchkit/images/buildkit.tar
echo "[launchkit] buildkitd image loaded"

echo "[launchkit] Creating buildx builder..."
docker buildx create --name rb --driver docker-container --use
docker buildx inspect --bootstrap

echo "[launchkit] Running railpack prepare..."
railpack prepare /workspace --plan-out /workspace/railpack-plan.json

echo "[launchkit] Building + pushing image..."
docker buildx build \
  --build-arg BUILDKIT_SYNTAX="ghcr.io/railwayapp/railpack-frontend" \
  -f /workspace/railpack-plan.json \
  --push \
  -t "$IMAGE_TAG" \
  /workspace

echo "[launchkit] Done!"
