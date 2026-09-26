// Package judgment reads the investor's manually entered decision log only.
package judgment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/aayushgogia/stock-research-tool/internal/gcp"
)

type Entry struct{ Week, Ticker, Status, SummaryRef, Decision, Reason string }
type Reader interface {
	LastWeek(context.Context, string) ([]Entry, error)
}
type Sheets struct {
	ID, Range string
	Tokens    gcp.TokenSource
	HTTP      *http.Client
}

func (s Sheets) LastWeek(ctx context.Context, week string) ([]Entry, error) {
	if s.ID == "" {
		return nil, nil
	}
	t, e := s.Tokens.Token(ctx)
	if e != nil {
		return nil, e
	}
	rng := s.Range
	if rng == "" {
		rng = "Sheet1!A:F"
	}
	u := "https://sheets.googleapis.com/v4/spreadsheets/" + url.PathEscape(s.ID) + "/values/" + url.PathEscape(rng)
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+t)
	h := s.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	r, e := h.Do(req)
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
	out := []Entry{}
	for i, row := range v.Values {
		if i == 0 || len(row) < 6 {
			continue
		}
		if row[0] == week {
			out = append(out, Entry{row[0], row[1], row[2], row[3], row[4], row[5]})
		}
	}
	return out, nil
}
