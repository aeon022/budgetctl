package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/tuitest"
)

// TestRenderLandingMockups writes an ANSI screenshot of the real TUI (made-up demo data) for the landing
// page: BUDGETCTL_MOCKUP_DIR=/dir go test ./internal/tui -run TestRenderLandingMockups
func TestRenderLandingMockups(t *testing.T) {
	dir := os.Getenv("BUDGETCTL_MOCKUP_DIR")
	if dir == "" {
		t.Skip("set BUDGETCTL_MOCKUP_DIR to render landing mockups")
	}
	isolate(t)
	now := time.Now()
	day := func(d int) time.Time { return time.Date(now.Year(), now.Month(), max(now.Day()-d, 1), 12, 0, 0, 0, now.Location()) }
	tx := func(id string, d int, payee, desc, cat string, amt float64) models.Transaction {
		return models.Transaction{ID: id, Date: day(d), Payee: payee, Description: desc, Category: cat, Account: "Girokonto", Amount: amt}
	}
	txs := []models.Transaction{
		tx("1", 0, "Billa", "Groceries", "Groceries", -42.80),
		tx("2", 1, "Nordlicht GmbH", "Invoice 2026-10", "Income", 1850.00),
		tx("3", 2, "Spotify", "Subscription", "Subscriptions", -10.99),
		tx("4", 3, "Café Central", "Lunch with Mara", "Eating out", -18.40),
		tx("5", 4, "Wiener Linien", "Monthly pass", "Transport", -51.00),
		tx("6", 5, "Hofer", "Groceries", "Groceries", -67.35),
		tx("7", 6, "Hosting AG", "missionctl.sh", "Work", -12.00),
		tx("8", 7, "Landlord", "Rent October", "Housing", -820.00),
	}
	m := New()
	mi, _ := tuitest.Send(m, tuitest.Resize(118, 16), txLoadedMsg{txs: txs, months: []string{now.Format("2006-01")}, accounts: []string{"Girokonto"}, categories: []string{"Groceries", "Income", "Subscriptions", "Eating out", "Transport", "Work", "Housing"}})
	out := mi.(Model).View().Content
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "budgetctl.ansi"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}
