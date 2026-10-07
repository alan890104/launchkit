package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	projectsvc "github.com/alan890104/launchkit/server/internal/service/project"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ─────────────────────────────────────────────────────────────────────────────
// rollback
// ─────────────────────────────────────────────────────────────────────────────

func RegisterRollback(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("rollback",
			mcp.WithDescription(
				"Roll back a service to a previous deployment. "+
					"Lists recent deployments and redeploys the specified version. "+
					"Use this when a new deployment breaks something and you want to go back immediately.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Required(),
				mcp.Description("The service name to roll back"),
			),
			mcp.WithString("deployment_id",
				mcp.Description("Specific deployment ID to roll back to. "+
					"If omitted, rolls back to the previous successful deployment."),
			),
		),
		makeRollbackHandler(deps),
	)
}

func makeRollbackHandler(deps *Deps) server.ToolHandlerFunc {
	svc := buildProjectService(deps)
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		service, ok := args["service"].(string)
		if !ok || service == "" {
			return mcp.NewToolResultError("service is required"), nil
		}

		targetDeploymentID, _ := args["deployment_id"].(string)

		// Resolve project ID for the authenticated user.
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		rbResult, err := svc.Rollback(ctx, projectsvc.RollbackInput{
			ProjectID:    projectID,
			ServiceName:  service,
			DeploymentID: targetDeploymentID,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(map[string]any{
			"status":         "rolled_back",
			"project":        project,
			"service":        service,
			"rolled_back_to": rbResult.RolledBackTo,
			"image":          rbResult.ImageURI,
			"message":        "Rollback complete. The previous version is now live.",
		})
		return mcp.NewToolResultText(string(data)), nil
	})
}
