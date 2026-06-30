package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
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
	s.AddTool(toolTag(), handleTag)
	s.AddTool(toolApplyRules(), handleApplyRules)
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

	s, err := store.New(config.DBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.Close()

	sum, err := s.Summary(context.Background(), month)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Budget summary for %s:\n\n", month))
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
