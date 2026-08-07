package cmd

import (
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "budgetctl",
	Short: "Budget tracking from the terminal — import, categorize, report",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		profile, _ := cmd.Flags().GetString("profile")
		if profile == "" {
			return nil
		}
		return config.SetSessionProfile(profile)
	},
}

func Execute() error {
	config.Init()
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringP("profile", "p", "", `Run this one command against a specific profile (e.g. "firma" or "default"), without changing your saved active profile`)
}
