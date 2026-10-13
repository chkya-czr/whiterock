package watchlist

import (
	"fmt"
	"math"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

// DataIssue is an informational finding about the price series. It never
// changes bucketing; it is logged and shown beside the affected item.
type DataIssue struct{ Kind, Detail string }

const (
	// MaxDailyMovePct is large enough that a normal earnings gap rarely trips
	// it, so a hit is more likely an unadjusted split or a bad bar.
	MaxDailyMovePct = 25.0
	// MaxStaleDays tolerates a long weekend plus a holiday.
	MaxStaleDays = 7
)

// CheckCandles inspects chronological daily closes for data problems that
// would silently corrupt every derived number.
func CheckCandles(c []marketdata.Candle, now time.Time) []DataIssue {
	var out []DataIssue
	if len(c) == 0 {
		return out
	}
	var worst float64
	var worstDate time.Time
	for i, x := range c {
		if x.Close <= 0 {
			out = append(out, DataIssue{"NON_POSITIVE_CLOSE", fmt.Sprintf("non-positive close %.2f on %s", x.Close, x.Date.Format("2006-01-02"))})
			continue
		}
		if i == 0 {
			continue
		}
		prev := c[i-1]
		if !x.Date.After(prev.Date) {
			out = append(out, DataIssue{"BAD_ORDER", fmt.Sprintf("bar dated %s does not follow %s", x.Date.Format("2006-01-02"), prev.Date.Format("2006-01-02"))})
			continue
		}
		if prev.Close > 0 {
			if m := math.Abs(x.Close/prev.Close-1) * 100; m > worst {
				worst, worstDate = m, x.Date
			}
		}
	}
	if worst > MaxDailyMovePct {
		out = append(out, DataIssue{"LARGE_DAILY_MOVE", fmt.Sprintf("largest one-day move is %.1f%% on %s; confirm it is not a split or data error", worst, worstDate.Format("2006-01-02"))})
	}
	last := c[len(c)-1].Date
	if age := int(now.Sub(last).Hours() / 24); age > MaxStaleDays {
		out = append(out, DataIssue{"STALE_DATA", fmt.Sprintf("latest bar is %d days old (%s)", age, last.Format("2006-01-02"))})
	}
	return out
}
