package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/store"
)

// ── Commands ──────────────────────────────────────────────────────────────────

// parseImportCmd parses path for the preview step — no DB write yet.
func parseImportCmd(path string) tea.Cmd {
	return func() tea.Msg {
		txs, err := budget.Import(path)
		return importParsedMsg{txs: txs, err: err}
	}
}

// runImportCmd performs the actual import (upsert + optional AI
// categorization) after the user confirms the preview. account overrides
// every parsed transaction's account field when non-empty (see the "t"
// binding in the preview step); an empty account leaves each row's
// bank-detected account (or "" for generic CSVs) untouched.
func runImportCmd(path, account string, useAI bool) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return importDoneMsg{err: err}
		}
		defer s.Close()
		res, err := budget.ImportFile(context.Background(), s, path, account, useAI)
		return importDoneMsg{res: res, err: err}
	}
}

// loadCmd fetches transactions for month/account, unfiltered by search text
// — search is applied client-side (filterTxs) over the result, live as the
// user types, rather than round-tripping to SQLite on every keystroke or
// baking a LIKE clause into the query. Store.Filter.Query / the SQL LIKE
// path still exists and is still used by the CLI (`budgetctl list --query`),
// just not from here anymore.
func loadCmd(month, account, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()

		txs, err := s.List(ctx, store.Filter{Month: month, Account: account, Category: category, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		months, _ := s.ListMonths(ctx)
		accounts, _ := s.ListAccounts(ctx)
		categories, _ := s.ListCategories(ctx)

		// summary for active month (and account, if one is selected) — NOT
		// scoped to the category filter: the point of the summary is to
		// see spend ACROSS categories, filtering it to one category would
		// make the "By category" breakdown show just that one row.
		sum, _ := s.Summary(ctx, month, account)

		// goals with current-month spend (always across all accounts — a
		// budget goal like "dining < 200€" isn't naturally per-account)
		goals, _ := s.GoalStatuses(ctx, month)

		trend, _ := s.MonthlyTrend(ctx, account, 12)

		// Recurring-payment detection needs the full history, not just the
		// active month — reuses the same detector as the `recurring` CLI
		// command, just surfaced in the summary popup too.
		var recurring []budget.RecurringPattern
		if allTxs, err := s.List(ctx, store.Filter{}); err == nil {
			recurring = budget.DetectRecurring(allTxs)
		}

		return txLoadedMsg{txs: txs, months: months, accounts: accounts, categories: categories, sum: sum, goals: goals, trend: trend, recurring: recurring}
	}
}

// loadSearchCmd fetches every transaction in the current account/category
// scope, unbounded by month — "/" search used to only see whatever the
// active month tab had loaded, so a match sitting in any other month was
// simply invisible until you clicked through tabs to find it. No Limit (all
// 880-ish rows for a real account is nothing for local SQLite), matching
// what DetectRecurring already does for the same reason.
func loadSearchCmd(account, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return searchLoadedMsg{}
		}
		defer s.Close()
		txs, _ := s.List(context.Background(), store.Filter{Account: account, Category: category})
		return searchLoadedMsg{txs: txs}
	}
}
