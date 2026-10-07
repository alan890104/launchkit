package tools

import (
	"context"
	"encoding/json"

	"github.com/alan890104/launchkit/server/internal/auth"
	domainsvc "github.com/alan890104/launchkit/server/internal/service/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterDomainPurchase registers domain search, purchase, and listing tools.
func RegisterDomainPurchase(srv *server.MCPServer, deps *Deps) {
	domainService := newDomainService(deps)

	srv.AddTool(
		mcp.NewTool("search_domain",
			mcp.WithDescription(
				"Search for available domains to purchase. "+
					"Checks availability and returns pricing. "+
					"Input can be a single domain (myapp.com), a base name without TLD (myapp) "+
					"which auto-checks popular TLDs, or a comma-separated list.",
			),
			mcp.WithString("query", mcp.Required(), mcp.Description(
				"Domain name(s) to search. Examples: 'myapp.com', 'myapp', 'myapp.com,myapp.io'",
			)),
		),
		makeSearchDomainHandler(domainService),
	)

	srv.AddTool(
		mcp.NewTool("purchase_domain",
			mcp.WithDescription(
				"Purchase a domain and automatically configure DNS for a deployed service. "+
					"Deducts cost from team balance. DNS is auto-configured — zero manual setup needed.",
			),
			mcp.WithString("domain", mcp.Required(), mcp.Description("Domain to purchase (e.g. myapp.com)")),
			mcp.WithString("project_name", mcp.Required(), mcp.Description("Project to configure the domain for")),
			mcp.WithString("service_name", mcp.Required(), mcp.Description("Service within the project")),
			mcp.WithNumber("years", mcp.Description("Registration years (1-10, default 1)")),
		),
		makePurchaseDomainHandler(deps, domainService),
	)

	srv.AddTool(
		mcp.NewTool("list_purchased_domains",
			mcp.WithDescription("List all domains purchased through LaunchKit for your team."),
		),
		makeListPurchasedDomainsHandler(domainService),
	)
}

func makeSearchDomainHandler(domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, _ auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := domainService.Search(ctx, request.GetArguments()["query"].(string))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return domainToolResult(result), nil
	})
}

func makePurchaseDomainHandler(deps *Deps, domainService *domainsvc.Service) server.ToolHandlerFunc {
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

		years := 1
		if y, ok := request.GetArguments()["years"].(float64); ok {
			years = int(y)
		}

		result, err := domainService.Purchase(ctx, domainsvc.PurchaseInput{
			Domain:      request.GetArguments()["domain"].(string),
			ProjectID:   projectID,
			ProjectName: projectName,
			ServiceName: request.GetArguments()["service_name"].(string),
			TeamID:      teamID,
			Years:       years,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		WriteAuditLog(ctx, deps, teamID, scope.UserID, "purchase_domain", "domain", result.Domain, map[string]any{
			"domain":    result.Domain,
			"price_usd": result.PriceUSD,
			"years":     result.Years,
			"project":   projectName,
		})

		return domainToolResult(result), nil
	})
}

func makeListPurchasedDomainsHandler(domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := domainService.ListPurchased(ctx, scope.TeamIDs)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return domainToolResult(result), nil
	})
}

func newDomainService(deps *Deps) *domainsvc.Service {
	return domainsvc.NewService(deps.Config, deps.DB, deps.Logger, deps.Compute, deps.DomainRegistrar)
}

func domainToolResult(v any) *mcp.CallToolResult {
	data, _ := json.MarshalIndent(v, "", "  ")
	return mcp.NewToolResultText(string(data))
}
