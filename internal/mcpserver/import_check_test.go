package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportOnlyAcceptsCSVFiles(t *testing.T) {
	d := t.TempDir()
	ok := filepath.Join(d, "bank.csv")
	os.WriteFile(ok, []byte("date;amount\n"), 0o600)
	bad := filepath.Join(d, "id_rsa")
	os.WriteFile(bad, []byte("secret"), 0o600)
	if err := checkImportFile(ok); err != nil {
		t.Errorf("csv must pass: %v", err)
	}
	for _, p := range []string{bad, d, filepath.Join(d, "missing.csv")} {
		if checkImportFile(p) == nil {
			t.Errorf("%s must be refused", p)
		}
	}
}
