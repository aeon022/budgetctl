package tui

import (
	"testing"

	"github.com/aeon022/missionctl-core/theme"
	"github.com/aeon022/missionctl-core/tuitest"
)

func TestThemeKeySwitchesLiveAndSaves(t *testing.T) {
	isolate(t)
	orig := theme.Current()
	t.Cleanup(func() { _ = theme.Apply(orig) })

	m := New()
	m.view = viewList
	before := styleHeader.GetForeground()
	mm, _ := tuitest.Send(m, tuitest.Key("T"))

	if theme.Current() == orig {
		t.Fatalf("T must switch to the next theme, still %q", orig)
	}
	if styleHeader.GetForeground() == before {
		t.Error("cached styles must be rebuilt when the theme changes")
	}
	if got := mm.(Model).status; got != "Theme: "+theme.Current() {
		t.Errorf("a short confirmation should name the theme, got %q", got)
	}
}
