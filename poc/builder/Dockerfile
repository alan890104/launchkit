FROM ubuntu:24.04

ARG RAILPACK_VERSION=0.23.0
ARG CRANE_VERSION=0.20.3

# Docker CLI + buildx
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable" \
    > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin \
    && rm -rf /var/lib/apt/lists/*

# railpack CLI
RUN wget -q "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/railpack-v${RAILPACK_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
    -O /tmp/railpack.tar.gz \
    && tar xzf /tmp/railpack.tar.gz -C /usr/local/bin/ \
    && rm /tmp/railpack.tar.gz

# crane (downloads images without needing a Docker daemon)
RUN curl -fsSL "https://github.com/google/go-containerregistry/releases/download/v${CRANE_VERSION}/go-containerregistry_Linux_x86_64.tar.gz" \
    | tar xz -C /usr/local/bin/ crane

# Pre-download buildkitd + railpack base images as tarballs (downloaded at build time, loaded at runtime)
RUN mkdir -p /opt/launchkit/images \
    && crane pull --platform linux/amd64 moby/buildkit:buildx-stable-1 /opt/launchkit/images/buildkit.tar \
    && crane pull --platform linux/amd64 ghcr.io/railwayapp/railpack-frontend:latest /opt/launchkit/images/railpack-frontend.tar \
    && crane pull --platform linux/amd64 ghcr.io/railwayapp/railpack-builder:mise-2026.3.17 /opt/launchkit/images/railpack-builder.tar \
    && crane pull --platform linux/amd64 ghcr.io/railwayapp/railpack-runtime:mise-2026.3.17 /opt/launchkit/images/railpack-runtime.tar \
    && ls -lh /opt/launchkit/images/

# Startup script
COPY build.sh /usr/local/bin/launchkit-build.sh
RUN chmod +x /usr/local/bin/launchkit-build.sh

RUN docker --version && docker buildx version && railpack --version
