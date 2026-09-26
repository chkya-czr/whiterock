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

type opts struct{ config, localEnv, project, twelve, fred, deepinfra, smtpSecret, smtpHost, from, to, sheet, sheetRange, model string }

func main() {
	var o opts
	flag.StringVar(&o.config, "watchlist", "watchlist.yaml", "")
	flag.StringVar(&o.localEnv, "local-env", "", "local-only dotenv file; never use in Cloud Run")
	flag.StringVar(&o.project, "gcp-project", "", "")
	flag.StringVar(&o.twelve, "twelve-data-secret", "", "")
	flag.StringVar(&o.fred, "fred-secret", "", "")
	flag.StringVar(&o.deepinfra, "deepinfra-secret", "", "")
	flag.StringVar(&o.smtpSecret, "smtp-credential-secret", "", "")
	flag.StringVar(&o.smtpHost, "smtp-host", "", "")
	flag.StringVar(&o.from, "mail-from", "", "")
	flag.StringVar(&o.to, "mail-to", "", "")
	flag.StringVar(&o.sheet, "sheet-id", "", "")
	flag.StringVar(&o.sheetRange, "sheet-range", "Sheet1!A:F", "")
	flag.StringVar(&o.model, "llm-model", "deepseek-ai/DeepSeek-V3.2", "")
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
	cfg, e := config.Load(o.config)
	if e != nil {
		return e
	}
	var tokens gcp.TokenSource = gcp.MetadataTokenSource{}
	if o.localEnv != "" {
		tokens = gcp.StaticTokenSource{AccessToken: os.Getenv("GOOGLE_ACCESS_TOKEN")}
	}
	sm := gcp.SecretManager{Project: o.project, Tokens: tokens}
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
	p := llm.NewDeepInfra(strings.TrimSpace(dk), o.model)
	mb, e := macroBrief(ctx, p, mc)
	if e != nil {
		return e
	}
	week := time.Now().UTC().Format("2006-01-02")
	store := history.Firestore{Project: o.project, Tokens: tokens}
	j, e := judgment.Sheets{ID: o.sheet, Range: o.sheetRange, Tokens: tokens}.LastWeek(ctx, time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02"))
	if e != nil {
		return e
	}
	d := report.Digest{Week: week, Macro: mb.Summary, Judgments: j}
	cond := watchlist.MacroConditions{DollarWeak: mc.Dollar1MonthPct < 0, SectorOutperforming: positive(mc.SectorRS), RiskOn: mc.VIXRegime == "low" && mc.CreditRegime == "tight"}
	for _, w := range cfg.Watchlist {
		if e := ticker(ctx, md, p, store, w, week, mc, mb, cond, &d); e != nil {
			return e
		}
	}
	u, pw := smtpCred(cred)
	return delivery.SMTP{Host: o.smtpHost, From: o.from, To: o.to, Username: u, Password: pw}.Send("Weekly personal stock watchlist — "+week, report.Render(d))
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
	if e != nil {
		return e
	}
	price := c[len(c)-1].Close
	status := watchlist.Evaluate(w, price, in, cond)
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
	macroJSON, _ := json.Marshal(mc)
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
	} else if status.WatchOnly {
		d.WatchOnly = append(d.WatchOnly, item)
	}
	return nil
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
