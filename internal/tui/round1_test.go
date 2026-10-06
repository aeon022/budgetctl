package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/tuitest"
	"github.com/charmbracelet/x/ansi"
)

func TestMonthAndAccountTabsShareOneLook(t *testing.T) {
	if styleTabActive.Render("x") != styleAcctTabActive.Render("x") || styleTabInact.Render("x") != styleAcctTabInact.Render("x") {
		t.Error("month and account tabs must use the same active/inactive style (months were blue, accounts green)")
	}
}

func TestTabHitTestStillMatchesRenderingAfterRestyle(t *testing.T) {
	m := layoutModel(t, 100, 30)
	m.months = []string{"2026-10", "2026-09", "2026-08"}
	bar := ansi.Strip(m.renderMonthTabBar(100))
	col := strings.Index(bar, "2026-09") + 2 // inside the second tab's text
	if got := m.tabHitTest(col, 2); got != 1 {
		t.Errorf("click at col %d of %q hit tab %d, want 1", col, strings.TrimSpace(bar), got)
	}
}

func TestRowColorsPayeePlainCategoryMutedUncategorizedAmber(t *testing.T) {
	d := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	cat := formatTxRowCols(&models.Transaction{Description: "d", Payee: "Hausverwaltung", Category: "Housing", Amount: -980, Date: d}, 120, "", false)
	unc := formatTxRowCols(&models.Transaction{Description: "d", Payee: "Amazon", Amount: -9, Date: d}, 120, "", false)
	if strings.Contains(cat, stylePayee.Render("")) && stylePayee.Render("x") != "x" {
		t.Error("payee must not carry a color")
	}
	if stylePayee.Render("Hausverwaltung") != "Hausverwaltung" {
		t.Errorf("payee style adds styling: %q", stylePayee.Render("Hausverwaltung"))
	}
	if !strings.Contains(cat, styleCategoryRow.Render("Housing         ")) {
		t.Errorf("a categorized row shows its category muted")
	}
	if !strings.Contains(unc, styleCategory.Render("—               ")) {
		t.Errorf("an uncategorized row shows an amber —")
	}
	if styleCategoryRow.Render("x") == styleCategory.Render("x") {
		t.Error("muted and amber must differ")
	}
}

func TestColumnChartScalingLabelsAndEdges(t *testing.T) {
	pts := []models.MonthlyPoint{
		{Month: "2026-05", Expenses: -100}, {Month: "2026-06", Expenses: -400}, {Month: "2026-07", Expenses: 0},
		{Month: "2026-08", Expenses: -200}, {Month: "2026-09", Expenses: -50}, {Month: "2026-10", Expenses: -800},
	}
	rows, maxV := columnChart(pts, 30, 4)
	if maxV != 800 || len(rows) != 5 {
		t.Fatalf("max=%v rows=%d, want 800 / 5 (4 chart rows + labels)", maxV, len(rows))
	}
	plainRows := make([]string, len(rows))
	for i, r := range rows {
		plainRows[i] = ansi.Strip(r)
		if lipgloss.Width(r) > 30 {
			t.Errorf("row %d is %d wide (max 30)", i, lipgloss.Width(r))
		}
	}
	// the largest month fills every chart row, the zero month none
	cols := strings.Fields(plainRows[0])
	if len(cols) != 1 || !strings.HasSuffix(strings.TrimRight(plainRows[0], " "), "███") {
		t.Errorf("only the 800€ month reaches the top row: %q", plainRows[0])
	}
	if strings.TrimSpace(string([]rune(plainRows[3])[8:11])) != "" { // 3rd column (July, 0) is blank even on the bottom row
		t.Errorf("a zero month draws nothing: %q", plainRows[3])
	}
	if lab := plainRows[4]; !strings.Contains(lab, "May") || !strings.Contains(lab, "Oct") {
		t.Errorf("month labels: %q", lab)
	}
	// 100 of 800 over 4 rows × 8 = 32 levels → 4 levels → the bottom row shows a ▄
	if !strings.Contains(string([]rune(plainRows[3])[:3]), "▄") {
		t.Errorf("1/8-cell resolution: %q", plainRows[3])
	}
}

func TestColumnChartDegenerateInputs(t *testing.T) {
	if rows, m := columnChart(nil, 20, 4); rows != nil || m != 0 {
		t.Error("no points → nothing")
	}
	rows, m := columnChart([]models.MonthlyPoint{{Month: "2026-10", Expenses: 0}}, 20, 3)
	if m != 0 || len(rows) != 4 || strings.TrimSpace(ansi.Strip(strings.Join(rows[:3], ""))) != "" {
		t.Errorf("all-zero data draws an empty chart without dividing by zero: %q", rows)
	}
	for _, w := range []int{4, 8, 17, 60} { // narrow panels shrink the columns instead of overflowing
		rows, _ := columnChart([]models.MonthlyPoint{{Month: "2026-08", Expenses: -1}, {Month: "2026-09", Expenses: -2}, {Month: "2026-10", Expenses: -3}}, w, 4)
		for _, r := range rows {
			if lipgloss.Width(r) > max(w, 5) { // 3 columns need at least 5 cells (1+1+1 plus 2 gaps)
				t.Errorf("width %d: row %d wide", w, lipgloss.Width(r))
			}
		}
	}
}

func TestInsightsLabelsKeepLongCategoryNames(t *testing.T) {
	m := layoutModel(t, 140, 32)
	m.allTxs = []models.Transaction{
		{Amount: -300, Category: "Auto Finanzierung"}, {Amount: -100, Category: "Versicherungen"}, {Amount: -50, Category: "Cloud-Dienste"},
	}
	m.trend = []models.MonthlyPoint{{Month: "2026-09", Expenses: -100}, {Month: "2026-10", Expenses: -450}}
	out := ansi.Strip(m.insightsContent(40))
	for _, want := range []string{"Auto Finanzierung", "Versicherungen", "Cloud-Dienste", "Sep", "Oct", "max 450€"} {
		if !strings.Contains(out, want) {
			t.Errorf("insights missing %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if lipgloss.Width(l) > 40 {
			t.Errorf("insights line %d wide: %q", lipgloss.Width(l), l)
		}
	}
}

func TestWideListStillFitsAfterRound1(t *testing.T) {
	m := layoutModel(t, 140, 32)
	text := tuitest.Text(m)
	for i, l := range strings.Split(text, "\n") {
		if lipgloss.Width(l) > 140 {
			t.Errorf("line %d is %d wide", i, lipgloss.Width(l))
		}
	}
}
