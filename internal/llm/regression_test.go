package llm

import (
	"encoding/json"
	"testing"
)

// These cases pin down how the rating validator treats the failure classes
// seen in production dry-runs: rejections such as `untraceable model number
// "4.07"` and `"14"` on driver claims. The inputs are synthetic
// reconstructions of those classes, not recorded model output. A change to
// the validator that flips any verdict here should be a deliberate decision.

type ratingJSON struct {
	Rating, Confidence, Summary, CaseFor, CaseAgainst, WouldChangeIf, Change string
	Drivers                                                                  []Driver
}

func (r ratingJSON) raw() string {
	b, _ := json.Marshal(map[string]any{
		"rating": r.Rating, "confidence": r.Confidence, "summary": r.Summary,
		"case_for": r.CaseFor, "case_against": r.CaseAgainst, "drivers": r.Drivers,
		"would_change_if": r.WouldChangeIf, "change_since_last_week": r.Change,
	})
	return string(b)
}

func base() ratingJSON {
	return ratingJSON{Rating: "WAIT", Confidence: "LOW", Summary: "facts are mixed", CaseFor: "momentum is improving", CaseAgainst: "valuation is stretched", WouldChangeIf: "new evidence appears", Change: "changed"}
}

func TestRatingValidatorRegressionCases(t *testing.T) {
	evidence := []Evidence{
		{ID: "F1", Text: "price 120.50; RSI 41.20; week -3.10%; month 4.07%"},
		{ID: "F2", Text: "macro regime: late cycle"},
		{ID: "A1", Text: "Company reported Q3 2026 revenue growth of 12% and cut 15% of staff."},
		{ID: "A2", Text: "Shares are at 1,500 after the report."},
	}
	cases := []struct {
		name    string
		mutate  func(r *ratingJSON)
		allowed []string
		prev    string
		wantErr bool
	}{
		{"clean rating passes", func(r *ratingJSON) {}, []string{"BUY", "WAIT"}, "", false},
		{"driver number only in a different source is rejected (4.07 class)", func(r *ratingJSON) {
			r.Drivers = []Driver{{Claim: "month change was 4.07", SourceID: "A1"}}
		}, []string{"BUY", "WAIT"}, "", true},
		{"driver number in its own source passes", func(r *ratingJSON) {
			r.Drivers = []Driver{{Claim: "month change was 4.07", SourceID: "F1"}}
		}, []string{"BUY", "WAIT"}, "", false},
		{"invented window length in prose is rejected (14 class)", func(r *ratingJSON) {
			r.CaseFor = "the 14 day trend is improving"
		}, []string{"BUY", "WAIT"}, "", true},
		{"prose number traceable to any evidence passes", func(r *ratingJSON) {
			r.CaseFor = "revenue growth was 12 percent"
		}, []string{"BUY", "WAIT"}, "", false},
		{"calendar year and quarter labels are tolerated", func(r *ratingJSON) {
			r.Summary = "Q3 2026 results were reported"
		}, []string{"BUY", "WAIT"}, "", false},
		{"comma and percent formatting is tolerated", func(r *ratingJSON) {
			r.CaseFor = "shares are at 1500 and the week moved -3.10%"
		}, []string{"BUY", "WAIT"}, "", false},
		{"unknown source id is rejected", func(r *ratingJSON) {
			r.Drivers = []Driver{{Claim: "macro is late cycle", SourceID: "A9"}}
		}, []string{"BUY", "WAIT"}, "", true},
		{"BUY with one source is rejected", func(r *ratingJSON) {
			r.Rating = "BUY"
			r.Drivers = []Driver{{Claim: "macro is late cycle", SourceID: "F2"}}
		}, []string{"BUY", "WAIT"}, "", true},
		{"BUY with two sources passes", func(r *ratingJSON) {
			r.Rating = "BUY"
			r.Drivers = []Driver{{Claim: "macro is late cycle", SourceID: "F2"}, {Claim: "revenue growth was 12", SourceID: "A1"}}
		}, []string{"BUY", "WAIT"}, "", false},
		{"rating outside the allowed set is rejected", func(r *ratingJSON) { r.Rating = "SELL" }, []string{"BUY", "WAIT"}, "", true},
		{"changed rating claiming no change is rejected", func(r *ratingJSON) {
			r.Change = "No material change"
		}, []string{"BUY", "WAIT"}, "BUY", true},
		{"same rating claiming no change passes", func(r *ratingJSON) {
			r.Change = "No material change"
		}, []string{"BUY", "WAIT"}, "WAIT", false},
	}
	for _, c := range cases {
		r := base()
		c.mutate(&r)
		_, err := ParseRating(r.raw(), c.allowed, evidence, c.prev)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err=%v wantErr=%t", c.name, err, c.wantErr)
		}
	}
}
