package budget

import (
	"testing"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
)

func tx(desc string, amount float64, date string) models.Transaction {
	d, _ := time.Parse("2006-01-02", date)
	return models.Transaction{Description: desc, Amount: amount, Date: d}
}

func TestDetectRecurringFindsMonthlyWeeklyAndAnnual(t *testing.T) {
	txs := []models.Transaction{
		// monthly, reference codes differ every month
		tx("Netflix Ref 1234567", -12.99, "2026-07-03"),
		tx("Netflix Ref 7654321", -12.99, "2026-08-03"),
		tx("Netflix Ref 1111111", -12.99, "2026-09-03"),
		// weekly
		tx("Markt Gemuese", -20, "2026-09-01"),
		tx("Markt Gemuese", -21, "2026-09-08"),
		tx("Markt Gemuese", -19, "2026-09-15"),
		// annual
		tx("Haftpflicht Versicherung", -120, "2024-11-01"),
		tx("Haftpflicht Versicherung", -120, "2025-11-01"),
	}
	got := map[string]string{}
	for _, p := range DetectRecurring(txs) {
		got[normalizeDesc(p.Description)] = p.Frequency
	}
	want := map[string]string{"netflix ref": "monthly", "markt gemuese": "weekly", "haftpflicht versicherung": "annual"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}

func TestDetectRecurringIgnoresIncomeOneOffsAndWildAmounts(t *testing.T) {
	txs := []models.Transaction{
		tx("Gehalt Firma", 2500, "2026-07-01"), // income is never a "payment"
		tx("Gehalt Firma", 2500, "2026-08-01"),
		tx("Einmalkauf Elektronik", -300, "2026-08-02"), // a single payment
		tx("Restaurant Besuch", -10, "2026-07-05"),      // amounts vary by more than 30 %
		tx("Restaurant Besuch", -60, "2026-08-05"),
		tx("Restaurant Besuch", -11, "2026-09-05"),
		tx("Cent Abo", -0.5, "2026-07-01"), // below 1 EUR is noise
		tx("Cent Abo", -0.5, "2026-08-01"),
	}
	if got := DetectRecurring(txs); len(got) != 0 {
		t.Errorf("expected nothing, got %+v", got)
	}
}

func TestDetectRecurringKeepsTheLatestDescriptionAndSortsByAmount(t *testing.T) {
	txs := []models.Transaction{
		tx("Spotify AB 3333333", -10, "2026-10-03"), // newest first on purpose
		tx("Spotify AB 1111111", -10, "2026-08-03"),
		tx("Spotify AB 2222222", -10, "2026-09-03"),
		tx("Fitness Studio", -40, "2026-08-10"),
		tx("Fitness Studio", -40, "2026-09-10"),
	}
	got := DetectRecurring(txs)
	if len(got) != 2 || got[0].Amount != 40 {
		t.Fatalf("want 2 patterns, biggest first: %+v", got)
	}
	if got[1].Description != "Spotify AB 3333333" || got[1].Count != 3 {
		t.Errorf("latest description expected, got %+v", got[1])
	}
}

func TestParseUserAmount(t *testing.T) {
	for in, want := range map[string]float64{"12.50": 12.5, "-12,50": -12.5, "1.250,00": 1250, "€ 5": 5, " -42 ": -42} {
		got, err := ParseUserAmount(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "12,5x"} {
		if _, err := ParseUserAmount(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
