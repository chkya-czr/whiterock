// Package macro fetches official FRED observations and calculates the macro
// context locally. No prose model receives the underlying time series.
package macro

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Observation struct {
	Date  time.Time
	Value float64
}
type Fred struct {
	APIKey  string
	HTTP    *http.Client
	BaseURL string
}

func NewFred(key string) *Fred {
	return &Fred{APIKey: key, HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: "https://api.stlouisfed.org/fred"}
}
func (f *Fred) Series(ctx context.Context, id string, limit int) ([]Observation, error) {
	if f.APIKey == "" {
		return nil, fmt.Errorf("FRED API key is empty")
	}
	u, _ := url.Parse(f.BaseURL + "/series/observations")
	q := u.Query()
	q.Set("series_id", id)
	q.Set("api_key", f.APIKey)
	q.Set("file_type", "json")
	// Fetch the most recent window. FRED applies limit after sorting; ascending
	// order would silently return observations from the start of a long series.
	q.Set("sort_order", "desc")
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return nil, e
	}
	r, e := f.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("FRED %s: HTTP %s", id, r.Status)
	}
	var b struct {
		ErrorCode    int    `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Observations []struct {
			Date  string `json:"date"`
			Value string `json:"value"`
		} `json:"observations"`
	}
	if e = json.NewDecoder(r.Body).Decode(&b); e != nil {
		return nil, e
	}
	if b.ErrorMessage != "" {
		return nil, fmt.Errorf("FRED %s: %s", id, b.ErrorMessage)
	}
	out := make([]Observation, 0, len(b.Observations))
	for _, o := range b.Observations {
		if o.Value == "." {
			continue
		}
		d, e := time.Parse("2006-01-02", o.Date)
		if e != nil {
			return nil, e
		}
		v, e := strconv.ParseFloat(o.Value, 64)
		if e != nil {
			return nil, e
		}
		out = append(out, Observation{d, v})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("FRED %s: no numeric observations", id)
	}
	// The API returned newest first; all downstream trend arithmetic expects
	// oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
