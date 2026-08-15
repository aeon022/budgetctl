package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aeon022/budgetctl/internal/models"
	coreai "github.com/aeon022/missionctl-core/ai"
)

// categoryExamples gives the system prompt a set of example category names
// in the target language — an instruction to "use language X" placed only
// in the user message loses to English few-shot examples sitting in the
// system prompt itself (confirmed against a local qwen2.5-coder: it kept
// answering in English despite an explicit user-message instruction, until
// the examples in the system prompt were German too). Only a couple of
// languages get real translated examples; anything else falls back to the
// English set — translating examples for every possible language up front
// would be guessing at demand that doesn't exist yet.
func categoryExamples(lang string) string {
	switch lang {
	case "de":
		return `"Lebensmittel", "Transport", "Restaurants", "Abos", "Miete", "Gesundheit", "Shopping", "Unterhaltung"`
	default:
		return `"Groceries", "Transport", "Restaurants", "Subscriptions", "Rent", "Health", "Shopping", "Entertainment"`
	}
}

func categorizeSystemPrompt(lang string) string {
	return fmt.Sprintf(
		"You are a personal finance categorizer. Assign each transaction description a short category name, "+
			"written in this language (ISO 639-1 code): %s (e.g. %s).\n\n"+
			"Return ONLY a JSON object mapping each description exactly to its category. No markdown, no explanation.",
		lang, categoryExamples(lang),
	)
}

// AICategories sends uncategorized transactions to the configured AI
// provider (Anthropic/OpenAI/Gemini/local Ollama — see
// missionctl-core/ai) and returns a map of description → category.
// Existing category names are passed as hints.
func AICategories(ctx context.Context, txs []models.Transaction, existingCategories []string) (map[string]string, error) {
	if len(txs) == 0 {
		return nil, nil
	}

	var descLines []string
	for _, tx := range txs {
		descLines = append(descLines, tx.Description)
	}

	lang := categoryLanguage()
	catsHint := ""
	if len(existingCategories) > 0 {
		catsHint = fmt.Sprintf(
			"The user already uses these categories — reuse one whenever a transaction fits, matching its exact "+
				"spelling even if it's not in %s (from earlier, inconsistent categorization). Only invent a new "+
				"category when none of these fit: %s.\n\n",
			lang, strings.Join(existingCategories, ", "),
		)
	}
	prompt := fmt.Sprintf("%sTransactions:\n%s", catsHint, strings.Join(descLines, "\n"))

	info, err := coreai.Detect("BUDGETCTL")
	if err != nil {
		return nil, err
	}

	text, err := coreai.CallJSON(ctx, info, categorizeSystemPrompt(lang), prompt)
	if err != nil {
		return nil, err
	}

	// Strip any surrounding markdown code fence the model might add
	text = strings.TrimSpace(text)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("parse AI response: %w (raw: %s)", err, text)
	}

	return result, nil
}

// categoryLanguage picks the language new AI-invented category names should
// use. Deliberately not inferred from existing category names — that just
// mirrors whatever's already in the book, which is exactly the problem this
// exists to fix: import/AI categorization run before this locale check
// existed left mostly-English categories in an otherwise German book, and
// "match the existing ones" only entrenched that majority further.
// BUDGETCTL_CATEGORY_LANG overrides for anyone whose system locale doesn't
// match the language they categorize in; falls back to $LANG/$LC_ALL
// (macOS/Linux locale strings look like "de_AT.UTF-8" — just the part
// before "_" or "." is a valid ISO 639-1 code), then "en".
func categoryLanguage() string {
	if v := os.Getenv("BUDGETCTL_CATEGORY_LANG"); v != "" {
		return v
	}
	for _, env := range []string{"LC_ALL", "LANG"} {
		v := os.Getenv(env)
		if v == "" || v == "C" || v == "POSIX" {
			continue
		}
		if i := strings.IndexAny(v, "_."); i > 0 {
			v = v[:i]
		}
		if v != "" {
			return v
		}
	}
	return "en"
}
