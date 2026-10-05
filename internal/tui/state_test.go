package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/budgetctl/internal/config"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/aeon022/budgetctl/internal/store"
)

// isolate gives a test its own HOME (config + data live under it) and a clean
// in-memory config, so nothing can reach the developer's real files.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	config.ResetForTest()
	t.Cleanup(config.ResetForTest)
	return home
}

func key(m Model, k tea.KeyPressMsg) (Model, tea.Cmd) {
	tm, cmd := m.Update(k)
	return tm.(Model), cmd
}

func TestProfilesCreateSwitchRemove(t *testing.T) {
	isolate(t)
	m := New()
	m.view = viewList

	m, _ = typeKeys(t, m, "p")
	if m.view != viewProfiles {
		t.Fatalf("p must open profiles, view=%v", m.view)
	}
	if names := profileDisplayNames(); len(names) != 1 || names[0] != "default" {
		t.Fatalf("fresh config must list only default, got %v", names)
	}

	// create "firma"; empty name is a no-op, a real name selects it
	m, _ = typeKeys(t, m, "n", "enter")
	if m.profileCreating || len(config.Profiles()) != 0 {
		t.Fatalf("empty name must create nothing (creating=%v, profiles=%v)", m.profileCreating, config.Profiles())
	}
	m, _ = typeKeys(t, m, "n", "firma", "enter")
	if !config.ProfileExists("firma") {
		t.Fatal("firma was not created")
	}
	if names := profileDisplayNames(); names[m.profilesCursor] != "firma" {
		t.Errorf("cursor must land on the new profile, names=%v cursor=%d", names, m.profilesCursor)
	}

	// duplicate creation surfaces an error and keeps the screen
	m, _ = typeKeys(t, m, "n", "firma", "enter")
	if m.profileErr == nil {
		t.Error("creating a duplicate profile must set profileErr")
	}

	// enter on firma switches, resets list state, queues a reload
	m.months, m.cursor, m.searchQ = []string{"2026-01"}, 3, "x"
	var cmd tea.Cmd
	m, cmd = typeKeys(t, m, "enter")
	if config.ActiveProfile() != "firma" || m.view != viewList || cmd == nil {
		t.Fatalf("switch failed: active=%q view=%v cmd=%v", config.ActiveProfile(), m.view, cmd != nil)
	}
	if m.months != nil || m.cursor != 0 || m.searchQ != "" || m.activeAccount != -1 {
		t.Errorf("switching must reset list state: %+v", m)
	}

	// default can never be removed; firma needs a y
	m, _ = typeKeys(t, m, "p")
	m, _ = typeKeys(t, m, "k", "d") // cursor to default
	if m.profileRemoving != "" {
		t.Error("default must not be removable")
	}
	m, _ = typeKeys(t, m, "j", "d", "n") // any key but y cancels
	if m.profileRemoving != "" || !config.ProfileExists("firma") {
		t.Error("non-y must cancel removal")
	}
	m, _ = typeKeys(t, m, "d", "y")
	if config.ProfileExists("firma") || config.ActiveProfile() != "" {
		t.Errorf("firma should be gone and active cleared: exists=%v active=%q", config.ProfileExists("firma"), config.ActiveProfile())
	}
}

func TestSettingsConfirmFlow(t *testing.T) {
	isolate(t)
	m := New()
	m.view = viewList

	m, _ = typeKeys(t, m, "o")
	if m.view != viewSettings {
		t.Fatalf("o must open settings, view=%v", m.view)
	}
	// r (reset) is only meaningful when a data_dir is configured
	m, _ = typeKeys(t, m, "r")
	if m.settingsConfirming {
		t.Error("r must do nothing without a configured data_dir")
	}

	m = m.confirmDataDir("/some/where")
	if !m.settingsConfirming || m.settingsPendingDir != "/some/where" {
		t.Fatal("confirmDataDir must stage the change")
	}
	// anything except y cancels and applies nothing
	var cmd tea.Cmd
	m, cmd = typeKeys(t, m, "n")
	if m.settingsConfirming || cmd != nil || config.Shared() {
		t.Errorf("non-y must cancel without applying (confirming=%v cmd=%v shared=%v)", m.settingsConfirming, cmd != nil, config.Shared())
	}
	// y returns the apply command
	m = m.confirmDataDir(t.TempDir())
	if _, cmd = typeKeys(t, m, "y"); cmd == nil {
		t.Error("y must return the apply command")
	}
	// esc leaves the screen
	m.settingsConfirming = false
	m, _ = typeKeys(t, m, "esc")
	if m.view != viewList {
		t.Errorf("esc must leave settings, view=%v", m.view)
	}
}

func TestApplyDataDirCmd(t *testing.T) {
	isolate(t)

	oldPath := config.DBPath()
	if err := os.WriteFile(oldPath, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	newDir := t.TempDir()

	msg := applyDataDirCmd(oldPath, newDir)().(settingsAppliedMsg)
	if msg.err != nil || !strings.Contains(msg.status, "Moved") {
		t.Fatalf("expected move, got %+v", msg)
	}
	if _, err := os.Stat(filepath.Join(newDir, "budget.db")); err != nil {
		t.Errorf("db not moved to new dir: %v", err)
	}
	if _, err := os.Stat(oldPath); err == nil {
		t.Error("old db must be gone after the move")
	}
	if !config.Shared() {
		t.Error("data_dir must be persisted")
	}

	// pointing at a dir that already holds a database never overwrites it
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "budget.db"), []byte("theirs"), 0o600)
	oldPath = config.DBPath()
	os.WriteFile(oldPath, []byte("mine"), 0o600)
	msg = applyDataDirCmd(oldPath, other)().(settingsAppliedMsg)
	if !strings.Contains(msg.status, "existing database") {
		t.Errorf("status = %q", msg.status)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "budget.db")); string(b) != "theirs" {
		t.Errorf("existing db was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(oldPath); string(b) != "mine" {
		t.Error("previous local db must stay untouched")
	}

	// reset to local default
	if msg = applyDataDirCmd(config.DBPath(), "")().(settingsAppliedMsg); !strings.Contains(msg.status, "local database") || config.Shared() {
		t.Errorf("reset: %+v shared=%v", msg, config.Shared())
	}
}

func TestSummaryGoalInputValidation(t *testing.T) {
	isolate(t)
	config.Set("db_path", filepath.Join(t.TempDir(), "budget.db"))
	m := New()
	m.view = viewSummary

	cases := []struct {
		input   string
		wantErr string
	}{
		{"Dining", "expected"},
		{"Dining abc", "positive number"},
		{"Dining -5", "positive number"},
		{"Dining 0", "positive number"},
	}
	for _, c := range cases {
		m, _ = typeKeys(t, m, "g")
		if !m.settingGoal {
			t.Fatal("g must start goal input")
		}
		var cmd tea.Cmd
		m, cmd = typeKeys(t, m, c.input, "enter")
		if cmd != nil || m.err == nil || !strings.Contains(m.err.Error(), c.wantErr) {
			t.Errorf("%q: err=%v cmd=%v, want error containing %q", c.input, m.err, cmd != nil, c.wantErr)
		}
		m.err = nil
	}

	// multi-word category + valid amount persists a goal
	m, _ = typeKeys(t, m, "g")
	m, cmd := typeKeys(t, m, "Eating Out 200", "enter")
	if cmd == nil {
		t.Fatalf("valid goal must return a command, err=%v", m.err)
	}
	feed(t, m, cmd)
	s, err := store.New(config.DBPath(), config.Shared())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	goals, _ := s.ListGoals(context.Background())
	if len(goals) != 1 || !strings.EqualFold(goals[0].Category, "Eating Out") || goals[0].Monthly != 200 {
		t.Errorf("goals = %+v", goals)
	}

	// esc cancels without a command
	m, _ = typeKeys(t, m, "g")
	if m, cmd = typeKeys(t, m, "esc"); m.settingGoal || cmd != nil {
		t.Error("esc must cancel goal input")
	}
}

func TestSummaryNavigation(t *testing.T) {
	isolate(t)
	m := New()
	m.view = viewSummary
	m.months = []string{"2026-03", "2026-02", "2025-12", "2025-11"}
	m.accounts = []string{"N26", "ING"}
	m.activeAccount = -1

	m, cmd := typeKeys(t, m, "tab")
	if m.activeTab != 1 || cmd == nil {
		t.Errorf("tab: activeTab=%d cmd=%v", m.activeTab, cmd != nil)
	}
	m.activeTab = 0
	m, _ = key(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.activeTab != len(m.months)-1 {
		t.Errorf("shift+tab must wrap to last month, got %d", m.activeTab)
	}

	m.activeTab = 0
	m, _ = typeKeys(t, m, "y") // already in the newest year: stays
	if m.months[m.activeTab] != "2026-03" {
		t.Errorf("y in newest year → %s, want unchanged 2026-03", m.months[m.activeTab])
	}
	m, _ = typeKeys(t, m, "Y") // previous year: its newest month
	if m.months[m.activeTab] != "2025-12" {
		t.Errorf("Y → %s, want 2025-12", m.months[m.activeTab])
	}
	m, _ = typeKeys(t, m, "y")
	if m.months[m.activeTab] != "2026-02" { // forward lands on the EARLIEST month of the next year
		t.Errorf("y → %s, want 2026-02", m.months[m.activeTab])
	}

	m, _ = typeKeys(t, m, "]")
	if m.activeAccount != 0 {
		t.Errorf("] → %d, want 0", m.activeAccount)
	}
	m, _ = typeKeys(t, m, "[", "[")
	if m.activeAccount != len(m.accounts)-1 && m.activeAccount != -1 {
		t.Errorf("[ cycling out of range: %d", m.activeAccount)
	}

	// t without the Bundle license explains instead of calling the AI
	m, cmd = typeKeys(t, m, "t")
	if m.view != viewSummary || cmd != nil || m.translateLoading {
		t.Errorf("unlicensed t must not start translation (view=%v loading=%v)", m.view, m.translateLoading)
	}
	m, _ = typeKeys(t, m, "q")
	if m.view != viewList {
		t.Errorf("q must return to list, view=%v", m.view)
	}
}

func TestCategoryTranslateSelectionAndApply(t *testing.T) {
	isolate(t)
	config.Set("db_path", filepath.Join(t.TempDir(), "budget.db"))
	s, _ := store.New(config.DBPath(), config.Shared())
	ctx := context.Background()
	for i, c := range []string{"Groceries", "Rent", "Fun"} {
		s.Upsert(ctx, &models.Transaction{ID: string(rune('a' + i)), Date: time.Now(), Description: c, Amount: -1, Category: c, Account: "N26"})
	}
	s.Close()

	m := New()
	m.view = viewCategoryTranslate
	m.translateSuggestions = []categoryRename{{Old: "Groceries", New: "Lebensmittel"}, {Old: "Rent", New: "Miete"}}
	m.translateSelected = map[int]bool{0: true, 1: true}

	m, _ = typeKeys(t, m, "down", "j", "j") // clamps at last
	if m.translateCursor != 1 {
		t.Errorf("cursor = %d, want clamp at 1", m.translateCursor)
	}
	m, _ = key(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.translateSelected[1] {
		t.Error("space must deselect the row under the cursor")
	}
	m, _ = typeKeys(t, m, "A")
	if !m.translateSelected[0] || !m.translateSelected[1] {
		t.Error("A must select all")
	}
	m, _ = key(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}) // deselect Rent again
	m, _ = typeKeys(t, m, "k")
	var cmd tea.Cmd
	m, cmd = typeKeys(t, m, "enter")
	if cmd == nil {
		t.Fatal("enter with a selection must apply")
	}
	feed(t, m, cmd)

	s, _ = store.New(config.DBPath(), config.Shared())
	defer s.Close()
	cats, _ := s.ListCategories(ctx)
	got := strings.Join(cats, ",")
	if !strings.Contains(got, "Lebensmittel") || !strings.Contains(got, "Rent") || strings.Contains(got, "Groceries") {
		t.Errorf("only the selected rename may apply, categories = %v", cats)
	}

	// enter with nothing selected just closes
	m.view = viewCategoryTranslate
	m.translateSelected = map[int]bool{}
	if m, cmd = typeKeys(t, m, "enter"); cmd != nil || m.view != viewSummary {
		t.Errorf("empty selection: view=%v cmd=%v", m.view, cmd != nil)
	}
}

func TestSplitCategories(t *testing.T) {
	got := splitCategories(" Auto ;; Business ; ")
	if len(got) != 2 || got[0] != "Auto" || got[1] != "Business" {
		t.Errorf("splitCategories = %q", got)
	}
	if got := splitCategories(" ; "); len(got) != 0 {
		t.Errorf("only separators must yield nothing, got %q", got)
	}
}

func TestBatchAndSplitCategoryCmds(t *testing.T) {
	isolate(t)
	config.Set("db_path", filepath.Join(t.TempDir(), "budget.db"))
	ctx := context.Background()
	s, _ := store.New(config.DBPath(), config.Shared())
	d := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	s.Upsert(ctx, &models.Transaction{ID: "1", Date: d, Description: "REWE", Amount: -10, Account: "N26"})
	s.Upsert(ctx, &models.Transaction{ID: "2", Date: d, Description: "EDEKA", Amount: -20, Account: "N26"})
	s.Close()

	if msg := batchSetCategoryCmd([]string{"1", "2"}, "Food")().(txSavedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg := categorizeCmd([]string{"1"}, "Groceries", "REWE")().(txSavedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	s, _ = store.New(config.DBPath(), config.Shared())
	defer s.Close()
	txs, _ := s.List(ctx, store.Filter{})
	cat := map[string]string{}
	for _, tx := range txs {
		cat[tx.ID] = tx.Category
	}
	if cat["1"] != "Groceries" || cat["2"] != "Food" {
		t.Errorf("categories = %v (categorizeCmd must change only its ids)", cat)
	}
	if rules, _ := s.ListRules(ctx); len(rules) != 1 || !strings.EqualFold(rules[0].Pattern, "REWE") {
		t.Errorf("rule not saved: %+v", rules)
	}
}
