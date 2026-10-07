package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterCron registers cron job management tools.
func RegisterCron(srv *server.MCPServer, deps *Deps) {
	registerCreateCronJob(srv, deps)
	registerListCronJobs(srv, deps)
	registerDeleteCronJob(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// create_cron_job
// ─────────────────────────────────────────────────────────────────────────────

func registerCreateCronJob(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("create_cron_job",
			mcp.WithDescription(
				"Create a scheduled cron job for a service. "+
					"The job runs a command on the specified schedule (standard cron expression).",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service_name",
				mcp.Required(),
				mcp.Description("The service name to attach the cron job to"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("A descriptive name for the cron job"),
			),
			mcp.WithString("schedule",
				mcp.Required(),
				mcp.Description("Cron expression (e.g. \"0 2 * * *\" for daily at 2 AM UTC)"),
			),
			mcp.WithString("command",
				mcp.Required(),
				mcp.Description("The command to execute on schedule"),
			),
		),
		makeCreateCronJobHandler(deps),
	)
}

func makeCreateCronJobHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		serviceName, ok := args["service_name"].(string)
		if !ok || serviceName == "" {
			return mcp.NewToolResultError("service_name is required"), nil
		}

		name, ok := args["name"].(string)
		if !ok || name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}

		schedule, ok := args["schedule"].(string)
		if !ok || schedule == "" {
			return mcp.NewToolResultError("schedule is required"), nil
		}
		if err := deploy.ValidateCronExpression(schedule); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid cron expression: %v", err)), nil
		}

		command, ok := args["command"].(string)
		if !ok || command == "" {
			return mcp.NewToolResultError("command is required"), nil
		}

		// Auth: resolve project via scoped teams
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Resolve service_id from project + service name
		var serviceID string
		err = deps.DB.QueryRow(ctx, `
			SELECT s.id
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1 AND s.name = $2
			LIMIT 1
		`, projectID, serviceName).Scan(&serviceID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("service %q not found in project %q: %v", serviceName, projectName, err)), nil
		}

		// Insert the cron job
		var jobID string
		var createdAt time.Time
		err = deps.DB.QueryRow(ctx, `
			INSERT INTO cron_jobs (service_id, name, schedule, command)
			VALUES ($1, $2, $3, $4)
			RETURNING id, created_at
		`, serviceID, name, schedule, command).Scan(&jobID, &createdAt)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create cron job: %v", err)), nil
		}

		data, err := json.Marshal(map[string]any{
			"id":           jobID,
			"project_name": projectName,
			"service_name": serviceName,
			"name":         name,
			"schedule":     schedule,
			"command":      command,
			"status":       "active",
			"created_at":   createdAt.UTC().Format(time.RFC3339),
			"message":      "Cron job created successfully.",
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// list_cron_jobs
// ─────────────────────────────────────────────────────────────────────────────

func registerListCronJobs(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("list_cron_jobs",
			mcp.WithDescription(
				"List all cron jobs for a project.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name"),
			),
		),
		makeListCronJobsHandler(deps),
	)
}

func makeListCronJobsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		// Auth: resolve project via scoped teams
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		rows, err := deps.DB.Query(ctx, `
			SELECT c.id, s.name, c.name, c.schedule, c.command, c.status, c.last_run_at, c.next_run_at, c.created_at
			FROM cron_jobs c
			JOIN services s ON c.service_id = s.id
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1
			ORDER BY c.created_at DESC
		`, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query cron jobs: %v", err)), nil
		}
		defer rows.Close()

		type cronJob struct {
			ID          string  `json:"id"`
			ServiceName string  `json:"service_name"`
			Name        string  `json:"name"`
			Schedule    string  `json:"schedule"`
			Command     string  `json:"command"`
			Status      string  `json:"status"`
			LastRunAt   *string `json:"last_run_at,omitempty"`
			NextRunAt   *string `json:"next_run_at,omitempty"`
			CreatedAt   string  `json:"created_at"`
		}

		var jobs []cronJob
		for rows.Next() {
			var j cronJob
			var lastRun, nextRun *time.Time
			var createdAt time.Time
			if err := rows.Scan(&j.ID, &j.ServiceName, &j.Name, &j.Schedule, &j.Command, &j.Status, &lastRun, &nextRun, &createdAt); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan cron job: %v", err)), nil
			}
			j.CreatedAt = createdAt.UTC().Format(time.RFC3339)
			if lastRun != nil {
				ts := lastRun.UTC().Format(time.RFC3339)
				j.LastRunAt = &ts
			}
			if nextRun != nil {
				ts := nextRun.UTC().Format(time.RFC3339)
				j.NextRunAt = &ts
			}
			jobs = append(jobs, j)
		}
		if err := rows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate cron jobs: %v", err)), nil
		}

		if jobs == nil {
			jobs = []cronJob{}
		}

		data, err := json.Marshal(map[string]any{
			"project_name": projectName,
			"cron_jobs":    jobs,
			"total":        len(jobs),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// delete_cron_job
// ─────────────────────────────────────────────────────────────────────────────

func registerDeleteCronJob(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("delete_cron_job",
			mcp.WithDescription(
				"Delete a cron job by ID.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("job_id",
				mcp.Required(),
				mcp.Description("The cron job ID to delete"),
			),
		),
		makeDeleteCronJobHandler(deps),
	)
}

func makeDeleteCronJobHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		jobID, ok := args["job_id"].(string)
		if !ok || jobID == "" {
			return mcp.NewToolResultError("job_id is required"), nil
		}

		// Auth: resolve project via scoped teams
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Delete the cron job, ensuring it belongs to the user's project
		tag, err := deps.DB.Exec(ctx, `
			DELETE FROM cron_jobs c
			USING services s, environments e
			WHERE c.id = $1
			  AND c.service_id = s.id
			  AND s.environment_id = e.id
			  AND e.project_id = $2
		`, jobID, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete cron job: %v", err)), nil
		}

		if tag.RowsAffected() == 0 {
			return mcp.NewToolResultError("cron job not found or does not belong to this project"), nil
		}

		data, err := json.Marshal(map[string]any{
			"deleted":      true,
			"job_id":       jobID,
			"project_name": projectName,
			"message":      "Cron job deleted successfully.",
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}
