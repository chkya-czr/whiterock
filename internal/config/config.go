// Package config loads and validates the hand-maintained watchlist.
package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Attention Attention `yaml:"attention"`
	Rating    Rating    `yaml:"rating"`
	News      News      `yaml:"news"`
	Review    Review    `yaml:"review"`
	Memory    Memory    `yaml:"memory"`
	Watchlist []Watch   `yaml:"watchlist"`
}

type Attention struct {
	PctBelow52WHigh float64 `yaml:"pct_below_52w_high"`
	RSILow          float64 `yaml:"rsi_low"`
	RSIHigh         float64 `yaml:"rsi_high"`
	WeeklyMovePct   float64 `yaml:"weekly_move_pct"`
	MonthlyMovePct  float64 `yaml:"monthly_move_pct"`
	SMA200Cross     bool    `yaml:"sma200_cross"`
	New52WExtreme   bool    `yaml:"new_52w_extreme"`
	ApproachingBand float64 `yaml:"approaching_band_pct"`
}

// Review bounds the REVIEW and FYI sections of the weekly email.
type Review struct {
	MaxItems     int `yaml:"max_items"`
	FYIMaxListed int `yaml:"fyi_max_listed"`
}

// Memory controls how long a logged "Waited" decision suppresses a REVIEW
// item and how far back a logged decision is quoted as context.
type Memory struct {
	WaitedSuppressWeeks  int `yaml:"waited_suppress_weeks"`
	DecisionContextWeeks int `yaml:"decision_context_weeks"`
}

type Rating struct {
	Horizon string `yaml:"horizon"`
}

type News struct {
	WindowDays int      `yaml:"window_days"`
	NumResults int      `yaml:"num_results"`
	Domains    []string `yaml:"domains"`
}

type Watch struct {
	Ticker             string   `yaml:"ticker"`
	Held               bool     `yaml:"held"`
	PositionPct        *float64 `yaml:"position_pct"`
	PositionCeilingPct *float64 `yaml:"position_ceiling_pct"`
	Trigger            Trigger  `yaml:"trigger"`
	MacroPrecondition  *string  `yaml:"macro_precondition"`
	Rationale          string   `yaml:"rationale"`
	NewsQuery          string   `yaml:"news_query"`
}

type Trigger struct {
	Type      string   `yaml:"type"`
	Value     *float64 `yaml:"value"`
	Entry     *float64 `yaml:"entry"`
	Pct       *float64 `yaml:"pct"`
	Condition string   `yaml:"condition"`
}

func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read watchlist: %w", err)
	}
	return Parse(b)
}

// Parse validates a watchlist obtained from a trusted source, including Secret
// Manager in production. It shares exactly the same validation as file loading.
func Parse(b []byte) (File, error) {
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse watchlist: %w", err)
	}
	applyDefaults(&f)
	if err := f.Validate(); err != nil {
		return File{}, err
	}
	if len(f.Watchlist) == 0 {
		return File{}, fmt.Errorf("watchlist must contain at least one entry")
	}
	seen := map[string]bool{}
	for i := range f.Watchlist {
		w := &f.Watchlist[i]
		w.Ticker = strings.ToUpper(strings.TrimSpace(w.Ticker))
		if w.MacroPrecondition != nil {
			log.Printf("WARNING: watchlist[%d] (%s): macro_precondition is ignored", i, w.Ticker)
			w.MacroPrecondition = nil
		}
		if w.Trigger.Type == "qualitative" {
			log.Printf("WARNING: watchlist[%d] (%s): qualitative trigger is ignored and treated as none", i, w.Ticker)
			w.Trigger = Trigger{Type: "none"}
		}
		if err := w.Validate(); err != nil {
			return File{}, fmt.Errorf("watchlist[%d] (%s): %w", i, w.Ticker, err)
		}
		if seen[w.Ticker] {
			return File{}, fmt.Errorf("duplicate ticker %q", w.Ticker)
		}
		seen[w.Ticker] = true
	}
	return f, nil
}

func applyDefaults(f *File) {
	if f.Attention == (Attention{}) {
		f.Attention = Attention{PctBelow52WHigh: 15, RSILow: 30, RSIHigh: 70, WeeklyMovePct: 8, SMA200Cross: true, New52WExtreme: true, ApproachingBand: 5}
	}
	if f.Attention.MonthlyMovePct == 0 {
		f.Attention.MonthlyMovePct = 20
	}
	if f.Review == (Review{}) {
		f.Review = Review{MaxItems: 3, FYIMaxListed: 15}
	}
	if f.Memory == (Memory{}) {
		f.Memory = Memory{WaitedSuppressWeeks: 4, DecisionContextWeeks: 8}
	}
	if f.Rating.Horizon == "" {
		f.Rating.Horizon = "6-12 months"
	}
	if f.News.WindowDays == 0 && f.News.NumResults == 0 && len(f.News.Domains) == 0 {
		f.News = News{WindowDays: 7, NumResults: 5, Domains: []string{"reuters.com", "bloomberg.com", "cnbc.com", "ft.com", "wsj.com", "barrons.com", "marketwatch.com", "sec.gov"}}
	}
}

func (f File) Validate() error {
	a := f.Attention
	if a.PctBelow52WHigh <= 0 || a.RSILow < 0 || a.RSIHigh > 100 || a.RSILow >= a.RSIHigh || a.WeeklyMovePct <= 0 || a.MonthlyMovePct <= 0 || a.ApproachingBand <= 0 {
		return fmt.Errorf("invalid attention configuration")
	}
	if f.Review.MaxItems <= 0 || f.Review.FYIMaxListed <= 0 {
		return fmt.Errorf("review.max_items and review.fyi_max_listed must be positive")
	}
	if f.Memory.WaitedSuppressWeeks <= 0 || f.Memory.DecisionContextWeeks <= 0 {
		return fmt.Errorf("memory.waited_suppress_weeks and memory.decision_context_weeks must be positive")
	}
	if strings.TrimSpace(f.Rating.Horizon) == "" {
		return fmt.Errorf("rating.horizon is required")
	}
	if f.News.WindowDays <= 0 || f.News.NumResults <= 0 || len(f.News.Domains) == 0 {
		return fmt.Errorf("news.window_days, news.num_results, and news.domains are required")
	}
	return nil
}

func (w Watch) Validate() error {
	if w.Ticker == "" {
		return fmt.Errorf("ticker is required")
	}
	if strings.TrimSpace(w.Rationale) == "" {
		return fmt.Errorf("rationale is required")
	}
	if w.PositionPct != nil && (*w.PositionPct < 0 || *w.PositionPct > 100) {
		return fmt.Errorf("position_pct must be 0..100")
	}
	if w.PositionCeilingPct != nil && (*w.PositionCeilingPct < 0 || *w.PositionCeilingPct > 100) {
		return fmt.Errorf("position_ceiling_pct must be 0..100")
	}
	if w.PositionPct != nil && w.PositionCeilingPct != nil && *w.PositionPct > *w.PositionCeilingPct {
		return fmt.Errorf("position_pct cannot exceed position_ceiling_pct")
	}
	if strings.TrimSpace(w.NewsQuery) == "" {
		return fmt.Errorf("news_query is required")
	}
	switch w.Trigger.Type {
	case "none":
		return nil // Deliberately valid: a visible watch-only holding.
	case "price_below", "price_above", "rsi_below":
		if w.Trigger.Value == nil {
			return fmt.Errorf("trigger.value is required for %s", w.Trigger.Type)
		}
	case "price_above_pct_from_entry":
		if w.Trigger.Entry == nil || w.Trigger.Pct == nil {
			return fmt.Errorf("trigger.entry and trigger.pct are required")
		}
	default:
		return fmt.Errorf("unknown trigger.type %q", w.Trigger.Type)
	}
	return nil
}
