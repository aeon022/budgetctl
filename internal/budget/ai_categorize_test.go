package budget

import "testing"

func TestParseCategoryResponse(t *testing.T) {
	t.Run("plain flat map", func(t *testing.T) {
		got, err := parseCategoryResponse(`{"REWE Graz": "Lebensmittel", "Netflix": "Abos"}`)
		if err != nil {
			t.Fatal(err)
		}
		if got["REWE Graz"] != "Lebensmittel" || got["Netflix"] != "Abos" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("markdown fence stripped", func(t *testing.T) {
		got, err := parseCategoryResponse("```json\n{\"Netflix\": \"Abos\"}\n```")
		if err != nil {
			t.Fatal(err)
		}
		if got["Netflix"] != "Abos" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("non-string values skipped, valid ones kept", func(t *testing.T) {
		// A model hallucinating a structured transaction schema instead of
		// the requested flat map for one entry shouldn't discard the rest.
		got, err := parseCategoryResponse(`{
			"Netflix": "Abos",
			"LIEFERSE AMSTERDAM": {"date": "123", "merchant": "Lieferando", "amount": null}
		}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got["Netflix"] != "Abos" {
			t.Errorf("got %+v, want only Netflix kept", got)
		}
	})

	t.Run("entirely wrong schema errors instead of returning empty", func(t *testing.T) {
		_, err := parseCategoryResponse(`{"transactions": [{"date": "123", "merchant": "x"}]}`)
		if err == nil {
			t.Fatal("expected an error when nothing in the response matches the expected shape")
		}
	})

	t.Run("prose response with no JSON at all errors with truncated raw text", func(t *testing.T) {
		prose := "The text appears to be a list of transactions. " + string(make([]byte, 500))
		_, err := parseCategoryResponse(prose)
		if err == nil {
			t.Fatal("expected an error")
		}
		if len(err.Error()) > 400 {
			t.Errorf("error message not truncated, length %d", len(err.Error()))
		}
	})
}

func TestCategoryLanguage(t *testing.T) {
	tests := []struct {
		name         string
		override     string
		lcAll        string
		lang         string
		wantLanguage string
	}{
		{"explicit override wins", "fr", "de_AT.UTF-8", "en_US.UTF-8", "fr"},
		{"LC_ALL over LANG", "", "de_AT.UTF-8", "en_US.UTF-8", "de"},
		{"LANG fallback", "", "", "en_US.UTF-8", "en"},
		{"dot-only locale", "", "", "de.UTF-8", "de"},
		{"POSIX ignored, falls to en", "", "POSIX", "", "en"},
		{"nothing set falls to en", "", "", "", "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BUDGETCTL_CATEGORY_LANG", tt.override)
			t.Setenv("LC_ALL", tt.lcAll)
			t.Setenv("LANG", tt.lang)
			if got := categoryLanguage(); got != tt.wantLanguage {
				t.Errorf("categoryLanguage() = %q, want %q", got, tt.wantLanguage)
			}
		})
	}
}
