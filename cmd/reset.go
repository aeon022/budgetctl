package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete all transactions (optionally scoped to one account)",
	Long: `Delete all transactions, or every transaction for one account.

Useful for wiping a bad import and starting over cleanly, e.g.:

  budgetctl reset --account "Privat 1146"
  budgetctl import fixed-export.csv --account "Privat 1146"

Category rules and budget goals are untouched — this only removes
transactions. Asks for confirmation unless --yes is passed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		account, _ := cmd.Flags().GetString("account")
		skipConfirm, _ := cmd.Flags().GetBool("yes")

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		ctx := context.Background()
		count, err := s.List(ctx, store.Filter{Account: account, Limit: 0})
		if err != nil {
			return err
		}
		if len(count) == 0 {
			if account != "" {
				fmt.Printf("No transactions found for account %q — nothing to delete.\n", account)
			} else {
				fmt.Println("No transactions found — nothing to delete.")
			}
			return nil
		}

		target := fmt.Sprintf("all %d transactions", len(count))
		if account != "" {
			target = fmt.Sprintf("%d transactions for account %q", len(count), account)
		}

		if !skipConfirm {
			fmt.Printf("This will permanently delete %s. Type \"yes\" to confirm: ", target)
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			if strings.TrimSpace(line) != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		n, err := s.DeleteAll(ctx, account)
		if err != nil {
			return fmt.Errorf("reset: %w", err)
		}
		fmt.Printf("Deleted %d transaction(s).\n", n)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resetCmd)
	resetCmd.Flags().StringP("account", "a", "", "Only delete transactions for this account (default: all accounts)")
	resetCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
}
