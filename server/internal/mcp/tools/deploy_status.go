package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterDeployStatus(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("deploy_status",
			mcp.WithDescription(
				"Get current status of all services in a project, including per-step deployment progress. "+
					"Returns service names, URLs, status, and individual step progress from deployment_steps.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
		),
		makeDeployStatusHandler(deps),
	)
}

func makeDeployStatusHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, ok := request.GetArguments()["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Query services for this project — scoped to authenticated user via projectID.
		svcRows, err := deps.DB.Query(ctx, `
			SELECT s.name, s.type, s.target, s.url, s.status, s.framework, s.updated_at
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1
			ORDER BY s.name
		`, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query services: %v", err)), nil
		}
		defer svcRows.Close()

		type svcStatus struct {
			Name      string `json:"name"`
			Type      string `json:"type"`
			Target    string `json:"target"`
			URL       string `json:"url"`
			Status    string `json:"status"`
			Framework string `json:"framework"`
			UpdatedAt string `json:"updated_at"`
		}

		var services []svcStatus
		for svcRows.Next() {
			var s svcStatus
			var updatedAt time.Time
			if err := svcRows.Scan(&s.Name, &s.Type, &s.Target, &s.URL, &s.Status, &s.Framework, &updatedAt); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan service: %v", err)), nil
			}
			s.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
			services = append(services, s)
		}
		if err := svcRows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate services: %v", err)), nil
		}

		// Query latest deployment steps (per-step progress)
		type stepStatus struct {
			Step       string  `json:"step"`
			Status     string  `json:"status"`
			StartedAt  *string `json:"started_at,omitempty"`
			FinishedAt *string `json:"finished_at,omitempty"`
			Error      *string `json:"error,omitempty"`
		}

		var steps []stepStatus

		// Get the latest deployment for this project — use already-validated projectID.
		var latestDeploymentID *string
		_ = deps.DB.QueryRow(ctx, `
			SELECT d.id
			FROM deployments d
			JOIN services s ON d.service_id = s.id
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1
			ORDER BY d.created_at DESC
			LIMIT 1
		`, projectID).Scan(&latestDeploymentID)

		if latestDeploymentID != nil {
			stepRows, err := deps.DB.Query(ctx,
				"SELECT step, status, started_at::text, finished_at::text, error FROM deployment_steps WHERE deployment_id = $1 ORDER BY started_at NULLS LAST",
				*latestDeploymentID,
			)
			if err == nil {
				defer stepRows.Close()
				for stepRows.Next() {
					var s stepStatus
					if err := stepRows.Scan(&s.Step, &s.Status, &s.StartedAt, &s.FinishedAt, &s.Error); err != nil {
						slog.Error("scan deployment step", "deployment_id", *latestDeploymentID, "error", err)
						continue
					}
					steps = append(steps, s)
				}
				if err := stepRows.Err(); err != nil {
					slog.Error("iterate deployment steps", "deployment_id", *latestDeploymentID, "error", err)
				}
			}
		}

		// Check if deployment is waiting for secrets
		var secretFormURL *string
		if latestDeploymentID != nil {
			var reqID string
			err := deps.DB.QueryRow(ctx,
				`SELECT provider_id FROM deployment_steps
				 WHERE deployment_id = $1 AND step = 'secrets' AND status = 'running'`,
				*latestDeploymentID,
			).Scan(&reqID)
			if err == nil && reqID != "" {
				u := fmt.Sprintf("%s/s/%s", deps.Config.BaseURL, reqID)
				secretFormURL = &u
			}
		}

		if len(services) == 0 && len(steps) == 0 {
			empty, _ := json.Marshal(map[string]any{
				"project":  project,
				"services": []any{},
				"steps":    []any{},
				"message":  "No services found. Deploy first with plan_deployment + deploy_project.",
			})
			return mcp.NewToolResultText(string(empty)), nil
		}

		data, err := json.Marshal(map[string]any{
			"project":         project,
			"services":        services,
			"steps":           steps,
			"secret_form_url": secretFormURL,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}
