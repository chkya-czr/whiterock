package watchlist

import (
	"fmt"
	"strings"
	"testing"
)

func TestContextLineVariants(t *testing.T) {
	full := &LoggedDecision{Week: "2026-10-05", Decision: "Waited", Reason: "wait for earnings", SnapshotFound: true, PriceThen: 100}
	if got := ContextLine(full, 90, testToday, 8); got != "You logged Waited on 2026-10-05: wait for earnings. Price then 100.00, now 90.00 (-10.00%)." {
		t.Fatal(got)
	}
	noPrice := &LoggedDecision{Week: "2026-10-05", Decision: "Waited", Reason: "wait for earnings"}
	if got := ContextLine(noPrice, 90, testToday, 8); got != "You logged Waited on 2026-10-05: wait for earnings." {
		t.Fatal(got)
	}
	noReason := &LoggedDecision{Week: "2026-10-05", Decision: "Bought", SnapshotFound: true, PriceThen: 50}
	if got := ContextLine(noReason, 55, testToday, 8); got != "You logged Bought on 2026-10-05. Price then 50.00, now 55.00 (+10.00%)." {
		t.Fatal(got)
	}
	old := &LoggedDecision{Week: "2026-07-01", Decision: "Bought"}
	if got := ContextLine(old, 55, testToday, 8); got != "" {
		t.Fatalf("outside the context window: %q", got)
	}
	if ContextLine(nil, 1, testToday, 8) != "" {
		t.Fatal("nil decision")
	}
}

func TestWaitedQuestion(t *testing.T) {
	if got := WaitedQuestion(&LoggedDecision{Week: "2026-09-07"}); got != "Still waiting? What has changed since 2026-09-07?" {
		t.Fatal(got)
	}
}

func TestLastWeekOpenAndResolved(t *testing.T) {
	lines, more, all := LastWeekLines([]LastWeekInput{
		{Ticker: "ZZZ", Decision: "Bought", Reason: "thesis intact"},
		{Ticker: "AAA", Tag: "ATTENTION", PriceThen: 10, HavePriceThen: true, PriceNow: 12},
		{Ticker: "BBB", Tag: "TRIGGER", PriceNow: 7},
		{Ticker: "CCC", Decision: "Waited"},
	}, 10)
	want := []string{
		"AAA: still open (ATTENTION) - price then 10.00, now 12.00. Log your decision in the Sheet.",
		"BBB: still open (TRIGGER) - now 7.00. Log your decision in the Sheet.",
		"CCC: logged Waited",
		"ZZZ: logged Bought - thesis intact",
	}
	if more != 0 || all || strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines=%q more=%d all=%t", lines, more, all)
	}
}

func TestLastWeekCapAndAllResolved(t *testing.T) {
	var items []LastWeekInput
	for i := 0; i < 12; i++ {
		items = append(items, LastWeekInput{Ticker: fmt.Sprintf("T%02d", i), Tag: "ATTENTION", PriceNow: 1})
	}
	lines, more, all := LastWeekLines(items, 10)
	if len(lines) != 10 || more != 2 || all {
		t.Fatalf("%d %d %t", len(lines), more, all)
	}
	_, _, all = LastWeekLines([]LastWeekInput{{Ticker: "A", Decision: "Bought"}}, 10)
	if !all {
		t.Fatal("all resolved not detected")
	}
	_, _, all = LastWeekLines(nil, 10)
	if all {
		t.Fatal("empty last week must not claim all resolved")
	}
}
