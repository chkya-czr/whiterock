// Package report renders the human-facing weekly digest. It deliberately has no
// recommendation language and always produces a report, including quiet weeks.
package report

import (
	"fmt"
	"strings"

	"github.com/aayushgogia/stock-research-tool/internal/judgment"
)

type Item struct {
	Ticker, Status, Comparison, Rationale, Sizing string
	WatchOnly                                     bool
	Brief                                         string
}
type Digest struct {
	Week, Macro                       string
	Triggered, Approaching, WatchOnly []Item
	Judgments                         []judgment.Entry
}

func Render(d Digest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Personal stock watchlist — week of %s\n\n", d.Week)
	fmt.Fprintf(&b, "Macro backdrop\n%s\n", d.Macro)
	section := func(name string, items []Item) {
		fmt.Fprintf(&b, "\n%s\n", name)
		if len(items) == 0 {
			b.WriteString("None this week.\n")
			return
		}
		for _, i := range items {
			fmt.Fprintf(&b, "\n%s — %s\nWhy watched: %s\nPosition sizing: %s\n", i.Ticker, i.Comparison, i.Rationale, i.Sizing)
			if i.Brief != "" {
				fmt.Fprintf(&b, "%s\n", i.Brief)
			}
		}
	}
	section("Triggered", d.Triggered)
	section("Approaching", d.Approaching)
	if len(d.WatchOnly) > 0 {
		section("Watch only (no rule set)", d.WatchOnly)
	}
	if len(d.Judgments) > 0 {
		b.WriteString("\nLast week's judgment log\n")
		for _, j := range d.Judgments {
			fmt.Fprintf(&b, "%s: %s — %s\n", j.Ticker, j.Decision, j.Reason)
		}
	}
	b.WriteString("\nThis is a factual research digest for human review, not investment advice.\n")
	return b.String()
}
