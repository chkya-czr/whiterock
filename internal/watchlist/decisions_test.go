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
