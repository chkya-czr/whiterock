package watchlist

import (
	"strings"
	"testing"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
)

var testToday = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func testCfg() config.File {
	return config.File{
		Review: config.Review{MaxItems: 3, FYIMaxListed: 15},
		Memory: config.Memory{WaitedSuppressWeeks: 4, DecisionContextWeeks: 8},
	}
}

func ev(kind string) AttentionEvent { return AttentionEvent{Kind: kind, Short: kind} }

// newEvent builds an input with one event of kind, active for weeks weeks.
func in(ticker string, held bool, mag float64, weeks int, kinds ...string) Input {
	i := Input{Ticker: ticker, Held: held, WeekPct: mag, HasPrior: true, WeeksActive: map[string]int{}}
	for _, k := range kinds {
		i.Events = append(i.Events, ev(k))
		i.WeeksActive[k] = weeks
	}
	return i
}

func tickers(ps []Placement) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Ticker
	}
	return strings.Join(out, ",")
}

func TestACTIsNeverSuppressedAndIgnoresWaited(t *testing.T) {
	a := in("AAA", true, 1, 1, "RSI_LOW")
	a.Numeric = DecisionTrigger
	a.Logged = &LoggedDecision{Week: "2026-10-05", Decision: "Waited", SnapshotFound: true, KindsThen: []string{"RSI_LOW"}}
	got := Place([]Input{a}, testCfg(), testToday)
	if len(got.Act) != 1 || got.Act[0].Suppressed || len(got.Review)+len(got.FYI) != 0 {
		t.Fatalf("ACT must survive a Waited decision: %+v", got)
	}
}

func TestOngoingStateEventGoesToFYI(t *testing.T) {
	got := Place([]Input{in("MSFT", false, 2, 3, "RSI_HIGH")}, testCfg(), testToday)
	if len(got.Review) != 0 || len(got.FYI) != 1 || got.FYI[0].FYILabel != "RSI_HIGH, ongoing" {
		t.Fatalf("got %+v", got)
	}
}

func TestNewEventIsReviewCandidateAndQuietIsNotListed(t *testing.T) {
	q := in("QUIET", false, 0, 0)
	got := Place([]Input{in("AMD", false, 9, 1, "WEEKLY_MOVE"), q}, testCfg(), testToday)
	if tickers(got.Review) != "AMD" || len(got.Quiet) != 1 || got.Quiet[0].Listed {
		t.Fatalf("got %+v", got)
	}
}

func TestFirstRunTreatsStateKindsAsOngoingButKeepsOthers(t *testing.T) {
	i := in("NEW", false, 5, 1, "BELOW_52W_HIGH", "RSI_LOW", "WEEKLY_MOVE")
	i.HasPrior = false
	got := Place([]Input{i}, testCfg(), testToday)
	if len(got.Review) != 1 || len(got.Review[0].NewEvents) != 1 || got.Review[0].NewEvents[0].Kind != "WEEKLY_MOVE" || len(got.Review[0].OngoingEvents) != 2 {
		t.Fatalf("got %+v", got.Review)
	}
	only := in("ONLY", false, 5, 1, "BELOW_52W_HIGH", "RSI_HIGH")
	only.HasPrior = false
	got = Place([]Input{only}, testCfg(), testToday)
	if len(got.Review) != 0 || len(got.FYI) != 1 {
		t.Fatalf("state-only first run should be FYI: %+v", got)
	}
}

func TestMagnitudeUsesLargerOfWeeklyAndMonthly(t *testing.T) {
	a := in("AAA", false, 2, 1, "MONTHLY_MOVE")
	a.MonthPct = -30
	b := in("BBB", false, 10, 1, "WEEKLY_MOVE")
	got := Place([]Input{b, a}, testCfg(), testToday)
	if tickers(got.Review) != "AAA,BBB" {
		t.Fatalf("order %s", tickers(got.Review))
	}
}

func TestSelectionGuaranteesHeldAndNonHeld(t *testing.T) {
	cands := []Input{
		in("H1", true, 30, 1, "WEEKLY_MOVE"), in("H2", true, 29, 1, "WEEKLY_MOVE"), in("H3", true, 28, 1, "WEEKLY_MOVE"),
		in("N1", false, 12, 1, "WEEKLY_MOVE"), in("N2", false, 11, 1, "WEEKLY_MOVE"), in("N3", false, 10, 1, "WEEKLY_MOVE"),
	}
	got := Place(cands, testCfg(), testToday)
	if tickers(got.Review) != "H1,H2,N1" {
		t.Fatalf("review=%s", tickers(got.Review))
	}
	if len(got.FYI) != 3 {
		t.Fatalf("overflow=%d", len(got.FYI))
	}
	for _, p := range got.FYI {
		if !p.Overflow || p.FYILabel != "WEEKLY_MOVE, new this week" {
			t.Fatalf("overflow label %+v", p)
		}
	}
}

func TestSelectionDoesNotReserveSlotWhenOnlyHeld(t *testing.T) {
	cands := []Input{in("H1", true, 4, 1, "WEEKLY_MOVE"), in("H2", true, 3, 1, "WEEKLY_MOVE"), in("H3", true, 2, 1, "WEEKLY_MOVE"), in("H4", true, 1, 1, "WEEKLY_MOVE")}
	got := Place(cands, testCfg(), testToday)
	if tickers(got.Review) != "H1,H2,H3" {
		t.Fatalf("review=%s", tickers(got.Review))
	}
}

func waited(week string, snapshot bool, kinds ...string) *LoggedDecision {
	return &LoggedDecision{Week: week, Decision: "Waited", SnapshotFound: snapshot, KindsThen: kinds}
}

func TestWaitedSuppression(t *testing.T) {
	cases := []struct {
		name       string
		logged     *LoggedDecision
		newKind    string
		wantReview bool
		resurfaced bool
	}{
		{"within window, same kinds", waited("2026-10-05", true, "RSI_LOW"), "RSI_LOW", false, false},
		{"within window, new kind", waited("2026-10-05", true, "RSI_LOW"), "WEEKLY_MOVE", true, false},
		{"after window", waited("2026-09-07", true, "RSI_LOW"), "RSI_LOW", true, true},
		{"just inside window", waited("2026-09-15", true, "RSI_LOW"), "RSI_LOW", false, false},
		{"missing snapshot", waited("2026-10-05", false), "RSI_LOW", true, false},
		{"other decision is not suppressed", &LoggedDecision{Week: "2026-10-05", Decision: "Bought", SnapshotFound: true, KindsThen: []string{"RSI_LOW"}}, "RSI_LOW", true, false},
	}
	for _, c := range cases {
		i := in("FICO", false, 20, 1, c.newKind)
		i.Logged = c.logged
		got := Place([]Input{i}, testCfg(), testToday)
		if (len(got.Review) == 1) != c.wantReview {
			t.Fatalf("%s: review=%d fyi=%+v", c.name, len(got.Review), got.FYI)
		}
		if c.wantReview && got.Review[0].Resurfaced != c.resurfaced {
			t.Fatalf("%s: resurfaced=%t", c.name, got.Review[0].Resurfaced)
		}
		if !c.wantReview && (len(got.FYI) != 1 || got.FYI[0].FYILabel != "waited "+c.logged.Week || !got.FYI[0].Suppressed) {
			t.Fatalf("%s: %+v", c.name, got.FYI)
		}
	}
}

func TestSuppressedWaitedDoesNotConsumeReviewSlot(t *testing.T) {
	sup := in("SUP", false, 50, 1, "RSI_LOW")
	sup.Logged = waited("2026-10-05", true, "RSI_LOW")
	cands := []Input{sup, in("A", false, 3, 1, "WEEKLY_MOVE"), in("B", false, 2, 1, "WEEKLY_MOVE"), in("C", false, 1, 1, "WEEKLY_MOVE")}
	got := Place(cands, testCfg(), testToday)
	if tickers(got.Review) != "A,B,C" {
		t.Fatalf("review=%s", tickers(got.Review))
	}
}

func TestDescribeTrigger(t *testing.T) {
	w := config.Watch{Trigger: config.Trigger{Type: "price_below", Value: n(4000)}}
	f, c, ok := DescribeTrigger(w, 4100, 0)
	if !ok || f != "4100.00 vs your level 4000.00 (2.50% above)" || c != "at or below 4000.00" {
		t.Fatalf("%q %q", f, c)
	}
	r := config.Watch{Trigger: config.Trigger{Type: "rsi_below", Value: n(30)}}
	f, c, _ = DescribeTrigger(r, 0, 27)
	if f != "RSI 27.00 vs your level 30.00 (10.00% below)" || c != "RSI at or below 30.00" {
		t.Fatalf("%q %q", f, c)
	}
}
