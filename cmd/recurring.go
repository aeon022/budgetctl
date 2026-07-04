package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var recurringCmd = &cobra.Command{
	Use:   "recurring",
	Short: "Detect recurring payments (subscriptions, rent, utilities)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		txs, err := s.List(context.Background(), store.Filter{})
		if err != nil {
			return err
		}

		patterns := budget.DetectRecurring(txs)
		if len(patterns) == 0 {
			fmt.Println("No recurring patterns detected.")
			return nil
		}

		fmt.Printf("Detected %d recurring payments:\n\n", len(patterns))
		for _, p := range patterns {
			cat := p.Category
			if cat == "" {
				cat = "(uncategorized)"
			}
			fmt.Printf("  %-30s  %7.2f €  %-8s  %-18s  seen %dx  last: %s\n",
				truncate(p.Description, 30),
				p.Amount,
				p.Frequency,
				cat,
				p.Count,
				p.LastSeen.Format("2006-01-02"),
			)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(recurringCmd)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
