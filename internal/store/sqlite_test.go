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
