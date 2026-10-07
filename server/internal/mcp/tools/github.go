package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterGitHub(srv *server.MCPServer, deps *Deps) {
	RegisterConnectGitHub(srv, deps)
	RegisterDisconnectGitHub(srv, deps)
}

// ─────────────────────────────────────────────────────────────────────────────
// connect_github
// ─────────────────────────────────────────────────────────────────────────────

func RegisterConnectGitHub(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("connect_github",
			mcp.WithDescription(
				"Connect a GitHub repository to a LaunchKit project for push-to-deploy. "+
					"Returns a webhook URL and secret. The user must add the webhook in their "+
					"GitHub repo Settings → Webhooks with content type application/json and the "+
					"returned secret. After setup, every push to the configured branch triggers "+
					"an automatic deployment.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The LaunchKit project name"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("GitHub repository in owner/repo format (e.g. 'acme/my-app')"),
			),
			mcp.WithString("branch",
				mcp.Description("Branch to deploy on push (default: 'main')"),
			),
		),
		makeConnectGitHubHandler(deps),
	)
}

func makeConnectGitHubHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		repo, ok := args["repo"].(string)
		if !ok || repo == "" {
			return mcp.NewToolResultError("repo is required (format: owner/repo)"), nil
		}

		branch := "main"
		if b, ok := args["branch"].(string); ok && b != "" {
			branch = b
		}

		// Auth
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		// Generate a cryptographically random webhook secret.
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("generate secret: %v", err)), nil
		}
		webhookSecret := hex.EncodeToString(secretBytes)

		// Upsert the connection (update secret + branch if reconnecting same repo).
		_, err = deps.DB.Exec(ctx, `
			INSERT INTO github_connections (project_id, repo_full_name, branch, webhook_secret)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (project_id, repo_full_name) DO UPDATE SET
				branch = EXCLUDED.branch,
				webhook_secret = EXCLUDED.webhook_secret,
				status = 'active',
				updated_at = NOW()
		`, projectID, repo, branch, webhookSecret)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("save connection: %v", err)), nil
		}

		webhookURL := deps.Config.BaseURL + "/webhooks/github"

		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "connect_github", "project", projectID, map[string]any{
			"repo":   repo,
			"branch": branch,
		})

		data, _ := json.Marshal(map[string]any{
			"status":      "connected",
			"project":     projectName,
			"repo":        repo,
			"branch":      branch,
			"webhook_url": webhookURL,
			"secret":      webhookSecret,
			"setup_instructions": fmt.Sprintf(
				"Add a webhook in GitHub:\n"+
					"1. Go to https://github.com/%s/settings/hooks/new\n"+
					"2. Payload URL: %s\n"+
					"3. Content type: application/json\n"+
					"4. Secret: %s\n"+
					"5. Events: Just the push event\n"+
					"6. Click 'Add webhook'\n\n"+
					"After setup, every push to '%s' will trigger an automatic deployment.",
				repo, webhookURL, webhookSecret, branch,
			),
		})
		return mcp.NewToolResultText(string(data)), nil
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// disconnect_github
// ─────────────────────────────────────────────────────────────────────────────

func RegisterDisconnectGitHub(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("disconnect_github",
			mcp.WithDescription(
				"Disconnect a GitHub repository from push-to-deploy. "+
					"The user should also remove the webhook from their GitHub repo settings.",
			),
			mcp.WithString("project_name",
				mcp.Required(),
				mcp.Description("The LaunchKit project name"),
			),
			mcp.WithString("repo",
				mcp.Required(),
				mcp.Description("GitHub repository in owner/repo format"),
			),
		),
		makeDisconnectGitHubHandler(deps),
	)
}

func makeDisconnectGitHubHandler(deps *Deps) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		projectName, ok := args["project_name"].(string)
		if !ok || projectName == "" {
			return mcp.NewToolResultError("project_name is required"), nil
		}

		repo, ok := args["repo"].(string)
		if !ok || repo == "" {
			return mcp.NewToolResultError("repo is required"), nil
		}

		// Auth
		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("project not found: %v", err)), nil
		}

		tag, err := deps.DB.Exec(ctx, `
			DELETE FROM github_connections
			WHERE project_id = $1 AND repo_full_name = $2
		`, projectID, repo)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("delete connection: %v", err)), nil
		}

		if tag.RowsAffected() == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("no GitHub connection found for repo %q in project %q", repo, projectName)), nil
		}

		WriteAuditLog(ctx, deps, teamIDForProject(ctx, deps, projectID), scope.UserID, "disconnect_github", "project", projectID, map[string]any{
			"repo": repo,
		})

		data, _ := json.Marshal(map[string]any{
			"status":  "disconnected",
			"project": projectName,
			"repo":    repo,
			"message": fmt.Sprintf(
				"GitHub connection removed. Don't forget to also delete the webhook at:\nhttps://github.com/%s/settings/hooks",
				repo,
			),
		})
		return mcp.NewToolResultText(string(data)), nil
	})
}
