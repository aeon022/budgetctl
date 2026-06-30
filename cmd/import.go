package cmd

import (
	"context"
	"fmt"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import <file.csv>",
	Short: "Import bank CSV (N26, ING, DKB, or generic format)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		account, _ := cmd.Flags().GetString("account")

		txs, err := budget.Import(path)
		if err != nil {
			return fmt.Errorf("import: %w", err)
		}

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		ctx := context.Background()
		rules, _ := s.ListRules(ctx)

		new, dup := 0, 0
		for i := range txs {
			if account != "" {
				txs[i].Account = account
			}
			// auto-categorize if rule matches
			if txs[i].Category == "" && len(rules) > 0 {
				txs[i].Category = budget.Categorize(txs[i].Description, rules)
			}
			if err := s.Upsert(ctx, &txs[i]); err != nil {
				fmt.Printf("warn: %s: %v\n", txs[i].Description, err)
			} else {
				new++
			}
			_ = dup
		}

		fmt.Printf("Imported %d transactions from %s\n", new, path)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().StringP("account", "a", "", "Override account name")
}
