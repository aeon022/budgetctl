package tui

import (
	"fmt"
	"math"
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
	// label column: as wide as the longest category needs (so names aren't cut
	// at 12 cells), but never more than half the panel; the bar takes the rest.
	nameW := 8
	for _, c := range cats {
		nameW = max(nameW, lipgloss.Width(c.name))
	}
	nameW = min(nameW, max(inner/2, 8))
	barW := max(inner-nameW-6, 4)
	var b strings.Builder
	b.WriteString(styleMuted.Render("Top spending") + "\n")
	for _, c := range cats {
		ratio, withGoal := c.spent/total, false
		if g, ok := goal[c.name]; ok && g > 0 {
			ratio, withGoal = c.spent/g, true
		}
		b.WriteString(padRunes(ui.MidEllipsis(c.name, nameW), nameW) + " " + ui.Bar(barW, ratio, withGoal) + fmt.Sprintf(" %3.0f%%", ratio*100) + "\n")
	}
	if len(m.trend) > 1 {
		pts := m.trend
		if len(pts) > 6 {
			pts = pts[len(pts)-6:]
		}
		rows, maxV := columnChart(pts, inner, 4)
		b.WriteString("\n" + styleMuted.Render(fmt.Sprintf("Spending, last %d months · max %.0f€", len(pts), maxV)) + "\n")
		for i, r := range rows {
			if i == len(rows)-1 {
				b.WriteString(styleMuted.Render(r) + "\n")
			} else {
				b.WriteString(r + "\n")
			}
		}
	}
	if withGoals := len(goal); withGoals > 0 {
		b.WriteString("\n" + styleMuted.Render("Bars with a goal fill toward 100%"))
	}
	return b.String()
}

// monthLabel is only used by tests/snapshots for readability.
func monthLabel(t time.Time) string { return t.Format("2006-01") }

// columnChart draws monthly spending as height rows of columns (1/8-cell
// resolution per row) with a label row of month names underneath, within
// width cells. The last column (the newest month) is accented, the others
// muted. It returns the rows (height chart rows + 1 label row) and the largest
// monthly spending, so the caller can label the scale.
func columnChart(pts []models.MonthlyPoint, width, height int) ([]string, float64) {
	n := len(pts)
	if n == 0 || height < 1 {
		return nil, 0
	}
	vals := make([]float64, n)
	maxV := 0.0
	for i, p := range pts {
		vals[i] = -p.Expenses
		if vals[i] < 0 {
			vals[i] = 0
		}
		maxV = math.Max(maxV, vals[i])
	}
	cw := min(max((width+1)/n-1, 1), 3) // column width: 3 when it fits, down to 1
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	accent := lipgloss.NewStyle().Foreground(colorBlue)
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	rows := make([]string, 0, height+1)
	for r := 0; r < height; r++ {
		var line strings.Builder
		for i, v := range vals {
			level := 0
			if maxV > 0 {
				level = int(math.Round(v / maxV * float64(height*8)))
			}
			cell := min(max(level-(height-1-r)*8, 0), 8)
			st := dim
			if i == n-1 {
				st = accent
			}
			if i > 0 {
				line.WriteString(" ")
			}
			line.WriteString(st.Render(strings.Repeat(string(blocks[cell]), cw)))
		}
		rows = append(rows, line.String())
	}
	var lab strings.Builder
	for i, p := range pts {
		if i > 0 {
			lab.WriteString(" ")
		}
		name := p.Month
		if t, err := time.Parse("2006-01", p.Month); err == nil {
			name = t.Format("Jan")
		}
		lab.WriteString(padRunes(truncRunes(name, cw), cw))
	}
	return append(rows, lab.String()), maxV
}
