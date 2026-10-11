package watchlist

import (
	"fmt"
	"math"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

// AttentionEvent is one deterministic observation. Short is the compact label
// used in the FYI list; Negative marks events that point down or weaker.
type AttentionEvent struct {
	Ticker   string `json:"ticker"`
	Kind     string `json:"kind"`
	Fact     string `json:"fact"`
	Short    string `json:"short,omitempty"`
	Negative bool   `json:"negative,omitempty"`
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
	add := func(kind, fact, short string, negative bool) {
		events = append(events, AttentionEvent{Ticker: ticker, Kind: kind, Fact: fact, Short: short, Negative: negative})
	}

	if len(closes) < 252 {
		skipped = append(skipped, "BELOW_52W_HIGH")
	} else {
		high := max(closes[len(closes)-252:])
		below := (1 - closes[len(closes)-1]/high) * 100
		if below >= c.PctBelow52WHigh {
			add("BELOW_52W_HIGH", fmt.Sprintf("%.2f%% below 52-week high of %.2f", below, high), fmt.Sprintf("%.1f%% below 52-week high", below), true)
		}
	}
	if len(closes) < 15 {
		skipped = append(skipped, "RSI")
	} else if rsi, err := marketdata.RSI(closes, 14); err == nil {
		if rsi < c.RSILow {
			add("RSI_LOW", fmt.Sprintf("RSI(14) %.2f below %.2f", rsi, c.RSILow), fmt.Sprintf("RSI %.1f", rsi), false)
		}
		if rsi > c.RSIHigh {
			add("RSI_HIGH", fmt.Sprintf("RSI(14) %.2f above %.2f", rsi, c.RSIHigh), fmt.Sprintf("RSI %.1f", rsi), false)
		}
	}
	if len(closes) < 6 {
		skipped = append(skipped, "WEEKLY_MOVE")
	} else {
		move := (closes[len(closes)-1]/closes[len(closes)-6] - 1) * 100
		if math.Abs(move) >= c.WeeklyMovePct {
			add("WEEKLY_MOVE", fmt.Sprintf("%.2f%% move over 5 sessions", move), fmt.Sprintf("%+.1f%% wk", move), move < 0)
		}
	}
	// Rounded to the two decimals shown in the email so that a move of exactly
	// the configured threshold fires despite binary floating-point error.
	if len(closes) < 22 {
		skipped = append(skipped, "MONTHLY_MOVE")
	} else {
		move := math.Round((closes[len(closes)-1]/closes[len(closes)-22]-1)*100*100) / 100
		if c.MonthlyMovePct > 0 && math.Abs(move) >= c.MonthlyMovePct {
			add("MONTHLY_MOVE", fmt.Sprintf("%.2f%% move over 21 sessions", move), fmt.Sprintf("%+.1f%% mo", move), move < 0)
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
			add("SMA200_CROSS_UP", fmt.Sprintf("close %.2f crossed above SMA200 %.2f", closes[len(closes)-1], latestSMA), "crossed above SMA200", false)
		} else if latest < 0 && prior >= 0 {
			add("SMA200_CROSS_DOWN", fmt.Sprintf("close %.2f crossed below SMA200 %.2f", closes[len(closes)-1], latestSMA), "crossed below SMA200", true)
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
				add("NEW_52W_HIGH", fmt.Sprintf("new 52-week high %.2f above prior %.2f", close, high), "new 52-week high", false)
				break
			}
		}
		for _, close := range closes[len(closes)-5:] {
			if close < low {
				add("NEW_52W_LOW", fmt.Sprintf("new 52-week low %.2f below prior %.2f", close, low), "new 52-week low", true)
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
