package config

import (
	"os"
	"path/filepath"
	"strings"
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

// TestDefaultDBPathIgnoresActiveProfile guards against a regression where
// `budgetctl profile list`'s "default" row showed the active profile's own
// path instead of the true unscoped default — because it read DBPath(),
// which always follows ActiveProfile(). DefaultDBPath/DefaultShared must
// stay stable regardless of which profile (if any) is active.
func TestDefaultDBPathIgnoresActiveProfile(t *testing.T) {
	home := resetViper(t)
	wantDefault := filepath.Join(home, ".local", "share", "budgetctl", "budget.db")

	if got := DefaultDBPath(); got != wantDefault {
		t.Errorf("DefaultDBPath() before any profile = %q, want %q", got, wantDefault)
	}

	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := SetActiveProfile("firma"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}

	if got := DefaultDBPath(); got != wantDefault {
		t.Errorf("DefaultDBPath() with firma active = %q, want %q (must stay the unscoped default)", got, wantDefault)
	}
	if got := DBPath(); got == wantDefault {
		t.Error("DBPath() with firma active should NOT equal the unscoped default path")
	}
	if DefaultShared() {
		t.Error("DefaultShared() = true, want false (no top-level data_dir set)")
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

// TestSetSessionProfileDoesNotPersist covers the --profile flag's use
// case: a one-off override that must NOT survive into the next process
// (unlike SetActiveProfile, which writes to config).
func TestSetSessionProfileDoesNotPersist(t *testing.T) {
	home := resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}

	cfgFile := filepath.Join(home, ".config", "budgetctl", "budgetctl.yaml")
	before, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("reading config file after AddProfile: %v", err)
	}

	if err := SetSessionProfile("firma"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	if ActiveProfile() != "firma" {
		t.Errorf("ActiveProfile() = %q after SetSessionProfile, want firma", ActiveProfile())
	}

	// SetSessionProfile must not touch the config file on disk — a fresh
	// process reading it later must not see firma as the active profile.
	after, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("reading config file after SetSessionProfile: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("config file changed after SetSessionProfile:\nbefore: %s\nafter:  %s", before, after)
	}
	if strings.Contains(string(after), "active_profile: firma") {
		t.Error("config file contains active_profile: firma — SetSessionProfile must not persist")
	}
}

func TestSetSessionProfileDefaultClears(t *testing.T) {
	resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := SetActiveProfile("firma"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}
	if err := SetSessionProfile("default"); err != nil {
		t.Fatalf("SetSessionProfile(default): %v", err)
	}
	if ActiveProfile() != "" {
		t.Errorf("ActiveProfile() = %q, want \"\" after SetSessionProfile(\"default\")", ActiveProfile())
	}
}

func TestSetSessionProfileRequiresExisting(t *testing.T) {
	resetViper(t)
	if err := SetSessionProfile("ghost"); err == nil {
		t.Error("SetSessionProfile(unknown) = nil error, want an error")
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

func TestMoveProfileDataDirMovesExistingData(t *testing.T) {
	home := resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	oldDir, _ := ProfileDir("firma")
	oldPath := filepath.Join(oldDir, "budget.db")
	if err := os.WriteFile(oldPath, []byte("fake db"), 0o644); err != nil {
		t.Fatalf("seeding old db: %v", err)
	}

	newDir := filepath.Join(home, "Dropbox", "budgetctl-firma")
	status, err := MoveProfileDataDir("firma", newDir)
	if err != nil {
		t.Fatalf("MoveProfileDataDir: %v", err)
	}
	if !strings.Contains(status, "Moved existing data") {
		t.Errorf("status = %q, want it to mention the data was moved", status)
	}

	newPath := filepath.Join(newDir, "budget.db")
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected db at new path %s: %v", newPath, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("expected old db to be gone from %s, stat err = %v", oldPath, err)
	}
	if dir, shared := ProfileDir("firma"); dir != newDir || !shared {
		t.Errorf("ProfileDir(firma) = (%q, %v), want (%q, true)", dir, shared, newDir)
	}
}

func TestMoveProfileDataDirRejectsDefaultCollision(t *testing.T) {
	home := resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	defaultDir := filepath.Join(home, ".local", "share", "budgetctl")
	if _, err := MoveProfileDataDir("firma", defaultDir); err == nil {
		t.Error("MoveProfileDataDir onto the default's own dir = nil error, want an error")
	}
}

func TestMoveProfileDataDirRejectsOtherProfileCollision(t *testing.T) {
	home := resetViper(t)
	shared := filepath.Join(home, "Dropbox", "budgetctl-shared")
	if err := AddProfile("firma", shared); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if err := AddProfile("privat", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if _, err := MoveProfileDataDir("privat", shared); err == nil {
		t.Error("MoveProfileDataDir onto another profile's dir = nil error, want an error")
	}
}

func TestMoveProfileDataDirNoExistingData(t *testing.T) {
	home := resetViper(t)
	if err := AddProfile("firma", ""); err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	newDir := filepath.Join(home, "Dropbox", "budgetctl-firma")
	status, err := MoveProfileDataDir("firma", newDir)
	if err != nil {
		t.Fatalf("MoveProfileDataDir: %v", err)
	}
	if !strings.Contains(status, "Now syncing new data") {
		t.Errorf("status = %q, want it to mention there was nothing to move", status)
	}
}
