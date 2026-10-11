package watchlist

import (
	"fmt"
	"math"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

type AttentionEvent struct {
	Ticker string `json:"ticker"`
	Kind   string `json:"kind"`
	Fact   string `json:"fact"`
}

// ActiveWeeks counts consecutive weekly appearances for this ticker's active
// event kinds from the previous persisted snapshot.
func ActiveWeeks(events []AttentionEvent, previous []string, prior map[string]int) map[string]int {
	was := map[string]bool{}
	for _, k := range previous {
		was[k] = true
	}
	out := map[string]int{}
	for _, e := range events {
		if was[e.Kind] {
			out[e.Kind] = prior[e.Kind] + 1
		} else {
			out[e.Kind] = 1
		}
	}
	return out
}

// Attention evaluates only supplied daily closes. Rules lacking their required
// history are skipped; callers can log the returned rule names without turning
// a newly listed symbol into a failed run.
func Attention(ticker string, candles []marketdata.Candle, c config.Attention) ([]AttentionEvent, []string) {
	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	var events []AttentionEvent
	var skipped []string
	add := func(kind, fact string) {
		events = append(events, AttentionEvent{Ticker: ticker, Kind: kind, Fact: fact})
	}

	if len(closes) < 252 {
		skipped = append(skipped, "BELOW_52W_HIGH")
	} else {
		high := max(closes[len(closes)-252:])
		below := (1 - closes[len(closes)-1]/high) * 100
		if below >= c.PctBelow52WHigh {
			add("BELOW_52W_HIGH", fmt.Sprintf("%.2f%% below 52-week high of %.2f", below, high))
		}
	}
	if len(closes) < 15 {
		skipped = append(skipped, "RSI")
	} else if rsi, err := marketdata.RSI(closes, 14); err == nil {
		if rsi < c.RSILow {
			add("RSI_LOW", fmt.Sprintf("RSI(14) %.2f below %.2f", rsi, c.RSILow))
		}
		if rsi > c.RSIHigh {
			add("RSI_HIGH", fmt.Sprintf("RSI(14) %.2f above %.2f", rsi, c.RSIHigh))
		}
	}
	if len(closes) < 6 {
		skipped = append(skipped, "WEEKLY_MOVE")
	} else {
		move := (closes[len(closes)-1]/closes[len(closes)-6] - 1) * 100
		if math.Abs(move) >= c.WeeklyMovePct {
			add("WEEKLY_MOVE", fmt.Sprintf("%.2f%% move over 5 sessions", move))
		}
	}
	if !c.SMA200Cross {
		// Explicitly disabled in configuration.
	} else if len(closes) < 205 {
		skipped = append(skipped, "SMA200_CROSS")
	} else {
		latestSMA, _ := marketdata.SMA(closes, 200)
		priorSMA, _ := marketdata.SMA(closes[:len(closes)-5], 200)
		latest := closes[len(closes)-1] - latestSMA
		prior := closes[len(closes)-6] - priorSMA
		if latest >= 0 && prior < 0 {
			add("SMA200_CROSS_UP", fmt.Sprintf("close %.2f crossed above SMA200 %.2f", closes[len(closes)-1], latestSMA))
		} else if latest < 0 && prior >= 0 {
			add("SMA200_CROSS_DOWN", fmt.Sprintf("close %.2f crossed below SMA200 %.2f", closes[len(closes)-1], latestSMA))
		}
	}
	if !c.New52WExtreme {
		// Explicitly disabled in configuration.
	} else if len(closes) < 257 {
		skipped = append(skipped, "NEW_52W_EXTREME")
	} else {
		prior := closes[len(closes)-257 : len(closes)-5]
		high, low := max(prior), min(prior)
		for _, close := range closes[len(closes)-5:] {
			if close > high {
				add("NEW_52W_HIGH", fmt.Sprintf("new 52-week high %.2f above prior %.2f", close, high))
				break
			}
		}
		for _, close := range closes[len(closes)-5:] {
			if close < low {
				add("NEW_52W_LOW", fmt.Sprintf("new 52-week low %.2f below prior %.2f", close, low))
				break
			}
		}
	}
	return events, skipped
}

func max(v []float64) float64 {
	x := v[0]
	for _, n := range v[1:] {
		if n > x {
			x = n
		}
	}
	return x
}
func min(v []float64) float64 {
	x := v[0]
	for _, n := range v[1:] {
		if n < x {
			x = n
		}
	}
	return x
}
