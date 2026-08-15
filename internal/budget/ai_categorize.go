package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aeon022/budgetctl/internal/models"
	coreai "github.com/aeon022/missionctl-core/ai"
)

const categorizeSystemPrompt = `You are a personal finance categorizer. Assign each transaction description a short category name (e.g. "Groceries", "Transport", "Restaurants", "Subscriptions", "Rent", "Health", "Shopping", "Entertainment").

Return ONLY a JSON object mapping each description exactly to its category. No markdown, no explanation.`

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

	catsHint := ""
	if len(existingCategories) > 0 {
		catsHint = fmt.Sprintf(
			"Prefer these known categories where they fit (create new ones if needed): %s.\n\n",
			strings.Join(existingCategories, ", "),
		)
	}
	prompt := fmt.Sprintf("%sTransactions:\n%s", catsHint, strings.Join(descLines, "\n"))

	info, err := coreai.Detect("BUDGETCTL")
	if err != nil {
		return nil, err
	}

	text, err := coreai.CallJSON(ctx, info, categorizeSystemPrompt, prompt)
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
