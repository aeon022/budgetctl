package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
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
		useAI, _ := cmd.Flags().GetBool("ai")

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

		// AI categorization of remaining uncategorized transactions
		aiCount := 0
		if useAI {
			var uncategorized []models.Transaction
			for _, tx := range txs {
				if tx.Category == "" {
					uncategorized = append(uncategorized, tx)
				}
			}
			if len(uncategorized) > 0 {
				// Collect distinct category names from rules as hints
				catSet := make(map[string]bool)
				for _, r := range rules {
					if r.Category != "" {
						catSet[r.Category] = true
					}
				}
				var cats []string
				for c := range catSet {
					cats = append(cats, c)
				}
				sort.Strings(cats)

				aiMap, err := budget.AICategories(ctx, uncategorized, cats)
				if err != nil {
					fmt.Printf("warn: AI categorization failed: %v\n", err)
				} else {
					for i := range txs {
						if txs[i].Category == "" {
							if cat, ok := aiMap[txs[i].Description]; ok && cat != "" {
								fmt.Printf("Claude: %s → %s\n", txs[i].Description, cat)
								txs[i].Category = cat
								_ = s.Upsert(ctx, &txs[i])
								aiCount++
							}
						}
					}
				}
			}
		}

		fmt.Printf("Imported %d transactions from %s\n", new, path)
		if useAI && aiCount > 0 {
			fmt.Printf("AI-categorized: %d transactions\n", aiCount)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().StringP("account", "a", "", "Override account name")
	importCmd.Flags().Bool("ai", false, "Use Claude to categorize uncategorized transactions")
}
