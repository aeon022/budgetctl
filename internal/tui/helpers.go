package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeon022/budgetctl/internal/models"
	"github.com/sahilm/fuzzy"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func (m *Model) setStatus(s string) {
	m.status = s
	m.statusTime = time.Now()
}

// payeeColW is the fixed display width of the Payee column in formatTxRow.
// doubleClickWindow opens the detail popup on a second click within this
// window, same pattern and duration taskctl uses for its own double-click.
const doubleClickWindow = 400 * time.Millisecond

// undoWindow is how long after a delete "u" still restores it — same
// duration taskctl uses for its own delete-undo.
const undoWindow = 5 * time.Second

const payeeColW = 20

func formatTxRow(t *models.Transaction, width int, query string) string {
	amtStr := fmt.Sprintf("%+8.2f€", t.Amount)
	amtStyled := ""
	if t.Amount >= 0 {
		amtStyled = styleIncome.Render(amtStr)
	} else {
		amtStyled = styleExpense.Render(amtStr)
	}

	cat := t.Category
	if cat == "" {
		cat = "—"
	}
	// padRunes, not fmt's "%-*s" — that pads by BYTE length, which
	// misaligns columns the moment a category/payee contains a multi-byte
	// rune (umlauts are routine in German bank text: ä/ö/ü/ß).
	catStyled := styleCategory.Render(padRunes(truncRunes(cat, 16), 16))

	dateStr := t.Date.Format("2006-01-02")
	dateStyled := coloredDate(dateStr, t.Date)

	// Truncate the PLAIN string first, then highlight the truncated
	// result, then pad via an ANSI-aware lipgloss.Width() wrap — padRunes
	// counts runes naively and would miscount escape-code bytes as
	// "runes" if applied to already-highlighted (ANSI-embedded) text.
	payee := t.Payee
	if payee == "" {
		payee = "—"
	}
	payeeMatchIdx := fuzzyMatchIndexes(query, t.Payee)
	payeeStyled := lipgloss.NewStyle().Width(payeeColW).
		Render(highlightMatches(truncRunes(payee, payeeColW), payeeMatchIdx, stylePayee))

	// purpose (Description) fills whatever's left — truncated by RUNE, not
	// byte: German bank text is full of multi-byte umlauts (ä/ö/ü/ß), and
	// byte-slicing mid-rune corrupts the output.
	purposeW := width - 12 - 10 - 18 - (payeeColW + 2) - 4
	if purposeW < 10 {
		purposeW = 10
	}
	descMatchIdx := fuzzyMatchIndexes(query, t.Description)
	purpose := highlightMatches(truncRunes(t.Description, purposeW), descMatchIdx, lipgloss.NewStyle())

	return fmt.Sprintf("%s  %s  %s  %s  %s",
		dateStyled,
		amtStyled,
		catStyled,
		payeeStyled,
		purpose,
	)
}

// truncRunes truncates s to at most n runes, appending "…" if it had to cut
// (the ellipsis itself counts toward n). Rune-safe, unlike raw byte slicing.
// filterTxs fuzzy-matches q against each transaction's payee OR
// description (github.com/sahilm/fuzzy), keeping a transaction if either
// matches. Unlike habctl's filterHabits, this does NOT re-rank by match
// quality — transactions are naturally date-ordered, and re-sorting by
// fuzzy score would scramble that chronological order (same reasoning as
// taskctl/calctl's list/day grouping preservation).
func filterTxs(txs []models.Transaction, q string) []models.Transaction {
	q = strings.TrimSpace(q)
	if q == "" {
		return txs
	}
	payees := make([]string, len(txs))
	descs := make([]string, len(txs))
	for i, t := range txs {
		payees[i] = t.Payee
		descs[i] = t.Description
	}
	matched := make(map[int]bool, len(txs))
	for _, mt := range fuzzy.Find(q, payees) {
		matched[mt.Index] = true
	}
	for _, mt := range fuzzy.Find(q, descs) {
		matched[mt.Index] = true
	}
	out := make([]models.Transaction, 0, len(matched))
	for i, t := range txs {
		if matched[i] {
			out = append(out, t)
		}
	}
	return out
}

// fuzzyMatchIndexes returns the rune indexes within s that q fuzzy-matched,
// or nil if q is empty or doesn't match at all.
func fuzzyMatchIndexes(q, s string) []int {
	if q == "" {
		return nil
	}
	matches := fuzzy.Find(q, []string{s})
	if len(matches) == 0 {
		return nil
	}
	return matches[0].MatchedIndexes
}

// highlightMatches renders s with the rune positions in idxs (from
// fuzzyMatchIndexes) styled via a warm, underlined variant of base, and
// every other character via base itself — fzf-style match highlighting.
//
// Renders one character at a time rather than nesting a highlighted span
// inside a single outer Render() call: lipgloss's Render() ends every
// string with a full SGR reset, so an inner Render() call's reset would
// wipe out the outer style for everything after the first highlighted
// character. Per-character rendering keeps every segment self-contained.
//
// idxs are indexes into s BEFORE any truncation — callers must resolve
// indexes against the same, untruncated string used to compute them.
func highlightMatches(s string, idxs []int, base lipgloss.Style) string {
	if len(idxs) == 0 {
		return base.Render(s)
	}
	hi := base.Foreground(colorAmber).Underline(true)
	matchSet := make(map[int]bool, len(idxs))
	for _, i := range idxs {
		matchSet[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if matchSet[i] {
			b.WriteString(hi.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// padRunes right-pads s with spaces to n runes. Assumes s already fits
// within n runes (callers truncate first); no-ops otherwise.
func padRunes(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(r))
}

func coloredDate(s string, t time.Time) string {
	now := time.Now()
	switch {
	case sameDay(t, now):
		return styleToday.Render(s)
	case t.After(now.AddDate(0, 0, -7)):
		return styleDateWeek.Render(s)
	case t.After(now.AddDate(0, 0, -30)):
		return styleDateMonth.Render(s)
	default:
		return styleDateOld.Render(s)
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// sparklineChars are the 8 block-height levels used by sparkline, low to high.
var sparklineChars = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a compact one-line bar chart, one character
// per value, height-scaled to the min/max of the series and colored green
// (positive) or red (negative). Each character is rendered with its own
// merged style rather than wrapping the whole line in one Render() call —
// nesting styled Render() output inside another Render() call silently
// resets everything after the inner segment (every Render() call ends with
// a full SGR reset), a bug found and fixed the hard way in habctl earlier.
func sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	minV, maxV := values[0], values[0]
	for _, v := range values {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	rangeV := maxV - minV

	var b strings.Builder
	for _, v := range values {
		idx := len(sparklineChars) / 2
		if rangeV > 0 {
			idx = int((v - minV) / rangeV * float64(len(sparklineChars)-1))
		}
		style := styleIncome
		if v < 0 {
			style = styleExpense
		}
		b.WriteString(style.Render(string(sparklineChars[idx])))
	}
	return b.String()
}
