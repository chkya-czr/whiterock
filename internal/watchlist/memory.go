package watchlist

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ContextLine quotes the investor's latest logged decision for a ticker when
// it falls inside the context window. It returns "" otherwise. The price
// clause is omitted when the decision week's snapshot had no price, and the
// reason clause is omitted when the reason is blank.
func ContextLine(l *LoggedDecision, priceNow float64, today time.Time, contextWeeks int) string {
	if l == nil || strings.TrimSpace(l.Decision) == "" {
		return ""
	}
	age, ok := ageDays(l.Week, today)
	if !ok || age >= contextWeeks*7 {
		return ""
	}
	s := fmt.Sprintf("You logged %s on %s", strings.TrimSpace(l.Decision), l.Week)
	if r := strings.TrimRight(strings.TrimSpace(l.Reason), ". "); r != "" {
		s += ": " + r
	}
	s += "."
	if l.SnapshotFound && l.PriceThen > 0 {
		s += fmt.Sprintf(" Price then %.2f, now %.2f (%+.2f%%).", l.PriceThen, priceNow, (priceNow/l.PriceThen-1)*100)
	}
	return s
}

// WaitedQuestion is the question shown when a Waited ticker resurfaces after
// its suppression window.
func WaitedQuestion(l *LoggedDecision) string {
	return fmt.Sprintf("Still waiting? What has changed since %s?", l.Week)
}

// LastWeekInput is one ACT or REVIEW item from last week's snapshots joined
// with the Sheet row logged for it.
type LastWeekInput struct {
	Ticker, Tag      string
	PriceThen        float64
	HavePriceThen    bool
	PriceNow         float64
	Decision, Reason string
}

// LastWeekLines renders the "From last week" section body. Open items come
// first; at most max lines are returned and more counts the remainder.
func LastWeekLines(items []LastWeekInput, max int) (lines []string, more int, allResolved bool) {
	sorted := append([]LastWeekInput{}, items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		oi, oj := strings.TrimSpace(sorted[i].Decision) == "", strings.TrimSpace(sorted[j].Decision) == ""
		if oi != oj {
			return oi
		}
		return sorted[i].Ticker < sorted[j].Ticker
	})
	open := 0
	for _, x := range sorted {
		if strings.TrimSpace(x.Decision) == "" {
			open++
		}
	}
	if len(sorted) > 0 && open == 0 {
		allResolved = true
	}
	for i, x := range sorted {
		if i >= max {
			more = len(sorted) - max
			break
		}
		if d := strings.TrimSpace(x.Decision); d != "" {
			line := fmt.Sprintf("%s: logged %s", x.Ticker, d)
			if r := strings.TrimSpace(x.Reason); r != "" {
				line += " - " + r
			}
			lines = append(lines, line)
			continue
		}
		price := fmt.Sprintf("now %.2f", x.PriceNow)
		if x.HavePriceThen {
			price = fmt.Sprintf("price then %.2f, now %.2f", x.PriceThen, x.PriceNow)
		}
		lines = append(lines, fmt.Sprintf("%s: still open (%s) - %s. Log your decision in the Sheet.", x.Ticker, x.Tag, price))
	}
	return lines, more, allResolved
}
