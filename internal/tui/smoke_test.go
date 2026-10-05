package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/tuitest"
)

func smokeModel(t *testing.T, withData bool) Model {
	t.Helper()
	isolate(t)
	m := New()
	m.view = viewList
	m.months = []string{"2026-10", "2026-09"}
	if withData {
		for _, d := range []struct {
			desc string
			amt  float64
			cat  string
		}{{"REWE Markt", -42.5, "Groceries"}, {"Gehalt", 3000, "Income"}, {"Miete", -900, ""}} {
			m.txs = append(m.txs, models.Transaction{Description: d.desc, Amount: d.amt, Category: d.cat})
		}
		m.allTxs = m.txs
	}
	return m
}

// every view/mode reachable from the list, each left via esc
var smokeFlows = [][]string{
	{"?", "esc"},
	{"s", "j", "k", "tab", "esc"},
	{"/", "r", "e", "esc"},
	{":", "esc"},
	{"c", "esc"},
	{"f", "esc"},
	{"p", "esc"},
	{"o", "esc"},
	{"i", "esc"},
	{"n", "esc"},
	{"enter", "esc"},
	{"v", "space", "esc"},
	{"j", "k", "g", "G", "tab", "shift+tab", "y", "Y", "[", "]"},
}

func TestSmokeAllViews(t *testing.T) {
	for _, withData := range []bool{true, false} {
		for _, f := range smokeFlows {
			name := f[0]
			t.Run(name, func(t *testing.T) { tuitest.Smoke(t, smokeModel(t, withData), f...) })
		}
	}
}

func TestSmokeSmallTerminal(t *testing.T) {
	for _, f := range smokeFlows {
		mm, _ := tuitest.Send(smokeModel(t, true), tuitest.Resize(60, 15))
		mm, _ = tuitest.Keys(mm, f...)
		if strings.TrimSpace(tuitest.Text(mm)) == "" {
			t.Errorf("empty view at 60x15 after %v", f)
		}
	}
}

// the key bar (last two lines) must never be wider than the terminal; the
// transaction rows above it have their own fixed columns and aren't covered.
func TestListFooterFitsWidth(t *testing.T) {
	for _, w := range []int{60, 80, 100} {
		m := smokeModel(t, true)
		m.width, m.height = w, 30
		lines := strings.Split(m.renderList(), "\n")
		for _, l := range lines[len(lines)-2:] {
			if lw := lipgloss.Width(l); lw > w {
				t.Errorf("width %d: key bar line is %d wide: %q", w, lw, l)
			}
		}
	}
}
