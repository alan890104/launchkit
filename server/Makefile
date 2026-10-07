.PHONY: dev dev-down run build test tidy

# Start Postgres + run server with dev defaults
dev: dev-up
	DATABASE_URL=postgresql://launchkit:launchkit@localhost:5432/launchkit?sslmode=disable \
	go run ./cmd/api

dev-up:
	docker compose up -d --wait

dev-down:
	docker compose down

run:
	go run ./cmd/api

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/launchkit ./cmd/api

test:
	go test ./...

tidy:
	go mod tidy
