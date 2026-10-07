package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterPlanDeployment(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("plan_deployment",
			mcp.WithDescription(
				"Submit project hints and receive a validated deployment plan with an upload URL. "+
					"Claude should analyze the user's source code locally, then call this tool with the detailed results. "+
					"Returns a Plan JSON with a presigned upload URL for the source tarball. "+
					"CRITICAL: If a variable should be injected from a resource, set 'inject_from' to the exact resource 'name' (Logical ID) you defined in the resources array. If 'inject_from' is set but the resource name does not exist, the Plan Engine will fail your request.",
			),
			mcp.WithInputSchema[deploy.Hints](),
		),
		mcp.NewTypedToolHandler[deploy.Hints](makePlanDeploymentHandler(deps)),
	)
}

func makePlanDeploymentHandler(deps *Deps) mcp.TypedToolHandlerFunc[deploy.Hints] {
	return func(ctx context.Context, request mcp.CallToolRequest, hints deploy.Hints) (*mcp.CallToolResult, error) {
		if err := hints.Validate(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("hints validation failed: %v", err)), nil
		}

		// Resolve project name → ID, auto-creating user/team/project in dev mode
		projectID, err := resolveOrCreateProject(ctx, deps, hints.ProjectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolve project: %v", err)), nil
		}
		hints.ProjectID = projectID

		plan, err := deps.PlanEngine.Generate(ctx, hints)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("plan generation failed: %v", err)), nil
		}

		result, err := json.Marshal(plan)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal plan: %v", err)), nil
		}
		return mcp.NewToolResultText(string(result)), nil
	}
}

// MakePlanDeploymentHandlerForTest exports the handler for security testing.
// DO NOT use in production code.
func MakePlanDeploymentHandlerForTest(deps *Deps) mcp.TypedToolHandlerFunc[deploy.Hints] {
	return makePlanDeploymentHandler(deps)
}

// resolveOrCreateProject looks up a project by name scoped to the authenticated user.
// In dev mode, auto-creates user/team/project if missing.
func resolveOrCreateProject(ctx context.Context, deps *Deps, projectName string) (string, error) {
	// In production: enforce user scoping
	if !deps.Config.IsDev() {
		userID, err := requireUserID(ctx)
		if err != nil {
			return "", err
		}
		return projectIDForUser(ctx, deps.DB, projectName, userID)
	}

	// Dev mode: try scoped lookup first (using authenticated or dev user),
	// then auto-create if missing. The scoped lookup prevents accessing
	// projects belonging to other users even in dev mode.
	userID := auth.UserIDFromContext(ctx)
	if userID == "" {
		userID = "dev-user"
	}
	projectID, scopeErr := projectIDForUser(ctx, deps.DB, projectName, userID)
	if scopeErr == nil {
		return projectID, nil
	}

	// Ensure a dev user exists
	const devUserID = "dev-user"
	_, err := deps.DB.Exec(ctx,
		"INSERT INTO users (id, email, name) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
		devUserID, "dev@launchkit.local", "Dev User",
	)
	if err != nil {
		return "", fmt.Errorf("create dev user: %w", err)
	}

	// Ensure a dev team exists
	const devTeamID = "dev-team"
	_, err = deps.DB.Exec(ctx,
		"INSERT INTO teams (id, name, owner_id) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
		devTeamID, "Dev Team", devUserID,
	)
	if err != nil {
		return "", fmt.Errorf("create dev team: %w", err)
	}

	// Ensure dev user is a member of the dev team (required for scoped lookups)
	_, err = deps.DB.Exec(ctx,
		"INSERT INTO team_members (team_id, user_id, role) VALUES ($1, $2, 'owner') ON CONFLICT (team_id, user_id) DO NOTHING",
		devTeamID, devUserID,
	)
	if err != nil {
		return "", fmt.Errorf("create dev team member: %w", err)
	}

	// Create the project (idempotent via team_id+name unique constraint)
	err = deps.DB.QueryRow(ctx,
		"INSERT INTO projects (team_id, name) VALUES ($1, $2) ON CONFLICT (team_id, name) DO UPDATE SET name = EXCLUDED.name RETURNING id",
		devTeamID, projectName,
	).Scan(&projectID)
	if err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}

	return projectID, nil
}
