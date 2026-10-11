package watchlist

import (
	"github.com/aayushgogia/stock-research-tool/internal/config"
)

type DecisionKind string

const (
	DecisionTrigger   DecisionKind = "TRIGGER"
	DecisionNear      DecisionKind = "NEAR TRIGGER"
	DecisionAttention DecisionKind = "ATTENTION"
)

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
