package judgment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticToken struct{}

func (staticToken) Token(context.Context) (string, error) { return "test-token", nil }

// fakeSheet serves GET (values.get) from `rows` and records any POST
// (values.append) bodies it receives, appending them into `rows` so a
// second Sync call in the same test sees them as already present.
type fakeSheet struct {
	rows     [][]string
	appended [][]any
}

func (f *fakeSheet) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"values": f.rows})
			return
		}
		var body struct {
			Values [][]any `json:"values"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.appended = append(f.appended, body.Values...)
		for _, v := range body.Values {
			row := make([]string, 0, len(v))
			for _, cell := range v {
				s, _ := cell.(string)
				row = append(row, s)
			}
			f.rows = append(f.rows, row)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
}

func TestSyncAppendsMissingRows(t *testing.T) {
	fs := &fakeSheet{rows: [][]string{{"week", "ticker", "status", "ref", "decision", "reason"}}}
	srv := fs.server()
	defer srv.Close()
	s := Sheets{ID: "sheet1", Tokens: staticToken{}, BaseURL: srv.URL}

	rows := []Row{
		{Week: "2026-09-27", Ticker: "AMD", Status: "TRIGGERED", SummaryRef: "firestore:AMD_2026-09-27"},
		{Week: "2026-09-27", Ticker: "XAU/USD", Status: "APPROACHING", SummaryRef: "firestore:XAU/USD_2026-09-27"},
	}
	if e := s.Sync(context.Background(), rows); e != nil {
		t.Fatalf("Sync: %v", e)
	}
	if len(fs.appended) != 2 {
		t.Fatalf("want 2 appended rows, got %d", len(fs.appended))
	}
	for _, v := range fs.appended {
		if v[4] != "" || v[5] != "" {
			t.Fatalf("expected blank decision/reason, got %v", v)
		}
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	fs := &fakeSheet{rows: [][]string{{"week", "ticker", "status", "ref", "decision", "reason"}}}
	srv := fs.server()
	defer srv.Close()
	s := Sheets{ID: "sheet1", Tokens: staticToken{}, BaseURL: srv.URL}

	rows := []Row{{Week: "2026-09-27", Ticker: "AMD", Status: "TRIGGERED", SummaryRef: "firestore:AMD_2026-09-27"}}
	if e := s.Sync(context.Background(), rows); e != nil {
		t.Fatalf("first Sync: %v", e)
	}
	if e := s.Sync(context.Background(), rows); e != nil {
		t.Fatalf("second Sync: %v", e)
	}
	if len(fs.appended) != 1 {
		t.Fatalf("rerunning the same week/ticker must not duplicate rows, got %d appended", len(fs.appended))
	}
}

func TestSyncNeverOverwritesManualDecision(t *testing.T) {
	fs := &fakeSheet{rows: [][]string{
		{"week", "ticker", "status", "ref", "decision", "reason"},
		{"2026-09-27", "AMD", "TRIGGERED", "firestore:AMD_2026-09-27", "bought", "good entry"},
	}}
	srv := fs.server()
	defer srv.Close()
	s := Sheets{ID: "sheet1", Tokens: staticToken{}, BaseURL: srv.URL}

	rows := []Row{{Week: "2026-09-27", Ticker: "AMD", Status: "TRIGGERED", SummaryRef: "firestore:AMD_2026-09-27"}}
	if e := s.Sync(context.Background(), rows); e != nil {
		t.Fatalf("Sync: %v", e)
	}
	if len(fs.appended) != 0 {
		t.Fatalf("existing row must be skipped, not appended again: %v", fs.appended)
	}
	if fs.rows[1][4] != "bought" || fs.rows[1][5] != "good entry" {
		t.Fatalf("manual decision/reason must survive untouched, got %v", fs.rows[1])
	}
}

func TestSyncSkipsWithoutSheetID(t *testing.T) {
	s := Sheets{Tokens: staticToken{}}
	if e := s.Sync(context.Background(), []Row{{Week: "2026-09-27", Ticker: "AMD"}}); e != nil {
		t.Fatalf("Sync with no sheet ID should no-op, got: %v", e)
	}
}
