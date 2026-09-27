package marketdata

import "testing"

func TestSMAAndRSI(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5}
	got, err := SMA(v, 3)
	if err != nil || got != 4 {
		t.Fatalf("SMA=%v %v", got, err)
	}
	r, err := RSI(v, 3)
	if err != nil || r != 100 {
		t.Fatalf("RSI=%v %v", r, err)
	}
}

func TestComputePulseAllowsPartialHistory(t *testing.T) {
	c := make([]Candle, 74)
	for i := range c {
		c[i].Close = float64(i + 1)
	}
	got, err := ComputePulse(c)
	if err != nil {
		t.Fatal(err)
	}
	if got.FullHistory || got.HistorySessions != 74 {
		t.Fatalf("unexpected history metadata: %+v", got)
	}
	if got.SMA50 != 0 || got.SMA200 != 0 || got.High52 != 0 || got.Low52 != 0 {
		t.Fatalf("partial pulse must not provide full-history measures: %+v", got)
	}
}
