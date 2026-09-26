package macro

import (
	"context"
	"fmt"
	"sort"

	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

// Build fetches each official series once, then applies local arithmetic. Units
// are preserved from FRED: WALCL/TGA/RRP are millions and converted to billions.
func Build(ctx context.Context, f *Fred, md marketdata.Client) (Context, error) {
	ids := []string{"DFF", "DGS10", "DGS2", "PCEPILFE", "WALCL", "WTREGEN", "RRPONTSYD", "DTWEXBGS", "BAMLH0A0HYM2", "VIXCLS"}
	series := map[string][]Observation{}
	for _, id := range ids {
		v, e := f.Series(ctx, id, 120)
		if e != nil {
			return Context{}, e
		}
		series[id] = v
	}
	get := func(id string) (float64, error) { return Latest(series[id]) }
	var c Context
	var e error
	if c.FedFunds, e = get("DFF"); e != nil {
		return c, e
	}
	ten, e := get("DGS10")
	if e != nil {
		return c, e
	}
	two, e := get("DGS2")
	if e != nil {
		return c, e
	}
	c.CurveSpreadBps = (ten - two) * 100
	// PCEPILFE is an index level, so calculate the actual YoY percentage.
	if c.CorePCEYoY, e = ChangePct(series["PCEPILFE"], 12); e != nil {
		return c, e
	}
	wal, e := get("WALCL")
	if e != nil {
		return c, e
	}
	tga, e := get("WTREGEN")
	if e != nil {
		return c, e
	}
	rrp, e := get("RRPONTSYD")
	if e != nil {
		return c, e
	}
	c.NetLiquidityB = (wal - tga - rrp) / 1000
	liq, e := NetLiquidity(series["WALCL"], series["WTREGEN"], series["RRPONTSYD"])
	if e != nil {
		return c, e
	}
	c.Liquidity8WeekPct, e = ChangePct(liq, 8)
	if e != nil {
		return c, e
	}
	if c.Dollar, e = get("DTWEXBGS"); e != nil {
		return c, e
	}
	c.Dollar1MonthPct, e = ChangePct(series["DTWEXBGS"], 22)
	if e != nil {
		return c, e
	}
	if c.HYSpreadBps, e = get("BAMLH0A0HYM2"); e != nil {
		return c, e
	}
	c.HYSpreadBps *= 100 // FRED supplies percentage points; digest uses bps.
	if c.VIX, e = get("VIXCLS"); e != nil {
		return c, e
	}
	Regimes(&c)
	c.SectorRS, e = SectorRelativeStrength(ctx, md)
	if e != nil {
		return c, e
	}
	return c, nil
}
func SectorRelativeStrength(ctx context.Context, md marketdata.Client) (map[string]float64, error) {
	symbols := []string{"SPY", "SMH", "XLK", "XLF", "XLE", "GLD"}
	r := map[string]float64{}
	var spy float64
	for _, s := range symbols {
		v, e := md.Daily(ctx, s, 30)
		if e != nil {
			return nil, e
		}
		if len(v) < 21 {
			return nil, fmt.Errorf("%s has fewer than 21 prices", s)
		}
		change := (v[len(v)-1].Close/v[len(v)-21].Close - 1) * 100
		if s == "SPY" {
			spy = change
		} else {
			r[s] = change
		}
	}
	for s := range r {
		r[s] -= spy
	}
	return r, nil
}
func RankedSectorRS(rs map[string]float64) []string {
	out := make([]string, 0, len(rs))
	for k, v := range rs {
		out = append(out, fmt.Sprintf("%s %.2f%%", k, v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}
