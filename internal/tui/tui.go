package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/aeon022/missionctl-core/overlay"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList    view = iota
	viewSummary view = iota
	viewHelp    view = iota
	viewForm    view = iota
	viewImport  view = iota
	viewDetail  view = iota
)

// ── Import assistant steps ──────────────────────────────────────────────────

type importStep int

const (
	importPickFile importStep = iota
	importPreview
	importRunning
	importDone
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
	// Shared across the suite via missionctl-core/theme — keeping the local
	// names so every existing style reference below stays unchanged.
	colorBlue   = theme.Blue
	colorGreen  = theme.Green
	colorRed    = theme.Red
	colorMuted  = theme.Muted
	colorSubtle = theme.Subtle
	colorAmber  = theme.Amber

	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(colorBlue).
			Padding(0, 2)
	styleTabInact      = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
	styleAcctTabActive = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(colorGreen).
				Padding(0, 2)
	styleAcctTabInact = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
	styleDivider      = lipgloss.NewStyle().Foreground(colorSubtle)
	styleHeader       = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleHelp         = lipgloss.NewStyle().Foreground(colorMuted)
	styleErr          = lipgloss.NewStyle().Foreground(colorRed)
	styleOK           = lipgloss.NewStyle().Foreground(colorGreen)
	styleMuted        = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected     = lipgloss.NewStyle().
				Background(theme.SelectedBg).
				Foreground(theme.SelectedFg).
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
	txs      []models.Transaction
	months   []string
	accounts []string
	sum      *models.Summary
	goals    []models.GoalStatus
	trend    []models.MonthlyPoint
}
type errMsg struct{ err error }
type txSavedMsg struct{ err error }
type txDeletedMsg struct{ err error }
type importParsedMsg struct {
	txs []models.Transaction
	err error
}
type importDoneMsg struct {
	res budget.ImportResult
	err error
}

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	view   view
	width  int
	height int

	txs           []models.Transaction
	cursor        int
	months        []string // ["2026-06", "2026-05", ...]
	activeTab     int      // index into months; -1 = all
	accounts      []string // ["N26", "ING", ...]
	activeAccount int      // index into accounts; -1 = all
	summary       *models.Summary
	goals         []models.GoalStatus
	trend         []models.MonthlyPoint
	searchQ       string
	searching     bool
	searchInput   textinput.Model
	filterCat     string
	vp            viewport.Model

	// add/edit form
	form    [fCount]textinput.Model
	formIdx int
	editTx  *models.Transaction // nil = new entry

	// quick categorize + delete confirm
	categorizing bool
	catInput     textinput.Model
	deleteTarget *models.Transaction

	// "enter" transaction detail popup
	detailTx *models.Transaction

	// "?" transient help popup
	helpVP   viewport.Model
	helpPopW int
	helpPopH int

	// CSV import assistant
	importStep        importStep
	fp                filepicker.Model
	importPath        string
	importParsed      []models.Transaction // parsed preview, before any DB write
	importErr         error
	importUseAI       bool
	importResult      budget.ImportResult
	importAcctInput   textinput.Model
	importEditingAcct bool

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
	return Model{searchInput: si, catInput: ci, activeTab: 0, activeAccount: -1}
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
	return tea.Batch(loadCmd("", "", ""), tea.WindowSize())
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

// cycleAccount steps an activeAccount index by dir (+1/-1) across the range
// [-1, n-1], where -1 means "all accounts combined".
func cycleAccount(active, n, dir int) int {
	idx := active + 1 // shift to [0, n]
	idx = (idx + dir + (n + 1)) % (n + 1)
	return idx - 1
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
		m.trend = msg.trend
		if len(msg.months) > 0 {
			m.months = msg.months
		}
		m.accounts = msg.accounts
		if m.activeAccount >= len(m.accounts) {
			m.activeAccount = -1
		}
		if m.cursor >= len(m.txs) {
			m.cursor = max(0, len(m.txs)-1)
		}
		if m.view == viewSummary && m.summary != nil {
			m.vp.SetContent(renderSummary(m.summary, m.goals, m.trend, m.width))
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
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
		}

	case txDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus("deleted")
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
		}

	case importParsedMsg:
		if msg.err != nil {
			m.importErr = msg.err
			return m, nil
		}
		m.importErr = nil
		m.importParsed = msg.txs
		m.importStep = importPreview
		detected := ""
		if len(msg.txs) > 0 {
			detected = msg.txs[0].Account
		}
		m.importAcctInput.SetValue(detected)
		return m, nil

	case importDoneMsg:
		m.importResult = msg.res
		m.importErr = msg.err
		m.importStep = importDone
		return m, nil

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
		case tea.MouseButtonLeft:
			if msg.Action != tea.MouseActionPress || m.view != viewList {
				return m, nil
			}
			if i := m.tabHitTest(msg.X, msg.Y); i >= 0 {
				if i != m.activeTab {
					m.activeTab = i
					m.cursor = 0
					return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
				}
				return m, nil
			}
			if i := m.accountTabHitTest(msg.X, msg.Y); i >= -1 {
				if i != m.activeAccount {
					m.activeAccount = i
					m.cursor = 0
					return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
				}
				return m, nil
			}
			if i := m.rowHitTest(msg.Y); i >= 0 {
				m.cursor = i
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
		case viewImport:
			return m.updateImport(msg)
		case viewHelp:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "q", "esc", "?":
				m.view = viewList
				return m, nil
			}
			var cmd tea.Cmd
			m.helpVP, cmd = m.helpVP.Update(msg)
			return m, cmd
		case viewDetail:
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "e":
				if m.detailTx != nil {
					t := *m.detailTx
					m.view = viewForm
					m.editTx = &t
					m.form = newForm(&t)
					m.formIdx = 0
					m.detailTx = nil
					return m, m.form[fDate].Focus()
				}
			default:
				m.view = viewList
				m.detailTx = nil
			}
			return m, nil
		}
	}

	if m.view == viewSummary {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	if m.view == viewImport && m.importStep == importPickFile {
		// Non-key messages (directory-read results, etc.) the filepicker
		// needs to function — key messages are handled in updateImport.
		var cmd tea.Cmd
		m.fp, cmd = m.fp.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openImport opens the CSV import assistant, rooted at ~/Downloads (falling
// back to the home directory) since that's where bank exports usually land.
func (m Model) openImport() Model {
	fp := filepicker.New()
	fp.AllowedTypes = []string{".csv"}
	if home, err := os.UserHomeDir(); err == nil {
		fp.CurrentDirectory = home
		if downloads := filepath.Join(home, "Downloads"); isDir(downloads) {
			fp.CurrentDirectory = downloads
		}
	}
	// Budget: 2(title+blank) + 2(desc, wraps to 2 lines at the popup's max
	// width) + 1(blank after desc) + 1(blank after the file list) +
	// 1(footer) + 2(border) + 2(padding) = 11 lines of "chrome" around the
	// file list, plus bubbles/filepicker's own View() always emits
	// Height+1 lines (it pads through i<=Height inclusive) — so the file
	// list's budget needs to be one shorter again.
	h := m.height - 12
	if h < 5 {
		h = 5
	}
	fp.SetHeight(h)

	ai := textinput.New()
	ai.Placeholder = "account (e.g. N26)…"
	ai.CharLimit = 60

	m.fp = fp
	m.importStep = importPickFile
	m.importPath = ""
	m.importParsed = nil
	m.importErr = nil
	m.importUseAI = os.Getenv("ANTHROPIC_API_KEY") != ""
	m.importAcctInput = ai
	m.importEditingAcct = false
	m.view = viewImport
	return m
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (m Model) updateImport(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.importStep {
	case importPickFile:
		if msg.String() == "esc" {
			m.view = viewList
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.fp, cmd = m.fp.Update(msg)
		if didSelect, path := m.fp.DidSelectFile(msg); didSelect {
			m.importPath = path
			m.importErr = nil
			return m, tea.Batch(cmd, parseImportCmd(path))
		}
		return m, cmd

	case importPreview:
		if m.importEditingAcct {
			switch msg.String() {
			case "enter", "esc":
				m.importEditingAcct = false
				m.importAcctInput.Blur()
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.importAcctInput, cmd = m.importAcctInput.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "esc":
			m.importStep = importPickFile
			m.importErr = nil
			return m, nil
		case "a":
			m.importUseAI = !m.importUseAI
			return m, nil
		case "t":
			m.importEditingAcct = true
			m.importAcctInput.CursorEnd()
			return m, m.importAcctInput.Focus()
		case "enter", "y":
			if len(m.importParsed) == 0 {
				return m, nil
			}
			m.importStep = importRunning
			return m, runImportCmd(m.importPath, strings.TrimSpace(m.importAcctInput.Value()), m.importUseAI)
		case "ctrl+c":
			return m, tea.Quit
		}
		return m, nil

	case importRunning:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil

	case importDone:
		m.view = viewList
		m.importStep = importPickFile
		return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
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
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
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
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
		}
	case "]":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), 1)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
		}
	case "[":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), -1)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.searchQ, m.activeAccountName())
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
		m.vp.SetContent(renderSummary(m.summary, m.goals, m.trend, m.width))
		m.vp.GotoTop()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
	case "?":
		m = m.openHelp()
	case "n":
		m.view = viewForm
		m.editTx = nil
		m.form = newForm(nil)
		m.formIdx = 0
		return m, m.form[fDate].Focus()
	case "i":
		m = m.openImport()
		return m, m.fp.Init()
	case "enter":
		if len(m.txs) > 0 {
			t := m.txs[m.cursor]
			m.detailTx = &t
			m.view = viewDetail
		}
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
			return m, loadCmd(m.activeMonth(), "", m.activeAccountName())
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
			return m, loadCmd(m.activeMonth(), "", m.activeAccountName())
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			return m, loadCmd(m.activeMonth(), "", m.activeAccountName())
		}
	case "]":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), 1)
			return m, loadCmd(m.activeMonth(), "", m.activeAccountName())
		}
	case "[":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), -1)
			return m, loadCmd(m.activeMonth(), "", m.activeAccountName())
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
		// "?" is only reachable from the main list, so the list is always
		// the correct background to keep visible behind the popup. No
		// enclosing border on the list view, so inset 0 is safe.
		return overlay.Center(m.renderList(), m.renderHelpPopup(), m.width, m.height, 0)
	case viewForm:
		return m.renderForm()
	case viewImport:
		return overlay.Center(m.renderList(), m.renderImportPopup(), m.width, m.height, 0)
	case viewDetail:
		return overlay.Center(m.renderList(), m.renderDetailPopup(), m.width, m.height, 0)
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
	right := styleMuted.Render(time.Now().Format("Mon, 02 Jan 2006"))
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
	row := 4
	if len(m.accounts) > 0 {
		row++
	}
	if m.searching {
		row += 2
	}
	if m.searchQ != "" {
		row++
	}
	if m.categorizing {
		row++
	}
	return row
}

// tabHitTest returns the month index at column x on the tab row, or -1 if
// the click didn't land on a tab.
func (m Model) tabHitTest(x, y int) int {
	const tabRow = 2 // header title(0) + rule(1) + tabs(2)
	if y != tabRow || len(m.months) == 0 {
		return -1
	}
	col := 0
	for i, mo := range m.months {
		w := lipgloss.Width(styleTabInact.Render(mo))
		if i == m.activeTab {
			w = lipgloss.Width(styleTabActive.Render(mo))
		}
		if x >= col && x < col+w {
			return i
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
	const acctTabRow = 3 // header title(0) + rule(1) + month tabs(2) + account tabs(3)
	if y != acctTabRow || len(m.accounts) == 0 {
		return -2
	}
	col := 0
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
	listH := m.height - m.listStartRow() - 2 // divider + footer bar
	if listH < 1 {
		listH = 1
	}
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

	// ── account tab bar (only worth showing once there's more than one) ──
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

	b.WriteString(styleDivider.Render(strings.Repeat("─", w)) + "\n")

	if m.searching {
		b.WriteString("  " + m.searchInput.View() + "\n\n")
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
	}
	if m.categorizing {
		b.WriteString("  " + styleCategory.Render("category: ") + m.catInput.View() + "\n")
	}

	listH := m.height - m.listStartRow() - 2 // divider + trailing status bar
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
		bar = styleHelp.Render("enter:details  n:new  i:import  e:edit  d:delete  c:categorize  s:summary  /:search  tab:month  ]:account  ?:help  q:quit")
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

func (m Model) helpContent() string {
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
	b.WriteString(row("enter", "view full details (untruncated description, source, raw row)"))
	b.WriteString(row("n", "new entry (manual income/expense)"))
	b.WriteString(row("i", "import CSV (N26, ING, DKB, generic) — t at preview: tag account"))
	b.WriteString(row("e", "edit selected entry"))
	b.WriteString(row("d", "delete entry (asks to confirm)"))
	b.WriteString(row("c", "set category for selected entry"))
	b.WriteString(section("Data"))
	b.WriteString(row("/", "search transactions (esc clears)"))
	b.WriteString(row("s", "summary — categories, charts, budget goals"))
	b.WriteString(section("Accounts"))
	b.WriteString("  " + styleHelp.Render("No separate \"create account\" step — an account is just a text tag") + "\n")
	b.WriteString("  " + styleHelp.Render("on transactions. It appears the first time you tag something with it:") + "\n")
	b.WriteString("  " + styleHelp.Render("  · CLI:  budgetctl import file.csv --account \"Sparkasse\"") + "\n")
	b.WriteString("  " + styleHelp.Render("  · TUI:  i → pick file → t (at preview) → type a name → enter") + "\n")
	b.WriteString("  " + styleHelp.Render("Redo a bad import: budgetctl reset --account \"Sparkasse\" (asks to confirm)") + "\n")
	b.WriteString(row("[ / ]", "cycle accounts (tab/click also works)"))
	b.WriteString(section("Other"))
	b.WriteString(row("?", "toggle this help"))
	b.WriteString(row("q", "quit"))
	b.WriteString("\n" + styleHelp.Render("  Import & categorize on the CLI: budgetctl import file.csv · budgetctl tag PATTERN --category NAME") + "\n")
	return b.String()
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

	vp := viewport.New(popW-4, popH-3) // border 1+1, padding(0,1) → 2 cols; -1 row for footer
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
	if m.helpVP.TotalLineCount() > m.helpVP.Height {
		footer = fmt.Sprintf("j/k scroll (%d%%)  ·  %s", int(m.helpVP.ScrollPercent()*100), footer)
	}
	body := m.helpVP.View() + "\n" + styleHelp.Render(footer)
	return stylePopupBorder.Width(m.helpPopW).Render(body)
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
	m.vp.Height = vpH
	b.WriteString(m.vp.View())

	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
		pct = fmt.Sprintf(" %d%%", int(m.vp.ScrollPercent()*100))
	}
	b.WriteString("\n  " + styleHelp.Render("esc:back  tab:month  ]:account  ↑↓:scroll  q:quit") + styleMuted.Render(pct))
	return b.String()
}

func renderSummary(sum *models.Summary, goals []models.GoalStatus, trend []models.MonthlyPoint, width int) string {
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
		b.WriteString("  " + sparkline(nets) + "  " + styleMuted.Render(fmt.Sprintf("(%s → %s)", labels[0], labels[len(labels)-1])) + "\n")
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

// parseImportCmd parses path for the preview step — no DB write yet.
func parseImportCmd(path string) tea.Cmd {
	return func() tea.Msg {
		txs, err := budget.Import(path)
		return importParsedMsg{txs: txs, err: err}
	}
}

// runImportCmd performs the actual import (upsert + optional AI
// categorization) after the user confirms the preview. account overrides
// every parsed transaction's account field when non-empty (see the "t"
// binding in the preview step); an empty account leaves each row's
// bank-detected account (or "" for generic CSVs) untouched.
func runImportCmd(path, account string, useAI bool) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return importDoneMsg{err: err}
		}
		defer s.Close()
		res, err := budget.ImportFile(context.Background(), s, path, account, useAI)
		return importDoneMsg{res: res, err: err}
	}
}

func loadCmd(month, query, account string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()

		txs, err := s.List(ctx, store.Filter{Month: month, Query: query, Account: account, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		months, _ := s.ListMonths(ctx)
		accounts, _ := s.ListAccounts(ctx)

		// summary for active month (and account, if one is selected)
		sum, _ := s.Summary(ctx, month, account)

		// goals with current-month spend (always across all accounts — a
		// budget goal like "dining < 200€" isn't naturally per-account)
		goals, _ := s.GoalStatuses(ctx, month)

		trend, _ := s.MonthlyTrend(ctx, account, 6)

		return txLoadedMsg{txs: txs, months: months, accounts: accounts, sum: sum, goals: goals, trend: trend}
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

// sparklineChars are the 8 block-height levels used by sparkline, low to high.
var sparklineChars = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a compact one-line bar chart, one character
// per value, height-scaled to the min/max of the series and colored green
// (positive) or red (negative). Each character is rendered with its own
// merged style rather than wrapping the whole line in one Render() call —
// nesting styled Render() output inside another Render() call silently
// resets everything after the inner segment (every Render() call ends with
// a full SGR reset), a bug found and fixed the hard way in habctl earlier.
func sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	rangeV := maxV - minV

	var b strings.Builder
	for _, v := range values {
		idx := len(sparklineChars) / 2
		if rangeV > 0 {
			idx = int((v - minV) / rangeV * float64(len(sparklineChars)-1))
		}
		style := styleIncome
		if v < 0 {
			style = styleExpense
		}
		b.WriteString(style.Render(string(sparklineChars[idx])))
	}
	return b.String()
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
