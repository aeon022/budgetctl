package cmd

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
)

func TestGoalAlertStatus(t *testing.T) {
	for pct, want := range map[float64]string{0: "ok", 79.9: "ok", 80: "warn", 99.99: "warn", 100: "over", 250: "over"} {
		if got := goalAlertStatus(pct); got != want {
			t.Errorf("goalAlertStatus(%v) = %q, want %q", pct, got, want)
		}
	}
}

func TestProgressBar(t *testing.T) {
	cases := []struct {
		pct   float64
		width int
		want  string
	}{
		{0, 10, "[" + strings.Repeat("░", 10) + "]"},
		{50, 10, "[" + strings.Repeat("█", 5) + strings.Repeat("░", 5) + "]"},
		{100, 4, "[████]"},
		{300, 4, "[████]"}, // over budget never overflows the bar
		{-20, 4, "[░░░░]"}, // nor underflows
	}
	for _, c := range cases {
		if got := progressBar(c.pct, c.width); got != c.want {
			t.Errorf("progressBar(%v,%d) = %q, want %q", c.pct, c.width, got, c.want)
		}
	}
}

func TestTrailingCategoryAverage(t *testing.T) {
	ctx := context.Background()
	s, err := store.New(filepath.Join(t.TempDir(), "b.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	add := func(id string, y int, mo time.Month, amount float64, cat string) {
		t.Helper()
		if err := s.Upsert(ctx, &models.Transaction{ID: id, Date: time.Date(y, mo, 10, 0, 0, 0, 0, time.UTC), Description: id, Amount: amount, Category: cat, Account: "N26"}); err != nil {
			t.Fatal(err)
		}
	}
	// Jan and Feb have data; Dec (empty) must be skipped, not averaged in as 0.
	add("a", 2026, time.January, -100, "Food")
	add("b", 2026, time.February, -50, "Food")
	add("c", 2026, time.February, -30, "Fun")
	add("inc", 2026, time.February, 2000, "Salary") // income never counts as spend

	got, err := trailingCategoryAverage(ctx, s, "2026-03", 3) // Feb, Jan, Dec
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got["Food"]-75) > 1e-9 || math.Abs(got["Fun"]-15) > 1e-9 {
		t.Errorf("averages = %v, want Food 75 (over 2 months with data), Fun 15", got)
	}
	if _, ok := got["Salary"]; ok {
		t.Error("income must not appear in the spend averages")
	}

	if got, err := trailingCategoryAverage(ctx, s, "2020-01", 3); err != nil || got != nil {
		t.Errorf("no data in window: got %v, %v; want nil, nil", got, err)
	}
	if _, err := trailingCategoryAverage(ctx, s, "March", 3); err == nil {
		t.Error("invalid month must error")
	}
}
