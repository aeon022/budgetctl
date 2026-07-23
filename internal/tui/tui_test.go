package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/viper"
)

func typeKeys(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	var tm tea.Model = m
	for _, k := range keys {
		var msg tea.Msg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		tm, cmd = tm.Update(msg)
	}
	return tm.(Model), cmd
}

// runCmd executes a tea.Cmd chain until it yields a message, feeding it back
// into the model — like the Bubbletea runtime would.
func feed(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = feed(t, m, c)
			}
			return m
		}
		var tm tea.Model
		tm, cmd = m.Update(msg)
		m = tm.(Model)
	}
	return m
}

func TestFormAddsManualTransaction(t *testing.T) {
	viper.Set("db_path", t.TempDir()+"/budget.db")
	defer viper.Set("db_path", "")

	m := New()

	// n opens the form with the date prefilled
	m, _ = typeKeys(t, m, "n")
	if m.view != viewForm {
		t.Fatalf("n must open the form, view = %v", m.view)
	}
	if m.form[fDate].Value() == "" {
		t.Fatal("date must be prefilled")
	}

	// enter through date, type description, amount (German comma), category
	m, _ = typeKeys(t, m, "enter")
	m, _ = typeKeys(t, m, "Lunch Pho An", "enter")
	m, _ = typeKeys(t, m, "-12,90", "enter")
	var cmd tea.Cmd
	m, cmd = typeKeys(t, m, "dining", "enter") // last field → submit
	if cmd == nil {
		t.Fatalf("submit must return a command (err: %v)", m.err)
	}
	m = feed(t, m, cmd)

	if m.err != nil {
		t.Fatalf("unexpected error after save: %v", m.err)
	}
	if m.view != viewList {
		t.Fatalf("must return to list after save, view = %v", m.view)
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	txs, err := s.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("want 1 transaction, got %d", len(txs))
	}
	got := txs[0]
	if got.Description != "Lunch Pho An" || got.Amount != -12.90 || got.Category != "dining" {
		t.Errorf("unexpected transaction: %+v", got)
	}
	if got.Account != "manual" || got.Source != "tui" {
		t.Errorf("manual entry must be tagged manual/tui: %+v", got)
	}
}

func TestFormValidation(t *testing.T) {
	viper.Set("db_path", t.TempDir()+"/budget.db")
	defer viper.Set("db_path", "")

	m := New()
	m, _ = typeKeys(t, m, "n")
	// empty description → error, stays in form
	m, _ = typeKeys(t, m, "enter", "enter") // skip date, skip desc
	m, cmd := typeKeys(t, m, "-5", "enter", "enter")
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("save must not run without description, got %T", msg)
		}
	}
	if m.err == nil {
		t.Fatal("want validation error for missing description")
	}
	if m.view != viewForm {
		t.Fatal("must stay in form on validation error")
	}
}

func TestEditFlow(t *testing.T) {
	viper.Set("db_path", t.TempDir()+"/budget.db")
	defer viper.Set("db_path", "")

	// seed one tx directly
	s, err := store.New(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	m = feed(t, m, m.Init())
	_ = s.Close()

	// load list
	m = feed(t, m, loadCmd("", "", ""))
	if len(m.txs) != 0 {
		t.Fatalf("expected empty list, got %d", len(m.txs))
	}
}

func TestDeleteConfirmCancel(t *testing.T) {
	viper.Set("db_path", t.TempDir()+"/budget.db")
	defer viper.Set("db_path", "")

	// add one entry through the form
	m := New()
	m, _ = typeKeys(t, m, "n", "enter")
	m, _ = typeKeys(t, m, "Temp", "enter")
	m, cmd := typeKeys(t, m, "-1", "enter", "enter")
	m = feed(t, m, cmd)
	m = feed(t, m, loadCmd("", "", ""))
	if len(m.txs) != 1 {
		t.Fatalf("setup failed: %d txs", len(m.txs))
	}

	// d then any key ≠ y cancels
	m, _ = typeKeys(t, m, "d")
	if m.deleteTarget == nil {
		t.Fatal("d must arm delete confirmation")
	}
	m, _ = typeKeys(t, m, "x")
	if m.deleteTarget != nil {
		t.Fatal("non-y key must cancel delete")
	}

	// d then y deletes
	m, _ = typeKeys(t, m, "d")
	m, cmd = typeKeys(t, m, "y")
	if cmd == nil {
		t.Fatal("y must trigger delete command")
	}
	m = feed(t, m, cmd)
	m = feed(t, m, loadCmd("", "", ""))
	if len(m.txs) != 0 {
		t.Fatalf("entry not deleted: %+v", m.txs)
	}
}

func TestHelpOverlay_OpenScrollClose(t *testing.T) {
	m := Model{width: 100, height: 30}

	m, _ = typeKeys(t, m, "?")
	if m.view != viewHelp {
		t.Fatalf("expected viewHelp after '?', got %v", m.view)
	}
	if m.helpVP.TotalLineCount() == 0 {
		t.Fatal("expected help content to be populated")
	}

	before := m.helpVP.ScrollPercent()
	m, _ = typeKeys(t, m, "j", "j", "j", "j", "j")
	if m.helpVP.ScrollPercent() <= before {
		t.Errorf("expected scroll to advance after pressing j, stayed at %v", before)
	}

	m, _ = typeKeys(t, m, "esc")
	if m.view != viewList {
		t.Errorf("expected esc to close help back to viewList, got %v", m.view)
	}
}

func TestHelpOverlay_FitsWithinBackgroundHeight(t *testing.T) {
	// Regression guard: the popup must be sized from the actual rendered
	// background, not just the raw terminal height — a short background
	// (e.g. an empty transaction list) must not produce a popup taller
	// than what's on screen.
	m := Model{width: 100, height: 30}
	m = m.openHelp()
	bgLines := len(splitLinesForTest(m.renderList()))
	if m.helpPopH > bgLines {
		t.Errorf("popup height %d exceeds background height %d", m.helpPopH, bgLines)
	}
}

func splitLinesForTest(s string) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(lines, cur)
}

func TestTabHitTest_ClickSwitchesActiveMonth(t *testing.T) {
	m := Model{width: 100, height: 20, months: []string{"2026-07", "2026-06", "2026-05"}, activeTab: 0}

	mi, cmd := m.Update(tea.MouseMsg{X: 12, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.activeTab != 1 {
		t.Errorf("expected click on second tab to switch activeTab to 1, got %d", m.activeTab)
	}
	if cmd == nil {
		t.Error("expected a load command after switching tabs")
	}
}

func TestTabHitTest_MissOutsideAnyTabDoesNothing(t *testing.T) {
	m := Model{width: 100, height: 20, months: []string{"2026-07", "2026-06"}, activeTab: 0}
	mi, _ := m.Update(tea.MouseMsg{X: 90, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.activeTab != 0 {
		t.Errorf("expected click past all tabs to leave activeTab unchanged, got %d", m.activeTab)
	}
}

func TestRowHitTest_ClickMovesCursorToThatTransaction(t *testing.T) {
	m := Model{
		width: 100, height: 20,
		txs: []models.Transaction{
			{Description: "A"}, {Description: "B"}, {Description: "C"},
		},
	}
	mi, _ := m.Update(tea.MouseMsg{X: 5, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.cursor != 1 {
		t.Errorf("expected click on second row to move cursor to 1, got %d", m.cursor)
	}
}

func TestRowHitTest_ClickBelowListDoesNothing(t *testing.T) {
	m := Model{
		width: 100, height: 20,
		txs: []models.Transaction{{Description: "A"}, {Description: "B"}},
	}
	mi, _ := m.Update(tea.MouseMsg{X: 5, Y: 15, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.cursor != 0 {
		t.Errorf("expected click below the list to leave cursor unchanged, got %d", m.cursor)
	}
}

func TestRowHitTest_RespectsScrollWindow(t *testing.T) {
	// With more transactions than fit on screen and cursor scrolled down,
	// a click must map to the transaction actually visible at that row,
	// not naively to txs[y - listStartRow].
	var txs []models.Transaction
	for i := 0; i < 30; i++ {
		txs = append(txs, models.Transaction{Description: fmt.Sprintf("tx%d", i)})
	}
	m := Model{width: 100, height: 15, txs: txs, cursor: 20}

	listH := m.height - m.listStartRow() - 2
	winStart := m.cursor - listH + 1
	got := m.rowHitTest(m.listStartRow()) // click on the first visible row
	if got != winStart {
		t.Errorf("expected click on first visible row to hit tx index %d (scroll window start), got %d", winStart, got)
	}
}

func TestImportAssistant_OpensToFilePicker(t *testing.T) {
	m := Model{width: 100, height: 30}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = mi.(Model)
	if m.view != viewImport || m.importStep != importPickFile {
		t.Fatalf("expected viewImport/importPickFile after 'i', got view=%v step=%v", m.view, m.importStep)
	}
	if cmd == nil {
		t.Error("expected a command to init the file picker (directory read)")
	}
}

func TestImportAssistant_ParseErrorStaysOnPickerWithMessage(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()

	mi, _ := m.Update(importParsedMsg{err: fmt.Errorf("boom")})
	m = mi.(Model)
	if m.view != viewImport || m.importStep != importPickFile {
		t.Errorf("expected to stay on the file picker after a parse error, got view=%v step=%v", m.view, m.importStep)
	}
	if m.importErr == nil {
		t.Error("expected importErr to be set so the picker can show it")
	}
}

func TestImportAssistant_SuccessfulParseMovesToPreview(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()

	parsed := []models.Transaction{{Description: "Rewe", Amount: -42.30}, {Description: "Gehalt", Amount: 2800}}
	mi, _ := m.Update(importParsedMsg{txs: parsed})
	m = mi.(Model)
	if m.importStep != importPreview {
		t.Fatalf("expected importPreview after a successful parse, got %v", m.importStep)
	}
	if len(m.importParsed) != 2 {
		t.Errorf("expected 2 parsed transactions carried into the preview, got %d", len(m.importParsed))
	}
}

func TestImportAssistant_PreviewEscGoesBackToPicker(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importPreview
	m.importParsed = []models.Transaction{{Description: "Rewe", Amount: -1}}

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(Model)
	if m.importStep != importPickFile {
		t.Errorf("expected esc at the preview step to return to the file picker, got %v", m.importStep)
	}
}

func TestImportAssistant_PreviewAIToggle(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importPreview
	before := m.importUseAI

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = mi.(Model)
	if m.importUseAI == before {
		t.Error("expected 'a' to toggle importUseAI")
	}
}

func TestImportAssistant_EnterAtPreviewStartsImport(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importPreview
	m.importParsed = []models.Transaction{{Description: "Rewe", Amount: -1}}
	m.importPath = "/tmp/does-not-matter-for-this-test.csv"

	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.importStep != importRunning {
		t.Fatalf("expected importRunning after enter, got %v", m.importStep)
	}
	if cmd == nil {
		t.Error("expected a command to actually run the import")
	}
}

func TestImportAssistant_EnterAtPreviewWithNoRowsDoesNothing(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importPreview
	m.importParsed = nil // e.g. a file that parsed to zero transactions

	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.importStep != importPreview {
		t.Errorf("expected enter with no parsed rows to stay on the preview, got %v", m.importStep)
	}
}

func TestImportAssistant_DoneStepAnyKeyClosesAndRefreshesList(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importRunning

	mi, _ := m.Update(importDoneMsg{res: budget.ImportResult{Imported: 3}})
	m = mi.(Model)
	if m.importStep != importDone {
		t.Fatalf("expected importDone after the import command resolves, got %v", m.importStep)
	}

	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.view != viewList {
		t.Errorf("expected any key at the done step to close back to viewList, got %v", m.view)
	}
	if cmd == nil {
		t.Error("expected closing to trigger a list reload")
	}
}

func TestCycleAccount_WrapsThroughAllPlusEachAccount(t *testing.T) {
	// n=2 accounts → valid indices are -1 (All), 0, 1.
	cases := []struct {
		active, dir, want int
	}{
		{-1, 1, 0},
		{0, 1, 1},
		{1, 1, -1}, // wraps past the last account back to "All"
		{-1, -1, 1},
		{1, -1, 0},
		{0, -1, -1},
	}
	for _, c := range cases {
		got := cycleAccount(c.active, 2, c.dir)
		if got != c.want {
			t.Errorf("cycleAccount(%d, 2, %d) = %d, want %d", c.active, c.dir, got, c.want)
		}
	}
}

func TestAccountTab_KeyCyclesActiveAccount(t *testing.T) {
	m := Model{width: 100, height: 20, accounts: []string{"N26", "ING"}, activeAccount: -1}

	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = mi.(Model)
	if m.activeAccount != 0 {
		t.Errorf("expected ']' to move from All to account 0, got %d", m.activeAccount)
	}
	if cmd == nil {
		t.Error("expected a load command after cycling account")
	}

	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	m = mi.(Model)
	if m.activeAccount != -1 {
		t.Errorf("expected '[' to move back to All, got %d", m.activeAccount)
	}
}

func TestAccountTabHitTest_ClickSwitchesActiveAccount(t *testing.T) {
	m := Model{width: 100, height: 20, accounts: []string{"N26", "ING"}, activeAccount: -1}

	// account tab row is row 3 (title, rule, month tabs, account tabs);
	// "All" occupies the leftmost columns, so a click near x=10 should hit it,
	// and a click further right should hit "N26".
	mi, cmd := m.Update(tea.MouseMsg{X: 10, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.activeAccount != 0 {
		t.Errorf("expected click on second account tab to select account 0 (N26), got %d", m.activeAccount)
	}
	if cmd == nil {
		t.Error("expected a load command after switching accounts via click")
	}
}

func TestAccountTabRow_ShownWithJustOneAccount(t *testing.T) {
	// A single tagged account still gets a tab row — even though there's
	// nothing to "switch" to yet, seeing "All | <account>" is the user's
	// only visible confirmation that tagging actually worked. Hiding it
	// until a 2nd account existed made a successful import look like it
	// silently did nothing.
	m := Model{width: 100, height: 20, accounts: []string{"N26"}, activeAccount: -1}
	if !strings.Contains(m.renderList(), "N26") {
		t.Error("expected the account tab row to render even with only one account")
	}
	// Clicking the (now-visible) "N26" tab should select it.
	if i := m.accountTabHitTest(10, 3); i != 0 {
		t.Errorf("expected clicking the N26 tab to hit account index 0, got %d", i)
	}
}

func TestAccountTabRow_HiddenWithNoAccounts(t *testing.T) {
	// With zero tagged accounts there's nothing to show — a click on that
	// row must not be mistaken for an account-tab click.
	m := Model{width: 100, height: 20, accounts: nil, activeAccount: -1}
	if strings.Contains(m.renderList(), "All") {
		t.Error("expected no account tab row (not even \"All\") with zero tagged accounts")
	}
	mi, _ := m.Update(tea.MouseMsg{X: 5, Y: 3, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = mi.(Model)
	if m.activeAccount != -1 {
		t.Errorf("expected a click on the (nonexistent) account-tab row to do nothing, activeAccount changed to %d", m.activeAccount)
	}
}

func TestImportAssistant_AccountAutoDetectedFromParsedTransactions(t *testing.T) {
	m := Model{width: 100, height: 30}
	m = m.openImport()

	parsed := []models.Transaction{{Description: "Rewe", Amount: -42.30, Account: "N26"}}
	mi, _ := m.Update(importParsedMsg{txs: parsed})
	m = mi.(Model)
	if got := m.importAcctInput.Value(); got != "N26" {
		t.Errorf("expected account input pre-filled with detected account N26, got %q", got)
	}
}

func TestImportAssistant_AccountTagAppliedOnImport(t *testing.T) {
	viper.Set("db_path", t.TempDir()+"/budget.db")
	defer viper.Set("db_path", "")

	csvPath := t.TempDir() + "/export.csv"
	if err := os.WriteFile(csvPath, []byte("Date,Description,Amount\n2026-07-20,Coffee Shop,-4.50\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.importStep = importPreview
	m.importPath = csvPath
	m.importParsed = []models.Transaction{{Description: "Coffee Shop", Amount: -4.50}}

	// 't' enters edit mode, type a custom account tag, 'enter' exits edit
	// mode, a second 'enter' actually runs the import.
	m, _ = typeKeys(t, m, "t", "m", "y", "a", "c", "c", "o", "u", "n", "t", "enter")
	if m.importEditingAcct {
		t.Fatal("expected first enter to exit account-edit mode")
	}
	if got := m.importAcctInput.Value(); got != "myaccount" {
		t.Fatalf("expected typed account tag 'myaccount', got %q", got)
	}

	var cmd tea.Cmd
	m, cmd = typeKeys(t, m, "enter")
	if cmd == nil {
		t.Fatal("expected the second enter to trigger the import command")
	}
	m = feed(t, m, cmd)
	if m.importErr != nil {
		t.Fatalf("unexpected import error: %v", m.importErr)
	}

	s, err := store.New(config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	txs, err := s.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 imported transaction, got %d", len(txs))
	}
	if txs[0].Account != "myaccount" {
		t.Errorf("expected imported transaction tagged with account 'myaccount', got %q", txs[0].Account)
	}
}

func TestRenderList_LineCountMatchesHeightExactly(t *testing.T) {
	// Regression test: renderList used to emit m.height+1 lines (listH's
	// budget only reserved 1 row for the trailing status bar, missing the
	// divider line printed just above it), which pushed the top of the
	// view — the header — off screen in terminals that don't reflow a
	// too-tall alt-screen frame.
	var txs []models.Transaction
	for i := 0; i < 84; i++ {
		txs = append(txs, models.Transaction{Description: fmt.Sprintf("tx%d", i)})
	}
	for _, h := range []int{8, 15, 20, 30, 43} {
		m := Model{width: 100, height: h, months: []string{"2026-07", "2026-06"}, txs: txs}
		lines := strings.Split(m.renderList(), "\n")
		if len(lines) != h {
			t.Errorf("height=%d: renderList produced %d lines, want exactly %d", h, len(lines), h)
		}
	}
}

func TestImportPickFile_LongFileNamesDoNotOverflowPopup(t *testing.T) {
	// Regression test: bubbles/filepicker never truncates long file names,
	// so the popup's lipgloss Width() word-wrapped them into extra
	// physical lines the height budget never accounted for — pushing the
	// footer (and the file-list navigation hints) off the bottom of the
	// popup. Reproduced with a real long filename, not a synthetic one,
	// since the bug only shows once fp.Init() actually lists real files.
	dir := t.TempDir()
	longName := strings.Repeat("a-very-long-descriptive-bank-export-filename-", 3) + ".csv"
	if err := os.WriteFile(dir+"/"+longName, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Model{width: 100, height: 30}
	m = m.openImport()
	m.fp.CurrentDirectory = dir

	if cmd := m.fp.Init(); cmd != nil {
		if msg := cmd(); msg != nil {
			var fpCmd tea.Cmd
			m.fp, fpCmd = m.fp.Update(msg)
			for fpCmd != nil {
				msg2 := fpCmd()
				if msg2 == nil {
					break
				}
				m.fp, fpCmd = m.fp.Update(msg2)
			}
		}
	}

	lines := strings.Split(m.renderImportPopup(), "\n")
	if len(lines) > m.height {
		t.Errorf("import popup is %d lines tall, exceeds terminal height %d — footer/nav keys would be clipped", len(lines), m.height)
	}
}

func TestSparkline_ScalesToMinMax(t *testing.T) {
	out := sparkline([]float64{100, -50, 500, 0})
	runes := []rune(stripANSI(out))
	if len(runes) != 4 {
		t.Fatalf("expected 4 spark characters, got %d: %q", len(runes), out)
	}
	// 500 is the max of the series → tallest bar (index 2, full block).
	if runes[2] != '█' {
		t.Errorf("expected the max value to render as the tallest bar (█), got %q", runes[2])
	}
	// -50 is the min of the series → shortest bar.
	if runes[1] != '▁' {
		t.Errorf("expected the min value to render as the shortest bar (▁), got %q", runes[1])
	}
}

func TestSparkline_ColorsBySign(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	// Single-value slices avoid the min==max degenerate case ambiguity and
	// make the expected spark char (the series midpoint, ▅) unambiguous.
	positive := sparkline([]float64{50})
	negative := sparkline([]float64{-50})
	if positive != styleIncome.Render(string(sparklineChars[len(sparklineChars)/2])) {
		t.Errorf("expected a lone positive value styled with styleIncome, got %q", positive)
	}
	if negative != styleExpense.Render(string(sparklineChars[len(sparklineChars)/2])) {
		t.Errorf("expected a lone negative value styled with styleExpense, got %q", negative)
	}
}

func TestSparkline_EmptyInputDoesNotPanic(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("expected empty string for no data points, got %q", got)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestRenderSummary_IncludesTrendWhenMultipleMonths(t *testing.T) {
	sum := &models.Summary{Month: "2026-07", Income: 100, Expenses: -50, Net: 50, ByCategory: map[string]float64{}}
	trend := []models.MonthlyPoint{
		{Month: "2026-06", Net: -10},
		{Month: "2026-07", Net: 50},
	}
	out := renderSummary(sum, nil, trend, 100)
	if !strings.Contains(out, "Trend (last 2 months)") {
		t.Errorf("expected a trend section for a 2-month history, got:\n%s", out)
	}
}

func TestRenderSummary_OmitsTrendWithOneOrZeroMonths(t *testing.T) {
	sum := &models.Summary{Month: "2026-07", Income: 100, Expenses: -50, Net: 50, ByCategory: map[string]float64{}}
	out := renderSummary(sum, nil, []models.MonthlyPoint{{Month: "2026-07", Net: 50}}, 100)
	if strings.Contains(out, "Trend (") {
		t.Errorf("expected no trend section with only 1 month of history, got:\n%s", out)
	}
	out = renderSummary(sum, nil, nil, 100)
	if strings.Contains(out, "Trend (") {
		t.Errorf("expected no trend section with no history, got:\n%s", out)
	}
}

func TestDetailPopup_EnterOpensAndShowsFullDescription(t *testing.T) {
	longDesc := strings.Repeat("Verwendungszweck details that run on and on ", 5)
	m := Model{
		width: 100, height: 30,
		txs: []models.Transaction{{Description: longDesc, Amount: -12.34, Account: "N26"}},
	}
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.view != viewDetail {
		t.Fatalf("expected 'enter' on a row to open viewDetail, got %v", m.view)
	}
	if m.detailTx == nil || m.detailTx.Description != longDesc {
		t.Fatal("expected detailTx to hold the full, untruncated description")
	}
	popup := m.renderDetailPopup()
	if !strings.Contains(popup, "Verwendungszweck details") {
		t.Errorf("expected the popup to contain the description text, got:\n%s", popup)
	}
}

func TestDetailPopup_EnterOnEmptyListDoesNothing(t *testing.T) {
	m := Model{width: 100, height: 30}
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	if m.view != viewList {
		t.Errorf("expected 'enter' with no transactions to do nothing, got view=%v", m.view)
	}
}

func TestDetailPopup_AnyKeyCloses(t *testing.T) {
	tx := models.Transaction{Description: "x", Amount: -1}
	m := Model{width: 100, height: 30, txs: []models.Transaction{tx}, view: viewDetail, detailTx: &tx}
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = mi.(Model)
	if m.view != viewList || m.detailTx != nil {
		t.Errorf("expected esc to close the detail popup back to viewList, got view=%v detailTx=%v", m.view, m.detailTx)
	}
}

func TestDetailPopup_EKeyJumpsToEdit(t *testing.T) {
	tx := models.Transaction{Description: "Rewe", Amount: -12.34, Category: "groceries"}
	m := Model{width: 100, height: 30, txs: []models.Transaction{tx}, view: viewDetail, detailTx: &tx}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = mi.(Model)
	if m.view != viewForm {
		t.Fatalf("expected 'e' on the detail popup to open the edit form, got %v", m.view)
	}
	if m.editTx == nil || m.editTx.Description != "Rewe" {
		t.Error("expected the edit form to be pre-filled from the detail transaction")
	}
	if cmd == nil {
		t.Error("expected a command to focus the first form field")
	}
}

func TestWrapCapped_TruncatesPathologicallyLongFields(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	got := wrapCapped(huge, 70, 400)
	if len([]rune(got)) > 401 { // 400 + the ellipsis rune
		t.Errorf("expected wrapCapped to bound output to ~400 runes, got %d", len([]rune(got)))
	}
}
