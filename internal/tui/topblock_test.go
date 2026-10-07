package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/charmbracelet/x/ansi"
)

// topModel is layoutModel with distinctive account names (so the account row
// can be located in the render) and a configurable number of months/accounts.
func topModel(t *testing.T, w, h, accounts, months int) Model {
	t.Helper()
	m := layoutModel(t, w, h)
	m.accounts = []string{"AcctOne", "AcctTwo"}[:accounts]
	for i := range m.txs {
		if accounts > 0 {
			m.txs[i].Account = m.accounts[i%accounts]
		}
	}
	m.allTxs = m.txs
	m.months = nil
	for i := 0; i < months; i++ {
		m.months = append(m.months, time.Date(2026, time.Month(10-i%12), 1, 0, 0, 0, 0, time.Local).AddDate(0, -(i/12)*12, 0).Format("2006-01"))
	}
	mm, _ := tuitest.Send(m, tuitest.Resize(w, h))
	return mm.(Model)
}

// cellIndex is the display column where sub starts in s (-1 if absent) — byte
// offsets would drift after the 3-byte "€".
func cellIndex(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(s[:i])
}

func lines(m Model) []string { return strings.Split(tuitest.Text(m), "\n") }

func TestHeadRowCountMatchesWhatIsRendered(t *testing.T) {
	for _, w := range []int{40, 59, 60, 100, 149, 150, 170} {
		for _, h := range []int{20, 31, 32, 40} {
			for _, acc := range []int{0, 1, 2} {
				for _, months := range []int{1, 4, 30} {
					m := topModel(t, w, h, acc, months)
					if got, want := m.headRowCount(), len(m.headBase()); got != want {
						t.Errorf("%dx%d accounts=%d months=%d: headRowCount=%d but headBase has %d rows", w, h, acc, months, got, want)
					}
				}
			}
		}
	}
}

func TestRowPositionsMatchRendering(t *testing.T) {
	for _, w := range []int{80, 110, 150} {
		for _, h := range []int{28, 40} {
			for _, acc := range []int{0, 2} {
				m := topModel(t, w, h, acc, 4)
				ls := lines(m)
				if y := m.tabRowY(); !strings.Contains(ls[y], "2026-09") {
					t.Errorf("%dx%d acc=%d: tabRowY=%d but that line is %q", w, h, acc, y, ls[y])
				}
				if acc > 0 {
					if y := m.acctRowY(); !strings.Contains(ls[y], "AcctOne") {
						t.Errorf("%dx%d acc=%d: acctRowY=%d but that line is %q", w, h, acc, y, ls[y])
					}
				}
				if y := m.listStartRow(); !strings.Contains(ls[y], "2026-10-01") {
					t.Errorf("%dx%d acc=%d: listStartRow=%d but that line is %q", w, h, acc, y, ls[y])
				}
			}
		}
	}
}

func TestClicksLandOnTabsAccountsAndRowsInBothTiers(t *testing.T) {
	for _, w := range []int{80, 110, 150} {
		for _, h := range []int{28, 40} {
			for _, acc := range []int{0, 2} {
				name := fmt.Sprintf("%dx%d acc=%d", w, h, acc)
				m := topModel(t, w, h, acc, 4)
				ls := lines(m)

				// every visible month tab: click its text
				for i, mo := range m.months {
					x := strings.Index(ls[m.tabRowY()], mo) + 2
					if got := m.tabHitTest(x, m.tabRowY()); got != i {
						t.Errorf("%s: click month %s (x=%d) -> %d, want %d", name, mo, x, got, i)
					}
				}
				// the account chips (All = -1, then 0, 1)
				if acc > 0 {
					row := ls[m.acctRowY()]
					for idx, label := range []string{"All", "AcctOne", "AcctTwo"} {
						x := strings.Index(row, " "+label+" ") + 2
						if got := m.accountTabHitTest(x, m.acctRowY()); got != idx-1 {
							t.Errorf("%s: click account %q (x=%d) -> %d, want %d; row %q", name, label, x, got, idx-1, row)
						}
					}
				} else if got := m.accountTabHitTest(2, m.acctRowY()); got != -2 {
					t.Errorf("%s: no accounts, but a click was treated as an account tab (%d)", name, got)
				}
				// transaction rows: click each visible row and land on it
				for want := 0; want < min(len(m.txs), m.listRows()); want++ {
					if got := m.rowHitTest(m.listStartRow() + want); got != want {
						t.Errorf("%s: click row %d -> %d", name, want, got)
					}
				}
				// clicks above the list never select a row
				if got := m.rowHitTest(m.listStartRow() - 1); got != -1 {
					t.Errorf("%s: header click selected row %d", name, got)
				}
			}
		}
	}
}

func TestStatStripFitsAndLabelsSitOverValues(t *testing.T) {
	for _, w := range []int{60, 80, 100, 140, 170} {
		m := topModel(t, w, 40, 2, 4)
		strip := m.statStrip(w)
		if len(strip) != 2 {
			t.Fatalf("w=%d: want 2 rows, got %d", w, len(strip))
		}
		for _, l := range strip {
			if lipgloss.Width(l) > w {
				t.Errorf("w=%d: stat line is %d cells wide: %q", w, lipgloss.Width(l), ansi.Strip(l))
			}
		}
		labels, values := ansi.Strip(strip[0]), ansi.Strip(strip[1])
		for col, pair := range [][2]string{{"INCOME", "+3,200.00€"}, {"EXPENSES", "-1,095.19€"}, {"BALANCE", "+2,104.81€"}, {"SAVED", "66%"}} {
			li, vi := cellIndex(labels, pair[0]), cellIndex(values, pair[1])
			if li < 0 || vi < 0 || li != vi {
				t.Errorf("w=%d col %d: label %q at %d but value %q at %d\n%s\n%s", w, col, pair[0], li, pair[1], vi, labels, values)
			}
		}
	}
}

func TestStatStripWithoutIncomeShowsDashNotARate(t *testing.T) {
	m := topModel(t, 100, 40, 0, 1)
	m.allTxs = []models.Transaction{{Amount: -50}, {Amount: -25.5}}
	vals := ansi.Strip(m.statStrip(100)[1])
	if !strings.Contains(vals, "—") || strings.Contains(vals, "%") || !strings.Contains(vals, "-75.50€") {
		t.Errorf("no income: want a dash for SAVED, no percentage, balance -75.50€: %q", vals)
	}
	m.allTxs = nil // empty month must not panic either
	if got := ansi.Strip(m.statStrip(100)[1]); !strings.Contains(got, "0.00€") || !strings.Contains(got, "—") {
		t.Errorf("empty month: %q", got)
	}
	// overspending: negative rate renders, bar stays empty and in range
	m.allTxs = []models.Transaction{{Amount: 100}, {Amount: -300}}
	if got := ansi.Strip(m.statStrip(100)[1]); !strings.Contains(got, "-200%") {
		t.Errorf("overspend: %q", got)
	}
}

func TestTierSwitchesAtTheHeightThreshold(t *testing.T) {
	// terminal rows: the model keeps one row in reserve (height = rows-1)
	compact, roomy := topModel(t, 110, spaciousMinHeight-1, 2, 4), topModel(t, 110, spaciousMinHeight, 2, 4)
	if compact.spacious() || !roomy.spacious() {
		t.Fatalf("spacious() at %d and %d rows", spaciousMinHeight-1, spaciousMinHeight)
	}
	if got := strings.Join(lines(compact), "\n"); strings.Contains(got, "INCOME") || strings.Contains(got, "Accounts") {
		t.Errorf("compact tier must keep the one-line balance and the plain account row:\n%s", got)
	}
	if got := strings.Join(lines(roomy), "\n"); !strings.Contains(got, "INCOME") || !strings.Contains(got, "Accounts") {
		t.Errorf("spacious tier shows the stat strip and the labeled account row:\n%s", got)
	}
	if compact.headRowCount() != 4 || roomy.headRowCount() != 9 {
		t.Errorf("head rows: compact %d (want 4), spacious %d (want 9)", compact.headRowCount(), roomy.headRowCount())
	}
	// the screen is always exactly as tall as the terminal
	for _, m := range []Model{compact, roomy} {
		if n := len(lines(m)); n != m.height {
			t.Errorf("height %d renders %d lines", m.height, n)
		}
	}
}

func TestAccountRowPlacementRule(t *testing.T) {
	if m := topModel(t, acctSameLineWidth-1, 40, 2, 4); m.acctOnTabLine() || m.acctRowY() != m.tabRowY()+1 {
		t.Errorf("below %d columns the accounts get their own row", acctSameLineWidth)
	}
	m := topModel(t, acctSameLineWidth, 40, 2, 4)
	if !m.acctOnTabLine() || m.acctRowY() != m.tabRowY() {
		t.Fatalf("at %d columns, with room, the accounts share the tab row", acctSameLineWidth)
	}
	row := lines(m)[m.tabRowY()]
	if lipgloss.Width(row) != acctSameLineWidth || !strings.HasSuffix(strings.TrimRight(row, " "), "AcctTwo") {
		t.Errorf("accounts must be right-aligned on the tab row: %q", row)
	}
	// 30 months overflow: the tabs would have to be windowed, so the accounts keep their own row
	if m := topModel(t, acctSameLineWidth, 40, 2, 30); m.acctOnTabLine() {
		t.Error("months that don't fit unwindowed must not share their row")
	}
	// compact tier never shares
	if m := topModel(t, 170, 28, 2, 4); m.acctOnTabLine() {
		t.Error("compact tier keeps the plain layout")
	}
}

func TestSelectedRowKeepsDateAndCentsReadable(t *testing.T) {
	m := topModel(t, 110, 40, 0, 1)
	m.txs = []models.Transaction{
		{ID: "a", Description: "Old A", Payee: "P", Amount: -12.34, Category: "X", Date: time.Date(2020, 1, 2, 0, 0, 0, 0, time.Local)},
		{ID: "b", Description: "Old B", Payee: "P", Amount: -56.78, Category: "X", Date: time.Date(2020, 1, 3, 0, 0, 0, 0, time.Local)},
	}
	m.allTxs, m.cursor = m.txs, 0
	mm, _ := tuitest.Send(m, tuitest.Resize(110, 40))
	m = mm.(Model)

	probe := lipgloss.NewStyle().Foreground(colorSubtle).Render("\x00")
	subtle := probe[:strings.Index(probe, "\x00")]
	if subtle == "" {
		t.Skip("subtle has no foreground sequence")
	}
	var sel, unsel string
	for _, l := range strings.Split(m.renderList(), "\n") {
		switch {
		case strings.Contains(ansi.Strip(l), "2020-01-02"):
			sel = l
		case strings.Contains(ansi.Strip(l), "2020-01-03"):
			unsel = l
		}
	}
	if !strings.Contains(unsel, subtle) {
		t.Fatal("test setup: an old date on an unselected row is drawn in the Subtle foreground")
	}
	if strings.Contains(sel, subtle) {
		t.Errorf("the selected row still paints text in the Subtle foreground, which can equal the selection background (date/cents vanished):\n%q", sel)
	}
	if text := ansi.Strip(sel); !strings.Contains(text, "2020-01-02") || !strings.Contains(text, "-12.34€") {
		t.Errorf("selected row lost its date or cents: %q", text)
	}
}

func TestDigitJumpUsesTheSameWindowAsTheWidePanel(t *testing.T) {
	// the "1"-"9" jump used height-start-2 while the renderer subtracts one more
	// row for the wide panel's bottom border: with a scrolled cursor the digit
	// jumped one row too far
	m := topModel(t, 140, 40, 0, 1)
	m.txs = nil
	for i := 0; i < 60; i++ {
		m.txs = append(m.txs, models.Transaction{ID: fmt.Sprint(i), Description: fmt.Sprint("tx", i), Amount: -1, Date: time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)})
	}
	m.allTxs, m.cursor = m.txs, 50
	mm, _ := tuitest.Send(m, tuitest.Resize(140, 40))
	m = mm.(Model)
	listH := m.listRows()
	winStart := m.cursor - listH + 1
	mm, _ = tuitest.Keys(m, "3")
	if got, want := mm.(Model).cursor, winStart+2; got != want {
		t.Errorf("digit 3 -> cursor %d, want %d (3rd visible row)", got, want)
	}
}
