package budget

import (
	"bytes"
	"crypto/sha1"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aeon022/budgetctl/internal/models"
)

// Import reads a CSV bank export and returns transactions.
// Supports N26, ING, Deutsche Bank, and generic formats.
func Import(path string) ([]models.Transaction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	format := detectFormat(path, f)
	_, _ = f.Seek(0, io.SeekStart)

	switch format {
	case "n26":
		return parseN26(f, filepath.Base(path))
	case "ing":
		return parseING(f, filepath.Base(path))
	case "dkb":
		return parseDKB(f, filepath.Base(path))
	case "at-umsatzliste":
		return parseATUmsatzliste(f, filepath.Base(path))
	default:
		return parseGeneric(f, filepath.Base(path))
	}
}

// ── Format detection ──────────────────────────────────────────────────────────

// atUmsatzlisteStart matches the first data row of an Austrian bank
// "Umsatzliste" export (e.g. Steiermärkische Sparkasse): no header row at
// all, just DD.MM.YYYY;"quoted description"... straight from the first byte
// (after an optional UTF-8 BOM).
var atUmsatzlisteStart = regexp.MustCompile(`^\d{2}\.\d{2}\.\d{4};"`)

func detectFormat(path string, f *os.File) string {
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	raw := bytes.TrimPrefix(buf[:n], []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	header := strings.ToLower(string(raw))

	switch {
	case strings.Contains(header, "n26") || strings.Contains(path, "n26"):
		return "n26"
	// "bank;ing" matches ING's actual preamble line ("Bank;ING ") — a bare
	// "ing " substring is too loose and false-positives on ordinary text
	// (e.g. a transaction description mentioning someone named "Wanting").
	case strings.Contains(header, "ing-diba") || strings.Contains(header, "bank;ing"):
		return "ing"
	case strings.Contains(header, "dkb") || strings.Contains(header, "deutsche kreditbank"):
		return "dkb"
	case atUmsatzlisteStart.Match(raw):
		return "at-umsatzliste"
	default:
		return "generic"
	}
}

// ── N26 CSV ───────────────────────────────────────────────────────────────────
// Format: Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
// Date format: 2025-12-31

func parseN26(r io.Reader, source string) ([]models.Transaction, error) {
	rows, err := readCSV(r, ',')
	if err != nil {
		return nil, err
	}
	var txs []models.Transaction
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		date, err := time.Parse("2006-01-02", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		amount, err := parseAmount(row[5])
		if err != nil {
			continue
		}
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Payee:       clean(row[1]),
			Description: clean(row[4]),
			Amount:      amount,
			Account:     "N26",
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// ── ING CSV ───────────────────────────────────────────────────────────────────
// ING has a header block before the CSV data, then:
// Buchung;Valuta;Auftraggeber/Empfänger;Buchungstext;Verwendungszweck;Betrag;Gläubiger-ID;Mandatsreferenz;Kundenreferenz
// Date format: DD.MM.YYYY, amount uses comma decimal

func parseING(r io.Reader, source string) ([]models.Transaction, error) {
	// skip non-CSV header lines
	data, _ := io.ReadAll(r)
	lines := strings.Split(string(data), "\n")
	var csvLines []string
	inData := false
	for _, l := range lines {
		if !inData {
			if strings.HasPrefix(l, "Buchung;") || strings.HasPrefix(l, "\"Buchung\";") {
				inData = true
			}
		}
		if inData {
			csvLines = append(csvLines, l)
		}
	}
	rows, err := readCSV(strings.NewReader(strings.Join(csvLines, "\n")), ';')
	if err != nil {
		return nil, err
	}
	var txs []models.Transaction
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		date, err := time.Parse("02.01.2006", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		amount, err := parseAmountDE(row[5])
		if err != nil {
			continue
		}
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Payee:       clean(row[2]),
			Description: clean(row[4]),
			Amount:      amount,
			Account:     "ING",
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// ── DKB CSV ───────────────────────────────────────────────────────────────────
// Format: "Buchungstag";"Wertstellung";"Gläubiger-ID";"Auftraggeber/Begünstigter";"Verwendungszweck";"Kontonummer";"BLZ";"Betrag (€)";"Gläubiger-ID";"Mandatsreferenz";"Kundenreferenz";

func parseDKB(r io.Reader, source string) ([]models.Transaction, error) {
	data, _ := io.ReadAll(r)
	lines := strings.Split(string(data), "\n")
	var csvLines []string
	inData := false
	for _, l := range lines {
		if !inData && (strings.Contains(l, "Buchungstag") || strings.Contains(l, "\"Buchungstag\"")) {
			inData = true
		}
		if inData {
			csvLines = append(csvLines, l)
		}
	}
	rows, err := readCSV(strings.NewReader(strings.Join(csvLines, "\n")), ';')
	if err != nil {
		return nil, err
	}
	var txs []models.Transaction
	for _, row := range rows {
		if len(row) < 8 {
			continue
		}
		date, err := time.Parse("02.01.2006", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		amount, err := parseAmountDE(row[7])
		if err != nil {
			continue
		}
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Payee:       clean(row[3]),
			Description: clean(row[4]),
			Amount:      amount,
			Account:     "DKB",
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// ── Austrian bank "Umsatzliste" CSV ─────────────────────────────────────────────
// No header row at all — straight data from byte 0 (after an optional UTF-8
// BOM). 6 semicolon-separated columns:
// Buchungsdatum;"description text";Valutadatum;Betrag;Währung;Timestamp
// Date format: DD.MM.YYYY, amount uses German comma decimal.
// Account is left unset (like the generic parser) since nothing in the file
// identifies which bank/account it came from — tag it via the TUI import
// assistant's "t" step or `budgetctl import --account`.

func parseATUmsatzliste(r io.Reader, source string) ([]models.Transaction, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	rows, err := readCSV(bytes.NewReader(data), ';')
	if err != nil {
		return nil, err
	}
	var txs []models.Transaction
	for _, row := range rows {
		if len(row) < 4 {
			continue
		}
		date, err := time.Parse("02.01.2006", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		amount, err := parseAmountDE(row[3])
		if err != nil {
			continue
		}
		payee, purpose := splitATFields(row[1])
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Payee:       payee,
			Description: purpose,
			Amount:      amount,
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// atFieldLabels matches the labeled sub-fields packed into an AT-Umsatzliste
// description blob, e.g. "Zahlungsempfänger: X Verwendungszweck: Y IBAN
// Zahlungsempfänger: AT... BIC Zahlungsempfänger: ...". The "IBAN|BIC \S+:"
// alternative MUST come first: it matches (and so consumes) compound labels
// like "IBAN Zahlungsempfänger:" before the bare "Zahlungsempfänger:"
// alternative below gets a chance to — regexp's leftmost-first matching
// means once "IBAN Zahlungsempfänger:" matches starting at "IBAN", the
// "Zahlungsempfänger:" substring inside it is already consumed and never
// re-examined as a separate match.
var atFieldLabels = regexp.MustCompile(
	`(?:IBAN|BIC)\s+\S+:|` +
		`(Zahlungsempfänger|Auftraggeber|Empfänger|Verwendungszweck|Zahlungsreferenz|Empfänger-Kennung|Auftraggeberreferenz|Mandat|Kartenfolge-Nr\.?):`,
)

// splitATFields best-effort splits an AT-Umsatzliste description blob into
// a counterparty name (payee) and purpose text, for display as separate
// table columns. Falls back to the whole cleaned blob as purpose when
// nothing recognizable is found — never loses information, since Raw still
// holds the original CSV row for the detail popup.
func splitATFields(blob string) (payee, purpose string) {
	locs := atFieldLabels.FindAllStringSubmatchIndex(blob, -1)
	zahlungsreferenz := ""
	for i, loc := range locs {
		if loc[2] < 0 { // unnamed (noise) alternative matched, no label captured
			continue
		}
		label := blob[loc[2]:loc[3]]
		valEnd := len(blob)
		if i+1 < len(locs) {
			valEnd = locs[i+1][0]
		}
		value := clean(blob[loc[1]:valEnd])

		switch label {
		case "Zahlungsempfänger", "Auftraggeber", "Empfänger":
			if payee == "" {
				payee = value
			}
		case "Verwendungszweck":
			if purpose == "" {
				purpose = value
			}
		case "Zahlungsreferenz":
			if zahlungsreferenz == "" {
				zahlungsreferenz = value
			}
		}
	}
	if purpose == "" {
		purpose = zahlungsreferenz
	}
	if purpose == "" {
		purpose = clean(blob)
	}
	return payee, purpose
}

// ── Generic CSV ───────────────────────────────────────────────────────────────
// Best-effort: looks for date, description, amount columns by header name

func parseGeneric(r io.Reader, source string) ([]models.Transaction, error) {
	rows, err := readCSV(r, ',')
	if err != nil {
		rows2, err2 := readCSV(r, ';')
		if err2 != nil {
			return nil, err
		}
		rows = rows2
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("no data rows in CSV")
	}

	// find column indices from header. First match wins for each field —
	// several real bank exports (e.g. N26) have more than one column whose
	// name contains "amount" ("Amount (EUR)" AND "Amount (Foreign
	// Currency)"); always taking the LAST match here used to silently pick
	// the near-always-empty foreign-currency column instead, making every
	// row's amount unparseable and dropping the entire import with no
	// error at all.
	header := rows[0]
	dateCol, descCol, amtCol := -1, -1, -1
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case dateCol < 0 && (strings.Contains(h, "date") || strings.Contains(h, "datum")):
			dateCol = i
		case descCol < 0 && (strings.Contains(h, "description") || strings.Contains(h, "verwendung") || strings.Contains(h, "payee") || strings.Contains(h, "empfänger")):
			descCol = i
		case amtCol < 0 && (strings.Contains(h, "amount") || strings.Contains(h, "betrag")):
			amtCol = i
		}
	}
	if dateCol < 0 || amtCol < 0 {
		return nil, fmt.Errorf("could not detect date/amount columns from header: %v", header)
	}

	var txs []models.Transaction
	for _, row := range rows[1:] {
		if len(row) <= amtCol {
			continue
		}
		date, err := parseDate(strings.TrimSpace(row[dateCol]))
		if err != nil {
			continue
		}
		desc := ""
		if descCol >= 0 && descCol < len(row) {
			desc = clean(row[descCol])
		}
		amount, err := parseAmount(row[amtCol])
		if err != nil {
			amount, err = parseAmountDE(row[amtCol])
			if err != nil {
				continue
			}
		}
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Description: desc,
			Amount:      amount,
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func readCSV(r io.Reader, sep rune) ([][]string, error) {
	cr := csv.NewReader(r)
	cr.Comma = sep
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	return cr.ReadAll()
}

func parseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, ",", "")
	return strconv.ParseFloat(s, 64)
}

func parseAmountDE(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, ".", "")  // German thousands separator
	s = strings.ReplaceAll(s, ",", ".") // German decimal separator
	return strconv.ParseFloat(s, 64)
}

// ParseUserAmount parses a user-typed amount: "12.50", "-12,50", "1.250,00", "€ 5".
// A comma switches interpretation to German format (dot = thousands separator).
func ParseUserAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("amount is required")
	}
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") {
		return parseAmountDE(s)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q (use e.g. -42.50 or -42,50)", s)
	}
	return f, nil
}

func parseDate(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02", "02.01.2006", "01/02/2006", "02/01/2006",
		"2006-01-02 15:04:05", "02.01.2006 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %q", s)
}

func clean(s string) string {
	s = strings.TrimSpace(s)
	// collapse multiple spaces
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

func txID(source, raw string) string {
	h := sha1.Sum([]byte(source + "|" + raw))
	return fmt.Sprintf("%x", h[:10])
}

// Categorize applies a set of rules to a description, returning the first match.
func Categorize(desc string, rules []models.CategoryRule) string {
	desc = strings.ToLower(desc)
	for _, r := range rules {
		if strings.Contains(desc, strings.ToLower(r.Pattern)) {
			return r.Category
		}
	}
	return ""
}
