package budget

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/aeon022/budgetctl/internal/models"
)

// RecurringPattern describes a detected recurring payment.
type RecurringPattern struct {
	Description string
	Category    string
	Amount      float64 // typical amount (abs value)
	Frequency   string  // "monthly", "weekly", "annual"
	LastSeen    time.Time
	Count       int
}

// DetectRecurring scans transactions for recurring payment patterns.
// It groups by normalized description+amount, then checks date intervals.
func DetectRecurring(txs []models.Transaction) []RecurringPattern {
	type bucket struct {
		desc     string
		category string
		amounts  []float64
		dates    []time.Time
	}

	groups := map[string]*bucket{}
	for _, t := range txs {
		if t.Amount >= 0 {
			continue // only expenses
		}
		key := normalizeDesc(t.Description)
		if key == "" {
			continue
		}
		b, ok := groups[key]
		if !ok {
			b = &bucket{desc: t.Description, category: t.Category}
			groups[key] = b
		}
		b.amounts = append(b.amounts, -t.Amount) // store as positive
		b.dates = append(b.dates, t.Date)
		// prefer most-recent description
		if t.Date.After(b.dates[len(b.dates)-1]) {
			b.desc = t.Description
			b.category = t.Category
		}
	}

	var out []RecurringPattern
	for _, b := range groups {
		if len(b.dates) < 2 {
			continue
		}
		sort.Slice(b.dates, func(i, j int) bool { return b.dates[i].Before(b.dates[j]) })
		sort.Float64s(b.amounts)

		// check amount consistency: median ±30%
		median := b.amounts[len(b.amounts)/2]
		if median < 1 {
			continue
		}
		consistent := true
		for _, a := range b.amounts {
			if math.Abs(a-median)/median > 0.30 {
				consistent = false
				break
			}
		}
		if !consistent {
			continue
		}

		freq := detectFrequency(b.dates)
		if freq == "" {
			continue
		}

		out = append(out, RecurringPattern{
			Description: b.desc,
			Category:    b.category,
			Amount:      math.Round(median*100) / 100,
			Frequency:   freq,
			LastSeen:    b.dates[len(b.dates)-1],
			Count:       len(b.dates),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out
}

func detectFrequency(dates []time.Time) string {
	if len(dates) < 2 {
		return ""
	}
	var gaps []float64
	for i := 1; i < len(dates); i++ {
		gap := dates[i].Sub(dates[i-1]).Hours() / 24
		gaps = append(gaps, gap)
	}
	sort.Float64s(gaps)
	median := gaps[len(gaps)/2]

	switch {
	case median >= 25 && median <= 35:
		return "monthly"
	case median >= 5 && median <= 9:
		return "weekly"
	case median >= 340 && median <= 390:
		return "annual"
	default:
		return ""
	}
}

// normalizeDesc produces a stable grouping key from a transaction description.
func normalizeDesc(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// strip noise suffixes: reference codes, IBANs, numbers in parens
	var words []string
	for _, w := range strings.Fields(s) {
		// skip tokens that look like reference codes (>= 6 chars of digits)
		if isRefCode(w) {
			continue
		}
		// keep only alpha words
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) {
				return r
			}
			return -1
		}, w)
		if len(clean) >= 3 {
			words = append(words, clean)
		}
	}
	if len(words) == 0 {
		return ""
	}
	// take first 3 meaningful words as key
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}

func isRefCode(s string) bool {
	digits := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return digits >= 5
}
