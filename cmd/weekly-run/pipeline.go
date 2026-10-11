package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/delivery"
	"github.com/aayushgogia/stock-research-tool/internal/gcp"
	"github.com/aayushgogia/stock-research-tool/internal/history"
	"github.com/aayushgogia/stock-research-tool/internal/judgment"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
	"github.com/aayushgogia/stock-research-tool/internal/news"
	"github.com/aayushgogia/stock-research-tool/internal/report"
	"github.com/aayushgogia/stock-research-tool/internal/watchlist"
)

type tickerFact struct {
	w        config.Watch
	price    float64
	ind      marketdata.Indicators
	events   []watchlist.AttentionEvent
	weeks    map[string]int
	prev     *history.Snapshot
	readErr  error
	rating   llm.Rating
	articles []news.Article
	decision bool
	sizing   watchlist.Sizing
	partial  bool
	status   string
}

func collect(ctx context.Context, md marketdata.Client, store history.Store, w config.Watch, a config.Attention, week string) (tickerFact, error) {
	f := tickerFact{w: w, sizing: watchlist.CheckSizing(w)}
	c, e := md.Daily(ctx, w.Ticker, 260)
	if e != nil {
		return f, fmt.Errorf("%s market data: %w", w.Ticker, e)
	}
	f.price = c[len(c)-1].Close
	f.ind, e = marketdata.Compute(c)
	if e != nil {
		if w.Trigger.Type != "none" {
			return f, fmt.Errorf("%s indicators: %w", w.Ticker, e)
		}
		f.ind, e = marketdata.ComputePulse(c)
		if e != nil {
			return f, e
		}
		f.partial = true
	}
	var skipped []string
	f.events, skipped = watchlist.Attention(w.Ticker, c, a)
	for _, rule := range skipped {
		log.Printf("ticker %s: attention rule %s skipped (insufficient history)", w.Ticker, rule)
	}
	f.prev, e = store.Previous(ctx, w.Ticker, week)
	if e != nil {
		log.Printf("ticker %s Firestore read failed (non-blocking): %v", w.Ticker, e)
		f.prev = nil
		f.readErr = e
	}
	var prior []string
	priorWeeks := map[string]int{}
	if f.prev != nil {
		prior = f.prev.AttentionKinds
		priorWeeks = f.prev.WeeksActive
	}
	f.weeks = watchlist.ActiveWeeks(f.events, prior, priorWeeks)
	return f, nil
}

// executePipeline keeps market/macro data as blocking inputs, while treating
// enrichment and persistence as deferred failures: the weekly email still goes
// out with explicit fallbacks and the process then exits non-zero for retry.
func executePipeline(ctx context.Context, o opts, cfg config.File, twelveKey, fredKey, deepInfraKey, exaKey, smtpCredential string, tokens gcp.TokenSource) error {
	md := marketdata.NewTwelveData(twelveKey)
	log.Printf("macro data collection starting")
	mc, err := macro.Build(ctx, macro.NewFred(fredKey), md)
	if err != nil {
		return fmt.Errorf("macro: %w", err)
	}
	log.Printf("macro data fetched: VIX regime=%s, credit=%s, liquidity=%s", mc.VIXRegime, mc.CreditRegime, mc.LiquidityTrend)
	provider := llm.NewDeepInfra(deepInfraKey, o.model)
	mb, macroOK := macroBrief(ctx, provider, mc)
	if macroOK {
		log.Printf("macro brief ready: %s", mb.RegimeTag)
	}

	week := time.Now().UTC().Format("2006-01-02")
	var store history.Store = history.Firestore{Project: o.project, Tokens: tokens}
	if o.dryRun {
		store = history.DryRun{Store: store}
	}
	facts := make([]tickerFact, 0, len(cfg.Watchlist))
	var deferred []error
	if !macroOK {
		deferred = append(deferred, errors.New("macro LLM fallback used"))
	}
	for _, w := range cfg.Watchlist {
		f, e := collect(ctx, md, store, w, cfg.Attention, week)
		if e != nil {
			return e
		}
		if f.readErr != nil {
			deferred = append(deferred, fmt.Errorf("Firestore previous %s: %w", w.Ticker, f.readErr))
		}
		facts = append(facts, f)
	}

	candidates := make([]watchlist.Candidate, 0)
	for i := range facts {
		if c, ok := candidateFor(facts[i], cfg.Attention.ApproachingBand); ok {
			candidates = append(candidates, c)
			facts[i].status = string(c.Kind)
		} else if facts[i].w.Trigger.Type == "none" {
			facts[i].status = "WATCH ONLY"
		} else {
			facts[i].status = "QUIET"
		}
		log.Printf("ticker %s: %s", facts[i].w.Ticker, facts[i].status)
	}
	selected, radar := watchlist.Candidates(candidates)
	inDecisions := map[string]watchlist.Candidate{}
	for _, c := range selected {
		inDecisions[c.Ticker] = c
	}

	exa := news.NewExa(exaKey)
	articleCache := map[string][]news.Article{}
	newsState := map[string]bool{}
	newsCount := 0
	loadNews := func(f tickerFact) ([]news.Article, bool) {
		key := f.w.NewsQuery
		if a, ok := articleCache[key]; ok {
			return a, newsState[key]
		}
		a, e := exa.Search(ctx, key, cfg.News.WindowDays, cfg.News.NumResults, cfg.News.Domains, time.Now().UTC())
		if e != nil {
			log.Printf("news %s unavailable (non-blocking): %v", f.w.Ticker, e)
			deferred = append(deferred, fmt.Errorf("news %s: %w", f.w.Ticker, e))
			articleCache[key], newsState[key] = nil, false
			return nil, false
		}
		newsCount += len(a)
		articleCache[key], newsState[key] = a, true
		return a, true
	}

	digest := report.Digest{Week: week, MacroDetails: macroDetails(mc), MacroNotable: mb.NotableShifts}
	if macroOK {
		digest.MacroSummary = mb.Summary
	}
	var sheet judgment.Sheets
	if !o.dryRun {
		sheet = judgment.Sheets{ID: o.sheet, Range: o.sheetRange, Tokens: tokens}
		previousWeek := time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02")
		if entries, e := sheet.LastWeek(ctx, previousWeek); e != nil {
			log.Printf("Sheets read failed (non-blocking): %v", e)
			deferred = append(deferred, e)
		} else {
			digest.Judgments = entries
		}
	}
	// Ratings are only generated for decisions and held names. A held name that
	// is selected as a decision is deliberately represented once, in Decisions.
	for i := range facts {
		f := &facts[i]
		candidate, decision := inDecisions[f.w.Ticker]
		if !decision && !f.w.Held {
			continue
		}
		articles, newsOK := loadNews(*f)
		rating, e := rate(ctx, provider, *f, cfg, mc, mb, articles, newsOK)
		if e != nil {
			log.Printf("rating %s unavailable (non-blocking): %v", f.w.Ticker, e)
			deferred = append(deferred, fmt.Errorf("rating %s: %w", f.w.Ticker, e))
			rating = fallbackRating(*f)
		}
		if rating.Rating == "NO RATING" {
			deferred = append(deferred, fmt.Errorf("rating %s: NO RATING", f.w.Ticker))
		}
		storeRating(f, rating, articles, decision)
		if decision {
			digest.Decisions = append(digest.Decisions, decisionView(*f, candidate, rating, articles, newsOK))
		} else {
			digest.Holdings = append(digest.Holdings, holdingView(*f, rating, articles))
		}
	}
	log.Printf("Exa articles accepted: %d", newsCount)

	// Full snapshots are retained for every symbol, including names not rated.
	for i := range facts {
		f := &facts[i]
		if _, ok := inDecisions[f.w.Ticker]; !ok && !f.w.Held {
			storeRating(f, llm.Rating{}, nil, false)
		}
		if e := saveSnapshot(ctx, store, week, *f, mc, mb); e != nil {
			log.Printf("Firestore save %s failed (non-blocking): %v", f.w.Ticker, e)
			deferred = append(deferred, e)
		}
	}

	for _, f := range facts {
		if !f.w.Held {
			if _, isDecision := inDecisions[f.w.Ticker]; !isDecision {
				digest.Pulse = append(digest.Pulse, report.PulseItem{Ticker: f.w.Ticker, Price: f.price, WeekChangePct: f.ind.WeekChangePct, MonthChangePct: f.ind.MonthChangePct, RSI14: f.ind.RSI14})
			}
		}
	}
	sortPulse(digest.Pulse)
	if p, e := pulseBrief(ctx, provider, digest.Pulse); e != nil {
		log.Printf("watchlist pulse unavailable (non-blocking): %v", e)
		deferred = append(deferred, fmt.Errorf("pulse: %w", e))
	} else {
		digest.PulseSummary = p
	}
	for _, x := range radar {
		digest.Radar = append(digest.Radar, x)
	}

	rows := sheetRows(week, digest)
	body := report.Render(digest)
	if o.dryRun {
		fmt.Print(body)
		fmt.Printf("\nProspective Sheets rows (%d):\n", len(rows))
		for _, row := range rows {
			fmt.Printf("%s | %s | %s | %s | %s | %s\n", row.Week, row.Ticker, row.Status, row.SummaryRef, row.ModelRating, row.ModelConfidence)
		}
		log.Printf("dry run: Firestore/email/Sheets writes skipped")
		return errors.Join(deferred...)
	}
	if e := sheet.Sync(ctx, rows); e != nil {
		log.Printf("Sheets sync failed (non-blocking): %v", e)
		deferred = append(deferred, e)
	}
	u, password := smtpCred(smtpCredential)
	subject := fmt.Sprintf("Watchlist - week of %s - %d need a decision", week, len(digest.Decisions))
	if len(digest.Decisions) == 0 {
		subject = fmt.Sprintf("Watchlist - week of %s - nothing needs a decision", week)
	}
	if e := (delivery.SMTP{Host: o.smtpHost, From: o.from, To: o.to, Username: u, Password: password}).Send(subject, body); e != nil {
		return e
	}
	log.Printf("weekly run complete: digest delivered")
	return errors.Join(deferred...)
}

func stateName(f tickerFact) string {
	if f.status != "" {
		return f.status
	}
	if c, ok := candidateFor(f, 5); ok {
		return string(c.Kind)
	}
	if f.w.Trigger.Type == "none" {
		return "WATCH ONLY"
	}
	return "QUIET"
}

func fallbackRating(f tickerFact) llm.Rating {
	return llm.Rating{Rating: "NO RATING", Confidence: "LOW", Summary: "No validated model rating is available this week.", CaseFor: "The deterministic facts are retained below for human review.", CaseAgainst: "No model rating is shown because the generated output was unavailable or failed validation.", WouldChangeIf: decisionQuestion(f), ChangeSinceLastWeek: "No validated rating comparison is available."}
}
func decisionQuestion(f tickerFact) string {
	if !f.w.Held {
		return "Start a position, or keep watching?"
	}
	if f.sizing.AtCeiling {
		return "Trim or hold?"
	}
	return "Add, trim, or hold?"
}
func driversFor(r llm.Rating, articles []news.Article) []report.Driver {
	byID := map[string]news.Article{}
	for _, a := range articles {
		byID[a.ID] = a
	}
	out := make([]report.Driver, 0, len(r.Drivers))
	for _, d := range r.Drivers {
		a, ok := byID[d.SourceID]
		if ok {
			out = append(out, report.Driver{Claim: d.Claim, Source: report.Source{ID: a.ID, Label: a.Source, URL: a.URL, Date: a.Date}})
		} else {
			out = append(out, report.Driver{Claim: d.Claim, Source: report.Source{ID: d.SourceID}})
		}
	}
	return out
}
func decisionView(f tickerFact, c watchlist.Candidate, r llm.Rating, articles []news.Article, newsOK bool) report.Decision {
	note := ""
	if !newsOK {
		note = "News unavailable this week; confidence is capped at Medium."
	}
	return report.Decision{Ticker: f.w.Ticker, Kind: string(c.Kind), Rating: r.Rating, Confidence: r.Confidence, PreviousRating: previousRating(f.prev), Summary: r.Summary, CaseFor: r.CaseFor, CaseAgainst: r.CaseAgainst, WouldChangeIf: r.WouldChangeIf, Question: decisionQuestion(f), Facts: factsText(c), Drivers: driversFor(r, articles), NewsNote: note}
}
func holdingView(f tickerFact, r llm.Rating, articles []news.Article) report.Holding {
	return report.Holding{Ticker: f.w.Ticker, Rating: r.Rating, Confidence: r.Confidence, Summary: r.Summary, Price: f.price, WeekChangePct: f.ind.WeekChangePct, Drivers: driversFor(r, articles)}
}
func previousRating(s *history.Snapshot) string {
	if s == nil {
		return ""
	}
	return s.Rating
}
func eventKinds(events []watchlist.AttentionEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}
func storeRating(f *tickerFact, r llm.Rating, articles []news.Article, decision bool) {
	f.rating, f.articles, f.decision = r, articles, decision
}
func saveSnapshot(ctx context.Context, store history.Store, week string, f tickerFact, mc macro.Context, mb llm.MacroBrief) error {
	facts, _ := json.Marshal(struct {
		Price      float64                    `json:"price"`
		Indicators marketdata.Indicators      `json:"indicators"`
		Attention  []watchlist.AttentionEvent `json:"attention"`
		Sizing     string                     `json:"sizing"`
	}{f.price, f.ind, f.events, f.sizing.Description})
	macroJSON, _ := json.Marshal(struct {
		Context macro.Context  `json:"context"`
		Brief   llm.MacroBrief `json:"brief"`
	}{mc, mb})
	drivers, _ := json.Marshal(f.rating.Drivers)
	articles, _ := json.Marshal(f.articles)
	llmJSON, _ := json.Marshal(f.rating)
	return store.Save(ctx, history.Snapshot{Week: week, Ticker: f.w.Ticker, Price: f.price, Status: stateName(f), AttentionKinds: eventKinds(f.events), WeeksActive: f.weeks, InDecisions: f.decision, Rating: f.rating.Rating, Confidence: f.rating.Confidence, Drivers: drivers, Articles: articles, Facts: facts, Macro: macroJSON, LLM: llmJSON, SPYPrice: mc.SPYPrice})
}
func sheetRows(week string, d report.Digest) []judgment.Row {
	rows := []judgment.Row{}
	for _, x := range d.Decisions {
		rows = append(rows, judgment.Row{Week: week, Ticker: x.Ticker, Status: x.Kind, SummaryRef: "firestore:" + x.Ticker + "_" + week, ModelRating: x.Rating, ModelConfidence: x.Confidence})
	}
	for _, x := range d.Holdings {
		if x.Rating == "BUY" || x.Rating == "TRIM" || x.Rating == "SELL" {
			rows = append(rows, judgment.Row{Week: week, Ticker: x.Ticker, Status: "RATING", SummaryRef: "firestore:" + x.Ticker + "_" + week, ModelRating: x.Rating, ModelConfidence: x.Confidence})
		}
	}
	return rows
}

func candidateFor(f tickerFact, band float64) (watchlist.Candidate, bool) {
	k, ok := watchlist.NumericCandidate(f.w, f.price, f.ind.RSI14, band)
	if ok {
		events := append([]watchlist.AttentionEvent{{Ticker: f.w.Ticker, Kind: string(k), Fact: triggerFact(f, k)}}, f.events...)
		return watchlist.Candidate{Ticker: f.w.Ticker, Kind: k, Held: f.w.Held, WeekChangePct: f.ind.WeekChangePct, Events: events, WeeksActive: maxWeeks(f.weeks)}, true
	}
	if len(f.events) > 0 {
		return watchlist.Candidate{Ticker: f.w.Ticker, Kind: watchlist.DecisionAttention, Held: f.w.Held, WeekChangePct: f.ind.WeekChangePct, Events: f.events, WeeksActive: maxWeeks(f.weeks)}, true
	}
	return watchlist.Candidate{}, false
}
func triggerFact(f tickerFact, kind ...watchlist.DecisionKind) string {
	t := f.w.Trigger
	near := len(kind) > 0 && kind[0] == watchlist.DecisionNear
	switch t.Type {
	case "price_below":
		if near {
			return fmt.Sprintf("price %.2f is within the configured band above stated level %.2f", f.price, *t.Value)
		}
		return fmt.Sprintf("price %.2f is at or below stated level %.2f", f.price, *t.Value)
	case "price_above":
		if near {
			return fmt.Sprintf("price %.2f is within the configured band below stated level %.2f", f.price, *t.Value)
		}
		return fmt.Sprintf("price %.2f is at or above stated level %.2f", f.price, *t.Value)
	case "rsi_below":
		if near {
			return fmt.Sprintf("RSI(14) %.2f is within the configured band above stated level %.2f", f.ind.RSI14, *t.Value)
		}
		return fmt.Sprintf("RSI(14) %.2f is at or below stated level %.2f", f.ind.RSI14, *t.Value)
	case "price_above_pct_from_entry":
		level := *t.Entry * (1 + *t.Pct/100)
		if near {
			return fmt.Sprintf("price %.2f is within the configured band below entry-derived level %.2f", f.price, level)
		}
		return fmt.Sprintf("price %.2f is at or above entry-derived level %.2f", f.price, level)
	}
	return "deterministic trigger condition met"
}
func maxWeeks(m map[string]int) int {
	x := 1
	for _, v := range m {
		if v > x {
			x = v
		}
	}
	return x
}

func evidenceFor(f tickerFact, mc macro.Context, mb llm.MacroBrief) []llm.Evidence {
	e := []llm.Evidence{}
	if f.w.Trigger.Type != "none" {
		e = append(e, llm.Evidence{ID: "F0", Text: triggerFact(f, watchlist.DecisionKind(f.status))})
	}
	for i, v := range f.events {
		e = append(e, llm.Evidence{ID: fmt.Sprintf("F%d", i+1), Text: v.Fact})
	}
	n := len(e) + 1
	if f.ind.FullHistory {
		e = append(e, llm.Evidence{ID: fmt.Sprintf("F%d", n), Text: fmt.Sprintf("price %.2f; 52-week low %.2f high %.2f; RSI %.2f; SMA50 %.2f; SMA200 %.2f; week %.2f%%; month %.2f%%", f.price, f.ind.Low52, f.ind.High52, f.ind.RSI14, f.ind.SMA50, f.ind.SMA200, f.ind.WeekChangePct, f.ind.MonthChangePct)})
	} else {
		e = append(e, llm.Evidence{ID: fmt.Sprintf("F%d", n), Text: fmt.Sprintf("price %.2f; RSI %.2f; week %.2f%%; month %.2f%%; %d history sessions; 52-week range and moving averages unavailable", f.price, f.ind.RSI14, f.ind.WeekChangePct, f.ind.MonthChangePct, f.ind.HistorySessions)})
	}
	e = append(e, llm.Evidence{ID: fmt.Sprintf("F%d", n+1), Text: fmt.Sprintf("macro %s: %s", mb.RegimeTag, mb.Summary)})
	return e
}

func positionText(f tickerFact) string { return f.sizing.Description }

func rate(ctx context.Context, p llm.Provider, f tickerFact, cfg config.File, mc macro.Context, mb llm.MacroBrief, articles []news.Article, newsOK bool) (llm.Rating, error) {
	e := evidenceFor(f, mc, mb)
	for _, a := range articles {
		e = append(e, llm.Evidence{ID: a.ID, Text: strings.Join(a.Highlights, " ")})
	}
	prev := ""
	if f.prev != nil {
		prev = f.prev.Rating
	}
	allowed := llm.AllowedRatings(f.w.Held, f.sizing.AtCeiling)
	sys, user := llm.RatingPrompt(f.w.Ticker, f.w.NewsQuery, cfg.Rating.Horizon, f.w.Rationale, positionText(f), prev, allowed, e)
	var last error
	for i := 0; i < 2; i++ {
		attemptUser := user
		if last != nil {
			attemptUser += "\nYour prior JSON was rejected because: " + last.Error() + ". Repair that issue. Return no unsupported numerals; use words when possible."
		}
		raw, err := p.Complete(ctx, sys, attemptUser)
		if err != nil {
			last = err
			continue
		}
		r, err := llm.ParseRating(raw, allowed, e, prev)
		if err == nil {
			if !newsOK && r.Confidence == "HIGH" {
				r.Confidence = "MEDIUM"
			}
			return r, nil
		}
		last = err
	}
	return llm.Rating{}, last
}

func factsText(c watchlist.Candidate) []string {
	out := []string{}
	for _, e := range c.Events {
		s := e.Fact
		if c.WeeksActive > 1 {
			s += fmt.Sprintf(" (week %d running)", c.WeeksActive)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		out = append(out, string(c.Kind))
	}
	return out
}

func sortPulse(p []report.PulseItem) {
	sort.Slice(p, func(i, j int) bool {
		a, b := p[i].WeekChangePct, p[j].WeekChangePct
		if a < 0 {
			a = -a
		}
		if b < 0 {
			b = -b
		}
		return a > b
	})
}
