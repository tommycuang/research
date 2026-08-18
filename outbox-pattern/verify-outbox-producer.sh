#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../compose.yaml"
BASE_URL="${BASE_URL:-http://localhost:8080}"
FAIL_PORT="${FAIL_PORT:-}"
FAIL_URL=""
FAIL_LOG=$(mktemp -t outbox-pattern-api-failure.XXXXXX)
FAIL_BIN=""
source_id=""
destination_id=""
failed_source_id=""
failed_destination_id=""
fail_pid=""

cleanup() {
  if [[ -n "$fail_pid" ]]; then
    kill "$fail_pid" >/dev/null 2>&1 || true
    wait "$fail_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "$source_id" ]]; then
    query "DELETE FROM outbox_events WHERE payload->>'source_wallet_id' IN ('$source_id', '$failed_source_id') OR payload->>'destination_wallet_id' IN ('$destination_id', '$failed_destination_id')" >/dev/null || true
    query "DELETE FROM wallets WHERE id IN ($source_id, $destination_id, $failed_source_id, $failed_destination_id)" >/dev/null || true
  fi
  rm -f "$FAIL_LOG"
  if [[ -n "$FAIL_BIN" ]]; then
    rm -f "$FAIL_BIN"
  fi
}
trap cleanup EXIT

query() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc "$1"
}

choose_fail_port() {
  if [[ -n "$FAIL_PORT" ]]; then
    if lsof -nP -iTCP:"$FAIL_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
      printf 'FAIL_PORT is already in use: %s\n' "$FAIL_PORT" >&2
      return 1
    fi
  else
    for candidate in {18081..18120}; do
      if ! lsof -nP -iTCP:"$candidate" -sTCP:LISTEN >/dev/null 2>&1; then
        FAIL_PORT=$candidate
        break
      fi
    done
  fi
  if [[ -z "$FAIL_PORT" ]]; then
    printf 'could not find a free failure-test port\n' >&2
    return 1
  fi
  FAIL_URL="http://localhost:$FAIL_PORT"
}

wait_for_api() {
  local url=$1
  for _ in {1..50}; do
    route_status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
      --request POST "$url/transfer/outbox" \
      --header 'Content-Type: application/json' \
      --data '{}' || true)
    if [[ "$route_status" == "400" ]]; then
      return 0
    fi
    sleep 0.2
  done
  printf 'API did not become ready: %s\n' "$url" >&2
  return 1
}

curl --silent --fail "$BASE_URL/" >/dev/null

source_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-producer-source', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-producer-destination', 1000.00) RETURNING id) SELECT id FROM inserted")
failed_source_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-producer-failed-source', 1000.00) RETURNING id) SELECT id FROM inserted")
failed_destination_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-producer-failed-destination', 1000.00) RETURNING id) SELECT id FROM inserted")

success_status=$(curl --silent --show-error --output /tmp/outbox-producer-success.json --write-out '%{http_code}' \
  --request POST "$BASE_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$source_id,\"destination_wallet_id\":$destination_id,\"amount\":10}")
if [[ "$success_status" != "200" ]]; then
  printf 'successful producer request returned HTTP %s\n' "$success_status" >&2
  exit 1
fi

success_balances=$(query "SELECT (SELECT balance::text FROM wallets WHERE id = $source_id) || '|' || (SELECT balance::text FROM wallets WHERE id = $destination_id)")
success_events=$(query "SELECT count(*) FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id' AND payload->>'destination_wallet_id' = '$destination_id'")
success_status_value=$(query "SELECT status FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id' AND payload->>'destination_wallet_id' = '$destination_id'")
printf 'Committed producer result: balances=%s outbox_events=%s status=%s\n' "$success_balances" "$success_events" "$success_status_value"
if [[ "$success_balances" != "990.00|1010.00" || "$success_events" != "1" || "$success_status_value" != "pending" ]]; then
  printf 'committed producer evidence did not match expected state\n' >&2
  exit 1
fi

choose_fail_port
FAIL_BIN=$(mktemp -t outbox-pattern-failure-api-bin.XXXXXX)
(
  cd "$SCRIPT_DIR"
  go build -o "$FAIL_BIN" .
)
PORT="$FAIL_PORT" OUTBOX_FAIL_POINT=before-commit PROCESSING_DELAY=0 "$FAIL_BIN" >"$FAIL_LOG" 2>&1 &
fail_pid=$!
wait_for_api "$FAIL_URL"

failed_status=$(curl --silent --show-error --output /tmp/outbox-producer-failed.json --write-out '%{http_code}' \
  --request POST "$FAIL_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$failed_source_id,\"destination_wallet_id\":$failed_destination_id,\"amount\":10}")
if [[ "$failed_status" != "500" ]]; then
  printf 'injected producer failure returned HTTP %s, want 500\n' "$failed_status" >&2
  exit 1
fi

failed_balances=$(query "SELECT (SELECT balance::text FROM wallets WHERE id = $failed_source_id) || '|' || (SELECT balance::text FROM wallets WHERE id = $failed_destination_id)")
failed_events=$(query "SELECT count(*) FROM outbox_events WHERE payload->>'source_wallet_id' = '$failed_source_id' AND payload->>'destination_wallet_id' = '$failed_destination_id'")
printf 'Rolled-back producer result: balances=%s outbox_events=%s\n' "$failed_balances" "$failed_events"
if [[ "$failed_balances" != "1000.00|1000.00" || "$failed_events" != "0" ]]; then
  printf 'rolled-back producer evidence did not match expected state\n' >&2
  exit 1
fi

printf 'PASSED: wallet transfer and outbox row commit or roll back together\n'
