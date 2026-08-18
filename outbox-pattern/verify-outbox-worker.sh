#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../compose.yaml"
BASE_URL="${BASE_URL:-http://localhost:8080}"
SINK_PATH=$(mktemp -t outbox-pattern-worker.XXXXXX)
source_id=""
destination_id=""

cleanup() {
  if [[ -n "$source_id" ]]; then
    query "DELETE FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id' AND payload->>'destination_wallet_id' = '$destination_id'" >/dev/null || true
    query "DELETE FROM wallets WHERE id IN ($source_id, $destination_id)" >/dev/null || true
  fi
  rm -f "$SINK_PATH"
}
trap cleanup EXIT

query() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc "$1"
}

curl --silent --fail "$BASE_URL/" >/dev/null
source_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-worker-source', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-worker-destination', 1000.00) RETURNING id) SELECT id FROM inserted")

status=$(curl --silent --show-error --output /tmp/outbox-worker-transfer.json --write-out '%{http_code}' \
  --request POST "$BASE_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$source_id,\"destination_wallet_id\":$destination_id,\"amount\":10}")
if [[ "$status" != "200" ]]; then
  printf 'producer request returned HTTP %s\n' "$status" >&2
  exit 1
fi

event_id=$(query "SELECT id FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id' AND payload->>'destination_wallet_id' = '$destination_id'")
pending_status=$(query "SELECT status FROM outbox_events WHERE id = $event_id")
printf 'Before worker: event_id=%s status=%s\n' "$event_id" "$pending_status"
if [[ "$pending_status" != "pending" ]]; then
  printf 'event was not pending before worker run\n' >&2
  exit 1
fi

(
  cd "$SCRIPT_DIR"
  go run ./cmd/worker --once --batch-size 10 --lease 5s --sink-path "$SINK_PATH"
)

published_state=$(query "SELECT status || '|' || attempts::text || '|' || (published_at IS NOT NULL)::text FROM outbox_events WHERE id = $event_id")
sink_lines=$(wc -l < "$SINK_PATH" | tr -d ' ')
sink_body=$(<"$SINK_PATH")
printf 'After worker: state=%s sink_lines=%s\n' "$published_state" "$sink_lines"
if [[ "$published_state" != "published|1|true" || "$sink_lines" != "1" || "$sink_body" != *"\"event_id\":\"$event_id\""* ]]; then
  printf 'worker evidence did not match expected publication state\n' >&2
  exit 1
fi

printf 'PASSED: worker claimed, emitted, and published one outbox event\n'
