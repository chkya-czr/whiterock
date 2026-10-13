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

func TestSubjectLines(t *testing.T) {
	got := Subject(Digest{Week: "2026-10-12", Act: []Item{{}}, Review: []Item{{}, {}}})
	if got != "Watchlist - week of 2026-10-12 - 1 act, 2 review" {
		t.Fatal(got)
	}
	if got := Subject(Digest{Week: "2026-10-12"}); got != "Watchlist - week of 2026-10-12 - nothing to act on" {
		t.Fatal(got)
	}
}

func TestRenderEmptyStatesAndSectionOrder(t *testing.T) {
	got := Render(Digest{Week: "2026-10-12", FYI: []string{"MSFT (RSI 72.3, ongoing)"}, FYIMore: 4, Holdings: []Holding{{Ticker: "AAA", Price: 1}}})
	for _, want := range []string{"ACT: nothing this week.", "REVIEW: nothing new this week.", "MSFT (RSI 72.3, ongoing)", "+4 more"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	order := []string{"ACT: nothing", "REVIEW: nothing", "\nHoldings\n", "\nFYI\n", "Macro backdrop", "Watchlist pulse"}
	last := -1
	for _, o := range order {
		i := strings.Index(got, o)
		if i <= last {
			t.Fatalf("section %q out of order:\n%s", o, got)
		}
		last = i
	}
	if strings.Contains(got, "From last week") {
		t.Fatalf("From last week must be omitted when absent:\n%s", got)
	}
}

func TestRenderFailedRatingShowsOneUnavailableLineAndNoPlaceholderCases(t *testing.T) {
	got := Render(Digest{Week: "2026-10-12", Review: []Item{{
		Header:    "FICO - REVIEW",
		Lines:     []string{"-30.50% move over 21 sessions (held, 4%)"},
		Headlines: []Headline{{Title: "FICO falls", Source: "reuters.com", URL: "https://example.test/a", Date: "2026-10-10"}},
		Model:     Model{Unavailable: "output cited an unsupported number"},
		Question:  "What changed? Thesis break, or re-rating?",
	}}})
	if strings.Count(got, "Model rating: unavailable") != 1 {
		t.Fatalf("want exactly one unavailable line:\n%s", got)
	}
	for _, bad := range []string{"For:", "Against:", "Would change if", "Case for", "Case against", "NO RATING"} {
		if strings.Contains(got, bad) {
			t.Fatalf("placeholder %q rendered:\n%s", bad, got)
		}
	}
	for _, want := range []string{"FICO - REVIEW", "(held, 4%)", "[FICO falls](https://example.test/a)", "Question for review: What changed?"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestRenderValidatedRatingAnnotation(t *testing.T) {
	got := Render(Digest{Week: "2026-10-12", Act: []Item{{Header: "AMD - ACT - TRIGGER", Model: Model{OK: true, Rating: "BUY", Confidence: "MEDIUM", Summary: "facts", CaseFor: "for", CaseAgainst: "against", WouldChangeIf: "change", Drivers: []Driver{{Claim: "claim", Source: Source{Label: "reuters.com", URL: "https://example.test/x", Date: "2026-10-09"}}}}}}})
	for _, want := range []string{"Model: BUY (MEDIUM) - facts", "For: for", "Against: against", "Driver [reuters.com](https://example.test/x) (2026-10-09): claim", "Would change if: change"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestRenderLastWeekVariants(t *testing.T) {
	open := Render(Digest{LastWeek: LastWeek{Show: true, Lines: []string{"AMD: still open (ATTENTION) - now 1.00. Log your decision in the Sheet."}, More: 2}})
	if !strings.Contains(open, "From last week\nAMD: still open") || !strings.Contains(open, "+2 more") {
		t.Fatal(open)
	}
	if got := Render(Digest{LastWeek: LastWeek{Show: true, AllResolved: true}}); !strings.Contains(got, "All items from last week have a logged decision.") {
		t.Fatal(got)
	}
	if got := Render(Digest{LastWeek: LastWeek{Show: true, Unavailable: true}}); !strings.Contains(got, "From last week\nDecision memory unavailable this week") {
		t.Fatal(got)
	}
}

func TestRenderHoldingsNonHoldFirstThenWeeklyMove(t *testing.T) {
	ok := func(r string) Model { return Model{OK: true, Rating: r, Confidence: "HIGH", Summary: "sum"} }
	got := Render(Digest{Holdings: []Holding{
		{Ticker: "H1", Price: 10, WeekChangePct: -9, Model: ok("HOLD")},
		{Ticker: "H2", Price: 10, WeekChangePct: 1, Model: ok("TRIM")},
		{Ticker: "H3", Price: 10, WeekChangePct: 3},
	}})
	i1, i2, i3 := strings.Index(got, "H1:"), strings.Index(got, "H2:"), strings.Index(got, "H3:")
	if !(i2 < i1 && i1 < i3) {
		t.Fatalf("order wrong:\n%s", got)
	}
	if !strings.Contains(got, "H3: 10.00 (+3.00% wk) - no rating") || !strings.Contains(got, "H2: 10.00 (+1.00% wk) - TRIM (HIGH)\n  sum") {
		t.Fatal(got)
	}
}

func TestRenderShowsDataCheckNotes(t *testing.T) {
	got := Render(Digest{Review: []Item{{Header: "XYZ - REVIEW", Lines: []string{"fact"}, Notes: []string{"largest one-day move is 48.0% on 2026-10-01; confirm it is not a split or data error"}, Model: Model{Unavailable: "x"}}}})
	if !strings.Contains(got, "fact\nData check: largest one-day move is 48.0%") {
		t.Fatal(got)
	}
}
