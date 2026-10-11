package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAllowsNoneTrigger(t *testing.T) {
	p := filepath.Join(t.TempDir(), "watchlist.yaml")
	y := "watchlist:\n  - ticker: AMD\n    held: false\n    trigger: {type: none, value: null}\n    rationale: watch only\n    news_query: Advanced Micro Devices\n"
	if err := os.WriteFile(p, []byte(y), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Watchlist[0].Trigger.Type; got != "none" {
		t.Fatalf("got %q", got)
	}
}

func TestParseAllowsNoneTrigger(t *testing.T) {
	y := []byte("watchlist:\n  - ticker: AMD\n    held: false\n    trigger: {type: none, value: null}\n    rationale: watch only\n    news_query: Advanced Micro Devices\n")
	f, err := Parse(y)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Watchlist[0].Ticker; got != "AMD" {
		t.Fatalf("ticker = %q", got)
	}
}

func TestNewBlocksDefaultAndParse(t *testing.T) {
	base := "watchlist:\n  - ticker: AMD\n    rationale: r\n    news_query: AMD\n    trigger: {type: none}\n"
	f, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if f.Attention.MonthlyMovePct != 20 || f.Review.MaxItems != 3 || f.Review.FYIMaxListed != 15 || f.Memory.WaitedSuppressWeeks != 4 || f.Memory.DecisionContextWeeks != 8 {
		t.Fatalf("defaults: %+v %+v %+v", f.Attention, f.Review, f.Memory)
	}
	// An older attention block without monthly_move_pct still gets the default.
	old := "attention: {pct_below_52w_high: 15, rsi_low: 30, rsi_high: 70, weekly_move_pct: 8, sma200_cross: true, new_52w_extreme: true, approaching_band_pct: 5}\n" + base
	if f, err = Parse([]byte(old)); err != nil || f.Attention.MonthlyMovePct != 20 {
		t.Fatalf("%v %+v", err, f.Attention)
	}
	custom := "review: {max_items: 2, fyi_max_listed: 5}\nmemory: {waited_suppress_weeks: 6, decision_context_weeks: 10}\n" + base
	if f, err = Parse([]byte(custom)); err != nil || f.Review.MaxItems != 2 || f.Memory.WaitedSuppressWeeks != 6 {
		t.Fatalf("%v %+v", err, f)
	}
	if _, err = Parse([]byte("review: {max_items: -1, fyi_max_listed: 5}\n" + base)); err == nil {
		t.Fatal("negative max_items must be rejected")
	}
}
