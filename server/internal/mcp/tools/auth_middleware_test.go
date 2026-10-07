package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestAuthMiddleware_RejectsUnauthenticated verifies that the global
// middleware rejects tool calls when no userID is in the context.
func TestAuthMiddleware_RejectsUnauthenticated(t *testing.T) {
	// nil DB is fine — middleware should reject before hitting DB.
	middleware := AuthToolMiddleware(&Deps{})

	// A dummy handler that should never be called.
	called := false
	handler := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "deploy_status" // requires auth
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("handler was called despite missing auth")
	}
	if result == nil {
		t.Fatal("expected error result, got nil")
	}
	// Result should contain "unauthenticated"
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			if strings.Contains(tc.Text, "unauthenticated") {
				return // success
			}
		}
	}
	t.Fatal("expected result to contain 'unauthenticated'")
}

// TestAuthMiddleware_AllowsPingWithoutAuth verifies that tools in the
// noAuthTools allowlist pass through even without authentication.
func TestAuthMiddleware_AllowsPingWithoutAuth(t *testing.T) {
	middleware := AuthToolMiddleware(&Deps{})

	called := false
	handler := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("pong"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "ping"
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("ping handler was not called — should bypass auth")
	}
}

// TestAuthMiddleware_AllowsRegistryCredentialsWithoutAuth verifies
// get_registry_credentials also bypasses auth.
func TestAuthMiddleware_AllowsRegistryCredentialsWithoutAuth(t *testing.T) {
	middleware := AuthToolMiddleware(&Deps{})

	called := false
	handler := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "get_registry_credentials"
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("get_registry_credentials handler was not called — should bypass auth")
	}
}

// TestAuthMiddleware_NoAuthToolsAllowlist verifies the allowlist is
// exactly the set of tools that should bypass authentication.
func TestAuthMiddleware_NoAuthToolsAllowlist(t *testing.T) {
	expected := map[string]bool{
		"ping":                     true,
		"get_registry_credentials": true,
	}

	if len(noAuthTools) != len(expected) {
		t.Fatalf("noAuthTools has %d entries, expected %d", len(noAuthTools), len(expected))
	}

	for tool := range expected {
		if !noAuthTools[tool] {
			t.Errorf("expected %q in noAuthTools", tool)
		}
	}
}

// TestAuthScope_ContextRoundTrip verifies AuthScope survives context
// injection and extraction.
func TestAuthScope_ContextRoundTrip(t *testing.T) {
	scope := auth.AuthScope{
		UserID:  "user-123",
		TeamIDs: []string{"team-a", "team-b"},
	}

	ctx := auth.ContextWithScope(context.Background(), scope)
	got, ok := auth.ScopeFromContext(ctx)
	if !ok {
		t.Fatal("ScopeFromContext returned false")
	}
	if got.UserID != scope.UserID {
		t.Errorf("UserID = %q, want %q", got.UserID, scope.UserID)
	}
	if len(got.TeamIDs) != 2 || got.TeamIDs[0] != "team-a" || got.TeamIDs[1] != "team-b" {
		t.Errorf("TeamIDs = %v, want %v", got.TeamIDs, scope.TeamIDs)
	}
}

// TestAuthScope_MissingFromContext verifies that ScopeFromContext returns
// false when no scope has been set.
func TestAuthScope_MissingFromContext(t *testing.T) {
	_, ok := auth.ScopeFromContext(context.Background())
	if ok {
		t.Fatal("expected ScopeFromContext to return false for empty context")
	}
}

// TestWithAuth_ExtractsScope verifies the WithAuth wrapper correctly
// extracts AuthScope from context and passes it to the handler.
func TestWithAuth_ExtractsScope(t *testing.T) {
	var receivedScope auth.AuthScope

	handler := WithAuth(func(ctx context.Context, scope auth.AuthScope, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		receivedScope = scope
		return mcp.NewToolResultText("ok"), nil
	})

	scope := auth.AuthScope{UserID: "user-1", TeamIDs: []string{"team-1"}}
	ctx := auth.ContextWithScope(context.Background(), scope)

	req := mcp.CallToolRequest{}
	_, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedScope.UserID != "user-1" {
		t.Errorf("WithAuth passed UserID=%q, want %q", receivedScope.UserID, "user-1")
	}
}

// TestWithAuth_RejectsMissingScope verifies the WithAuth wrapper returns
// an error when AuthScope is not in context (middleware missing).
func TestWithAuth_RejectsMissingScope(t *testing.T) {
	handler := WithAuth(func(ctx context.Context, scope auth.AuthScope, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Fatal("handler should not be called without scope")
		return nil, nil
	})

	req := mcp.CallToolRequest{}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return error result mentioning BUG
	for _, content := range result.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			if strings.Contains(tc.Text, "BUG") {
				return // success
			}
		}
	}
	t.Fatal("expected BUG error in result")
}

// TestAuthMiddleware_RejectsAllNonAllowlistedTools verifies that every
// known tool name (not in the allowlist) is rejected without auth.
func TestAuthMiddleware_RejectsAllNonAllowlistedTools(t *testing.T) {
	sampleTools := []string{
		"plan_deployment", "deploy_project", "deploy_status",
		"list_projects", "destroy", "scale", "restart",
		"get_metrics", "set_alert", "add_domain",
		"invite_member", "purchase_domain", "manage_dns",
	}

	middleware := AuthToolMiddleware(&Deps{})

	for _, toolName := range sampleTools {
		t.Run(toolName, func(t *testing.T) {
			called := false
			handler := middleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				called = true
				return mcp.NewToolResultText("ok"), nil
			})

			req := mcp.CallToolRequest{}
			req.Params.Name = toolName
			_, err := handler(context.Background(), req) // no userID in context
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if called {
				t.Fatalf("tool %q handler was called without auth", toolName)
			}
		})
	}
}
