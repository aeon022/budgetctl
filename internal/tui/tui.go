package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList    view = iota
	viewSummary view = iota
	viewHelp    view = iota
	viewForm    view = iota
)

// form field indices
const (
	fDate = iota
	fDesc
	fAmount
	fCategory
	fCount
)

var formLabels = [fCount]string{"Date", "Description", "Amount", "Category"}

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorBlue   = lipgloss.AdaptiveColor{Light: "25",  Dark: "33"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "28",  Dark: "42"}
	colorRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "243", Dark: "246"}
	colorSubtle = lipgloss.AdaptiveColor{Light: "250", Dark: "244"}
	colorAmber  = lipgloss.AdaptiveColor{Light: "214", Dark: "220"}

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(colorBlue).
			Padding(0, 2)
	styleTabInact   = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
	styleDivider    = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHeader     = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleHelp       = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr        = lipgloss.NewStyle().Foreground(colorRed)
	styleOK         = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted      = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected   = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "189", Dark: "17"}).
				Foreground(lipgloss.AdaptiveColor{Light: "16",  Dark: "255"}).
				Bold(true)
	styleIncome    = lipgloss.NewStyle().Foreground(colorGreen)
	styleExpense   = lipgloss.NewStyle().Foreground(colorRed)
	styleCategory  = lipgloss.NewStyle().Foreground(colorAmber)
	styleSummaryH  = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleToday     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"}).Bold(true)
	styleDateWeek  = lipgloss.NewStyle().Foreground(colorMuted)
	styleDateMonth = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "247", Dark: "242"})
	styleDateOld   = lipgloss.NewStyle().Foreground(colorSubtle)
)

// ── Messages ──────────────────────────────────────────────────────────────────

type txLoadedMsg struct {
	txs    []models.Transaction
	months []string
	sum    *models.Summary
	goals  []models.GoalStatus
}
type errMsg struct{ err error }
type txSavedMsg struct{ err error }
type txDeletedMsg struct{ err error }

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	view   view
	width  int
	height int

	txs        []models.Transaction
	cursor     int
	months     []string // ["2026-06", "2026-05", ...]
	activeTab  int      // index into months; -1 = all
	summary    *models.Summary
	goals      []models.GoalStatus
	searchQ    string
	searching  bool
	searchInput textinput.Model
	filterCat  string
	vp         viewport.Model

	// add/edit form
	form    [fCount]textinput.Model
	formIdx int
	editTx  *models.Transaction // nil = new entry

	// quick categorize + delete confirm
	categorizing bool
	catInput     textinput.Model
	deleteTarget *models.Transaction

	status     string
	statusTime time.Time
	err        error
}

func New() Model {
	si := textinput.New()
	si.Placeholder = "search transactions…"
	si.CharLimit = 100
	ci := textinput.New()
	ci.Placeholder = "category…"
	ci.CharLimit = 60
	return Model{searchInput: si, catInput: ci, activeTab: 0}
}

func newForm(t *models.Transaction) [fCount]textinput.Model {
	var form [fCount]textinput.Model
	placeholders := [fCount]string{
		time.Now().Format("2006-01-02"),
		"Rewe Einkauf",
		"-42.50   (negative = expense, positive = income)",
		"groceries (optional)",
	}
	for i := range form {
		in := textinput.New()
		in.Placeholder = placeholders[i]
		in.CharLimit = 200
		form[i] = in
	}
	if t != nil {
		form[fDate].SetValue(t.Date.Format("2006-01-02"))
		form[fDesc].SetValue(t.Description)
		form[fAmount].SetValue(fmt.Sprintf("%.2f", t.Amount))
		form[fCategory].SetValue(t.Category)
	} else {
		form[fDate].SetValue(time.Now().Format("2006-01-02"))
	}
	return form
}

func Run() error {
	m := New()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadCmd("", ""), tea.WindowSize())
}

func (m Model) activeMonth() string {
	if m.activeTab < 0 || m.activeTab >= len(m.months) {
		return ""
	}
	return m.months[m.activeTab]
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = viewport.New(msg.Width, m.height-6)

	case txLoadedMsg:
		m.txs = msg.txs
		m.summary = msg.sum
		m.goals = msg.goals
		if len(msg.months) > 0 {
			m.months = msg.months
		}
		if m.cursor >= len(m.txs) {
			m.cursor = max(0, len(m.txs)-1)
		}
		if m.view == viewSummary && m.summary != nil {
			m.vp.SetContent(renderSummary(m.summary, m.goals, m.width))
		}

	case errMsg:
		m.err = msg.err

	case txSavedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.view = viewList
			m.editTx = nil
			m.setStatus("saved")
			return m, loadCmd(m.activeMonth(), m.searchQ)
		}

	case txDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus("deleted")
			return m, loadCmd(m.activeMonth(), m.searchQ)
		}

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.view == viewSummary {
				m.vp.LineUp(3)
			} else if m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseButtonWheelDown:
			if m.view == viewSummary {
				m.vp.LineDown(3)
			} else if m.cursor < len(m.txs)-1 {
				m.cursor++
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.err = nil
		if time.Since(m.statusTime) > 4*time.Second {
			m.status = ""
		}
		switch m.view {
		case viewList:
			return m.updateList(msg)
		case viewSummary:
			return m.updateSummary(msg)
		case viewForm:
			return m.updateForm(msg)
		case viewHelp:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc", "?":
				m.view = viewList
			}
			return m, nil
		}
	}

	if m.view == viewSummary {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// delete confirmation (status-bar prompt)
	if m.deleteTarget != nil {
		switch msg.String() {
		case "y", "Y":
			target := m.deleteTarget
			m.deleteTarget = nil
			return m, deleteTxCmd(target.ID)
		default:
			m.deleteTarget = nil
		}
		return m, nil
	}

	// quick categorize input
	if m.categorizing {
		switch msg.String() {
		case "enter":
			m.categorizing = false
			m.catInput.Blur()
			if len(m.txs) > 0 {
				id := m.txs[m.cursor].ID
				cat := strings.TrimSpace(m.catInput.Value())
				return m, setCategoryCmd(id, cat)
			}
			return m, nil
		case "esc":
			m.categorizing = false
			m.catInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.catInput, cmd = m.catInput.Update(msg)
		return m, cmd
	}

	if m.searching {
		switch msg.String() {
		case "enter":
			m.searchQ = m.searchInput.Value()
			m.searching = false
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ)
		case "esc":
			m.searching = false
			m.searchInput.SetValue("")
			m.searchQ = ""
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab + 1) % len(m.months)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ)
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ)
		}
	case "j", "down":
		if m.cursor < len(m.txs)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "pgdown", "ctrl+f":
		page := max(1, m.height/3)
		m.cursor = min(len(m.txs)-1, m.cursor+page)
	case "pgup", "ctrl+b":
		page := max(1, m.height/3)
		m.cursor = max(0, m.cursor-page)
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(m.txs)-1)
	case "S", "s":
		m.view = viewSummary
		m.vp.SetContent(renderSummary(m.summary, m.goals, m.width))
		m.vp.GotoTop()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
	case "?":
		m.view = viewHelp
	case "n":
		m.view = viewForm
		m.editTx = nil
		m.form = newForm(nil)
		m.formIdx = 0
		return m, m.form[fDate].Focus()
	case "e":
		if len(m.txs) > 0 {
			t := m.txs[m.cursor]
			m.view = viewForm
			m.editTx = &t
			m.form = newForm(&t)
			m.formIdx = 0
			return m, m.form[fDate].Focus()
		}
	case "d":
		if len(m.txs) > 0 {
			t := m.txs[m.cursor]
			m.deleteTarget = &t
		}
	case "c":
		if len(m.txs) > 0 {
			m.categorizing = true
			m.catInput.SetValue(m.txs[m.cursor].Category)
			m.catInput.CursorEnd()
			return m, m.catInput.Focus()
		}
	case "esc":
		if m.searchQ != "" {
			m.searchQ = ""
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), "")
		}
	}
	return m, nil
}

func (m Model) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.view = viewList
		return m, nil
	case "tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab + 1) % len(m.months)
			return m, loadCmd(m.activeMonth(), "")
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			return m, loadCmd(m.activeMonth(), "")
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = viewList
		m.editTx = nil
		return m, nil
	case "tab", "down":
		m.form[m.formIdx].Blur()
		m.formIdx = (m.formIdx + 1) % fCount
		return m, m.form[m.formIdx].Focus()
	case "shift+tab", "up":
		m.form[m.formIdx].Blur()
		m.formIdx = (m.formIdx - 1 + fCount) % fCount
		return m, m.form[m.formIdx].Focus()
	case "enter":
		if m.formIdx < fCount-1 {
			m.form[m.formIdx].Blur()
			m.formIdx++
			return m, m.form[m.formIdx].Focus()
		}
		return m.submitForm()
	case "ctrl+s":
		return m.submitForm()
	}
	var cmd tea.Cmd
	m.form[m.formIdx], cmd = m.form[m.formIdx].Update(msg)
	return m, cmd
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	dateStr := strings.TrimSpace(m.form[fDate].Value())
	desc := strings.TrimSpace(m.form[fDesc].Value())
	amountStr := strings.TrimSpace(m.form[fAmount].Value())
	category := strings.TrimSpace(m.form[fCategory].Value())

	if desc == "" {
		m.err = fmt.Errorf("description is required")
		return m, nil
	}
	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		m.err = fmt.Errorf("invalid date %q (use YYYY-MM-DD)", dateStr)
		return m, nil
	}
	amount, err := budget.ParseUserAmount(amountStr)
	if err != nil {
		m.err = err
		return m, nil
	}

	t := models.Transaction{
		Date:        date,
		Description: desc,
		Amount:      amount,
		Category:    category,
		Account:     "manual",
		Source:      "tui",
	}
	if m.editTx != nil {
		t.ID = m.editTx.ID
		t.Account = m.editTx.Account
		t.Source = m.editTx.Source
		return m, updateTxCmd(&t)
	}
	t.ID = fmt.Sprintf("manual-%d", time.Now().UnixNano())
	return m, insertTxCmd(&t)
}

func insertTxCmd(t *models.Transaction) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		return txSavedMsg{s.Upsert(context.Background(), t)}
	}
}

func updateTxCmd(t *models.Transaction) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		return txSavedMsg{s.Update(context.Background(), t)}
	}
}

func deleteTxCmd(id string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return txDeletedMsg{err}
		}
		defer s.Close()
		return txDeletedMsg{s.Delete(context.Background(), id)}
	}
}

func setCategoryCmd(id, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		return txSavedMsg{s.SetCategory(context.Background(), id, category)}
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.view {
	case viewSummary:
		return m.renderSummaryView()
	case viewHelp:
		return m.renderHelp()
	case viewForm:
		return m.renderForm()
	default:
		return m.renderList()
	}
}

// renderHeader draws the one header shared by every view: app name +
// current section on the left, live date on the right, rule underneath.
// section is what changes ("Transactions", "Summary", "New Entry", "Help").
func (m Model) renderHeader(section string) string {
	left := styleHeader.Render("budgetctl") + styleMuted.Render(" · "+section)
	right := styleMuted.Render(time.Now().Format("Mon, 02 Jan 2006"))
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right + "\n" +
		styleDivider.Render(strings.Repeat("─", m.width)) + "\n"
}

func (m Model) renderList() string {
	var b strings.Builder
	w := m.width

	b.WriteString(m.renderHeader("Transactions"))

	// ── month tab bar ──
	var parts []string
	for i, mo := range m.months {
		label := mo
		if i == m.activeTab {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTabInact.Render(label))
		}
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, "") + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n")

	overhead := 5 // header + rule + tabs + divider + statusbar
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
		overhead += 2
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
		overhead++
	}
	if m.categorizing {
		b.WriteString("  " + styleCategory.Render("category: ") + m.catInput.View() + "\n")
		overhead++
	}

	listH := m.height - overhead
	if listH < 1 {
		listH = 1
	}

	rowW := w - 2
	if len(m.txs) == 0 {
		b.WriteString("\n" + styleHelp.Render("  No transactions yet — press n to add one, or import a CSV: budgetctl import file.csv") + "\n")
	} else {
		start := 0
		if m.cursor >= listH {
			start = m.cursor - listH + 1
		}
		end := min(len(m.txs), start+listH)
		for i := start; i < end; i++ {
			t := &m.txs[i]
			line := formatTxRow(t, rowW)
			if i == m.cursor {
				line = styleSelected.Width(rowW).Render(line)
			}
			b.WriteString("  " + line + "\n")
		}
	}

	// ── status bar ──
	netStr := ""
	if m.summary != nil {
		col := styleIncome
		if m.summary.Net < 0 {
			col = styleExpense
		}
		netStr = styleMuted.Render(" net:") + col.Render(fmt.Sprintf(" %+.0f€", m.summary.Net))
	}
	posStr := ""
	if len(m.txs) > 0 {
		posStr = styleMuted.Render(fmt.Sprintf(" %d/%d", m.cursor+1, len(m.txs)))
	}

	var bar string
	if m.deleteTarget != nil {
		bar = styleErr.Render(fmt.Sprintf("Delete %q (%+.2f€)?  ", m.deleteTarget.Description, m.deleteTarget.Amount)) +
			styleHelp.Render("y confirm · any key cancel")
	} else if m.err != nil {
		bar = styleErr.Render("✗ " + m.err.Error())
	} else if m.status != "" {
		bar = styleOK.Render("✓ " + m.status)
	} else {
		bar = styleHelp.Render("n:new  e:edit  d:delete  c:categorize  s:summary  /:search  tab:month  ?:help  q:quit")
	}
	right := netStr + posStr
	pad := rowW - lipgloss.Width(bar) - lipgloss.Width(right)
	if pad < 0 {
		pad = 0
	}
	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString("  " + bar + strings.Repeat(" ", pad) + right)
	return b.String()
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

func (m Model) renderHelp() string {
	keyw := func(k string) string { return styleHeader.Render(fmt.Sprintf("%-10s", k)) }
	row := func(k, desc string) string { return "  " + keyw(k) + styleHelp.Render(desc) + "\n" }
	section := func(t string) string { return "\n  " + styleSummaryH.Render(t) + "\n" }

	var b strings.Builder
	b.WriteString(m.renderHeader("Help"))
	b.WriteString(section("Navigation"))
	b.WriteString(row("j / ↓", "move down"))
	b.WriteString(row("k / ↑", "move up"))
	b.WriteString(row("g / G", "jump to top / bottom"))
	b.WriteString(row("pgdn/pgup", "page down / up"))
	b.WriteString(row("tab", "next month"))
	b.WriteString(row("shift+tab", "previous month"))
	b.WriteString(section("Entries"))
	b.WriteString(row("n", "new entry (manual income/expense)"))
	b.WriteString(row("e", "edit selected entry"))
	b.WriteString(row("d", "delete entry (asks to confirm)"))
	b.WriteString(row("c", "set category for selected entry"))
	b.WriteString(section("Data"))
	b.WriteString(row("/", "search transactions (esc clears)"))
	b.WriteString(row("s", "summary — categories, charts, budget goals"))
	b.WriteString(section("Other"))
	b.WriteString(row("?", "toggle this help"))
	b.WriteString(row("q", "quit"))
	b.WriteString("\n" + styleHelp.Render("  Import & categorize on the CLI: budgetctl import file.csv · budgetctl tag PATTERN --category NAME") + "\n")
	return b.String()
}

func (m Model) renderSummaryView() string {
	var b strings.Builder

	b.WriteString(m.renderHeader("Summary"))

	// month tabs
	var parts []string
	for i, mo := range m.months {
		if i == m.activeTab {
			parts = append(parts, styleTabActive.Render(mo))
		} else {
			parts = append(parts, styleTabInact.Render(mo))
		}
	}
	if len(parts) > 0 {
		b.WriteString(strings.Join(parts, "") + "\n")
	}
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	m.vp.Height = m.height - 7
	b.WriteString(m.vp.View())

	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	b.WriteString("\n  " + styleHelp.Render("esc:back  tab:month  ↑↓:scroll  q:quit") + styleMuted.Render(pct))
	return b.String()
}

func renderSummary(sum *models.Summary, goals []models.GoalStatus, width int) string {
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
		if a := abs(item.v); a > maxAmt {
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
			barLen = int(abs(item.v) / maxAmt * float64(barW))
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

	_ = width
	return b.String()
}

// ── Commands ──────────────────────────────────────────────────────────────────

func loadCmd(month, query string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()

		txs, err := s.List(ctx, store.Filter{Month: month, Query: query, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		months, _ := s.ListMonths(ctx)

		// summary for active month
		sum, _ := s.Summary(ctx, month)

		// goals with current-month spend
		goals, _ := s.GoalStatuses(ctx, month)

		return txLoadedMsg{txs: txs, months: months, sum: sum, goals: goals}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

func formatTxRow(t *models.Transaction, width int) string {
	amtStr := fmt.Sprintf("%+8.2f€", t.Amount)
	amtStyled := ""
	if t.Amount >= 0 {
		amtStyled = styleIncome.Render(amtStr)
	} else {
		amtStyled = styleExpense.Render(amtStr)
	}

	cat := t.Category
	if cat == "" {
		cat = "—"
	}
	catStyled := styleCategory.Render(fmt.Sprintf("%-16s", cat))

	dateStr := t.Date.Format("2006-01-02")
	dateStyled := coloredDate(dateStr, t.Date)

	// description truncated
	descW := width - 12 - 10 - 18 - 4
	if descW < 10 {
		descW = 10
	}
	desc := t.Description
	if len(desc) > descW {
		desc = desc[:descW-1] + "…"
	}

	return fmt.Sprintf("%s  %s  %s  %s",
		dateStyled,
		amtStyled,
		catStyled,
		desc,
	)
}

func coloredDate(s string, t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return styleToday.Render(s)
	case t.After(now.AddDate(0, 0, -7)):
		return styleDateWeek.Render(s)
	case t.After(now.AddDate(0, 0, -30)):
		return styleDateMonth.Render(s)
	default:
		return styleDateOld.Render(s)
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
