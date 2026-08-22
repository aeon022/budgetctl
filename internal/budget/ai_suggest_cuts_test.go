package budget

import "testing"

func TestParseSuggestions(t *testing.T) {
	t.Run("wrapped object, plain", func(t *testing.T) {
		got, err := parseSuggestions(`{"suggestions": [{"category": "Dining", "current_monthly": 300, "suggested_cap": 200, "reason": "too much takeout"}]}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Category != "Dining" || got[0].SuggestedCap != 200 {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("markdown fence stripped", func(t *testing.T) {
		got, err := parseSuggestions("```json\n{\"suggestions\": [{\"category\": \"Netflix\", \"current_monthly\": 15, \"suggested_cap\": 0, \"reason\": \"unused\"}]}\n```")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Category != "Netflix" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("caps at MaxCutSuggestions", func(t *testing.T) {
		text := `{"suggestions": [`
		for i := 0; i < MaxCutSuggestions+3; i++ {
			if i > 0 {
				text += ","
			}
			text += `{"category": "c", "current_monthly": 1, "suggested_cap": 0, "reason": "r"}`
		}
		text += `]}`
		got, err := parseSuggestions(text)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != MaxCutSuggestions {
			t.Errorf("got %d suggestions, want %d", len(got), MaxCutSuggestions)
		}
	})

	t.Run("bare single object without suggestions key yields no error but no suggestions", func(t *testing.T) {
		// A weaker local model has been observed collapsing a requested array
		// down to a single bare object when response_format=json_object is
		// forced — this shouldn't be silently mistaken for zero suggestions
		// worth surfacing, but it also shouldn't crash the caller.
		got, err := parseSuggestions(`{"category": "x", "current_monthly": 1, "suggested_cap": 0, "reason": "r"}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("got %+v, want none", got)
		}
	})

	t.Run("prose response with no JSON errors with truncated raw text", func(t *testing.T) {
		prose := "Here are my thoughts on your spending. " + string(make([]byte, 500))
		_, err := parseSuggestions(prose)
		if err == nil {
			t.Fatal("expected an error")
		}
		if len(err.Error()) > 400 {
			t.Errorf("error message not truncated, length %d", len(err.Error()))
		}
	})
}
