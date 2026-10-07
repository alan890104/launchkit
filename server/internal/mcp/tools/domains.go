package tools

import (
	"context"

	"github.com/alan890104/launchkit/server/internal/auth"
	domainsvc "github.com/alan890104/launchkit/server/internal/service/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterDomains registers the add_domain, verify_domain, and remove_domain tools.
func RegisterDomains(srv *server.MCPServer, deps *Deps) {
	domainService := newDomainService(deps)

	srv.AddTool(
		mcp.NewTool("add_domain",
			mcp.WithDescription("Map a custom domain to a deployed service. Returns DNS records the user must add at their registrar."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
			mcp.WithString("service_name", mcp.Required(), mcp.Description("Service name within the project")),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Custom domain to map (e.g. api.myapp.com)")),
		),
		makeAddDomainHandler(deps, domainService),
	)

	srv.AddTool(
		mcp.NewTool("verify_domain",
			mcp.WithDescription("Check DNS propagation and SSL status for a custom domain. Call after the user adds DNS records."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
			mcp.WithString("service_name", mcp.Required(), mcp.Description("Service name within the project")),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Custom domain to verify")),
		),
		makeVerifyDomainHandler(domainService),
	)

	srv.AddTool(
		mcp.NewTool("remove_domain",
			mcp.WithDescription("Remove a custom domain mapping from a service."),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project name")),
			mcp.WithString("service_name", mcp.Required(), mcp.Description("Service name within the project")),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Custom domain to remove")),
		),
		makeRemoveDomainHandler(deps, domainService),
	)
}

func makeAddDomainHandler(deps *Deps, domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectName := request.GetArguments()["project_name"].(string)
		projectID, err := domainService.ResolveProjectID(ctx, projectName, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		teamID, err := domainService.ResolveTeamID(ctx, projectID, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := domainService.AddDomain(ctx, domainsvc.AddDomainInput{
			ProjectID:   projectID,
			ServiceName: request.GetArguments()["service_name"].(string),
			Domain:      request.GetArguments()["domain"].(string),
			TeamIDs:     scope.TeamIDs,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		WriteAuditLog(ctx, deps, teamID, scope.UserID, "add_domain", "service", result.ServiceID, map[string]any{
			"domain": result.Domain,
		})
		return domainToolResult(result), nil
	})
}

func makeVerifyDomainHandler(domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectID, err := domainService.ResolveProjectID(ctx, request.GetArguments()["project_name"].(string), scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := domainService.VerifyDomain(ctx, domainsvc.VerifyDomainInput{
			ProjectID:   projectID,
			ServiceName: request.GetArguments()["service_name"].(string),
			Domain:      request.GetArguments()["domain"].(string),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return domainToolResult(result), nil
	})
}

func makeRemoveDomainHandler(deps *Deps, domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		projectName := request.GetArguments()["project_name"].(string)
		projectID, err := domainService.ResolveProjectID(ctx, projectName, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		teamID, err := domainService.ResolveTeamID(ctx, projectID, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		result, err := domainService.RemoveDomain(ctx, domainsvc.RemoveDomainInput{
			ProjectID:   projectID,
			ServiceName: request.GetArguments()["service_name"].(string),
			Domain:      request.GetArguments()["domain"].(string),
			TeamIDs:     scope.TeamIDs,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		WriteAuditLog(ctx, deps, teamID, scope.UserID, "remove_domain", "service", result.ServiceID, map[string]any{
			"domain": result.Domain,
		})
		return domainToolResult(result), nil
	})
}
