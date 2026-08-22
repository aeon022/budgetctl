package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	coreconfig "github.com/aeon022/missionctl-core/config"
	"github.com/aeon022/missionctl-core/licensing"
	"github.com/spf13/viper"
)

func Init() {
	viper.SetConfigName("budgetctl")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.config/budgetctl")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix("BUDGETCTL")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}

// DBPath returns the database file path: if a profile is active, its own
// directory (see ProfileDir); else db_path (legacy, a full file path) if
// set, else data_dir/budget.db, where data_dir is resolved via
// coreconfig.ResolveDir — a user-configured directory (e.g. inside iCloud
// Drive/Dropbox) if the data_dir key is set, else the tool's private
// default. data_dir takes precedence if both are set.
func DBPath() string {
	if name := ActiveProfile(); name != "" {
		dir, _ := ProfileDir(name)
		return filepath.Join(dir, "budget.db")
	}
	return DefaultDBPath()
}

// DefaultDBPath returns the unscoped database path — what DBPath resolves
// to with no active profile — regardless of whether a profile is currently
// active. Used to show the "default" row in `budgetctl profile list`
// correctly even while a different profile is active; DBPath itself can't
// be reused for that since it always follows ActiveProfile.
func DefaultDBPath() string {
	if dir := viper.GetString("data_dir"); dir != "" {
		resolved, _ := coreconfig.ResolveDir("budgetctl", dir)
		return filepath.Join(resolved, "budget.db")
	}
	if p := viper.GetString("db_path"); p != "" {
		return expandHome(p)
	}
	dir, _ := coreconfig.ResolveDir("budgetctl", "")
	return filepath.Join(dir, "budget.db")
}

// Shared reports whether DBPath currently resolves to a user-configured
// directory (a profile's own data_dir, or the top-level data_dir) rather
// than a private default — used to decide SQLite journal mode and whether
// to treat the path as possibly folder-synced. The legacy db_path key is a
// full file path, not a directory, so it isn't treated as "shared" here
// even though it's also a user override; data_dir is the supported way to
// opt into sync safety.
func Shared() bool {
	if name := ActiveProfile(); name != "" {
		_, shared := ProfileDir(name)
		return shared
	}
	return DefaultShared()
}

// DefaultShared mirrors DefaultDBPath for Shared(): whether the unscoped
// default resolves to a user-configured (possibly synced) directory,
// regardless of whether a profile is currently active.
func DefaultShared() bool {
	return viper.GetString("data_dir") != ""
}

// SetDataDir persists data_dir to ~/.config/budgetctl/budgetctl.yaml and
// updates the running process's view of it immediately, so DBPath/Shared
// reflect the change without a restart. Pass "" to clear the override and
// revert to the private default. Used by the TUI's "o" settings screen.
func SetDataDir(dir string) error {
	viper.Set("data_dir", contractHome(dir))
	return writeConfigFile()
}

// Profiles returns the configured profile names, sorted.
func Profiles() []string {
	m := viper.GetStringMap("profiles")
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ActiveProfile returns the currently active profile name, or "" for the
// unscoped default database.
func ActiveProfile() string {
	return viper.GetString("active_profile")
}

// ProfileExists reports whether name is a configured profile.
func ProfileExists(name string) bool {
	_, ok := viper.GetStringMap("profiles")[name]
	return ok
}

// ProfileDir resolves a profile's data directory: its own data_dir
// override (profiles.<name>.data_dir) if set, resolved the same way
// top-level data_dir is; otherwise a private, non-synced subfolder under
// this tool's default data directory.
func ProfileDir(name string) (dir string, shared bool) {
	if override := viper.GetString("profiles." + name + ".data_dir"); override != "" {
		return coreconfig.ResolveDir("budgetctl", override)
	}
	dir = filepath.Join(coreconfig.DataDir("budgetctl"), "profiles", name)
	_ = os.MkdirAll(dir, 0o755)
	return dir, false
}

// AddProfile registers a new profile. dataDir is optional; empty means the
// profile's database lives in a private default subfolder. Pass a folder
// (e.g. inside iCloud Drive/Dropbox) to sync that profile like data_dir
// does for the unscoped default.
func AddProfile(name, dataDir string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("profile name required")
	}
	if name == "default" || strings.ContainsAny(name, "/\\.") {
		return fmt.Errorf("profile name can't be \"default\" or contain path separators or dots")
	}
	if ProfileExists(name) {
		return fmt.Errorf("profile %q already exists", name)
	}
	viper.Set("profiles."+name+".data_dir", contractHome(dataDir))
	return writeConfigFile()
}

// MoveProfileDataDir points an existing profile at a new directory (e.g. to
// start syncing it via iCloud Drive/Dropbox) and, if there's an existing
// database that needs to move to make the change actually take effect,
// moves it — the CLI/non-interactive equivalent of the TUI's "o" settings
// screen for the unscoped default:
//   - refuses if newDir is already used by another profile or by the
//     unscoped default — profiles exist to keep data apart; silently
//     reusing another one's file would merge them instead.
//   - if the new location already has a database, it's used as-is (the
//     "joining a device that already set this profile up" case) — the
//     previous local database is left untouched at its old path, not
//     merged or deleted.
//   - else if the old location has a database, it's moved to the new
//     location.
//   - else there's nothing to move (a fresh setup).
//
// Returns a human-readable status describing what happened. newDir == ""
// moves the profile back to its private default subfolder.
func MoveProfileDataDir(name, newDir string) (status string, err error) {
	if !ProfileExists(name) {
		return "", fmt.Errorf("no profile named %q", name)
	}

	if newDir != "" {
		resolved, _ := coreconfig.ResolveDir("budgetctl", contractHome(newDir))
		if defaultDir := filepath.Dir(DefaultDBPath()); resolved == defaultDir {
			return "", fmt.Errorf("%s is already used by the default (unscoped) database — pick a different folder so profiles stay separate", resolved)
		}
		for _, other := range Profiles() {
			if other == name {
				continue
			}
			if otherDir, _ := ProfileDir(other); otherDir == resolved {
				return "", fmt.Errorf("%s is already used by profile %q — pick a different folder so profiles stay separate", resolved, other)
			}
		}
	}

	oldDir, _ := ProfileDir(name)
	oldPath := filepath.Join(oldDir, "budget.db")

	if err := SetProfileDataDir(name, newDir); err != nil {
		return "", err
	}

	newResolvedDir, _ := ProfileDir(name)
	newPath := filepath.Join(newResolvedDir, "budget.db")
	if newPath == oldPath {
		return fmt.Sprintf("Now using %s.", newResolvedDir), nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Sprintf("Found an existing database at %s — now using it (previous data untouched at %s).", newResolvedDir, oldPath), nil
	}
	if _, err := os.Stat(oldPath); err == nil {
		if err := MoveDBFile(oldPath, newPath); err != nil {
			return "", fmt.Errorf("moving existing database: %w", err)
		}
		_ = os.Remove(oldPath + ".lock")
		return fmt.Sprintf("Moved existing data to %s.", newResolvedDir), nil
	}
	return fmt.Sprintf("Now syncing new data to %s.", newResolvedDir), nil
}

// SetProfileDataDir changes an existing profile's directory override
// without moving any existing database — MoveProfileDataDir wraps this
// with the actual file move; call it directly only when you know there's
// nothing to move (e.g. tests).
func SetProfileDataDir(name, dir string) error {
	if !ProfileExists(name) {
		return fmt.Errorf("no profile named %q", name)
	}
	viper.Set("profiles."+name+".data_dir", contractHome(dir))
	return writeConfigFile()
}

// MoveDBFile renames oldPath to newPath, falling back to copy-then-remove
// if they're on different filesystems (os.Rename returns EXDEV) — a
// folder-synced directory (iCloud Drive, Dropbox) is usually on the same
// volume as $HOME, but not guaranteed.
func MoveDBFile(oldPath, newPath string) error {
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err == nil {
		return nil
	}
	src, err := os.Open(oldPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(newPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(oldPath)
}

// RemoveProfile forgets a profile's mapping — it does NOT delete the
// on-disk database, only the name that points at it. If name was the
// active profile, the active profile is cleared back to the default.
func RemoveProfile(name string) error {
	if !ProfileExists(name) {
		return fmt.Errorf("no profile named %q", name)
	}
	profiles := viper.GetStringMap("profiles")
	delete(profiles, name)
	viper.Set("profiles", profiles)
	if ActiveProfile() == name {
		viper.Set("active_profile", "")
	}
	return writeConfigFile()
}

// SetActiveProfile switches the active profile. "" clears it, reverting
// DBPath/Shared to the unscoped default (data_dir/db_path/private
// default). A non-empty name must already exist.
func SetActiveProfile(name string) error {
	if name != "" && !ProfileExists(name) {
		return fmt.Errorf("no profile named %q — create it first with: budgetctl profile add %s", name, name)
	}
	viper.Set("active_profile", name)
	return writeConfigFile()
}

// SetSessionProfile overrides the active profile for the current process
// only — unlike SetActiveProfile, it does NOT persist to config, so it's
// safe for a one-off override (the --profile flag) without touching
// whichever profile is configured as the sticky default. "default" clears
// back to the unscoped database, same as SetActiveProfile("").
func SetSessionProfile(name string) error {
	if name == "default" {
		name = ""
	}
	if name != "" && !ProfileExists(name) {
		return fmt.Errorf("no profile named %q — create it first with: budgetctl profile add %s", name, name)
	}
	viper.Set("active_profile", name)
	return nil
}

// writeConfigFile persists the current viper state to
// ~/.config/budgetctl/budgetctl.yaml, creating the directory if needed.
func writeConfigFile() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgDir := filepath.Join(home, ".config", "budgetctl")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}
	return viper.WriteConfigAs(filepath.Join(cfgDir, "budgetctl.yaml"))
}

// bundleBenefitID and budgetctlBenefitID identify the missionctl Bundle's
// and budgetctl's own individual-product license-key benefits in Polar.
// Both start empty (the budgetctl-only product doesn't exist in Polar
// yet) — see licensing.Result.Grants: empty IDs fall back to "any active
// key under our org grants access", so this is a no-op until both are
// filled in once the individual product is created and its benefit ID is
// known.
const (
	bundleBenefitID    = "de1be860-1dfc-43da-99a8-206fb2573f09"
	budgetctlBenefitID = "d296301d-461e-4799-bf2e-a6ff0f7c44cd"
)

// IsPro reports whether a valid Pro/Bundle or budgetctl-only license is
// active on this machine — gates AI categorization, recurring-payment
// detection, and AI-suggested budget cuts.
func IsPro() bool {
	result := licensing.Result{Status: LicenseStatus(), BenefitID: LicenseBenefitID()}
	return result.Grants(budgetctlBenefitID, bundleBenefitID)
}

// Truncate shortens s to at most n bytes, replacing the last one with "…"
// when it doesn't fit.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ProFeatureMessage returns the standard paywall message shown when a
// missionctl Bundle feature is used without an active license.
func ProFeatureMessage(feature string) string {
	return fmt.Sprintf("%s is a missionctl Bundle feature.\nGet it at https://missionctl.sh/#pricing, then: budgetctl license activate <key>", feature)
}

func LicenseKey() string {
	return viper.GetString("license_key")
}

func LicenseStatus() string {
	return viper.GetString("license_status")
}

func LicenseBenefitID() string {
	return viper.GetString("license_benefit_id")
}

func PolarOrgID() string {
	if v := viper.GetString("polar_org_id"); v != "" {
		return v
	}
	return licensing.DefaultOrgID
}

// SetLicense persists the license key/status/benefit to
// ~/.config/budgetctl/budgetctl.yaml.
func SetLicense(key, status, benefitID string) error {
	viper.Set("license_key", key)
	viper.Set("license_status", status)
	viper.Set("license_benefit_id", benefitID)
	return writeConfigFile()
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func contractHome(p string) string {
	if p == "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if len(p) > len(home) && p[:len(home)] == home {
		return "~" + p[len(home):]
	}
	return p
}
