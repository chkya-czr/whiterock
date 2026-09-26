package watchlist

import (
	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
	"testing"
)

func f(v float64) *float64 { return &v }
func TestNumericTriggerAndApproach(t *testing.T) {
	w := config.Watch{Ticker: "X", Rationale: "r", Trigger: config.Trigger{Type: "price_below", Value: f(100)}}
	if r := Evaluate(w, 100, marketdata.Indicators{}, MacroConditions{}); r.Status != Triggered {
		t.Fatal(r)
	}
	if r := Evaluate(w, 104, marketdata.Indicators{}, MacroConditions{}); r.Status != Approaching {
		t.Fatal(r)
	}
	if r := Evaluate(w, 106, marketdata.Indicators{}, MacroConditions{}); r.Status != Quiet {
		t.Fatal(r)
	}
}
func TestNoneIsVisibleWatchOnly(t *testing.T) {
	w := config.Watch{Ticker: "X", Rationale: "r", Trigger: config.Trigger{Type: "none"}}
	r := Evaluate(w, 1, marketdata.Indicators{}, MacroConditions{})
	if !r.WatchOnly || r.Status != Quiet {
		t.Fatal(r)
	}
}
