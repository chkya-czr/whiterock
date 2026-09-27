package llm

import "testing"

func TestParsePulseRejectsUntraceableNumber(t *testing.T) {
	_, err := ParsePulse(`{"pulse_summary":"The price moved to 101."}`, []float64{100})
	if err == nil {
		t.Fatal("hallucinated pulse number passed validation")
	}
}

func TestPulsePromptIncludesOnlyUnflaggedFacts(t *testing.T) {
	_, user, allowed := PulsePrompt([]PulseFact{{Ticker: "QUIET", Price: 10, WeekChangePct: 1, MonthChangePct: 2, RSI14: 50}})
	if user == "" || len(allowed) != 4 {
		t.Fatalf("unexpected pulse prompt: %q %#v", user, allowed)
	}
}
