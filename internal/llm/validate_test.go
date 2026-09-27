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

func TestParseMacroAcceptsNotableShiftArray(t *testing.T) {
	raw := `{"regime_tag":"steady conditions","summary":"Conditions are stable.","notable_shifts":["Credit remains tight."]}`
	v, err := ParseMacro(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.NotableShifts != "Credit remains tight." {
		t.Fatalf("got %q", v.NotableShifts)
	}
}
