package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/missionctl-core/tuitest"
)

// Every non-list view shares the list's chrome: a header, a one-line footer
// (full-screen views) or a titled panel (popups), never wider than the
// terminal and never taller than it.
var secondaryViews = []struct {
	name   string
	keys   []string
	title  string // header section (full-screen) or panel title (popup)
	screen bool   // full-screen view built with ui.Frame
}{
	{"summary", []string{"s"}, "Summary", true},
	{"new form", []string{"n"}, "New Entry", true},
	{"edit form", []string{"e"}, "Edit Entry", true},
	{"help", []string{"?"}, "Help", false},
	{"detail", []string{"enter"}, "Transaction", false},
	{"category filter", []string{"f"}, "Filter by Category", false},
	{"settings", []string{"o"}, "Settings", false},
	{"profiles", []string{"p"}, "Profiles", false},
	{"import", []string{"i"}, "Import CSV", false},
}

func TestSecondaryViewsShareTheListsChrome(t *testing.T) {
	for _, v := range secondaryViews {
		for _, w := range []int{40, 60, 80, 100, 140, 170} {
			for _, h := range []int{24, 36} {
				m, _ := tuitest.Send(smokeModel(t, true), tuitest.Resize(w, h))
				m, _ = tuitest.Keys(m, v.keys...)
				text := tuitest.Text(m)
				lines := strings.Split(text, "\n")
				if len(lines) > h {
					t.Errorf("%s %dx%d: %d lines, terminal has %d", v.name, w, h, len(lines), h)
				}
				for i, l := range lines {
					if lw := lipgloss.Width(l); lw > w {
						t.Errorf("%s %dx%d: line %d is %d wide: %q", v.name, w, h, i, lw, l)
					}
				}
				if !strings.Contains(text, v.title) && w >= 60 {
					t.Errorf("%s %dx%d: missing %q:\n%s", v.name, w, h, v.title, text)
				}
				if v.screen {
					if len(lines) != h-1 {
						t.Errorf("%s %dx%d: full-screen view must fill the terminal height (%d), got %d", v.name, w, h, h-1, len(lines))
					}
					if !strings.Contains(lines[0], "budgetctl") {
						t.Errorf("%s %dx%d: header missing: %q", v.name, w, h, lines[0])
					}
					if last := lines[len(lines)-1]; !strings.Contains(last, "esc") {
						t.Errorf("%s %dx%d: footer must show esc back/cancel: %q", v.name, w, h, last)
					}
				}
			}
		}
	}
}

func TestPopupsAreTitledPanelsOverADimmedList(t *testing.T) {
	m, _ := tuitest.Send(smokeModel(t, true), tuitest.Resize(100, 30))
	m, _ = tuitest.Keys(m, "p")
	text := tuitest.Text(m)
	if !strings.Contains(text, "╭─ Profiles") {
		t.Errorf("popups carry their title in the border:\n%s", text)
	}
	if !strings.Contains(m.View().Content, "\x1b[2m") {
		t.Error("the list behind a modal popup must be dimmed")
	}
}
