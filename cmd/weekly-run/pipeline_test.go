package main

import (
	"context"
	"strings"
	"testing"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/history"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
	"github.com/aayushgogia/stock-research-tool/internal/news"
	"github.com/aayushgogia/stock-research-tool/internal/watchlist"
)

type captureStore struct{ saved history.Snapshot }

func (s *captureStore) Previous(context.Context, string, string) (*history.Snapshot, error) {
	return nil, nil
}
func (s *captureStore) Save(_ context.Context, got history.Snapshot) error { s.saved = got; return nil }

type staticProvider struct{ response string }

func (p staticProvider) Complete(context.Context, string, string) (string, error) {
	return p.response, nil
}

type retryProvider struct{ users []string }

func (p *retryProvider) Complete(_ context.Context, _ string, user string) (string, error) {
	p.users = append(p.users, user)
	if len(p.users) == 1 {
		return `{"rating":"WAIT","confidence":"LOW","summary":"price 11","case_for":"price 10","case_against":"price 10","drivers":[],"would_change_if":"facts","change_since_last_week":"changed"}`, nil
	}
	return `{"rating":"WAIT","confidence":"LOW","summary":"facts","case_for":"facts","case_against":"facts","drivers":[],"would_change_if":"facts","change_since_last_week":"changed"}`, nil
}

func TestSaveSnapshotCarriesDecisionRatingAndEvidence(t *testing.T) {
	store := &captureStore{}
	f := tickerFact{
		w: config.Watch{Ticker: "AMD"}, price: 12.5, status: "ATTENTION",
		events: []watchlist.AttentionEvent{{Kind: "RSI_LOW", Fact: "RSI(14) 25 below 30"}},
		weeks:  map[string]int{"RSI_LOW": 2}, decision: true,
		rating:   llm.Rating{Rating: "BUY", Confidence: "MEDIUM", Drivers: []llm.Driver{{Claim: "RSI 25", SourceID: "F1"}}},
		articles: []news.Article{{ID: "A1", URL: "https://example.test/story"}},
	}
	if err := saveSnapshot(context.Background(), store, "2026-10-12", f, macro.Context{SPYPrice: 500}, llm.MacroBrief{RegimeTag: "mixed"}); err != nil {
		t.Fatal(err)
	}
	got := store.saved
	if got.Week != "2026-10-12" || got.Ticker != "AMD" || !got.InDecisions || got.Rating != "BUY" || got.Confidence != "MEDIUM" || got.SPYPrice != 500 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if len(got.Drivers) == 0 || len(got.Articles) == 0 || len(got.Facts) == 0 || len(got.Macro) == 0 || len(got.LLM) == 0 {
		t.Fatalf("missing persisted provenance: %+v", got)
	}
}

func TestTriggerFactDescribesNearWithoutClaimingItFired(t *testing.T) {
	level := 100.0
	f := tickerFact{w: config.Watch{Trigger: config.Trigger{Type: "price_below", Value: &level}}, price: 104}
	got := triggerFact(f, watchlist.DecisionNear)
	if got == "" || got == "price 104.00 is at or below stated level 100.00" {
		t.Fatalf("near trigger fact is misleading: %q", got)
	}
}

func TestPartialHistoryEvidenceDoesNotFabricateUnavailableIndicators(t *testing.T) {
	f := tickerFact{w: config.Watch{Ticker: "NEW", Trigger: config.Trigger{Type: "none"}}, price: 10, ind: marketdata.Indicators{RSI14: 40, WeekChangePct: 2, MonthChangePct: 3, HistorySessions: 74, FullHistory: false}}
	evidence := evidenceFor(f, macro.Context{}, llm.MacroBrief{})
	if len(evidence) == 0 || strings.Contains(evidence[len(evidence)-2].Text, "SMA200 0") || strings.Contains(evidence[len(evidence)-2].Text, "52-week low 0") {
		t.Fatalf("partial evidence invents unavailable measures: %#v", evidence)
	}
}

func TestRateCapsHighConfidenceWhenNewsIsUnavailable(t *testing.T) {
	f := tickerFact{w: config.Watch{Ticker: "AMD", Rationale: "watch", NewsQuery: "AMD", Trigger: config.Trigger{Type: "none"}}, price: 10, ind: marketdata.Indicators{RSI14: 40, WeekChangePct: 1, MonthChangePct: 2, HistorySessions: 74}}
	provider := staticProvider{response: `{"rating":"WAIT","confidence":"HIGH","summary":"facts are mixed","case_for":"the supplied facts are available","case_against":"the supplied facts are limited","drivers":[],"would_change_if":"new supplied evidence","change_since_last_week":"no material change"}`}
	rating, err := rate(context.Background(), provider, f, config.File{Rating: config.Rating{Horizon: "6-12 months"}}, macro.Context{}, llm.MacroBrief{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if rating.Confidence != "MEDIUM" {
		t.Fatalf("confidence=%s, want MEDIUM", rating.Confidence)
	}
}

func TestRateGivesRetryValidatorFeedback(t *testing.T) {
	f := tickerFact{w: config.Watch{Ticker: "AMD", Rationale: "watch", NewsQuery: "AMD", Trigger: config.Trigger{Type: "none"}}, price: 10, ind: marketdata.Indicators{RSI14: 40, WeekChangePct: 1, MonthChangePct: 2, HistorySessions: 74}}
	p := &retryProvider{}
	if _, err := rate(context.Background(), p, f, config.File{Rating: config.Rating{Horizon: "6-12 months"}}, macro.Context{}, llm.MacroBrief{}, nil, true); err != nil {
		t.Fatal(err)
	}
	if len(p.users) != 2 || !strings.Contains(p.users[1], "prior JSON was rejected") {
		t.Fatalf("retry feedback not supplied: %#v", p.users)
	}
}
