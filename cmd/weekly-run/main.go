// Command weekly-run executes exactly one research cycle and exits non-zero on
// any missing data, rejected LLM output, persistence failure, or email failure.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/delivery"
	"github.com/aayushgogia/stock-research-tool/internal/gcp"
	"github.com/aayushgogia/stock-research-tool/internal/history"
	"github.com/aayushgogia/stock-research-tool/internal/judgment"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/localenv"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
	"github.com/aayushgogia/stock-research-tool/internal/report"
	"github.com/aayushgogia/stock-research-tool/internal/watchlist"
)

type opts struct{ config, localEnv, project, watchlistSecret, twelve, fred, deepinfra, smtpSecret, smtpHost, from, to, sheet, sheetRange, model string }

func main() {
	var o opts
	flag.StringVar(&o.config, "watchlist", "watchlist.yaml", "")
	flag.StringVar(&o.localEnv, "local-env", "", "local-only dotenv file; never use in Cloud Run")
	flag.StringVar(&o.project, "gcp-project", "", "")
	flag.StringVar(&o.watchlistSecret, "watchlist-secret", "", "Secret Manager secret containing watchlist YAML; required outside local mode")
	flag.StringVar(&o.twelve, "twelve-data-secret", "", "")
	flag.StringVar(&o.fred, "fred-secret", "", "")
	flag.StringVar(&o.deepinfra, "deepinfra-secret", "", "")
	flag.StringVar(&o.smtpSecret, "smtp-credential-secret", "", "")
	flag.StringVar(&o.smtpHost, "smtp-host", "", "")
	flag.StringVar(&o.from, "mail-from", "", "")
	flag.StringVar(&o.to, "mail-to", "", "")
	flag.StringVar(&o.sheet, "sheet-id", "", "")
	flag.StringVar(&o.sheetRange, "sheet-range", "Sheet1!A:F", "")
	flag.StringVar(&o.model, "llm-model", "deepseek-ai/DeepSeek-V4-Pro-0813", "")
	flag.Parse()
	if e := run(context.Background(), o); e != nil {
		log.Printf("FAILED: %v", e)
		os.Exit(1)
	}
}
func run(ctx context.Context, o opts) error {
	if o.localEnv != "" {
		if e := localenv.Load(o.localEnv); e != nil {
			return e
		}
		applyLocalConfig(&o)
	}
	if o.project == "" {
		return fmt.Errorf("--gcp-project required")
	}
	var tokens gcp.TokenSource = gcp.MetadataTokenSource{}
	if o.localEnv != "" {
		tokens = gcp.StaticTokenSource{AccessToken: os.Getenv("GOOGLE_ACCESS_TOKEN")}
	}
	sm := gcp.SecretManager{Project: o.project, Tokens: tokens}
	var cfg config.File
	var e error
	if o.localEnv != "" {
		cfg, e = config.Load(o.config)
	} else {
		if o.watchlistSecret == "" {
			return fmt.Errorf("--watchlist-secret required outside local mode")
		}
		var raw string
		raw, e = sm.Access(ctx, o.watchlistSecret)
		if e == nil {
			cfg, e = config.Parse([]byte(raw))
		}
	}
	if e != nil {
		return e
	}
	log.Printf("weekly run starting: %d watchlist names (local mode=%t)", len(cfg.Watchlist), o.localEnv != "")
	secret := func(n, envName string) (string, error) {
		if o.localEnv != "" {
			if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
				return value, nil
			}
			return "", fmt.Errorf("%s is required in %s", envName, o.localEnv)
		}
		if n == "" {
			return "", fmt.Errorf("required secret flag missing")
		}
		return sm.Access(ctx, n)
	}
	td, e := secret(o.twelve, "TWELVE_DATA_API_KEY")
	if e != nil {
		return e
	}
	fk, e := secret(o.fred, "FRED_API_KEY")
	if e != nil {
		return e
	}
	dk, e := secret(o.deepinfra, "DEEPINFRA_API_KEY")
	if e != nil {
		return e
	}
	cred, e := secret(o.smtpSecret, "SMTP_CREDENTIAL")
	if e != nil {
		return e
	}
	md := marketdata.NewTwelveData(strings.TrimSpace(td))
	mc, e := macro.Build(ctx, macro.NewFred(strings.TrimSpace(fk)), md)
	if e != nil {
		return fmt.Errorf("macro: %w", e)
	}
	log.Printf("macro data fetched: VIX regime=%s, credit=%s, liquidity=%s", mc.VIXRegime, mc.CreditRegime, mc.LiquidityTrend)
	p := llm.NewDeepInfra(strings.TrimSpace(dk), o.model)
	mb, e := macroBrief(ctx, p, mc)
	if e != nil {
		return e
	}
	log.Printf("macro brief ready: %s", mb.RegimeTag)
	week := time.Now().UTC().Format("2006-01-02")
	store := history.Firestore{Project: o.project, Tokens: tokens}
	judgmentSheet := judgment.Sheets{ID: o.sheet, Range: o.sheetRange, Tokens: tokens}
	j, e := judgmentSheet.LastWeek(ctx, time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02"))
	if e != nil {
		return e
	}
	d := report.Digest{Week: week, MacroSummary: mb.Summary, MacroDetails: macroDetails(mc), MacroNotable: mb.NotableShifts, Judgments: j}
	cond := watchlist.MacroConditions{DollarWeak: mc.Dollar1MonthPct < 0, SectorOutperforming: positive(mc.SectorRS), RiskOn: mc.VIXRegime == "low" && mc.CreditRegime == "tight"}
	for _, w := range cfg.Watchlist {
		if e := ticker(ctx, md, p, store, w, week, mc, mb, cond, &d); e != nil {
			return e
		}
	}
	sort.SliceStable(d.Pulse, func(i, j int) bool {
		if d.Pulse[i].Status != d.Pulse[j].Status {
			return d.Pulse[i].Status == string(watchlist.Quiet)
		}
		return d.Pulse[i].Ticker < d.Pulse[j].Ticker
	})
	d.PulseSummary = pulseBrief(ctx, p, d.Pulse)
	log.Printf("watchlist buckets: %d triggered, %d approaching, %d pulse names", len(d.Triggered), len(d.Approaching), len(d.Pulse))
	syncJudgmentLog(ctx, judgmentSheet, week, d)
	u, pw := smtpCred(cred)
	if e := (delivery.SMTP{Host: o.smtpHost, From: o.from, To: o.to, Username: u, Password: pw}).Send("Weekly personal stock watchlist — "+week, report.Render(d)); e != nil {
		return e
	}
	log.Printf("weekly run complete: digest delivered")
	return nil
}

func applyLocalConfig(o *opts) {
	set := func(target *string, env string) {
		if *target == "" {
			*target = strings.TrimSpace(os.Getenv(env))
		}
	}
	set(&o.project, "GCP_PROJECT")
	set(&o.smtpHost, "SMTP_HOST")
	set(&o.from, "MAIL_FROM")
	set(&o.to, "MAIL_TO")
	set(&o.sheet, "SHEET_ID")
	if o.model == "deepseek-ai/DeepSeek-V4-Pro-0813" {
		set(&o.model, "LLM_MODEL")
	}
	if o.sheetRange == "Sheet1!A:F" {
		set(&o.sheetRange, "SHEET_RANGE")
	}
}
func ticker(ctx context.Context, md marketdata.Client, p llm.Provider, store history.Store, w config.Watch, week string, mc macro.Context, mb llm.MacroBrief, cond watchlist.MacroConditions, d *report.Digest) error {
	c, e := md.Daily(ctx, w.Ticker, 300)
	if e != nil {
		return fmt.Errorf("%s market data: %w", w.Ticker, e)
	}
	in, e := marketdata.Compute(c)
	partialHistory := false
	if e != nil {
		if w.Trigger.Type != "none" {
			return fmt.Errorf("%s indicators: %w", w.Ticker, e)
		}
		in, e = marketdata.ComputePulse(c)
		if e != nil {
			return fmt.Errorf("%s watch-only pulse: %w", w.Ticker, e)
		}
		partialHistory = true
	}
	price := c[len(c)-1].Close
	status := watchlist.Evaluate(w, price, in, cond)
	if partialHistory {
		log.Printf("ticker %s: %s (watch-only, partial history: %d sessions)", w.Ticker, status.Status, in.HistorySessions)
	} else {
		log.Printf("ticker %s: %s", w.Ticker, status.Status)
	}
	size := watchlist.CheckSizing(w)
	prev, e := store.Previous(ctx, w.Ticker, week)
	if e != nil {
		return e
	}
	facts := struct {
		Price      float64
		Indicators marketdata.Indicators
		Comparison string
		Previous   *history.Snapshot
		Sizing     string
	}{price, in, status.Comparison, prev, size.Description}
	fb, _ := json.Marshal(facts)
	macroJSON, _ := json.Marshal(struct {
		Context macro.Context  `json:"context"`
		Brief   llm.MacroBrief `json:"brief"`
	}{mc, mb})
	var out json.RawMessage
	brief := ""
	if status.Status != watchlist.Quiet {
		b, e := tickerBrief(ctx, p, w, status, price, in, prev, mc, mb, size.Description)
		if e != nil {
			return e
		}
		out, _ = json.Marshal(b)
		brief = b.CaseFor + "\nCase against: " + b.CaseAgainst + "\nChange: " + b.ChangeSinceLastWeek + "\nRisk: " + b.Risk
	}
	if e := store.Save(ctx, history.Snapshot{Week: week, Ticker: w.Ticker, Price: price, Status: string(status.Status), Facts: fb, Macro: macroJSON, LLM: out}); e != nil {
		return e
	}
	item := report.Item{Ticker: w.Ticker, Status: string(status.Status), Comparison: status.Comparison, Rationale: w.Rationale, Sizing: size.Description, WatchOnly: status.WatchOnly, Brief: brief}
	if status.Status == watchlist.Triggered {
		d.Triggered = append(d.Triggered, item)
	} else if status.Status == watchlist.Approaching {
		d.Approaching = append(d.Approaching, item)
	} else {
		pulseStatus := string(status.Status)
		if status.WatchOnly {
			pulseStatus = "WATCH-ONLY-NO-RULE"
			d.WatchOnly = append(d.WatchOnly, item)
		}
		d.Pulse = append(d.Pulse, report.PulseItem{Ticker: w.Ticker, Status: pulseStatus, Price: price, WeekChangePct: in.WeekChangePct, MonthChangePct: in.MonthChangePct, RSI14: in.RSI14})
	}
	return nil
}

// syncJudgmentLog appends a blank decision-log row for each newly triggered
// or approaching ticker this run. It is a nice-to-have layer on top of
// Firestore, which remains the source of truth regardless of outcome here,
// so failures are logged and never block the email send.
func syncJudgmentLog(ctx context.Context, sheet judgment.Writer, week string, d report.Digest) {
	var rows []judgment.Row
	for _, item := range d.Triggered {
		rows = append(rows, judgment.Row{Week: week, Ticker: item.Ticker, Status: "TRIGGERED", SummaryRef: "firestore:" + item.Ticker + "_" + week})
	}
	for _, item := range d.Approaching {
		rows = append(rows, judgment.Row{Week: week, Ticker: item.Ticker, Status: "APPROACHING", SummaryRef: "firestore:" + item.Ticker + "_" + week})
	}
	if len(rows) == 0 {
		return
	}
	log.Printf("judgment log sync: attempting %d row(s)", len(rows))
	if e := sheet.Sync(ctx, rows); e != nil {
		log.Printf("judgment log sync failed (non-blocking): %v", e)
		return
	}
	log.Printf("judgment log sync complete")
}

func macroDetails(c macro.Context) string {
	return fmt.Sprintf("Fed funds %.2f%% · 10Y/2Y %.0fbps (%s) · Core PCE %.2f%% · net liquidity %+.2f%% (8wk) · dollar %+.2f%% (1mo) · HY spread %.0fbps (%s) · VIX %.2f (%s)", c.FedFunds, c.CurveSpreadBps, c.CurveState, c.CorePCEYoY, c.Liquidity8WeekPct, c.Dollar1MonthPct, c.HYSpreadBps, c.CreditRegime, c.VIX, c.VIXRegime)
}

func pulseBrief(ctx context.Context, p llm.Provider, items []report.PulseItem) string {
	if len(items) == 0 {
		return ""
	}
	facts := make([]llm.PulseFact, 0, len(items))
	for _, item := range items {
		facts = append(facts, llm.PulseFact{Ticker: item.Ticker, Status: item.Status, Price: item.Price, WeekChangePct: item.WeekChangePct, MonthChangePct: item.MonthChangePct, RSI14: item.RSI14})
	}
	system, user, allowed := llm.PulsePrompt(facts)
	var lastErr error
	for i := 0; i < 2; i++ {
		raw, callErr := p.Complete(ctx, system, user)
		if callErr != nil {
			lastErr = callErr
			continue
		}
		brief, parseErr := llm.ParsePulse(raw, allowed)
		if parseErr == nil {
			log.Printf("watchlist pulse summary ready")
			return brief.PulseSummary
		}
		lastErr = parseErr
	}
	log.Printf("watchlist pulse LLM rejected; rendering deterministic pulse only: %v", lastErr)
	return ""
}
func macroBrief(ctx context.Context, p llm.Provider, c macro.Context) (llm.MacroBrief, error) {
	u := fmt.Sprintf("Fed funds %.2f%%; 10Y/2Y %.0f bps (%s); Core PCE YoY %.2f%%; net liquidity %.2fB (%s over 8 weeks, %.2f%%); dollar %.2f (%s over 1 month, %.2f%%); HY spread %.0f bps (%s); VIX %.2f (%s); sector RS: %s.", c.FedFunds, c.CurveSpreadBps, c.CurveState, c.CorePCEYoY, c.NetLiquidityB, c.LiquidityTrend, c.Liquidity8WeekPct, c.Dollar, c.DollarTrend, c.Dollar1MonthPct, c.HYSpreadBps, c.CreditRegime, c.VIX, c.VIXRegime, strings.Join(macro.RankedSectorRS(c.SectorRS), ", "))
	sys := "Use only the supplied facts. State current macro state and trend only; no forecasts. Return JSON: regime_tag, summary, notable_shifts. The summary must be three or four sentences. Do not use digits or numeric symbols in any JSON value: refer to measures by name and their supplied qualitative state instead."
	allowed := nums(c)
	var lastErr error
	for i := 0; i < 2; i++ {
		raw, callErr := p.Complete(ctx, sys, u)
		if callErr != nil {
			lastErr = callErr
			continue
		}
		b, parseErr := llm.ParseMacro(raw, allowed)
		if parseErr == nil {
			return b, nil
		}
		lastErr = parseErr
	}
	log.Printf("macro LLM rejected; using deterministic fallback: %v", lastErr)
	return llm.MacroBrief{RegimeTag: "Deterministic macro facts", Summary: u, NotableShifts: "none"}, nil
}
func tickerBrief(ctx context.Context, p llm.Provider, w config.Watch, s watchlist.Result, price float64, in marketdata.Indicators, prev *history.Snapshot, mc macro.Context, mb llm.MacroBrief, size string) (llm.TickerBrief, error) {
	last := "no prior snapshot"
	allowed := append(nums(mc), price, in.RSI14, in.SMA50, in.SMA200, in.High52, in.Low52, in.FromHighPct, in.FromLowPct, in.WeekChangePct, in.MonthChangePct, 14, 50, 200, 52)
	if prev != nil {
		last = fmt.Sprintf("last week: %s at %.2f", prev.Status, prev.Price)
		allowed = append(allowed, prev.Price)
	}
	u := fmt.Sprintf("Ticker %s. Rationale: %q. Deterministic status: %s; exact comparison: %s. Price %.2f. 52-week low %.2f, high %.2f, from high %.2f%%, from low %.2f%%. RSI(14) %.2f; SMA50 %.2f; SMA200 %.2f; weekly %.2f%%; monthly %.2f%%. %s. Position sizing: %s. Macro %s — %s. Sector RS %v.", w.Ticker, w.Rationale, s.Status, s.Comparison, price, in.Low52, in.High52, in.FromHighPct, in.FromLowPct, in.RSI14, in.SMA50, in.SMA200, in.WeekChangePct, in.MonthChangePct, last, size, mb.RegimeTag, mb.Summary, mc.SectorRS)
	sys := "Use ONLY supplied facts; give no advice or recommendation; never invent news. Present both cases. Return JSON only: case_for, case_against, change_since_last_week, risk. Do not use digits or numeric symbols in any JSON value: describe supplied facts by name and direction/state instead."
	var lastErr error
	for i := 0; i < 2; i++ {
		raw, callErr := p.Complete(ctx, sys, u)
		if callErr != nil {
			lastErr = callErr
			continue
		}
		b, parseErr := llm.ParseTicker(raw, allowed)
		if parseErr == nil {
			return b, nil
		}
		lastErr = parseErr
	}
	log.Printf("ticker %s LLM rejected; using deterministic fallback: %v", w.Ticker, lastErr)
	return llm.TickerBrief{CaseFor: "No validated generated brief is available from the supplied facts.", CaseAgainst: "No validated generated brief is available from the supplied facts.", ChangeSinceLastWeek: "No validated generated comparison is available.", Risk: "The supplied facts may be incomplete."}, nil
}
func nums(c macro.Context) []float64 {
	a := []float64{c.FedFunds, c.CurveSpreadBps, c.CorePCEYoY, c.NetLiquidityB, c.Liquidity8WeekPct, c.Dollar, c.Dollar1MonthPct, c.HYSpreadBps, c.VIX, 10, 2, 8, 4}
	for _, v := range c.SectorRS {
		a = append(a, v)
	}
	return a
}
func positive(m map[string]float64) bool {
	for _, v := range m {
		if v > 0 {
			return true
		}
	}
	return false
}
func smtpCred(s string) (string, string) {
	x := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(x) == 2 {
		return x[0], x[1]
	}
	return "", s
}
