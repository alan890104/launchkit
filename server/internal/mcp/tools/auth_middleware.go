package tools

import (
	"context"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// noAuthTools lists tools that do NOT require authentication.
// Every other tool is rejected if the request has no authenticated user.
var noAuthTools = map[string]bool{
	"ping":                     true,
	"get_registry_credentials": true,
}

// AuthToolMiddleware returns an mcp-go ToolHandlerMiddleware that:
//  1. Rejects unauthenticated requests (unless the tool is in noAuthTools)
//  2. Resolves the user's team memberships ONCE and injects auth.AuthScope
//     into context so that handlers never need to JOIN team_members themselves.
func AuthToolMiddleware(deps *Deps) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Skip auth for explicitly exempted tools.
			if noAuthTools[req.Params.Name] {
				return next(ctx, req)
			}

			userID := auth.UserIDFromContext(ctx)
			if userID == "" {
				return mcp.NewToolResultError("unauthenticated: no user ID in context"), nil
			}

			scope, err := auth.ResolveAuthScope(ctx, deps.DB, userID)
			if err != nil {
				deps.Logger.ErrorContext(ctx, "resolve auth scope", "error", err, "user_id", userID)
				return mcp.NewToolResultError("failed to resolve authorization"), nil
			}

			ctx = auth.ContextWithScope(ctx, scope)
			return next(ctx, req)
		}
	}
}
