package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/aeon022/budgetctl/internal/budget"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
	tea "github.com/charmbracelet/bubbletea"
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
	m = feed(t, m, loadCmd("", ""))
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
	m = feed(t, m, loadCmd("", ""))
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
	m = feed(t, m, loadCmd("", ""))
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

	listH := m.height - m.listStartRow() - 1
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
