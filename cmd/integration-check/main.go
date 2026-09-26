// Command integration-check performs small live checks against each configured
// dependency. It does not write Firestore; SMTP delivery is opt-in.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aayushgogia/stock-research-tool/internal/config"
	"github.com/aayushgogia/stock-research-tool/internal/delivery"
	"github.com/aayushgogia/stock-research-tool/internal/gcp"
	"github.com/aayushgogia/stock-research-tool/internal/history"
	"github.com/aayushgogia/stock-research-tool/internal/judgment"
	"github.com/aayushgogia/stock-research-tool/internal/llm"
	"github.com/aayushgogia/stock-research-tool/internal/localenv"
	"github.com/aayushgogia/stock-research-tool/internal/macro"
	"github.com/aayushgogia/stock-research-tool/internal/marketdata"
)

func main() {
	envFile := flag.String("env", ".env", "local dotenv file (ignored by Git)")
	watchlist := flag.String("watchlist", "watchlist.yaml", "watchlist YAML to validate")
	sendEmail := flag.Bool("send-email", false, "send one real SMTP test email")
	flag.Parse()
	if err := localenv.Load(*envFile); err != nil {
		fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var failures []error
	check := func(name string, fn func() error) {
		if err := fn(); err != nil {
			fmt.Printf("FAIL  %s: %v\n", name, err)
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
			return
		}
		fmt.Printf("PASS  %s\n", name)
	}

	check("watchlist config", func() error { _, err := config.Load(*watchlist); return err })
	project := required("GCP_PROJECT")
	token := gcp.StaticTokenSource{AccessToken: required("GOOGLE_ACCESS_TOKEN")}
	check("Twelve Data", func() error {
		candles, err := marketdata.NewTwelveData(required("TWELVE_DATA_API_KEY")).Daily(ctx, valueOr("INTEGRATION_TEST_TICKER", "SPY"), 5)
		if err == nil && len(candles) == 0 {
			return fmt.Errorf("empty price series")
		}
		return err
	})
	check("FRED", func() error {
		observations, err := macro.NewFred(required("FRED_API_KEY")).Series(ctx, "DFF", 2)
		if err == nil && len(observations) == 0 {
			return fmt.Errorf("empty DFF series")
		}
		return err
	})
	check("DeepInfra", func() error {
		raw, err := llm.NewDeepInfra(required("DEEPINFRA_API_KEY"), "").Complete(ctx, "Return JSON only.", "Return exactly {\"status\":\"ok\"}.")
		if err == nil && !strings.Contains(raw, "status") {
			return fmt.Errorf("response did not contain status")
		}
		return err
	})

	sm := gcp.SecretManager{Project: project, Tokens: token}
	check("Secret Manager", func() error { _, err := sm.Access(ctx, required("SECRET_MANAGER_TEST_SECRET")); return err })
	store := history.Firestore{Project: project, Tokens: token}
	check("Firestore read", func() error { _, err := store.Previous(ctx, "__INTEGRATION_CHECK__", "9999-12-31"); return err })
	check("Google Sheets read", func() error {
		_, err := (judgment.Sheets{ID: required("SHEET_ID"), Range: valueOr("SHEET_RANGE", "Sheet1!A:F"), Tokens: token}).LastWeek(ctx, time.Now().UTC().Format("2006-01-02"))
		return err
	})
	if *sendEmail {
		check("SMTP delivery", func() error {
			u, p := smtpCredential(required("SMTP_CREDENTIAL"))
			return (delivery.SMTP{Host: required("SMTP_HOST"), From: required("MAIL_FROM"), To: required("MAIL_TO"), Username: u, Password: p}).Send("Whiterock integration check", "This is a deliberate SMTP integration-test email.")
		})
	} else {
		fmt.Println("SKIP  SMTP delivery (rerun with --send-email to send one test message)")
	}
	if len(failures) > 0 {
		fail(errors.Join(failures...))
	}
}
func required(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return ""
}
func valueOr(name, fallback string) string {
	if v := required(name); v != "" {
		return v
	}
	return fallback
}
func smtpCredential(v string) (string, string) {
	parts := strings.SplitN(v, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", v
}
func fail(err error) { fmt.Fprintln(os.Stderr, "integration check failed:", err); os.Exit(1) }
