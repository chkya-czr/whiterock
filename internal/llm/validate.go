package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

type TickerBrief struct {
	CaseFor             string `json:"case_for"`
	CaseAgainst         string `json:"case_against"`
	ChangeSinceLastWeek string `json:"change_since_last_week"`
	Risk                string `json:"risk"`
}
type MacroBrief struct {
	RegimeTag     string `json:"regime_tag"`
	Summary       string `json:"summary"`
	NotableShifts string `json:"notable_shifts"`
}

var number = regexp.MustCompile(`[-+]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?`)

// ValidateNumbers rejects any numeric token in model-controlled fields that is
// not one of the explicitly supplied facts (using numeric equality to permit
// harmless `$`/`%` decoration and comma formatting).
func ValidateNumbers(text string, allowed []float64) error {
	for _, token := range number.FindAllString(text, -1) {
		n, e := strconv.ParseFloat(removeCommas(token), 64)
		if e != nil {
			return fmt.Errorf("parse output number %q: %w", token, e)
		}
		found := false
		for _, a := range allowed {
			scale := max(1, abs(a))
			if abs(n-a) <= scale*1e-8 {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("untraceable model number %q", token)
		}
	}
	return nil
}
func ParseTicker(raw string, allowed []float64) (TickerBrief, error) {
	var v TickerBrief
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		return v, fmt.Errorf("invalid ticker JSON: %w", e)
	}
	if v.CaseFor == "" || v.CaseAgainst == "" || v.ChangeSinceLastWeek == "" || v.Risk == "" {
		return v, fmt.Errorf("ticker JSON has missing required text")
	}
	if e := ValidateNumbers(v.CaseFor+" "+v.CaseAgainst+" "+v.Risk, allowed); e != nil {
		return v, e
	}
	return v, nil
}
func ParseMacro(raw string, allowed []float64) (MacroBrief, error) {
	var v MacroBrief
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		return v, fmt.Errorf("invalid macro JSON: %w", e)
	}
	if v.RegimeTag == "" || v.Summary == "" || v.NotableShifts == "" {
		return v, fmt.Errorf("macro JSON has missing required text")
	}
	if e := ValidateNumbers(v.RegimeTag+" "+v.Summary+" "+v.NotableShifts, allowed); e != nil {
		return v, e
	}
	return v, nil
}
func removeCommas(s string) string {
	out := make([]byte, 0, len(s))
	for i := range s {
		if s[i] != ',' {
			out = append(out, s[i])
		}
	}
	return string(out)
}
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
