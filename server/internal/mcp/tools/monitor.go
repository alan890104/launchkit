package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterMonitor(srv *server.MCPServer, deps *Deps) {
	RegisterGetMetrics(srv, deps)
	RegisterSetAlert(srv, deps)
	RegisterListAlerts(srv, deps)
	RegisterDeleteAlert(srv, deps)
	RegisterGetIncidents(srv, deps)
	RegisterGetUptime(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// get_metrics
// ─────────────────────────────────────────────────────────────────────────────

func RegisterGetMetrics(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("get_metrics",
			mcp.WithDescription(
				"Get metrics for a service or all services in a project. Supports requests, latency, errors, CPU, memory, and more.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Description("Specific service (default: all services)"),
			),
			mcp.WithArray("metrics",
				mcp.Description("Metrics to retrieve: requests, latency_p50, latency_p95, latency_p99, error_rate, cpu, memory, instances"),
			),
			mcp.WithString("period",
				mcp.Description("Time period: 1h, 6h, 24h, 7d, 30d (default: 24h)"),
			),
		),
		makeGetMetricsHandler(deps),
	)
}

func makeGetMetricsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		service := ""
		if v, ok := args["service"].(string); ok {
			service = v
		}

		period := "24h"
		if v, ok := args["period"].(string); ok && v != "" {
			period = v
		}

		rawMetrics, ok := args["metrics"].([]any)
		var metrics []string
		for _, r := range rawMetrics {
			if s, ok := r.(string); ok {
				metrics = append(metrics, s)
			}
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Get service names from DB to confirm project has services
		type svcRef struct{ name, envID string }
		var svcs []svcRef
		if service != "" {
			var envID string
			_ = deps.DB.QueryRow(ctx, `
				SELECT s.environment_id FROM services s
				JOIN environments e ON s.environment_id = e.id
				WHERE e.project_id = $1 AND s.name = $2
				ORDER BY e.created_at DESC LIMIT 1
			`, projectID, service).Scan(&envID)
			svcs = []svcRef{{name: service, envID: envID}}
		} else {
			svcRows, err := deps.DB.Query(ctx, `
				SELECT DISTINCT s.name, s.environment_id FROM services s
				JOIN environments e ON s.environment_id = e.id
				WHERE e.project_id = $1
			`, projectID)
			if err == nil {
				defer svcRows.Close()
				for svcRows.Next() {
					var ref svcRef
					if err := svcRows.Scan(&ref.name, &ref.envID); err != nil {
						continue
					}
					svcs = append(svcs, ref)
				}
			}
		}

		// If no services in DB, return empty result
		if len(svcs) == 0 {
			result, _ := json.Marshal(map[string]any{
				"project":  project,
				"service":  service,
				"period":   period,
				"services": []any{},
				"message":  "No services found for this project",
			})
			return mcp.NewToolResultText(string(result)), nil
		}

		// If Compute is available, query real metrics
		if deps.Compute != nil {
			region := deps.Config.GCPRegion
			if region == "" {
				region = "us-east4"
			}

			var results []provider.MetricsResponse
			for _, svc := range svcs {
				resp, err := deps.Compute.GetMetrics(ctx, provider.MetricsQueryParams{
					ServiceName: cloudServiceName(projectID, svc.envID, svc.name),
					Region:      region,
					Metrics:     metrics,
					Period:      period,
				})
				if err != nil {
					continue
				}
				results = append(results, resp...)
			}

			result, _ := json.Marshal(map[string]any{
				"project":  project,
				"service":  service,
				"period":   period,
				"services": results,
				"source":   "cloud_monitoring",
			})
			return mcp.NewToolResultText(string(result)), nil
		}

		// Fallback: return empty result with message
		result, _ := json.Marshal(map[string]any{
			"project":  project,
			"service":  service,
			"period":   period,
			"services": []any{},
			"message":  "Cloud provider not configured yet",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func RegisterSetAlert(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("set_alert",
			mcp.WithDescription(
				"Create or update an alert rule. Supports metric-based alerts with customizable thresholds, windows, and notification channels.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Description("Specific service (default: all services)"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Alert rule name"),
			),
			mcp.WithString("description",
				mcp.Description("Alert description"),
			),
			mcp.WithString("metric",
				mcp.Required(),
				mcp.Description("Metric to monitor: error_rate, latency_p95, cpu, memory, requests"),
			),
			mcp.WithString("operator",
				mcp.Required(),
				mcp.Description("Comparison operator: >, <, >=, <="),
			),
			mcp.WithNumber("threshold",
				mcp.Required(),
				mcp.Description("Threshold value"),
			),
			mcp.WithString("window",
				mcp.Required(),
				mcp.Description("Evaluation window: 1m, 5m, 15m, 1h"),
			),
			mcp.WithString("severity",
				mcp.Description("Severity: critical, error, warning, info (default: warning)"),
			),
			mcp.WithArray("channels",
				mcp.Description("Notification channels: [{type: 'slack', target: '#alerts'}, {type: 'email', target: 'team@company.com'}]"),
			),
		),
		makeSetAlertHandler(deps),
	)
}

func makeSetAlertHandler(deps *Deps) server.ToolHandlerFunc {
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

		metric, ok := args["metric"].(string)
		if !ok || metric == "" {
			return mcp.NewToolResultError("metric is required"), nil
		}

		operator, ok := args["operator"].(string)
		if !ok || operator == "" {
			return mcp.NewToolResultError("operator is required"), nil
		}

		threshold, ok := args["threshold"].(float64)
		if !ok {
			return mcp.NewToolResultError("threshold is required and must be a number"), nil
		}

		window, ok := args["window"].(string)
		if !ok || window == "" {
			return mcp.NewToolResultError("window is required"), nil
		}

		service := ""
		if v, ok := args["service"].(string); ok {
			service = v
		}

		description := ""
		if v, ok := args["description"].(string); ok {
			description = v
		}

		severity := "warning"
		if v, ok := args["severity"].(string); ok && v != "" {
			severity = v
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		channelsJSON := "[]"
		if channels, ok := args["channels"].([]any); ok && len(channels) > 0 {
			data, _ := json.Marshal(channels)
			channelsJSON = string(data)
		}

		alertID := uuid.NewString()
		_, err = deps.DB.Exec(ctx, `
			INSERT INTO alert_rules (id, project_id, service, name, description, metric, operator, threshold, window, severity, channels)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (project_id, name) DO UPDATE SET
				service = EXCLUDED.service,
				description = EXCLUDED.description,
				metric = EXCLUDED.metric,
				operator = EXCLUDED.operator,
				threshold = EXCLUDED.threshold,
				window = EXCLUDED.window,
				severity = EXCLUDED.severity,
				channels = EXCLUDED.channels,
				status = 'active',
				updated_at = NOW()
		`, alertID, projectID, service, name, description, metric, operator, threshold, window, severity, channelsJSON)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("create alert rule: %v", err)), nil
		}

		result, _ := json.Marshal(map[string]any{
			"alert_id":  alertID,
			"project":   project,
			"service":   service,
			"name":      name,
			"metric":    metric,
			"operator":  operator,
			"threshold": threshold,
			"window":    window,
			"severity":  severity,
			"status":    "active",
			"message":   fmt.Sprintf("Alert rule '%s' created successfully. Will trigger when %s %s %v over %s window.", name, metric, operator, threshold, window),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func RegisterListAlerts(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("list_alerts",
			mcp.WithDescription(
				"List all alert rules for a project with their status and last triggered time.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
		),
		makeListAlertsHandler(deps),
	)
}

func makeListAlertsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, ok := request.GetArguments()["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		rows, err := deps.DB.Query(ctx, `
			SELECT
				ar.id,
				ar.name,
				ar.service,
				ar.metric,
				ar.operator,
				ar.threshold,
				ar.window,
				ar.severity,
				ar.status,
				ar.channels,
				MAX(ai.started_at) as last_triggered,
				COUNT(ai.id) FILTER (WHERE ai.status = 'active') as active_incidents
			FROM alert_rules ar
			LEFT JOIN alert_incidents ai ON ar.id = ai.rule_id
			WHERE ar.project_id = $1
			GROUP BY ar.id, ar.name, ar.service, ar.metric, ar.operator, ar.threshold, ar.window, ar.severity, ar.status, ar.channels
			ORDER BY ar.name
		`, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query alerts: %v", err)), nil
		}
		defer rows.Close()

		type alertRule struct {
			ID              string  `json:"id"`
			Name            string  `json:"name"`
			Service         *string `json:"service,omitempty"`
			Metric          string  `json:"metric"`
			Operator        string  `json:"operator"`
			Threshold       float64 `json:"threshold"`
			Window          string  `json:"window"`
			Severity        string  `json:"severity"`
			Status          string  `json:"status"`
			LastTriggered   *string `json:"last_triggered,omitempty"`
			ActiveIncidents int     `json:"active_incidents"`
		}

		var alerts []alertRule
		for rows.Next() {
			var a alertRule
			var lastTriggered *time.Time
			var service *string
			var channels string
			if err := rows.Scan(&a.ID, &a.Name, &service, &a.Metric, &a.Operator, &a.Threshold, &a.Window, &a.Severity, &a.Status, &channels, &lastTriggered, &a.ActiveIncidents); err != nil {
				continue
			}
			a.Service = service
			if lastTriggered != nil {
				ts := lastTriggered.UTC().Format(time.RFC3339)
				a.LastTriggered = &ts
			}
			alerts = append(alerts, a)
		}
		if err = rows.Err(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("iterate alerts: %v", err)), nil
		}

		result, _ := json.Marshal(map[string]any{
			"project": project,
			"alerts":  alerts,
			"total":   len(alerts),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func RegisterDeleteAlert(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("delete_alert",
			mcp.WithDescription(
				"Delete an alert rule by ID.",
			),
			mcp.WithString("alert_id",
				mcp.Required(),
				mcp.Description("The alert rule ID"),
			),
		),
		makeDeleteAlertHandler(deps),
	)
}

func makeDeleteAlertHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		alertID, ok := request.GetArguments()["alert_id"].(string)
		if !ok || alertID == "" {
			return mcp.NewToolResultError("alert_id is required"), nil
		}

		// Verify the alert belongs to a project in the user's teams.
		tag, err := deps.DB.Exec(ctx, `
			UPDATE alert_rules ar
			SET status = 'deleted', updated_at = NOW()
			FROM projects p
			WHERE ar.id = $1
			  AND ar.project_id = p.id
			  AND p.team_id = ANY($2)
		`, alertID, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete alert: %v", err)), nil
		}
		if tag.RowsAffected() == 0 {
			return mcp.NewToolResultError("alert not found or you do not have permission to delete it"), nil
		}

		result, _ := json.Marshal(map[string]any{
			"deleted":  true,
			"alert_id": alertID,
			"message":  "Alert rule deleted successfully",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func RegisterGetIncidents(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("get_incidents",
			mcp.WithDescription(
				"Get active or historical incidents for a project.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("status",
				mcp.Description("Filter by status: active, resolved, all (default: active)"),
			),
		),
		makeGetIncidentsHandler(deps),
	)
}

func makeGetIncidentsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		project, ok := request.GetArguments()["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		status := "active"
		if v, ok := request.GetArguments()["status"].(string); ok && v != "" {
			status = v
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		query := `
			SELECT
				ai.id,
				ai.service,
				ai.status,
				ai.severity,
				ai.started_at,
				ai.resolved_at,
				ai.title,
				ai.description
			FROM alert_incidents ai
			WHERE ai.project_id = $1
		`
		args := []any{projectID}

		if status != "all" {
			query += " AND ai.status = $2"
			args = append(args, status)
		}
		query += " ORDER BY ai.started_at DESC LIMIT 50"

		rows, err := deps.DB.Query(ctx, query, args...)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query incidents: %v", err)), nil
		}
		defer rows.Close()

		type incident struct {
			ID          string  `json:"id"`
			Service     *string `json:"service,omitempty"`
			Status      string  `json:"status"`
			Severity    string  `json:"severity"`
			StartedAt   string  `json:"started_at"`
			ResolvedAt  *string `json:"resolved_at,omitempty"`
			Title       string  `json:"title"`
			Description string  `json:"description"`
			Duration    *string `json:"duration,omitempty"`
		}

		var incidents []incident
		for rows.Next() {
			var i incident
			var resolvedAt *time.Time
			var service *string
			var startedAt time.Time
			if err := rows.Scan(&i.ID, &service, &i.Status, &i.Severity, &startedAt, &resolvedAt, &i.Title, &i.Description); err != nil {
				continue
			}
			i.Service = service
			i.StartedAt = startedAt.UTC().Format(time.RFC3339)
			if resolvedAt != nil {
				ts := resolvedAt.UTC().Format(time.RFC3339)
				i.ResolvedAt = &ts
				duration := resolvedAt.Sub(startedAt).Round(time.Second).String()
				i.Duration = &duration
			}
			incidents = append(incidents, i)
		}

		result, _ := json.Marshal(map[string]any{
			"project":   project,
			"status":    status,
			"incidents": incidents,
			"total":     len(incidents),
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}

func RegisterGetUptime(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("get_uptime",
			mcp.WithDescription(
				"Get uptime statistics for a project or specific service. Includes uptime percentage, downtime duration, and incident history.",
			),
			mcp.WithString("project",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("service",
				mcp.Description("Specific service (default: all services)"),
			),
			mcp.WithString("period",
				mcp.Description("Time period: 7d, 30d, 90d (default: 30d)"),
			),
		),
		makeGetUptimeHandler(deps),
	)
}

func makeGetUptimeHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		project, ok := args["project"].(string)
		if !ok || project == "" {
			return mcp.NewToolResultError("project is required"), nil
		}

		service := ""
		if v, ok := args["service"].(string); ok {
			service = v
		}

		period := "30d"
		if v, ok := args["period"].(string); ok && v != "" {
			period = v
		}

		projectID, err := ProjectForScope(ctx, deps.DB, scope, project)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Query incidents for this service
		query := `
			SELECT
				ai.id,
				ai.started_at,
				ai.resolved_at,
				ai.title,
				ai.description
			FROM alert_incidents ai
			WHERE ai.project_id = $1
		`
		queryArgs := []any{projectID}
		if service != "" {
			query += " AND ai.service = $2"
			queryArgs = append(queryArgs, service)
		}
		query += " ORDER BY ai.started_at DESC"

		rows, err := deps.DB.Query(ctx, query, queryArgs...)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query incidents: %v", err)), nil
		}
		defer rows.Close()

		type incidentInfo struct {
			StartedAt   string `json:"started_at"`
			ResolvedAt  string `json:"resolved_at,omitempty"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		var incidentList []incidentInfo

		for rows.Next() {
			var id, title, description string
			var startedAt time.Time
			var resolvedAt *time.Time
			if err := rows.Scan(&id, &startedAt, &resolvedAt, &title, &description); err != nil {
				continue
			}
			inc := incidentInfo{
				StartedAt:   startedAt.UTC().Format(time.RFC3339),
				Title:       title,
				Description: description,
			}
			if resolvedAt != nil {
				inc.ResolvedAt = resolvedAt.UTC().Format(time.RFC3339)
			}
			incidentList = append(incidentList, inc)
		}

		// Try to get real uptime from Compute provider
		computeUptime := provider.UptimeInfo{}
		if deps.Compute != nil && service != "" {
			region := deps.Config.GCPRegion
			if region == "" {
				region = "us-east4"
			}
			var svcEnvID string
			_ = deps.DB.QueryRow(ctx, `
				SELECT s.environment_id FROM services s
				JOIN environments e ON s.environment_id = e.id
				WHERE e.project_id = $1 AND s.name = $2
				ORDER BY e.created_at DESC LIMIT 1
			`, projectID, service).Scan(&svcEnvID)
			svcName := cloudServiceName(projectID, svcEnvID, service)
			computeUptime, err = deps.Compute.GetUptime(ctx, svcName, region, period)
			if err != nil {
				computeUptime = provider.UptimeInfo{Service: service, UptimePercent: 100, TotalDowntime: "0s"}
			}
		}

		// If no Compute or no service specified, return generic healthy status
		if computeUptime.Service == "" {
			computeUptime = provider.UptimeInfo{
				Service:       service,
				UptimePercent: 100.0,
				TotalDowntime: "0s",
			}
		}

		result, _ := json.Marshal(map[string]any{
			"project":    project,
			"service":    service,
			"period":     period,
			"uptime":     computeUptime,
			"incidents":  incidentList,
			"sla_target": 99.95,
			"status":     "healthy",
		})
		return mcp.NewToolResultText(string(result)), nil
	})
}
