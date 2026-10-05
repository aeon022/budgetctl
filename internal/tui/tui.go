package tui

import (
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/models"
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
