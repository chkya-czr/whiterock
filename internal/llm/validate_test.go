package llm

import "testing"

func TestRejectsHallucinatedNumber(t *testing.T) {
	if err := ValidateNumbers("Price is 101.00", []float64{100}); err == nil {
		t.Fatal("hallucinated number passed")
	}
	if err := ValidateNumbers("Price is $100.00", []float64{100}); err != nil {
		t.Fatal(err)
	}
}
