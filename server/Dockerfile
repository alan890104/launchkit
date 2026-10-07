# ── Build stage ──────────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/launchkit ./cmd/api

# ── Runtime stage ────────────────────────────────
FROM debian:bookworm-slim

ARG RAILPACK_VERSION=0.23.0

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin \
    && rm -rf /var/lib/apt/lists/*

# Railpack CLI
RUN curl -fsSL "https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}/railpack-v${RAILPACK_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
    | tar xz -C /usr/local/bin/

RUN useradd -r -s /usr/sbin/nologin launchkit

COPY --from=builder /bin/launchkit /bin/launchkit

# DB migrations — read at startup by goose
COPY internal/db/migrations /app/migrations

USER launchkit
EXPOSE 8080

ENTRYPOINT ["/bin/launchkit"]
