#!/usr/bin/env bash

set -euo pipefail

MODE="${1:-optimistic}"
REQUEST_COUNT="${2:-10}"
AMOUNT="${3:-10}"
WALLET_ID="${4:-1}"
BASE_URL="${BASE_URL:-http://localhost:8080}"

case "$MODE" in
  normal | pessimistic | optimistic)
    ;;
  *)
    printf 'Usage: %s [normal|pessimistic|optimistic] [request_count] [amount] [wallet_id]\n' "$0" >&2
    exit 2
    ;;
esac

if ! [[ "$REQUEST_COUNT" =~ ^[1-9][0-9]*$ ]]; then
  printf 'request_count must be a positive integer\n' >&2
  exit 2
fi

if ! [[ "$WALLET_ID" =~ ^[1-9][0-9]*$ ]]; then
  printf 'wallet_id must be a positive integer\n' >&2
  exit 2
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../compose.yaml"
RESULT_DIR=$(mktemp -d)
trap 'rm -rf "$RESULT_DIR"' EXIT

query_wallet() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc \
    "SELECT balance::text || '|' || version FROM wallets WHERE id = $WALLET_ID"
}

before=$(query_wallet)
if [[ -z "$before" ]]; then
  printf 'Wallet %s not found\n' "$WALLET_ID" >&2
  exit 1
fi

before_balance=${before%%|*}
before_version=${before##*|}
if [[ "$MODE" == "normal" ]]; then
  endpoint="$BASE_URL/transfer"
else
  endpoint="$BASE_URL/transfer/$MODE"
fi

printf 'Sending %s concurrent requests to %s\n' "$REQUEST_COUNT" "$endpoint"
printf 'Before: balance=%s version=%s\n' "$before_balance" "$before_version"

pids=()
for ((i = 1; i <= REQUEST_COUNT; i++)); do
  curl --silent --show-error \
    --output "$RESULT_DIR/body-$i.json" \
    --write-out '%{http_code}' \
    --request POST "$endpoint" \
    --header 'Content-Type: application/json' \
    --data "{\"wallet_id\":$WALLET_ID,\"amount\":$AMOUNT}" \
    >"$RESULT_DIR/status-$i" &
  pids+=("$!")
done

curl_failures=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then
    curl_failures=$((curl_failures + 1))
  fi
done

http_failures=0
for ((i = 1; i <= REQUEST_COUNT; i++)); do
  status=$(<"$RESULT_DIR/status-$i")
  body=$(<"$RESULT_DIR/body-$i.json")
  printf 'Request %02d: HTTP %s %s\n' "$i" "$status" "$body"
  if [[ "$status" != "200" ]]; then
    http_failures=$((http_failures + 1))
  fi
done

after=$(query_wallet)
after_balance=${after%%|*}
after_version=${after##*|}
expected=$(docker compose -f "$COMPOSE_FILE" exec -T postgres \
  psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc \
  "SELECT ($before_balance::numeric - ($AMOUNT::numeric * $REQUEST_COUNT))::numeric(20,2) || '|' || ($before_version + $REQUEST_COUNT)")

printf 'After:  balance=%s version=%s\n' "$after_balance" "$after_version"
printf 'Expect: %s\n' "$expected"

if ((curl_failures > 0 || http_failures > 0)); then
  printf 'FAILED: curl_failures=%s http_failures=%s\n' "$curl_failures" "$http_failures" >&2
  exit 1
fi

if [[ "$after" != "$expected" ]]; then
  printf 'FAILED: database state does not match completed transfers\n' >&2
  exit 1
fi

printf 'PASSED: all concurrent transfers applied exactly once\n'
