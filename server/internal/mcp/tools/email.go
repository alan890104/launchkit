package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	emailsvc "github.com/alan890104/launchkit/server/internal/service/email"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterEmail registers the setup_email, verify_email, and get_email_config tools.
func RegisterEmail(srv *server.MCPServer, deps *Deps) {
	svc := emailsvc.NewService(deps.DB, deps.Email, deps.Enc)

	srv.AddTool(
		mcp.NewTool("setup_email",
			mcp.WithDescription("Set up transactional email sending for a project domain. Returns DNS records (DKIM, SPF, DMARC) the user must add for verification."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Domain to send email from (e.g. myapp.com)")),
		),
		makeSetupEmailHandler(deps, svc),
	)

	srv.AddTool(
		mcp.NewTool("verify_email",
			mcp.WithDescription("Verify email domain DNS records. Once verified, a scoped API key is auto-generated and injected as RESEND_API_KEY."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Email domain to verify")),
		),
		makeVerifyEmailHandler(deps, svc),
	)

	srv.AddTool(
		mcp.NewTool("get_email_config",
			mcp.WithDescription("Get email configuration and usage instructions for a project."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
		),
		makeGetEmailConfigHandler(deps, svc),
	)
}

func makeSetupEmailHandler(deps *Deps, svc *emailsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectName := request.GetArguments()["project_name"].(string)
		domain := request.GetArguments()["domain"].(string)

		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := svc.Setup(ctx, emailsvc.SetupInput{ProjectID: projectID, Domain: domain})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp := map[string]any{
			"domain":      result.Domain,
			"provider":    "resend",
			"status":      "pending_dns",
			"dns_records": result.Records,
			"instructions": "Add all DNS records above at your domain registrar. " +
				"These include DKIM records for email authentication, SPF for sender verification, " +
				"and DMARC for policy. Once added, use verify_email to confirm.",
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func makeVerifyEmailHandler(deps *Deps, svc *emailsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectName := request.GetArguments()["project_name"].(string)
		domain := request.GetArguments()["domain"].(string)

		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := svc.Verify(ctx, emailsvc.VerifyInput{ProjectID: projectID, Domain: domain})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		resp := map[string]any{
			"domain":  result.Domain,
			"status":  result.Status,
			"records": result.Records,
		}

		if result.Status == "verified" {
			if result.APIKeyInjected {
				resp["api_key_injected"] = true
				resp["env_var"] = result.EnvVar
				resp["message"] = fmt.Sprintf(
					"Email domain verified! RESEND_API_KEY has been auto-injected. "+
						"Your app can send emails from any address @%s. "+
						"On next deploy, the key will be available as the RESEND_API_KEY environment variable.",
					result.Domain,
				)
				resp["usage_example"] = map[string]string{
					"node": fmt.Sprintf(`import { Resend } from 'resend';
const resend = new Resend(process.env.RESEND_API_KEY);
await resend.emails.send({
  from: 'noreply@%s',
  to: 'user@example.com',
  subject: 'Hello',
  html: '<p>Welcome!</p>'
});`, result.Domain),
					"python": fmt.Sprintf(`import resend
resend.api_key = os.environ["RESEND_API_KEY"]
resend.Emails.send({
    "from": "noreply@%s",
    "to": "user@example.com",
    "subject": "Hello",
    "html": "<p>Welcome!</p>"
})`, result.Domain),
				}
			} else {
				resp["message"] = "Email domain is already verified and active."
			}
		} else {
			resp["message"] = "DNS records not yet verified. This can take 5-30 minutes after adding records. Try again shortly."
		}

		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}

func makeGetEmailConfigHandler(deps *Deps, svc *emailsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectName := request.GetArguments()["project_name"].(string)

		projectID, err := ProjectForScope(ctx, deps.DB, scope, projectName)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := svc.GetConfig(ctx, projectID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if len(result.Domains) == 0 {
			return mcp.NewToolResultText(`{"email_domains": [], "message": "No email domains configured. Use setup_email to add one."}`), nil
		}

		var domains []map[string]any
		for _, d := range result.Domains {
			domains = append(domains, map[string]any{
				"domain":   d.Domain,
				"provider": d.Provider,
				"status":   d.Status,
				"records":  d.Records,
			})
		}

		resp := map[string]any{
			"email_domains": domains,
			"env_var":       "RESEND_API_KEY",
			"note":          "The RESEND_API_KEY is automatically injected into your services on deploy. Use the Resend SDK to send emails.",
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		return mcp.NewToolResultText(string(data)), nil
	})
}
