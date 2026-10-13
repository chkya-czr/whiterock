package watchlist

import (
	"testing"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

func bars(start time.Time, closes ...float64) []marketdata.Candle {
	out := make([]marketdata.Candle, len(closes))
	for i, c := range closes {
		out[i] = marketdata.Candle{Date: start.AddDate(0, 0, i), Close: c}
	}
	return out
}

func kinds(is []DataIssue) map[string]bool {
	m := map[string]bool{}
	for _, i := range is {
		m[i.Kind] = true
	}
	return m
}

func TestCheckCandles(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		c    []marketdata.Candle
		want []string
	}{
		{"clean", bars(start, 100, 101, 99, 100, 102), nil},
		{"unadjusted split looks like a halving", bars(start, 100, 101, 50, 51, 52), []string{"LARGE_DAILY_MOVE"}},
		{"exactly at threshold is not flagged", bars(start, 100, 125, 126), nil},
		{"zero close", bars(start, 100, 0, 99), []string{"NON_POSITIVE_CLOSE"}},
		{"stale last bar", bars(start.AddDate(0, 0, -20), 100, 101, 102), []string{"STALE_DATA"}},
	}
	for _, c := range cases {
		got := kinds(CheckCandles(c.c, now))
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
		for _, w := range c.want {
			if !got[w] {
				t.Fatalf("%s: missing %s in %v", c.name, w, got)
			}
		}
	}
	dup := bars(start, 100, 101)
	dup[1].Date = dup[0].Date
	if !kinds(CheckCandles(dup, now))["BAD_ORDER"] {
		t.Fatal("duplicate date not flagged")
	}
	if len(CheckCandles(nil, now)) != 0 {
		t.Fatal("empty series must not panic or flag")
	}
}
