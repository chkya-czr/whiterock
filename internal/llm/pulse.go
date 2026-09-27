package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PulseFact is a fully deterministic input for the shared unflagged-name
// summary. Flagged names are intentionally never included here.
type PulseFact struct {
	Ticker, Status                              string
	Price, WeekChangePct, MonthChangePct, RSI14 float64
}

type PulseBrief struct {
	PulseSummary string `json:"pulse_summary"`
}

func PulsePrompt(facts []PulseFact) (system, user string, allowed []float64) {
	system = "You are writing a brief, plain-language pulse update on a personal stock watchlist for weekly review. Use ONLY the facts given. No investment advice, recommendations, or predictions. Write three to five sentences on the general shape of movement across the list; group and summarize rather than restating every ticker. Flagged names are covered elsewhere; do not repeat them. If you use any number, copy it exactly as printed. To avoid accidental numeric reformatting, prefer describing direction and relative movement without digits. Return JSON only: {\"pulse_summary\": \"<3-5 sentences>\"}."
	var rows []string
	for _, f := range facts {
		rows = append(rows, fmt.Sprintf("%s: price %.2f, week change %.2f%%, month change %.2f%%, RSI %.2f", f.Ticker, f.Price, f.WeekChangePct, f.MonthChangePct, f.RSI14))
		allowed = append(allowed, f.Price, f.WeekChangePct, f.MonthChangePct, f.RSI14)
	}
	user = "This week's facts for unflagged watchlist names:\n" + strings.Join(rows, "\n")
	return system, user, allowed
}

func ParsePulse(raw string, allowed []float64) (PulseBrief, error) {
	var v PulseBrief
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, fmt.Errorf("invalid pulse JSON: %w", err)
	}
	if strings.TrimSpace(v.PulseSummary) == "" {
		return v, fmt.Errorf("pulse JSON has missing pulse_summary")
	}
	if err := ValidateNumbers(v.PulseSummary, allowed); err != nil {
		return v, err
	}
	return v, nil
}
