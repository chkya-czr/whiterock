package macro

import (
	"fmt"
	"time"
)

// Context is the full set of values allowed in the macro narrative.
type Context struct {
	FedFunds, CurveSpreadBps, CorePCEYoY, NetLiquidityB, Liquidity8WeekPct, Dollar, Dollar1MonthPct, HYSpreadBps, VIX float64
	CurveState, LiquidityTrend, DollarTrend, CreditRegime, VIXRegime                                                  string
	SectorRS                                                                                                          map[string]float64
}

func Latest(v []Observation) (float64, error) {
	if len(v) == 0 {
		return 0, fmt.Errorf("no observations")
	}
	return v[len(v)-1].Value, nil
}
func ChangePct(v []Observation, periods int) (float64, error) {
	if len(v) <= periods {
		return 0, fmt.Errorf("need %d observations", periods+1)
	}
	a, b := v[len(v)-periods-1].Value, v[len(v)-1].Value
	if a == 0 {
		return 0, fmt.Errorf("cannot calculate change from zero")
	}
	return (b/a - 1) * 100, nil
}
func NetLiquidity(walcl, tga, rrp []Observation) ([]Observation, error) {
	// WALCL is the weekly reference calendar. TGA and RRP have different
	// frequencies, so select each as of the same (or preceding) WALCL date;
	// slice-index alignment would create false trends.
	out := make([]Observation, 0, len(walcl))
	for _, w := range walcl {
		t, okT := asOf(tga, w.Date)
		r, okR := asOf(rrp, w.Date)
		if okT && okR {
			out = append(out, Observation{Date: w.Date, Value: w.Value - t - r})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no date-aligned liquidity observations")
	}
	return out, nil
}
func asOf(v []Observation, date time.Time) (float64, bool) {
	for i := len(v) - 1; i >= 0; i-- {
		if !v[i].Date.After(date) {
			return v[i].Value, true
		}
	}
	return 0, false
}
func Regimes(c *Context) {
	if c.CurveSpreadBps < 0 {
		c.CurveState = "inverted"
	} else {
		c.CurveState = "normal"
	}
	if c.Liquidity8WeekPct >= 0 {
		c.LiquidityTrend = "expanding"
	} else {
		c.LiquidityTrend = "contracting"
	}
	if c.Dollar1MonthPct < 0 {
		c.DollarTrend = "weakening"
	} else {
		c.DollarTrend = "strengthening"
	}
	if c.HYSpreadBps >= 500 {
		c.CreditRegime = "widening"
	} else {
		c.CreditRegime = "tight"
	}
	if c.VIX >= 30 {
		c.VIXRegime = "high"
	} else if c.VIX >= 20 {
		c.VIXRegime = "elevated"
	} else {
		c.VIXRegime = "low"
	}
}
