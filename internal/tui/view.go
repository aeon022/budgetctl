package tui

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/emptystate"
	"github.com/aeon022/missionctl-core/keymap"
	"github.com/aeon022/missionctl-core/overlay"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/missionctl-core/statusbar"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/ui"
	"github.com/charmbracelet/x/ansi"
)

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	// v1's WithAltScreen()/WithMouseAllMotion() are per-View fields in v2.
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.ReportFocus = true // FocusMsg → reload when the window regains focus
	return v
}

func (m Model) viewContent() string {
	switch m.view {
	case viewSummary:
		return m.renderSummaryView()
	case viewHelp:
		// "?" is only reachable from the main list, so the list is always
		// the correct background to keep visible behind the popup. No
		// enclosing border on the list view, so inset 0 is safe.
		return overlay.CenterDim(m.renderList(), m.renderHelpPopup(), m.width, m.height, 0)
	case viewForm:
		return m.renderForm()
	case viewImport:
		return overlay.Center(m.renderList(), m.renderImportPopup(), m.width, m.height, 0)
	case viewDetail:
		return overlay.Center(m.renderList(), m.renderDetailPopup(), m.width, m.height, 0)
	case viewCategoryPick:
		return overlay.Center(m.renderList(), m.renderCategoryPickPopup(), m.width, m.height, 0)
	case viewSettings:
		return overlay.Center(m.renderList(), m.renderSettingsPopup(), m.width, m.height, 0)
	case viewProfiles:
		return overlay.Center(m.renderList(), m.renderProfilesPopup(), m.width, m.height, 0)
	case viewCategoryTranslate:
		return overlay.Center(m.renderSummaryView(), m.renderCategoryTranslatePopup(), m.width, m.height, 0)
	default:
		return m.renderList()
	}
}

// renderDetailPopup shows the full, untruncated fields of the selected
// transaction — mainly the description, which formatTxRow truncates to fit
// the list's row width and real bank exports routinely run to hundreds of
// characters (Verwendungszweck/Zahlungsreferenz text).
func (m Model) renderDetailPopup() string {
	t := m.detailTx
	if t == nil {
		return ""
	}
	w := m.importPopupWidth()
	contentW := w - 6 // border(2) + padding(4), same budget as the import popup

	amtStyle := styleIncome
	if t.Amount < 0 {
		amtStyle = styleExpense
	}
	cat := t.Category
	if cat == "" {
		cat = "(uncategorized)"
	}
	acct := t.Account
	if acct == "" {
		acct = "(none)"
	}

	// Budget the description/raw fields off the ACTUAL terminal height so a
	// pathologically long field can't blow the popup past the screen the
	// way the unbudgeted file-picker wrap once did. Fixed chrome (title,
	// field rows, section headers, footer, border, padding) eats ~13 rows;
	// whatever's left is split into wrapped lines at contentW, converted
	// back to a character budget, and further split with Raw if it'll show.
	hasRaw := t.Raw != "" && t.Raw != t.Description
	fixedRows := 13
	if t.Source != "" {
		fixedRows++
	}
	if t.Payee != "" {
		fixedRows++
	}
	if hasRaw {
		fixedRows += 2 // "Raw:" header + its own blank separator line
	}
	availLines := m.height - fixedRows
	if availLines < 2 {
		availLines = 2
	}
	if hasRaw {
		availLines /= 2
	}
	maxLen := min(400, availLines*contentW)
	if maxLen < 80 {
		maxLen = 80
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render("Transaction") + "\n\n")
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Date:", t.Date.Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Amount:", amtStyle.Render(fmt.Sprintf("%+.2f €", t.Amount))))
	if t.Payee != "" {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", "Payee:", stylePayee.Render(t.Payee)))
	}
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Category:", styleCategory.Render(cat)))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Account:", acct))
	if t.Source != "" {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", "Source:", styleMuted.Render(t.Source)))
	}
	b.WriteString("\n  " + styleSummaryH.Render("Description:") + "\n")
	b.WriteString("  " + wrapCapped(t.Description, contentW, maxLen) + "\n")
	if hasRaw {
		b.WriteString("\n  " + styleSummaryH.Render("Raw:") + "\n")
		b.WriteString("  " + styleMuted.Render(wrapCapped(t.Raw, contentW, maxLen)) + "\n")
	}
	b.WriteString("\n" + styleMuted.Render("e: edit  ·  any other key: close"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

// wrapCapped truncates s to maxLen (with an ellipsis) before letting the
// caller's outer lipgloss Width() word-wrap it, so a single field can never
// produce an unbounded number of physical lines.
func wrapCapped(s string, width, maxLen int) string {
	if len([]rune(s)) > maxLen {
		s = ansi.Truncate(s, maxLen, "…")
	}
	return s
}

// importPopupWidth is the fixed outer width of the import assistant's
// bordered popup. Shared by renderImportPopup (which applies it) and
// renderImportPickFile (which must truncate the file list to the matching
// CONTENT width — see the comment there for why).
func (m Model) importPopupWidth() int {
	w := min(76, m.width-4)
	if w < 50 {
		w = 50
	}
	return w
}

func (m Model) renderImportPopup() string {
	var body string
	switch m.importStep {
	case importPickFile:
		body = m.renderImportPickFile()
	case importPreview:
		body = m.renderImportPreview()
	case importRunning:
		body = styleHeader.Render("Importing…") + "\n\n" + styleMuted.Render("please wait")
	case importDone:
		body = m.renderImportDone()
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(m.importPopupWidth()).
		Render(body)
}

func (m Model) renderImportPickFile() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Import CSV") + "\n\n")
	if m.importErr != nil {
		b.WriteString(styleErr.Render("✗ "+m.importErr.Error()) + "\n\n")
	}
	b.WriteString(styleMuted.Render("Pick a bank CSV export (N26, ING, DKB, or a generic CSV with date/description/amount columns).") + "\n\n")

	// bubbles/filepicker never truncates long file names itself — it emits
	// them at full length. The bordered popup below applies lipgloss
	// Width(), which WORD-WRAPS anything too long instead of truncating,
	// silently turning one file-list row into two physical lines. That
	// desynced the file list's actual height from fp.SetHeight()'s budget,
	// pushing the footer (and the bottom of the list itself) off the
	// bottom of the popup. Truncate each row ourselves first so 1 file =
	// always exactly 1 physical line.
	contentW := m.importPopupWidth() - 6 // border(2) + padding(4)
	for _, line := range strings.Split(m.fp.View(), "\n") {
		b.WriteString(ansi.Truncate(line, contentW, "…") + "\n")
	}

	b.WriteString(styleMuted.Render("↑/↓ or j/k: navigate  ·  enter: open dir / select file  ·  esc: cancel"))
	return b.String()
}

func (m Model) renderImportPreview() string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Import Preview") + "\n\n")
	b.WriteString(fmt.Sprintf("File: %s\n", filepath.Base(m.importPath)))
	b.WriteString(fmt.Sprintf("Transactions found: %d\n\n", len(m.importParsed)))

	if len(m.importParsed) == 0 {
		b.WriteString(styleErr.Render("No transactions detected in this file — check it's a supported format.") + "\n\n")
	} else {
		minD, maxD := m.importParsed[0].Date, m.importParsed[0].Date
		var income, expense float64
		for _, t := range m.importParsed {
			if t.Date.Before(minD) {
				minD = t.Date
			}
			if t.Date.After(maxD) {
				maxD = t.Date
			}
			if t.Amount >= 0 {
				income += t.Amount
			} else {
				expense += t.Amount
			}
		}
		b.WriteString(fmt.Sprintf("Date range: %s – %s\n", minD.Format("2006-01-02"), maxD.Format("2006-01-02")))
		b.WriteString(styleIncome.Render(fmt.Sprintf("Income:   %+.2f€", income)) + "\n")
		b.WriteString(styleExpense.Render(fmt.Sprintf("Expenses: %+.2f€", expense)) + "\n\n")

		b.WriteString(styleMuted.Render("Sample:") + "\n")
		n := min(5, len(m.importParsed))
		for i := 0; i < n; i++ {
			t := m.importParsed[i]
			amtStyle := styleIncome
			if t.Amount < 0 {
				amtStyle = styleExpense
			}
			desc := t.Description
			if r := []rune(desc); len(r) > 40 {
				desc = string(r[:39]) + "…"
			}
			b.WriteString(fmt.Sprintf("  %s  %s  %s\n", t.Date.Format("2006-01-02"), amtStyle.Render(fmt.Sprintf("%9.2f€", t.Amount)), desc))
		}
		if len(m.importParsed) > n {
			b.WriteString(styleMuted.Render(fmt.Sprintf("  … and %d more\n", len(m.importParsed)-n)))
		}
	}

	if m.importEditingAcct {
		b.WriteString("\n" + styleMuted.Render("Account: ") + m.importAcctInput.View() + "\n")
	} else {
		acct := m.importAcctInput.Value()
		if acct == "" {
			acct = "(none — generic import)"
		}
		b.WriteString("\n" + styleMuted.Render(fmt.Sprintf("Account: %s  (t to edit)", acct)) + "\n")
	}

	aiLabel := "off"
	if m.importUseAI {
		aiLabel = "on"
	}
	b.WriteString(styleMuted.Render(fmt.Sprintf("AI-categorize uncategorized entries: %s  (a to toggle)", aiLabel)) + "\n")
	b.WriteString(styleMuted.Render("enter: import  ·  esc: back  ·  ctrl+c: quit"))
	return b.String()
}

func (m Model) renderImportDone() string {
	var b strings.Builder
	if m.importErr != nil {
		b.WriteString(styleErr.Render("✗ Import failed: "+m.importErr.Error()) + "\n\n")
	} else {
		b.WriteString(styleOK.Render(fmt.Sprintf("✓ Imported %d transaction(s)", m.importResult.Imported)) + "\n")
		if m.importUseAI && m.importResult.AICategorized > 0 {
			b.WriteString(styleMuted.Render(fmt.Sprintf("AI-categorized: %d", m.importResult.AICategorized)) + "\n")
		}
	}
	b.WriteString("\n" + styleMuted.Render("press any key to continue"))
	return b.String()
}

// renderHeader draws the one header shared by every view: app name +
// current section on the left, live date on the right, rule underneath.
// section is what changes ("Transactions", "Summary", "New Entry", "Help").
func (m Model) renderHeader(section string) string {
	left := styleHeader.Render("budgetctl") + styleMuted.Render(" · "+section)
	right := styleMuted.Render(time.Now().Format("Mon 02 Jan"))
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right + "\n" +
		styleDivider.Render(strings.Repeat("─", m.width)) + "\n"
}

// listStartRow returns the row (0-indexed within the rendered View()) the
// first transaction row appears on: header title(1) + rule(1) + tabs(1) +
// divider(1), plus an account-tab row when more than one account exists,
// plus any active search/categorize prompt. Shared by renderList (to size
// the visible window) and the mouse hit-test helpers below, so a click
// always lands on the row it visually appears to.
func (m Model) listStartRow() int {
	row := m.headRowCount() + 1 // + the column header (narrow) or the panel's top border (wide)
	if m.wide() {
		row++ // wide: the in-panel column header sits below the border
	}
	if m.searching {
		row += 2
	}
	if m.inPalette {
		row += 8
	}
	if m.searchQ != "" {
		row++
	}
	if m.categoryFilter != "" {
		row++
	}
	if m.categorizing {
		row++
	}
	if m.savingRule {
		row++
	}
	return row
}

// monthTabWindow returns the slice of m.months (and its start index into
// the full slice) that fits within width and should be visible in the tab
// bar right now. With enough months (18+ after importing more than a
// year), rendering every tab unconditionally overflows the terminal width
// with no way to reach the tabs that don't fit — this windows them the
// same way the vertical transaction list already windows rows around
// m.cursor: scrolled just enough to keep activeTab in view, not recentered
// on every change. Shared by renderList/renderSummaryView (rendering) and
// tabHitTest (hit-testing) so a click always lands on the tab it visually
// appears to be over.
func (m Model) monthTabWindow(width int) (visible []string, start int) {
	if len(m.months) == 0 {
		return nil, 0
	}
	// First pass at full width: if everything fits, no window (and so no
	// scroll indicators) needed at all.
	if monthTabFitCount(m.months, width) >= len(m.months) {
		return m.months, 0
	}
	// Windowing is needed, which means at least one scroll indicator will
	// render — reserve room for both up front so the rendered line never
	// exceeds width. Conservative (may fit one fewer tab than technically
	// possible on whichever side ends up with no indicator), but simple
	// and always correct.
	indicatorW := lipgloss.Width(styleMuted.Render("‹ ")) + lipgloss.Width(styleMuted.Render(" ›"))
	count := monthTabFitCount(m.months, width-indicatorW)
	if count < 1 {
		count = 1
	}
	start = 0
	if m.activeTab >= count {
		start = m.activeTab - count + 1
	}
	if start > len(m.months)-count {
		start = len(m.months) - count
	}
	if start < 0 {
		start = 0
	}
	end := start + count
	return m.months[start:end], start
}

// monthTabFitCount returns how many leading months fit within width when
// rendered as tabs (all inactive-width, since active/inactive share the
// same Padding(0,2) sizing).
func monthTabFitCount(months []string, width int) int {
	count := 0
	usedW := 0
	for _, mo := range months {
		w := lipgloss.Width(styleTabInact.Render(mo))
		if count > 0 && usedW+w > width {
			break
		}
		usedW += w
		count++
	}
	return count
}

// renderMonthTabBar renders the (possibly windowed) month tab row, with a
// "…" indicator on whichever side has months scrolled out of view.
func (m Model) renderMonthTabBar(width int) string {
	visible, start := m.monthTabWindow(width)
	if len(visible) == 0 {
		return "\n"
	}
	var parts []string
	if start > 0 {
		parts = append(parts, styleMuted.Render("‹ "))
	}
	for i, mo := range visible {
		globalIdx := start + i
		if globalIdx == m.activeTab {
			parts = append(parts, styleTabActive.Render(mo))
		} else {
			parts = append(parts, styleTabInact.Render(mo))
		}
	}
	if start+len(visible) < len(m.months) {
		parts = append(parts, styleMuted.Render(" ›"))
	}
	return strings.Join(parts, "") + "\n"
}

// tabHitTest returns the month index at column x on the tab row, or -1 if
// the click didn't land on a tab. Mirrors renderMonthTabBar's windowing
// exactly, including the "‹"/"›" scroll indicators, so clicking the
// leftmost/rightmost visible tab always maps to the right month.
func (m Model) tabHitTest(x, y int) int {
	if y != m.tabRowY() || len(m.months) == 0 {
		return -1
	}
	visible, start := m.monthTabWindow(m.width)
	col := 0
	if start > 0 {
		col += lipgloss.Width(styleMuted.Render("‹ "))
	}
	for i, mo := range visible {
		globalIdx := start + i
		w := lipgloss.Width(styleTabInact.Render(mo))
		if globalIdx == m.activeTab {
			w = lipgloss.Width(styleTabActive.Render(mo))
		}
		if x >= col && x < col+w {
			return globalIdx
		}
		col += w
	}
	return -1
}

// accountTabHitTest returns the account index at column x on the account tab
// row (only rendered when there's more than one account), or -1 if the click
// didn't land on a tab. -1 also stands for "the click missed", so callers
// checking "which account was selected" must first confirm the row matched;
// activeAccountName/m.accounts[-1] is never dereferenced here directly —
// the returned index is offset by one internally so -1 ("All") is a valid hit.
func (m Model) accountTabHitTest(x, y int) int {
	if y != m.acctRowY() || len(m.accounts) == 0 {
		return -2
	}
	col := m.acctCol0()
	labels := append([]string{"All"}, m.accounts...)
	for i, label := range labels {
		w := lipgloss.Width(styleAcctTabInact.Render(label))
		if i-1 == m.activeAccount {
			w = lipgloss.Width(styleAcctTabActive.Render(label))
		}
		if x >= col && x < col+w {
			return i - 1
		}
		col += w
	}
	return -2
}

// rowHitTest returns the transaction index at row y, or -1 if the click
// landed outside the visible list rows. Mirrors the exact scroll-window
// math renderList uses so a click lands on the transaction it visually
// appears to be over.
func (m Model) rowHitTest(y int) int {
	idx := y - m.listStartRow()
	if idx < 0 || len(m.txs) == 0 {
		return -1
	}
	listH := m.listRows()
	winStart := 0
	if m.cursor >= listH {
		winStart = m.cursor - listH + 1
	}
	txIdx := winStart + idx
	if txIdx >= len(m.txs) {
		return -1
	}
	return txIdx
}

func (m Model) renderList() string {
	w := m.width
	head := m.headBase()

	// Narrow layout: the dimmed column header takes the place the divider used
	// to have, so the list still starts on listStartRow. Wide layout: the
	// column header lives inside the Transactions panel instead.
	if !m.wide() {
		head = append(head, "  "+ansi.Truncate(m.columnHeader(), max(w-2, 0), ""))
	}

	if m.searching {
		head = append(head, "  "+m.searchInput.View(), "")
	}
	if m.inPalette {
		head = append(head, "  "+m.paletteInput.View())
		matches := palette.Match(paletteCommands, m.paletteInput.Value())
		if len(matches) > 6 {
			matches = matches[:6]
		}
		if len(matches) == 0 {
			head = append(head, "    "+styleHelp.Render("no matching command"))
		}
		for i, c := range matches {
			row := fmt.Sprintf("%-9s %s", c.Name, c.Desc)
			if i == m.paletteCursor {
				head = append(head, "    "+styleSelected.Render("▶ "+row))
			} else {
				head = append(head, "      "+styleHelp.Render(row))
			}
		}
		head = append(head, "")
	}
	if m.searchQ != "" {
		head = append(head, styleMuted.Render("  /"+m.searchQ))
	}
	if m.categoryFilter != "" {
		head = append(head, styleMuted.Render("  filter: ")+styleCategory.Render(m.categoryFilter)+styleMuted.Render("  (esc to clear)"))
	}
	if m.categorizing {
		head = append(head, "  "+styleCategory.Render("category: ")+m.catInput.View())
	} else if m.savingRule {
		head = append(head, "  "+styleCategory.Render("save as rule? ")+m.ruleInput.View())
	} else if m.selecting {
		head = append(head, "  "+styleSelected.Render(fmt.Sprintf("select: %d", len(m.selected)))+
			styleHelp.Render("  space toggle  A all  c categorize  esc cancel"))
	}

	listH := m.listRows()
	leftW := w
	if m.wide() {
		leftW = w - insightsWidth - 1
	}
	// Row convention (both layouts): every row is a 2-cell lead ("  " or the
	// accent bar "▌ ") plus textW cells of text. Inside the wide panel the
	// usable width is leftW-3 (border, one pad column, border).
	fullW := w
	if m.wide() {
		fullW = leftW - 3
	}
	textW := fullW - 2
	selRowW := textW
	if m.selecting {
		selRowW -= 4 // room for the "[x] " checkbox prefix
	}

	var rows []string
	if len(m.txs) == 0 {
		if listH >= 3 {
			rows = strings.Split(emptystate.Render(fullW, listH, "", "No transactions yet", "press n to add one, or import a CSV: budgetctl import file.csv"), "\n")
		} else {
			rows = []string{styleHelp.Render("  No transactions yet — press n to add one, or import a CSV: budgetctl import file.csv")}
		}
	} else {
		start := 0
		if m.cursor >= listH {
			start = m.cursor - listH + 1
		}
		end := min(len(m.txs), start+listH)
		for i := start; i < end; i++ {
			t := &m.txs[i]
			checkbox, checkboxPlain := "", ""
			if m.selecting {
				if m.selected[t.ID] {
					checkbox, checkboxPlain = styleSelected.Render("[x]")+" ", "[x] "
				} else {
					checkbox, checkboxPlain = styleHelp.Render("[ ]")+" ", "[ ] "
				}
			}
			switch {
			case i == m.cursor:
				// ui.Row keeps the cells' own colors (it re-paints the selection
				// background after every inner reset) and lifts dimmed (Subtle)
				// text to Muted, which can equal the selection background.
				rows = append(rows, ui.Row(fullW, true, checkboxPlain+formatTxRowCols(t, selRowW, "", m.showAcctCol())))
			case i == m.hoverRow:
				rows = append(rows, "  "+checkbox+theme.HoverV2.Width(selRowW).Render(formatTxRowCols(t, selRowW, "", m.showAcctCol())))
			default:
				rows = append(rows, "  "+checkbox+formatTxRowCols(t, selRowW, m.searchQ, m.showAcctCol()))
			}
		}
	}

	var body string
	if m.wide() {
		content := "  " + m.columnHeader() + "\n" + strings.Join(rows, "\n")
		left := ui.Panel(leftW, listH+3, "Transactions", content, true)
		right := ui.Panel(insightsWidth, listH+3, "Insights", m.insightsContent(insightsWidth-3), false)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	} else {
		body = strings.Join(rows, "\n")
	}

	return ui.Frame(m.height, strings.Join(head, "\n"), body, m.listFooter(w))
}

// listFooter is the divider plus ONE status/hint line: a pending confirmation,
// error or status message, else the key hints in priority order. The right
// side carries the cursor position (and the selected sum while multi-selecting).
func (m Model) listFooter(w int) string {
	rowW := w - 2
	right := m.footerRight()
	rule := styleDivider.Render(strings.Repeat("─", w))

	if m.deleteTarget != nil || m.err != nil || m.status != "" {
		var bar string
		switch {
		case m.deleteTarget != nil:
			bar = styleErr.Render(fmt.Sprintf("Delete %q (%+.2f€)?  ", m.deleteTarget.Description, m.deleteTarget.Amount)) +
				styleHelp.Render("y confirm · any key cancel")
		case m.err != nil:
			bar = styleErr.Render("✗ " + m.err.Error())
		default:
			bar = styleOK.Render("✓ " + m.status)
		}
		// No room for both → drop the right side rather than overflow.
		if rowW-lipgloss.Width(bar)-lipgloss.Width(right) < 0 {
			right = ""
		}
		return rule + "\n  " + statusbar.Line(rowW, bar, right)
	}

	hints := statusbar.Hints(rowW-lipgloss.Width(right)-1,
		[2]string{"?", "help"}, [2]string{"q", "quit"}, [2]string{"enter", "details"}, [2]string{"n", "new"},
		[2]string{"/", "search"}, [2]string{"s", "summary"}, [2]string{"c", "categorize"}, [2]string{"e", "edit"},
		[2]string{"d", "delete"}, [2]string{"tab", "month"}, [2]string{"i", "import"}, [2]string{"f", "filter"},
		[2]string{"v", "select"}, [2]string{"u", "undo"}, [2]string{"a", "ai-categorize"}, [2]string{":", "cmd"},
		[2]string{"y", "year"}, [2]string{"[/]", "account"})
	return rule + "\n  " + statusbar.Line(rowW, hints, right)
}

func (m Model) renderForm() string {
	var b strings.Builder
	heading := "New Entry"
	if m.editTx != nil {
		heading = "Edit Entry"
	}
	b.WriteString(m.renderHeader(heading) + "\n")
	for i := range m.form {
		label := formLabels[i]
		labelStyle := styleMuted
		if i == m.formIdx {
			labelStyle = styleHeader
		}
		b.WriteString("  " + labelStyle.Render(fmt.Sprintf("%-13s", label)) + m.form[i].View() + "\n")
	}
	b.WriteString("\n  " + styleHelp.Render("negative amount = expense · positive = income") + "\n")
	if m.err != nil {
		b.WriteString("\n  " + styleErr.Render("✗ "+m.err.Error()) + "\n")
	}
	b.WriteString("\n  " + styleHelp.Render("tab/enter: next field  ·  ctrl+s: save  ·  esc: cancel") + "\n")
	return b.String()
}

func (m Model) helpContent() string {
	body := keymap.Bare().
		Section("Navigation").
		Row("j / ↓", "move down").
		Row("k / ↑", "move up").
		Row("g / G", "jump to top / bottom").
		Row("pgdn/up", "page down / up").
		Row("tab", "next month").
		Row("s-tab", "previous month").
		Row("y / Y", "jump to next / previous year (skips to where data exists)").
		Section("Entries").
		Row("enter", "view full details (untruncated description, source, raw row)").
		Row("n", "new entry (manual income/expense)").
		Row("i", "import CSV (N26, ING, DKB, generic) — t at preview: tag account").
		Row("e", "edit selected entry").
		Row("d", "delete entry (asks to confirm)").
		Row("c", "set category for selected entry — then offers to save it as a rule (pattern -> category) applied to every matching transaction. Type \"Cat1;Cat2\" to split the amount evenly across categories instead").
		Row("a", "AI-categorize all uncategorized entries (missionctl Bundle feature)").
		Section("Data").
		Row("/", "search transactions (esc clears)").
		Row(":", "command palette — type an action by name").
		Row("f", "filter by category — fuzzy-searchable popup (esc clears)").
		Row("s", "summary — categories, charts, budget goals").
		Row("g", "(in summary) set a budget goal — \"category amount\"").
		Row("t", "(in summary) AI-suggest category renames for language mismatches — review before applying").
		Section("Accounts").
		Text("No separate \"create account\" step — an account is just a text tag").
		Text("on transactions. It appears the first time you tag something with it:").
		Text("  · CLI:  budgetctl import file.csv --account \"Sparkasse\"").
		Text("  · TUI:  i → pick file → t (at preview) → type a name → enter").
		Text("Redo a bad import: budgetctl reset --account \"Sparkasse\" (asks to confirm)").
		Row("[ / ]", "cycle accounts (tab/click also works)").
		Section("Other").
		Row("o", "settings — sync your data across devices (iCloud Drive, Dropbox, …)").
		Row("p", "profiles — fully separate databases (e.g. \"firma\" vs personal accounts)").
		Row("?", "toggle this help").
		Row("q", "quit").
		Text("").
		Text("Import & categorize on the CLI: budgetctl import file.csv · budgetctl tag PATTERN --category NAME").
		String()
	return m.renderHeader("Help") + body
}

// openHelp sizes and populates the transient help popup (see
// renderHelpPopup/overlay.Center) from the ACTUAL rendered background
// height, not the terminal size — budgetctl's list has no enclosing
// border (inset 0 is safe), but the popup still shouldn't try to be
// taller than what's actually on screen.
func (m Model) openHelp() Model {
	bg := m.renderList()
	bgLines := strings.Split(bg, "\n")

	safeH := max(6, len(bgLines))
	popH := min(safeH, 22)
	popW := min(70, m.width)
	if popW < 40 {
		popW = 40
	}

	vp := viewport.New(viewport.WithWidth(popW-4), viewport.WithHeight(popH-4)) // border 1+1, padding(0,1) → 2 cols; -1 row for footer, -1 blank spacer above it
	vp.SetContent(m.helpContent())

	m.helpVP = vp
	m.helpPopW = popW
	m.helpPopH = popH
	m.view = viewHelp
	return m
}

var stylePopupBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBlue).Padding(0, 1)

// renderHelpPopup renders the help viewport in a bordered box, meant to be
// composited over the list view via overlay.Center rather than replacing
// the whole screen — the list stays visible around it.
func (m Model) renderHelpPopup() string {
	footer := "esc / ?  close"
	if m.helpVP.TotalLineCount() > m.helpVP.Height() {
		footer = fmt.Sprintf("j/k scroll (%d%%)  ·  %s", int(m.helpVP.ScrollPercent()*100), footer)
	}
	body := m.helpVP.View() + "\n\n" + styleHelp.Render(footer)
	return stylePopupBorder.Width(m.helpPopW).Render(body)
}

func (m Model) renderSummaryView() string {
	var b strings.Builder

	b.WriteString(m.renderHeader("Summary"))

	// month tabs (windowed — see renderMonthTabBar)
	b.WriteString(m.renderMonthTabBar(m.width))

	if len(m.accounts) > 0 {
		var aparts []string
		labels := append([]string{"All"}, m.accounts...)
		for i, label := range labels {
			if i-1 == m.activeAccount {
				aparts = append(aparts, styleAcctTabActive.Render(label))
			} else {
				aparts = append(aparts, styleAcctTabInact.Render(label))
			}
		}
		b.WriteString(strings.Join(aparts, "") + "\n")
	}

	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	vpH := m.height - 7
	if len(m.accounts) > 0 {
		vpH--
	}
	if m.settingGoal {
		vpH--
	}
	m.vp.SetHeight(vpH)
	b.WriteString(m.vp.View())

	if m.settingGoal {
		b.WriteString("  " + styleCategory.Render("goal (category amount): ") + m.goalInput.View() + "\n")
	}

	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height() {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	b.WriteString("\n  " + styleHelp.Render("esc:back  g:goal  t:translate  tab:month  y:year  ]:account  ↑↓:scroll  q:quit") + styleMuted.Render(pct))
	return b.String()
}

func renderSummary(sum *models.Summary, goals []models.GoalStatus, trend []models.MonthlyPoint, recurring []budget.RecurringPattern, width int) string {
	if sum == nil {
		return "No data for this month."
	}
	var b strings.Builder

	b.WriteString("  " + styleSummaryH.Render(fmt.Sprintf("Summary: %s", sum.Month)) + "\n\n")

	incomeColor := styleIncome
	expColor := styleExpense
	netColor := styleOK
	if sum.Net < 0 {
		netColor = styleExpense
	}

	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Income:", incomeColor.Render(fmt.Sprintf("%+.2f €", sum.Income))))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Expenses:", expColor.Render(fmt.Sprintf("%+.2f €", sum.Expenses))))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Net:", netColor.Render(fmt.Sprintf("%+.2f €", sum.Net))))

	if len(trend) > 1 {
		b.WriteString("\n  " + styleSummaryH.Render(fmt.Sprintf("Trend (last %d months):", len(trend))) + "\n\n")
		var nets []float64
		var labels []string
		for _, p := range trend {
			nets = append(nets, p.Net)
			labels = append(labels, p.Month)
		}
		b.WriteString("  " + sparkline(nets) + "  " + styleMuted.Render(fmt.Sprintf("(%s → %s)", labels[0], labels[len(labels)-1])) + "\n\n")

		// Per-month breakdown, grouped by year — the sparkline only plots
		// Net, but each MonthlyPoint already carries Income/Expenses too;
		// newest first to match the transaction list's own ordering.
		lastYear := ""
		for i := len(trend) - 1; i >= 0; i-- {
			p := trend[i]
			year := p.Month[:4]
			if year != lastYear {
				b.WriteString("\n  " + styleSummaryH.Render(year) + "\n")
				lastYear = year
			}
			pNetColor := styleOK
			if p.Net < 0 {
				pNetColor = styleExpense
			}
			b.WriteString(fmt.Sprintf("  %-9s %s  %s  %s\n",
				p.Month,
				styleIncome.Render(fmt.Sprintf("%+9.2f €", p.Income)),
				styleExpense.Render(fmt.Sprintf("%+9.2f €", p.Expenses)),
				pNetColor.Render(fmt.Sprintf("%+9.2f €", p.Net)),
			))
		}
	}

	b.WriteString("\n  " + styleSummaryH.Render("By category:") + "\n\n")

	type kv struct {
		k string
		v float64
	}
	var sorted []kv
	for k, v := range sum.ByCategory {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v < sorted[j].v })

	maxAmt := 0.0
	for _, item := range sorted {
		if a := math.Abs(item.v); a > maxAmt {
			maxAmt = a
		}
	}

	barW := 20
	for _, item := range sorted {
		cat := item.k
		if cat == "" {
			cat = "(uncategorized)"
		}
		amtStr := fmt.Sprintf("%+.2f €", item.v)
		barLen := 0
		if maxAmt > 0 {
			barLen = int(math.Abs(item.v) / maxAmt * float64(barW))
		}
		bar := ""
		if item.v < 0 {
			bar = styleExpense.Render(strings.Repeat("█", barLen))
		} else {
			bar = styleIncome.Render(strings.Repeat("█", barLen))
		}
		b.WriteString(fmt.Sprintf("  %-22s %s  %s%s\n",
			styleCategory.Render(cat),
			fmt.Sprintf("%10s", amtStr),
			bar,
			strings.Repeat("░", barW-barLen),
		))
	}

	// ── Goals ────────────────────────────────────────────────────────────────
	if len(goals) > 0 {
		b.WriteString("\n  " + styleSummaryH.Render("Budget goals:") + "\n\n")
		for _, gs := range goals {
			filled := int(gs.Percent / 100 * float64(barW))
			if filled > barW {
				filled = barW
			}
			if filled < 0 {
				filled = 0
			}
			barStyle := styleOK
			labelStyle := styleOK
			if gs.Percent >= 100 {
				barStyle = styleExpense
				labelStyle = styleExpense
			} else if gs.Percent >= 80 {
				barStyle = lipgloss.NewStyle().Foreground(colorAmber)
				labelStyle = lipgloss.NewStyle().Foreground(colorAmber)
			}
			bar := "[" + barStyle.Render(strings.Repeat("█", filled)) +
				styleMuted.Render(strings.Repeat("░", barW-filled)) + "]"
			pctStr := labelStyle.Render(fmt.Sprintf("%5.0f%%", gs.Percent))
			remaining := ""
			if gs.Remaining >= 0 {
				remaining = styleOK.Render(fmt.Sprintf("  %.0f€ left", gs.Remaining))
			} else {
				remaining = styleExpense.Render(fmt.Sprintf("  %.0f€ over", -gs.Remaining))
			}
			b.WriteString(fmt.Sprintf("  %-22s %s  %s  %s%s\n",
				styleCategory.Render(gs.Category),
				fmt.Sprintf("%10s", fmt.Sprintf("%.0f/%.0f€", gs.Spent, gs.Monthly)),
				bar, pctStr, remaining,
			))
		}
	}

	// ── Savings insights ────────────────────────────────────────────────────
	// Recurring payments normalized to a monthly figure (weekly × 4.33,
	// annual ÷ 12) so "what am I paying every month on autopilot" is one
	// number, not three unit systems mixed together — the cheapest concrete
	// answer to "how can I save" without inventing new tracking.
	if len(recurring) > 0 {
		sorted := append([]budget.RecurringPattern(nil), recurring...)
		monthly := func(p budget.RecurringPattern) float64 {
			switch p.Frequency {
			case "weekly":
				return p.Amount * 4.33
			case "annual":
				return p.Amount / 12
			default:
				return p.Amount
			}
		}
		sort.Slice(sorted, func(i, j int) bool { return monthly(sorted[i]) > monthly(sorted[j]) })

		var total float64
		for _, p := range sorted {
			total += monthly(p)
		}

		b.WriteString("\n  " + styleSummaryH.Render("Savings insights:") + "\n\n")
		b.WriteString(fmt.Sprintf("  %d recurring payments ≈ %s\n\n",
			len(sorted), styleExpense.Render(fmt.Sprintf("%.2f €/month", total))))
		for _, p := range sorted {
			cat := p.Category
			if cat == "" {
				cat = "(uncategorized)"
			}
			b.WriteString(fmt.Sprintf("  %-30s %s  %-8s %s\n",
				truncRunes(p.Description, 30),
				styleExpense.Render(fmt.Sprintf("%7.2f €", p.Amount)),
				p.Frequency,
				styleCategory.Render(cat),
			))
		}
	}

	_ = width
	return b.String()
}
