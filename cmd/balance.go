package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var balanceCmd = &cobra.Command{
	Use:   "balance",
	Short: "Show account balances (needs an opening balance per account: 'balance set')",
	Long: `Imported bank exports usually start mid-history, so the sum of transactions is not a balance.
budgetctl therefore only shows a balance for accounts where you typed in the balance from a statement
('balance set ACCOUNT AMOUNT --as-of DATE'); it never guesses one.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		bs, err := s.Balances(context.Background())
		if err != nil {
			return err
		}
		if j, _ := cmd.Flags().GetBool("json"); j {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"data": bs, "currency": "EUR"})
		}
		if len(bs) == 0 {
			fmt.Println("No accounts yet — import a bank CSV first.")
			return nil
		}
		var total float64
		missing := 0
		for _, b := range bs {
			if !b.Anchored {
				missing++
				fmt.Printf("  %-20s  no opening balance — set one: budgetctl balance set %q AMOUNT --as-of DATE\n", b.Account, b.Account)
				continue
			}
			total += b.Balance
			fmt.Printf("  %-20s %12.2f €   (anchor %.2f on %s, %d bookings since)\n", b.Account, b.Balance, b.Anchor, b.AsOf, b.Since)
		}
		fmt.Printf("  %-20s %12.2f €", "Total (with balance)", total)
		if missing > 0 {
			fmt.Printf("   — %d account(s) without opening balance not counted", missing)
		}
		fmt.Println()
		return nil
	},
}

var balanceSetCmd = &cobra.Command{
	Use:   "set ACCOUNT AMOUNT --as-of DATE",
	Short: "Record the balance of an account at the end of a day (from a bank statement)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := strconv.ParseFloat(strings.Replace(args[1], ",", ".", 1), 64)
		if err != nil {
			return fmt.Errorf("amount %q is not a number", args[1])
		}
		v, _ := cmd.Flags().GetString("as-of")
		if v == "" {
			return fmt.Errorf("--as-of DATE is required (the day the balance is from, YYYY-MM-DD)")
		}
		asOf, err := time.Parse("2006-01-02", v)
		if err != nil {
			return fmt.Errorf("--as-of %q: use YYYY-MM-DD", v)
		}
		if asOf.After(time.Now().AddDate(0, 0, 1)) {
			return fmt.Errorf("--as-of %s is in the future", v)
		}
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.SetAnchor(context.Background(), args[0], asOf, amount); err != nil {
			return err
		}
		fmt.Printf("Opening balance set: %s = %.2f € on %s\n", args[0], amount, v)
		return nil
	},
}

var balanceClearCmd = &cobra.Command{
	Use:   "clear ACCOUNT",
	Short: "Remove an account's opening balance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		ok, err := s.ClearAnchor(context.Background(), args[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no opening balance for %q", args[0])
		}
		fmt.Printf("Opening balance removed: %s\n", args[0])
		return nil
	},
}

func init() {
	balanceCmd.Flags().Bool("json", false, "Output as JSON")
	balanceSetCmd.Flags().String("as-of", "", "day the balance is from (YYYY-MM-DD)")
	balanceCmd.AddCommand(balanceSetCmd, balanceClearCmd)
	rootCmd.AddCommand(balanceCmd)
}
