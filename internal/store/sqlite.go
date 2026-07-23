package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	return s, s.migrate()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id          TEXT PRIMARY KEY,
			date        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			amount      REAL NOT NULL DEFAULT 0,
			category    TEXT NOT NULL DEFAULT '',
			account     TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT '',
			raw         TEXT NOT NULL DEFAULT '',
			imported_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tx_date     ON transactions(date);
		CREATE INDEX IF NOT EXISTS idx_tx_category ON transactions(category);
		CREATE INDEX IF NOT EXISTS idx_tx_account  ON transactions(account);

		CREATE TABLE IF NOT EXISTS category_rules (
			pattern  TEXT PRIMARY KEY,
			category TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS budget_goals (
			category TEXT PRIMARY KEY,
			monthly  REAL NOT NULL DEFAULT 0
		);
	`)
	return err
}

func (s *Store) Upsert(ctx context.Context, t *models.Transaction) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO transactions (id,date,description,amount,category,account,source,raw,imported_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			category=excluded.category, imported_at=excluded.imported_at
	`,
		t.ID,
		t.Date.UTC().Format("2006-01-02"),
		t.Description,
		t.Amount,
		t.Category,
		t.Account,
		t.Source,
		t.Raw,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

type Filter struct {
	Month    string // "2026-01"
	Category string
	Account  string
	Query    string
	Limit    int
}

func (s *Store) List(ctx context.Context, f Filter) ([]models.Transaction, error) {
	q := `SELECT id,date,description,amount,category,account,source FROM transactions WHERE 1=1`
	var args []any
	if f.Month != "" {
		q += ` AND date LIKE ?`
		args = append(args, f.Month+"%")
	}
	if f.Category != "" {
		q += ` AND category=?`
		args = append(args, f.Category)
	}
	if f.Account != "" {
		q += ` AND account=?`
		args = append(args, f.Account)
	}
	if f.Query != "" {
		q += ` AND description LIKE ?`
		args = append(args, "%"+f.Query+"%")
	}
	q += ` ORDER BY date DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTx(rows)
}

func (s *Store) Summary(ctx context.Context, month, account string) (*models.Summary, error) {
	where := ` WHERE 1=1`
	var args []any
	if month != "" {
		where += ` AND date LIKE ?`
		args = append(args, month+"%")
	}
	if account != "" {
		where += ` AND account=?`
		args = append(args, account)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT category, SUM(amount) FROM transactions`+where+` GROUP BY category`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sum := &models.Summary{
		Month:      month,
		ByCategory: make(map[string]float64),
	}
	for rows.Next() {
		var cat string
		var total float64
		if err := rows.Scan(&cat, &total); err != nil {
			return nil, err
		}
		sum.ByCategory[cat] = total
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Income/Expenses are summed per-TRANSACTION sign, not per-category net —
	// a single category (esp. "" uncategorized) routinely holds both income
	// and expense rows, and netting them at the category level before
	// classifying would silently swallow whichever direction lost.
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount),0) FROM transactions`+where+` AND amount > 0`, args...,
	).Scan(&sum.Income); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount),0) FROM transactions`+where+` AND amount < 0`, args...,
	).Scan(&sum.Expenses); err != nil {
		return nil, err
	}
	sum.Net = sum.Income + sum.Expenses
	return sum, nil
}

func (s *Store) ListMonths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT substr(date,1,7) as month FROM transactions ORDER BY month DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var months []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		months = append(months, m)
	}
	return months, rows.Err()
}

// MonthlyTrend returns per-transaction-sign income/expenses/net for the
// most recent `limit` months that have any data, oldest first (so callers
// can render it left-to-right as a trend). account filters to one account
// when non-empty, like Summary/List.
func (s *Store) MonthlyTrend(ctx context.Context, account string, limit int) ([]models.MonthlyPoint, error) {
	where := ` WHERE 1=1`
	var args []any
	if account != "" {
		where += ` AND account=?`
		args = append(args, account)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(date,1,7) as month,
		       COALESCE(SUM(CASE WHEN amount > 0 THEN amount ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN amount < 0 THEN amount ELSE 0 END), 0)
		FROM transactions`+where+`
		GROUP BY month
		ORDER BY month DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []models.MonthlyPoint
	for rows.Next() {
		var p models.MonthlyPoint
		if err := rows.Scan(&p.Month, &p.Income, &p.Expenses); err != nil {
			return nil, err
		}
		p.Net = p.Income + p.Expenses
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	return points, nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT account FROM transactions WHERE account != '' ORDER BY account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		accts = append(accts, a)
	}
	return accts, rows.Err()
}

// SetCategory updates the category for a single transaction.
// Update rewrites date, description, amount, and category of an existing transaction.
func (s *Store) Update(ctx context.Context, t *models.Transaction) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE transactions SET date=?, description=?, amount=?, category=? WHERE id=?
	`,
		t.Date.UTC().Format("2006-01-02"),
		t.Description,
		t.Amount,
		t.Category,
		t.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transaction %q not found", t.ID)
	}
	return nil
}

// Delete removes a transaction permanently.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM transactions WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("transaction %q not found", id)
	}
	return nil
}

func (s *Store) SetCategory(ctx context.Context, id, category string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE transactions SET category=? WHERE id=?`, category, id)
	return err
}

// ── Category rules ────────────────────────────────────────────────────────────

func (s *Store) SaveRule(ctx context.Context, pattern, category string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO category_rules (pattern,category) VALUES (?,?)
		ON CONFLICT(pattern) DO UPDATE SET category=excluded.category
	`, strings.ToLower(pattern), category)
	return err
}

func (s *Store) ListRules(ctx context.Context) ([]models.CategoryRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pattern,category FROM category_rules ORDER BY pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []models.CategoryRule
	for rows.Next() {
		var r models.CategoryRule
		if err := rows.Scan(&r.Pattern, &r.Category); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ApplyRules re-categorizes all transactions using stored rules.
func (s *Store) ApplyRules(ctx context.Context) (int, error) {
	rules, err := s.ListRules(ctx)
	if err != nil {
		return 0, err
	}
	if len(rules) == 0 {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,description FROM transactions`)
	if err != nil {
		return 0, err
	}
	type row struct{ id, desc string }
	var all []row
	for rows.Next() {
		var r row
		_ = rows.Scan(&r.id, &r.desc)
		all = append(all, r)
	}
	rows.Close()

	count := 0
	for _, tx := range all {
		desc := strings.ToLower(tx.desc)
		for _, rule := range rules {
			if strings.Contains(desc, rule.Pattern) {
				_ = s.SetCategory(ctx, tx.id, rule.Category)
				count++
				break
			}
		}
	}
	return count, nil
}

// ── Budget goals ──────────────────────────────────────────────────────────────

func (s *Store) SaveGoal(ctx context.Context, category string, monthly float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO budget_goals (category, monthly) VALUES (?, ?)
		 ON CONFLICT(category) DO UPDATE SET monthly=excluded.monthly`,
		strings.ToLower(category), monthly)
	return err
}

func (s *Store) DeleteGoal(ctx context.Context, category string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM budget_goals WHERE category=?`, strings.ToLower(category))
	return err
}

func (s *Store) ListGoals(ctx context.Context) ([]models.BudgetGoal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category, monthly FROM budget_goals ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var goals []models.BudgetGoal
	for rows.Next() {
		var g models.BudgetGoal
		if err := rows.Scan(&g.Category, &g.Monthly); err != nil {
			return nil, err
		}
		goals = append(goals, g)
	}
	return goals, rows.Err()
}

func (s *Store) GoalStatuses(ctx context.Context, month string) ([]models.GoalStatus, error) {
	goals, err := s.ListGoals(ctx)
	if err != nil {
		return nil, err
	}
	sum, err := s.Summary(ctx, month, "") // goals are budgeted across all accounts combined
	if err != nil {
		return nil, err
	}
	var out []models.GoalStatus
	for _, g := range goals {
		spent := -sum.ByCategory[g.Category] // expenses are negative in DB
		if spent < 0 {
			spent = 0
		}
		remaining := g.Monthly - spent
		pct := 0.0
		if g.Monthly > 0 {
			pct = (spent / g.Monthly) * 100
		}
		out = append(out, models.GoalStatus{
			BudgetGoal: g,
			Spent:      spent,
			Remaining:  remaining,
			Percent:    pct,
		})
	}
	return out, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func scanTx(rows *sql.Rows) ([]models.Transaction, error) {
	var txs []models.Transaction
	for rows.Next() {
		var t models.Transaction
		var dateStr string
		if err := rows.Scan(&t.ID, &dateStr, &t.Description, &t.Amount,
			&t.Category, &t.Account, &t.Source); err != nil {
			return nil, err
		}
		t.Date, _ = time.Parse("2006-01-02", dateStr)
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

// JSONSummary encodes a summary as pretty JSON.
func JSONSummary(sum *models.Summary) (string, error) {
	b, err := json.MarshalIndent(sum, "", "  ")
	return string(b), err
}
