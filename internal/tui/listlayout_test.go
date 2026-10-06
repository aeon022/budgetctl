package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/tuitest"
)

func layoutModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := smokeModel(t, false)
	for i, r := range []struct {
		d, p, ac string
		a        float64
		c        string
	}{
		{"Gehalt Oktober", "Employer GmbH", "N26", 3200, "Income"}, {"Miete", "Hausverwaltung", "ING", -980, "Housing"},
		{"REWE Markt", "REWE", "N26", -64.3, "Groceries"}, {"Spotify", "Spotify AB", "N26", -10.99, "Subscriptions"},
		{"Amazon", "Amazon", "ING", -39.9, ""},
	} {
		m.txs = append(m.txs, models.Transaction{ID: fmt.Sprintf("id%d", i), Description: r.d, Payee: r.p, Account: r.ac, Amount: r.a,
			Category: r.c, Date: time.Date(2026, 10, i+1, 0, 0, 0, 0, time.Local)})
	}
	m.allTxs = m.txs
	m.accounts = []string{"N26", "ING"}
	m.activeAccount = -1
	mm, _ := tuitest.Send(m, tuitest.Resize(w, h))
	return mm.(Model)
}

func TestMonthBalanceNumbers(t *testing.T) {
	b := monthBalance([]models.Transaction{{Amount: 3200}, {Amount: -1000}, {Amount: -252.38}})
	if b.income != 3200 || math.Abs(b.expenses+1252.38) > 1e-9 || math.Abs(b.net-1947.62) > 1e-9 {
		t.Fatalf("balance = %+v", b)
	}
	if !b.hasRate || math.Abs(b.rate-60.86) > 0.01 {
		t.Errorf("savings rate = %v (has %v), want ≈60.86", b.rate, b.hasRate)
	}
	// zero income: no divide by zero, no rate shown
	z := monthBalance([]models.Transaction{{Amount: -50}})
	if z.hasRate || math.IsNaN(z.rate) || math.IsInf(z.rate, 0) || z.net != -50 {
		t.Errorf("zero income = %+v", z)
	}
	if e := monthBalance(nil); e.net != 0 || e.hasRate {
		t.Errorf("empty = %+v", e)
	}
	// spending more than earned → negative savings rate
	if o := monthBalance([]models.Transaction{{Amount: 100}, {Amount: -150}}); o.rate != -50 {
		t.Errorf("overspend rate = %v, want -50", o.rate)
	}
}

func TestBalanceLineTextAndFit(t *testing.T) {
	m := layoutModel(t, 100, 30)
	line := tuitest.Text(m)
	for _, want := range []string{"Income +3,200.00", "Expenses -1,095.19", "Balance +2,104.81", "Saved 66%"} {
		if !strings.Contains(line, want) {
			t.Errorf("balance line missing %q:\n%s", want, line)
		}
	}
	for _, w := range []int{20, 40, 60, 80} {
		if got := lipgloss.Width(m.balanceLine(w)); got > w {
			t.Errorf("balanceLine(%d) is %d wide", w, got)
		}
	}
	if full := plain(m.balanceLine(100)); !strings.Contains(full, "Income") {
		t.Errorf("wide enough → labels: %q", full)
	}
	if short := plain(m.balanceLine(50)); strings.Contains(short, "Income") {
		t.Errorf("narrow → numbers only: %q", short)
	}
}

func TestListLinesNeverExceedWidthAndFillHeight(t *testing.T) {
	for _, w := range []int{60, 80, 100, 140} {
		m := layoutModel(t, w, 28)
		out := m.renderList()
		lines := strings.Split(out, "\n")
		if len(lines) != m.height { // the model reserves one row of the window itself
			t.Errorf("w=%d: %d lines, want the model height %d", w, len(lines), m.height)
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got > w {
				t.Errorf("w=%d line %d is %d wide: %q", w, i, got, plain(l))
			}
		}
		// header block: title bar, balance, tabs
		if !strings.Contains(plain(lines[0]), "budgetctl") || !strings.Contains(plain(lines[1]), "Balance") && !strings.Contains(plain(lines[1]), "=") {
			t.Errorf("w=%d header/balance lines: %q / %q", w, plain(lines[0]), plain(lines[1]))
		}
	}
}

func TestColumnHeadersAndAccountColumn(t *testing.T) {
	for _, w := range []int{100, 140} {
		text := tuitest.Text(layoutModel(t, w, 28))
		for _, col := range []string{"Date", "Amount", "Category", "Account", "Payee", "Description"} {
			if !strings.Contains(text, col) {
				t.Errorf("w=%d missing column header %q", w, col)
			}
		}
		if !strings.Contains(text, "N26") || !strings.Contains(text, "ING") {
			t.Errorf("w=%d: account names must be shown per row when accounts are mixed", w)
		}
	}
	// a single account scope: the Account column is redundant → not shown
	m := layoutModel(t, 100, 28)
	m.activeAccount = 0
	text := tuitest.Text(m)
	if strings.Contains(text, "Account  ") {
		t.Errorf("single-account scope should hide the Account column:\n%s", text)
	}
	if !strings.Contains(plain(m.headerContext()), "N26") {
		t.Errorf("header context names the account: %q", plain(m.headerContext()))
	}
}

func TestPanelsOnlyFrom120Columns(t *testing.T) {
	for _, tc := range []struct {
		w    int
		want bool
	}{{100, false}, {119, false}, {120, true}, {160, true}} {
		text := tuitest.Text(layoutModel(t, tc.w, 28))
		has := strings.Contains(text, "╭─ Transactions") && strings.Contains(text, "Insights")
		if has != tc.want {
			t.Errorf("w=%d: panels present=%v, want %v", tc.w, has, tc.want)
		}
	}
	if !strings.Contains(tuitest.Text(layoutModel(t, 140, 28)), "Housing") {
		t.Error("insights should list the top spending category")
	}
}

func TestInsightsGoalBarsAndEmpty(t *testing.T) {
	m := layoutModel(t, 140, 28)
	m.goals = []models.GoalStatus{{BudgetGoal: models.BudgetGoal{Category: "Housing", Monthly: 1000}}}
	got := plain(m.insightsContent(37))
	if !strings.Contains(got, "Housing") || !strings.Contains(got, " 98%") { // 980 / 1000 goal, not the share of spend
		t.Errorf("goal bar should show spent/goal:\n%s", got)
	}
	m.allTxs = []models.Transaction{{Amount: 100}}
	if got := plain(m.insightsContent(37)); !strings.Contains(got, "No spending") {
		t.Errorf("no spending → message: %q", got)
	}
}

func TestFooterIsOneLineWithHintsAndPosition(t *testing.T) {
	for _, w := range []int{60, 80, 100} {
		lines := strings.Split(layoutModel(t, w, 28).renderList(), "\n")
		foot := plain(lines[len(lines)-1])
		if !strings.Contains(foot, "? help") || !strings.Contains(foot, "q quit") || !strings.Contains(foot, "1/5") {
			t.Errorf("w=%d footer = %q", w, foot)
		}
		if !strings.HasPrefix(plain(lines[len(lines)-2]), "──") {
			t.Errorf("w=%d the line above the footer is the divider, got %q", w, plain(lines[len(lines)-2]))
		}
	}
	m := layoutModel(t, 100, 28)
	m.selecting = true
	m.selected = map[string]bool{"id1": true, "id2": true}
	if foot := plain(m.listFooter(100)); !strings.Contains(foot, "Σ -1044.30€") { // -980 + -64.30
		t.Errorf("selection sum missing: %q", foot)
	}
}

func TestMouseClickHitsRowsInBothLayouts(t *testing.T) {
	for _, w := range []int{100, 140} {
		m := layoutModel(t, w, 28)
		for want := 0; want < 3; want++ {
			y := m.listStartRow() + want
			mm, _ := tuitest.Send(m, tuitest.Click(10, y))
			if got := mm.(Model).cursor; got != want {
				t.Errorf("w=%d click on screen row %d → cursor %d, want %d", w, y, got, want)
			}
		}
		// the click must land on the row that is actually drawn there
		lines := strings.Split(plain(m.renderList()), "\n")
		if !strings.Contains(lines[m.listStartRow()], "2026-10-01") {
			t.Errorf("w=%d first data row is not on listStartRow=%d: %q", w, m.listStartRow(), lines[m.listStartRow()])
		}
	}
}
