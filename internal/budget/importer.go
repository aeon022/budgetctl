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
	"unicode/utf8"

	"github.com/aeon022/budgetctl/internal/models"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Import reads a CSV bank export and returns transactions.
// Supports N26, ING, DKB, Steiermärkische Sparkasse (George), and generic
// formats.
func Import(path string) ([]models.Transaction, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data, err := decodeToUTF8(raw)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	source := filepath.Base(path)

	switch detectFormat(path, data) {
	case "n26":
		return parseN26(bytes.NewReader(data), source)
	case "ing":
		return parseING(bytes.NewReader(data), source)
	case "dkb":
		return parseDKB(bytes.NewReader(data), source)
	case "at-umsatzliste":
		return parseATUmsatzliste(bytes.NewReader(data), source)
	case "george":
		return parseGeorge(bytes.NewReader(data), source)
	default:
		return parseGeneric(bytes.NewReader(data), source)
	}
}

// decodeToUTF8 transcodes a bank CSV export to UTF-8, if it isn't already.
// Bank exports vary wildly in encoding — Excel's Windows "CSV UTF-16
// Unicode" Save-As option is a common default for Austrian/German banking
// software and produces files that are pure garbage to any byte-oriented
// CSV parser without this step first.
//
// A BOM (UTF-8, UTF-16LE, or UTF-16BE) is the reliable signal, so it's
// checked first and handled via BOMOverride, which strips it either way.
// With no BOM, raw is checked for valid UTF-8 BEFORE attempting any
// decode — unicode.UTF8's own decoder is lossy (it silently substitutes
// U+FFFD for invalid bytes rather than erroring), and U+FFFD encodes as
// perfectly valid UTF-8, so decoding first and checking validity after
// would always report "valid" and never reach the Windows-1252 fallback
// below — the common default for exports with no explicit "UTF-8" choice.
func decodeToUTF8(raw []byte) ([]byte, error) {
	hasBOM := bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || // UTF-16LE
		bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) || // UTF-16BE
		bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8
	if hasBOM {
		out, _, err := transform.Bytes(unicode.BOMOverride(unicode.UTF8.NewDecoder()), raw)
		return out, err
	}
	if utf8.Valid(raw) {
		return raw, nil
	}
	out, _, err := transform.Bytes(charmap.Windows1252.NewDecoder(), raw)
	return out, err
}

// ── Format detection ──────────────────────────────────────────────────────────

// atUmsatzlisteStart matches the first data row of an Austrian bank
// "Umsatzliste" export (e.g. Steiermärkische Sparkasse's older format): no
// header row at all, just DD.MM.YYYY;"quoted description"...
var atUmsatzlisteStart = regexp.MustCompile(`^\d{2}\.\d{2}\.\d{4};"`)

// detectFormat expects data already decoded to UTF-8 (see decodeToUTF8).
func detectFormat(path string, data []byte) string {
	n := len(data)
	if n > 512 {
		n = 512
	}
	raw := bytes.TrimPrefix(data[:n], []byte{0xEF, 0xBB, 0xBF}) // stray UTF-8 BOM, if decodeToUTF8 left one behind
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
	// "Eigene IBAN"/"Partner IBAN" is George's (Erste Bank/Sparkasse online
	// banking) CSV export header — distinctive enough not to collide with
	// any other supported format.
	case strings.Contains(header, "eigene iban") && strings.Contains(header, "partner iban"):
		return "george"
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
	rows, err := readCSV(r, ';')
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
	// Card purchases (POS/ePayment) have no Zahlungsempfänger/Auftraggeber
	// label at all — the merchant name is just embedded as the first part
	// of the purpose text instead, e.g. "APPLE.COM/BILL CORK UNKNOWN
	// Zahlungsreferenz: ePAYMENT ... Kartenfolge-Nr.: 1". Every card-
	// terminal descriptor in this bank's export carries "Kartenfolge-Nr."
	// (card sequence number) — genuine fee/administrative lines (interest,
	// account maintenance, failed-payment notices) never do, so it's a
	// reliable gate that avoids misreading e.g. "Sollzinsen" as a merchant.
	if payee == "" && strings.Contains(blob, "Kartenfolge-Nr") {
		payee = extractMerchant(purpose)
	}
	return payee, purpose
}

// atMerchantAlias maps common brand identifiers (as they appear in payment
// terminal descriptors) to a clean display name. Longer/more specific keys
// must come before shorter ones they contain (e.g. "AMAZON.DE" before
// "AMAZON") since matching is prefix-based on the merchant token.
var atMerchantAlias = []struct{ prefix, name string }{
	{"APPLE.COM", "Apple"}, {"APPLE", "Apple"},
	{"AMAZON.DE", "Amazon"}, {"AMAZON", "Amazon"},
	{"PAYPAL", "PayPal"},
	{"GOOGLE", "Google"},
	{"MCDONALDS", "McDonald's"},
	{"KLARNA", "Klarna"},
	{"AUDIBLE", "Audible"},
	{"MOONPAY", "MoonPay"},
}

// atTrailingNoise matches a single token worth stripping off the end of a
// merchant descriptor: store/reference numbers, card-terminal codes
// (D1/K1/D02), dates, times.
var atTrailingNoise = regexp.MustCompile(`^(?:\d+|[DK]\d+|\d{2}\.\d{2}\.?|\d{2}:\d{2}(?::\d{2})?|R\d+)$`)

// extractMerchant best-effort cleans a card-purchase purpose string down to
// just the merchant name: known international brands get a canonical name
// via atMerchantAlias; anything else gets its trailing reference-number/
// terminal-code/date/time noise stripped and is title-cased. Returns "" if
// nothing usable is left.
func extractMerchant(purpose string) string {
	text := purpose
	if i := strings.Index(text, "*"); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	upper := strings.ToUpper(text)
	for _, a := range atMerchantAlias {
		if strings.HasPrefix(upper, a.prefix) {
			return a.name
		}
	}

	tokens := strings.Fields(text)
	for len(tokens) > 0 && atTrailingNoise.MatchString(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Title(strings.ToLower(strings.Join(tokens, " ")))
}

// ── George CSV (Erste Bank / Sparkasse online banking export) ──────────────────
// Header row, COMMA-separated (the amount column's own German comma
// decimal sits safely inside its quotes, e.g. "-357,87") — distinct from
// the older, semicolon-separated headerless "Umsatzliste" export above.
// German column names:
// Eigener Kontoname,Eigene IBAN,Buchungsdatum,Partnername,Partner IBAN,
// BIC/SWIFT,Partner Kontonummer,Bankleitzahl,Betrag,Währung,
// Buchungs-Details,Buchungsreferenz,Notiz,Highlight,Valutadatum,...
// Date format: DD.MM.YYYY, amount uses German comma decimal. Columns are
// looked up by name rather than position, since George has added optional
// trailing columns (card number, app, mandate/creditor IDs, ...) across
// export versions.

func parseGeorge(r io.Reader, source string) ([]models.Transaction, error) {
	rows, err := readCSV(r, ',')
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("no data rows in CSV")
	}

	col := columnIndex(rows[0])
	dateI, amtI := col("Buchungsdatum"), col("Betrag")
	if dateI < 0 || amtI < 0 {
		return nil, fmt.Errorf("could not detect date/amount columns from header: %v", rows[0])
	}
	acctI, partnerI, detailI, refI := col("Eigener Kontoname"), col("Partnername"), col("Buchungs-Details"), col("Zahlungsreferenz")

	var txs []models.Transaction
	for _, row := range rows[1:] {
		if len(row) <= amtI {
			continue
		}
		date, err := time.Parse("02.01.2006", strings.TrimSpace(row[dateI]))
		if err != nil {
			continue
		}
		amount, err := parseAmountDE(row[amtI])
		if err != nil {
			continue
		}
		desc := field(row, detailI)
		if desc == "" {
			desc = field(row, refI)
		}
		account := field(row, acctI)
		if account == "" {
			account = "Sparkasse"
		}
		raw := strings.Join(row, ";")
		txs = append(txs, models.Transaction{
			ID:          txID(source, raw),
			Date:        date,
			Payee:       field(row, partnerI),
			Description: desc,
			Amount:      amount,
			Account:     account,
			Source:      source,
			Raw:         raw,
		})
	}
	return txs, nil
}

// columnIndex builds a case-insensitive header-name → column-index lookup,
// so a format's parser can find columns by name instead of hardcoded
// position (safe against the bank adding/reordering trailing columns).
func columnIndex(header []string) func(name string) int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return func(name string) int {
		if i, ok := idx[strings.ToLower(name)]; ok {
			return i
		}
		return -1
	}
}

// field returns row[i], cleaned, or "" if i is out of range (column absent
// from this particular export, or a short row).
func field(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return clean(row[i])
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
