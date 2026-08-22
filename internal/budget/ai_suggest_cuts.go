package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	coreai "github.com/aeon022/missionctl-core/ai"
)

// CutSuggestion is one AI-suggested budget cut for a category.
type CutSuggestion struct {
	Category       string  `json:"category"`
	CurrentMonthly float64 `json:"current_monthly"`
	SuggestedCap   float64 `json:"suggested_cap"`
	Reason         string  `json:"reason"`
}

// MaxCutSuggestions caps how many cuts the AI is asked for — a short,
// actionable list beats a suggestion for every category in the book.
const MaxCutSuggestions = 5

func suggestCutsSystemPrompt() string {
	return fmt.Sprintf(
		"You are a personal finance advisor. Given a user's spending by category this month "+
			"(optionally with a trailing average for the same category), suggest up to %d categories "+
			"where they could realistically cut back.\n\n"+
			"For each suggestion give: the exact category name as given, the current monthly spend, a "+
			"suggested new monthly cap (lower than the current spend, but realistic — not a token cut), "+
			"and a one-sentence reason. Favor discretionary categories (dining, shopping, subscriptions, "+
			"entertainment) or categories spending noticeably above their trailing average over "+
			"essentials (rent, insurance, utilities) — only suggest cutting an essential if it's "+
			"genuinely unusual. Skip categories with nothing meaningful to cut; return fewer than %d "+
			"suggestions if that's all that's warranted.\n\n"+
			"Return ONLY a JSON object (no markdown, no explanation) with one key, \"suggestions\", whose "+
			"value is an array of at most %d items, in this shape: "+
			"{\"suggestions\": [{\"category\": \"...\", \"current_monthly\": 123.45, \"suggested_cap\": 90.00, \"reason\": \"...\"}]}",
		MaxCutSuggestions, MaxCutSuggestions, MaxCutSuggestions,
	)
}

// AISuggestCuts asks the configured AI provider (see coreai.Detect;
// Anthropic/OpenAI/Gemini/local Ollama) to suggest budget cuts from this
// month's spending by category. spend maps category -> positive monthly
// spend. trailingAvg, when non-nil, gives each category's average spend
// over recent months so the AI can flag "well above your usual" rather than
// judging each category in isolation — pass nil if that data isn't
// available. Returns at most MaxCutSuggestions suggestions.
func AISuggestCuts(ctx context.Context, spend map[string]float64, trailingAvg map[string]float64) ([]CutSuggestion, error) {
	if len(spend) == 0 {
		return nil, nil
	}

	cats := make([]string, 0, len(spend))
	for c := range spend {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return spend[cats[i]] > spend[cats[j]] })

	lines := make([]string, 0, len(cats))
	for _, c := range cats {
		name := c
		if name == "" {
			name = "(uncategorized)"
		}
		line := fmt.Sprintf("%s: %.2f €", name, spend[c])
		if avg, ok := trailingAvg[c]; ok && avg > 0 {
			line += fmt.Sprintf(" (trailing average: %.2f €)", avg)
		}
		lines = append(lines, line)
	}
	prompt := "Spending by category this month:\n" + strings.Join(lines, "\n")

	info, err := coreai.Detect("BUDGETCTL")
	if err != nil {
		return nil, err
	}
	text, err := coreai.CallJSON(ctx, info, suggestCutsSystemPrompt(), prompt)
	if err != nil {
		return nil, err
	}
	return parseSuggestions(text)
}

// parseSuggestions extracts a []CutSuggestion from the raw AI response text.
// The AI is asked for {"suggestions": [...]} rather than a bare array
// because CallJSON forces response_format=json_object on the non-Anthropic
// path (OpenAI/Gemini/Ollama), and that mode requires a top-level JSON
// object — a bare array gets silently collapsed down to a single object by
// at least one local model observed in testing (qwen2.5-coder). Split out
// from AISuggestCuts so it's testable without a live AI call.
func parseSuggestions(text string) ([]CutSuggestion, error) {
	text = strings.TrimSpace(text)
	// Strip any surrounding markdown code fence the model might add anyway.
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}

	var wrapped struct {
		Suggestions []CutSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(text), &wrapped); err != nil {
		raw := text
		if len(raw) > 300 {
			raw = raw[:300] + "…"
		}
		return nil, fmt.Errorf("parse AI response: %w (raw: %s)", err, raw)
	}

	suggestions := wrapped.Suggestions
	if len(suggestions) > MaxCutSuggestions {
		suggestions = suggestions[:MaxCutSuggestions]
	}
	return suggestions, nil
}
