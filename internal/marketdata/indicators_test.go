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
