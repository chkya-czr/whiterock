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
	rows          [][]string
	appended      [][]any
	headerUpdated bool
}

func (f *fakeSheet) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"values": f.rows})
			return
		}
		if r.Method == http.MethodPut {
			f.headerUpdated = true
			if len(f.rows) > 0 {
				f.rows[0] = append(f.rows[0], "model_rating", "model_confidence")
			}
			w.WriteHeader(http.StatusOK)
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
	if !fs.headerUpdated {
		t.Fatal("expected G/H model headers to be added")
	}
	for _, v := range fs.appended {
		if v[4] != "" || v[5] != "" {
			t.Fatalf("expected blank decision/reason, got %v", v)
		}
		if len(v) != 8 {
			t.Fatalf("expected columns A-H, got %v", v)
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

func TestLegacyRangeIsExtendedForModelColumns(t *testing.T) {
	s := Sheets{Range: "Sheet1!A:F"}
	if got := s.rangeOrDefault(); got != "Sheet1!A:H" {
		t.Fatalf("range=%q", got)
	}
}

func TestEntriesLocateColumnsByHeaderNotPosition(t *testing.T) {
	fs := &fakeSheet{rows: [][]string{
		{"my_reason", "ticker", "model_rating", "week", "my_decision", "flagged_status"},
		{"earnings first", "fico", "WAIT", "2026-10-05", "Waited", "ATTENTION"},
		{"", "AMD", "", "not-a-week", "Bought", "TRIGGER"},
		{"older", "FICO", "", "2026-09-01", "Bought", "ATTENTION"},
		{"", "NOW", "", "2026-10-05", "  ", "ATTENTION"},
	}}
	srv := fs.server()
	defer srv.Close()
	s := Sheets{ID: "sheet1", Tokens: staticToken{}, BaseURL: srv.URL}
	entries, err := s.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("unparseable week should be skipped: %+v", entries)
	}
	latest := LatestDecisions(entries)
	f := latest["FICO"]
	if f.Week != "2026-10-05" || f.Decision != "Waited" || f.Reason != "earnings first" || f.Status != "ATTENTION" || f.ModelRating != "WAIT" {
		t.Fatalf("FICO latest = %+v", f)
	}
	if _, ok := latest["NOW"]; ok {
		t.Fatal("blank my_decision must not count as a decision")
	}
	if _, ok := Find(entries, "2026-10-05", "now"); !ok {
		t.Fatal("Find should be case-insensitive on ticker")
	}
}

func TestEntriesRejectsSheetWithoutDecisionHeaders(t *testing.T) {
	if _, err := parseEntries([][]string{{"week", "ticker"}, {"2026-10-05", "AMD"}}); err == nil {
		t.Fatal("expected missing-header error")
	}
}
