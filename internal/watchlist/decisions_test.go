package watchlist

import (
	"github.com/aayushgogia/stock-research-tool/internal/config"
	"testing"
)

func n(v float64) *float64 { return &v }
func TestXAUTriggerBand(t *testing.T) {
	w := config.Watch{Trigger: config.Trigger{Type: "price_below", Value: n(4000)}}
	for _, x := range []struct {
		p  float64
		k  DecisionKind
		ok bool
	}{{3990, DecisionTrigger, true}, {4100, DecisionNear, true}, {4300, "", false}} {
		k, ok := NumericCandidate(w, x.p, 0, 5)
		if k != x.k || ok != x.ok {
			t.Fatalf("%.0f: %q,%t", x.p, k, ok)
		}
	}
}
func TestCandidateOrdering(t *testing.T) {
	in := []Candidate{{Ticker: "A", Kind: DecisionAttention, Held: false, WeeksActive: 2}, {Ticker: "B", Kind: DecisionNear}, {Ticker: "C", Kind: DecisionTrigger}, {Ticker: "D", Kind: DecisionAttention, Held: true}, {Ticker: "E", Kind: DecisionAttention, WeeksActive: 1}, {Ticker: "F", Kind: DecisionAttention, WeeksActive: 1}}
	got, more := Candidates(in)
	if len(got) != 5 || got[0].Ticker != "C" || got[1].Ticker != "B" || got[2].Ticker != "D" || len(more) != 1 {
		t.Fatalf("got=%+v more=%v", got, more)
	}
}
