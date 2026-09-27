# Personal Stock Watchlist Research Tool

Weekly, unattended factual research for a hand-edited watchlist. It is not a
trading system and it never makes a recommendation. Go code computes every
number and applies every trigger; the LLM only writes from supplied facts and
its output is rejected if it introduces a number that cannot be traced to them.

## Local preparation

1. Copy/edit `watchlist.yaml`; `trigger.type: none` is valid and appears as
   **Watch only** in the digest.
2. Run `go test ./...`.
3. Build with `go build ./cmd/weekly-run`.
4. Run inside Cloud Run Job with a service account granted Secret Manager
   Secret Accessor, Firestore user, and Sheets Viewer to the relevant sheet.
   The executable obtains an access token from the Cloud Run metadata service;
   credentials are not read from environment variables.

The job accepts non-secret deployment settings as flags. Its API keys and SMTP
password are read by name from Secret Manager. `--smtp-credential-secret` must
contain `username:password` (or a password alone). The project uses raw Google
REST APIs to remain a single static binary.

`deploy/cloudrun-job.sh` is a deployment template. Cloud Scheduler should POST
weekly to the Job's `:run` endpoint with an OAuth service account holding
`run.jobs.run`; deploy only after manual run validation.

## Temporary local `.env` mode

For temporary live testing only, copy `.env.example` to the ignored `.env`,
fill it with real values, and run `go run ./cmd/weekly-run --local-env=.env`.
`GOOGLE_ACCESS_TOKEN` must be a short-lived value from `gcloud auth
application-default print-access-token`; it lets the program access the same
Secret Manager-independent Firestore and Sheets APIs locally. Do not commit
`.env`, and remove this local mode before production deployment.

The default LLM is the pinned DeepInfra release
`deepseek-ai/DeepSeek-V4-Pro-0813`. Set `LLM_MODEL` in `.env` or pass
`--llm-model` only when intentionally overriding it.

To check every external dependency before a full run, use
`scripts/test-integrations.sh .env`. It validates config, Twelve Data, FRED,
DeepInfra, Secret Manager, Firestore, and Sheets. Its Firestore probe writes
then deletes one temporary document. Add `--send-email` only when you want it
to send one real SMTP test email.

`CSU` is intentionally not included as a bare symbol: Twelve Data's exchange
suffix for Toronto listings must be verified against the account/API before
adding it to the hand-edited YAML.
