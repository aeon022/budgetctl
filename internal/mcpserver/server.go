package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func Serve() error {
	s := server.NewMCPServer("budgetctl", "0.1.0",
		server.WithToolCapabilities(true),
	)
	s.AddTool(toolList(), handleList)
	s.AddTool(toolSummary(), handleSummary)
	s.AddTool(toolImport(), handleImport)
	s.AddTool(toolAddTx(), handleAddTx)
	s.AddTool(toolDeleteTx(), handleDeleteTx)
	s.AddTool(toolTag(), handleTag)
	s.AddTool(toolApplyRules(), handleApplyRules)
	s.AddTool(toolListGoals(), handleListGoals)
	s.AddTool(toolSetGoal(), handleSetGoal)
	s.AddTool(toolDeleteGoal(), handleDeleteGoal)
	s.AddTool(toolDetectRecurring(), handleDetectRecurring)
	return server.ServeStdio(s)
}

func toolList() mcp.Tool {
	return mcp.NewTool("list_transactions",
		mcp.WithDescription("List transactions from the local database. Filter by month, category, or search term. Returns date, amount, category, description."),
		mcp.WithString("month", mcp.Description("Filter by month (YYYY-MM, default: current month)")),
		mcp.WithString("category", mcp.Description("Filter by category")),
		mcp.WithString("query", mcp.Description("Search in description")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 50)")),
	)
}

func toolSummary() mcp.Tool {
	return mcp.NewTool("budget_summary",
		mcp.WithDescription("Monthly income/expense summary with category breakdown. Great for AI analysis of spending patterns."),
		mcp.WithString("month", mcp.Description("Month (YYYY-MM, default: current month)")),
		mcp.WithString("account", mcp.Description("Filter to one account (e.g. \"N26\"), default: all accounts combined")),
	)
}

func toolAddTx() mcp.Tool {
	return mcp.NewTool("add_transaction",
		mcp.WithDescription("Add a manual income or expense entry. Negative amount = expense, positive = income. Use this when the user mentions a purchase, bill, or income that is not in a bank export."),
		mcp.WithString("description", mcp.Required(), mcp.Description("What the money was for, e.g. 'Coffee at Balthasar'")),
		mcp.WithNumber("amount", mcp.Required(), mcp.Description("Amount in EUR; negative for expenses (e.g. -4.5), positive for income")),
		mcp.WithString("date", mcp.Description("Date YYYY-MM-DD (default: today)")),
		mcp.WithString("category", mcp.Description("Category, e.g. groceries, dining, income")),
	)
}

func toolDeleteTx() mcp.Tool {
	return mcp.NewTool("delete_transaction",
		mcp.WithDescription("Delete a transaction by its ID (as returned by list_transactions)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Transaction ID")),
	)
}

func toolImport() mcp.Tool {
	return mcp.NewTool("import_transactions",
		mcp.WithDescription("Import transactions from a bank CSV file. Supports N26, ING, DKB, and generic CSV formats."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the CSV file")),
		mcp.WithString("account", mcp.Description("Account name override")),
	)
}

func toolTag() mcp.Tool {
	return mcp.NewTool("tag_transactions",
		mcp.WithDescription("Create a category rule: any transaction description matching the pattern will get the given category. Apply to existing transactions with apply=true."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Substring to match in description (case-insensitive)")),
		mcp.WithString("category", mcp.Required(), mcp.Description("Category name to assign")),
		mcp.WithBoolean("apply", mcp.Description("Also apply all rules to existing transactions")),
	)
}

func toolApplyRules() mcp.Tool {
	return mcp.NewTool("apply_category_rules",
		mcp.WithDescription("Re-apply all saved category rules to all transactions in the database."),
	)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	month := req.GetString("month", "")
	category := req.GetString("category", "")
	query := req.GetString("query", "")
	limit := int(req.GetFloat("limit", 50))
	if month == "" {
		month = time.Now().Format("2006-01")
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	txs, err := s.List(context.Background(), store.Filter{
		Month:    month,
		Category: category,
		Query:    query,
		Limit:    limit,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(txs) == 0 {
		return mcp.NewToolResultText("No transactions found. Import a CSV file first."), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d transactions (%s):\n\n", len(txs), month))
	for _, t := range txs {
		cat := t.Category
		if cat == "" {
			cat = "(uncategorized)"
		}
		b.WriteString(fmt.Sprintf("%s  %+8.2f€  %-18s  %s\n",
			t.Date.Format("2006-01-02"), t.Amount, cat, t.Description))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func handleSummary(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	month := req.GetString("month", "")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	account := req.GetString("account", "")

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	sum, err := s.Summary(context.Background(), month, account)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var b strings.Builder
	if account != "" {
		b.WriteString(fmt.Sprintf("Budget summary for %s (account: %s):\n\n", month, account))
	} else {
		b.WriteString(fmt.Sprintf("Budget summary for %s:\n\n", month))
	}
	b.WriteString(fmt.Sprintf("Income:   %+.2f €\n", sum.Income))
	b.WriteString(fmt.Sprintf("Expenses: %+.2f €\n", sum.Expenses))
	b.WriteString(fmt.Sprintf("Net:      %+.2f €\n\n", sum.Net))
	b.WriteString("By category:\n")

	type kv struct {
		k string
		v float64
	}
	var sorted []kv
	for k, v := range sum.ByCategory {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v < sorted[j].v })
	for _, item := range sorted {
		cat := item.k
		if cat == "" {
			cat = "(uncategorized)"
		}
		b.WriteString(fmt.Sprintf("  %-22s %+.2f €\n", cat, item.v))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func handleAddTx(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	desc := req.GetString("description", "")
	amount := req.GetFloat("amount", 0)
	dateStr := req.GetString("date", "")
	category := req.GetString("category", "")
	if desc == "" {
		return mcp.NewToolResultError("description is required"), nil
	}
	if amount == 0 {
		return mcp.NewToolResultError("amount must be non-zero (negative = expense, positive = income)"), nil
	}

	date := time.Now()
	if dateStr != "" {
		var err error
		date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid date %q (use YYYY-MM-DD)", dateStr)), nil
		}
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	ctx := context.Background()

	if category == "" {
		rules, _ := s.ListRules(ctx)
		category = budget.Categorize(desc, rules)
	}

	t := &models.Transaction{
		ID:          fmt.Sprintf("manual-%d", time.Now().UnixNano()),
		Date:        date,
		Description: desc,
		Amount:      amount,
		Category:    category,
		Account:     "manual",
		Source:      "mcp",
	}
	if err := s.Upsert(ctx, t); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Added: %s %+.2f€ on %s (id: %s, category: %s)",
		desc, amount, date.Format("2006-01-02"), t.ID, orDash(category))), nil
}

func handleDeleteTx(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := req.GetString("id", "")
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	if err := s.Delete(context.Background(), id); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Deleted transaction " + id), nil
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func handleImport(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := req.GetString("path", "")
	account := req.GetString("account", "")
	if path == "" {
		return mcp.NewToolResultError("path is required"), nil
	}

	txs, err := budget.Import(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	ctx := context.Background()
	rules, _ := s.ListRules(ctx)

	for i := range txs {
		if account != "" {
			txs[i].Account = account
		}
		if txs[i].Category == "" {
			txs[i].Category = budget.Categorize(txs[i].Description, rules)
		}
		_ = s.Upsert(ctx, &txs[i])
	}
	return mcp.NewToolResultText(fmt.Sprintf("Imported %d transactions from %s", len(txs), path)), nil
}

func handleTag(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pattern := req.GetString("pattern", "")
	category := req.GetString("category", "")
	apply := req.GetBool("apply", false)

	if pattern == "" || category == "" {
		return mcp.NewToolResultError("pattern and category are required"), nil
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.SaveRule(ctx, pattern, category); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	result := fmt.Sprintf("Saved rule: %q → %s", pattern, category)
	if apply {
		n, _ := s.ApplyRules(ctx)
		result += fmt.Sprintf("\nApplied to %d transactions", n)
	}
	return mcp.NewToolResultText(result), nil
}

func toolListGoals() mcp.Tool {
	return mcp.NewTool("list_budget_goals",
		mcp.WithDescription("List all budget goals with current-month spending progress. Shows spent vs. budget and remaining amount."),
		mcp.WithString("month", mcp.Description("Month for spend comparison (YYYY-MM, default: current)")),
	)
}

func toolSetGoal() mcp.Tool {
	return mcp.NewTool("set_budget_goal",
		mcp.WithDescription("Set or update a monthly spending limit for a category."),
		mcp.WithString("category", mcp.Required(), mcp.Description("Category name (must match category in transactions)")),
		mcp.WithNumber("monthly", mcp.Required(), mcp.Description("Monthly budget limit in euros (positive number)")),
	)
}

func toolDeleteGoal() mcp.Tool {
	return mcp.NewTool("delete_budget_goal",
		mcp.WithDescription("Remove a budget goal for a category."),
		mcp.WithString("category", mcp.Required(), mcp.Description("Category name")),
	)
}

func handleListGoals(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	month := req.GetString("month", "")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	statuses, err := s.GoalStatuses(context.Background(), month)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(statuses) == 0 {
		return mcp.NewToolResultText("No budget goals set."), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Budget goals for %s:\n\n", month))
	for _, gs := range statuses {
		status := "ok"
		if gs.Percent >= 100 {
			status = "OVER BUDGET"
		} else if gs.Percent >= 80 {
			status = "warning"
		}
		b.WriteString(fmt.Sprintf("%-20s  spent %.2f / %.2f €  (%.0f%%)  [%s]\n",
			gs.Category, gs.Spent, gs.Monthly, gs.Percent, status))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func handleSetGoal(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	category := req.GetString("category", "")
	monthly := req.GetFloat("monthly", 0)
	if category == "" || monthly <= 0 {
		return mcp.NewToolResultError("category and positive monthly amount are required"), nil
	}
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	if err := s.SaveGoal(context.Background(), category, monthly); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Goal set: %s = %.2f €/month", category, monthly)), nil
}

func handleDeleteGoal(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	category := req.GetString("category", "")
	if category == "" {
		return mcp.NewToolResultError("category is required"), nil
	}
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	if err := s.DeleteGoal(context.Background(), category); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Goal deleted: %s", category)), nil
}

func toolDetectRecurring() mcp.Tool {
	return mcp.NewTool("detect_recurring_payments",
		mcp.WithDescription("Scan all transactions for recurring payment patterns (subscriptions, rent, utilities). Returns detected patterns with frequency, typical amount, and last seen date."),
	)
}

func handleDetectRecurring(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	txs, err := s.List(context.Background(), store.Filter{})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	patterns := budget.DetectRecurring(txs)
	if len(patterns) == 0 {
		return mcp.NewToolResultText("No recurring patterns detected."), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Detected %d recurring payments:\n\n", len(patterns)))
	for _, p := range patterns {
		cat := p.Category
		if cat == "" {
			cat = "(uncategorized)"
		}
		b.WriteString(fmt.Sprintf("%-30s  %.2f €/%-8s  %-18s  last: %s  seen %dx\n",
			truncateStr(p.Description, 30), p.Amount, p.Frequency, cat,
			p.LastSeen.Format("2006-01-02"), p.Count))
	}
	return mcp.NewToolResultText(b.String()), nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func handleApplyRules(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()
	n, err := s.ApplyRules(context.Background())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Applied rules to %d transactions", n)), nil
}
