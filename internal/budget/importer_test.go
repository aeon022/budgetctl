package budget

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aeon022/budgetctl/internal/models"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportN26(t *testing.T) {
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,
2026-01-16,Employer GmbH,,Income,Salary January,2500.00,,,
`
	txs, err := Import(writeTemp(t, "n26-export.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("want 2 transactions, got %d", len(txs))
	}
	if txs[0].Amount != -42.37 {
		t.Errorf("want amount -42.37, got %v", txs[0].Amount)
	}
	if txs[0].Account != "N26" {
		t.Errorf("want account N26, got %q", txs[0].Account)
	}
	if txs[0].Description != "REWE Markt — Groceries" {
		t.Errorf("unexpected description: %q", txs[0].Description)
	}
	if txs[0].Date.Format("2006-01-02") != "2026-01-15" {
		t.Errorf("unexpected date: %v", txs[0].Date)
	}
	if txs[1].Amount != 2500.00 {
		t.Errorf("want amount 2500.00, got %v", txs[1].Amount)
	}
}

func TestImportING(t *testing.T) {
	csv := "Umsatzanzeige;Datei erstellt am: 15.01.2026\n" +
		"IBAN;DE00 1234\n" +
		"Bank;ING \n" +
		"\n" +
		"Buchung;Valuta;Auftraggeber/Empfänger;Buchungstext;Verwendungszweck;Betrag;Gläubiger-ID;Mandatsreferenz;Kundenreferenz\n" +
		"14.01.2026;14.01.2026;Netflix;Lastschrift;Abo Januar;-12,99;;;\n" +
		"13.01.2026;13.01.2026;Vermieter;Dauerauftrag;Miete;-1.250,00;;;\n"
	txs, err := Import(writeTemp(t, "ing-umsaetze.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("want 2 transactions, got %d", len(txs))
	}
	if txs[0].Amount != -12.99 {
		t.Errorf("want -12.99 (German decimal comma), got %v", txs[0].Amount)
	}
	if txs[1].Amount != -1250.00 {
		t.Errorf("want -1250.00 (German thousands separator), got %v", txs[1].Amount)
	}
	if txs[0].Account != "ING" {
		t.Errorf("want account ING, got %q", txs[0].Account)
	}
	if txs[0].Date.Format("02.01.2006") != "14.01.2026" {
		t.Errorf("unexpected date: %v", txs[0].Date)
	}
}

func TestImportDKB(t *testing.T) {
	csv := "\"Kontonummer:\";\"DE00 5678 / Girokonto DKB\"\n" +
		"\n" +
		"\"Buchungstag\";\"Wertstellung\";\"Buchungstext\";\"Auftraggeber/Begünstigter\";\"Verwendungszweck\";\"Kontonummer\";\"BLZ\";\"Betrag (€)\";\"Gläubiger-ID\";\"Mandatsreferenz\";\"Kundenreferenz\"\n" +
		"\"10.01.2026\";\"10.01.2026\";\"Lastschrift\";\"Stadtwerke\";\"Strom Abschlag\";\"DE11\";\"12030000\";\"-89,50\";\"\";\"\";\"\"\n"
	txs, err := Import(writeTemp(t, "dkb-export.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("want 1 transaction, got %d", len(txs))
	}
	if txs[0].Amount != -89.50 {
		t.Errorf("want -89.50, got %v", txs[0].Amount)
	}
	if txs[0].Account != "DKB" {
		t.Errorf("want account DKB, got %q", txs[0].Account)
	}
}

func TestImportGeneric(t *testing.T) {
	csv := `Date,Description,Amount
2026-02-01,Coffee Shop,-4.50
01.02.2026,Refund,10.00
`
	txs, err := Import(writeTemp(t, "export.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("want 2 transactions, got %d", len(txs))
	}
	if txs[0].Description != "Coffee Shop" {
		t.Errorf("unexpected description: %q", txs[0].Description)
	}
	// second row uses German date format, must still parse
	if txs[1].Date.Format("2006-01-02") != "2026-02-01" {
		t.Errorf("unexpected date: %v", txs[1].Date)
	}
}

func TestImportGenericMissingColumns(t *testing.T) {
	csv := `Foo,Bar
1,2
`
	if _, err := Import(writeTemp(t, "broken.csv", csv)); err == nil {
		t.Fatal("want error for CSV without date/amount columns")
	}
}

func TestImportSkipsMalformedRows(t *testing.T) {
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
not-a-date,Payee,,,ref,-1.00,,,
2026-01-15,Valid,,,ref,not-a-number,,,
2026-01-16,Valid,,,ref,-5.00,,,
`
	txs, err := Import(writeTemp(t, "n26-partial.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 1 {
		t.Fatalf("want 1 valid transaction, got %d", len(txs))
	}
}

func TestParseAmount(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"-42.37", -42.37},
		{"1,234.56", 1234.56},
		{" 12.00 ", 12},
		{"€ 5.00", 5},
	}
	for _, c := range cases {
		got, err := parseAmount(c.in)
		if err != nil {
			t.Errorf("parseAmount(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAmount(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseAmountDE(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"-12,99", -12.99},
		{"-1.250,00", -1250},
		{"3,50 €", 3.5},
	}
	for _, c := range cases {
		got, err := parseAmountDE(c.in)
		if err != nil {
			t.Errorf("parseAmountDE(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseAmountDE(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTxIDStable(t *testing.T) {
	a := txID("export.csv", "row")
	b := txID("export.csv", "row")
	if a != b {
		t.Error("txID must be deterministic for identical input")
	}
	if a == txID("other.csv", "row") {
		t.Error("txID must differ for different sources")
	}
}

func TestCategorize(t *testing.T) {
	rules := []models.CategoryRule{
		{Pattern: "netflix", Category: "streaming"},
		{Pattern: "rewe", Category: "groceries"},
	}
	if got := Categorize("NETFLIX.COM Abo", rules); got != "streaming" {
		t.Errorf("want streaming, got %q", got)
	}
	if got := Categorize("REWE Markt GmbH", rules); got != "groceries" {
		t.Errorf("want groceries, got %q", got)
	}
	if got := Categorize("unrelated", rules); got != "" {
		t.Errorf("want empty category, got %q", got)
	}
}
