package watchlist

import (
	"fmt"
	"github.com/aayushgogia/stock-research-tool/internal/config"
)

type Sizing struct {
	AtCeiling   bool
	Description string
}

func CheckSizing(w config.Watch) Sizing {
	if !w.Held {
		return Sizing{Description: "not held"}
	}
	if w.PositionPct == nil {
		return Sizing{Description: "held; portfolio percentage not recorded"}
	}
	if w.PositionCeilingPct == nil {
		return Sizing{Description: fmt.Sprintf("%.2f%% of portfolio; no ceiling set", *w.PositionPct)}
	}
	return Sizing{AtCeiling: *w.PositionPct >= *w.PositionCeilingPct, Description: fmt.Sprintf("%.2f%% of portfolio, ceiling is %.2f%%", *w.PositionPct, *w.PositionCeilingPct)}
}
