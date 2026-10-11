package watchlist

import (
	"math"
	"sort"

	"github.com/aayushgogia/stock-research-tool/internal/config"
)

type DecisionKind string

const (
	DecisionTrigger   DecisionKind = "TRIGGER"
	DecisionNear      DecisionKind = "NEAR TRIGGER"
	DecisionAttention DecisionKind = "ATTENTION"
)

type Candidate struct {
	Ticker        string
	Kind          DecisionKind
	Held          bool
	WeekChangePct float64
	Events        []AttentionEvent
	WeeksActive   int
}

// Candidates merges deterministic trigger and attention reasons by ticker and
// returns at most five ranked names plus overflow symbols.
func Candidates(items []Candidate) (selected []Candidate, overflow []string) {
	tier := func(c Candidate) int {
		switch c.Kind {
		case DecisionTrigger:
			return 1
		case DecisionNear:
			return 2
		}
		if c.Held {
			return 3
		}
		if c.WeeksActive == 1 {
			return 4
		}
		return 5
	}
	sort.SliceStable(items, func(i, j int) bool {
		ti, tj := tier(items[i]), tier(items[j])
		if ti != tj {
			return ti < tj
		}
		ai, aj := math.Abs(items[i].WeekChangePct), math.Abs(items[j].WeekChangePct)
		if ai != aj {
			return ai > aj
		}
		return items[i].Ticker < items[j].Ticker
	})
	if len(items) <= 5 {
		return items, nil
	}
	for _, c := range items[5:] {
		overflow = append(overflow, c.Ticker)
	}
	return items[:5], overflow
}

func NumericCandidate(w config.Watch, price, rsi, band float64) (DecisionKind, bool) {
	if w.Trigger.Type == "none" {
		return "", false
	}
	level := 0.0
	switch w.Trigger.Type {
	case "price_below":
		level = *w.Trigger.Value
		if price <= level {
			return DecisionTrigger, true
		}
		if price <= level*(1+band/100) {
			return DecisionNear, true
		}
	case "price_above":
		level = *w.Trigger.Value
		if price >= level {
			return DecisionTrigger, true
		}
		if price >= level*(1-band/100) {
			return DecisionNear, true
		}
	case "rsi_below":
		level = *w.Trigger.Value
		if rsi <= level {
			return DecisionTrigger, true
		}
		if rsi <= level*(1+band/100) {
			return DecisionNear, true
		}
	case "price_above_pct_from_entry":
		level = *w.Trigger.Entry * (1 + *w.Trigger.Pct/100)
		if price >= level {
			return DecisionTrigger, true
		}
		if price >= level*(1-band/100) {
			return DecisionNear, true
		}
	}
	return "", false
}
