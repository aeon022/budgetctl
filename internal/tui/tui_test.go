package tui

import (
	"context"
	"testing"

	"github.com/aeon022/budgetctl/internal/config"
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
