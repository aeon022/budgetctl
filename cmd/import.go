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
		useAI, _ := cmd.Flags().GetBool("ai")

		if useAI && !config.IsPro() {
			fmt.Println("AI categorization is a missionctl Bundle feature.")
			fmt.Println("Get it at https://missionctl.sh/#pricing, then: budgetctl license activate <key>")
			fmt.Println("Importing without AI categorization...")
			useAI = false
		}

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()

		res, err := budget.ImportFile(context.Background(), s, path, account, useAI)
		if err != nil {
			return fmt.Errorf("import: %w", err)
		}

		fmt.Printf("Imported %d transactions from %s\n", res.Imported, path)
		if useAI && res.AICategorized > 0 {
			fmt.Printf("AI-categorized: %d transactions\n", res.AICategorized)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(importCmd)
	importCmd.Flags().StringP("account", "a", "", "Tag every imported transaction with this account name (creates it — accounts have no separate setup step)")
	importCmd.Flags().Bool("ai", false, "Use AI (Anthropic/OpenAI/Gemini/local Ollama) to categorize uncategorized transactions")
}
