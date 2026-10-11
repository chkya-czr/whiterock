package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
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
	w         config.Watch
	price     float64
	ind       marketdata.Indicators
	events    []watchlist.AttentionEvent
	weeks     map[string]int
	prev      *history.Snapshot
	hist      []history.Snapshot // earlier snapshots, newest first
	readErr   error
	rating    llm.Rating
	ratingOK  bool   // rating passed validation
	ratingWhy string // short reason when it did not
	articles  []news.Article
	decision  bool // ACT or REVIEW this week
	sizing    watchlist.Sizing
	partial   bool
	status    string // flagged_status vocabulary: TRIGGER, NEAR TRIGGER, ATTENTION, FYI, QUIET, WATCH ONLY
	place     watchlist.Placement
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
	f.hist, e = history.Load(ctx, store, w.Ticker, week)
	if e != nil {
		log.Printf("ticker %s Firestore read failed (non-blocking): %v", w.Ticker, e)
		f.hist = nil
		f.readErr = e
	}
	if len(f.hist) > 0 {
		f.prev = &f.hist[0]
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

	now := time.Now().UTC()
	week := now.Format("2006-01-02")
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
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

	// Decision memory is read-only here (also in dry-run) and never blocks.
	sheet := judgment.Sheets{ID: o.sheet, Range: o.sheetRange, Tokens: tokens}
	var entries []judgment.Entry
	var memoryErr error
	if o.sheet == "" {
		memoryErr = errors.New("no --sheet-id configured")
	} else if entries, memoryErr = sheet.Entries(ctx); memoryErr != nil {
		log.Printf("Sheets read failed (non-blocking): %v", memoryErr)
		deferred = append(deferred, memoryErr)
	}
	latest := map[string]judgment.Entry{}
	if memoryErr == nil {
		latest = judgment.LatestDecisions(entries)
	}

	inputs := make([]watchlist.Input, len(facts))
	for i := range facts {
		inputs[i] = placementInput(facts[i], cfg.Attention.ApproachingBand, latest)
	}
	placed := watchlist.Place(inputs, cfg, today)
	byTicker := map[string]watchlist.Placement{}
	for _, p := range placed.All() {
		byTicker[p.Ticker] = p
	}
	for i := range facts {
		f := &facts[i]
		f.place = byTicker[f.w.Ticker]
		f.decision = f.place.Bucket == watchlist.BucketACT || f.place.Bucket == watchlist.BucketReview
		f.status = flaggedStatus(*f)
		log.Printf("ticker %s: %s - %s", f.w.Ticker, f.place.Bucket, f.place.Reason)
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

	// Ratings and news run for ACT, REVIEW and held names only; FYI-only,
	// non-held names make no model call and no news fetch.
	newsOKByTicker := map[string]bool{}
	for i := range facts {
		f := &facts[i]
		if !f.decision && !f.w.Held {
			continue
		}
		articles, newsOK := loadNews(*f)
		newsOKByTicker[f.w.Ticker] = newsOK
		rating, e := rate(ctx, provider, *f, cfg, mc, mb, articles, newsOK)
		f.articles = articles
		if e != nil {
			log.Printf("rating %s unavailable (non-blocking): %v", f.w.Ticker, e)
			deferred = append(deferred, fmt.Errorf("rating %s: %w", f.w.Ticker, e))
			f.rating, f.ratingOK, f.ratingWhy = fallbackRating(*f), false, shortReason(e)
		} else {
			f.rating, f.ratingOK = rating, true
		}
	}
	log.Printf("Exa articles accepted: %d", newsCount)

	factsByTicker := map[string]*tickerFact{}
	for i := range facts {
		factsByTicker[facts[i].w.Ticker] = &facts[i]
	}
	for _, p := range placed.Act {
		digest.Act = append(digest.Act, itemView(*factsByTicker[p.Ticker], p, cfg, today, newsOKByTicker[p.Ticker]))
	}
	for _, p := range placed.Review {
		digest.Review = append(digest.Review, itemView(*factsByTicker[p.Ticker], p, cfg, today, newsOKByTicker[p.Ticker]))
	}
	for _, f := range facts {
		if f.w.Held && !f.decision {
			digest.Holdings = append(digest.Holdings, report.Holding{Ticker: f.w.Ticker, Price: f.price, WeekChangePct: f.ind.WeekChangePct, Model: modelView(f, f.articles)})
		}
	}
	for i, p := range placed.FYI {
		if i >= cfg.Review.FYIMaxListed {
			digest.FYIMore = len(placed.FYI) - cfg.Review.FYIMaxListed
			break
		}
		digest.FYI = append(digest.FYI, fmt.Sprintf("%s (%s)", p.Ticker, p.FYILabel))
	}
	digest.LastWeek = lastWeekView(facts, entries, memoryErr, today)

	// Full snapshots are retained for every symbol, including names not rated.
	for i := range facts {
		f := &facts[i]
		if e := saveSnapshot(ctx, store, week, *f, mc, mb); e != nil {
			log.Printf("Firestore save %s failed (non-blocking): %v", f.w.Ticker, e)
			deferred = append(deferred, e)
		}
	}

	for _, f := range facts {
		if !f.w.Held && !f.decision {
			digest.Pulse = append(digest.Pulse, report.PulseItem{Ticker: f.w.Ticker, Price: f.price, WeekChangePct: f.ind.WeekChangePct, MonthChangePct: f.ind.MonthChangePct, RSI14: f.ind.RSI14})
		}
	}
	sortPulse(digest.Pulse)
	if p, e := pulseBrief(ctx, provider, digest.Pulse); e != nil {
		log.Printf("watchlist pulse unavailable (non-blocking): %v", e)
		deferred = append(deferred, fmt.Errorf("pulse: %w", e))
	} else {
		digest.PulseSummary = p
	}

	rows := sheetRows(week, facts)
	body := report.Render(digest)
	if o.dryRun {
		fmt.Println("Placement (ticker | bucket | reason):")
		for _, p := range placed.All() {
			fmt.Printf("  %-8s | %-6s | %s\n", p.Ticker, p.Bucket, p.Reason)
		}
		fmt.Println()
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
	if e := (delivery.SMTP{Host: o.smtpHost, From: o.from, To: o.to, Username: u, Password: password}).Send(report.Subject(digest), body); e != nil {
		return e
	}
	log.Printf("weekly run complete: digest delivered")
	return errors.Join(deferred...)
}

// placementInput gathers the facts the bucket classifier needs, joining the
// investor's latest logged decision with that week's Firestore snapshot.
func placementInput(f tickerFact, band float64, latest map[string]judgment.Entry) watchlist.Input {
	in := watchlist.Input{Ticker: f.w.Ticker, Held: f.w.Held, Events: f.events, WeeksActive: f.weeks, HasPrior: f.prev != nil, WeekPct: f.ind.WeekChangePct, MonthPct: f.ind.MonthChangePct}
	if k, ok := watchlist.NumericCandidate(f.w, f.price, f.ind.RSI14, band); ok {
		in.Numeric = k
	}
	if d, ok := latest[f.w.Ticker]; ok {
		l := &watchlist.LoggedDecision{Week: d.Week, Decision: d.Decision, Reason: d.Reason}
		for _, s := range f.hist {
			if s.Week == d.Week {
				l.SnapshotFound, l.KindsThen, l.PriceThen = true, s.AttentionKinds, s.Price
				break
			}
		}
		in.Logged = l
	}
	return in
}

// flaggedStatus uses the Sheet's existing flagged_status vocabulary.
func flaggedStatus(f tickerFact) string {
	switch f.place.Bucket {
	case watchlist.BucketACT:
		return string(f.place.Numeric)
	case watchlist.BucketReview:
		return string(watchlist.DecisionAttention)
	}
	switch {
	case f.place.Listed:
		return "FYI"
	case f.w.Trigger.Type == "none":
		return "WATCH ONLY"
	}
	return "QUIET"
}

// snapshotBucket reads a prior snapshot's bucket, falling back to the older
// status/in-decisions fields written before buckets existed.
func snapshotBucket(s history.Snapshot) string {
	if s.Bucket != "" {
		return s.Bucket
	}
	if s.InDecisions {
		if s.Status == "TRIGGER" || s.Status == "NEAR TRIGGER" {
			return string(watchlist.BucketACT)
		}
		return string(watchlist.BucketReview)
	}
	return string(watchlist.BucketFYI)
}

// lastWeekView lists last week's ACT and REVIEW items (not suppressed) and
// whether the investor has logged a decision for each.
func lastWeekView(facts []tickerFact, entries []judgment.Entry, memoryErr error, today time.Time) report.LastWeek {
	var inputs []watchlist.LastWeekInput
	for _, f := range facts {
		if f.prev == nil {
			continue
		}
		s := *f.prev
		if t, err := time.Parse("2006-01-02", s.Week); err != nil || today.Sub(t) > 10*24*time.Hour {
			continue
		}
		if b := snapshotBucket(s); s.Suppressed || (b != "ACT" && b != "REVIEW") {
			continue
		}
		x := watchlist.LastWeekInput{Ticker: f.w.Ticker, Tag: s.Status, PriceThen: s.Price, HavePriceThen: s.Price > 0, PriceNow: f.price}
		if x.Tag == "" || x.Tag == "FYI" {
			x.Tag = "ATTENTION"
		}
		if e, ok := judgment.Find(entries, s.Week, f.w.Ticker); ok {
			x.Decision, x.Reason = strings.TrimSpace(e.Decision), strings.TrimSpace(e.Reason)
			if e.Status != "" {
				x.Tag = e.Status
			}
		}
		inputs = append(inputs, x)
	}
	if len(inputs) == 0 {
		return report.LastWeek{}
	}
	if memoryErr != nil {
		return report.LastWeek{Show: true, Unavailable: true}
	}
	lines, more, all := watchlist.LastWeekLines(inputs, 10)
	return report.LastWeek{Show: true, Lines: lines, More: more, AllResolved: all}
}

// shortReason compresses a rating failure into a few words for the email.
func shortReason(err error) string {
	m := err.Error()
	switch {
	case strings.Contains(m, "untraceable"):
		return "output cited an unsupported number"
	case strings.Contains(m, "distinct evidence"), strings.Contains(m, "unknown source_id"):
		return "output cited insufficient or unknown sources"
	case strings.Contains(m, "not allowed"), strings.Contains(m, "invalid confidence"):
		return "output used a disallowed value"
	case strings.Contains(m, "missing required"), strings.Contains(m, "invalid rating JSON"), strings.Contains(m, "empty driver"):
		return "output was incomplete or malformed"
	case strings.Contains(m, "no material change"):
		return "output contradicted last week's rating"
	}
	return "model call failed"
}

// fallbackRating carries no prose: an unvalidated rating is rendered only as
// "Model rating: unavailable (reason)".
func fallbackRating(f tickerFact) llm.Rating {
	return llm.Rating{Rating: "NO RATING", Confidence: "LOW"}
}

// decisionQuestion is the fixed question for a triggered or held name.
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

func modelView(f tickerFact, articles []news.Article) report.Model {
	if !f.ratingOK {
		why := f.ratingWhy
		if why == "" {
			why = "not generated"
		}
		return report.Model{Unavailable: why}
	}
	r := f.rating
	return report.Model{OK: true, Rating: r.Rating, Confidence: r.Confidence, Summary: r.Summary, CaseFor: r.CaseFor, CaseAgainst: r.CaseAgainst, WouldChangeIf: r.WouldChangeIf, Drivers: driversFor(r, articles)}
}

// headlinesFor returns up to three most recent articles, independent of
// whether the rating validated.
func headlinesFor(articles []news.Article) []report.Headline {
	sorted := append([]news.Article{}, articles...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date > sorted[j].Date })
	var out []report.Headline
	for _, a := range sorted {
		if len(out) == 3 {
			break
		}
		out = append(out, report.Headline{Title: a.Title, Source: a.Source, URL: a.URL, Date: a.Date})
	}
	return out
}

func heldSuffix(f tickerFact) string {
	if !f.w.Held {
		return ""
	}
	if f.w.PositionPct == nil {
		return " (held)"
	}
	return fmt.Sprintf(" (held, %s%%)", strconv.FormatFloat(*f.w.PositionPct, 'f', -1, 64))
}

// itemView builds an ACT or REVIEW entry. It stands on facts, headlines and a
// fixed question even when the model rating is unavailable.
func itemView(f tickerFact, p watchlist.Placement, cfg config.File, today time.Time, newsOK bool) report.Item {
	item := report.Item{
		Context:   watchlist.ContextLine(p.Logged, f.price, today, cfg.Memory.DecisionContextWeeks),
		Headlines: headlinesFor(f.articles),
		Model:     modelView(f, f.articles),
	}
	if !newsOK {
		item.NewsNote = "Headlines unavailable this week."
	}
	if p.Bucket == watchlist.BucketACT {
		item.Header = fmt.Sprintf("%s - ACT - %s", f.w.Ticker, p.Numeric)
		if facts, cond, ok := watchlist.DescribeTrigger(f.w, f.price, f.ind.RSI14); ok {
			item.Lines = append(item.Lines, facts)
			if p.Numeric == watchlist.DecisionTrigger {
				item.Lines = append(item.Lines, "Your condition is met.")
			} else {
				item.Lines = append(item.Lines, "Not triggered yet. Triggers at: "+cond)
			}
		}
		if p.Numeric == watchlist.DecisionTrigger {
			item.Question = decisionQuestion(f)
		}
		return item
	}
	item.Header = fmt.Sprintf("%s - REVIEW", f.w.Ticker)
	negative := false
	for _, e := range p.NewEvents {
		item.Lines = append(item.Lines, e.Fact+heldSuffix(f))
		negative = negative || e.Negative
	}
	switch {
	case p.Resurfaced && p.Logged != nil:
		item.Question = watchlist.WaitedQuestion(p.Logged)
	case negative:
		item.Question = "What changed? Thesis break, or re-rating?"
	default:
		item.Question = decisionQuestion(f)
	}
	return item
}

func eventKinds(events []watchlist.AttentionEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
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
	return store.Save(ctx, history.Snapshot{Week: week, Ticker: f.w.Ticker, Price: f.price, Status: f.status, AttentionKinds: eventKinds(f.events), WeeksActive: f.weeks, InDecisions: f.decision, Bucket: string(f.place.Bucket), Suppressed: f.place.Suppressed, SuppressedReason: f.place.SuppressedReason, Rating: f.rating.Rating, Confidence: f.rating.Confidence, Drivers: drivers, Articles: articles, Facts: facts, Macro: macroJSON, LLM: llmJSON, SPYPrice: mc.SPYPrice})
}

// sheetRows appends one row per ACT or REVIEW item, plus held names the model
// rated BUY, TRIM or SELL. Sync is idempotent on (week, ticker) and never
// touches my_decision or my_reason.
func sheetRows(week string, facts []tickerFact) []judgment.Row {
	rows := []judgment.Row{}
	for _, f := range facts {
		status := ""
		switch {
		case f.decision:
			status = f.status
		case f.w.Held && f.ratingOK && (f.rating.Rating == "BUY" || f.rating.Rating == "TRIM" || f.rating.Rating == "SELL"):
			status = "RATING"
		default:
			continue
		}
		rows = append(rows, judgment.Row{Week: week, Ticker: f.w.Ticker, Status: status, SummaryRef: "firestore:" + f.w.Ticker + "_" + week, ModelRating: f.rating.Rating, ModelConfidence: f.rating.Confidence})
	}
	return rows
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
func evidenceFor(f tickerFact, mc macro.Context, mb llm.MacroBrief) []llm.Evidence {
	e := []llm.Evidence{}
	if f.status == string(watchlist.DecisionTrigger) || f.status == string(watchlist.DecisionNear) {
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
	if f.prev != nil && f.prev.Rating != "NO RATING" {
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
