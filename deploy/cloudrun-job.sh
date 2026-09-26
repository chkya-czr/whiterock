#!/usr/bin/env bash
# Fill in non-secret values before deployment. API keys remain in Secret Manager.
set -euo pipefail
: "${PROJECT_ID:?set PROJECT_ID}"
: "${REGION:?set REGION}"
: "${IMAGE:?set IMAGE}"
: "${SERVICE_ACCOUNT:?set SERVICE_ACCOUNT}"
: "${MAIL_FROM:?set MAIL_FROM}"
: "${MAIL_TO:?set MAIL_TO}"
: "${SMTP_HOST:?set SMTP_HOST}"
gcloud run jobs deploy stock-watchlist-weekly \
  --project="$PROJECT_ID" --region="$REGION" --image="$IMAGE" \
  --service-account="$SERVICE_ACCOUNT" --max-retries=1 --task-timeout=45m \
  --args="--gcp-project=$PROJECT_ID,--twelve-data-secret=twelve-data-api-key,--fred-secret=fred-api-key,--deepinfra-secret=deepinfra-api-key,--smtp-credential-secret=smtp-credential,--smtp-host=$SMTP_HOST,--mail-from=$MAIL_FROM,--mail-to=$MAIL_TO"
# Cloud Scheduler should POST weekly to this Job's :run URL using an OIDC
# service account with run.jobs.run permission.
