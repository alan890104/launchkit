package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	projectsvc "github.com/alan890104/launchkit/server/internal/service/project"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterManage(srv *server.MCPServer, deps *Deps) {
	RegisterListProjects(srv, deps)
	RegisterDestroy(srv, deps)
	RegisterScale(srv, deps)
	RegisterRestart(srv, deps)
	RegisterUsage(srv, deps)
	RegisterRollback(srv, deps)
	RegisterRedeploy(srv, deps)
	RegisterUpdateEnv(srv, deps)
	RegisterCancelDeployment(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// list_projects
// ─────────────────────────────────────────────────────────────────────────────

func RegisterListProjects(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("list_projects",
			mcp.WithDescription(
				"List all projects for the current user with status, service count, and monthly cost.",
			),
		),
		makeListProjectsHandler(deps),
	)
}

func makeListProjectsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rows, err := deps.DB.Query(ctx, `
			SELECT
				p.name,
				p.region,
				COUNT(DISTINCT s.id) FILTER (WHERE s.id IS NOT NULL) as services_count,
				COUNT(DISTINCT e.id) FILTER (WHERE e.id IS NOT NULL) as environments_count,
				MAX(d.created_at) FILTER (WHERE d.id IS NOT NULL) as last_deployed,
				COALESCE((
					SELECT SUM(ur.cost) FROM usage_records ur
					WHERE ur.project_id = p.id
					  AND ur.period_start >= date_trunc('month', NOW())
				), 0) as monthly_cost
			FROM projects p
			LEFT JOIN environments e ON p.id = e.project_id
			LEFT JOIN services s ON e.id = s.environment_id
			LEFT JOIN deployments d ON s.id = d.service_id
			WHERE p.team_id = ANY($1)
			GROUP BY p.id, p.name, p.region
			ORDER BY p.name
		`, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query projects: %v", err)), nil
		}
		defer rows.Close()

		type projectInfo struct {
			Name          string  `json:"name"`
			Region        string  `json:"region"`
			ServicesCount int     `json:"services_count"`
			Environments  int     `json:"environments_count"`
			MonthlyCost   float64 `json:"monthly_cost"`
			LastDeployed  *string `json:"last_deployed,omitempty"`
			Status        string  `json:"status"`
		}

		var projects []projectInfo
		for rows.Next() {
			var p projectInfo
			var lastDeployed *time.Time
			if err := rows.Scan(&p.Name, &p.Region, &p.ServicesCount, &p.Environments, &lastDeployed, &p.MonthlyCost); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan project: %v", err)), nil
			}
			if lastDeployed != nil {
				ts := lastDeployed.UTC().Format(time.RFC3339)
				p.LastDeployed = &ts
			}
			// Status based on service health
			p.Status = "healthy"
			if p.ServicesCount == 0 {
				p.Status = "idle"
			}
			projects = append(projects, p)
		}
		if err := rows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate projects: %v", err)), nil
		}

		if len(projects) == 0 {
			empty, _ := json.Marshal(map[string]any{
				"projects": []any{},
				"message":  "No projects found. Create one with plan_deployment + deploy_project.",
			})
			return mcp.NewToolResultText(string(empty)), nil
		}

		// Query total monthly cost across ALL teams the user belongs to.
		// Uses period_start (not recorded_at) to avoid month-boundary misattribution.
		var totalMonthlyCost float64
		_ = deps.DB.QueryRow(ctx, `
			SELECT COALESCE(SUM(ur.cost), 0)
			FROM usage_records ur
			WHERE ur.team_id = ANY($1)
			  AND ur.period_start >= date_trunc('month', NOW())
		`, scope.TeamIDs).Scan(&totalMonthlyCost)

		data, err := json.Marshal(map[string]any{
			"projects":           projects,
			"total_projects":     len(projects),
			"total_monthly_cost": totalMonthlyCost,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// destroy
// ─────────────────────────────────────────────────────────────────────────────

func RegisterDestroy(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("destroy",
			mcp.WithDescription(
				"Permanently delete a project and all its resources (services, databases, domains). "+
					"This action is irreversible. Requires confirm=true.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name to destroy"),
			),
			mcp.WithBoolean("confirm",
				mcp.Required(),
				mcp.Description("Must be true to confirm destruction"),
			),
		),
		makeDestroyHandler(deps),
	)
}

func makeDestroyHandler(deps *Deps) server.ToolHandlerFunc {
	svc := buildProjectService(deps)
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		confirm, ok := args["confirm"].(bool)
		if !confirm {
			return mcp.NewToolResultError("confirm must be true to destroy a project"), nil
		}

		// Get project ID and all related resources
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		destroyed, err := svc.Destroy(ctx, projectsvc.DestroyInput{
			ProjectID: projectID,
			Confirm:   confirm,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, _ := json.Marshal(map[string]any{
			"destroyed":         true,
			"project":           project,
			"resources_cleaned": destroyed,
			"message":           "Project and all resources have been permanently deleted.",
		})
		return mcp.NewToolResultText(string(data)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// scale
// ─────────────────────────────────────────────────────────────────────────────

func RegisterScale(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("scale",
			mcp.WithDescription(
				"Scale a service's resources (min/max instances, CPU, memory). "+
					"Changes take effect immediately with zero downtime.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Required(),
				mcp.Description("The service name"),
			),
			mcp.WithObject("config",
				mcp.Required(),
				mcp.Description("Scaling configuration"),
			),
		),
		makeScaleHandler(deps),
	)
}

func makeScaleHandler(deps *Deps) server.ToolHandlerFunc {
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

		configRaw, ok := args["config"].(map[string]any)
		if !ok {
			return mcp.NewToolResultError("config is required and must be an object"), nil
		}

		// Get project ID for authenticated user first.
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		minInstances := 0
		maxInstances := 10
		cpu := ""
		memory := ""

		if v, ok := configRaw["min_instances"].(float64); ok {
			minInstances = int(v)
		}
		if v, ok := configRaw["max_instances"].(float64); ok {
			maxInstances = int(v)
		}
		if v, ok := configRaw["cpu"].(string); ok {
			cpu = v
		}
		if v, ok := configRaw["memory"].(string); ok {
			memory = v
		}

		scaled, err := svc.Scale(ctx, projectsvc.ScaleInput{
			ProjectID:    projectID,
			ServiceName:  service,
			MinInstances: minInstances,
			MaxInstances: maxInstances,
			CPU:          cpu,
			Memory:       memory,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, _ := json.Marshal(map[string]any{
			"status":  "scaled",
			"project": project,
			"service": service,
			"config": map[string]any{
				"min_instances": scaled.Applied.MinInstances,
				"max_instances": scaled.Applied.MaxInstances,
				"cpu":           scaled.Applied.CPU,
				"memory":        scaled.Applied.Memory,
			},
			"message": "Service scaled successfully. Changes take effect immediately.",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// restart
// ─────────────────────────────────────────────────────────────────────────────

func RegisterRestart(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("restart",
			mcp.WithDescription(
				"Restart a service (forces a new revision deployment with zero downtime). "+
					"Useful for applying configuration changes or recovering from issues.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Required(),
				mcp.Description("The service name"),
			),
		),
		makeRestartHandler(deps),
	)
}

func makeRestartHandler(deps *Deps) server.ToolHandlerFunc {
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

		// Get project ID for authenticated user first.
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		_, err = svc.Restart(ctx, projectsvc.RestartInput{
			ProjectID:   projectID,
			ServiceName: service,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, _ := json.Marshal(map[string]any{
			"status":             "restarting",
			"project":            project,
			"service":            service,
			"estimated_duration": "30-60s",
			"message":            "Service restart initiated. New revision will deploy with zero downtime.",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func buildProjectService(deps *Deps) *projectsvc.Service {
	return projectsvc.New(deps.DB, deps.Compute, deps.Config)
}

// ─────────────────────────────────────────────────────────────────────────────
// usage
// ─────────────────────────────────────────────────────────────────────────────

func RegisterUsage(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("usage",
			mcp.WithDescription(
				"Get current usage and billing information for the current user.",
			),
		),
		makeUsageHandler(deps),
	)
}

func makeUsageHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		type usageInfo struct {
			TotalProjects    int     `json:"total_projects"`
			TotalServices    int     `json:"total_services"`
			TotalDeployments int     `json:"total_deployments"`
			ActiveDatabases  int     `json:"active_databases"`
			CurrentMonthCost float64 `json:"current_month_cost"`
			LastUpdated      string  `json:"last_updated"`
		}

		var usage usageInfo

		_ = deps.DB.QueryRow(ctx, `
			SELECT COUNT(DISTINCT p.id) FROM projects p
			WHERE p.team_id = ANY($1)
		`, scope.TeamIDs).Scan(&usage.TotalProjects)

		_ = deps.DB.QueryRow(ctx, `
			SELECT COUNT(DISTINCT s.id) FROM services s
			JOIN environments e ON s.environment_id = e.id
			JOIN projects p ON e.project_id = p.id
			WHERE p.team_id = ANY($1)
		`, scope.TeamIDs).Scan(&usage.TotalServices)

		_ = deps.DB.QueryRow(ctx, `
			SELECT COUNT(DISTINCT d.id) FROM deployments d
			JOIN services s ON d.service_id = s.id
			JOIN environments e ON s.environment_id = e.id
			JOIN projects p ON e.project_id = p.id
			WHERE p.team_id = ANY($1) AND d.created_at >= date_trunc('month', NOW())
		`, scope.TeamIDs).Scan(&usage.TotalDeployments)

		_ = deps.DB.QueryRow(ctx, `
			SELECT COUNT(DISTINCT r.id) FROM resources r
			JOIN environments e ON r.environment_id = e.id
			JOIN projects p ON e.project_id = p.id
			WHERE p.team_id = ANY($1) AND r.type = 'postgres' AND r.status = 'active'
		`, scope.TeamIDs).Scan(&usage.ActiveDatabases)

		// Query real billing data across ALL teams the user belongs to.
		// Uses period_start (not recorded_at) to avoid month-boundary misattribution.
		_ = deps.DB.QueryRow(ctx, `
			SELECT COALESCE(SUM(ur.cost), 0)
			FROM usage_records ur
			WHERE ur.team_id = ANY($1)
			  AND ur.period_start >= date_trunc('month', NOW())
		`, scope.TeamIDs).Scan(&usage.CurrentMonthCost)

		// Get total balance across all teams
		var balance float64
		_ = deps.DB.QueryRow(ctx, `
			SELECT COALESCE(SUM(t.balance), 0)
			FROM teams t
			WHERE t.id = ANY($1)
		`, scope.TeamIDs).Scan(&balance)

		// Cost breakdown by resource type (all teams)
		type costBreakdown struct {
			ResourceType string  `json:"resource_type"`
			Cost         float64 `json:"cost"`
		}
		var breakdown []costBreakdown
		rows, err := deps.DB.Query(ctx, `
			SELECT ur.resource_type, COALESCE(SUM(ur.cost), 0) as total_cost
			FROM usage_records ur
			WHERE ur.team_id = ANY($1)
			  AND ur.period_start >= date_trunc('month', NOW())
			GROUP BY ur.resource_type
			ORDER BY total_cost DESC
		`, scope.TeamIDs)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var b costBreakdown
				if scanErr := rows.Scan(&b.ResourceType, &b.Cost); scanErr == nil {
					breakdown = append(breakdown, b)
				}
			}
		}

		// Show last metering run time instead of NOW() (audit finding C1)
		var lastMetered *time.Time
		_ = deps.DB.QueryRow(ctx,
			"SELECT MAX(finished_at) FROM metering_runs WHERE status IN ('completed','partial')",
		).Scan(&lastMetered)
		if lastMetered != nil {
			usage.LastUpdated = lastMetered.UTC().Format(time.RFC3339)
		} else {
			usage.LastUpdated = time.Now().UTC().Format(time.RFC3339)
		}

		result, _ := json.Marshal(map[string]any{
			"usage": usage,
			"billing": map[string]any{
				"current_month_cost": usage.CurrentMonthCost,
				"balance":            balance,
				"currency":           "USD",
				"cost_breakdown":     breakdown,
				"note":               "Costs are updated hourly. Last metered: " + usage.LastUpdated,
			},
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// update_env
// ─────────────────────────────────────────────────────────────────────────────

func RegisterUpdateEnv(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("update_env",
			mcp.WithDescription(
				"Update an environment variable on a running service. "+
					"Stores the value and triggers a service restart to pick up the change. "+
					"For sensitive values, set sensitive=true to store encrypted in the secrets table.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service_name",
				mcp.Required(),
				mcp.Description("The service name"),
			),
			mcp.WithString("key",
				mcp.Required(),
				mcp.Description("The environment variable name"),
			),
			mcp.WithString("value",
				mcp.Required(),
				mcp.Description("The environment variable value"),
			),
			mcp.WithString("environment",
				mcp.Description("Target environment (defaults to \"production\")"),
			),
			mcp.WithBoolean("sensitive",
				mcp.Description("If true, store in secrets table instead of env_bindings (default false)"),
			),
		),
		makeUpdateEnvHandler(deps),
	)
}

func makeUpdateEnvHandler(deps *Deps) server.ToolHandlerFunc {
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

		key, ok := args["key"].(string)
		if !ok || key == "" {
			return mcp.NewToolResultError("key is required"), nil
		}
		if err := deploy.ValidateEnvKeyForUpdate(key); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid env key: %v", err)), nil
		}

		value, ok := args["value"].(string)
		if !ok {
			return mcp.NewToolResultError("value is required"), nil
		}

		environment := "production"
		if env, ok := args["environment"].(string); ok && env != "" {
			environment = env
		}

		sensitive, _ := args["sensitive"].(bool)

		// Auth: resolve project via scoped membership
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Look up the service and its target/image for the restart
		var serviceID, target, imageURI, envID string
		err = deps.DB.QueryRow(ctx, `
			SELECT s.id, s.target, COALESCE(d.image_uri, ''), s.environment_id
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			JOIN projects p ON e.project_id = p.id
			LEFT JOIN deployments d ON s.id = d.service_id AND d.status = 'live'
			WHERE p.id = $1 AND s.name = $2 AND e.name = $3
		`, projectID, serviceName, environment).Scan(&serviceID, &target, &imageURI, &envID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("service %q not found in environment %q: %v", serviceName, environment, err)), nil
		}

		// Store the env var
		if sensitive {
			// Store in secrets table (encrypted storage — Tink integration pending)
			err = deploy.SetSecretValues(ctx, deps.DB, projectID, map[string]string{key: value}, deps.Enc)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store secret: %v", err)), nil
			}

			// Also create an env_binding that references the secret
			_, err = deps.DB.Exec(ctx, `
				INSERT INTO env_bindings (id, service_id, key, secret_id, source)
				VALUES (
					gen_random_uuid()::TEXT,
					$1, $2,
					(SELECT id FROM secrets WHERE project_id = $3 AND name = $2),
					'user_input'
				)
				ON CONFLICT (service_id, key) DO UPDATE SET
					secret_id = EXCLUDED.secret_id,
					value = NULL,
					source = 'user_input'
			`, serviceID, key, projectID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("bind secret to service: %v", err)), nil
			}
		} else {
			// Store in env_bindings table directly
			_, err = deps.DB.Exec(ctx, `
				INSERT INTO env_bindings (id, service_id, key, value, source)
				VALUES (gen_random_uuid()::TEXT, $1, $2, $3, 'user_input')
				ON CONFLICT (service_id, key) DO UPDATE SET
					value = EXCLUDED.value,
					secret_id = NULL,
					source = 'user_input'
			`, serviceID, key, value)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("store env binding: %v", err)), nil
			}
		}

		slog.Info("env var updated",
			"project", projectName,
			"service", serviceName,
			"key", key,
			"sensitive", sensitive,
			"environment", environment,
		)
		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "update_env", "service", serviceID, map[string]any{
			"key":         key,
			"sensitive":   sensitive,
			"environment": environment,
		})

		// Restart the service to pick up the new env var.
		// Cloud Run / ECS require a new revision for env changes.
		restarted := false
		if deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
			if deps.Compute == nil {
				return mcp.NewToolResultError("cloud provider not configured — set GCP_PROJECT_ID or AWS_ACCOUNT_ID"), nil
			}
			cloudServiceName := cloudServiceName(projectID, envID, serviceName)

			// Gather all env vars for this service to pass to the deploy
			envRows, err := deps.DB.Query(ctx, `
				SELECT eb.key, COALESCE(eb.value, '') as val, COALESCE(s.name, '') as secret_name
				FROM env_bindings eb
				LEFT JOIN secrets s ON eb.secret_id = s.id
				WHERE eb.service_id = $1
			`, serviceID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("query env bindings: %v", err)), nil
			}
			defer envRows.Close()

			envVars := make(map[string]string)
			var hasSecrets bool
			for envRows.Next() {
				var k, v, secretName string
				if err := envRows.Scan(&k, &v, &secretName); err != nil {
					continue
				}
				if secretName != "" {
					hasSecrets = true
				} else {
					envVars[k] = v
				}
			}

			// Resolve secrets and merge into envVars
			if hasSecrets {
				secrets, resolveErr := deploy.ResolveUserSecrets(ctx, deps.DB, projectID, deps.Enc)
				if resolveErr != nil {
					slog.Warn("failed to resolve secrets for restart", "error", resolveErr)
				} else {
					for k, v := range secrets {
						envVars[k] = v
					}
				}
			}

			_, err = deps.Compute.Deploy(ctx, provider.DeployOpts{
				ServiceName: cloudServiceName,
				Region:      deps.Config.Region(),
				ImageURI:    imageURI,
				EnvVars:     envVars,
			})
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("restart service after env update: %v", err)), nil
			}
			restarted = true
		}

		result, _ := json.Marshal(map[string]any{
			"status":      "updated",
			"project":     projectName,
			"service":     serviceName,
			"environment": environment,
			"key":         key,
			"sensitive":   sensitive,
			"restarted":   restarted,
			"message":     "Environment variable updated. Service restarted to apply the change.",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// cancel_deployment
// ─────────────────────────────────────────────────────────────────────────────

func RegisterCancelDeployment(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("cancel_deployment",
			mcp.WithDescription(
				"Cancel an in-progress or queued deployment. "+
					"If the deployment is already running, the worker is asked to stop gracefully. "+
					"Terminal deployments (live, failed, cancelled) cannot be cancelled.",
			),
			mcp.WithString("deployment_id",
				mcp.Required(),
				mcp.Description("The deployment ID returned by deploy_project"),
			),
		),
		makeCancelDeploymentHandler(deps),
	)
}

func makeCancelDeploymentHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		deploymentID, _ := request.GetArguments()["deployment_id"].(string)
		if deploymentID == "" {
			return mcp.NewToolResultError("deployment_id is required"), nil
		}

		// Look up the project ID for this deployment.
		var projectID string
		err := deps.DB.QueryRow(ctx, `
			SELECT e.project_id
			FROM deployments d
			JOIN services s ON d.service_id = s.id
			JOIN environments e ON s.environment_id = e.id
			WHERE d.id = $1 AND e.project_id IN (
				SELECT p.id FROM projects p WHERE p.team_id = ANY($2)
			)
		`, deploymentID, scope.TeamIDs).Scan(&projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("deployment %q not found", deploymentID)), nil
		}

		svc := buildProjectService(deps)
		_, err = svc.Cancel(ctx, projectsvc.CancelInput{
			ProjectID:    projectID,
			DeploymentID: deploymentID,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Also ask River to cancel the job if it exists.
		var riverJobID *int64
		var status string
		_ = deps.DB.QueryRow(ctx,
			"SELECT status, river_job_id FROM deployments WHERE id = $1",
			deploymentID,
		).Scan(&status, &riverJobID)

		if riverJobID != nil && deps.RiverClient != nil {
			if _, cancelErr := deps.RiverClient.JobCancel(ctx, *riverJobID); cancelErr != nil {
				slog.Warn("river JobCancel returned error", "job_id", *riverJobID, "error", cancelErr)
			}
		}

		result, _ := json.Marshal(map[string]any{
			"status":        "cancelled",
			"deployment_id": deploymentID,
			"message":       "Cancellation requested. In-progress builds may take a few seconds to stop.",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}
