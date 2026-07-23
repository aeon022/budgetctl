package budget

import (
	"context"
	"sort"

	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
)

// ImportResult summarizes what ImportFile actually did, for both the CLI
// and the TUI import assistant to report back to the user.
type ImportResult struct {
	Transactions  []models.Transaction // parsed rows, before any DB write
	Imported      int
	AICategorized int
}

// ImportFile parses path, upserts every row into s (auto-categorizing via
// existing rules), and optionally asks Claude to categorize whatever's
// still uncategorized afterward. Shared by the CLI `import` command and the
// TUI import assistant so both go through the exact same store/categorize
// logic instead of two copies drifting apart.
func ImportFile(ctx context.Context, s *store.Store, path, account string, useAI bool) (ImportResult, error) {
	txs, err := Import(path)
	if err != nil {
		return ImportResult{}, err
	}

	rules, _ := s.ListRules(ctx)
	res := ImportResult{Transactions: txs}

	for i := range txs {
		if account != "" {
			txs[i].Account = account
		}
		if txs[i].Category == "" && len(rules) > 0 {
			txs[i].Category = Categorize(txs[i].Payee+" "+txs[i].Description, rules)
		}
		if err := s.Upsert(ctx, &txs[i]); err == nil {
			res.Imported++
		}
	}

	if useAI {
		var uncategorized []models.Transaction
		for _, tx := range txs {
			if tx.Category == "" {
				uncategorized = append(uncategorized, tx)
			}
		}
		if len(uncategorized) > 0 {
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

			if aiMap, err := AICategories(ctx, uncategorized, cats); err == nil {
				for i := range txs {
					if txs[i].Category == "" {
						if cat, ok := aiMap[txs[i].Description]; ok && cat != "" {
							txs[i].Category = cat
							if err := s.Upsert(ctx, &txs[i]); err == nil {
								res.AICategorized++
							}
						}
					}
				}
			}
		}
	}

	return res, nil
}
