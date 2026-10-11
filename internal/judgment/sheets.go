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
	"net/http"
	"net/url"
	"strings"

	"github.com/aayushgogia/stock-research-tool/internal/gcp"
)

type Entry struct{ Week, Ticker, Status, SummaryRef, Decision, Reason, ModelRating, ModelConfidence string }

// Row is a new judgment-log line this run wants recorded. Decision and
// Reason are always written blank; those columns are for the investor.
type Row struct{ Week, Ticker, Status, SummaryRef, ModelRating, ModelConfidence string }

type Reader interface {
	LastWeek(context.Context, string) ([]Entry, error)
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

func (s Sheets) LastWeek(ctx context.Context, week string) ([]Entry, error) {
	if s.ID == "" {
		return nil, nil
	}
	rows, e := s.values(ctx)
	if e != nil {
		return nil, e
	}
	out := []Entry{}
	for i, row := range rows {
		if i == 0 || len(row) < 6 {
			continue
		}
		if row[0] == week {
			e := Entry{Week: row[0], Ticker: row[1], Status: row[2], SummaryRef: row[3], Decision: row[4], Reason: row[5]}
			if len(row) > 6 {
				e.ModelRating = row[6]
			}
			if len(row) > 7 {
				e.ModelConfidence = row[7]
			}
			out = append(out, e)
		}
	}
	return out, nil
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
