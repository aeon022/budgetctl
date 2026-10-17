package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// confirmed refuses a destructive tool call unless the caller passes confirm=true. It makes the model ask
// the user first (an injected instruction has to say so explicitly); it is a speed bump, not a lock.
func confirmed(h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !req.GetBool("confirm", false) {
			return mcp.NewToolResultError("this deletes data permanently: ask the user to confirm, then call again with confirm=true"), nil
		}
		return h(ctx, req)
	}
}

// confirmParam is the parameter every destructive tool declares.
var confirmParam = mcp.WithBoolean("confirm", mcp.Required(), mcp.Description("Must be true. Only set it after the user explicitly approved this deletion."))
