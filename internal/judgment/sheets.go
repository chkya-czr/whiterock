// Package judgment reads and appends to the investor's manually entered
// decision log. Appends are additive only: an existing (week, ticker) row —
// including any my_decision/my_reason the investor has filled in — is never
// modified or duplicated by an automated write.
package judgment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/gcp"
)

type Entry struct{ Week, Ticker, Status, SummaryRef, Decision, Reason, ModelRating, ModelConfidence string }

// Row is a new judgment-log line this run wants recorded. Decision and
// Reason are always written blank; those columns are for the investor.
type Row struct{ Week, Ticker, Status, SummaryRef, ModelRating, ModelConfidence string }

type Reader interface {
	LastWeek(context.Context, string) ([]Entry, error)
	Entries(context.Context) ([]Entry, error)
}
type Writer interface {
	Sync(context.Context, []Row) error
}
type Sheets struct {
	ID, Range string
	Tokens    gcp.TokenSource
	HTTP      *http.Client
	// BaseURL overrides the Sheets API root; used only by tests.
	BaseURL string
}

func (s Sheets) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}
func (s Sheets) rangeOrDefault() string {
	if s.Range == "" {
		return "Sheet1!A:H"
	}
	// A:F was the original decision-log schema. Treat an explicitly retained
	// legacy range as its non-destructive A:H extension.
	if strings.HasSuffix(s.Range, "!A:F") {
		return strings.TrimSuffix(s.Range, "!A:F") + "!A:H"
	}
	return s.Range
}
func (s Sheets) baseURL() string {
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return "https://sheets.googleapis.com/v4/spreadsheets"
}
func (s Sheets) values(ctx context.Context) ([][]string, error) {
	t, e := s.Tokens.Token(ctx)
	if e != nil {
		return nil, e
	}
	u := s.baseURL() + "/" + url.PathEscape(s.ID) + "/values/" + url.PathEscape(s.rangeOrDefault())
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+t)
	r, e := s.client().Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("Sheets read: HTTP %s", r.Status)
	}
	var v struct {
		Values [][]string `json:"values"`
	}
	if e = json.NewDecoder(r.Body).Decode(&v); e != nil {
		return nil, e
	}
	return v.Values, nil
}

// LastWeek returns the rows logged for exactly one week.
func (s Sheets) LastWeek(ctx context.Context, week string) ([]Entry, error) {
	all, e := s.Entries(ctx)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for _, x := range all {
		if x.Week == week {
			out = append(out, x)
		}
	}
	return out, nil
}

// headerAliases lists accepted header spellings per field. The first entry is
// the canonical name; the legacy schema used the shorter ones.
var headerAliases = map[string][]string{
	"week":       {"week"},
	"ticker":     {"ticker"},
	"status":     {"flagged_status", "status"},
	"ref":        {"summary_ref", "ref", "summary_reference"},
	"decision":   {"my_decision", "decision"},
	"reason":     {"my_reason", "reason"},
	"rating":     {"model_rating"},
	"confidence": {"model_confidence"},
}

// Entries reads every row, locating columns by header name rather than
// position, so reordering or inserting columns cannot misattribute a
// decision. Tickers are upper-cased; rows with an unparseable week or no
// ticker are skipped and logged.
func (s Sheets) Entries(ctx context.Context) ([]Entry, error) {
	if s.ID == "" {
		return nil, nil
	}
	rows, e := s.values(ctx)
	if e != nil {
		return nil, e
	}
	return parseEntries(rows)
}

func parseEntries(rows [][]string) ([]Entry, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	col := func(field string) int {
		for _, name := range headerAliases[field] {
			if i, ok := idx[name]; ok {
				return i
			}
		}
		return -1
	}
	cols := map[string]int{}
	for f := range headerAliases {
		cols[f] = col(f)
	}
	for _, need := range []string{"week", "ticker", "decision", "reason"} {
		if cols[need] < 0 {
			return nil, fmt.Errorf("Sheet header is missing %q column", headerAliases[need][0])
		}
	}
	cell := func(row []string, field string) string {
		if i := cols[field]; i >= 0 && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	out := make([]Entry, 0, len(rows))
	for n, row := range rows[1:] {
		week := cell(row, "week")
		if _, err := time.Parse("2006-01-02", week); err != nil {
			if len(row) > 0 && strings.TrimSpace(strings.Join(row, "")) != "" {
				log.Printf("Sheet row %d skipped: unparseable week %q", n+2, week)
			}
			continue
		}
		ticker := strings.ToUpper(cell(row, "ticker"))
		if ticker == "" {
			log.Printf("Sheet row %d skipped: blank ticker", n+2)
			continue
		}
		out = append(out, Entry{Week: week, Ticker: ticker, Status: cell(row, "status"), SummaryRef: cell(row, "ref"), Decision: cell(row, "decision"), Reason: cell(row, "reason"), ModelRating: cell(row, "rating"), ModelConfidence: cell(row, "confidence")})
	}
	return out, nil
}

// LatestDecisions maps ticker to its most recent entry with a non-blank
// my_decision (greatest week; the later row wins a tie).
func LatestDecisions(entries []Entry) map[string]Entry {
	out := map[string]Entry{}
	for _, x := range entries {
		if strings.TrimSpace(x.Decision) == "" {
			continue
		}
		if cur, ok := out[x.Ticker]; !ok || x.Week >= cur.Week {
			out[x.Ticker] = x
		}
	}
	return out
}

// Find returns the entry for (week, ticker), preferring a row that carries a
// decision when duplicates exist.
func Find(entries []Entry, week, ticker string) (Entry, bool) {
	var found Entry
	ok := false
	for _, x := range entries {
		if x.Week == week && x.Ticker == strings.ToUpper(ticker) {
			if !ok || (found.Decision == "" && x.Decision != "") {
				found, ok = x, true
			}
		}
	}
	return found, ok
}

// Sync appends one row per entry in rows whose (week, ticker) does not
// already exist in the sheet, in whatever position that row happens to
// occupy. It never edits or reorders existing rows.
func (s Sheets) Sync(ctx context.Context, rows []Row) error {
	if s.ID == "" || len(rows) == 0 {
		return nil
	}
	existingRows, e := s.values(ctx)
	if e != nil {
		return fmt.Errorf("Sheets sync read: %w", e)
	}
	if len(existingRows) > 0 && len(existingRows[0]) < 8 {
		if e := s.ensureModelHeaders(ctx); e != nil {
			return fmt.Errorf("Sheets header update: %w", e)
		}
	}
	existing := map[string]bool{}
	for i, row := range existingRows {
		if i == 0 || len(row) < 2 {
			continue
		}
		existing[row[0]+"|"+row[1]] = true
	}
	values := make([][]any, 0, len(rows))
	for _, row := range rows {
		if existing[row.Week+"|"+row.Ticker] {
			continue
		}
		values = append(values, []any{row.Week, row.Ticker, row.Status, row.SummaryRef, "", "", row.ModelRating, row.ModelConfidence})
	}
	if len(values) == 0 {
		return nil
	}
	return s.append(ctx, values)
}

// ensureModelHeaders adds only the two new model-owned columns. Existing
// A:F columns, especially the investor's decision and reason, are untouched.
func (s Sheets) ensureModelHeaders(ctx context.Context) error {
	t, e := s.Tokens.Token(ctx)
	if e != nil {
		return e
	}
	sheet := strings.SplitN(s.rangeOrDefault(), "!", 2)[0]
	rng := sheet + "!G1:H1"
	body, e := json.Marshal(map[string]any{"values": [][]string{{"model_rating", "model_confidence"}}})
	if e != nil {
		return e
	}
	u := s.baseURL() + "/" + url.PathEscape(s.ID) + "/values/" + url.PathEscape(rng) + "?valueInputOption=RAW"
	req, e := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t)
	r, e := s.client().Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("Sheets header update: HTTP %s", r.Status)
	}
	return nil
}
func (s Sheets) append(ctx context.Context, values [][]any) error {
	t, e := s.Tokens.Token(ctx)
	if e != nil {
		return e
	}
	body, e := json.Marshal(map[string]any{"values": values})
	if e != nil {
		return e
	}
	u := s.baseURL() + "/" + url.PathEscape(s.ID) + "/values/" + url.PathEscape(s.rangeOrDefault()) + ":append?valueInputOption=RAW&insertDataOption=INSERT_ROWS"
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t)
	r, e := s.client().Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("Sheets append: HTTP %s", r.Status)
	}
	return nil
}
