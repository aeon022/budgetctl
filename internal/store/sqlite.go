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

func (s *Store) Summary(ctx context.Context, month string) (*models.Summary, error) {
	q := `SELECT category, SUM(amount) FROM transactions WHERE 1=1`
	var args []any
	if month != "" {
		q += ` AND date LIKE ?`
		args = append(args, month+"%")
	}
	q += ` GROUP BY category`

	rows, err := s.db.QueryContext(ctx, q, args...)
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
		if total > 0 {
			sum.Income += total
		} else {
			sum.Expenses += total
		}
	}
	sum.Net = sum.Income + sum.Expenses
	return sum, rows.Err()
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
