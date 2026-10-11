package watchlist

import (
	"testing"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

func candles(values []float64) []marketdata.Candle {
	out := make([]marketdata.Candle, len(values))
	for i, v := range values {
		out[i] = marketdata.Candle{Date: time.Date(2025, 1, i+1, 0, 0, 0, 0, time.UTC), Close: v}
	}
	return out
}

func attentionConfig() config.Attention {
	return config.Attention{PctBelow52WHigh: 15, RSILow: 30, RSIHigh: 70, WeeklyMovePct: 8, SMA200Cross: true, New52WExtreme: true, ApproachingBand: 5}
}

func TestAttentionBelow52WeekHighExactBoundary(t *testing.T) {
	v := make([]float64, 252)
	for i := range v {
		v[i] = 100
	}
	v[0], v[len(v)-1] = 120, 102 // exactly fifteen percent below 120
	events, _ := Attention("X", candles(v), attentionConfig())
	if len(events) == 0 || events[0].Kind != "BELOW_52W_HIGH" {
		t.Fatalf("events = %+v", events)
	}
}

func TestAttentionInsufficientHistorySkipsWithoutError(t *testing.T) {
	v := make([]float64, 74)
	for i := range v {
		v[i] = 100
	}
	_, skipped := Attention("SPCX", candles(v), attentionConfig())
	if len(skipped) == 0 {
		t.Fatalf("skipped=%v", skipped)
	}
}

func TestAttentionNewHigh(t *testing.T) {
	v := make([]float64, 257)
	for i := range v {
		v[i] = 100
	}
	v[len(v)-1] = 101
	events, _ := Attention("X", candles(v), attentionConfig())
	found := false
	for _, e := range events {
		found = found || e.Kind == "NEW_52W_HIGH"
	}
	if !found {
		t.Fatalf("events=%+v", events)
	}
}
