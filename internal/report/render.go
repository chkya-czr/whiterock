// Package report renders the weekly digest as plain text. It performs no
// analysis: every string it prints is supplied by the pipeline.
package report

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type Source struct{ ID, Label, URL, Date string }
type Driver struct {
	Claim  string
	Source Source
}
type Headline struct{ Title, Source, URL, Date string }

// Model is the rating annotation. When OK is false only Unavailable (a short
// reason) is rendered; no placeholder case text is ever shown.
type Model struct {
	OK                                                               bool
	Rating, Confidence, Summary, CaseFor, CaseAgainst, WouldChangeIf string
	Drivers                                                          []Driver
	Unavailable                                                      string
}

// Item is one ACT or REVIEW entry. Header is the full header line.
type Item struct {
	Header    string
	Lines     []string
	Notes     []string // data-quality notes shown under the facts
	Context   string
	Headlines []Headline
	NewsNote  string
	Model     Model
	Question  string
}
type Holding struct {
	Ticker        string
	Price         float64
	WeekChangePct float64
	Model         Model
}
type PulseItem struct {
	Status                                      string
	Ticker                                      string
	Price, WeekChangePct, MonthChangePct, RSI14 float64
}

// LastWeek is the "From last week" section. Show=false omits it entirely.
type LastWeek struct {
	Show        bool
	Unavailable bool
	Lines       []string
	More        int
	AllResolved bool
}
type Digest struct {
	Week, MacroSummary, MacroDetails, MacroNotable, PulseSummary string
	Act, Review                                                  []Item
	LastWeek                                                     LastWeek
	Holdings                                                     []Holding
	FYI                                                          []string
	FYIMore                                                      int
	Pulse                                                        []PulseItem
}

// Subject is the email subject line.
func Subject(d Digest) string {
	if len(d.Act) == 0 && len(d.Review) == 0 {
		return fmt.Sprintf("Watchlist - week of %s - nothing to act on", d.Week)
	}
	return fmt.Sprintf("Watchlist - week of %s - %d act, %d review", d.Week, len(d.Act), len(d.Review))
}

func Render(d Digest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", Subject(d))

	section(&b, "ACT", "ACT: nothing this week.", d.Act)
	section(&b, "REVIEW", "REVIEW: nothing new this week.", d.Review)

	if d.LastWeek.Show {
		fmt.Fprint(&b, "\nFrom last week\n")
		switch {
		case d.LastWeek.Unavailable:
			b.WriteString("Decision memory unavailable this week\n")
		case d.LastWeek.AllResolved:
			b.WriteString("All items from last week have a logged decision.\n")
		default:
			for _, l := range d.LastWeek.Lines {
				fmt.Fprintf(&b, "%s\n", l)
			}
			if d.LastWeek.More > 0 {
				fmt.Fprintf(&b, "+%d more\n", d.LastWeek.More)
			}
		}
	}

	if len(d.Holdings) > 0 {
		fmt.Fprint(&b, "\nHoldings\n")
		holdings(&b, d.Holdings)
	}

	if len(d.FYI) > 0 {
		fmt.Fprint(&b, "\nFYI\n")
		for _, l := range d.FYI {
			fmt.Fprintf(&b, "%s\n", l)
		}
		if d.FYIMore > 0 {
			fmt.Fprintf(&b, "+%d more\n", d.FYIMore)
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
	b.WriteString("\nModel-generated rating from public information only; it does not know your cash, tax situation or the rest of your portfolio. Not investment advice.\n")
	return b.String()
}

func section(b *strings.Builder, title, empty string, items []Item) {
	if len(items) == 0 {
		fmt.Fprintf(b, "\n%s\n", empty)
		return
	}
	fmt.Fprintf(b, "\n%s\n", title)
	for _, x := range items {
		fmt.Fprintf(b, "\n%s\n", x.Header)
		for _, l := range x.Lines {
			fmt.Fprintf(b, "%s\n", l)
		}
		for _, n := range x.Notes {
			fmt.Fprintf(b, "Data check: %s\n", n)
		}
		if x.Context != "" {
			fmt.Fprintf(b, "%s\n", x.Context)
		}
		headlines(b, x.Headlines)
		if x.NewsNote != "" {
			fmt.Fprintf(b, "%s\n", x.NewsNote)
		}
		model(b, x.Model)
		if x.Question != "" {
			fmt.Fprintf(b, "Question for review: %s\n", x.Question)
		}
	}
}

func headlines(b *strings.Builder, hs []Headline) {
	if len(hs) == 0 {
		return
	}
	fmt.Fprint(b, "Recent headlines:\n")
	for _, h := range hs {
		title := or(h.Title, h.Source)
		meta := strings.Trim(strings.Join([]string{h.Source, h.Date}, ", "), ", ")
		if h.URL != "" {
			fmt.Fprintf(b, "- [%s](%s) (%s)\n", title, h.URL, meta)
		} else {
			fmt.Fprintf(b, "- %s (%s)\n", title, meta)
		}
	}
}

func model(b *strings.Builder, m Model) {
	if !m.OK {
		fmt.Fprintf(b, "Model rating: unavailable (%s)\n", or(m.Unavailable, "no validated output"))
		return
	}
	fmt.Fprintf(b, "Model: %s (%s) - %s\nFor: %s\nAgainst: %s\n", m.Rating, m.Confidence, m.Summary, m.CaseFor, m.CaseAgainst)
	drivers(b, m.Drivers)
	fmt.Fprintf(b, "Would change if: %s\n", m.WouldChangeIf)
}

// holdings lists non-HOLD validated ratings first (with summary and driver
// links), then everything else, each group by absolute weekly move.
func holdings(b *strings.Builder, hs []Holding) {
	sorted := append([]Holding{}, hs...)
	flagged := func(h Holding) bool { return h.Model.OK && h.Model.Rating != "HOLD" }
	sort.SliceStable(sorted, func(i, j int) bool {
		fi, fj := flagged(sorted[i]), flagged(sorted[j])
		if fi != fj {
			return fi
		}
		ai, aj := math.Abs(sorted[i].WeekChangePct), math.Abs(sorted[j].WeekChangePct)
		if ai != aj {
			return ai > aj
		}
		return sorted[i].Ticker < sorted[j].Ticker
	})
	for _, h := range sorted {
		rating := "no rating"
		if h.Model.OK {
			rating = fmt.Sprintf("%s (%s)", h.Model.Rating, h.Model.Confidence)
		}
		fmt.Fprintf(b, "%s: %.2f (%+.2f%% wk) - %s\n", h.Ticker, h.Price, h.WeekChangePct, rating)
		if flagged(h) {
			fmt.Fprintf(b, "  %s\n", h.Model.Summary)
			drivers(b, h.Model.Drivers)
		}
	}
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
