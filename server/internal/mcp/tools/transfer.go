package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTransfer registers the transfer_project tool.
func RegisterTransfer(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("transfer_project",
			mcp.WithDescription("Transfer a project to another team. The target team is identified by the team owner's email. You must be the owner of the current team."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project to transfer")),
			mcp.WithString("target_email", mcp.Required(), mcp.Description("Email of the target team owner")),
		),
		makeTransferProjectHandler(deps),
	)
}

func makeTransferProjectHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		projectName, _ := args["project_name"].(string)
		targetEmail, _ := args["target_email"].(string)

		if projectName == "" || targetEmail == "" {
			return mcp.NewToolResultError("project_name and target_email are required"), nil
		}

		// Verify caller owns the project's team
		var projectID, currentTeamID, teamOwnerID string
		err := deps.DB.QueryRow(ctx, `
			SELECT p.id, p.team_id, t.owner_id
			FROM projects p
			JOIN teams t ON p.team_id = t.id
			WHERE p.name = $1 AND p.team_id = ANY($2)
		`, projectName, scope.TeamIDs).Scan(&projectID, &currentTeamID, &teamOwnerID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project %q not found", projectName)), nil
		}

		if teamOwnerID != scope.UserID {
			return mcp.NewToolResultError("only the team owner can transfer projects"), nil
		}

		// Find target user and their personal team
		var targetUserID, targetTeamID, targetTeamName string
		err = deps.DB.QueryRow(ctx, `
			SELECT u.id, t.id, t.name
			FROM users u
			JOIN teams t ON t.owner_id = u.id
			WHERE u.email = $1
			LIMIT 1
		`, targetEmail).Scan(&targetUserID, &targetTeamID, &targetTeamName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("no user found with email %q — they must sign up first", targetEmail)), nil
		}

		if targetTeamID == currentTeamID {
			return mcp.NewToolResultError("project already belongs to this team"), nil
		}

		// Transfer: update project's team_id
		_, err = deps.DB.Exec(ctx, `
			UPDATE projects SET team_id = $1, updated_at = NOW() WHERE id = $2
		`, targetTeamID, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("transfer failed: %v", err)), nil
		}

		resp := map[string]any{
			"project":        projectName,
			"transferred_to": targetEmail,
			"new_team":       targetTeamName,
			"message":        fmt.Sprintf("Project %q has been transferred to %s's team. They now have full ownership.", projectName, targetEmail),
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}
