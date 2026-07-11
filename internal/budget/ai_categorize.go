package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/aeon022/budgetctl/internal/models"
)

// AICategories sends uncategorized transactions to Claude Haiku and returns
// a map of description → category. Existing category names are passed as hints.
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
			"\nPrefer these known categories where they fit (create new ones if needed): %s.",
			strings.Join(existingCategories, ", "),
		)
	}

	prompt := fmt.Sprintf(`You are a personal finance categorizer. Assign each transaction description below a short category name (e.g. "Groceries", "Transport", "Restaurants", "Subscriptions", "Rent", "Health", "Shopping", "Entertainment").%s

Return ONLY a JSON object mapping each description exactly to its category. No markdown, no explanation.

Transactions:
%s`, catsHint, strings.Join(descLines, "\n"))

	client := anthropic.NewClient()

	response, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.ModelClaudeHaiku4_5,
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("claude api: %w", err)
	}

	var text string
	for _, block := range response.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			text = v.Text
		}
	}

	// Strip any surrounding markdown code fence Claude might add
	text = strings.TrimSpace(text)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}

	var result map[string]string
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("parse claude response: %w (raw: %s)", err, text)
	}

	return result, nil
}
