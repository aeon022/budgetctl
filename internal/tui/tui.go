package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorBlue   = lipgloss.AdaptiveColor{Light: "21", Dark: "39"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	colorRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "244", Dark: "240"}
	colorSubtle = lipgloss.AdaptiveColor{Light: "250", Dark: "237"}
	colorAmber  = lipgloss.AdaptiveColor{Light: "214", Dark: "220"}

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(colorBlue).
			Padding(0, 2)
	styleTabInact  = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
	styleDivider   = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHeader    = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleHelp      = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr       = lipgloss.NewStyle().Foreground(colorRed)
	styleOK        = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted     = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected  = lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"}).Bold(true)
	styleIncome    = lipgloss.NewStyle().Foreground(colorGreen)
	styleExpense   = lipgloss.NewStyle().Foreground(colorRed)
	styleCategory  = lipgloss.NewStyle().Foreground(colorAmber)
	styleSummaryH  = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
)

// ── Messages ──────────────────────────────────────────────────────────────────

type txLoadedMsg struct {
	txs    []models.Transaction
	months []string
	sum    *models.Summary
}
type errMsg struct{ err error }

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
	searchQ    string
	searching  bool
	searchInput textinput.Model
	filterCat  string
	vp         viewport.Model

	status     string
	statusTime time.Time
	err        error
}

func New() Model {
	si := textinput.New()
	si.Placeholder = "search transactions…"
	si.CharLimit = 100
	return Model{searchInput: si, activeTab: 0}
}

func Run() error {
	m := New()
	p := tea.NewProgram(m, tea.WithAltScreen())
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
		if len(msg.months) > 0 {
			m.months = msg.months
		}
		if m.cursor >= len(m.txs) {
			m.cursor = max(0, len(m.txs)-1)
		}
		if m.view == viewSummary && m.summary != nil {
			m.vp.SetContent(renderSummary(m.summary, m.width))
		}

	case errMsg:
		m.err = msg.err

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
	case "g":
		m.cursor = 0
	case "G":
		m.cursor = max(0, len(m.txs)-1)
	case "S", "s":
		m.view = viewSummary
		m.vp.SetContent(renderSummary(m.summary, m.width))
		m.vp.GotoTop()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
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

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.view {
	case viewSummary:
		return m.renderSummaryView()
	default:
		return m.renderList()
	}
}

func (m Model) renderList() string {
	var b strings.Builder

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
		b.WriteString(styleHeader.Render("budgetctl") + "\n")
	}
	b.WriteString(styleDivider.Render(strings.Repeat("─", m.width)) + "\n")

	overhead := 3
	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
		overhead += 2
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
		overhead++
	}

	listH := m.height - overhead
	if listH < 1 {
		listH = 1
	}

	if len(m.txs) == 0 {
		b.WriteString("\n" + styleHelp.Render("  No transactions — import a CSV with: budgetctl import file.csv") + "\n")
	} else {
		start := 0
		if m.cursor >= listH {
			start = m.cursor - listH + 1
		}
		end := min(len(m.txs), start+listH)
		for i := start; i < end; i++ {
			t := &m.txs[i]
			line := formatTxRow(t, m.width)
			if i == m.cursor {
				line = styleSelected.Width(m.width).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	// ── status bar ──
	netStr := ""
	if m.summary != nil {
		color := styleIncome
		if m.summary.Net < 0 {
			color = styleExpense
		}
		netStr = styleMuted.Render(" net:") + color.Render(fmt.Sprintf(" %+.0f€", m.summary.Net))
	}
	countStr := ""
	if len(m.txs) > 0 {
		countStr = styleMuted.Render(fmt.Sprintf(" %d tx", len(m.txs)))
	}

	var bar string
	if m.err != nil {
		bar = styleErr.Render("✗ " + m.err.Error())
	} else if m.status != "" {
		bar = styleOK.Render("✓ " + m.status)
	} else {
		bar = styleHelp.Render("s:summary  /:search  tab:month  j/k:nav  q:quit")
	}
	right := netStr + countStr
	pad := m.width - lipgloss.Width(bar) - lipgloss.Width(right)
	if pad < 0 {
		pad = 0
	}
	b.WriteString("\n" + bar + strings.Repeat(" ", pad) + right)
	return b.String()
}

func (m Model) renderSummaryView() string {
	var b strings.Builder

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

	m.vp.Height = m.height - 5
	b.WriteString(m.vp.View())

	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	b.WriteString("\n" + styleHelp.Render("esc:back  tab:month  ↑↓:scroll  q:quit") + styleMuted.Render(pct))
	return b.String()
}

func renderSummary(sum *models.Summary, width int) string {
	if sum == nil {
		return "No data for this month."
	}
	var b strings.Builder

	b.WriteString(styleSummaryH.Render(fmt.Sprintf("Summary: %s", sum.Month)) + "\n\n")

	incomeColor := styleIncome
	expColor := styleExpense
	netColor := styleOK
	if sum.Net < 0 {
		netColor = styleExpense
	}

	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Income:", incomeColor.Render(fmt.Sprintf("%+.2f €", sum.Income))))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Expenses:", expColor.Render(fmt.Sprintf("%+.2f €", sum.Expenses))))
	b.WriteString(fmt.Sprintf("  %-12s %s\n", "Net:", netColor.Render(fmt.Sprintf("%+.2f €", sum.Net))))
	b.WriteString("\n" + styleSummaryH.Render("By category:") + "\n\n")

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

		return txLoadedMsg{txs: txs, months: months, sum: sum}
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
		t.Date.Format("2006-01-02"),
		amtStyled,
		catStyled,
		desc,
	)
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
