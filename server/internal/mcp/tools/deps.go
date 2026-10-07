package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/server"
	"github.com/riverqueue/river"
)

// Deps carries shared dependencies into tool handlers.
type Deps struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger

	// Deploy flow
	PlanEngine   *deploy.Engine
	Orchestrator *deploy.Orchestrator
	BuildStorage provider.BuildStorage
	Compute      provider.Compute

	// Secret encryption (nil in dev mode — secrets stored as plaintext)
	Enc *deploy.Encryptor

	// Email provider (Resend — optional)
	Email provider.Email

	// Domain registrar (Name.com — optional)
	DomainRegistrar provider.DomainRegistrar

	// River job queue (for async deploys)
	RiverClient *river.Client[pgx.Tx]

	// MCP server reference (for progress notifications)
	MCPServer *server.MCPServer
}

// projectIDForUser resolves a project name to its ID, enforcing that the
// authenticated user is a member of the project's team. Returns a generic
// "not found" error to avoid leaking whether a project exists at all.
func projectIDForUser(ctx context.Context, db *pgxpool.Pool, projectName, userID string) (string, error) {
	var id string
	err := db.QueryRow(ctx, `
		SELECT p.id
		FROM projects p
		JOIN team_members tm ON p.team_id = tm.team_id
		WHERE p.name = $1 AND tm.user_id = $2
	`, projectName, userID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("project %q not found", projectName)
	}
	return id, nil
}

// requireUserID extracts the authenticated user ID from ctx or returns an error
// suitable for returning directly to the MCP caller.
func requireUserID(ctx context.Context) (string, error) {
	userID := auth.UserIDFromContext(ctx)
	if userID == "" {
		return "", fmt.Errorf("unauthenticated: no user ID in context")
	}
	return userID, nil
}

// cloudServiceName returns the globally-unique cloud provider service name for
// a given project UUID, environment UUID, and logical service name.
//
// envID must be the environment's UUID (not its display name). Pass an empty
// string only for legacy single-environment projects; multi-environment
// projects will produce colliding cloud names without it.
func cloudServiceName(projectID, envID, serviceName string) string {
	return deploy.ScopeServiceNameWithEnv(projectID, envID, serviceName)
}
