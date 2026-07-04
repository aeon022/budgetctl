package models

// BudgetGoal defines a monthly spending limit for a category.
type BudgetGoal struct {
	Category string
	Monthly  float64
}

// GoalStatus extends a BudgetGoal with current-month spend data.
type GoalStatus struct {
	BudgetGoal
	Spent     float64
	Remaining float64
	Percent   float64 // 0–100+
}
