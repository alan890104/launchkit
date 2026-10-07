package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alan890104/launchkit/server/internal/auth"
	domainsvc "github.com/alan890104/launchkit/server/internal/service/domain"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterDNS registers the manage_dns tool for generic DNS record management.
func RegisterDNS(srv *server.MCPServer, deps *Deps) {
	domainService := newDomainService(deps)

	srv.AddTool(
		mcp.NewTool("manage_dns",
			mcp.WithDescription(
				"Manage DNS records for domains purchased through LaunchKit. "+
					"Supports listing, creating, and deleting records (A, AAAA, CNAME, MX, TXT, SRV, NS). "+
					"For creating records, you can pass multiple records in one call for batch operations.",
			),
			mcp.WithString("domain", mcp.Required(), mcp.Description(
				"The domain to manage DNS for. Can be the root domain (myapp.dev) or include "+
					"a subdomain — the root purchased domain will be resolved automatically.",
			)),
			mcp.WithString("action", mcp.Required(), mcp.Description(
				"Action to perform: 'list', 'create', or 'delete'",
			)),
			mcp.WithObject("records", mcp.Description(
				"For 'create': array of records to add. Each record needs: type (A/AAAA/CNAME/MX/TXT/SRV/NS), "+
					"host (subdomain or empty for apex), value, and optionally ttl and priority. "+
					"For 'delete': array of records with 'id' field (get IDs from 'list' action). "+
					"Example create: [{\"type\":\"MX\",\"host\":\"\",\"value\":\"aspmx.l.google.com\",\"priority\":1}]",
			)),
		),
		makeManageDNSHandler(deps, domainService),
	)
}

func makeManageDNSHandler(deps *Deps, domainService *domainsvc.Service) server.ToolHandlerFunc {
	return WithAuth(func(ctx context.Context, scope auth.AuthScope, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		domain := request.GetArguments()["domain"].(string)
		action := request.GetArguments()["action"].(string)

		switch action {
		case "list":
			result, err := domainService.ListDNS(ctx, domain, scope.TeamIDs)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return domainToolResult(result), nil
		case "create":
			records, err := parseDNSRecords(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			teamID, err := domainService.ResolveOwnedDomainTeamID(ctx, domain, scope.TeamIDs)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			result, err := domainService.CreateDNS(ctx, domain, scope.TeamIDs, records)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			WriteAuditLog(ctx, deps, teamID, scope.UserID, "manage_dns_create", "domain", result.Domain, map[string]any{
				"domain":        result.Domain,
				"records_count": len(records),
			})
			return domainToolResult(result), nil
		case "delete":
			records, err := parseDNSRecords(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			teamID, err := domainService.ResolveOwnedDomainTeamID(ctx, domain, scope.TeamIDs)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			result, err := domainService.DeleteDNS(ctx, domain, scope.TeamIDs, records)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			WriteAuditLog(ctx, deps, teamID, scope.UserID, "manage_dns_delete", "domain", result.Domain, map[string]any{
				"domain":        result.Domain,
				"records_count": len(records),
			})
			return domainToolResult(result), nil
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action %q — use 'list', 'create', or 'delete'", action)), nil
		}
	})
}

func parseDNSRecords(request mcp.CallToolRequest) ([]domainsvc.DNSRecordInput, error) {
	raw, ok := request.GetArguments()["records"]
	if !ok || raw == nil {
		return nil, nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid records format: %w", err)
	}

	var records []domainsvc.DNSRecordInput
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("records must be an array of {type, host, value, ttl?, priority?, id?}: %w", err)
	}
	return records, nil
}
