package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	"github.com/aeon022/missionctl-core/palette"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// ── Update ────────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		// -1, not msg.Height: reserves one row of slack so this app's own
		// layout math never lands exactly on the real terminal height —
		// avoids a long-standing bubbletea v1 quirk (charmbracelet/
		// bubbletea#304) where View() output with exactly as many lines as
		// the terminal and no trailing newline can fail to fully redraw.
		m.height = msg.Height - 1
		if m.height < 1 {
			m.height = 1
		}
		m.vp = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(m.height-6))

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

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			if m.view == viewSummary {
				m.vp.ScrollUp(3)
			} else if m.cursor > 0 {
				m.cursor--
			}
		case tea.MouseWheelDown:
			if m.view == viewSummary {
				m.vp.ScrollDown(3)
			} else if m.cursor < len(m.txs)-1 {
				m.cursor++
			}
		}
		return m, nil

	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft || m.view != viewList {
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
		return m, nil

	case tea.MouseMotionMsg:
		if m.view == viewList {
			m.hoverRow = m.rowHitTest(msg.Y)
		}
		return m, nil

	case tea.KeyPressMsg:
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
	ci.SetWidth(40) // v2: width 0 clips the placeholder to 1 char
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

func (m Model) updateCategoryPick(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
func (m Model) updateCategoryTranslate(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
	case "space":
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
	ai.SetWidth(40) // v2: width 0 clips the placeholder to 1 char

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

func (m Model) updateImport(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateSettings(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
			if err := config.MoveDBFile(oldPath, newPath); err != nil {
				return settingsAppliedMsg{err: fmt.Errorf("moving existing database: %w", err)}
			}
			_ = os.Remove(oldPath + ".lock")
			return settingsAppliedMsg{status: fmt.Sprintf("Moved your existing data to %s.", newDir)}
		}
		return settingsAppliedMsg{status: fmt.Sprintf("Now syncing new data to %s.", newDir)}
	}
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

func (m Model) updateProfiles(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		ni.SetWidth(40) // v2: width 0 clips the placeholder to 1 char
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

func (m Model) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		case "space":
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
			replay := tea.KeyPressMsg{Text: chosen.Key, Code: []rune(chosen.Key)[0]}
			if chosen.Key == "enter" {
				replay = tea.KeyPressMsg{Code: tea.KeyEnter}
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

func (m Model) updateSummary(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m Model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
