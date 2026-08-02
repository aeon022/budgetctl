package config

import (
	"os"
	"path/filepath"

	coreconfig "github.com/aeon022/missionctl-core/config"
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

// DBPath returns the database file path: db_path (legacy, a full file path)
// if set, else data_dir/budget.db, where data_dir is resolved via
// coreconfig.ResolveDir — a user-configured directory (e.g. inside iCloud
// Drive/Dropbox) if the data_dir key is set, else the tool's private
// default. data_dir takes precedence if both are set.
func DBPath() string {
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
// directory (data_dir) rather than the tool's private default — used to
// decide SQLite journal mode and whether to treat the path as possibly
// folder-synced. The legacy db_path key is a full file path, not a
// directory, so it isn't treated as "shared" here even though it's also a
// user override; data_dir is the supported way to opt into sync safety.
func Shared() bool {
	return viper.GetString("data_dir") != ""
}

// SetDataDir persists data_dir to ~/.config/budgetctl/budgetctl.yaml and
// updates the running process's view of it immediately, so DBPath/Shared
// reflect the change without a restart. Pass "" to clear the override and
// revert to the private default. Used by the TUI's "o" settings screen.
func SetDataDir(dir string) error {
	viper.Set("data_dir", contractHome(dir))
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
