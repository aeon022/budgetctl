package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/ui"
	"github.com/charmbracelet/x/ansi"
)

// wideMinWidth is where the list gets its framed two-panel layout
// (Transactions | Insights); below it the list is unframed.
const (
	wideMinWidth  = 120
	insightsWidth = 40
)

func (m Model) wide() bool { return m.width >= wideMinWidth }

// balance is the visible month's income/expenses/net, summed per transaction
// sign exactly like Store.Summary (expenses stay negative).
type balance struct {
	income, expenses, net float64
	rate                  float64 // net / income * 100
	hasRate               bool    // false when there is no income (no divide by zero)
}

func monthBalance(txs []models.Transaction) balance {
	var b balance
	for _, t := range txs {
		if t.Amount > 0 {
			b.income += t.Amount
		} else {
			b.expenses += t.Amount
		}
	}
	b.net = b.income + b.expenses
	if b.income > 0 {
		b.rate, b.hasRate = b.net/b.income*100, true
	}
	return b
}

func signedStyle(v float64) lipgloss.Style {
	switch {
	case v < 0:
		return styleExpense
	case v > 0:
		return styleIncome
	}
	return styleMuted
}

// balanceLine: "Income +3,200.00  Expenses -1,245.30  Balance +1,954.70  Saved 61%",
// shortened (savings rate first, then labels) to fit width.
func (m Model) balanceLine(width int) string {
	b := monthBalance(m.allTxs)
	num := func(v float64) string { return signedStyle(v).Render(strings.TrimSpace(plain(ui.Money(v, 0)))) }
	lbl := func(s string) string { return styleMuted.Render(s) }
	rate := ""
	if b.hasRate {
		rate = "  " + lbl("Saved ") + signedStyle(b.rate).Render(fmt.Sprintf("%.0f%%", b.rate))
	}
	full := "  " + lbl("Income ") + num(b.income) + "  " + lbl("Expenses ") + num(b.expenses) + "  " + lbl("Balance ") + num(b.net) + rate
	short := "  " + num(b.income) + "  " + num(b.expenses) + "  " + lbl("= ") + num(b.net) + rate
	switch {
	case lipgloss.Width(full) <= width:
		return full
	case lipgloss.Width(short) <= width:
		return short
	}
	return ansi.Truncate(short, width, "…")
}

func plain(s string) string { return ansi.Strip(s) }

// headerContext is the middle of the title bar: profile, account scope and
// the active category filter.
func (m Model) headerContext() string {
	var parts []string
	if p := config.ActiveProfile(); p != "" {
		parts = append(parts, "profile: "+p)
	}
	switch {
	case m.activeAccount >= 0 && m.activeAccount < len(m.accounts):
		parts = append(parts, m.accounts[m.activeAccount])
	case len(m.accounts) > 1:
		parts = append(parts, "all accounts")
	}
	if m.categoryFilter != "" {
		parts = append(parts, "category: "+m.categoryFilter)
	}
	return styleMuted.Render(strings.Join(parts, " · "))
}

// showAcctCol: several accounts are mixed in one list.
func (m Model) showAcctCol() bool {
	return len(m.accounts) > 1 && m.activeAccount < 0 && m.width >= 100 // too wide a column for small terminals
}

// columnHeader is the dimmed header row above the transactions, aligned with
// formatTxRowCols.
func (m Model) columnHeader() string {
	pad := func(s string, w int) string { return padRunes(s, w) }
	h := pad("Date", 10) + "  " + strings.Repeat(" ", amtColW-len("Amount")) + "Amount" + "  " + pad("Category", 16) + "  "
	if m.showAcctCol() {
		h += pad("Account", acctColW) + "  "
	}
	h += pad("Payee", payeeColW) + "  Description"
	return styleMuted.Render(h)
}

// listRows is how many transaction rows fit: everything below the header
// block minus the footer (divider + hints), and in the wide layout also the
// panel's bottom border. Shared by renderList and rowHitTest.
func (m Model) listRows() int {
	n := m.height - m.listStartRow() - 2
	if m.wide() {
		n--
	}
	return max(n, 1)
}

// netBadge / selectedSum for the footer's right side.
func (m Model) footerRight() string {
	var parts []string
	if m.selecting && len(m.selected) > 0 {
		sum := 0.0
		for _, t := range m.txs {
			if m.selected[t.ID] {
				sum += t.Amount
			}
		}
		parts = append(parts, styleMuted.Render("Σ ")+signedStyle(sum).Render(fmt.Sprintf("%+.2f€", sum)))
	}
	if len(m.txs) > 0 {
		parts = append(parts, styleMuted.Render(fmt.Sprintf("%d/%d", m.cursor+1, len(m.txs))))
	}
	return strings.Join(parts, "  ")
}

// insightsContent fills the wide layout's right panel: top expense categories
// as bars (against the budget goal when one exists, else as a share of all
// spending) and a 6-month spending sparkline.
func (m Model) insightsContent(inner int) string {
	byCat := map[string]float64{}
	total := 0.0
	for _, t := range m.allTxs {
		if t.Amount < 0 {
			c := t.Category
			if c == "" {
				c = "Uncategorized"
			}
			byCat[c] += -t.Amount
			total += -t.Amount
		}
	}
	if total == 0 {
		return styleMuted.Render("No spending this month.")
	}
	goal := map[string]float64{}
	for _, g := range m.goals {
		goal[g.Category] = g.Monthly
	}
	type cat struct {
		name  string
		spent float64
	}
	var cats []cat
	for n, v := range byCat {
		cats = append(cats, cat{n, v})
	}
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].spent != cats[j].spent {
			return cats[i].spent > cats[j].spent
		}
		return cats[i].name < cats[j].name
	})
	if len(cats) > 5 {
		cats = cats[:5]
	}
	nameW := 12
	barW := max(inner-nameW-6, 4)
	var b strings.Builder
	b.WriteString(styleMuted.Render("Top spending") + "\n")
	for _, c := range cats {
		ratio, withGoal := c.spent/total, false
		if g, ok := goal[c.name]; ok && g > 0 {
			ratio, withGoal = c.spent/g, true
		}
		b.WriteString(padRunes(truncRunes(c.name, nameW), nameW) + " " + ui.Bar(barW, ratio, withGoal) + fmt.Sprintf(" %3.0f%%", ratio*100) + "\n")
	}
	if len(m.trend) > 1 {
		pts := m.trend
		if len(pts) > 6 {
			pts = pts[len(pts)-6:]
		}
		vals := make([]float64, len(pts))
		for i, p := range pts {
			vals[i] = -p.Expenses
		}
		b.WriteString("\n" + styleMuted.Render(fmt.Sprintf("Spending, last %d months", len(pts))) + "\n" + ui.Spark(vals) + "\n")
	}
	if withGoals := len(goal); withGoals > 0 {
		b.WriteString("\n" + styleMuted.Render("Bars with a goal fill toward 100%"))
	}
	return b.String()
}

// monthLabel is only used by tests/snapshots for readability.
func monthLabel(t time.Time) string { return t.Format("2006-01") }
