package report

import (
	"fmt"
	"strings"

	"github.com/aayushgogia/stock-research-tool/internal/judgment"
)

type Source struct{ ID, Label, URL, Date string }
type Driver struct {
	Claim  string
	Source Source
}
type Decision struct {
	Ticker, Kind, Rating, Confidence, PreviousRating, Summary, CaseFor, CaseAgainst, WouldChangeIf, Question, NewsNote string
	Facts                                                                                                              []string
	Drivers                                                                                                            []Driver
}
type Holding struct {
	Ticker, Rating, Confidence, Summary string
	Price, WeekChangePct                float64
	Drivers                             []Driver
}
type PulseItem struct {
	Status                                      string
	Ticker                                      string
	Price, WeekChangePct, MonthChangePct, RSI14 float64
}
type Digest struct {
	Week, MacroSummary, MacroDetails, MacroNotable, PulseSummary string
	Decisions                                                    []Decision
	Radar                                                        []string
	Holdings                                                     []Holding
	Pulse                                                        []PulseItem
	Triggered, Approaching, WatchOnly                            []Item
	Judgments                                                    []judgment.Entry
}
type Item struct {
	Ticker, Status, Comparison, Rationale, Sizing, Brief string
	WatchOnly                                            bool
}

func Render(d Digest) string {
	var b strings.Builder
	title := fmt.Sprintf("Watchlist - week of %s - %d need a decision", d.Week, len(d.Decisions))
	if len(d.Decisions) == 0 {
		title = fmt.Sprintf("Watchlist - week of %s - nothing needs a decision", d.Week)
	}
	fmt.Fprintf(&b, "%s\n\nDecisions this week\n", title)
	if len(d.Decisions) == 0 {
		b.WriteString("Nothing needs a decision this week.\n")
	}
	for _, x := range d.Decisions {
		fmt.Fprintf(&b, "\n%s - %s\nModel rating: %s (%s) - last week: %s\n", x.Ticker, x.Kind, x.Rating, x.Confidence, or(x.PreviousRating, "none"))
		for _, f := range x.Facts {
			fmt.Fprintf(&b, "Why it surfaced: %s\n", f)
		}
		fmt.Fprintf(&b, "Summary: %s\nCase for: %s\nCase against: %s\n", x.Summary, x.CaseFor, x.CaseAgainst)
		drivers(&b, x.Drivers)
		if x.NewsNote != "" {
			fmt.Fprintf(&b, "%s\n", x.NewsNote)
		}
		fmt.Fprintf(&b, "Would change if: %s\n", x.WouldChangeIf)
		if x.Question != "" {
			fmt.Fprintf(&b, "Question for review: %s\n", x.Question)
		}
	}
	if len(d.Radar) > 0 {
		fmt.Fprintf(&b, "\nAlso on the radar: %s\n", strings.Join(d.Radar, ", "))
	}
	fmt.Fprint(&b, "\nHoldings review\n")
	for _, h := range d.Holdings {
		fmt.Fprintf(&b, "%s: %.2f (%+.2f%% wk) %s (%s) - %s\n", h.Ticker, h.Price, h.WeekChangePct, h.Rating, h.Confidence, h.Summary)
		if h.Rating != "HOLD" {
			drivers(&b, h.Drivers)
		}
	}
	fmt.Fprint(&b, "\nMacro backdrop\n")
	if d.MacroSummary != "" {
		fmt.Fprintf(&b, "%s\n", d.MacroSummary)
	} else {
		b.WriteString("Summary unavailable this week; deterministic macro details follow.\n")
	}
	if d.MacroDetails != "" {
		fmt.Fprintf(&b, "Details: %s\n", d.MacroDetails)
	}
	if strings.TrimSpace(strings.ToLower(d.MacroNotable)) != "" && strings.TrimSpace(strings.ToLower(d.MacroNotable)) != "none" {
		fmt.Fprintf(&b, "Notable this week: %s\n", d.MacroNotable)
	}
	fmt.Fprint(&b, "\nWatchlist pulse\n")
	if d.PulseSummary != "" {
		fmt.Fprintf(&b, "%s\n", d.PulseSummary)
	}
	for _, p := range d.Pulse {
		fmt.Fprintf(&b, "%s: %.2f (%+.2f%% wk, %+.2f%% mo) RSI %.2f\n", p.Ticker, p.Price, p.WeekChangePct, p.MonthChangePct, p.RSI14)
	}
	if len(d.Judgments) > 0 {
		b.WriteString("\nLast week's human decisions\n")
		for _, j := range d.Judgments {
			if j.Decision != "" || j.Reason != "" {
				line := or(j.Decision, "no decision logged")
				if j.Reason != "" {
					line += " — " + j.Reason
				}
				fmt.Fprintf(&b, "%s: %s\n", j.Ticker, line)
			}
		}
	}
	b.WriteString("\nModel-generated rating from public information only; it does not know your cash, tax situation or the rest of your portfolio. Not investment advice.\n")
	return b.String()
}
func drivers(b *strings.Builder, ds []Driver) {
	for _, d := range ds {
		if d.Source.URL != "" {
			fmt.Fprintf(b, "Driver [%s](%s) (%s): %s\n", d.Source.Label, d.Source.URL, d.Source.Date, d.Claim)
		} else {
			fmt.Fprintf(b, "Driver %s: %s\n", d.Source.ID, d.Claim)
		}
	}
}
func or(v, f string) string {
	if v == "" {
		return f
	}
	return v
}
