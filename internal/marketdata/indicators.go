package marketdata

import "fmt"

type Indicators struct{ RSI14, SMA50, SMA200, High52, Low52, FromHighPct, FromLowPct, WeekChangePct, MonthChangePct float64 }

func Compute(c []Candle) (Indicators, error) {
	if len(c) < 252 {
		return Indicators{}, fmt.Errorf("need 252 daily closes, got %d", len(c))
	}
	closes := make([]float64, len(c))
	for i := range c {
		closes[i] = c[i].Close
	}
	last := closes[len(closes)-1]
	high, low := closes[len(closes)-252], closes[len(closes)-252]
	for _, v := range closes[len(closes)-252:] {
		if v > high {
			high = v
		}
		if v < low {
			low = v
		}
	}
	rsi, err := RSI(closes, 14)
	if err != nil {
		return Indicators{}, err
	}
	sma50, _ := SMA(closes, 50)
	sma200, _ := SMA(closes, 200)
	return Indicators{RSI14: rsi, SMA50: sma50, SMA200: sma200, High52: high, Low52: low, FromHighPct: (last/high - 1) * 100, FromLowPct: (last/low - 1) * 100, WeekChangePct: (last/closes[len(closes)-6] - 1) * 100, MonthChangePct: (last/closes[len(closes)-22] - 1) * 100}, nil
}
func SMA(v []float64, n int) (float64, error) {
	if len(v) < n {
		return 0, fmt.Errorf("need %d values", n)
	}
	var s float64
	for _, x := range v[len(v)-n:] {
		s += x
	}
	return s / float64(n), nil
}

// RSI uses Wilder's smoothing, not a remote vendor's potentially different convention.
func RSI(v []float64, n int) (float64, error) {
	if len(v) < n+1 {
		return 0, fmt.Errorf("need %d values", n+1)
	}
	var gain, loss float64
	start := len(v) - n - 1
	for i := start + 1; i <= start+n; i++ {
		d := v[i] - v[i-1]
		if d >= 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain /= float64(n)
	loss /= float64(n)
	if loss == 0 {
		return 100, nil
	}
	return 100 - 100/(1+gain/loss), nil
}
