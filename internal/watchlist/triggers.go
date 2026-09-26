// Package watchlist holds deterministic, side-effect-free rules.
package watchlist

import (
	"fmt"
	"math"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

type Status string

const (
	Triggered   Status = "TRIGGERED"
	Approaching Status = "APPROACHING"
	Quiet       Status = "QUIET"
)

type Result struct {
	Status       Status
	Comparison   string
	WatchOnly    bool
	MacroMatched bool
}

// Evaluate makes no qualitative judgement: qualitative rules are forwarded as
// APPROACHING for the human/LLM research brief, while numeric rules alone can
// resolve to TRIGGERED.
func Evaluate(w config.Watch, price float64, ind marketdata.Indicators, macro MacroConditions) Result {
	if w.Trigger.Type == "none" {
		return Result{Status: Quiet, Comparison: "watch only, no rule set", WatchOnly: true}
	}
	macroMatch := macro.Matches(w.MacroPrecondition)
	if w.Trigger.Type == "qualitative" {
		return Result{Status: Approaching, Comparison: "qualitative condition requires research: " + w.Trigger.Condition, MacroMatched: macroMatch}
	}
	var hit, near bool
	var cmp string
	switch w.Trigger.Type {
	case "price_below":
		hit = price <= *w.Trigger.Value
		near = price <= *w.Trigger.Value*1.05
		cmp = fmt.Sprintf("price (%.2f) %s stated level (%.2f)", price, map[bool]string{true: "is at or below", false: "is above"}[hit], *w.Trigger.Value)
	case "price_above":
		hit = price >= *w.Trigger.Value
		near = price >= *w.Trigger.Value*.95
		cmp = fmt.Sprintf("price (%.2f) %s stated level (%.2f)", price, map[bool]string{true: "is at or above", false: "is below"}[hit], *w.Trigger.Value)
	case "rsi_below":
		hit = ind.RSI14 <= *w.Trigger.Value
		near = ind.RSI14 <= *w.Trigger.Value*1.05
		cmp = fmt.Sprintf("RSI(14) (%.2f) %s stated level (%.2f)", ind.RSI14, map[bool]string{true: "is at or below", false: "is above"}[hit], *w.Trigger.Value)
	case "price_above_pct_from_entry":
		level := *w.Trigger.Entry * (1 + *w.Trigger.Pct/100)
		hit = price >= level
		near = price >= level*.95
		cmp = fmt.Sprintf("price (%.2f) %s entry target (%.2f = %.2f + %.2f%%)", price, map[bool]string{true: "is at or above", false: "is below"}[hit], level, *w.Trigger.Entry, *w.Trigger.Pct)
	}
	if hit {
		return Result{Status: Triggered, Comparison: cmp, MacroMatched: macroMatch}
	}
	// A macro precondition causes research to be generated even if absent/not
	// satisfied. It cannot turn an unmet numeric condition into a trigger.
	if near || w.MacroPrecondition != nil {
		return Result{Status: Approaching, Comparison: cmp, MacroMatched: macroMatch}
	}
	return Result{Status: Quiet, Comparison: cmp, MacroMatched: macroMatch}
}

type MacroConditions struct{ DollarWeak, SectorOutperforming, RiskOn bool }

func (m MacroConditions) Matches(p *string) bool {
	if p == nil {
		return false
	}
	switch *p {
	case "dollar_weak":
		return m.DollarWeak
	case "sector_outperforming":
		return m.SectorOutperforming
	case "risk_on":
		return m.RiskOn
	}
	return false
}
func NearlyEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
