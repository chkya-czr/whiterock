package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/history"
	"github.com/aayushgogia/stock-research-tool/internal/judgment"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/news"
	"github.com/aayushgogia/stock-research-tool/internal/report"
	"github.com/aayushgogia/stock-research-tool/internal/watchlist"
)

var today = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

func fcfg() config.File {
	return config.File{Memory: config.Memory{WaitedSuppressWeeks: 4, DecisionContextWeeks: 8}}
}
func fp(v float64) *float64 { return &v }

func TestFailedRatingStillRendersFactsHeadlinesAndQuestion(t *testing.T) {
	f := tickerFact{
		w: config.Watch{Ticker: "FICO", Held: true, PositionPct: fp(4)}, price: 1500,
		ratingOK: false, ratingWhy: shortReason(errors.New("untraceable model number \"7\"")),
		articles: []news.Article{{ID: "A1", Title: "FICO falls", URL: "https://example.test/a", Source: "reuters.com", Date: "2026-10-10"}},
	}
	p := watchlist.Placement{Ticker: "FICO", Bucket: watchlist.BucketReview, NewEvents: []watchlist.AttentionEvent{{Kind: "MONTHLY_MOVE", Fact: "-30.50% move over 21 sessions", Negative: true}}}
	got := report.Render(report.Digest{Review: []report.Item{itemView(f, p, fcfg(), today, true)}})
	if n := strings.Count(got, "Model rating: unavailable"); n != 1 {
		t.Fatalf("unavailable lines = %d:\n%s", n, got)
	}
	for _, want := range []string{"FICO - REVIEW", "-30.50% move over 21 sessions (held, 4%)", "[FICO falls]", "What changed? Thesis break, or re-rating?"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Case for") || strings.Contains(got, "NO RATING") {
		t.Fatalf("placeholder text rendered:\n%s", got)
	}
}

func TestReviewQuestionTemplates(t *testing.T) {
	up := []watchlist.AttentionEvent{{Kind: "WEEKLY_MOVE", Fact: "9.00% move over 5 sessions"}}
	cases := []struct {
		name string
		f    tickerFact
		p    watchlist.Placement
		want string
	}{
		{"not held, upward", tickerFact{w: config.Watch{Ticker: "A"}}, watchlist.Placement{NewEvents: up}, "Start a position, or keep watching?"},
		{"held, upward", tickerFact{w: config.Watch{Ticker: "A", Held: true}}, watchlist.Placement{NewEvents: up}, "Add, trim, or hold?"},
		{"held at ceiling", tickerFact{w: config.Watch{Ticker: "A", Held: true}, sizing: watchlist.Sizing{AtCeiling: true}}, watchlist.Placement{NewEvents: up}, "Trim or hold?"},
		{"negative event wins", tickerFact{w: config.Watch{Ticker: "A"}}, watchlist.Placement{NewEvents: []watchlist.AttentionEvent{{Kind: "NEW_52W_LOW", Fact: "x", Negative: true}}}, "What changed? Thesis break, or re-rating?"},
		{"waited resurfaced overrides negative", tickerFact{w: config.Watch{Ticker: "A"}}, watchlist.Placement{Resurfaced: true, Logged: &watchlist.LoggedDecision{Week: "2026-09-07", Decision: "Waited"}, NewEvents: []watchlist.AttentionEvent{{Kind: "NEW_52W_LOW", Fact: "x", Negative: true}}}, "Still waiting? What has changed since 2026-09-07?"},
	}
	for _, c := range cases {
		c.p.Bucket = watchlist.BucketReview
		if got := itemView(c.f, c.p, fcfg(), today, true).Question; got != c.want {
			t.Fatalf("%s: %q", c.name, got)
		}
	}
}

func TestActItemTriggerAndNearTrigger(t *testing.T) {
	w := config.Watch{Ticker: "XAU", Trigger: config.Trigger{Type: "price_below", Value: fp(4000)}}
	trig := itemView(tickerFact{w: w, price: 3990}, watchlist.Placement{Ticker: "XAU", Bucket: watchlist.BucketACT, Numeric: watchlist.DecisionTrigger}, fcfg(), today, true)
	if trig.Header != "XAU - ACT - TRIGGER" || trig.Lines[0] != "3990.00 vs your level 4000.00 (0.25% below)" || trig.Lines[1] != "Your condition is met." || trig.Question != "Start a position, or keep watching?" {
		t.Fatalf("%+v", trig)
	}
	near := itemView(tickerFact{w: w, price: 4100}, watchlist.Placement{Ticker: "XAU", Bucket: watchlist.BucketACT, Numeric: watchlist.DecisionNear}, fcfg(), today, true)
	if near.Header != "XAU - ACT - NEAR TRIGGER" || near.Lines[1] != "Not triggered yet. Triggers at: at or below 4000.00" || near.Question != "" {
		t.Fatalf("%+v", near)
	}
}

func TestContextLineAppearsOnItem(t *testing.T) {
	f := tickerFact{w: config.Watch{Ticker: "FICO"}, price: 90}
	p := watchlist.Placement{Bucket: watchlist.BucketReview, Logged: &watchlist.LoggedDecision{Week: "2026-10-05", Decision: "Waited", Reason: "earnings", SnapshotFound: true, PriceThen: 100}}
	got := itemView(f, p, fcfg(), today, true).Context
	if got != "You logged Waited on 2026-10-05: earnings. Price then 100.00, now 90.00 (-10.00%)." {
		t.Fatal(got)
	}
}

func TestHeadlinesCapAtThreeMostRecent(t *testing.T) {
	var a []news.Article
	for i, d := range []string{"2026-10-01", "2026-10-09", "2026-10-03", "2026-10-08", "2026-10-07"} {
		a = append(a, news.Article{ID: string(rune('A' + i)), Title: d, Date: d})
	}
	got := headlinesFor(a)
	if len(got) != 3 || got[0].Date != "2026-10-09" || got[1].Date != "2026-10-08" || got[2].Date != "2026-10-07" {
		t.Fatalf("%+v", got)
	}
}

func lastWeekFact(ticker string, bucket string, suppressed bool) tickerFact {
	return tickerFact{w: config.Watch{Ticker: ticker}, price: 12, prev: &history.Snapshot{Week: "2026-10-05", Ticker: ticker, Price: 10, Status: "ATTENTION", Bucket: bucket, Suppressed: suppressed}}
}

func TestLastWeekView(t *testing.T) {
	facts := []tickerFact{lastWeekFact("OPEN", "REVIEW", false), lastWeekFact("DONE", "ACT", false), lastWeekFact("FYI", "FYI", false), lastWeekFact("SUP", "REVIEW", true)}
	entries := []judgment.Entry{{Week: "2026-10-05", Ticker: "DONE", Status: "TRIGGER", Decision: "Bought", Reason: "level hit"}}
	lw := lastWeekView(facts, entries, nil, today)
	if !lw.Show || lw.AllResolved || len(lw.Lines) != 2 || !strings.HasPrefix(lw.Lines[0], "OPEN: still open (ATTENTION) - price then 10.00, now 12.00.") || lw.Lines[1] != "DONE: logged Bought - level hit" {
		t.Fatalf("%+v", lw)
	}
	resolved := lastWeekView([]tickerFact{facts[1]}, entries, nil, today)
	if !resolved.AllResolved {
		t.Fatalf("%+v", resolved)
	}
	if got := lastWeekView([]tickerFact{facts[2], facts[3]}, nil, nil, today); got.Show {
		t.Fatalf("no ACT/REVIEW last week must omit the section: %+v", got)
	}
	if got := lastWeekView(facts, nil, errors.New("boom"), today); !got.Show || !got.Unavailable {
		t.Fatalf("%+v", got)
	}
	stale := lastWeekFact("OLD", "ACT", false)
	stale.prev.Week = "2026-08-01"
	if got := lastWeekView([]tickerFact{stale}, nil, nil, today); got.Show {
		t.Fatalf("a stale snapshot is not last week: %+v", got)
	}
}

func TestSheetRowsUseExistingStatusVocabulary(t *testing.T) {
	rev := tickerFact{w: config.Watch{Ticker: "R"}, decision: true, status: "ATTENTION", rating: llm.Rating{Rating: "NO RATING", Confidence: "LOW"}}
	act := tickerFact{w: config.Watch{Ticker: "A"}, decision: true, status: "NEAR TRIGGER", ratingOK: true, rating: llm.Rating{Rating: "WAIT", Confidence: "LOW"}}
	trim := tickerFact{w: config.Watch{Ticker: "T", Held: true}, ratingOK: true, rating: llm.Rating{Rating: "TRIM", Confidence: "HIGH"}}
	hold := tickerFact{w: config.Watch{Ticker: "H", Held: true}, ratingOK: true, rating: llm.Rating{Rating: "HOLD", Confidence: "HIGH"}}
	fyi := tickerFact{w: config.Watch{Ticker: "F"}}
	rows := sheetRows("2026-10-12", []tickerFact{rev, act, trim, hold, fyi})
	got := []string{}
	for _, r := range rows {
		got = append(got, r.Ticker+":"+r.Status)
	}
	if strings.Join(got, ",") != "R:ATTENTION,A:NEAR TRIGGER,T:RATING" {
		t.Fatal(got)
	}
}

func TestSnapshotCarriesBucketAndSuppression(t *testing.T) {
	store := &captureStore{}
	f := tickerFact{w: config.Watch{Ticker: "FICO"}, status: "FYI", place: watchlist.Placement{Bucket: watchlist.BucketFYI, Suppressed: true, SuppressedReason: "Waited on 2026-10-05"}}
	if err := saveSnapshot(context.Background(), store, "2026-10-12", f, macro.Context{}, llm.MacroBrief{}); err != nil {
		t.Fatal(err)
	}
	if s := store.saved; s.Bucket != "FYI" || !s.Suppressed || s.SuppressedReason != "Waited on 2026-10-05" || s.Status != "FYI" {
		t.Fatalf("%+v", s)
	}
}

func TestPlacementInputJoinsDecisionWeekSnapshot(t *testing.T) {
	f := tickerFact{w: config.Watch{Ticker: "FICO"}, price: 90, prev: &history.Snapshot{}, hist: []history.Snapshot{{Week: "2026-10-05", Price: 100, AttentionKinds: []string{"RSI_LOW"}}}}
	in := placementInput(f, 5, map[string]judgment.Entry{"FICO": {Week: "2026-10-05", Ticker: "FICO", Decision: "Waited", Reason: "r"}})
	if in.Logged == nil || !in.Logged.SnapshotFound || in.Logged.PriceThen != 100 || len(in.Logged.KindsThen) != 1 {
		t.Fatalf("%+v", in.Logged)
	}
	in = placementInput(tickerFact{w: config.Watch{Ticker: "FICO"}}, 5, map[string]judgment.Entry{"FICO": {Week: "2026-09-01", Decision: "Bought"}})
	if in.Logged == nil || in.Logged.SnapshotFound {
		t.Fatalf("missing snapshot must be reported: %+v", in.Logged)
	}
}
