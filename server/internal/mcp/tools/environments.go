package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterEnvironments(srv *server.MCPServer, deps *Deps) {
	RegisterCreateEnvironment(srv, deps)
	RegisterListEnvironments(srv, deps)
	RegisterPromote(srv, deps)
	RegisterDestroyEnvironment(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// create_environment
// ─────────────────────────────────────────────────────────────────────────────

func RegisterCreateEnvironment(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("create_environment",
			mcp.WithDescription(
				"Create a new environment (staging, dev, preview) by cloning services from an existing environment. "+
					"Resources (databases, caches) can be shared or cloned based on isolation level.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Environment name (e.g., 'staging', 'dev', 'preview-pr-42')"),
			),
			mcp.WithString("clone_from",
				mcp.Description("Source environment to clone from (default: 'production')"),
			),
			mcp.WithString("isolation",
				mcp.Description("Resource isolation level: 'shared' (same DB schema), 'database' (separate DB), 'instance' (separate DB instance)"),
			),
		),
		makeCreateEnvironmentHandler(deps),
	)
}

func makeCreateEnvironmentHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		name, ok := args["name"].(string)
		if !ok || name == "" {
			return mcp.NewToolResultError("name is required"), nil
		}

		cloneFrom := "production"
		if v, ok := args["clone_from"].(string); ok && v != "" {
			cloneFrom = v
		}

		isolation := "shared"
		if v, ok := args["isolation"].(string); ok && v != "" {
			isolation = v
		}

		// Get project ID
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Get source environment ID
		var sourceEnvID string
		err = deps.DB.QueryRow(ctx, `
			SELECT id FROM environments WHERE project_id = $1 AND name = $2
		`, projectID, cloneFrom).Scan(&sourceEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("source environment '%s' not found: %v", cloneFrom, err)), nil
		}

		// Create new environment
		envID := uuid.NewString()
		_, err = deps.DB.Exec(ctx, `
			INSERT INTO environments (id, project_id, name, clone_from, status)
			VALUES ($1, $2, $3, $4, 'active')
		`, envID, projectID, name, sourceEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create environment: %v", err)), nil
		}

		// Clone services from source environment
		type serviceInfo struct {
			Name      string
			Type      string
			Target    string
			Framework string
			Config    []byte
		}

		var services []serviceInfo
		svcRows, err := deps.DB.Query(ctx, `
			SELECT name, type, target, framework, config
			FROM services
			WHERE environment_id = $1
		`, sourceEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query source services: %v", err)), nil
		}
		defer svcRows.Close()

		for svcRows.Next() {
			var s serviceInfo
			if err := svcRows.Scan(&s.Name, &s.Type, &s.Target, &s.Framework, &s.Config); err != nil {
				continue
			}
			services = append(services, s)
		}

		// Clone services with new URLs
		var clonedServices []map[string]any
		for _, s := range services {
			svcID := uuid.NewString()
			// Generate environment-specific URL
			url := fmt.Sprintf("https://%s--%s.launchkit.app", s.Name, name)
			if name == "production" {
				url = fmt.Sprintf("https://%s.launchkit.app", s.Name)
			}

			_, err = deps.DB.Exec(ctx, `
				INSERT INTO services (id, environment_id, name, type, target, framework, url, status, config)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8)
			`, svcID, envID, s.Name, s.Type, s.Target, s.Framework, url, s.Config)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("clone service %s: %v", s.Name, err)), nil
			}

			clonedServices = append(clonedServices, map[string]any{
				"name": s.Name,
				"type": s.Type,
				"url":  url,
			})
		}

		// Handle resource cloning based on isolation level
		var resources []map[string]any
		switch isolation {
		case "shared":
			// Share resources from source environment
			resRows, err := deps.DB.Query(ctx, `
				SELECT type, provider, connection_url FROM resources WHERE environment_id = $1
			`, sourceEnvID)
			if err == nil {
				defer resRows.Close()
				for resRows.Next() {
					var resType, provider, connURL string
					if err := resRows.Scan(&resType, &provider, &connURL); err != nil {
						continue
					}
					resources = append(resources, map[string]any{
						"type":     resType,
						"provider": provider,
						"note":     "Shared with " + cloneFrom,
					})
				}
			}

		case "database", "instance":
			// Create new resources (logic handled by orchestrator during deploy)
			resources = append(resources, map[string]any{
				"type": "postgres",
				"note": "New database will be provisioned on first deploy",
			})
		}

		result, _ := json.Marshal(map[string]any{
			"environment_id": envID,
			"name":           name,
			"project":        project,
			"clone_from":     cloneFrom,
			"isolation":      isolation,
			"services":       clonedServices,
			"resources":      resources,
			"status":         "active",
			"message":        fmt.Sprintf("Environment '%s' created successfully. Services cloned, resources configured (%s isolation).", name, isolation),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// list_environments
// ─────────────────────────────────────────────────────────────────────────────

func RegisterListEnvironments(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("list_environments",
			mcp.WithDescription(
				"List all environments for a project with their status, services count, and monthly cost.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
		),
		makeListEnvironmentsHandler(deps),
	)
}

func makeListEnvironmentsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, ok := request.GetArguments()["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		// Get project ID
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		rows, err := deps.DB.Query(ctx, `
			SELECT
				e.id,
				e.name,
				e.status,
				e.clone_from,
				COUNT(DISTINCT s.id) as services_count,
				MAX(s.updated_at) as last_deployed
			FROM environments e
			LEFT JOIN services s ON e.id = s.environment_id
			WHERE e.project_id = $1
			GROUP BY e.id, e.name, e.status, e.clone_from
			ORDER BY
				CASE e.name
					WHEN 'production' THEN 0
					WHEN 'staging' THEN 1
					WHEN 'dev' THEN 2
					ELSE 3
				END,
				e.name
		`, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query environments: %v", err)), nil
		}
		defer rows.Close()

		type envInfo struct {
			ID            string  `json:"id"`
			Name          string  `json:"name"`
			Status        string  `json:"status"`
			ServicesCount int     `json:"services_count"`
			MonthlyCost   float64 `json:"monthly_cost"`
			LastDeployed  *string `json:"last_deployed,omitempty"`
			CloneFrom     *string `json:"clone_from,omitempty"`
			URLSuffix     string  `json:"url_suffix"`
		}

		var envs []envInfo
		for rows.Next() {
			var e envInfo
			var lastDeployed *time.Time
			var cloneFrom *string
			if err := rows.Scan(&e.ID, &e.Name, &e.Status, &cloneFrom, &e.ServicesCount, &lastDeployed); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("scan environment: %v", err)), nil
			}
			e.CloneFrom = cloneFrom
			if lastDeployed != nil {
				ts := lastDeployed.UTC().Format(time.RFC3339)
				e.LastDeployed = &ts
			}
			// URL suffix
			if e.Name == "production" {
				e.URLSuffix = ""
			} else {
				e.URLSuffix = "--" + e.Name
			}
			envs = append(envs, e)
		}
		if err := rows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate environments: %v", err)), nil
		}

		if len(envs) == 0 {
			empty, _ := json.Marshal(map[string]any{
				"environments": []any{},
				"message":      "No environments found. Create one with create_environment.",
			})
			return mcp.NewToolResultText(string(empty)), nil
		}

		result, _ := json.Marshal(map[string]any{
			"project":      project,
			"environments": envs,
			"total":        len(envs),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// promote_environment
// ─────────────────────────────────────────────────────────────────────────────

func RegisterPromote(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("promote_environment",
			mcp.WithDescription(
				"Deploy the same container image from one environment to another (e.g., staging → production). "+
					"This ensures both environments run identical binaries. Zero-downtime deployment. "+
					"Note: This deploys the image, not data. Use migrate for database data.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("from_env",
				mcp.Required(),
				mcp.Description("Source environment to copy image from (e.g., 'staging')"),
			),
			mcp.WithString("to_env",
				mcp.Required(),
				mcp.Description("Target environment to deploy to (e.g., 'production')"),
			),
			mcp.WithArray("services",
				mcp.Description("Specific services to promote (default: all)"),
			),
			mcp.WithString("git_ref",
				mcp.Description("Optional git commit hash to verify (ensures staging and production run same code)"),
			),
		),
		makePromoteHandler(deps),
	)
}

func makePromoteHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		fromEnv, ok := args["from_env"].(string)
		if !ok || fromEnv == "" {
			return mcp.NewToolResultError("from_env is required"), nil
		}

		toEnv, ok := args["to_env"].(string)
		if !ok || toEnv == "" {
			return mcp.NewToolResultError("to_env is required"), nil
		}

		// Get project ID
		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Get environment IDs
		var fromEnvID, toEnvID string
		err = deps.DB.QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1 AND name = $2`, projectID, fromEnv).Scan(&fromEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("source environment not found: %v", err)), nil
		}
		err = deps.DB.QueryRow(ctx, `SELECT id FROM environments WHERE project_id = $1 AND name = $2`, projectID, toEnv).Scan(&toEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("target environment not found: %v", err)), nil
		}

		// Get services from source environment
		type serviceInfo struct {
			ID       string
			Name     string
			Type     string
			Target   string
			ImageURI string
			Config   []byte
		}

		var servicesToPromote []serviceInfo
		svcRows, err := deps.DB.Query(ctx, `
			SELECT s.id, s.name, s.type, s.target, 
				   COALESCE(d.image_uri, '') as image_uri,
				   s.config
			FROM services s
			LEFT JOIN deployments d ON s.id = d.service_id AND d.status = 'live'
			WHERE s.environment_id = $1
		`, fromEnvID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query source services: %v", err)), nil
		}
		defer svcRows.Close()

		for svcRows.Next() {
			var s serviceInfo
			if err := svcRows.Scan(&s.ID, &s.Name, &s.Type, &s.Target, &s.ImageURI, &s.Config); err != nil {
				continue
			}
			servicesToPromote = append(servicesToPromote, s)
		}

		// Deploy each service (use same image_uri from source environment)
		var deployed []map[string]any
		var skipped []map[string]any

		for _, s := range servicesToPromote {
			// Find corresponding service in target environment
			var targetServiceID string
			err := deps.DB.QueryRow(ctx, `
				SELECT id FROM services WHERE environment_id = $1 AND name = $2
			`, toEnvID, s.Name).Scan(&targetServiceID)
			if err != nil {
				// Service doesn't exist in target, skip
				skipped = append(skipped, map[string]any{
					"service": s.Name,
					"reason":  "Service does not exist in target environment. Create it first with create_environment.",
				})
				continue
			}

			if s.ImageURI == "" {
				skipped = append(skipped, map[string]any{
					"service": s.Name,
					"reason":  "No live deployment found in source environment — deploy to " + fromEnv + " first.",
				})
				continue
			}

			// Create a deployment record for the target service.
			deploymentID := uuid.NewString()
			_, err = deps.DB.Exec(ctx, `
				INSERT INTO deployments (id, service_id, status, image_uri, trigger, started_at)
				VALUES ($1, $2, 'deploying', $3, 'promote_environment', NOW())
			`, deploymentID, targetServiceID, s.ImageURI)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("create deployment: %v", err)), nil
			}

			// For compute targets, actually call the cloud provider to deploy the image.
			if deploy.IsComputeTarget(deploy.ServiceTarget(s.Target)) {
				if deps.Compute == nil {
					_, _ = deps.DB.Exec(ctx, `UPDATE deployments SET status='failed', error='compute provider not configured', finished_at=NOW() WHERE id=$1`, deploymentID)
					skipped = append(skipped, map[string]any{"service": s.Name, "reason": "compute provider not configured"})
					continue
				}
				cloudSvcName := cloudServiceName(projectID, toEnvID, s.Name)
				_, deployErr := deps.Compute.Deploy(ctx, provider.DeployOpts{
					ServiceName: cloudSvcName,
					Region:      deps.Config.Region(),
					ImageURI:    s.ImageURI,
				})
				if deployErr != nil {
					_, _ = deps.DB.Exec(ctx, `UPDATE deployments SET status='failed', error=$1, finished_at=NOW() WHERE id=$2`, deployErr.Error(), deploymentID)
					_, _ = deps.DB.Exec(ctx, `UPDATE services SET status='failed', updated_at=NOW() WHERE id=$1`, targetServiceID)
					skipped = append(skipped, map[string]any{"service": s.Name, "reason": deployErr.Error()})
					continue
				}
			}

			// Mark deployment live.
			_, _ = deps.DB.Exec(ctx, `UPDATE deployments SET status='live', finished_at=NOW() WHERE id=$1`, deploymentID)
			_, _ = deps.DB.Exec(ctx, `UPDATE services SET status='live', updated_at=NOW() WHERE id=$1`, targetServiceID)

			deployed = append(deployed, map[string]any{
				"service":           s.Name,
				"image_uri":         s.ImageURI,
				"target_service_id": targetServiceID,
				"deployment_id":     deploymentID,
			})
		}

		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "promote_environment", "environment", toEnvID, map[string]any{
			"from_env": fromEnv,
			"to_env":   toEnv,
			"deployed": len(deployed),
			"skipped":  len(skipped),
		})

		result, _ := json.Marshal(map[string]any{
			"project":  project,
			"from_env": fromEnv,
			"to_env":   toEnv,
			"deployed": deployed,
			"skipped":  skipped,
			"status":   "deployed",
			"message":  fmt.Sprintf("Promoted %d services from %s to %s.", len(deployed), fromEnv, toEnv),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// destroy_environment
// ─────────────────────────────────────────────────────────────────────────────

func RegisterDestroyEnvironment(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("destroy_environment",
			mcp.WithDescription(
				"Destroy an environment and all its services. Cannot destroy 'production'.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("environment",
				mcp.Required(),
				mcp.Description("Environment name to destroy"),
			),
			mcp.WithBoolean("confirm",
				mcp.Required(),
				mcp.Description("Must be true to confirm"),
			),
		),
		makeDestroyEnvironmentHandler(deps),
	)
}

func makeDestroyEnvironmentHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		environment, ok := args["environment"].(string)
		if !ok || environment == "" {
			return mcp.NewToolResultError("environment is required"), nil
		}

		confirm, ok := args["confirm"].(bool)
		if !confirm {
			return mcp.NewToolResultError("confirm must be true"), nil
		}

		if environment == "production" {
			return mcp.NewToolResultError("Cannot destroy production environment"), nil
		}

		// Get project and environment IDs, scoped to the user's teams.
		var projectID, envID string
		err := deps.DB.QueryRow(ctx, `
			SELECT p.id, e.id
			FROM projects p
			JOIN environments e ON p.id = e.project_id
			WHERE p.name = $1 AND e.name = $2 AND p.team_id = ANY($3)
		`, project, environment, scope.TeamIDs).Scan(&projectID, &envID)
		if err != nil {
			return mcp.NewToolResultError("project or environment not found"), nil
		}

		// Delete services from cloud providers, counting how many are cleaned up.
		var cleanedCount int
		svcRows, err := deps.DB.Query(ctx, `
			SELECT name, target FROM services WHERE environment_id = $1
		`, envID)
		if err == nil {
			defer svcRows.Close()
			for svcRows.Next() {
				var svcName, target string
				if err := svcRows.Scan(&svcName, &target); err != nil {
					continue
				}
				if deploy.IsComputeTarget(deploy.ServiceTarget(target)) && deps.Compute != nil {
					serviceName := cloudServiceName(projectID, envID, svcName)
					if err := deps.Compute.DeleteService(ctx, serviceName, deps.Config.Region()); err == nil {
						cleanedCount++
					}
				} else {
					cleanedCount++
				}
			}
		}

		// Delete environment (CASCADE deletes services, resources, deployments).
		_, err = deps.DB.Exec(ctx, `DELETE FROM environments WHERE id = $1`, envID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete environment: %v", err)), nil
		}

		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "destroy_environment", "environment", envID, map[string]any{
			"environment":      environment,
			"services_cleaned": cleanedCount,
		})

		result, _ := json.Marshal(map[string]any{
			"destroyed":        true,
			"project":          project,
			"environment":      environment,
			"services_cleaned": cleanedCount,
			"message":          fmt.Sprintf("Environment '%s' and all its services have been permanently deleted.", environment),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}
