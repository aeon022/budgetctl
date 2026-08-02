package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var goalCmd = &cobra.Command{
	Use:   "goal",
	Short: "Manage monthly budget goals per category",
}

var goalSetCmd = &cobra.Command{
	Use:   "set <category> <amount>",
	Short: "Set a monthly budget goal for a category",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		category := args[0]
		amount, err := strconv.ParseFloat(args[1], 64)
		if err != nil || amount <= 0 {
			return fmt.Errorf("amount must be a positive number")
		}

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()

		if err := s.SaveGoal(context.Background(), category, amount); err != nil {
			return err
		}
		fmt.Printf("Goal set: %s = %.2f €/month\n", category, amount)
		return nil
	},
}

var goalDeleteCmd = &cobra.Command{
	Use:   "delete <category>",
	Short: "Remove a budget goal",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.DeleteGoal(context.Background(), args[0]); err != nil {
			return err
		}
		fmt.Printf("Goal deleted: %s\n", args[0])
		return nil
	},
}

// goalAlertStatus mirrors the terminal output's ok/warn/OVER thresholds so
// JSON consumers (like the suite-wide today dashboard) don't have to
// reimplement the 80%/100% cutoffs themselves.
func goalAlertStatus(pct float64) string {
	if pct >= 100 {
		return "over"
	}
	if pct >= 80 {
		return "warn"
	}
	return "ok"
}

var goalListCmd = &cobra.Command{
	Use:   "list",
	Short: "List budget goals with current month's progress",
	RunE: func(cmd *cobra.Command, args []string) error {
		month, _ := cmd.Flags().GetString("month")
		asJSON, _ := cmd.Flags().GetBool("json")
		if month == "" {
			month = time.Now().Format("2006-01")
		}

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return err
		}
		defer s.Close()

		statuses, err := s.GoalStatuses(context.Background(), month)
		if err != nil {
			return err
		}

		if asJSON {
			type goalAlert struct {
				models.GoalStatus
				Status string `json:"status"`
			}
			data := make([]goalAlert, 0, len(statuses))
			alerts := 0
			for _, gs := range statuses {
				st := goalAlertStatus(gs.Percent)
				if st != "ok" {
					alerts++
				}
				data = append(data, goalAlert{GoalStatus: gs, Status: st})
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"tool": "budgetctl", "command": "goal list",
				"month": month, "alerts": alerts,
				"data": data,
			})
		}

		if len(statuses) == 0 {
			fmt.Println("No budget goals set. Use: budgetctl goal set <category> <amount>")
			return nil
		}

		fmt.Printf("── Goals: %s ──────────────────────────────\n\n", month)
		for _, gs := range statuses {
			bar := progressBar(gs.Percent, 20)
			status := "ok"
			if gs.Percent >= 100 {
				status = "OVER"
			} else if gs.Percent >= 80 {
				status = "warn"
			}
			fmt.Printf("  %-20s  %s %5.0f%%  spent %.2f / %.2f €  [%s]\n",
				gs.Category, bar, math.Min(gs.Percent, 999), gs.Spent, gs.Monthly, status)
		}
		return nil
	},
}

func progressBar(pct float64, width int) string {
	filled := int(math.Round(pct / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func init() {
	rootCmd.AddCommand(goalCmd)
	goalCmd.AddCommand(goalSetCmd)
	goalCmd.AddCommand(goalDeleteCmd)
	goalCmd.AddCommand(goalListCmd)
	goalListCmd.Flags().StringP("month", "m", "", "Month (YYYY-MM, default: current)")
	goalListCmd.Flags().Bool("json", false, "Output as JSON")
}
