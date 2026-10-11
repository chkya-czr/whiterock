package watchlist

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
)

// Bucket is where a ticker lands in the weekly email.
type Bucket string

const (
	BucketACT    Bucket = "ACT"
	BucketReview Bucket = "REVIEW"
	BucketFYI    Bucket = "FYI"
)

// stateKinds describe a standing condition rather than something that
// happened. Without any prior snapshot they cannot be told apart from
// "already true for months", so they are treated as ongoing.
var stateKinds = map[string]bool{"BELOW_52W_HIGH": true, "RSI_LOW": true, "RSI_HIGH": true}

// LoggedDecision is the investor's most recent non-blank my_decision for a
// ticker, joined with the Firestore snapshot of that decision's week.
type LoggedDecision struct {
	Week, Decision, Reason string
	SnapshotFound          bool
	KindsThen              []string
	PriceThen              float64
}

// Input is everything the classifier needs for one ticker.
type Input struct {
	Ticker            string
	Held              bool
	Numeric           DecisionKind // TRIGGER or NEAR TRIGGER; empty when no numeric trigger fired
	Events            []AttentionEvent
	WeeksActive       map[string]int
	HasPrior          bool
	WeekPct, MonthPct float64
	Logged            *LoggedDecision
}

// Placement is a classified ticker plus the reason, for dry-run output.
type Placement struct {
	Ticker, Reason   string
	Bucket           Bucket
	Held             bool
	Numeric          DecisionKind
	NewEvents        []AttentionEvent
	OngoingEvents    []AttentionEvent
	Magnitude        float64
	Listed           bool // FYI names with attention events are listed; quiet names are not
	Suppressed       bool
	SuppressedReason string
	Overflow         bool
	Resurfaced       bool // REVIEW item whose Waited suppression window has lapsed
	Logged           *LoggedDecision
	FYILabel         string
	Input            Input
}

type Placed struct {
	Act, Review, FYI, Quiet []Placement
}

// All returns every placement, for dry-run diagnostics.
func (r Placed) All() []Placement {
	out := append([]Placement{}, r.Act...)
	out = append(out, r.Review...)
	out = append(out, r.FYI...)
	return append(out, r.Quiet...)
}

func magnitude(w, m float64) float64 { return math.Max(math.Abs(w), math.Abs(m)) }

func isNew(e AttentionEvent, in Input) bool {
	if in.WeeksActive[e.Kind] != 1 {
		return false
	}
	return in.HasPrior || !stateKinds[e.Kind]
}

func parseWeek(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	return t, err == nil
}

// ageDays is whole days between a logged week and today.
func ageDays(week string, today time.Time) (int, bool) {
	t, ok := parseWeek(week)
	if !ok {
		return 0, false
	}
	return int(today.Sub(t).Hours() / 24), true
}

func isWaited(l *LoggedDecision) bool {
	return l != nil && strings.EqualFold(strings.TrimSpace(l.Decision), "waited")
}

func eventLabel(in Input, e AttentionEvent) string {
	s := e.Short
	if s == "" {
		s = e.Kind
	}
	if isNew(e, in) {
		return s + ", new this week"
	}
	return s + ", ongoing"
}

// Classify assigns one ticker to ACT, a REVIEW candidate, or FYI. Selection
// among REVIEW candidates happens later in Place.
func Classify(in Input, cfg config.File, today time.Time) Placement {
	p := Placement{Ticker: in.Ticker, Held: in.Held, Numeric: in.Numeric, Magnitude: magnitude(in.WeekPct, in.MonthPct), Logged: in.Logged, Input: in}
	for _, e := range in.Events {
		if isNew(e, in) {
			p.NewEvents = append(p.NewEvents, e)
		} else {
			p.OngoingEvents = append(p.OngoingEvents, e)
		}
	}
	describe := func(events []AttentionEvent) string {
		parts := make([]string, len(events))
		for i, e := range events {
			parts[i] = fmt.Sprintf("%s(weeks_active=%d)", e.Kind, in.WeeksActive[e.Kind])
		}
		return strings.Join(parts, ", ")
	}
	switch {
	case in.Numeric != "":
		p.Bucket = BucketACT
		p.Reason = fmt.Sprintf("explicit numeric trigger is %s; never suppressed", in.Numeric)
		return p
	case len(p.NewEvents) > 0:
		p.Bucket = BucketReview // candidate; Place may demote to FYI
		p.Reason = "new attention event(s): " + describe(p.NewEvents)
		if isWaited(in.Logged) {
			p.Reason += suppressionNote(&p, cfg, today)
		}
		return p
	case len(p.OngoingEvents) > 0:
		p.Bucket, p.Listed = BucketFYI, true
		p.Reason = "ongoing event(s) only: " + describe(p.OngoingEvents)
		if !in.HasPrior {
			p.Reason += "; no prior snapshot, state events treated as ongoing"
		}
	default:
		p.Bucket = BucketFYI
		p.Reason = "no attention events (quiet; pulse only)"
	}
	return p
}

// suppressionNote only annotates; the decision is made by applyWaited.
func suppressionNote(p *Placement, cfg config.File, today time.Time) string {
	suppress, resurfaced, why := waitedState(*p, cfg, today)
	switch {
	case suppress:
		return "; " + why
	case resurfaced:
		return "; " + why
	}
	return "; waited-suppression not applied: " + why
}

// waitedState evaluates the Waited rules for a REVIEW candidate.
func waitedState(p Placement, cfg config.File, today time.Time) (suppress, resurfaced bool, why string) {
	l := p.Logged
	if !isWaited(l) {
		return false, false, "no Waited decision"
	}
	age, ok := ageDays(l.Week, today)
	if !ok {
		return false, false, "unparseable decision week"
	}
	limit := cfg.Memory.WaitedSuppressWeeks * 7
	if age >= limit {
		return false, true, fmt.Sprintf("Waited on %s is %d days old (>= %d), resurfaces", l.Week, age, limit)
	}
	if !l.SnapshotFound {
		return false, false, fmt.Sprintf("Waited on %s but that week's snapshot is missing; condition treated as changed", l.Week)
	}
	was := map[string]bool{}
	for _, k := range l.KindsThen {
		was[k] = true
	}
	for _, e := range p.NewEvents {
		if !was[e.Kind] {
			return false, false, fmt.Sprintf("Waited on %s but %s is a new event kind since then", l.Week, e.Kind)
		}
	}
	return true, false, fmt.Sprintf("Waited on %s (%d days ago), no new event kind since then", l.Week, age)
}

// Place classifies all inputs, applies Waited suppression, and selects the
// REVIEW set. Output ordering is deterministic.
func Place(inputs []Input, cfg config.File, today time.Time) Placed {
	var res Placed
	var candidates []Placement
	for _, in := range inputs {
		p := Classify(in, cfg, today)
		switch {
		case p.Bucket == BucketACT:
			res.Act = append(res.Act, p)
		case p.Bucket == BucketReview:
			suppress, resurfaced, why := waitedState(p, cfg, today)
			if suppress {
				p.Bucket, p.Listed, p.Suppressed, p.SuppressedReason = BucketFYI, true, true, why
				p.FYILabel = "waited " + p.Logged.Week
				p.Reason = "suppressed: " + why
				res.FYI = append(res.FYI, p)
				continue
			}
			p.Resurfaced = resurfaced
			candidates = append(candidates, p)
		case p.Listed:
			res.FYI = append(res.FYI, p)
		default:
			res.Quiet = append(res.Quiet, p)
		}
	}
	sel, over := SelectReview(candidates, cfg.Review.MaxItems)
	res.Review = sel
	for _, p := range over {
		p.Bucket, p.Listed, p.Overflow = BucketFYI, true, true
		p.Reason = fmt.Sprintf("REVIEW overflow (max_items=%d); new this week", cfg.Review.MaxItems)
		res.FYI = append(res.FYI, p)
	}
	sort.SliceStable(res.Act, func(i, j int) bool {
		a, b := res.Act[i], res.Act[j]
		if a.Numeric != b.Numeric {
			return a.Numeric == DecisionTrigger
		}
		return byMagnitude(a, b)
	})
	sort.SliceStable(res.FYI, func(i, j int) bool { return byMagnitude(res.FYI[i], res.FYI[j]) })
	sort.SliceStable(res.Quiet, func(i, j int) bool { return res.Quiet[i].Ticker < res.Quiet[j].Ticker })
	for i := range res.FYI {
		if res.FYI[i].FYILabel == "" {
			res.FYI[i].FYILabel = fyiLabel(res.FYI[i])
		}
	}
	return res
}

func byMagnitude(a, b Placement) bool {
	if a.Magnitude != b.Magnitude {
		return a.Magnitude > b.Magnitude
	}
	return a.Ticker < b.Ticker
}

func fyiLabel(p Placement) string {
	parts := make([]string, 0, len(p.Input.Events))
	for _, e := range p.Input.Events {
		parts = append(parts, eventLabel(p.Input, e))
	}
	return strings.Join(parts, "; ")
}

// SelectReview takes up to max candidates by magnitude, guaranteeing one held
// and one non-held name when both kinds exist. Selected items are returned in
// magnitude order; the remainder is overflow.
func SelectReview(c []Placement, max int) (selected, overflow []Placement) {
	sorted := append([]Placement{}, c...)
	sort.SliceStable(sorted, func(i, j int) bool { return byMagnitude(sorted[i], sorted[j]) })
	if len(sorted) <= max {
		return sorted, nil
	}
	pick := map[int]bool{}
	topHeld, topOther := -1, -1
	for i, p := range sorted {
		if p.Held && topHeld < 0 {
			topHeld = i
		}
		if !p.Held && topOther < 0 {
			topOther = i
		}
	}
	if topHeld >= 0 && topOther >= 0 && max >= 2 {
		pick[topHeld], pick[topOther] = true, true
	}
	for i := range sorted {
		if len(pick) >= max {
			break
		}
		pick[i] = true
	}
	for i, p := range sorted {
		if pick[i] {
			selected = append(selected, p)
		} else {
			overflow = append(overflow, p)
		}
	}
	return selected, overflow
}
