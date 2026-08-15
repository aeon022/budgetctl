package budget

import "testing"

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
