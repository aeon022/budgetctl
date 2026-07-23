package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tx(id string, amount float64) *models.Transaction {
	return &models.Transaction{
		ID:          id,
		Date:        time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		Description: "Test entry",
		Amount:      amount,
		Account:     "manual",
		Source:      "tui",
	}
}

func TestUpsertAndList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, tx("t1", -12.50)); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Amount != -12.50 {
		t.Fatalf("unexpected list: %+v", got)
	}
}

func TestUpdate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Upsert(ctx, tx("t1", -10)); err != nil {
		t.Fatal(err)
	}

	updated := tx("t1", -25.99)
	updated.Description = "Edited"
	updated.Category = "groceries"
	updated.Date = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}

	got, _ := s.List(ctx, Filter{})
	if len(got) != 1 {
		t.Fatalf("want 1 tx, got %d", len(got))
	}
	g := got[0]
	if g.Description != "Edited" || g.Amount != -25.99 || g.Category != "groceries" {
		t.Errorf("update not applied: %+v", g)
	}
	if g.Date.Format("2006-01-02") != "2026-07-01" {
		t.Errorf("date not updated: %v", g.Date)
	}
}

func TestUpdateMissing(t *testing.T) {
	s := testStore(t)
	if err := s.Update(context.Background(), tx("nope", 1)); err == nil {
		t.Fatal("want error for unknown id")
	}
}

func TestDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Upsert(ctx, tx("t1", -5)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List(ctx, Filter{})
	if len(got) != 0 {
		t.Fatalf("tx not deleted: %+v", got)
	}
	if err := s.Delete(ctx, "t1"); err == nil {
		t.Fatal("want error for double delete")
	}
}

func TestSetCategoryAndSummary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Upsert(ctx, tx("t1", -100)); err != nil {
		t.Fatal(err)
	}
	income := tx("t2", 2000)
	if err := s.Upsert(ctx, income); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCategory(ctx, "t1", "rent"); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summary(ctx, "2026-07", "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Income != 2000 || sum.Expenses != -100 {
		t.Errorf("unexpected summary: income %v, expenses %v", sum.Income, sum.Expenses)
	}
	if sum.ByCategory["rent"] != -100 {
		t.Errorf("category totals wrong: %+v", sum.ByCategory)
	}
}

func TestSummaryMixedSignsWithinOneCategoryAreNotNetted(t *testing.T) {
	// Regression test: category-level netting (GROUP BY category, then
	// classify the NET as income-or-expense) silently swallowed income
	// whenever a category's expenses outweighed its income — found with
	// real imported bank data where an uncategorized P2P income row sat
	// alongside much larger uncategorized expenses in the same month.
	s := testStore(t)
	ctx := context.Background()

	expense := tx("e1", -2728.33) // Category "" by default via the tx() helper
	if err := s.Upsert(ctx, expense); err != nil {
		t.Fatal(err)
	}
	income := tx("i1", 183.00) // same "" category, net for the category is negative
	if err := s.Upsert(ctx, income); err != nil {
		t.Fatal(err)
	}

	sum, err := s.Summary(ctx, "2026-07", "")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Income != 183.00 {
		t.Errorf("expected income 183.00 to survive despite a larger expense in the same category, got %v", sum.Income)
	}
	if sum.Expenses != -2728.33 {
		t.Errorf("expected expenses -2728.33, got %v", sum.Expenses)
	}
	if sum.Net != 183.00-2728.33 {
		t.Errorf("expected net %v, got %v", 183.00-2728.33, sum.Net)
	}
}

func TestSummaryFiltersByAccount(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	a := tx("a1", -50)
	a.Account = "N26"
	if err := s.Upsert(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := tx("b1", -30)
	b.Account = "ING"
	if err := s.Upsert(ctx, b); err != nil {
		t.Fatal(err)
	}

	all, err := s.Summary(ctx, "2026-07", "")
	if err != nil {
		t.Fatal(err)
	}
	if all.Expenses != -80 {
		t.Errorf("expected combined expenses -80 across both accounts, got %v", all.Expenses)
	}

	n26, err := s.Summary(ctx, "2026-07", "N26")
	if err != nil {
		t.Fatal(err)
	}
	if n26.Expenses != -50 {
		t.Errorf("expected N26-only expenses -50, got %v", n26.Expenses)
	}
}

func TestListAccounts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	a := tx("a1", -1)
	a.Account = "N26"
	b := tx("b1", -1)
	b.Account = "ING"
	c := tx("c1", -1)
	c.Account = "" // generic import with no detected account — must not show up as a phantom "" entry
	for _, t2 := range []*models.Transaction{a, b, c} {
		if err := s.Upsert(ctx, t2); err != nil {
			t.Fatal(err)
		}
	}

	accts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"N26": true, "ING": true}
	if len(accts) != 2 {
		t.Fatalf("expected 2 distinct accounts (empty excluded), got %+v", accts)
	}
	for _, a := range accts {
		if !want[a] {
			t.Errorf("unexpected account %q in list", a)
		}
	}
}

func TestMonthlyTrend(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	dated := func(id string, month string, amount float64) *models.Transaction {
		tr := tx(id, amount)
		d, err := time.Parse("2006-01-02", month+"-15")
		if err != nil {
			t.Fatal(err)
		}
		tr.Date = d
		return tr
	}
	for _, tr := range []*models.Transaction{
		dated("m1", "2026-05", -100),
		dated("m2", "2026-05", 500),
		dated("m3", "2026-06", -50),
		dated("m4", "2026-07", -20),
		dated("m5", "2026-07", 300),
	} {
		if err := s.Upsert(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}

	points, err := s.MonthlyTrend(ctx, "", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 months of data, got %d: %+v", len(points), points)
	}
	// oldest-first ordering
	if points[0].Month != "2026-05" || points[len(points)-1].Month != "2026-07" {
		t.Errorf("expected oldest-first ordering, got %+v", points)
	}
	if points[0].Income != 500 || points[0].Expenses != -100 || points[0].Net != 400 {
		t.Errorf("unexpected May point: %+v", points[0])
	}
	if points[2].Income != 300 || points[2].Expenses != -20 || points[2].Net != 280 {
		t.Errorf("unexpected July point: %+v", points[2])
	}

	limited, err := s.MonthlyTrend(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].Month != "2026-06" || limited[1].Month != "2026-07" {
		t.Errorf("expected limit to keep the 2 MOST RECENT months, oldest-first, got %+v", limited)
	}
}
