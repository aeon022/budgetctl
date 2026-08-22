package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

// suggestCutsTrailingMonths is how many prior months feed the trailing
// average shown to the AI alongside this month's spend — enough to smooth
// out a one-off spike without dragging in stale, pre-habit-change spending.
const suggestCutsTrailingMonths = 3

var suggestCutsCmd = &cobra.Command{
	Use:   "suggest-cuts",
	Short: "AI-suggest budget cuts from this month's spending, approve to set as goals (missionctl Bundle feature)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !config.IsPro() {
			fmt.Println(config.ProFeatureMessage("AI budget cut suggestions"))
			return nil
		}

		month, _ := cmd.Flags().GetString("month")
		if month == "" {
			month = time.Now().Format("2006-01")
		}

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		ctx := context.Background()

		summary, err := s.Summary(ctx, month, "")
		if err != nil {
			return err
		}

		spend := make(map[string]float64, len(summary.ByCategory))
		for cat, total := range summary.ByCategory {
			if total < 0 { // expenses are negative in the DB
				spend[cat] = -total
			}
		}
		if len(spend) == 0 {
			fmt.Printf("No expenses found for %s.\n", month)
			return nil
		}

		trailingAvg, err := trailingCategoryAverage(ctx, s, month, suggestCutsTrailingMonths)
		if err != nil {
			return err
		}

		suggestions, err := budget.AISuggestCuts(ctx, spend, trailingAvg)
		if err != nil {
			return fmt.Errorf("AI suggest-cuts: %w", err)
		}
		if len(suggestions) == 0 {
			fmt.Println("No cut suggestions — nothing stood out this month.")
			return nil
		}

		fmt.Printf("── Suggested cuts: %s ──────────────────────\n\n", month)
		reader := bufio.NewReader(os.Stdin)
		applied := 0
		for _, sug := range suggestions {
			fmt.Printf("  %s\n    current: %.2f €/month  ->  suggested cap: %.2f €/month\n    %s\n",
				sug.Category, sug.CurrentMonthly, sug.SuggestedCap, sug.Reason)
			fmt.Print("  Approve this goal? [y/N]: ")
			line, _ := reader.ReadString('\n')
			if strings.EqualFold(strings.TrimSpace(line), "y") {
				if err := s.SaveGoal(ctx, sug.Category, sug.SuggestedCap); err != nil {
					return fmt.Errorf("save goal for %q: %w", sug.Category, err)
				}
				fmt.Printf("  -> goal set: %s = %.2f €/month\n\n", sug.Category, sug.SuggestedCap)
				applied++
			} else {
				fmt.Printf("  -> skipped\n\n")
			}
		}

		fmt.Printf("Applied %d/%d suggested goal(s).\n", applied, len(suggestions))
		return nil
	},
}

// trailingCategoryAverage averages each category's expense spend over the
// `months` calendar months preceding `month` (exclusive), reusing
// Store.Summary one month at a time rather than adding a new aggregation
// query — this command runs occasionally, not in a hot path. Months with no
// data at all (e.g. before the book started) are skipped rather than
// counted as zero, so they don't drag the average down artificially.
func trailingCategoryAverage(ctx context.Context, s *store.Store, month string, months int) (map[string]float64, error) {
	cur, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, fmt.Errorf("invalid month %q, want YYYY-MM: %w", month, err)
	}

	totals := make(map[string]float64)
	counted := 0
	for i := 1; i <= months; i++ {
		m := cur.AddDate(0, -i, 0).Format("2006-01")
		sum, err := s.Summary(ctx, m, "")
		if err != nil {
			return nil, err
		}
		if len(sum.ByCategory) == 0 {
			continue
		}
		counted++
		for cat, total := range sum.ByCategory {
			if total < 0 {
				totals[cat] += -total
			}
		}
	}
	if counted == 0 {
		return nil, nil
	}
	for cat := range totals {
		totals[cat] /= float64(counted)
	}
	return totals, nil
}

func init() {
	rootCmd.AddCommand(suggestCutsCmd)
	suggestCutsCmd.Flags().StringP("month", "m", "", "Month (YYYY-MM, default: current)")
}
