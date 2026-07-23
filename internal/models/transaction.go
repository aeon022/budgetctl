package models

import "time"

type Transaction struct {
	ID          string
	Date        time.Time
	Description string
	Amount      float64 // negative = expense, positive = income
	Category    string
	Account     string
	Source      string // filename imported from
	Raw         string // original CSV row
}

type CategoryRule struct {
	Pattern  string // substring match (case-insensitive)
	Category string
}

type Summary struct {
	Month      string
	Income     float64
	Expenses   float64
	Net        float64
	ByCategory map[string]float64
}

// MonthlyPoint is one point in a multi-month trend (Store.MonthlyTrend).
type MonthlyPoint struct {
	Month    string
	Income   float64
	Expenses float64
	Net      float64
}
