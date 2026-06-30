package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List transactions",
	RunE: func(cmd *cobra.Command, args []string) error {
		month, _ := cmd.Flags().GetString("month")
		category, _ := cmd.Flags().GetString("category")
		account, _ := cmd.Flags().GetString("account")
		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt("limit")
		asJSON, _ := cmd.Flags().GetBool("json")

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		txs, err := s.List(context.Background(), store.Filter{
			Month:    month,
			Category: category,
			Account:  account,
			Query:    query,
			Limit:    limit,
		})
		if err != nil {
			return err
		}

		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(txs)
		}
		for _, t := range txs {
			cat := t.Category
			if cat == "" {
				cat = "-"
			}
			sign := ""
			if t.Amount > 0 {
				sign = "+"
			}
			fmt.Printf("%s  %+8.2f€  %-20s  %-16s  %s\n",
				t.Date.Format("2006-01-02"),
				t.Amount,
				sign,
				cat,
				t.Description)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().StringP("month", "m", "", "Filter by month (YYYY-MM)")
	listCmd.Flags().StringP("category", "c", "", "Filter by category")
	listCmd.Flags().StringP("account", "a", "", "Filter by account")
	listCmd.Flags().StringP("query", "q", "", "Search description")
	listCmd.Flags().IntP("limit", "n", 100, "Max results")
	listCmd.Flags().Bool("json", false, "Output as JSON")
}
