package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeon022/budgetctl/internal/models"
)

func focus(m Model) (Model, tea.Cmd) {
	tm, cmd := m.Update(tea.FocusMsg{})
	return tm.(Model), cmd
}

func TestFocusReloadsOnlyWhenBrowsingAndStale(t *testing.T) {
	isolate(t)
	stale := time.Now().Add(-time.Minute)

	m := New()
	m.view, m.lastLoad = viewList, stale
	m, cmd := focus(m)
	if cmd == nil {
		t.Fatal("focus while browsing with stale data must reload")
	}
	if time.Since(m.lastLoad) > time.Second {
		t.Error("issuing a reload must restamp lastLoad so a focus flicker can't fire twice")
	}
	if _, cmd = focus(m); cmd != nil {
		t.Error("focus right after a reload must not reload again")
	}

	// the summary screen is browsing too
	m = New()
	m.view, m.lastLoad = viewSummary, stale
	if _, cmd = focus(m); cmd == nil {
		t.Error("summary view should reload on focus")
	}
}

func TestFocusNeverDisturbsInput(t *testing.T) {
	isolate(t)
	stale := time.Now().Add(-time.Minute)
	tx := &models.Transaction{ID: "1"}
	cases := map[string]func(*Model){
		"form":           func(m *Model) { m.view = viewForm },
		"import":         func(m *Model) { m.view = viewImport },
		"settings":       func(m *Model) { m.view = viewSettings },
		"profiles":       func(m *Model) { m.view = viewProfiles },
		"help":           func(m *Model) { m.view = viewHelp },
		"category pick":  func(m *Model) { m.view = viewCategoryPick },
		"search":         func(m *Model) { m.searching = true },
		"palette":        func(m *Model) { m.inPalette = true },
		"categorizing":   func(m *Model) { m.categorizing = true },
		"save rule":      func(m *Model) { m.savingRule = true },
		"goal input":     func(m *Model) { m.view = viewSummary; m.settingGoal = true },
		"batch select":   func(m *Model) { m.selecting = true },
		"delete confirm": func(m *Model) { m.deleteTarget = tx },
		"detail popup":   func(m *Model) { m.detailTx = tx },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			m := New()
			m.view, m.lastLoad = viewList, stale
			set(&m)
			if _, cmd := focus(m); cmd != nil {
				t.Errorf("%s: focus must not trigger a reload", name)
			}
		})
	}
}

func TestViewReportsFocus(t *testing.T) {
	isolate(t)
	if !New().View().ReportFocus {
		t.Error("View must set ReportFocus or FocusMsg never arrives")
	}
}
