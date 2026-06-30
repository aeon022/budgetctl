package config

import (
	"os"
	"path/filepath"

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

func DBPath() string {
	if p := viper.GetString("db_path"); p != "" {
		return expandHome(p)
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "budgetctl")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "budget.db")
}

func expandHome(p string) string {
	if len(p) >= 2 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
