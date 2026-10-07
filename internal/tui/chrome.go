package tui

import (
	"strings"
	"time"

	"github.com/aeon022/missionctl-core/statusbar"
	"github.com/aeon022/missionctl-core/ui"
)

// Shared chrome for every non-list view (summary, form, popups), so they look
// like the main list: ui.Header + divider, a one-line statusbar footer and
// titled panels for popups.

// chromeHead is the standard header: title bar, divider and — in the tall
// tier, like the list — a blank line.
func (m Model) chromeHead(section, context string) []string {
	title := ui.Header(m.width, styleHeader.Render("budgetctl")+styleMuted.Render(" · "+section), context,
		styleMuted.Render(time.Now().Format("Mon 02 Jan")))
	head := []string{title, styleDivider.Render(strings.Repeat("─", m.width))}
	if m.spacious() {
		head = append(head, "")
	}
	return head
}

// chromeFoot is the one-line footer: hints in priority order on the left
// (the last ones are dropped first when narrow), status on the right.
func (m Model) chromeFoot(right string, hints ...[2]string) string {
	rw := 0
	if right != "" {
		rw = len([]rune(plain(right))) + 2
	}
	return statusbar.Line(m.width, "  "+statusbar.Hints(max(m.width-rw-2, 1), hints...), right)
}

// popupBox draws a modal popup as a titled panel (title in the border). Height
// follows the content (plus one blank row above and below); content is
// truncated to the panel width.
func popupBox(title string, w int, body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	lines = append([]string{""}, append(lines, "")...)
	return ui.Panel(w, len(lines)+2, title, strings.Join(lines, "\n"), true)
}
