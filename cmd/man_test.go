package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra/doc"
)

func TestManPagesGenerate(t *testing.T) {
	dir := t.TempDir()
	if err := doc.GenManTree(rootCmd, &doc.GenManHeader{Title: "BUDGETCTL", Section: "1"}, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "budgetctl.1")); err != nil {
		t.Errorf("no top-level man page: %v", err)
	}
}
