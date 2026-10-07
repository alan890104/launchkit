package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterPing(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("ping",
			mcp.WithDescription(
				"Test connectivity to LaunchKit. Returns server status. "+
					"Call this first to verify the connection is working.",
			),
		),
		makePingHandler(deps),
	)
}

type pingResult struct {
	Status    string `json:"status"`
	Server    string `json:"server"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
}

func makePingHandler(deps *Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := pingResult{
			Status:    "ok",
			Server:    "launchkit",
			Version:   deps.Config.Version,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
