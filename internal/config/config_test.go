package config

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// resetViper gives each test a clean, isolated HOME and viper state — the
// package's functions all read/write through the global viper instance.
func resetViper(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Reset()
	return home
}

func TestDBPathDefaultNoProfile(t *testing.T) {
	home := resetViper(t)
	want := filepath.Join(home, ".local", "share", "budgetctl", "budget.db")
	if got := DBPath(); got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
	if Shared() {
		t.Error("Shared() = true, want false for unscoped default")
	}
}

func TestAddProfileAndSwitch(t *testing.T) {
	home := resetViper(t)

	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if !ProfileExists("firma") {
		t.Fatal("ProfileExists(firma) = false after AddProfile")
	}
	if got, want := Profiles(), []string{"firma"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Profiles() = %v, want %v", got, want)
	}

	// Not yet active: DBPath still resolves to the unscoped default.
	defaultPath := DBPath()
	wantDefault := filepath.Join(home, ".local", "share", "budgetctl", "budget.db")
	if defaultPath != wantDefault {
		t.Errorf("DBPath() before switch = %q, want %q", defaultPath, wantDefault)
	}

	if err := SetActiveProfile("firma"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}
	if ActiveProfile() != "firma" {
		t.Errorf("ActiveProfile() = %q, want firma", ActiveProfile())
	}
	wantProfile := filepath.Join(home, ".local", "share", "budgetctl", "profiles", "firma", "budget.db")
	if got := DBPath(); got != wantProfile {
		t.Errorf("DBPath() with active profile = %q, want %q", got, wantProfile)
	}
	if got := defaultPath; got == wantProfile {
		t.Error("profile DBPath should differ from the default DBPath")
	}
	if Shared() {
		t.Error("Shared() = true for a profile with no data_dir override")
	}

	// Clearing back to "" restores the unscoped default.
	if err := SetActiveProfile(""); err != nil {
		t.Fatalf("SetActiveProfile(\"\"): %v", err)
	}
	if got := DBPath(); got != wantDefault {
		t.Errorf("DBPath() after clearing profile = %q, want %q", got, wantDefault)
	}
}

func TestAddProfileWithDataDir(t *testing.T) {
	home := resetViper(t)
	synced := filepath.Join(home, "Sync", "firma")

	if err := AddProfile("firma", synced); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := SetActiveProfile("firma"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}
	want := filepath.Join(synced, "budget.db")
	if got := DBPath(); got != want {
		t.Errorf("DBPath() = %q, want %q", got, want)
	}
	if !Shared() {
		t.Error("Shared() = false for a profile with an explicit data_dir")
	}
}

func TestAddProfileRejectsBadNames(t *testing.T) {
	resetViper(t)
	for _, name := range []string{"", "default", "a/b", "a.b", "a\\b"} {
		if err := AddProfile(name, ""); err == nil {
			t.Errorf("AddProfile(%q) = nil error, want an error", name)
		}
	}
}

func TestAddProfileRejectsDuplicate(t *testing.T) {
	resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := AddProfile("firma", ""); err == nil {
		t.Error("AddProfile duplicate name = nil error, want an error")
	}
}

func TestSetActiveProfileRequiresExisting(t *testing.T) {
	resetViper(t)
	if err := SetActiveProfile("ghost"); err == nil {
		t.Error("SetActiveProfile(unknown) = nil error, want an error")
	}
}

func TestRemoveProfile(t *testing.T) {
	resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := SetActiveProfile("firma"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}

	if err := RemoveProfile("firma"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if ProfileExists("firma") {
		t.Error("ProfileExists(firma) = true after RemoveProfile")
	}
	if ActiveProfile() != "" {
		t.Errorf("ActiveProfile() = %q after removing the active profile, want \"\"", ActiveProfile())
	}

	if err := RemoveProfile("firma"); err == nil {
		t.Error("RemoveProfile(unknown) = nil error, want an error")
	}
}
