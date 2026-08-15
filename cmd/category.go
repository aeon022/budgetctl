package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var categoryCmd = &cobra.Command{
	Use:   "category",
	Short: "Manage categories",
}

var categoryRenameCmd = &cobra.Command{
	Use:   "rename <old> <new>",
	Short: "Rename a category everywhere — transactions, rules, and goals (merges into <new> if it already exists)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		n, err := s.RenameCategory(context.Background(), args[0], args[1])
		if err != nil {
			return err
		}
		fmt.Printf("Renamed %q -> %q on %d transaction(s)\n", args[0], args[1], n)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(categoryCmd)
	categoryCmd.AddCommand(categoryRenameCmd)
}
