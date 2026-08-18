#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
COMPOSE_FILE="$SCRIPT_DIR/../compose.yaml"
BASE_URL="${BASE_URL:-http://localhost:8080}"
SINK_PATH=$(mktemp -t outbox-pattern-delivery.XXXXXX)
DEDUPE_PATH=$(mktemp -t outbox-pattern-delivery-applied.XXXXXX)
source_id=""
destination_id=""
source_id_two=""
destination_id_two=""
source_id_three=""
destination_id_three=""
event_id=""
event_id_two=""
event_id_three=""

cleanup() {
  for event in "$event_id" "$event_id_two" "$event_id_three"; do
    if [[ -n "$event" ]]; then
      query "DELETE FROM outbox_events WHERE id = $event" >/dev/null || true
    fi
  done
  if [[ -n "$source_id" ]]; then
    query "DELETE FROM outbox_events WHERE payload->>'source_wallet_id' IN ('$source_id', '$source_id_two', '$source_id_three') OR payload->>'destination_wallet_id' IN ('$destination_id', '$destination_id_two', '$destination_id_three')" >/dev/null || true
    for wallet in "$source_id" "$destination_id" "$source_id_two" "$destination_id_two" "$source_id_three" "$destination_id_three"; do
      if [[ -n "$wallet" ]]; then
        query "DELETE FROM wallets WHERE id = $wallet" >/dev/null || true
      fi
    done
  fi
  rm -f "$SINK_PATH" "$DEDUPE_PATH" "$DEDUPE_PATH.lock"
}
trap cleanup EXIT

query() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc "$1"
}

wait_for_expired_lease() {
  for _ in {1..50}; do
    lease_expired=$(query "SELECT (status = 'publishing' AND lease_until <= clock_timestamp())::text FROM outbox_events WHERE id = $event_id")
    if [[ "$lease_expired" == "true" ]]; then
      return 0
    fi
    sleep 0.1
  done
  printf 'event lease did not expire\n' >&2
  return 1
}

curl --silent --fail "$BASE_URL/" >/dev/null
source_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-delivery-source', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-delivery-destination', 1000.00) RETURNING id) SELECT id FROM inserted")
source_id_two=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-at-most-once-source', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id_two=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-at-most-once-destination', 1000.00) RETURNING id) SELECT id FROM inserted")
source_id_three=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-sink-failure-source', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id_three=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('outbox-sink-failure-destination', 1000.00) RETURNING id) SELECT id FROM inserted")

status=$(curl --silent --show-error --output /tmp/outbox-delivery-transfer.json --write-out '%{http_code}' \
  --request POST "$BASE_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$source_id,\"destination_wallet_id\":$destination_id,\"amount\":10}")
if [[ "$status" != "200" ]]; then
  printf 'producer request returned HTTP %s\n' "$status" >&2
  exit 1
fi
event_id=$(query "SELECT id FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id' AND payload->>'destination_wallet_id' = '$destination_id'")

printf 'Post-emit crash: event_id=%s\n' "$event_id"
if (
  cd "$SCRIPT_DIR"
  go run ./cmd/worker --once --lease 1s --crash-point after-emit --sink-path "$SINK_PATH" --dedupe-path "$DEDUPE_PATH"
); then
  printf 'expected worker to fail at after-emit crash point\n' >&2
  exit 1
fi

first_state=$(query "SELECT status || '|' || attempts::text FROM outbox_events WHERE id = $event_id")
first_lines=$(wc -l < "$SINK_PATH" | tr -d ' ')
printf 'After crash: state=%s sink_lines=%s\n' "$first_state" "$first_lines"
if [[ "$first_state" != "publishing|1" || "$first_lines" != "1" ]]; then
  printf 'post-emit crash evidence did not match expected state\n' >&2
  exit 1
fi

wait_for_expired_lease
(
  cd "$SCRIPT_DIR"
  go run ./cmd/worker --once --lease 1s --sink-path "$SINK_PATH" --dedupe-path "$DEDUPE_PATH"
)

final_state=$(query "SELECT status || '|' || attempts::text || '|' || (published_at IS NOT NULL)::text || '|' || coalesce(last_error, '') FROM outbox_events WHERE id = $event_id")
sink_lines=$(wc -l < "$SINK_PATH" | tr -d ' ')
applied_lines=$(wc -l < "$DEDUPE_PATH" | tr -d ' ')
sink_body=$(<"$SINK_PATH")
event_occurrences=0
while IFS= read -r line; do
  if [[ "$line" == *"\"event_id\":\"$event_id\""* ]]; then
    event_occurrences=$((event_occurrences + 1))
  fi
done < "$SINK_PATH"
printf 'After lease retry: state=%s sink_lines=%s matching_event_ids=%s applied_effects=%s\n' "$final_state" "$sink_lines" "$event_occurrences" "$applied_lines"
if [[ "$final_state" != "published|2|true|" || "$sink_lines" != "2" || "$event_occurrences" != "2" || "$applied_lines" != "1" ]]; then
  printf 'at-least-once evidence did not match expected duplicate delivery\n' >&2
  exit 1
fi

status=$(curl --silent --show-error --output /tmp/outbox-sink-failure-transfer.json --write-out '%{http_code}' \
  --request POST "$BASE_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$source_id_three,\"destination_wallet_id\":$destination_id_three,\"amount\":10}")
if [[ "$status" != "200" ]]; then
  printf 'sink failure producer request returned HTTP %s\n' "$status" >&2
  exit 1
fi
event_id_three=$(query "SELECT id FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id_three' AND payload->>'destination_wallet_id' = '$destination_id_three'")
(
  cd "$SCRIPT_DIR"
  go run ./cmd/worker --once --lease 1s --sink-fail-mode before-write --sink-path "$SINK_PATH" --dedupe-path "$DEDUPE_PATH"
)
sink_failure_state=$(query "SELECT status || '|' || attempts::text || '|' || coalesce(last_error, '') FROM outbox_events WHERE id = $event_id_three")
sink_failure_lines=$(wc -l < "$SINK_PATH" | tr -d ' ')
printf 'Sink failure result: state=%s sink_lines=%s\n' "$sink_failure_state" "$sink_failure_lines"
if [[ "$sink_failure_state" != "pending|1|injected sink failure before write" || "$sink_failure_lines" != "2" ]]; then
  printf 'sink failure evidence did not match expected retry state\n' >&2
  exit 1
fi

status=$(curl --silent --show-error --output /tmp/outbox-at-most-once-transfer.json --write-out '%{http_code}' \
  --request POST "$BASE_URL/transfer/outbox" \
  --header 'Content-Type: application/json' \
  --data "{\"source_wallet_id\":$source_id_two,\"destination_wallet_id\":$destination_id_two,\"amount\":10}")
if [[ "$status" != "200" ]]; then
  printf 'at-most-once comparison producer request returned HTTP %s\n' "$status" >&2
  exit 1
fi
event_id_two=$(query "SELECT id FROM outbox_events WHERE payload->>'source_wallet_id' = '$source_id_two' AND payload->>'destination_wallet_id' = '$destination_id_two'")
query "UPDATE outbox_events SET status = 'published', published_at = clock_timestamp(), lease_until = NULL WHERE id = $event_id_two" >/dev/null
sink_body=$(<"$SINK_PATH")
if [[ "$sink_body" == *"\"event_id\":\"$event_id_two\""* ]]; then
  printf 'at-most-once comparison unexpectedly emitted event %s\n' "$event_id_two" >&2
  exit 1
fi
printf 'At-most-once comparison: event_id=%s marked published before emit; sink_lines=%s\n' "$event_id_two" "$sink_lines"

printf 'PASSED: mark-after-emit gives at-least-once publication with duplicate delivery; mark-before-emit can lose events\n'
