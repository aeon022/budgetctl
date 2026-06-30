package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var summaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Monthly income/expense summary with category breakdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		month, _ := cmd.Flags().GetString("month")
		asJSON, _ := cmd.Flags().GetBool("json")

		if month == "" {
			month = time.Now().Format("2006-01")
		}

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		sum, err := s.Summary(context.Background(), month)
		if err != nil {
			return err
		}

		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(sum)
		}

		fmt.Printf("── %s ──────────────────────────────\n", month)
		fmt.Printf("  Income:   %+10.2f €\n", sum.Income)
		fmt.Printf("  Expenses: %+10.2f €\n", sum.Expenses)
		fmt.Printf("  Net:      %+10.2f €\n", sum.Net)
		fmt.Println()
		fmt.Println("By category:")

		type kv struct {
			k string
			v float64
		}
		var sorted []kv
		for k, v := range sum.ByCategory {
			sorted = append(sorted, kv{k, v})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].v < sorted[j].v })
		for _, kv := range sorted {
			cat := kv.k
			if cat == "" {
				cat = "(uncategorized)"
			}
			fmt.Printf("  %-24s %+10.2f €\n", cat, kv.v)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(summaryCmd)
	summaryCmd.Flags().StringP("month", "m", "", "Month (YYYY-MM, default: current)")
	summaryCmd.Flags().Bool("json", false, "Output as JSON")
}
