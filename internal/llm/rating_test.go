package llm

import "testing"

func TestRatingValidatorRejectsOneSourceBuy(t *testing.T) {
	raw := `{"rating":"BUY","confidence":"LOW","summary":"price 10","case_for":"price 10","case_against":"price 10","drivers":[{"claim":"price 10","source_id":"F1"}],"would_change_if":"price 10","change_since_last_week":"changed"}`
	_, e := ParseRating(raw, []string{"BUY", "WAIT"}, []Evidence{{ID: "F1", Text: "price 10"}}, "")
	if e == nil {
		t.Fatal("expected one-source BUY rejection")
	}
}

func TestAllowedRatingsCeiling(t *testing.T) {
	if contains(AllowedRatings(true, true), "BUY") {
		t.Fatal("ceiling must exclude BUY")
	}
	if contains(AllowedRatings(false, false), "SELL") {
		t.Fatal("unheld must exclude SELL")
	}
}

func TestRatingValidatorRejectsUnknownSourceAndHallucinatedNumber(t *testing.T) {
	evidence := []Evidence{{ID: "F1", Text: "price 10"}, {ID: "A1", Text: "revenue 20"}}
	badSource := `{"rating":"BUY","confidence":"LOW","summary":"price 10","case_for":"price 10","case_against":"revenue 20","drivers":[{"claim":"price 10","source_id":"F1"},{"claim":"revenue 20","source_id":"X9"}],"would_change_if":"price 10","change_since_last_week":"changed"}`
	if _, err := ParseRating(badSource, []string{"BUY", "WAIT"}, evidence, ""); err == nil {
		t.Fatal("expected unknown source rejection")
	}
	hallucinated := `{"rating":"WAIT","confidence":"LOW","summary":"price 11","case_for":"price 10","case_against":"revenue 20","drivers":[],"would_change_if":"price 10","change_since_last_week":"changed"}`
	if _, err := ParseRating(hallucinated, []string{"BUY", "WAIT"}, evidence, ""); err == nil {
		t.Fatal("expected hallucinated number rejection")
	}
}

func TestRatingValidatorRejectsUnexplainedRatingChange(t *testing.T) {
	raw := `{"rating":"BUY","confidence":"LOW","summary":"price 10","case_for":"price 10","case_against":"revenue 20","drivers":[{"claim":"price 10","source_id":"F1"},{"claim":"revenue 20","source_id":"A1"}],"would_change_if":"price 10","change_since_last_week":"no material change"}`
	_, err := ParseRating(raw, []string{"BUY", "WAIT"}, []Evidence{{ID: "F1", Text: "price 10"}, {ID: "A1", Text: "revenue 20"}}, "WAIT")
	if err == nil {
		t.Fatal("expected rating change rejection")
	}
}

func TestRatingValidatorRejectsNumberOutsideSummaryAndCases(t *testing.T) {
	raw := `{"rating":"WAIT","confidence":"LOW","summary":"price 10","case_for":"price 10","case_against":"price 10","drivers":[],"would_change_if":"price 11","change_since_last_week":"changed"}`
	_, err := ParseRating(raw, []string{"WAIT"}, []Evidence{{ID: "F1", Text: "price 10"}}, "")
	if err == nil {
		t.Fatal("expected would_change_if number rejection")
	}
}

func TestRatingValidatorAllowsYearAndQuarterLabels(t *testing.T) {
	raw := `{"rating":"WAIT","confidence":"LOW","summary":"Q3 2026 context","case_for":"price 10","case_against":"price 10","drivers":[],"would_change_if":"Q4 2026","change_since_last_week":"changed"}`
	if _, err := ParseRating(raw, []string{"WAIT"}, []Evidence{{ID: "F1", Text: "price 10"}}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRatingValidatorAllowsDashedYearRange(t *testing.T) {
	raw := `{"rating":"WAIT","confidence":"LOW","summary":"the 2027-2028 context","case_for":"price 10","case_against":"price 10","drivers":[],"would_change_if":"Q4 2028","change_since_last_week":"changed"}`
	if _, err := ParseRating(raw, []string{"WAIT"}, []Evidence{{ID: "F1", Text: "price 10"}}, ""); err != nil {
		t.Fatal(err)
	}
}
