package watchlist

import "testing"

func series(n int, first, last float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = first
	}
	v[n-1] = last
	return v
}

func hasKind(events []AttentionEvent, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestMonthlyMove(t *testing.T) {
	cfg := attentionConfig()
	cfg.MonthlyMovePct = 20
	cases := []struct {
		name string
		v    []float64
		want bool
	}{
		{"FICO-style -30.5% over 21 sessions", series(22, 100, 69.5), true},
		{"exactly +20.00%", series(22, 100, 120), true},
		{"exactly -20.00%", series(22, 100, 80), true},
		{"just under 20.00%", series(22, 100, 119.99), false},
	}
	for _, c := range cases {
		events, skipped := Attention("X", candles(c.v), cfg)
		if hasKind(events, "MONTHLY_MOVE") != c.want {
			t.Fatalf("%s: events=%+v skipped=%v", c.name, events, skipped)
		}
	}
	events, _ := Attention("FICO", candles(series(22, 100, 69.5)), cfg)
	for _, e := range events {
		if e.Kind == "MONTHLY_MOVE" && (!e.Negative || e.Fact != "-30.50% move over 21 sessions") {
			t.Fatalf("event=%+v", e)
		}
	}
}

func TestMonthlyMoveSkippedWithFewerThan22Bars(t *testing.T) {
	cfg := attentionConfig()
	cfg.MonthlyMovePct = 20
	events, skipped := Attention("X", candles(series(21, 100, 50)), cfg)
	if hasKind(events, "MONTHLY_MOVE") {
		t.Fatalf("fired with 21 bars: %+v", events)
	}
	found := false
	for _, s := range skipped {
		found = found || s == "MONTHLY_MOVE"
	}
	if !found {
		t.Fatalf("skipped=%v", skipped)
	}
}
