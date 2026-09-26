// Package history persists complete weekly inputs and outputs. A snapshot is
// kept even for QUIET names so "what changed" is deterministic next week.
package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/gcp"
)

type Snapshot struct {
	Week, Ticker string
	Price        float64
	Status       string
	Facts        json.RawMessage
	Macro        json.RawMessage
	LLM          json.RawMessage
	CreatedAt    time.Time
}
type Store interface {
	Previous(context.Context, string, string) (*Snapshot, error)
	Save(context.Context, Snapshot) error
}
type Firestore struct {
	Project string
	Tokens  gcp.TokenSource
	HTTP    *http.Client
}

func (f Firestore) base() string {
	return "https://firestore.googleapis.com/v1/projects/" + url.PathEscape(f.Project) + "/databases/(default)/documents/weekly_snapshots"
}
func (f Firestore) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return http.DefaultClient
}
func (f Firestore) auth(ctx context.Context, req *http.Request) error {
	t, e := f.Tokens.Token(ctx)
	if e == nil {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return e
}
func (f Firestore) Save(ctx context.Context, s Snapshot) error {
	if f.Project == "" {
		return fmt.Errorf("Firestore project is required")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	payload, e := json.Marshal(s)
	if e != nil {
		return e
	}
	doc := url.PathEscape(s.Ticker + "_" + s.Week)
	u := f.base() + "/" + doc
	body := map[string]any{"fields": map[string]any{"ticker": map[string]string{"stringValue": s.Ticker}, "week": map[string]string{"stringValue": s.Week}, "payload": map[string]string{"stringValue": string(payload)}}}
	raw, e := json.Marshal(body)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if e = f.auth(ctx, req); e != nil {
		return e
	}
	r, e := f.client().Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("Firestore save %s: HTTP %s", s.Ticker, r.Status)
	}
	return nil
}

// Delete removes an explicitly named snapshot. It is used only to clean up a
// temporary integration-check probe after exercising the Firestore write path.
func (f Firestore) Delete(ctx context.Context, ticker, week string) error {
	if f.Project == "" {
		return fmt.Errorf("Firestore project is required")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodDelete, f.base()+"/"+url.PathEscape(ticker+"_"+week), nil)
	if e != nil {
		return e
	}
	if e = f.auth(ctx, req); e != nil {
		return e
	}
	r, e := f.client().Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("Firestore delete %s: HTTP %s", ticker, r.Status)
	}
	return nil
}
func (f Firestore) Previous(ctx context.Context, ticker, week string) (*Snapshot, error) { // Uses a collection query and reads the JSON payload verbatim.
	if f.Project == "" {
		return nil, fmt.Errorf("Firestore project is required")
	}
	query := map[string]any{"structuredQuery": map[string]any{"from": []map[string]string{{"collectionId": "weekly_snapshots"}}, "where": map[string]any{"fieldFilter": map[string]any{"field": map[string]string{"fieldPath": "ticker"}, "op": "EQUAL", "value": map[string]string{"stringValue": ticker}}}}}
	raw, e := json.Marshal(query)
	if e != nil {
		return nil, e
	}
	u := "https://firestore.googleapis.com/v1/projects/" + url.PathEscape(f.Project) + "/databases/(default)/documents:runQuery"
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	if e = f.auth(ctx, req); e != nil {
		return nil, e
	}
	r, e := f.client().Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("Firestore previous %s: HTTP %s", ticker, r.Status)
	}
	var rows []struct {
		Document struct {
			Fields map[string]struct {
				StringValue string `json:"stringValue"`
			} `json:"fields"`
		} `json:"document"`
	}
	if e = json.NewDecoder(r.Body).Decode(&rows); e != nil {
		return nil, e
	}
	var found []Snapshot
	for _, row := range rows {
		p := row.Document.Fields["payload"].StringValue
		var s Snapshot
		if p != "" && json.Unmarshal([]byte(p), &s) == nil && s.Week < week {
			found = append(found, s)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Week > found[j].Week })
	return &found[0], nil
}
