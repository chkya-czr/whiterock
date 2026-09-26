package macro

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSeriesFetchesRecentValuesAndReturnsChronologicalOrder(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.Query().Get("sort_order"); got != "desc" {
			t.Errorf("sort_order=%q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"observations":[{"date":"2026-01-08","value":"2"},{"date":"2026-01-01","value":"1"}]}`)), Header: make(http.Header)}, nil
	})}
	f := &Fred{APIKey: "test", HTTP: client, BaseURL: "https://fred.test"}
	v, err := f.Series(context.Background(), "TEST", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 2 || v[0].Value != 1 || v[1].Value != 2 {
		t.Fatalf("got %#v", v)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
