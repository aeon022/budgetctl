package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var addCmd = &cobra.Command{
	Use:   "add <description> <amount>",
	Short: "Add a manual transaction (negative = expense, positive = income)",
	Long: `Add a manual income or expense entry.

Examples:
  budgetctl add "Coffee" -- -4.50
  budgetctl add "Freelance invoice" 1200 --category income --date 2026-07-01`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		desc := args[0]
		amount, err := budget.ParseUserAmount(args[1])
		if err != nil {
			return err
		}
		dateStr, _ := cmd.Flags().GetString("date")
		category, _ := cmd.Flags().GetString("category")

		date := time.Now()
		if dateStr != "" {
			date, err = time.Parse("2006-01-02", dateStr)
			if err != nil {
				return fmt.Errorf("invalid date %q (use YYYY-MM-DD)", dateStr)
			}
		}

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		t := &models.Transaction{
			ID:          fmt.Sprintf("manual-%d", time.Now().UnixNano()),
			Date:        date,
			Description: desc,
			Amount:      amount,
			Category:    category,
			Account:     "manual",
			Source:      "cli",
		}
		if err := s.Upsert(context.Background(), t); err != nil {
			return err
		}
		kind := "expense"
		if amount > 0 {
			kind = "income"
		}
		fmt.Printf("Added %s: %s  %+.2f€", kind, desc, amount)
		if category != "" {
			fmt.Printf("  [%s]", category)
		}
		fmt.Println()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(addCmd)
	addCmd.Flags().StringP("date", "d", "", "Date (YYYY-MM-DD, default today)")
	addCmd.Flags().StringP("category", "c", "", "Category")
}
