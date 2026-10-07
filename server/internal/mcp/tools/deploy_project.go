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

func RegisterDeployProject(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("deploy_project",
			mcp.WithDescription(
				"Deploy a project based on a confirmed plan. "+
					"The source code must already be uploaded to the presigned URL from plan_deployment. "+
					"This is a long-running operation (2-5 minutes). Progress is reported via notifications.",
			),
			mcp.WithString("plan_id",
				mcp.Required(),
				mcp.Description("The plan ID returned by plan_deployment"),
			),
		),
		makeDeployProjectHandler(deps),
	)
}

func makeDeployProjectHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		planID, ok := request.GetArguments()["plan_id"].(string)
		if !ok || planID == "" {
			return mcp.NewToolResultError("plan_id is required"), nil
		}

		// Retrieve the plan
		plan, err := deps.PlanEngine.GetPlan(ctx, planID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("plan not found: %v", err)), nil
		}

		if plan.Status != deploy.PlanPending && plan.Status != deploy.PlanConfirmed {
			return mcp.NewToolResultError(fmt.Sprintf("plan is in state %q, expected pending or confirmed", plan.Status)), nil
		}

		// Verify that the plan's project belongs to one of the user's teams.
		var planOwned bool
		_ = deps.DB.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM projects p
				WHERE p.id = $1 AND p.team_id = ANY($2)
			)
		`, plan.ProjectID, scope.TeamIDs).Scan(&planOwned)
		if !planOwned {
			return mcp.NewToolResultError("plan not found"), nil
		}

		// Cloud mode only: verify source was uploaded to GCS/S3.
		// Local mode: source is already on disk — no upload needed.
		if plan.BuildMode != "local" {
			sourceKey := fmt.Sprintf("sources/%s/source.archive", plan.ID)
			exists, err := deps.BuildStorage.Exists(ctx, sourceKey)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("check source upload: %v", err)), nil
			}
			if !exists {
				return mcp.NewToolResultError("source tarball not yet uploaded — upload to the presigned URL first"), nil
			}
		}

		// Update plan status to confirmed
		if err := deps.PlanEngine.UpdateStatus(ctx, planID, deploy.PlanConfirmed); err != nil {
			slog.Error("failed to confirm plan", "plan_id", planID, "error", err)
		}

		// Create a deployment record
		deploymentID := uuid.NewString()
		_, err = deps.DB.Exec(ctx,
			"INSERT INTO deployments (id, status, plan_id) VALUES ($1, 'pending', $2)",
			deploymentID, planID,
		)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create deployment record: %v", err)), nil
		}

		// Enqueue an async deploy job — the MCP handler returns immediately so the
		// connection is not held open for the 2-5 minute deployment duration.
		// Claude polls progress with deploy_status using the returned deployment_id.
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

		// Persist river job ID so cancel_deployment can cancel in-flight deploys.
		_, _ = deps.DB.Exec(ctx,
			"UPDATE deployments SET river_job_id = $1 WHERE id = $2",
			res.Job.ID, deploymentID,
		)

		// Audit log
		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, plan.ProjectID), scope.UserID, "deploy", "plan", planID, map[string]any{
			"deployment_id": deploymentID,
			"plan_id":       planID,
		})

		data, err := json.Marshal(map[string]any{
			"status":        "queued",
			"plan_id":       planID,
			"deployment_id": deploymentID,
			"job_id":        res.Job.ID,
			"message":       "Deployment queued. Use deploy_status with the deployment_id to poll progress.",
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	})
}
