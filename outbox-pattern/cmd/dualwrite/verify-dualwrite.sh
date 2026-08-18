#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=$(cd -- "$SCRIPT_DIR/../.." && pwd)
COMPOSE_FILE="$PROJECT_DIR/../compose.yaml"
SINK_PATH=$(mktemp -t outbox-pattern-dualwrite.XXXXXX)
source_id=""
destination_id=""
source_id_two=""
destination_id_two=""

cleanup() {
  if [[ -n "$source_id" ]]; then
    query "DELETE FROM wallets WHERE id IN ($source_id, $destination_id, $source_id_two, $destination_id_two)" >/dev/null || true
  fi
  rm -f "$SINK_PATH"
}
trap cleanup EXIT

query() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U postgres -d researchs -Atc "$1"
}

source_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('dual-write-source-a', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('dual-write-destination-a', 1000.00) RETURNING id) SELECT id FROM inserted")
source_id_two=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('dual-write-source-b', 1000.00) RETURNING id) SELECT id FROM inserted")
destination_id_two=$(query "WITH inserted AS (INSERT INTO wallets (wallet_name, balance) VALUES ('dual-write-destination-b', 1000.00) RETURNING id) SELECT id FROM inserted")

printf 'Prediction 1: database-first failure leaves changed balances and no sink event.\n'
if (
  cd "$PROJECT_DIR"
  go run ./cmd/dualwrite \
    --order database-first \
    --fail-point after-database \
    --source-wallet-id "$source_id" \
    --destination-wallet-id "$destination_id" \
    --amount 10 \
    --sink-path "$SINK_PATH"
); then
  printf 'expected database-first command to fail\n' >&2
  exit 1
fi

database_first_state=$(query "SELECT (SELECT balance::text FROM wallets WHERE id = $source_id) || '|' || (SELECT balance::text FROM wallets WHERE id = $destination_id)")
database_first_events=$(wc -l < "$SINK_PATH" | tr -d ' ')
printf 'Database-first result: balances=%s sink_lines=%s\n' "$database_first_state" "$database_first_events"
if [[ "$database_first_state" != "990.00|1010.00" || "$database_first_events" != "0" ]]; then
  printf 'database-first evidence did not match expected contradiction\n' >&2
  exit 1
fi

printf 'Prediction 2: sink-first failure leaves one sink event and unchanged balances.\n'
if (
  cd "$PROJECT_DIR"
  go run ./cmd/dualwrite \
    --order sink-first \
    --fail-point after-sink \
    --source-wallet-id "$source_id_two" \
    --destination-wallet-id "$destination_id_two" \
    --amount 10 \
    --sink-path "$SINK_PATH"
); then
  printf 'expected sink-first command to fail\n' >&2
  exit 1
fi

sink_first_state=$(query "SELECT (SELECT balance::text FROM wallets WHERE id = $source_id_two) || '|' || (SELECT balance::text FROM wallets WHERE id = $destination_id_two)")
sink_first_events=$(wc -l < "$SINK_PATH" | tr -d ' ')
printf 'Sink-first result: balances=%s sink_lines=%s\n' "$sink_first_state" "$sink_first_events"
if [[ "$sink_first_state" != "1000.00|1000.00" || "$sink_first_events" != "1" ]]; then
  printf 'sink-first evidence did not match expected contradiction\n' >&2
  exit 1
fi

printf 'PASSED: dual-write ordering leaves an inconsistent boundary in both directions\n'
