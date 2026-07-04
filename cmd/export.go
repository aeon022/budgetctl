package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export transactions to CSV or JSON (for tax reporting)",
	RunE: func(cmd *cobra.Command, args []string) error {
		year, _ := cmd.Flags().GetInt("year")
		format, _ := cmd.Flags().GetString("format")
		output, _ := cmd.Flags().GetString("output")

		if year == 0 {
			year = time.Now().Year()
		}

		s, err := store.New(config.DBPath())
		if err != nil {
			return err
		}
		defer s.Close()

		var txs []struct {
			Date        string
			Description string
			Amount      string
			Category    string
			Account     string
		}

		ctx := context.Background()
		for m := 1; m <= 12; m++ {
			month := fmt.Sprintf("%04d-%02d", year, m)
			batch, err := s.List(ctx, store.Filter{Month: month})
			if err != nil {
				return err
			}
			for _, t := range batch {
				txs = append(txs, struct {
					Date, Description, Amount, Category, Account string
				}{
					Date:        t.Date.Format("2006-01-02"),
					Description: t.Description,
					Amount:      fmt.Sprintf("%.2f", t.Amount),
					Category:    t.Category,
					Account:     t.Account,
				})
			}
		}

		w := os.Stdout
		if output != "" {
			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer f.Close()
			w = f
		}

		switch format {
		case "json":
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(txs)
		default: // csv
			cw := csv.NewWriter(w)
			_ = cw.Write([]string{"date", "description", "amount", "category", "account"})
			for _, t := range txs {
				_ = cw.Write([]string{t.Date, t.Description, t.Amount, t.Category, t.Account})
			}
			cw.Flush()
			if output != "" {
				fmt.Fprintf(os.Stderr, "Exported %d transactions → %s\n", len(txs), output)
			}
			return cw.Error()
		}
	},
}

func init() {
	rootCmd.AddCommand(exportCmd)
	exportCmd.Flags().Int("year", 0, "Year to export (default: current year)")
	exportCmd.Flags().String("format", "csv", "Output format: csv | json")
	exportCmd.Flags().StringP("output", "o", "", "Output file (default: stdout)")
}
