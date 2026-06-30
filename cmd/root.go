package cmd

import (
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "budgetctl",
	Short: "Budget tracking from the terminal — import, categorize, report",
}

func Execute() error {
	config.Init()
	return rootCmd.Execute()
}
