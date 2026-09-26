// Package config loads and validates the hand-maintained watchlist.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Watchlist []Watch `yaml:"watchlist"`
}

type Watch struct {
	Ticker             string   `yaml:"ticker"`
	Held               bool     `yaml:"held"`
	PositionPct        *float64 `yaml:"position_pct"`
	PositionCeilingPct *float64 `yaml:"position_ceiling_pct"`
	Trigger            Trigger  `yaml:"trigger"`
	MacroPrecondition  *string  `yaml:"macro_precondition"`
	Rationale          string   `yaml:"rationale"`
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
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("parse watchlist: %w", err)
	}
	if len(f.Watchlist) == 0 {
		return File{}, fmt.Errorf("watchlist must contain at least one entry")
	}
	seen := map[string]bool{}
	for i := range f.Watchlist {
		w := &f.Watchlist[i]
		w.Ticker = strings.ToUpper(strings.TrimSpace(w.Ticker))
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
	validMacro := map[string]bool{"": true, "dollar_weak": true, "sector_outperforming": true, "risk_on": true}
	if w.MacroPrecondition != nil && !validMacro[*w.MacroPrecondition] {
		return fmt.Errorf("unknown macro_precondition %q", *w.MacroPrecondition)
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
	case "qualitative":
		if strings.TrimSpace(w.Trigger.Condition) == "" {
			return fmt.Errorf("trigger.condition is required for qualitative")
		}
	default:
		return fmt.Errorf("unknown trigger.type %q", w.Trigger.Type)
	}
	return nil
}
