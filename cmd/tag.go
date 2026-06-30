package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var tagCmd = &cobra.Command{
	Use:   "tag <pattern> --category <name>",
	Short: "Set a category rule: any description matching pattern → category",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		category, _ := cmd.Flags().GetString("category")
		apply, _ := cmd.Flags().GetBool("apply")

		if category == "" {
			return fmt.Errorf("--category is required")
		}

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		if err := s.SaveRule(ctx, pattern, category); err != nil {
			return err
		}
		fmt.Printf("Saved rule: %q → %s\n", pattern, category)

		if apply {
			n, err := s.ApplyRules(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("Applied to %d transactions\n", n)
		}
		return nil
	},
}

var applyRulesCmd = &cobra.Command{
	Use:   "apply-rules",
	Short: "Re-apply all category rules to existing transactions",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()
		n, err := s.ApplyRules(context.Background())
		if err != nil {
			return err
		}
		fmt.Printf("Applied rules to %d transactions\n", n)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tagCmd)
	tagCmd.Flags().StringP("category", "c", "", "Category name (required)")
	tagCmd.Flags().Bool("apply", false, "Also apply all rules to existing transactions")
	rootCmd.AddCommand(applyRulesCmd)
}
