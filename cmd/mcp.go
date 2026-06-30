package cmd

import (
	"github.com/aeon022/budgetctl/internal/mcpserver"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server (stdio) — exposes budget tools to AI",
	RunE: func(cmd *cobra.Command, args []string) error {
		return mcpserver.Serve()
	},
}

func init() { rootCmd.AddCommand(mcpCmd) }
