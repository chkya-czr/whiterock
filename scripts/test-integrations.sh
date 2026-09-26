#!/usr/bin/env bash
# Usage: scripts/test-integrations.sh [path/to/.env] [--send-email]
set -euo pipefail
ENV_FILE="${1:-.env}"
if [[ $# -gt 0 ]]; then shift; fi
exec go run ./cmd/integration-check --env="$ENV_FILE" "$@"
