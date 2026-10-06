package tui

import (
	"fmt"
	"image/color"
	"os"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/missionctl-core/theme"
)

// ── Styles ────────────────────────────────────────────────────────────────────

// adaptive resolves a light/dark ANSI color pair once at startup — v2 dropped
// AdaptiveColor and these package-level styles are built once, not per render.
var adaptive = func() func(light, dark string) color.Color {
	pick := lipgloss.LightDark(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	return func(light, dark string) color.Color { return pick(lipgloss.Color(light), lipgloss.Color(dark)) }
}()

var (
	// Shared across the suite via missionctl-core/theme — keeping the local
	// names so every existing style reference below stays unchanged.
	colorBlue   = theme.BlueV2
	colorGreen  = theme.GreenV2
	colorRed    = theme.RedV2
	colorMuted  = theme.MutedV2
	colorSubtle = theme.SubtleV2
	colorAmber  = theme.AmberV2

	// One tab look for months AND accounts: the same filled pill as ui.Pill(…, ui.Info)
	// (it used to be a blue pill for months and a green one for accounts).
	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(theme.OnAccentV2).
			Background(colorBlue).
			Padding(0, 1)
	styleTabInact      = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)
	styleAcctTabActive = styleTabActive
	styleAcctTabInact  = styleTabInact
	styleDivider       = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHeader        = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleHelp          = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr           = lipgloss.NewStyle().Foreground(colorRed)
	styleOK            = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted         = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected      = lipgloss.NewStyle().
				Background(theme.SelectedBgV2).
				Foreground(theme.SelectedFgV2).
				Bold(true)
	styleIncome   = lipgloss.NewStyle().Foreground(colorGreen)
	styleExpense  = lipgloss.NewStyle().Foreground(colorRed)
	styleCategory = lipgloss.NewStyle().Foreground(colorAmber)
	// list rows: payee in the normal text color, category muted (amber only flags "uncategorized")
	stylePayee       = lipgloss.NewStyle()
	styleCategoryRow = lipgloss.NewStyle().Foreground(colorMuted)
	styleSummaryH    = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleToday       = lipgloss.NewStyle().Foreground(adaptive("214", "220")).Bold(true)
	styleDateWeek    = lipgloss.NewStyle().Foreground(colorMuted)
	styleDateMonth   = lipgloss.NewStyle().Foreground(adaptive("247", "242"))
	styleDateOld     = lipgloss.NewStyle().Foreground(colorSubtle)
)

// ── command palette (":") ────────────────────────────────────────────────────
//
// Types out full words instead of memorizing single-key shortcuts. Reuses
// the exact same key handling every shortcut already goes through
// (updateList) by replaying the mapped keypress, so behavior is guaranteed
// identical to typing the key directly. Matching logic lives in
// missionctl-core/palette (shared across the suite); this list is
// budgetctl-specific.
var paletteCommands = []palette.Command{
	{Name: "new", Desc: "New transaction (manual income/expense)", Key: "n"},
	{Name: "edit", Desc: "Edit selected entry", Key: "e"},
	{Name: "delete", Desc: "Delete entry (asks to confirm)", Key: "d"},
	{Name: "detail", Desc: "View full details", Key: "enter"},
	{Name: "import", Desc: "Import CSV (N26, ING, DKB, generic)", Key: "i"},
	{Name: "category", Desc: "Set category for selected entry", Key: "c"},
	{Name: "ai-categorize", Desc: "AI-categorize all uncategorized entries", Key: "a"},
	{Name: "undo", Desc: "Undo last delete", Key: "u"},
	{Name: "select", Desc: "Select mode (batch categorize)", Key: "v"},
	{Name: "search", Desc: "Search transactions", Key: "/"},
	{Name: "filter", Desc: "Filter by category", Key: "f"},
	{Name: "summary", Desc: "Summary — categories, charts, budget goals", Key: "s"},
	{Name: "settings", Desc: "Settings — sync across devices", Key: "o"},
	{Name: "profiles", Desc: "Switch or manage isolated data profiles", Key: "p"},
	{Name: "help", Desc: "Show help", Key: "?"},
	{Name: "quit", Desc: "Quit budgetctl", Key: "q"},
}

func New() Model {
	si := textinput.New()
	si.Placeholder = "search transactions…"
	si.CharLimit = 100
	si.SetWidth(40) // v2: width 0 clips the placeholder to 1 char
	pi := textinput.New()
	pi.Placeholder = "command…"
	pi.CharLimit = 40
	pi.SetWidth(40) // v2: width 0 clips the placeholder to 1 char
	ci := textinput.New()
	ci.Placeholder = "category… (or Cat1;Cat2 to split evenly)"
	ci.CharLimit = 60
	ci.SetWidth(60) // v2: width 0 clips the placeholder to 1 char
	gi := textinput.New()
	gi.Placeholder = "category amount, e.g. Dining 200"
	gi.CharLimit = 60
	gi.SetWidth(40) // v2: width 0 clips the placeholder to 1 char
	ri := textinput.New()
	ri.Placeholder = "pattern, e.g. RCIAT — enter to save as a rule, esc to skip"
	ri.CharLimit = 100
	ri.SetWidth(60) // v2: width 0 clips the placeholder to 1 char
	return Model{searchInput: si, paletteInput: pi, catInput: ci, goalInput: gi, ruleInput: ri, activeTab: 0, activeAccount: -1, hoverRow: -1, lastClickRow: -1}
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
		in.SetWidth(60) // v2: width 0 clips the placeholder to 1 char
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

// motionThrottleFilter drops MouseMotionMsg messages arriving <16ms apart —
// all-motion mouse mode re-renders on every pixel and can overwhelm the terminal.
func motionThrottleFilter() func(tea.Model, tea.Msg) tea.Msg {
	var lastMotion time.Time
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.MouseMotionMsg); !ok {
			return msg
		}
		now := time.Now()
		if now.Sub(lastMotion) < 16*time.Millisecond {
			return nil
		}
		lastMotion = now
		return msg
	}
}

func Run() error {
	m := New()
	p := tea.NewProgram(m, tea.WithFilter(motionThrottleFilter()), tea.WithFPS(30))
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(loadCmd("", "", ""), tea.RequestWindowSize)
}

func (m Model) activeMonth() string {
	if m.activeTab < 0 || m.activeTab >= len(m.months) {
		return ""
	}
	return m.months[m.activeTab]
}

// activeAccountName returns the currently selected account filter, or ""
// for "all accounts combined" (activeAccount == -1, the default).
func (m Model) activeAccountName() string {
	if m.activeAccount < 0 || m.activeAccount >= len(m.accounts) {
		return ""
	}
	return m.accounts[m.activeAccount]
}

// adjacentYearTab returns the index of the nearest month in months whose
// calendar year differs from the month at activeTab, scanning toward newer
// months (dir > 0) or older months (dir < 0) — months is assumed sorted
// newest-first, as ListMonths returns it. Returns (-1, false) if there's no
// year boundary left to cross in that direction (already at the oldest/
// newest year present, or months is empty).
//
// Landing point matches the direction crossed: scanning toward newer months
// (dir > 0) stops on the FIRST month of the next year (the earliest month
// you have data for that year); scanning toward older months (dir < 0)
// stops on the LAST month of the previous year (the most recent one) —
// both are simply "the first month encountered whose year differs",
// which naturally falls out of scanning in the respective direction over
// a newest-first-sorted slice.
func adjacentYearTab(months []string, activeTab, dir int) (int, bool) {
	if len(months) == 0 {
		return -1, false
	}
	curYear := ""
	if activeTab >= 0 && activeTab < len(months) {
		curYear = months[activeTab][:4]
	}
	if dir > 0 {
		for i := activeTab - 1; i >= 0; i-- {
			if months[i][:4] != curYear {
				return i, true
			}
		}
	} else {
		for i := activeTab + 1; i < len(months); i++ {
			if months[i][:4] != curYear {
				return i, true
			}
		}
	}
	return -1, false
}

// cycleAccount steps an activeAccount index by dir (+1/-1) across the range
// [-1, n-1], where -1 means "all accounts combined".
func cycleAccount(active, n, dir int) int {
	idx := active + 1 // shift to [0, n]
	idx = (idx + dir + (n + 1)) % (n + 1)
	return idx - 1
}
