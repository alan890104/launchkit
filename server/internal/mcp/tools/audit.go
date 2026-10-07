package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterAudit(srv *server.MCPServer, deps *Deps) {
	RegisterGetAuditLogs(srv, deps)
}

func RegisterGetAuditLogs(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("get_audit_logs",
			mcp.WithDescription(
				"Query the audit log for a project. Shows who did what and when — "+
					"deployments, scaling changes, environment updates, team changes, etc. "+
					"Useful for debugging, compliance, and understanding project history.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The project name"),
			),
			mcp.WithString("action",
				mcp.Description("Filter by action (e.g. 'deploy', 'scale', 'destroy', 'invite_member', 'update_env')"),
			),
			mcp.WithNumber("limit",
				mcp.Description("Max number of entries to return (default 25, max 100)"),
			),
		),
		makeGetAuditLogsHandler(deps),
	)
}

func makeGetAuditLogsHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		actionFilter, _ := args["action"].(string)

		limit := 25
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		if limit > 100 {
			limit = 100
		}

		// Resolve project → team, enforce membership via AuthScope.
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project %q not found", projectName)), nil
		}
		teamID, err := TeamIDForScope(ctx, deps.DB, scope, projectID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project %q not found", projectName)), nil
		}

		// Build query with optional action filter.
		query := `
			SELECT al.action, al.resource_type, al.resource_id, al.details,
			       al.created_at, COALESCE(u.email, al.user_id, 'system') as actor
			FROM audit_logs al
			LEFT JOIN users u ON al.user_id = u.id
			WHERE al.team_id = $1
		`
		qArgs := []any{teamID}

		if actionFilter != "" {
			query += " AND al.action = $2"
			qArgs = append(qArgs, actionFilter)
		}

		query += " ORDER BY al.created_at DESC LIMIT " + fmt.Sprintf("%d", limit)

		rows, err := deps.DB.Query(ctx, query, qArgs...)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query audit logs: %v", err)), nil
		}
		defer rows.Close()

		type entry struct {
			Action       string         `json:"action"`
			ResourceType *string        `json:"resource_type,omitempty"`
			ResourceID   *string        `json:"resource_id,omitempty"`
			Details      map[string]any `json:"details,omitempty"`
			Actor        string         `json:"actor"`
			Timestamp    string         `json:"timestamp"`
		}

		var entries []entry
		for rows.Next() {
			var e entry
			var resType, resID *string
			var details []byte
			var ts time.Time
			if err := rows.Scan(&e.Action, &resType, &resID, &details, &ts, &e.Actor); err != nil {
				continue
			}
			e.ResourceType = resType
			e.ResourceID = resID
			e.Timestamp = ts.Format(time.RFC3339)
			if len(details) > 0 {
				_ = json.Unmarshal(details, &e.Details)
			}
			entries = append(entries, e)
		}

		if entries == nil {
			entries = []entry{}
		}

		data, _ := json.Marshal(map[string]any{
			"project": projectName,
			"count":   len(entries),
			"entries": entries,
		})
		return mcp.NewToolResultText(string(data)), nil
	})
}

// teamIDForProject looks up the team_id for a given project ID.
// Returns "" on error — callers use this for best-effort audit logging.
func teamIDForProject(ctx context.Context, deps *Deps, projectID string) string {
	var teamID string
	_ = deps.DB.QueryRow(ctx, "SELECT team_id FROM projects WHERE id = $1", projectID).Scan(&teamID)
	return teamID
}

// WriteAuditLog inserts an entry into the audit_logs table.
// Intended to be called from other tool handlers after significant actions.
func WriteAuditLog(ctx context.Context, deps *Deps, teamID, userID, action, resourceType, resourceID string, details map[string]any) {
	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}
	_, _ = deps.DB.Exec(ctx, `
		INSERT INTO audit_logs (team_id, user_id, action, resource_type, resource_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, teamID, userID, action, resourceType, resourceID, detailsJSON)
}
