package mcpserver

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestDestructiveToolsNeedConfirm(t *testing.T) {
	called := 0
	h := confirmed(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called++
		return mcp.NewToolResultText("deleted"), nil
	})
	if res, _ := h(context.Background(), mcp.CallToolRequest{}); called != 0 || !res.IsError {
		t.Fatalf("without confirm nothing may happen (called=%d, res=%+v)", called, res)
	}
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"confirm": true}}}
	if res, _ := h(context.Background(), req); called != 1 || res.IsError {
		t.Fatalf("with confirm=true the call goes through (called=%d)", called)
	}
}
