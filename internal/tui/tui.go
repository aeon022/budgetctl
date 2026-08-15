package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/aeon022/missionctl-core/keymap"
	"github.com/aeon022/missionctl-core/overlay"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/aeon022/missionctl-core/theme"
	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// ── Views ─────────────────────────────────────────────────────────────────────

type view int

const (
	viewList              view = iota
	viewSummary           view = iota
	viewHelp              view = iota
	viewForm              view = iota
	viewImport            view = iota
	viewDetail            view = iota
	viewCategoryPick      view = iota
	viewSettings          view = iota
	viewProfiles          view = iota
	viewCategoryTranslate view = iota
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
			Foreground(theme.OnAccent).
			Background(colorBlue).
			Padding(0, 2)
	styleTabInact      = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 2)
	styleAcctTabActive = lipgloss.NewStyle().Bold(true).
				Foreground(theme.OnAccent).
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
	stylePayee     = lipgloss.NewStyle().Foreground(colorBlue)
	styleSummaryH  = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleToday     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "214", Dark: "220"}).Bold(true)
	styleDateWeek  = lipgloss.NewStyle().Foreground(colorMuted)
	styleDateMonth = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "247", Dark: "242"})
	styleDateOld   = lipgloss.NewStyle().Foreground(colorSubtle)
)

// ── Messages ──────────────────────────────────────────────────────────────────

type txLoadedMsg struct {
	txs        []models.Transaction
	months     []string
	accounts   []string
	categories []string
	sum        *models.Summary
	goals      []models.GoalStatus
	trend      []models.MonthlyPoint
	recurring  []budget.RecurringPattern
}
type searchLoadedMsg struct{ txs []models.Transaction }
type errMsg struct{ err error }
type txSavedMsg struct{ err error }
type txDeletedMsg struct{ err error }
type goalSavedMsg struct{ err error }
type aiCategorizedMsg struct {
	count int
	err   error
}

// aiCategorizeProgressMsg reports one chunk done, with more still queued —
// see aiCategorizeStepCmd.
type aiCategorizeProgressMsg struct {
	remaining          []models.Transaction
	existingCategories []string
	done, total        int
}

// categoryRename is one AI-suggested old->new category name pair, shown for
// review in the "t" (translate) popup before any of it is applied.
type categoryRename struct{ Old, New string }

type categoryTranslatedMsg struct {
	suggestions []categoryRename
	err         error
}

type categoryRenamesAppliedMsg struct {
	count int
	err   error
}
type importParsedMsg struct {
	txs []models.Transaction
	err error
}
type importDoneMsg struct {
	res budget.ImportResult
	err error
}
type settingsAppliedMsg struct {
	status string
	err    error
}

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	view   view
	width  int
	height int

	txs           []models.Transaction // filtered (by searchQ) view of allTxs
	allTxs        []models.Transaction // everything loaded for the current month/account scope
	cursor        int
	hoverRow      int // m.txs index under the mouse cursor, -1 when none
	lastClickRow  int // m.txs index of the previous left-click, -1 when none — double-click opens the detail popup, same window/pattern taskctl uses
	lastClickAt   time.Time
	months        []string // ["2026-06", "2026-05", ...]
	activeTab     int      // index into months; -1 = all
	accounts      []string // ["N26", "ING", ...]
	activeAccount int      // index into accounts; -1 = all
	summary       *models.Summary
	goals         []models.GoalStatus
	trend         []models.MonthlyPoint
	recurring     []budget.RecurringPattern
	searchTxs     []models.Transaction // all months (current account/category scope) — populated on "/", searched instead of allTxs so search isn't stuck on the active month tab
	searchQ       string
	searching     bool
	searchInput   textinput.Model
	vp            viewport.Model

	// ":" command palette
	inPalette     bool
	paletteInput  textinput.Model
	paletteCursor int

	// category filter ("f" opens a popup picker, viewCategoryPick)
	categories         []string // distinct categories in use, alphabetical
	categoryFilter     string   // active filter; "" = all categories
	categoryPickInput  textinput.Model
	categoryPickCursor int

	// category translate ("t" in summary, viewCategoryTranslate) — AI-suggested
	// renames for categories that don't match the categorization language
	translateSuggestions []categoryRename
	translateSelected    map[int]bool // index into translateSuggestions
	translateCursor      int
	translateLoading     bool
	translateErr         error

	// add/edit form
	form    [fCount]textinput.Model
	formIdx int
	editTx  *models.Transaction // nil = new entry

	// quick categorize + delete confirm
	categorizing bool
	catInput     textinput.Model
	deleteTarget *models.Transaction

	// "save as rule?" follow-up after quick categorize
	savingRule    bool
	ruleInput     textinput.Model
	pendingCatIDs []string
	pendingCat    string

	// goal quick-set ("g" in summary view) — "<category> <amount>" in one line
	settingGoal bool
	goalInput   textinput.Model

	// batch select mode ("v") — bulk-categorize, same pattern taskctl's
	// own select mode uses. When categorizing is entered while selecting
	// is true, committing applies to every selected transaction instead
	// of just the cursor row.
	selecting bool
	selected  map[string]bool // keyed by transaction ID

	// undo: "u" within undoWindow of a delete restores the deleted row —
	// same pattern and window taskctl uses for its own delete-undo.
	// statusTime (set alongside status below) doubles as its expiry clock.
	lastDeleted *models.Transaction

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

	// Settings ("o") — browse for a folder to sync budgetctl's data to
	// (iCloud Drive, Dropbox, ...). Reuses fp (the CSV import filepicker,
	// mutually exclusive with it) in directory-only mode. Deliberately
	// does NOT rely on fp.DidSelectFile: with DirAllowed set, Enter both
	// selects AND descends into a directory, so browsing would end the
	// moment you tried to go deeper — a dedicated "s" key confirms
	// fp.CurrentDirectory instead, leaving Enter free to navigate.
	settingsPicking    bool
	settingsConfirming bool
	settingsPendingDir string // "" = reset to the local default, when settingsConfirming
	settingsOldPath    string // config.DBPath() before the change, for the move
	settingsErr        error

	// Profiles ("p") — switch between isolated data profiles (see
	// internal/config's Profiles/ActiveProfile/SetActiveProfile). Each
	// profile is its own database, so this screen is just a picker over
	// config.Profiles() plus "default"; it doesn't cache any profile data
	// itself.
	profilesCursor  int
	profileCreating bool
	profileNewInput textinput.Model
	profileRemoving string // profile name pending removal confirmation, "" = none
	profileErr      error

	status     string
	statusTime time.Time
	err        error
}

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
	pi := textinput.New()
	pi.Placeholder = "command…"
	pi.CharLimit = 40
	ci := textinput.New()
	ci.Placeholder = "category… (or Cat1;Cat2 to split evenly)"
	ci.CharLimit = 60
	gi := textinput.New()
	gi.Placeholder = "category amount, e.g. Dining 200"
	gi.CharLimit = 60
	ri := textinput.New()
	ri.Placeholder = "pattern, e.g. RCIAT — enter to save as a rule, esc to skip"
	ri.CharLimit = 100
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
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
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

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.vp = viewport.New(msg.Width, m.height-6)

	case txLoadedMsg:
		// Init() loads with an empty month filter (months aren't known yet
		// to scope it to "the current one") — that unfiltered first load
		// shows every transaction ever recorded, while the month tab bar
		// already highlights activeTab 0 as if it were scoped. The first
		// action that reloads data (categorize, goal, import, ...) always
		// passes activeMonth() instead, which now resolves to a real month
		// and narrows the list down — looking like transactions had
		// vanished, when really the initial screen was just never scoped
		// to begin with. Re-scope immediately once the month list is known
		// instead of waiting for the user to trigger that reload themselves.
		firstLoad := len(m.months) == 0 && len(msg.months) > 0

		m.allTxs = msg.txs
		m.txs = filterTxs(m.allTxs, m.searchQ)
		m.summary = msg.sum
		m.goals = msg.goals
		m.trend = msg.trend
		m.recurring = msg.recurring
		if len(msg.months) > 0 {
			m.months = msg.months
		}
		m.accounts = msg.accounts
		m.categories = msg.categories
		if m.activeAccount >= len(m.accounts) {
			m.activeAccount = -1
		}
		if m.cursor >= len(m.txs) {
			m.cursor = max(0, len(m.txs)-1)
		}
		if m.view == viewSummary && m.summary != nil {
			m.vp.SetContent(renderSummary(m.summary, m.goals, m.trend, m.recurring, m.width))
		}
		if firstLoad {
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}

	case searchLoadedMsg:
		m.searchTxs = msg.txs
		if m.searching || m.searchQ != "" {
			m.txs = filterTxs(m.searchTxs, m.searchQ)
			if m.cursor >= len(m.txs) {
				m.cursor = max(0, len(m.txs)-1)
			}
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
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}

	case goalSavedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.setStatus("goal saved")
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}

	case categoryTranslatedMsg:
		m.translateLoading = false
		m.translateErr = msg.err
		m.translateSuggestions = msg.suggestions
		m.translateSelected = make(map[int]bool, len(msg.suggestions))
		for i := range msg.suggestions {
			m.translateSelected[i] = true // opt-out, not opt-in — reviewing and deselecting a bad suggestion is one keystroke, same as accepting a good one
		}
		m.translateCursor = 0
		return m, nil

	case categoryRenamesAppliedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			label := "categories"
			if msg.count == 1 {
				label = "category"
			}
			m.setStatus(fmt.Sprintf("Renamed %d %s", msg.count, label))
		}
		m.view = viewSummary
		return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)

	case aiCategorizeProgressMsg:
		m.setStatus(fmt.Sprintf("Categorizing via AI… %d/%d", msg.done, msg.total))
		return m, aiCategorizeStepCmd(msg.remaining, msg.existingCategories, msg.done, msg.total)

	case aiCategorizedMsg:
		switch {
		case msg.count > 0 && msg.err != nil:
			// Partial success — some batches went through before a later
			// one failed. Keep what worked, still surface the failure.
			m.err = fmt.Errorf("AI-categorized %d before failing: %w", msg.count, msg.err)
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		case msg.err != nil:
			m.err = msg.err
		case msg.count == 0:
			m.setStatus("nothing to categorize")
		default:
			m.setStatus(fmt.Sprintf("AI-categorized %d transaction(s)", msg.count))
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}

	case txDeletedMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Don't clobber the "Deleted X — press u to undo" toast the
			// delete-confirm handler already set — this message arrives
			// right after it, and setStatus would both overwrite the text
			// and reset the (longer) undo-window clock.
			if m.lastDeleted == nil {
				m.setStatus("deleted")
			}
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
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

	case settingsAppliedMsg:
		m.settingsPendingDir = ""
		m.settingsOldPath = ""
		if msg.err != nil {
			m.settingsErr = msg.err
			return m, nil
		}
		m.settingsErr = nil
		m.status = msg.status
		m.statusTime = time.Now()
		return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)

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
					return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
				}
				return m, nil
			}
			if i := m.accountTabHitTest(msg.X, msg.Y); i >= -1 {
				if i != m.activeAccount {
					m.activeAccount = i
					m.cursor = 0
					return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
				}
				return m, nil
			}
			if i := m.rowHitTest(msg.Y); i >= 0 {
				now := time.Now()
				if i == m.lastClickRow && now.Sub(m.lastClickAt) < doubleClickWindow {
					m.cursor = i
					m.lastClickRow = -1 // consumed, so a third click starts fresh
					t := m.txs[i]
					m.detailTx = &t
					m.view = viewDetail
					return m, nil
				}
				m.cursor = i
				m.lastClickRow = i
				m.lastClickAt = now
			}
		case tea.MouseButtonNone:
			if msg.Action == tea.MouseActionMotion && m.view == viewList {
				m.hoverRow = m.rowHitTest(msg.Y)
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.err = nil
		// The delete-undo toast gets the longer undoWindow instead of the
		// usual 3s — it's also the window "u" checks below, so the message
		// and the capability it describes expire together.
		clearAfter := 3 * time.Second
		if m.lastDeleted != nil {
			clearAfter = undoWindow
		}
		if time.Since(m.statusTime) > clearAfter {
			m.status = ""
			m.lastDeleted = nil
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
		case viewCategoryPick:
			return m.updateCategoryPick(msg)
		case viewSettings:
			return m.updateSettings(msg)
		case viewProfiles:
			return m.updateProfiles(msg)
		case viewCategoryTranslate:
			return m.updateCategoryTranslate(msg)
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
	if m.view == viewSettings && m.settingsPicking {
		var cmd tea.Cmd
		m.fp, cmd = m.fp.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openImport opens the CSV import assistant, rooted at ~/Downloads (falling
// back to the home directory) since that's where bank exports usually land.
// openCategoryPick opens the "f" category-filter popup, pre-focused for
// typing straight away.
func (m Model) openCategoryPick() Model {
	ci := textinput.New()
	ci.Placeholder = "type to filter…"
	ci.CharLimit = 60
	m.categoryPickInput = ci
	m.categoryPickCursor = 0
	m.view = viewCategoryPick
	return m
}

// categoryPickItems returns the picker's list for the current query:
// "All categories" always first — a reset action, not a search target, so
// it's never fuzzy-filtered away — followed by categories matching query,
// ranked best-match-first (github.com/sahilm/fuzzy). Unlike filterTxs (the
// transaction list itself), re-ranking by match quality here is correct:
// this is a one-shot fzf-style picker, not a persistent chronologically-
// ordered list where re-sorting would be disorienting.
func categoryPickItems(categories []string, query string) []string {
	items := []string{"All categories"}
	if query == "" {
		return append(items, categories...)
	}
	for _, mt := range fuzzy.Find(query, categories) {
		items = append(items, categories[mt.Index])
	}
	return items
}

func (m Model) updateCategoryPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := categoryPickItems(m.categories, m.categoryPickInput.Value())
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.view = viewList
		return m, nil
	case "enter":
		m.view = viewList
		if m.categoryPickCursor < 0 || m.categoryPickCursor >= len(items) {
			return m, nil
		}
		selected := items[m.categoryPickCursor]
		if selected == "All categories" {
			m.categoryFilter = ""
		} else {
			m.categoryFilter = selected
		}
		m.cursor = 0
		return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
	case "up", "ctrl+p":
		if m.categoryPickCursor > 0 {
			m.categoryPickCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.categoryPickCursor < len(items)-1 {
			m.categoryPickCursor++
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.categoryPickInput, cmd = m.categoryPickInput.Update(msg)
		// Clamp the cursor to the newly (possibly shorter) filtered list —
		// typing a character that narrows the results out from under the
		// current cursor position must not leave it pointing past the end.
		newItems := categoryPickItems(m.categories, m.categoryPickInput.Value())
		if m.categoryPickCursor >= len(newItems) {
			m.categoryPickCursor = max(0, len(newItems)-1)
		}
		return m, cmd
	}
}

// renderCategoryPickPopup renders the "f" category-filter picker: a text
// input for fuzzy search, a scrollable-by-typing list of matching
// categories, and the fixed "All categories" reset option.
func (m Model) renderCategoryPickPopup() string {
	w := m.importPopupWidth() // reuse the import popup's fixed width budget
	contentW := w - 6         // border(2) + padding(4)

	var b strings.Builder
	b.WriteString(styleHeader.Render("Filter by Category") + "\n\n")
	b.WriteString("  " + m.categoryPickInput.View() + "\n\n")

	query := m.categoryPickInput.Value()
	items := categoryPickItems(m.categories, query)
	const maxRows = 12 // cap so the popup doesn't grow unbounded with many categories
	for i, item := range items {
		if i >= maxRows {
			b.WriteString(styleMuted.Render(fmt.Sprintf("  … and %d more (keep typing to narrow)", len(items)-maxRows)) + "\n")
			break
		}
		itemW := contentW - 2
		if i == m.categoryPickCursor {
			// Selected row: plain padded text in one Render() call, no
			// nested highlight — same reasoning as the cursor row in the
			// main transaction list (nesting per-character ANSI inside
			// this wrap would clobber it).
			b.WriteString("> " + styleSelected.Render(padRunes(truncRunes(item, itemW), itemW)) + "\n")
			continue
		}
		label := item
		if item != "All categories" {
			label = highlightMatches(truncRunes(item, itemW), fuzzyMatchIndexes(query, item), lipgloss.NewStyle())
		}
		b.WriteString("  " + label + "\n")
	}
	if len(items) == 1 {
		b.WriteString("\n" + styleMuted.Render("  (no categorized transactions yet)") + "\n")
	}
	b.WriteString("\n" + styleMuted.Render("↑/↓ navigate  ·  enter: apply  ·  esc: cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

// updateCategoryTranslate handles the "t" (summary view) AI-suggested
// category-rename popup: navigate + toggle which suggestions to keep,
// enter applies the selected ones, esc cancels without changing anything.
func (m Model) updateCategoryTranslate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.view = viewSummary
		return m, nil
	case "up", "k":
		if m.translateCursor > 0 {
			m.translateCursor--
		}
	case "down", "j":
		if m.translateCursor < len(m.translateSuggestions)-1 {
			m.translateCursor++
		}
	case " ":
		if m.translateCursor < len(m.translateSuggestions) {
			m.translateSelected[m.translateCursor] = !m.translateSelected[m.translateCursor]
		}
	case "A":
		for i := range m.translateSuggestions {
			m.translateSelected[i] = true
		}
	case "enter":
		var chosen []categoryRename
		for i, r := range m.translateSuggestions {
			if m.translateSelected[i] {
				chosen = append(chosen, r)
			}
		}
		if len(chosen) == 0 {
			m.view = viewSummary
			return m, nil
		}
		return m, applyCategoryRenamesCmd(chosen)
	}
	return m, nil
}

// renderCategoryTranslatePopup renders the "t" popup: a loading state while
// the AI call is in flight, an error, or the reviewable list of suggested
// renames — all selected by default (see categoryTranslatedMsg handling),
// space to deselect one, "A" to reselect all, enter to apply what's checked.
func (m Model) renderCategoryTranslatePopup() string {
	w := m.importPopupWidth()
	contentW := w - 6

	var b strings.Builder
	b.WriteString(styleHeader.Render("Translate Categories (AI)") + "\n\n")

	switch {
	case m.translateLoading:
		b.WriteString(styleMuted.Render("Asking AI which categories to rename…"))
	case m.translateErr != nil:
		b.WriteString(styleErr.Render("✗ " + m.translateErr.Error()))
	case len(m.translateSuggestions) == 0:
		b.WriteString(styleMuted.Render("Nothing to rename — every category already fits."))
	default:
		const maxRows = 12
		for i, r := range m.translateSuggestions {
			if i >= maxRows {
				b.WriteString(styleMuted.Render(fmt.Sprintf("  … and %d more", len(m.translateSuggestions)-maxRows)) + "\n")
				break
			}
			checkbox := "[ ]"
			if m.translateSelected[i] {
				checkbox = "[x]"
			}
			row := fmt.Sprintf("%s %s -> %s", checkbox, r.Old, r.New)
			if i == m.translateCursor {
				b.WriteString(styleSelected.Render(padRunes(truncRunes(row, contentW-2), contentW-2)) + "\n")
			} else {
				b.WriteString(styleHelp.Render(row) + "\n")
			}
		}
		b.WriteString("\n" + styleMuted.Render("↑/↓ navigate  ·  space toggle  ·  A select all  ·  enter apply  ·  esc cancel"))
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

// renderSettingsPopup renders the "o" settings screen: current data
// directory + sync mode, a pending-move confirmation, or the directory
// browser, depending on which sub-state is active.
func (m Model) renderSettingsPopup() string {
	w := m.importPopupWidth()
	contentW := w - 6

	if m.settingsPicking {
		return m.renderSettingsBrowsePopup(w, contentW)
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render("Settings") + "\n\n")

	mode := "local (this machine only)"
	if config.Shared() {
		mode = "shared (folder-synced)"
	}
	b.WriteString(styleHelp.Render("Data directory:") + "\n")
	b.WriteString("  " + ansi.Truncate(filepath.Dir(config.DBPath()), contentW-2, "…") + "\n")
	b.WriteString("  " + styleMuted.Render(mode) + "\n\n")

	if m.settingsConfirming {
		msg := fmt.Sprintf("Point budgetctl at %s?", m.settingsPendingDir)
		if m.settingsPendingDir == "" {
			msg = "Switch back to the local (non-synced) database?"
		}
		b.WriteString(styleErr.Render(msg) + "\n\n")
		b.WriteString(styleMuted.Render("y: confirm  ·  any other key: cancel"))
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(1, 2).
			Width(w).
			Render(b.String())
	}

	if m.settingsErr != nil {
		b.WriteString(styleErr.Render("✗ "+m.settingsErr.Error()) + "\n\n")
	} else if m.status != "" {
		b.WriteString(styleOK.Render(m.status) + "\n\n")
	}

	b.WriteString(styleHelp.Render("b") + "  browse for a folder to sync (iCloud Drive, Dropbox, …)\n")
	if config.Shared() {
		b.WriteString(styleHelp.Render("r") + "  reset to the local (non-synced) database\n")
	}
	b.WriteString("\n" + styleMuted.Render("esc: close"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

// renderSettingsBrowsePopup renders the directory-only filepicker. Same
// per-line truncation as renderImportPickFile: bubbles/filepicker never
// truncates long names itself, and lipgloss's Width() word-wraps instead
// of truncating, which would desync the list's height from SetHeight's
// budget.
func (m Model) renderSettingsBrowsePopup(w, contentW int) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("Choose a folder to sync") + "\n")
	b.WriteString(styleMuted.Render(ansi.Truncate(m.fp.CurrentDirectory, contentW, "…")) + "\n\n")

	for _, line := range strings.Split(m.fp.View(), "\n") {
		b.WriteString(ansi.Truncate(line, contentW, "…") + "\n")
	}

	b.WriteString(styleMuted.Render("↑/↓ or j/k: navigate  ·  enter: open folder  ·  s: sync here  ·  esc: cancel"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

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
	m.importUseAI = os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("OPENAI_API_KEY") != "" ||
		os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("BUDGETCTL_PROVIDER") != ""
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
		return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
	}
	return m, nil
}

// openSettings opens the "o" settings popup, showing the current data
// directory / sync mode and offering to browse for a new one.
func (m Model) openSettings() Model {
	m.view = viewSettings
	m.settingsPicking = false
	m.settingsConfirming = false
	m.settingsPendingDir = ""
	m.settingsErr = nil
	return m
}

// openSettingsBrowse opens a directory-only filepicker rooted at iCloud
// Drive if present (the most common sync target), falling back to the
// home directory. Reuses the filepicker library in directory mode.
func (m Model) openSettingsBrowse() (Model, tea.Cmd) {
	fp := filepicker.New()
	fp.DirAllowed = true
	fp.FileAllowed = false
	fp.AllowedTypes = nil
	if home, err := os.UserHomeDir(); err == nil {
		fp.CurrentDirectory = home
		if icloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs"); isDir(icloud) {
			fp.CurrentDirectory = icloud
		}
	}
	h := m.height - 12
	if h < 5 {
		h = 5
	}
	fp.SetHeight(h)

	m.fp = fp
	m.settingsPicking = true
	m.settingsErr = nil
	return m, m.fp.Init()
}

// confirmDataDir stages a data_dir change for confirmation before doing
// anything — moving a real database is worth a deliberate "y", same as
// the delete confirmation elsewhere in this TUI. newDir == "" means
// "reset to the local default".
func (m Model) confirmDataDir(newDir string) Model {
	m.settingsOldPath = config.DBPath()
	m.settingsPendingDir = newDir
	m.settingsConfirming = true
	m.settingsPicking = false
	m.settingsErr = nil
	return m
}

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsConfirming {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "y", "Y":
			m.settingsConfirming = false
			return m, applyDataDirCmd(m.settingsOldPath, m.settingsPendingDir)
		default:
			m.settingsConfirming = false
		}
		return m, nil
	}

	if m.settingsPicking {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.settingsPicking = false
			return m, nil
		case "s":
			// Deliberately not fp.DidSelectFile/fp.Path: with DirAllowed
			// set, Enter both descends into a directory AND would satisfy
			// DidSelectFile, so browsing would end the instant you tried
			// to go deeper. Reading CurrentDirectory on a dedicated key
			// keeps Enter free to navigate.
			return m.confirmDataDir(m.fp.CurrentDirectory), nil
		}
		var cmd tea.Cmd
		m.fp, cmd = m.fp.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.view = viewList
		return m, nil
	case "b":
		return m.openSettingsBrowse()
	case "r":
		if config.Shared() {
			return m.confirmDataDir(""), nil
		}
	}
	return m, nil
}

// applyDataDirCmd persists the new data_dir and, if an existing database
// needs to move to make the change actually take effect, moves it:
//   - if the new location already has a database, it's used as-is (the
//     common "joining a device that already set up sync" case) — the
//     previous local database is left untouched at its old path, not
//     merged or deleted.
//   - else if the old location has a database, it's moved to the new
//     location (the "start syncing my existing data" case).
//   - else there's nothing to move (a fresh setup).
func applyDataDirCmd(oldPath, newDir string) tea.Cmd {
	return func() tea.Msg {
		if err := config.SetDataDir(newDir); err != nil {
			return settingsAppliedMsg{err: fmt.Errorf("save config: %w", err)}
		}
		if newDir == "" {
			return settingsAppliedMsg{status: "Switched back to the local database."}
		}

		newPath := config.DBPath()
		if newPath == oldPath {
			return settingsAppliedMsg{status: fmt.Sprintf("Now using %s.", newDir)}
		}
		if _, err := os.Stat(newPath); err == nil {
			return settingsAppliedMsg{status: fmt.Sprintf(
				"Found an existing database there — now using it (your previous local data is untouched at %s).", oldPath)}
		}
		if _, err := os.Stat(oldPath); err == nil {
			if err := moveFile(oldPath, newPath); err != nil {
				return settingsAppliedMsg{err: fmt.Errorf("moving existing database: %w", err)}
			}
			_ = os.Remove(oldPath + ".lock")
			return settingsAppliedMsg{status: fmt.Sprintf("Moved your existing data to %s.", newDir)}
		}
		return settingsAppliedMsg{status: fmt.Sprintf("Now syncing new data to %s.", newDir)}
	}
}

// moveFile renames oldPath to newPath, falling back to copy-then-remove
// if they're on different filesystems (os.Rename returns EXDEV) — a
// folder-synced directory (iCloud Drive, Dropbox) is usually on the same
// volume as $HOME, but not guaranteed.
func moveFile(oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}
	src, err := os.Open(oldPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(newPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(oldPath)
}

// profileDisplayNames returns "default" plus every configured profile, in
// the order the "p" screen lists them.
func profileDisplayNames() []string {
	return append([]string{"default"}, config.Profiles()...)
}

// openProfiles opens the "p" profiles screen, with the cursor starting on
// whichever profile is currently active.
func (m Model) openProfiles() Model {
	m.view = viewProfiles
	m.profileCreating = false
	m.profileRemoving = ""
	m.profileErr = nil
	active := config.ActiveProfile()
	m.profilesCursor = 0
	for i, name := range profileDisplayNames() {
		if name == active || (name == "default" && active == "") {
			m.profilesCursor = i
			break
		}
	}
	return m
}

func (m Model) updateProfiles(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.profileCreating {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.profileCreating = false
			m.profileNewInput.Blur()
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.profileNewInput.Value())
			m.profileCreating = false
			m.profileNewInput.Blur()
			if name == "" {
				return m, nil
			}
			if err := config.AddProfile(name, ""); err != nil {
				m.profileErr = err
				return m, nil
			}
			m.profileErr = nil
			for i, n := range profileDisplayNames() {
				if n == name {
					m.profilesCursor = i
				}
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.profileNewInput, cmd = m.profileNewInput.Update(msg)
		return m, cmd
	}

	if m.profileRemoving != "" {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "y", "Y":
			name := m.profileRemoving
			m.profileRemoving = ""
			if err := config.RemoveProfile(name); err != nil {
				m.profileErr = err
				return m, nil
			}
			m.profileErr = nil
			m.profilesCursor = 0
		default:
			m.profileRemoving = ""
		}
		return m, nil
	}

	names := profileDisplayNames()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.view = viewList
		return m, nil
	case "j", "down":
		if m.profilesCursor < len(names)-1 {
			m.profilesCursor++
		}
	case "k", "up":
		if m.profilesCursor > 0 {
			m.profilesCursor--
		}
	case "n":
		ni := textinput.New()
		ni.Placeholder = "profile name…"
		ni.CharLimit = 40
		m.profileNewInput = ni
		m.profileCreating = true
		m.profileErr = nil
		return m, m.profileNewInput.Focus()
	case "d":
		if m.profilesCursor >= 0 && m.profilesCursor < len(names) && names[m.profilesCursor] != "default" {
			m.profileRemoving = names[m.profilesCursor]
		}
	case "enter":
		if m.profilesCursor < 0 || m.profilesCursor >= len(names) {
			return m, nil
		}
		name := names[m.profilesCursor]
		if name == "default" {
			name = ""
		}
		if name == config.ActiveProfile() {
			m.view = viewList
			return m, nil
		}
		if err := config.SetActiveProfile(name); err != nil {
			m.profileErr = err
			return m, nil
		}
		m.profileErr = nil
		m.view = viewList
		// The previous profile's months/accounts/filters don't apply to
		// whatever's in the new one — start from a clean slate and let the
		// reload repopulate them.
		m.activeAccount = -1
		m.activeTab = 0
		m.months = nil
		m.accounts = nil
		m.cursor = 0
		m.searchQ = ""
		m.categoryFilter = ""
		if name == "" {
			m.setStatus("Switched to the default database.")
		} else {
			m.setStatus(fmt.Sprintf("Switched to profile %q.", name))
		}
		return m, loadCmd("", "", "")
	}
	return m, nil
}

// renderProfilesPopup renders the "p" profiles screen: default + configured
// profiles with the active one marked, or an inline create/remove prompt.
// Same bordered-popup style and row-highlighting approach (padRunes +
// truncRunes, one styleSelected.Render call per row) as
// renderCategoryPickPopup.
func (m Model) renderProfilesPopup() string {
	w := m.importPopupWidth()
	contentW := w - 6

	var b strings.Builder
	b.WriteString(styleHeader.Render("Profiles") + "\n\n")

	if m.profileRemoving != "" {
		b.WriteString(styleErr.Render(fmt.Sprintf("Forget profile %q? (its database stays on disk)", m.profileRemoving)) + "\n\n")
		b.WriteString(styleMuted.Render("y: confirm  ·  any other key: cancel"))
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(1, 2).
			Width(w).
			Render(b.String())
	}

	if m.profileCreating {
		b.WriteString(styleHelp.Render("New profile name:") + "\n  " + m.profileNewInput.View() + "\n\n")
		if m.profileErr != nil {
			b.WriteString(styleErr.Render("✗ "+m.profileErr.Error()) + "\n\n")
		}
		b.WriteString(styleMuted.Render("enter: create  ·  esc: cancel"))
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(1, 2).
			Width(w).
			Render(b.String())
	}

	b.WriteString(styleMuted.Render("Each profile is a fully separate database.") + "\n\n")

	active := config.ActiveProfile()
	itemW := contentW - 2
	for i, name := range profileDisplayNames() {
		label := name
		if name == active || (name == "default" && active == "") {
			label += "  (active)"
		}
		if i == m.profilesCursor {
			b.WriteString("> " + styleSelected.Render(padRunes(truncRunes(label, itemW), itemW)) + "\n")
			continue
		}
		b.WriteString("  " + truncRunes(label, itemW) + "\n")
	}

	if m.profileErr != nil {
		b.WriteString("\n" + styleErr.Render("✗ "+m.profileErr.Error()) + "\n")
	}

	b.WriteString("\n" + styleMuted.Render("↑/↓ navigate  ·  enter: switch  ·  n: new  ·  d: remove  ·  esc: close"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(1, 2).
		Width(w).
		Render(b.String())
}

func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// delete confirmation (status-bar prompt)
	if m.deleteTarget != nil {
		switch msg.String() {
		case "y", "Y":
			target := m.deleteTarget
			m.deleteTarget = nil
			m.lastDeleted = target
			m.status = fmt.Sprintf("Deleted %q — press u to undo", target.Description)
			m.statusTime = time.Now()
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
			cat := strings.TrimSpace(m.catInput.Value())

			var ids []string
			prefill := ""
			if m.selecting && len(m.selected) > 0 {
				for id := range m.selected {
					ids = append(ids, id)
				}
				m.selecting = false
				m.selected = nil
			} else if len(m.txs) > 0 {
				ids = []string{m.txs[m.cursor].ID}
				prefill = m.txs[m.cursor].Description
			}
			if len(ids) == 0 {
				return m, nil
			}
			if cat == "" {
				// Clearing the category — nothing to turn into a rule.
				return m, batchSetCategoryCmd(ids, cat)
			}
			if cats := splitCategories(cat); len(cats) > 1 {
				// "Auto;Business" — split each transaction's amount evenly
				// across the given categories. No rule-prompt here: a
				// CategoryRule maps one pattern to one category, a split
				// doesn't fit that shape.
				return m, splitCategoryCmd(ids, cats)
			}
			m.pendingCatIDs = ids
			m.pendingCat = cat
			m.savingRule = true
			m.ruleInput.SetValue(prefill)
			m.ruleInput.CursorEnd()
			return m, m.ruleInput.Focus()
		case "esc":
			m.categorizing = false
			m.catInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.catInput, cmd = m.catInput.Update(msg)
		return m, cmd
	}

	// "save as a rule?" follow-up after quick categorize — lets one manual
	// category assignment (e.g. "RCIAT" -> "Auto Finanzierung") retroactively
	// and automatically re-apply to every matching transaction, via the same
	// tag/apply-rules mechanism `budgetctl tag` already exposes on the CLI.
	if m.savingRule {
		switch msg.String() {
		case "enter", "esc":
			m.savingRule = false
			m.ruleInput.Blur()
			pattern := ""
			if msg.String() == "enter" {
				pattern = strings.TrimSpace(m.ruleInput.Value())
			}
			ids, cat := m.pendingCatIDs, m.pendingCat
			m.pendingCatIDs, m.pendingCat = nil, ""
			return m, categorizeCmd(ids, cat, pattern)
		}
		var cmd tea.Cmd
		m.ruleInput, cmd = m.ruleInput.Update(msg)
		return m, cmd
	}

	// batch select mode
	if m.selecting {
		switch msg.String() {
		case "esc":
			m.selecting = false
			m.selected = nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.txs)-1 {
				m.cursor++
			}
		case " ":
			if len(m.txs) > 0 {
				id := m.txs[m.cursor].ID
				if m.selected[id] {
					delete(m.selected, id)
				} else {
					m.selected[id] = true
				}
			}
		case "A":
			for _, t := range m.txs {
				m.selected[t.ID] = true
			}
		case "c":
			if len(m.selected) > 0 {
				m.categorizing = true
				m.catInput.SetValue("")
				m.catInput.CursorEnd()
				return m, m.catInput.Focus()
			}
		}
		return m, nil
	}

	if m.inPalette {
		closePalette := func(mm Model) Model {
			mm.inPalette = false
			mm.paletteInput.Blur()
			mm.paletteInput.SetValue("")
			mm.paletteCursor = 0
			return mm
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			return closePalette(m), nil
		case "up", "ctrl+p":
			if m.paletteCursor > 0 {
				m.paletteCursor--
			}
			return m, nil
		case "down", "ctrl+n":
			matches := palette.Match(paletteCommands, m.paletteInput.Value())
			if m.paletteCursor < len(matches)-1 {
				m.paletteCursor++
			}
			return m, nil
		case "enter":
			matches := palette.Match(paletteCommands, m.paletteInput.Value())
			if len(matches) == 0 {
				return closePalette(m), nil
			}
			if m.paletteCursor >= len(matches) {
				m.paletteCursor = len(matches) - 1
			}
			chosen := matches[m.paletteCursor]
			m = closePalette(m)
			replay := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(chosen.Key)}
			if chosen.Key == "enter" {
				replay = tea.KeyMsg{Type: tea.KeyEnter}
			}
			return m.updateList(replay)
		}
		var cmd tea.Cmd
		m.paletteInput, cmd = m.paletteInput.Update(msg)
		m.paletteCursor = 0
		return m, cmd
	}

	if m.searching {
		switch msg.String() {
		case "enter":
			// Filtering already happened live as the user typed (below) —
			// enter just closes the input box, no DB round-trip needed.
			m.searching = false
			m.cursor = 0
		case "esc":
			m.searching = false
			m.searchInput.SetValue("")
			m.searchQ = ""
			m.cursor = 0
			m.txs = filterTxs(m.allTxs, "")
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.searchQ = m.searchInput.Value()
			m.cursor = 0
			m.txs = filterTxs(m.searchTxs, m.searchQ)
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
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "y":
		if i, ok := adjacentYearTab(m.months, m.activeTab, 1); ok {
			m.activeTab = i
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "Y":
		if i, ok := adjacentYearTab(m.months, m.activeTab, -1); ok {
			m.activeTab = i
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "]":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), 1)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "[":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), -1)
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		// jump to the nth visible (on-screen) transaction row — reuses
		// rowHitTest's scroll-window math so "3" lands on the same row a
		// click at that screen position would.
		n := int(msg.String()[0] - '0')
		listH := m.height - m.listStartRow() - 2
		if listH < 1 {
			listH = 1
		}
		winStart := 0
		if m.cursor >= listH {
			winStart = m.cursor - listH + 1
		}
		if idx := winStart + n - 1; idx < len(m.txs) {
			m.cursor = idx
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
		m.vp.SetContent(renderSummary(m.summary, m.goals, m.trend, m.recurring, m.width))
		m.vp.GotoTop()
	case "/":
		m.searching = true
		m.searchInput.Focus()
		m.searchInput.SetValue("")
		m.searchTxs = m.allTxs // placeholder so the list isn't empty while the all-months fetch is in flight
		return m, loadSearchCmd(m.activeAccountName(), m.categoryFilter)
	case ":":
		m.inPalette = true
		m.paletteCursor = 0
		m.paletteInput.SetValue("")
		return m, m.paletteInput.Focus()
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
	case "f":
		m = m.openCategoryPick()
		return m, m.categoryPickInput.Focus()
	case "o":
		m = m.openSettings()
		return m, nil
	case "p":
		m = m.openProfiles()
		return m, nil
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
	case "u":
		if m.lastDeleted != nil {
			t := m.lastDeleted
			m.lastDeleted = nil
			m.status = ""
			return m, insertTxCmd(t)
		}
	case "c":
		if len(m.txs) > 0 {
			m.categorizing = true
			m.catInput.SetValue(m.txs[m.cursor].Category)
			m.catInput.CursorEnd()
			return m, m.catInput.Focus()
		}
	case "v":
		if len(m.txs) > 0 {
			m.selecting = true
			m.selected = map[string]bool{m.txs[m.cursor].ID: true}
		}
	case "a":
		if !config.IsPro() {
			m.setStatus("AI categorize is a missionctl Bundle feature — missionctl.sh/#pricing")
			return m, nil
		}
		var uncategorized []models.Transaction
		for _, t := range m.allTxs {
			if strings.TrimSpace(t.Category) == "" {
				uncategorized = append(uncategorized, t)
			}
		}
		if len(uncategorized) == 0 {
			m.setStatus("nothing to categorize")
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Categorizing via AI… 0/%d", len(uncategorized)))
		return m, aiCategorizeStepCmd(uncategorized, m.categories, 0, len(uncategorized))
	case "esc":
		switch {
		case m.searchQ != "":
			m.searchQ = ""
			m.cursor = 0
			m.txs = filterTxs(m.allTxs, "")
		case m.categoryFilter != "":
			m.categoryFilter = ""
			m.cursor = 0
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), "")
		}
	}
	return m, nil
}

func (m Model) updateSummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingGoal {
		switch msg.String() {
		case "enter":
			m.settingGoal = false
			m.goalInput.Blur()
			fields := strings.Fields(m.goalInput.Value())
			if len(fields) < 2 {
				m.err = fmt.Errorf("goal: expected \"<category> <amount>\"")
				return m, nil
			}
			amount, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil || amount <= 0 {
				m.err = fmt.Errorf("goal: amount must be a positive number")
				return m, nil
			}
			category := strings.Join(fields[:len(fields)-1], " ")
			return m, goalSetCmd(category, amount)
		case "esc":
			m.settingGoal = false
			m.goalInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.goalInput, cmd = m.goalInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "q", "esc":
		m.view = viewList
		return m, nil
	case "g":
		m.settingGoal = true
		m.goalInput.SetValue("")
		return m, m.goalInput.Focus()
	case "t":
		if !config.IsPro() {
			m.setStatus("AI category translate is a missionctl Bundle feature — missionctl.sh/#pricing")
			return m, nil
		}
		m.translateLoading = true
		m.translateSuggestions = nil
		m.translateErr = nil
		m.view = viewCategoryTranslate
		return m, categoryTranslateCmd()
	case "tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab + 1) % len(m.months)
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "shift+tab":
		if len(m.months) > 0 {
			m.activeTab = (m.activeTab - 1 + len(m.months)) % len(m.months)
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "y":
		if i, ok := adjacentYearTab(m.months, m.activeTab, 1); ok {
			m.activeTab = i
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "Y":
		if i, ok := adjacentYearTab(m.months, m.activeTab, -1); ok {
			m.activeTab = i
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "]":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), 1)
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
		}
	case "[":
		if len(m.accounts) > 0 {
			m.activeAccount = cycleAccount(m.activeAccount, len(m.accounts), -1)
			return m, loadCmd(m.activeMonth(), m.activeAccountName(), m.categoryFilter)
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
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		return txSavedMsg{s.Upsert(context.Background(), t)}
	}
}

func updateTxCmd(t *models.Transaction) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		return txSavedMsg{s.Update(context.Background(), t)}
	}
}

func deleteTxCmd(id string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txDeletedMsg{err}
		}
		defer s.Close()
		return txDeletedMsg{s.Delete(context.Background(), id)}
	}
}

// batchSetCategoryCmd applies one category to every given transaction ID —
// covers both the single quick-categorize ("c") case and batch mode ("v").
func batchSetCategoryCmd(ids []string, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		var lastErr error
		for _, id := range ids {
			if err := s.SetCategory(ctx, id, category); err != nil {
				lastErr = err
			}
		}
		return txSavedMsg{lastErr}
	}
}

// splitCategories parses a ";"-separated quick-categorize input like
// "Auto ; Business" into trimmed, non-empty category names.
func splitCategories(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitCategoryCmd divides each transaction's amount evenly across the
// given categories (see store.SetSplits) — the "Auto;Business" case at the
// quick-categorize prompt.
func splitCategoryCmd(ids []string, categories []string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		var lastErr error
		for _, id := range ids {
			if err := s.SetSplits(ctx, id, categories); err != nil {
				lastErr = err
			}
		}
		return txSavedMsg{lastErr}
	}
}

// categorizeCmd sets category on every given transaction ID and, if pattern
// is non-empty, also saves it as a category rule and re-applies all rules —
// the TUI equivalent of `budgetctl tag PATTERN --category NAME --apply`,
// reached via the "save as rule?" prompt after quick-categorize ("c").
func categorizeCmd(ids []string, category, pattern string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return txSavedMsg{err}
		}
		defer s.Close()
		ctx := context.Background()
		var lastErr error
		for _, id := range ids {
			if err := s.SetCategory(ctx, id, category); err != nil {
				lastErr = err
			}
		}
		if lastErr == nil && pattern != "" {
			if err := s.SaveRule(ctx, pattern, category); err != nil {
				return txSavedMsg{err}
			}
			_, lastErr = s.ApplyRules(ctx)
		}
		return txSavedMsg{lastErr}
	}
}

func goalSetCmd(category string, amount float64) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return goalSavedMsg{err}
		}
		defer s.Close()
		return goalSavedMsg{s.SaveGoal(context.Background(), category, amount)}
	}
}

// categoryTranslateCmd asks the AI which existing categories don't match
// the categorization language (see budget.CategoryLanguage) and returns
// suggested renames for review in the "t" popup — nothing is written yet.
func categoryTranslateCmd() tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return categoryTranslatedMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()

		categories, err := s.ListCategories(ctx)
		if err != nil {
			return categoryTranslatedMsg{err: err}
		}
		renames, err := budget.AITranslateCategories(ctx, categories, budget.CategoryLanguage())
		if err != nil {
			return categoryTranslatedMsg{err: err}
		}

		olds := make([]string, 0, len(renames))
		for old := range renames {
			olds = append(olds, old)
		}
		sort.Strings(olds)
		suggestions := make([]categoryRename, 0, len(olds))
		for _, old := range olds {
			suggestions = append(suggestions, categoryRename{Old: old, New: renames[old]})
		}
		return categoryTranslatedMsg{suggestions: suggestions}
	}
}

// applyCategoryRenamesCmd runs the given renames through the same
// RenameCategory used by `budgetctl category rename` — transactions,
// splits, rules, and goals all follow.
func applyCategoryRenamesCmd(renames []categoryRename) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return categoryRenamesAppliedMsg{err: err}
		}
		defer s.Close()
		ctx := context.Background()

		count := 0
		for _, r := range renames {
			if _, err := s.RenameCategory(ctx, r.Old, r.New); err != nil {
				return categoryRenamesAppliedMsg{count: count, err: err}
			}
			count++
		}
		return categoryRenamesAppliedMsg{count: count}
	}
}

// aiCategorizeStepCmd processes one AICategorizeBatchSize-sized chunk of
// remaining (already filtered to uncategorized) transactions and writes
// back whatever categories it returns, then either reports a chunk-progress
// message (more left) or a final result (done or errored). Driven one chunk
// at a time — rather than handing the whole list to budget.AICategories in
// a single call — so the TUI status bar can show "n/total" instead of
// sitting on a static "Categorizing…" for however long the full batch
// takes; each returned aiCategorizeProgressMsg re-triggers this for the
// next chunk (see the Update() case for it).
func aiCategorizeStepCmd(remaining []models.Transaction, existingCategories []string, done, total int) tea.Cmd {
	return func() tea.Msg {
		if len(remaining) == 0 {
			return aiCategorizedMsg{count: done}
		}
		size := budget.AICategorizeBatchSize
		if size > len(remaining) {
			size = len(remaining)
		}
		chunk, rest := remaining[:size], remaining[size:]

		result, aiErr := budget.AICategories(context.Background(), chunk, existingCategories)

		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return aiCategorizedMsg{count: done, err: err}
		}
		defer s.Close()

		ctx := context.Background()
		newDone := done
		for _, t := range chunk {
			if cat, ok := result[t.Description]; ok && cat != "" {
				if err := s.SetCategory(ctx, t.ID, cat); err == nil {
					newDone++
				}
			}
		}
		if aiErr != nil {
			return aiCategorizedMsg{count: newDone, err: aiErr}
		}
		return aiCategorizeProgressMsg{remaining: rest, existingCategories: existingCategories, done: newDone, total: total}
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
	const tabRow = 2 // header title(0) + rule(1) + tabs(2)
	if y != tabRow || len(m.months) == 0 {
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

	// ── month tab bar (windowed — see renderMonthTabBar) ──
	b.WriteString(m.renderMonthTabBar(w))

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
	if m.inPalette {
		b.WriteString("  " + m.paletteInput.View() + "\n")
		matches := palette.Match(paletteCommands, m.paletteInput.Value())
		if len(matches) > 6 {
			matches = matches[:6]
		}
		if len(matches) == 0 {
			b.WriteString("    " + styleHelp.Render("no matching command") + "\n")
		}
		for i, c := range matches {
			row := fmt.Sprintf("%-9s %s", c.Name, c.Desc)
			if i == m.paletteCursor {
				b.WriteString("    " + styleSelected.Render("▶ "+row) + "\n")
			} else {
				b.WriteString("      " + styleHelp.Render(row) + "\n")
			}
		}
		b.WriteString("\n")
	}
	if m.searchQ != "" {
		b.WriteString(styleMuted.Render("  /"+m.searchQ) + "\n")
	}
	if m.categoryFilter != "" {
		b.WriteString(styleMuted.Render("  filter: ") + styleCategory.Render(m.categoryFilter) + styleMuted.Render("  (esc to clear)") + "\n")
	}
	if m.categorizing {
		b.WriteString("  " + styleCategory.Render("category: ") + m.catInput.View() + "\n")
	} else if m.savingRule {
		b.WriteString("  " + styleCategory.Render("save as rule? ") + m.ruleInput.View() + "\n")
	} else if m.selecting {
		b.WriteString("  " + styleSelected.Render(fmt.Sprintf("select: %d", len(m.selected))) +
			styleHelp.Render("  space toggle  A all  c categorize  esc cancel") + "\n")
	}

	listH := m.height - m.listStartRow() - 4 // blank spacer + divider + 2-line key bar
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
		selRowW := rowW
		if m.selecting {
			selRowW -= 4 // room for the "[x] " checkbox prefix
		}
		for i := start; i < end; i++ {
			t := &m.txs[i]
			checkbox := ""
			if m.selecting {
				if m.selected[t.ID] {
					checkbox = styleSelected.Render("[x]") + " "
				} else {
					checkbox = styleHelp.Render("[ ]") + " "
				}
			}
			var line string
			switch {
			case i == m.cursor:
				// The cursor row wraps its whole line in a single
				// styleSelected.Render() call below — nesting highlighted
				// (real-ANSI) text inside that would clobber its background
				// for everything after the highlight, so no query here.
				line = styleSelected.Width(selRowW).Render(formatTxRow(t, selRowW, ""))
			case i == m.hoverRow:
				line = theme.Hover.Width(selRowW).Render(formatTxRow(t, selRowW, ""))
			default:
				line = formatTxRow(t, selRowW, m.searchQ)
			}
			b.WriteString("  " + checkbox + line + "\n")
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

	right := netStr + posStr
	b.WriteString("\n" + styleDivider.Render(strings.Repeat("─", w)) + "\n")

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
		pad := rowW - lipgloss.Width(bar) - lipgloss.Width(right)
		if pad < 0 {
			pad = 0
		}
		b.WriteString("  " + bar + strings.Repeat(" ", pad) + right)
		return b.String()
	}

	// Two fixed lines, same layout convention as the rest of the suite
	// (notectl, mailctl, ...) rather than a single line that either
	// overflows or falls back to a much shorter legend depending on
	// terminal width.
	line1 := styleHelp.Render("enter:details  n:new  e:edit  d:delete  u:undo  c:categorize  a:ai-categorize  v:select")
	line2 := styleHelp.Render("i:import  s:summary  /:search  f:filter  :cmd  tab:month  y:year  [/]:account  ?:help  q:quit")
	pad := rowW - lipgloss.Width(line2) - lipgloss.Width(right)
	if pad < 0 {
		pad = 0
	}
	b.WriteString("  " + line1 + "\n  " + line2 + strings.Repeat(" ", pad) + right)
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

	vp := viewport.New(popW-4, popH-4) // border 1+1, padding(0,1) → 2 cols; -1 row for footer, -1 blank spacer above it
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
	m.vp.Height = vpH
	b.WriteString(m.vp.View())

	if m.settingGoal {
		b.WriteString("  " + styleCategory.Render("goal (category amount): ") + m.goalInput.View() + "\n")
	}

	pct := ""
	if m.vp.TotalLineCount() > m.vp.Height {
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
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return importDoneMsg{err: err}
		}
		defer s.Close()
		res, err := budget.ImportFile(context.Background(), s, path, account, useAI)
		return importDoneMsg{res: res, err: err}
	}
}

// loadCmd fetches transactions for month/account, unfiltered by search text
// — search is applied client-side (filterTxs) over the result, live as the
// user types, rather than round-tripping to SQLite on every keystroke or
// baking a LIKE clause into the query. Store.Filter.Query / the SQL LIKE
// path still exists and is still used by the CLI (`budgetctl list --query`),
// just not from here anymore.
func loadCmd(month, account, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return errMsg{err}
		}
		defer s.Close()
		ctx := context.Background()

		txs, err := s.List(ctx, store.Filter{Month: month, Account: account, Category: category, Limit: 500})
		if err != nil {
			return errMsg{err}
		}
		months, _ := s.ListMonths(ctx)
		accounts, _ := s.ListAccounts(ctx)
		categories, _ := s.ListCategories(ctx)

		// summary for active month (and account, if one is selected) — NOT
		// scoped to the category filter: the point of the summary is to
		// see spend ACROSS categories, filtering it to one category would
		// make the "By category" breakdown show just that one row.
		sum, _ := s.Summary(ctx, month, account)

		// goals with current-month spend (always across all accounts — a
		// budget goal like "dining < 200€" isn't naturally per-account)
		goals, _ := s.GoalStatuses(ctx, month)

		trend, _ := s.MonthlyTrend(ctx, account, 12)

		// Recurring-payment detection needs the full history, not just the
		// active month — reuses the same detector as the `recurring` CLI
		// command, just surfaced in the summary popup too.
		var recurring []budget.RecurringPattern
		if allTxs, err := s.List(ctx, store.Filter{}); err == nil {
			recurring = budget.DetectRecurring(allTxs)
		}

		return txLoadedMsg{txs: txs, months: months, accounts: accounts, categories: categories, sum: sum, goals: goals, trend: trend, recurring: recurring}
	}
}

// loadSearchCmd fetches every transaction in the current account/category
// scope, unbounded by month — "/" search used to only see whatever the
// active month tab had loaded, so a match sitting in any other month was
// simply invisible until you clicked through tabs to find it. No Limit (all
// 880-ish rows for a real account is nothing for local SQLite), matching
// what DetectRecurring already does for the same reason.
func loadSearchCmd(account, category string) tea.Cmd {
	return func() tea.Msg {
		s, err := store.New(config.DBPath(), config.Shared())
		if err != nil {
			return searchLoadedMsg{}
		}
		defer s.Close()
		txs, _ := s.List(context.Background(), store.Filter{Account: account, Category: category})
		return searchLoadedMsg{txs: txs}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

// payeeColW is the fixed display width of the Payee column in formatTxRow.
// doubleClickWindow opens the detail popup on a second click within this
// window, same pattern and duration taskctl uses for its own double-click.
const doubleClickWindow = 400 * time.Millisecond

// undoWindow is how long after a delete "u" still restores it — same
// duration taskctl uses for its own delete-undo.
const undoWindow = 5 * time.Second

const payeeColW = 20

func formatTxRow(t *models.Transaction, width int, query string) string {
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
	// padRunes, not fmt's "%-*s" — that pads by BYTE length, which
	// misaligns columns the moment a category/payee contains a multi-byte
	// rune (umlauts are routine in German bank text: ä/ö/ü/ß).
	catStyled := styleCategory.Render(padRunes(truncRunes(cat, 16), 16))

	dateStr := t.Date.Format("2006-01-02")
	dateStyled := coloredDate(dateStr, t.Date)

	// Truncate the PLAIN string first, then highlight the truncated
	// result, then pad via an ANSI-aware lipgloss.Width() wrap — padRunes
	// counts runes naively and would miscount escape-code bytes as
	// "runes" if applied to already-highlighted (ANSI-embedded) text.
	payee := t.Payee
	if payee == "" {
		payee = "—"
	}
	payeeMatchIdx := fuzzyMatchIndexes(query, t.Payee)
	payeeStyled := lipgloss.NewStyle().Width(payeeColW).
		Render(highlightMatches(truncRunes(payee, payeeColW), payeeMatchIdx, stylePayee))

	// purpose (Description) fills whatever's left — truncated by RUNE, not
	// byte: German bank text is full of multi-byte umlauts (ä/ö/ü/ß), and
	// byte-slicing mid-rune corrupts the output.
	purposeW := width - 12 - 10 - 18 - (payeeColW + 2) - 4
	if purposeW < 10 {
		purposeW = 10
	}
	descMatchIdx := fuzzyMatchIndexes(query, t.Description)
	purpose := highlightMatches(truncRunes(t.Description, purposeW), descMatchIdx, lipgloss.NewStyle())

	return fmt.Sprintf("%s  %s  %s  %s  %s",
		dateStyled,
		amtStyled,
		catStyled,
		payeeStyled,
		purpose,
	)
}

// truncRunes truncates s to at most n runes, appending "…" if it had to cut
// (the ellipsis itself counts toward n). Rune-safe, unlike raw byte slicing.
// filterTxs fuzzy-matches q against each transaction's payee OR
// description (github.com/sahilm/fuzzy), keeping a transaction if either
// matches. Unlike habctl's filterHabits, this does NOT re-rank by match
// quality — transactions are naturally date-ordered, and re-sorting by
// fuzzy score would scramble that chronological order (same reasoning as
// taskctl/calctl's list/day grouping preservation).
func filterTxs(txs []models.Transaction, q string) []models.Transaction {
	q = strings.TrimSpace(q)
	if q == "" {
		return txs
	}
	payees := make([]string, len(txs))
	descs := make([]string, len(txs))
	for i, t := range txs {
		payees[i] = t.Payee
		descs[i] = t.Description
	}
	matched := make(map[int]bool, len(txs))
	for _, mt := range fuzzy.Find(q, payees) {
		matched[mt.Index] = true
	}
	for _, mt := range fuzzy.Find(q, descs) {
		matched[mt.Index] = true
	}
	out := make([]models.Transaction, 0, len(matched))
	for i, t := range txs {
		if matched[i] {
			out = append(out, t)
		}
	}
	return out
}

// fuzzyMatchIndexes returns the rune indexes within s that q fuzzy-matched,
// or nil if q is empty or doesn't match at all.
func fuzzyMatchIndexes(q, s string) []int {
	if q == "" {
		return nil
	}
	matches := fuzzy.Find(q, []string{s})
	if len(matches) == 0 {
		return nil
	}
	return matches[0].MatchedIndexes
}

// highlightMatches renders s with the rune positions in idxs (from
// fuzzyMatchIndexes) styled via a warm, underlined variant of base, and
// every other character via base itself — fzf-style match highlighting.
//
// Renders one character at a time rather than nesting a highlighted span
// inside a single outer Render() call: lipgloss's Render() ends every
// string with a full SGR reset, so an inner Render() call's reset would
// wipe out the outer style for everything after the first highlighted
// character. Per-character rendering keeps every segment self-contained.
//
// idxs are indexes into s BEFORE any truncation — callers must resolve
// indexes against the same, untruncated string used to compute them.
func highlightMatches(s string, idxs []int, base lipgloss.Style) string {
	if len(idxs) == 0 {
		return base.Render(s)
	}
	hi := base.Foreground(colorAmber).Underline(true)
	matchSet := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		matchSet[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if matchSet[i] {
			b.WriteString(hi.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// padRunes right-pads s with spaces to n runes. Assumes s already fits
// within n runes (callers truncate first); no-ops otherwise.
func padRunes(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(r))
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
