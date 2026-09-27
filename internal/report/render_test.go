package report

import (
	"strings"
	"testing"
)

func TestRenderUsesMacroSummaryAsPrimaryLine(t *testing.T) {
	got := Render(Digest{Week: "2026-09-27", MacroSummary: "Macro conditions remain mixed.", MacroDetails: "Fed funds 3.88% · VIX 14.21"})
	if !strings.Contains(got, "Macro backdrop\nMacro conditions remain mixed.\nDetails: Fed funds") {
		t.Fatalf("macro summary was not primary:\n%s", got)
	}
	if strings.Contains(got, "Macro backdrop\nFed funds") {
		t.Fatalf("raw details appeared as primary:\n%s", got)
	}
}

func TestRenderPulseListsQuietBeforeWatchOnly(t *testing.T) {
	got := Render(Digest{MacroSummary: "Summary", Pulse: []PulseItem{{Ticker: "QUIET", Status: "QUIET", Price: 1, RSI14: 2}, {Ticker: "WATCH", Status: "WATCH-ONLY-NO-RULE", Price: 3, RSI14: 4}}})
	if strings.Index(got, "QUIET:") > strings.Index(got, "WATCH:") {
		t.Fatalf("pulse ordering incorrect:\n%s", got)
	}
}
