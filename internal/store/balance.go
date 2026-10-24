package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// An anchor is the balance of one account at the END of a given day, typed in from a bank statement.
// Imported exports start mid-history, so the sum of transactions alone is not a balance; budgetctl
// only reports a balance for accounts that have an anchor and never guesses one.

func (s *Store) migrateAnchors() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS account_anchors (
		account TEXT PRIMARY KEY,
		as_of   TEXT NOT NULL,
		amount  REAL NOT NULL
	)`)
	return err
}

// AccountBalance is one account's anchored balance (Anchored=false: no anchor set, Balance unknown).
type AccountBalance struct {
	Account  string  `json:"account"`
	Anchored bool    `json:"anchored"`
	AsOf     string  `json:"as_of,omitempty"` // YYYY-MM-DD of the anchor
	Anchor   float64 `json:"anchor,omitempty"`
	Balance  float64 `json:"balance"` // anchor + bookings dated after AsOf
	Since    int     `json:"bookings_since"`
}

// SetAnchor records that account held amount at the end of asOf.
func (s *Store) SetAnchor(ctx context.Context, account string, asOf time.Time, amount float64) error {
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("account is required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO account_anchors (account, as_of, amount) VALUES (?,?,?)
		 ON CONFLICT(account) DO UPDATE SET as_of=excluded.as_of, amount=excluded.amount`,
		account, asOf.UTC().Format("2006-01-02"), amount)
	return err
}

// ClearAnchor removes an account's anchor; it reports whether there was one.
func (s *Store) ClearAnchor(ctx context.Context, account string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM account_anchors WHERE account=?`, account)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Balances lists every known account (from transactions or anchors), sorted by name.
func (s *Store) Balances(ctx context.Context) ([]AccountBalance, error) {
	accts, err := s.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	anchors := map[string]AccountBalance{}
	rows, err := s.db.QueryContext(ctx, `SELECT account, as_of, amount FROM account_anchors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b AccountBalance
		if err := rows.Scan(&b.Account, &b.AsOf, &b.Anchor); err != nil {
			return nil, err
		}
		b.Anchored = true
		anchors[b.Account] = b
		if !contains(accts, b.Account) {
			accts = append(accts, b.Account)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(accts)
	out := make([]AccountBalance, 0, len(accts))
	for _, a := range accts {
		b, ok := anchors[a]
		if !ok {
			out = append(out, AccountBalance{Account: a})
			continue
		}
		var sum float64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(amount),0), COUNT(*) FROM transactions WHERE account=? AND date > ?`, a, b.AsOf).Scan(&sum, &b.Since); err != nil {
			return nil, err
		}
		b.Balance = round2(b.Anchor + sum)
		out = append(out, b)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
