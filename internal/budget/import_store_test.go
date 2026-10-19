package budget

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aeon022/budgetctl/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "budget.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestImportFile_UpsertsAllParsedTransactions(t *testing.T) {
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,
2026-01-16,Employer GmbH,,Income,Salary January,2500.00,,,
`
	path := writeTemp(t, "n26-export.csv", csv)
	s := testStore(t)
	ctx := context.Background()

	res, err := ImportFile(ctx, s, path, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 2 {
		t.Errorf("expected 2 imported, got %d", res.Imported)
	}

	stored, err := s.List(ctx, store.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Errorf("expected 2 transactions in store, got %d", len(stored))
	}
}

func TestImportFile_AppliesExistingRulesDuringImport(t *testing.T) {
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,
`
	path := writeTemp(t, "n26-export.csv", csv)
	s := testStore(t)
	ctx := context.Background()

	if err := s.SaveRule(ctx, "rewe", "Groceries"); err != nil {
		t.Fatal(err)
	}

	if _, err := ImportFile(ctx, s, path, "", false); err != nil {
		t.Fatal(err)
	}

	stored, err := s.List(ctx, store.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Category != "Groceries" {
		t.Errorf("expected the rule to auto-categorize the import, got %+v", stored)
	}
}

func TestImportFile_AccountOverride(t *testing.T) {
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,
`
	path := writeTemp(t, "n26-export.csv", csv)
	s := testStore(t)
	ctx := context.Background()

	if _, err := ImportFile(ctx, s, path, "Joint Account", false); err != nil {
		t.Fatal(err)
	}

	stored, err := s.List(ctx, store.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Account != "Joint Account" {
		t.Errorf("expected account override to apply, got %+v", stored)
	}
}

func TestImportFile_UnrecognizedFileReturnsError(t *testing.T) {
	path := writeTemp(t, "not-a-csv.txt", "this is not csv content at all just some text")
	s := testStore(t)
	if _, err := ImportFile(context.Background(), s, path, "", false); err == nil {
		t.Error("expected an error for a file with no recognizable date/amount columns")
	}
}

func TestImportFile_SkipsRowsFromOverlappingExport(t *testing.T) {
	head := "Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate\n"
	a := writeTemp(t, "jan-feb.csv", head+"2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,\n")
	b := writeTemp(t, "jan-mar.csv", head+"2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,\n2026-03-01,Employer,,Income,Salary,2500.00,,,\n")
	s := testStore(t)
	ctx := context.Background()
	if _, err := ImportFile(ctx, s, a, "acc", false); err != nil {
		t.Fatal(err)
	}
	res, err := ImportFile(ctx, s, b, "acc", false)
	if err != nil || res.Imported != 1 || res.Duplicates != 1 {
		t.Fatalf("got %+v %v", res, err)
	}
	if stored, _ := s.List(ctx, store.Filter{Limit: 10}); len(stored) != 2 {
		t.Errorf("want 2 stored, got %d", len(stored))
	}
}
