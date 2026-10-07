package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/db"
	"github.com/alan890104/launchkit/server/internal/server"
)

func main() {
	// Structured JSON logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// Create connection pool
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Run database migrations
	slog.Info("running database migrations")
	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Start HTTP server
	srv := server.New(cfg, pool)
	if err := srv.Start(ctx); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
