package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/riverqueue/river"
)

// ─────────────────────────────────────────────────────────────────────────────
// redeploy
// ─────────────────────────────────────────────────────────────────────────────

func RegisterRedeploy(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("redeploy",
			mcp.WithDescription(
				"Re-deploy a project using the most recent deployment plan. "+
					"Skips plan_deployment — just rebuilds and deploys from the same plan. "+
					"Use this when code has changed but the architecture is the same.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name to redeploy"),
			),
			mcp.WithString("service_name",
				mcp.Description("Optional: redeploy only this service instead of the full project"),
			),
		),
		makeRedeployHandler(deps),
	)
}

func makeRedeployHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		serviceName, _ := args["service_name"].(string)

		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Find the most recent plan for this project.
		var planID string
		var planData []byte
		err = deps.DB.QueryRow(ctx, `
			SELECT id, plan_data FROM plans
			WHERE project_id = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, projectID).Scan(&planID, &planData)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("no deployment plan found for project %q: %v", projectName, err)), nil
		}

		// Parse the plan to validate and optionally filter by service.
		var plan deploy.Plan
		if err := json.Unmarshal(planData, &plan); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("corrupt plan data: %v", err)), nil
		}

		if serviceName != "" {
			found := false
			for _, svc := range plan.Services {
				if svc.Name == serviceName {
					found = true
					break
				}
			}
			if !found {
				return mcp.NewToolResultError(fmt.Sprintf("service %q not found in plan %s", serviceName, planID)), nil
			}
		}

		// For cloud mode, verify source still exists in storage.
		if plan.BuildMode != "local" {
			sourceKey := fmt.Sprintf("sources/%s/source.archive", planID)
			exists, err := deps.BuildStorage.Exists(ctx, sourceKey)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("check source archive: %v", err)), nil
			}
			if !exists {
				return mcp.NewToolResultError(
					"source archive no longer available — the original upload may have expired. " +
						"Run plan_deployment + upload + deploy_project instead.",
				), nil
			}
		}

		// Mark the plan as confirmed so the deploy worker accepts it.
		if err := deps.PlanEngine.UpdateStatus(ctx, planID, deploy.PlanConfirmed); err != nil {
			slog.Error("redeploy: failed to confirm plan", "plan_id", planID, "error", err)
		}

		// Create a new deployment record.
		deploymentID := uuid.NewString()
		_, err = deps.DB.Exec(ctx,
			"INSERT INTO deployments (id, status, plan_id, trigger) VALUES ($1, 'pending', $2, 'redeploy')",
			deploymentID, planID,
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create deployment record: %v", err)), nil
		}

		if deps.RiverClient == nil {
			return mcp.NewToolResultError("deploy queue not initialized (check server logs)"), nil
		}
		res, err := deps.RiverClient.Insert(ctx, &worker.DeployJobArgs{
			DeploymentID: deploymentID,
			PlanID:       planID,
			IsRecovery:   false,
		}, &river.InsertOpts{
			UniqueOpts: river.UniqueOpts{ByArgs: true},
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("enqueue deployment: %v", err)), nil
		}

		slog.Info("redeploy enqueued",
			"project", projectName,
			"plan_id", planID,
			"deployment_id", deploymentID,
			"service_filter", serviceName,
		)
		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "redeploy", "project", projectID, map[string]any{
			"plan_id":        planID,
			"deployment_id":  deploymentID,
			"service_filter": serviceName,
		})

		data, err := json.Marshal(map[string]any{
			"status":         "queued",
			"plan_id":        planID,
			"deployment_id":  deploymentID,
			"job_id":         res.Job.ID,
			"project":        projectName,
			"service_filter": serviceName,
			"message":        "Redeploy queued using existing plan. Use deploy_status with the deployment_id to poll progress.",
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}
