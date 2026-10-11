// Command weekly-run executes exactly one research cycle and exits non-zero on
// any missing data, rejected LLM output, persistence failure, or email failure.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/gcp"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/localenv"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/report"
)

type opts struct {
	config, localEnv, project, watchlistSecret, twelve, fred, deepinfra, exa, smtpSecret, smtpHost, from, to, sheet, sheetRange, model string
	dryRun                                                                                                                             bool
}

func main() {
	var o opts
	flag.StringVar(&o.config, "watchlist", "watchlist.yaml", "")
	flag.StringVar(&o.localEnv, "local-env", "", "local-only dotenv file; never use in Cloud Run")
	flag.StringVar(&o.project, "gcp-project", "", "")
	flag.StringVar(&o.watchlistSecret, "watchlist-secret", "", "Secret Manager secret containing watchlist YAML; required outside local mode")
	flag.StringVar(&o.twelve, "twelve-data-secret", "", "")
	flag.StringVar(&o.fred, "fred-secret", "", "")
	flag.StringVar(&o.deepinfra, "deepinfra-secret", "", "")
	flag.StringVar(&o.exa, "exa-secret", "exa-api-key", "")
	flag.BoolVar(&o.dryRun, "dry-run", false, "render without email, Firestore, or Sheets writes")
	flag.StringVar(&o.smtpSecret, "smtp-credential-secret", "", "")
	flag.StringVar(&o.smtpHost, "smtp-host", "", "")
	flag.StringVar(&o.from, "mail-from", "", "")
	flag.StringVar(&o.to, "mail-to", "", "")
	flag.StringVar(&o.sheet, "sheet-id", "", "")
	flag.StringVar(&o.sheetRange, "sheet-range", "Sheet1!A:H", "")
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
	cred := ""
	if !o.dryRun {
		cred, e = secret(o.smtpSecret, "SMTP_CREDENTIAL")
		if e != nil {
			return e
		}
	}
	exaKey, exaErr := secret(o.exa, "EXA_API_KEY")
	if exaErr != nil {
		log.Printf("Exa unavailable (non-blocking): %v", exaErr)
	}
	return executePipeline(ctx, o, cfg, strings.TrimSpace(td), strings.TrimSpace(fk), strings.TrimSpace(dk), strings.TrimSpace(exaKey), cred, tokens)
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
	if o.sheetRange == "Sheet1!A:H" {
		set(&o.sheetRange, "SHEET_RANGE")
	}
}
func macroDetails(c macro.Context) string {
	return fmt.Sprintf("Fed funds %.2f%% · 10Y/2Y %.0fbps (%s) · Core PCE %.2f%% · net liquidity %+.2f%% (8wk) · dollar %+.2f%% (1mo) · HY spread %.0fbps (%s) · VIX %.2f (%s)", c.FedFunds, c.CurveSpreadBps, c.CurveState, c.CorePCEYoY, c.Liquidity8WeekPct, c.Dollar1MonthPct, c.HYSpreadBps, c.CreditRegime, c.VIX, c.VIXRegime)
}

func pulseBrief(ctx context.Context, p llm.Provider, items []report.PulseItem) (string, error) {
	if len(items) == 0 {
		return "", nil
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
			return brief.PulseSummary, nil
		}
		lastErr = parseErr
	}
	log.Printf("watchlist pulse LLM rejected; rendering deterministic pulse only: %v", lastErr)
	return "", lastErr
}
func macroBrief(ctx context.Context, p llm.Provider, c macro.Context) (llm.MacroBrief, bool) {
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
			return b, true
		}
		lastErr = parseErr
	}
	log.Printf("macro LLM rejected; using deterministic fallback: %v", lastErr)
	return llm.MacroBrief{RegimeTag: "Deterministic macro facts", Summary: "", NotableShifts: "none"}, false
}
func nums(c macro.Context) []float64 {
	a := []float64{c.FedFunds, c.CurveSpreadBps, c.CorePCEYoY, c.NetLiquidityB, c.Liquidity8WeekPct, c.Dollar, c.Dollar1MonthPct, c.HYSpreadBps, c.VIX, 10, 2, 8, 4}
	for _, v := range c.SectorRS {
		a = append(a, v)
	}
	return a
}
func smtpCred(s string) (string, string) {
	x := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(x) == 2 {
		return x[0], x[1]
	}
	return "", s
}
