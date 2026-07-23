package budget

import (
	"os"
	"path/filepath"
	"strings"
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
	if txs[0].Payee != "REWE Markt" {
		t.Errorf("unexpected payee: %q", txs[0].Payee)
	}
	if txs[0].Description != "Groceries" {
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
	if txs[0].Payee != "Netflix" {
		t.Errorf("unexpected payee: %q", txs[0].Payee)
	}
	if txs[0].Description != "Abo Januar" {
		t.Errorf("unexpected description: %q", txs[0].Description)
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
	if txs[0].Payee != "Stadtwerke" {
		t.Errorf("unexpected payee: %q", txs[0].Payee)
	}
	if txs[0].Description != "Strom Abschlag" {
		t.Errorf("unexpected description: %q", txs[0].Description)
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

func TestImportATUmsatzliste(t *testing.T) {
	// Real-world shape of an Austrian bank "Umsatzliste" export (e.g.
	// Steiermärkische Sparkasse): NO header row at all, ';'-delimited,
	// UTF-8 BOM, German comma decimals. Every field-name-based header
	// detection (parseGeneric) fails on this by construction — there is no
	// header — so it needs its own dedicated parser.
	csv := "\xef\xbb\xbf" +
		"01.06.2026;\"Verwendungszweck: WIN2DAY WIEN 1030 Zahlungsreferenz: ePAYMENT 30,00 AT\";29.05.2026;-30,00;EUR;01.06.2026 06:29:24:070\n" +
		"21.07.2026;\"Auftraggeber: Wanting Shi-Weiher Zahlungsreferenz: lunch and dinner\";21.07.2026;183,00;EUR;21.07.2026 09:31:12:638\n"
	txs, err := Import(writeTemp(t, "Umsatzliste_AT163800000007631146.csv", csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("want 2 transactions, got %d", len(txs))
	}
	if txs[0].Amount != -30.00 {
		t.Errorf("want amount -30.00, got %v", txs[0].Amount)
	}
	if txs[0].Date.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("unexpected date: %v", txs[0].Date)
	}
	if !strings.Contains(txs[0].Description, "WIN2DAY") {
		t.Errorf("expected description to contain the raw Verwendungszweck text, got %q", txs[0].Description)
	}
	if txs[1].Amount != 183.00 {
		t.Errorf("want amount 183.00 (income), got %v", txs[1].Amount)
	}
	if txs[1].Payee != "Wanting Shi-Weiher" {
		t.Errorf("expected payee split out of the 'Auftraggeber:' label, got %q", txs[1].Payee)
	}
	if txs[1].Description != "lunch and dinner" {
		t.Errorf("expected purpose split out of the 'Zahlungsreferenz:' fallback, got %q", txs[1].Description)
	}
}

func TestSplitATFields(t *testing.T) {
	cases := []struct {
		name, blob, wantPayee, wantPurpose string
	}{
		{
			"payee + Verwendungszweck, IBAN/BIC noise stripped",
			"Zahlungsempfänger: T-Mobile Austria GmbH Verwendungszweck: Magenta Rechnung IBAN Zahlungsempfänger: AT82 BIC Zahlungsempfänger: XXX",
			"T-Mobile Austria GmbH", "Magenta Rechnung",
		},
		{
			"no payee label but a card purchase (Kartenfolge-Nr present) — merchant extracted from the purpose",
			"Verwendungszweck: GRAZ MOBIL GRAZ 8010 Zahlungsreferenz: ePAYMENT 69,00 AT D1 Kartenfolge-Nr.: 1",
			"Graz Mobil Graz", "GRAZ MOBIL GRAZ 8010",
		},
		{
			"no labels at all recognized AND no Kartenfolge-Nr (not a card purchase) — falls back to the whole blob, no invented payee",
			"SPAR 2361 D1 15.07. 08:57",
			"", "SPAR 2361 D1 15.07. 08:57",
		},
		{
			"genuine bank fee: no payee label, no Kartenfolge-Nr — must NOT be mistaken for a merchant",
			"Zahlungsreferenz: Sollzinsen",
			"", "Sollzinsen",
		},
		{
			"short 'Empfänger:' label variant (Dauerauftrag)",
			"Dauerauftrag 38000-1 Empfänger: Braucampus Zahlungsreferenz: Mitgliedsbeitrag IBAN Empfänger: AT03",
			"Braucampus", "Mitgliedsbeitrag",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payee, purpose := splitATFields(c.blob)
			if payee != c.wantPayee {
				t.Errorf("payee = %q, want %q", payee, c.wantPayee)
			}
			if purpose != c.wantPurpose {
				t.Errorf("purpose = %q, want %q", purpose, c.wantPurpose)
			}
		})
	}
}

func TestExtractMerchant(t *testing.T) {
	cases := []struct{ purpose, want string }{
		{"APPLE.COM/BILL CORK UNKNOWN", "Apple"},
		{"APPLE.COM/BILL CORK T23YK84", "Apple"},
		{"AMAZON* NO7A827A4 LUXEMBOURG 1855", "Amazon"},
		{"AMAZON.DE*I31204JS5 LUXEMBOURG L1855", "Amazon"},
		{"PAYPAL *ADD TO BAL 35314369001 L2449", "PayPal"},
		{"GOOGLE *CLOUD T52TS8 8888888888 D02 R296", "Google"},
		{"KLARNA*VEEPEE STOCKHOLM 1010", "Klarna"},
		{"AUDIBLE GMBH*2N9EO1955 BERLIN 10117", "Audible"},
		{"MOONPAY 5068 AMSTERDAM 1017BZ", "MoonPay"},
		// no '*' separator, no alias match — generic trailing-noise strip + title case
		{"SPAR         2361  D1   26.06. 16:38", "Spar"},
		{"MCDONALDS172 2360  D1   26.06. 11:49", "McDonald's"}, // alias match is prefix-based, so "MCDONALDS172" still matches
		{"CINEPLEXX    2520  D1   05.07. 16:24", "Cineplexx"},
		{"INTERSPAR    2361  D1   13.07. 13:45", "Interspar"},
		// nothing left after stripping noise
		{"12345 D1 01.01.", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.purpose, func(t *testing.T) {
			if got := extractMerchant(c.purpose); got != c.want {
				t.Errorf("extractMerchant(%q) = %q, want %q", c.purpose, got, c.want)
			}
		})
	}
}

func TestParseGeneric_PicksFirstMatchingAmountColumn(t *testing.T) {
	// Regression test: found by actually running a real import end-to-end
	// rather than just the existing filename-triggered N26-parser tests.
	// This is the exact N26 header shape, but a filename WITHOUT "n26" in
	// it, so it goes through parseGeneric instead of the dedicated N26
	// parser. The header has TWO columns containing "amount" — "Amount
	// (EUR)" and the near-always-empty "Amount (Foreign Currency)". Taking
	// the LAST match (the old behavior) picked the foreign-currency
	// column, which is empty for domestic transactions, made every row's
	// amount unparseable, and silently dropped the entire import with no
	// error at all — "Imported 0 transactions" and no indication why.
	csv := `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-07-20,Rewe Supermarkt,DE123,MasterCard Payment,Groceries,-42.30,,,
2026-07-18,Employer GmbH,DE456,Income,Salary,2800.00,,,
`
	txs, err := Import(writeTemp(t, "bank-export.csv", csv)) // no "n26" in the filename
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 2 {
		t.Fatalf("want 2 transactions, got %d (amount column was likely mis-detected)", len(txs))
	}
	if txs[0].Amount != -42.30 {
		t.Errorf("want amount -42.30, got %v", txs[0].Amount)
	}
	if txs[1].Amount != 2800.00 {
		t.Errorf("want amount 2800.00, got %v", txs[1].Amount)
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
