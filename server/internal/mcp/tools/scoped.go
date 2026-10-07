package tools

import (
	"context"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AuthToolHandler is the auth-aware handler signature. Handlers that use
// WithAuth receive the pre-resolved AuthScope directly, eliminating the
// need to call requireUserID() or projectIDForUser() manually.
type AuthToolHandler func(ctx context.Context, scope auth.AuthScope, req mcp.CallToolRequest) (*mcp.CallToolResult, error)

// WithAuth wraps an AuthToolHandler into the standard ToolHandlerFunc.
// The AuthScope is extracted from context (injected by AuthToolMiddleware).
// Security does NOT depend on this wrapper — the global middleware rejects
// unauthenticated calls regardless. This is for ergonomics.
func WithAuth(handler AuthToolHandler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		scope, ok := auth.ScopeFromContext(ctx)
		if !ok {
			// Should never happen if AuthToolMiddleware is active.
			return mcp.NewToolResultError("BUG: AuthScope not in context — is AuthToolMiddleware registered?"), nil
		}
		return handler(ctx, scope, req)
	}
}

// ProjectForScope resolves a project name to its ID using the pre-resolved
// team memberships in AuthScope. This replaces projectIDForUser and avoids
// the team_members JOIN on every query — membership was resolved once in
// the middleware.
//
// Returns a generic "not found" error to avoid leaking project existence.
func ProjectForScope(ctx context.Context, db *pgxpool.Pool, scope auth.AuthScope, name string) (string, error) {
	var id string
	err := db.QueryRow(ctx,
		"SELECT id FROM projects WHERE name = $1 AND team_id = ANY($2)",
		name, scope.TeamIDs,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("project %q not found", name)
	}
	return id, nil
}

// TeamIDForScope returns the team that owns a given project, verifying
// it belongs to one of the user's teams via AuthScope.
func TeamIDForScope(ctx context.Context, db *pgxpool.Pool, scope auth.AuthScope, projectID string) (string, error) {
	var teamID string
	err := db.QueryRow(ctx,
		"SELECT team_id FROM projects WHERE id = $1 AND team_id = ANY($2)",
		projectID, scope.TeamIDs,
	).Scan(&teamID)
	if err != nil {
		return "", fmt.Errorf("project not found or not accessible")
	}
	return teamID, nil
}
