package mcp

import (
	"github.com/mark3labs/mcp-go/server"

	"github.com/alan890104/launchkit/server/internal/mcp/tools"
)

// NewServer creates an MCPServer and registers all tools.
func NewServer(deps *tools.Deps) *server.MCPServer {
	srv := server.NewMCPServer(
		"launchkit",
		deps.Config.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithToolHandlerMiddleware(tools.AuthToolMiddleware(deps)),
	)

	// Register tools — one line per tool, one file per tool.
	tools.RegisterPing(srv, deps)
	tools.RegisterPlanDeployment(srv, deps)
	tools.RegisterDeployProject(srv, deps)
	tools.RegisterDeployStatus(srv, deps)
	tools.RegisterDeployLogs(srv, deps)
	tools.RegisterRegistryCredentials(srv, deps) // get_registry_credentials
	tools.RegisterManage(srv, deps)              // list_projects, destroy, scale, restart, usage
	tools.RegisterEnvironments(srv, deps)        // create_environment, list_environments, promote_environment, destroy_environment
	tools.RegisterMonitor(srv, deps)             // get_metrics, set_alert, list_alerts, delete_alert, get_incidents, get_uptime
	tools.RegisterDomains(srv, deps)             // add_domain, verify_domain, remove_domain
	tools.RegisterEmail(srv, deps)               // setup_email, verify_email, get_email_config
	tools.RegisterTeam(srv, deps)                // invite_member, list_members, update_member_role, remove_member
	tools.RegisterTransfer(srv, deps)            // transfer_project
	tools.RegisterCron(srv, deps)                // create_cron_job, list_cron_jobs, delete_cron_job
	tools.RegisterGitHub(srv, deps)              // connect_github, disconnect_github
	tools.RegisterAudit(srv, deps)               // get_audit_logs
	tools.RegisterDomainPurchase(srv, deps)      // search_domain, purchase_domain, list_purchased_domains
	tools.RegisterDNS(srv, deps)                 // manage_dns

	return srv
}
