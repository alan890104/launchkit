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

func RegisterDeployLogs(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("deploy_logs",
			mcp.WithDescription(
				"Get recent logs from a deployed service. "+
					"Returns log entries with timestamp, severity, and message.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Required(),
				mcp.Description("The service name"),
			),
			mcp.WithNumber("lines",
				mcp.Description("Number of log lines to return (default 50, max 500)"),
			),
		),
		makeDeployLogsHandler(deps),
	)
}

func makeDeployLogsHandler(deps *Deps) server.ToolHandlerFunc {
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

		lines := 50
		if l, ok := args["lines"].(float64); ok && l > 0 {
			lines = int(l)
			if lines > 500 {
				lines = 500
			}
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		var target, envID string
		err = deps.DB.QueryRow(ctx, `
			SELECT s.target, s.environment_id
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1 AND s.name = $2
		`, projectID, service).Scan(&target, &envID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("service not found: %v", err)), nil
		}

		if !deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
			return mcp.NewToolResultText(`{"message":"Logs are only available for compute services. Static site logs are available in the Cloudflare dashboard."}`), nil
		}

		// Fetch logs via the Compute provider
		if deps.Compute == nil {
			return mcp.NewToolResultError("cloud provider not configured"), nil
		}
		serviceName := cloudServiceName(projectID, envID, service)
		entries, err := deps.Compute.GetLogs(ctx, serviceName, deps.Config.Region(), lines)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("fetch logs: %v", err)), nil
		}

		type logLine struct {
			Timestamp string `json:"timestamp"`
			Severity  string `json:"severity"`
			Message   string `json:"message"`
		}

		logLines := make([]logLine, len(entries))
		for i, e := range entries {
			logLines[i] = logLine{
				Timestamp: e.Timestamp.Format("2006-01-02T15:04:05Z"),
				Severity:  e.Severity,
				Message:   e.Message,
			}
		}

		data, err := json.Marshal(map[string]any{
			"project": project,
			"service": service,
			"lines":   len(logLines),
			"logs":    logLines,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}
