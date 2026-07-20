package mcpserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/viper"
)

// setupTestDB points config.DBPath() at a temporary database via the existing
// viper "db_path" override and seeds it. budgetctl has no external
// integration (no AppleScript, no network) — every handler here is a pure
// local SQLite read/write, so all of them are safe to smoke-test directly.
func setupTestDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "budgetctl.db")
	viper.Set("db_path", path)
	t.Cleanup(func() { viper.Set("db_path", "") })

	s, err := store.New(path)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	month := time.Now().Format("2006-01")
	date, _ := time.Parse("2006-01", month)
	ctx := context.Background()
	txs := []*models.Transaction{
		{ID: "1", Date: date, Description: "Salary", Amount: 3000, Category: "income", Account: "checking", Source: "test"},
		{ID: "2", Date: date, Description: "Rent", Amount: -1200, Category: "housing", Account: "checking", Source: "test"},
	}
	for _, tx := range txs {
		if err := s.Upsert(ctx, tx); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
}

func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned an error result: %+v", res.Content)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestToolsAreRegisteredWithValidSchema(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool mcp.Tool
	}{
		{"list_transactions", toolList()},
		{"budget_summary", toolSummary()},
		{"import_transactions", toolImport()},
		{"add_transaction", toolAddTx()},
		{"delete_transaction", toolDeleteTx()},
		{"tag_transactions", toolTag()},
		{"apply_category_rules", toolApplyRules()},
		{"list_budget_goals", toolListGoals()},
		{"set_budget_goal", toolSetGoal()},
		{"delete_budget_goal", toolDeleteGoal()},
		{"detect_recurring_payments", toolDetectRecurring()},
	} {
		if tc.tool.Name != tc.name {
			t.Errorf("expected tool name %q, got %q", tc.name, tc.tool.Name)
		}
		if tc.tool.Description == "" {
			t.Errorf("tool %q has no description", tc.name)
		}
	}
}

func TestHandleList(t *testing.T) {
	setupTestDB(t)

	res := callTool(t, handleList, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "Salary") || !strings.Contains(text, "Rent") {
		t.Errorf("expected both seeded transactions in output, got:\n%s", text)
	}
}

func TestHandleSummary(t *testing.T) {
	setupTestDB(t)

	res := callTool(t, handleSummary, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "Income:   +3000.00") {
		t.Errorf("expected income in summary, got:\n%s", text)
	}
	if !strings.Contains(text, "Expenses: -1200.00") {
		t.Errorf("expected expenses in summary, got:\n%s", text)
	}
}

func TestHandleAddAndDeleteTx(t *testing.T) {
	setupTestDB(t)

	addRes := callTool(t, handleAddTx, map[string]any{
		"description": "Coffee",
		"amount":      -4.5,
	})
	addText := resultText(t, addRes)
	if !strings.Contains(addText, "Coffee") {
		t.Fatalf("expected confirmation of added tx, got:\n%s", addText)
	}

	listRes := callTool(t, handleList, nil)
	if !strings.Contains(resultText(t, listRes), "Coffee") {
		t.Fatal("expected newly added transaction to show up in list_transactions")
	}
}

func TestHandleAddTxRequiresDescriptionAndAmount(t *testing.T) {
	setupTestDB(t)

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"description": "no amount"}}}
	res, err := handleAddTx(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when amount is missing/zero")
	}
}

func TestHandleTagAndApplyRules(t *testing.T) {
	setupTestDB(t)

	callTool(t, handleTag, map[string]any{"pattern": "Rent", "category": "housing-fixed", "apply": true})

	res := callTool(t, handleList, map[string]any{"category": "housing-fixed"})
	text := resultText(t, res)
	if !strings.Contains(text, "Rent") {
		t.Errorf("expected Rent to be recategorized as housing-fixed, got:\n%s", text)
	}
}

func TestHandleGoals(t *testing.T) {
	setupTestDB(t)

	callTool(t, handleSetGoal, map[string]any{"category": "dining", "monthly": 200.0})

	res := callTool(t, handleListGoals, nil)
	text := resultText(t, res)
	if !strings.Contains(text, "dining") {
		t.Errorf("expected dining goal in output, got:\n%s", text)
	}

	callTool(t, handleDeleteGoal, map[string]any{"category": "dining"})
	res = callTool(t, handleListGoals, nil)
	if strings.Contains(resultText(t, res), "dining") {
		t.Error("expected dining goal to be removed after delete")
	}
}
