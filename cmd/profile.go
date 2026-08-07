package cmd

import (
	"fmt"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/spf13/cobra"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage isolated data profiles (e.g. work vs personal accounts)",
	Long: `Each profile has its own database, so transactions, categories, and
budget goals in one profile never show up in another. Use this to keep,
say, a "firma" (business) context fully separate from your private accounts.

With no active profile, budgetctl uses its normal unscoped default database.`,
}

var profileAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a new profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dataDir, _ := cmd.Flags().GetString("data-dir")
		if err := config.AddProfile(args[0], dataDir); err != nil {
			return err
		}
		fmt.Printf("Profile %q created. Switch to it with: budgetctl profile use %s\n", args[0], args[0])
		return nil
	},
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		active := config.ActiveProfile()

		printRow := func(name string) {
			marker := "  "
			if name == active || (name == "default" && active == "") {
				marker = "* "
			}
			dir, shared := "", false
			if name == "default" {
				dir, shared = config.DefaultDBPath(), config.DefaultShared()
			} else {
				d, s := config.ProfileDir(name)
				dir, shared = d, s
			}
			mode := "local"
			if shared {
				mode = "synced"
			}
			fmt.Printf("%s%-16s %s (%s)\n", marker, name, dir, mode)
		}

		printRow("default")
		for _, name := range config.Profiles() {
			printRow(name)
		}
		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: `Switch the active profile ("default" clears it)`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if name == "default" {
			name = ""
		}
		if err := config.SetActiveProfile(name); err != nil {
			return err
		}
		if name == "" {
			fmt.Println("Switched to the default database.")
		} else {
			fmt.Printf("Switched to profile %q.\n", name)
		}
		return nil
	},
}

var profileRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Forget a profile (does not delete its database)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.RemoveProfile(args[0]); err != nil {
			return err
		}
		fmt.Printf("Profile %q removed. Its database on disk was left untouched.\n", args[0])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileAddCmd)
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileRemoveCmd)
	profileAddCmd.Flags().String("data-dir", "", "Optional folder to store this profile's database in (e.g. for syncing)")
}
