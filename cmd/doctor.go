package cmd

import (
	"fmt"
	"os"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/missionctl-core/doctor"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check config and database health",
	Run: func(cmd *cobra.Command, args []string) {
		profile := config.ActiveProfile()
		if profile == "" {
			profile = "default"
		}
		fmt.Printf("Profile: %s\n", profile)
		checks := []doctor.Check{
			doctor.CheckSQLite("Database", config.DBPath(), "transactions"),
			doctor.CheckDataDir("Data directory", config.DBPath(), config.Shared()),
		}
		if !doctor.PrintReport(checks) {
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
