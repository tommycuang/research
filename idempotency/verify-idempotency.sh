#!/usr/bin/env bash
set -euo pipefail

# EXPERIMENT ONLY: diagnostic output intentionally prints Idempotency-Key values
# in request headers. Do not use this verifier with sensitive production keys.

if [[ $# -ne 4 ]]; then
  printf 'usage: %s REQUEST_COUNT AMOUNT SOURCE_ID DESTINATION_ID\n' "$0" >&2
  exit 2
fi

REQUEST_COUNT=$1
AMOUNT=$2
SOURCE_ID=$3
DESTINATION_ID=$4
BASE_URL=${BASE_URL:-http://localhost:8080}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
COMPOSE=(docker compose -f "$ROOT/compose.yaml")
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

if ! [[ $REQUEST_COUNT =~ ^[1-9][0-9]*$ ]]; then
  printf 'REQUEST_COUNT must be a positive integer\n' >&2
  exit 2
fi
if ! [[ $AMOUNT =~ ^[0-9]+(\.[0-9]{1,2})?$ ]]; then
  printf 'AMOUNT must be a decimal with at most two fractional digits\n' >&2
  exit 2
fi
if ! [[ $SOURCE_ID =~ ^[1-9][0-9]*$ && $DESTINATION_ID =~ ^[1-9][0-9]*$ ]]; then
  printf 'wallet IDs must be positive integers\n' >&2
  exit 2
fi
if [[ $SOURCE_ID == "$DESTINATION_ID" ]]; then
  printf 'source and destination wallet IDs must differ\n' >&2
  exit 2
fi

sql_snapshot() {
  local output_file=$1
  local snapshot
  snapshot=$(
    "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
      -At -F '|' \
      -c "
        SELECT source_wallet.balance::text,
               source_wallet.version::text,
               destination_wallet.balance::text,
               destination_wallet.version::text
        FROM wallets AS source_wallet
        CROSS JOIN wallets AS destination_wallet
        WHERE source_wallet.id = $SOURCE_ID
          AND destination_wallet.id = $DESTINATION_ID
      "
  )
  if [[ -z $snapshot || $snapshot != *'|'* ]]; then
    printf 'could not read seeded wallet snapshot\n' >&2
    return 1
  fi
  printf '%s\n' "$snapshot" > "$output_file"
}

numeric_calculation() {
  local left=$1
  local right=$2
  local operation=$3
  local expression
  case $operation in
    add) expression="$left::numeric + $right::numeric" ;;
    subtract) expression="$left::numeric - $right::numeric" ;;
    *) printf 'unsupported numeric operation\n' >&2; return 1 ;;
  esac
  "${COMPOSE[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs -At \
    -c "SELECT (($expression)::numeric(20,2))::text"
}

request() {
  local body_file=$1
  local headers_file=$2
  local key=$3
  local amount=$4
  local request_body_file=$5
  local request_headers_file=$6
  local payload
  payload=$(printf '{"source_wallet_id":%s,"destination_wallet_id":%s,"amount":%s}' \
    "$SOURCE_ID" "$DESTINATION_ID" "$amount")
  printf '%s\n' "$payload" > "$request_body_file"
  {
    printf 'Content-Type: application/json\n'
    printf 'Idempotency-Key: %s\n' "$key"
  } > "$request_headers_file"
  curl -sS -o "$body_file" -D "$headers_file" -w '%{http_code}' \
    -X POST "$BASE_URL/transfer" \
    -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $key" \
    --data "$payload"
}

print_file_lines() {
  local prefix=$1
  local file=$2
  while IFS= read -r line || [[ -n $line ]]; do
    printf '%s%s\n' "$prefix" "$line"
  done < "$file"
}

print_exchange() {
  local label=$1
  local request_headers_file=$2
  local request_body_file=$3
  local response_status=$4
  local response_headers_file=$5
  local response_body_file=$6

  printf '\n--- %s ---\n' "$label"
  printf 'request headers (configured):\n'
  print_file_lines '  ' "$request_headers_file"
  printf 'request body:\n'
  jq . "$request_body_file"
  printf 'response status: %s\n' "$response_status"
  printf 'response headers:\n'
  print_file_lines '  ' "$response_headers_file"
  printf 'response body:\n'
  jq . "$response_body_file"
}

assert_status() {
  local actual=$1
  local expected=$2
  local label=$3
  if [[ $actual != "$expected" ]]; then
    printf '%s: expected HTTP %s, got %s\n' "$label" "$expected" "$actual" >&2
    return 1
  fi
}

assert_same_json() {
  local first=$1
  local second=$2
  local label=$3
  if ! jq -e -s '.[0] == .[1]' "$first" "$second" >/dev/null; then
    printf '%s: response bodies differ\n' "$label" >&2
    return 1
  fi
}

assert_snapshot_same() {
  local first=$1
  local second=$2
  local label=$3
  if ! cmp -s "$first" "$second"; then
    printf '%s: wallet balances or versions changed unexpectedly\n' "$label" >&2
    return 1
  fi
}

assert_replay_header() {
  local headers_file=$1
  local label=$2
  if ! awk 'tolower($0) == "idempotency-replayed: true\r" || tolower($0) == "idempotency-replayed: true" { found = 1 } END { exit !found }' "$headers_file"; then
    printf '%s: missing Idempotency-Replayed: true header\n' "$label" >&2
    return 1
  fi
}

assert_no_replay_header() {
  local headers_file=$1
  local label=$2
  if awk 'tolower($0) == "idempotency-replayed: true\r" || tolower($0) == "idempotency-replayed: true" { found = 1 } END { exit (found ? 0 : 1) }' "$headers_file"; then
    printf '%s: unexpected Idempotency-Replayed header\n' "$label" >&2
    return 1
  fi
}

assert_transfer_response() {
  local body_file=$1
  local expected_source_balance=$2
  local expected_destination_balance=$3
  local expected_amount
  expected_amount=$(numeric_calculation "$AMOUNT" 0 add)
  jq -e \
    --argjson source_id "$SOURCE_ID" \
    --argjson destination_id "$DESTINATION_ID" \
    --arg amount "$expected_amount" \
    --arg source_balance "$expected_source_balance" \
    --arg destination_balance "$expected_destination_balance" \
    '.["source_wallet_id"] == $source_id and
     .["destination_wallet_id"] == $destination_id and
     .amount == $amount and
     .source_balance == $source_balance and
     .destination_balance == $destination_balance and
     (.transferred_at | type == "string") and
     (.transferred_at | length > 0)' "$body_file" >/dev/null
}

snapshot_value() {
  local file=$1
  local field_index=$2
  awk -F '|' -v field_index="$field_index" '{ print $field_index }' "$file"
}

new_key() {
  printf 'verify-%s-%s-%s' "$(date +%s%N)" "$$" "$1"
}

equivalent_amount() {
  if [[ $AMOUNT != *.* ]]; then
    printf '%s.0\n' "$AMOUNT"
    return
  fi
  local integer_part=${AMOUNT%.*}
  local fraction_part=${AMOUNT#*.}
  if [[ ${#fraction_part} -eq 1 ]]; then
    printf '%s0\n' "$AMOUNT"
  elif [[ $fraction_part == 00 ]]; then
    printf '%s\n' "$integer_part"
  else
    printf '%s\n' "$AMOUNT"
  fi
}

printf 'checking missing-key validation\n'
sql_snapshot "$TMP_DIR/missing-before"
missing_body=$TMP_DIR/missing-body
missing_headers=$TMP_DIR/missing-headers
missing_request_body=$TMP_DIR/missing-request-body
missing_request_headers=$TMP_DIR/missing-request-headers
missing_payload=$(printf '{"source_wallet_id":%s,"destination_wallet_id":%s,"amount":%s}' \
  "$SOURCE_ID" "$DESTINATION_ID" "$AMOUNT")
printf '%s\n' "$missing_payload" > "$missing_request_body"
printf 'Content-Type: application/json\n' > "$missing_request_headers"
missing_status=$(curl -sS -o "$missing_body" -D "$missing_headers" -w '%{http_code}' \
  -X POST "$BASE_URL/transfer" \
  -H 'Content-Type: application/json' \
  --data "$missing_payload")
print_exchange 'missing key' "$missing_request_headers" "$missing_request_body" \
  "$missing_status" "$missing_headers" "$missing_body"
assert_status "$missing_status" 400 'missing key'
sql_snapshot "$TMP_DIR/missing-after"
assert_snapshot_same "$TMP_DIR/missing-before" "$TMP_DIR/missing-after" 'missing key'

printf 'checking first request and replay\n'
first_key=$(new_key first)
sql_snapshot "$TMP_DIR/first-before"
first_body=$TMP_DIR/first-body
first_headers=$TMP_DIR/first-headers
first_request_body=$TMP_DIR/first-request-body
first_request_headers=$TMP_DIR/first-request-headers
first_status=$(request "$first_body" "$first_headers" "$first_key" "$AMOUNT" \
  "$first_request_body" "$first_request_headers")
print_exchange 'first keyed request' "$first_request_headers" "$first_request_body" \
  "$first_status" "$first_headers" "$first_body"
assert_status "$first_status" 200 'first keyed request'
assert_no_replay_header "$first_headers" 'first keyed request'
sql_snapshot "$TMP_DIR/first-after"
first_source_before=$(snapshot_value "$TMP_DIR/first-before" 1)
first_destination_before=$(snapshot_value "$TMP_DIR/first-before" 3)
first_source_after=$(snapshot_value "$TMP_DIR/first-after" 1)
first_destination_after=$(snapshot_value "$TMP_DIR/first-after" 3)
first_expected_source=$(numeric_calculation "$first_source_before" "$AMOUNT" subtract)
first_expected_destination=$(numeric_calculation "$first_destination_before" "$AMOUNT" add)
if [[ $first_source_after != "$first_expected_source" || $first_destination_after != "$first_expected_destination" ]]; then
  printf 'first keyed request: expected one debit and one credit\n' >&2
  exit 1
fi
if [[ $(snapshot_value "$TMP_DIR/first-after" 2) -ne $(( $(snapshot_value "$TMP_DIR/first-before" 2) + 1 )) || \
      $(snapshot_value "$TMP_DIR/first-after" 4) -ne $(( $(snapshot_value "$TMP_DIR/first-before" 4) + 1 )) ]]; then
  printf 'first keyed request: expected one version increment per wallet\n' >&2
  exit 1
fi
assert_transfer_response "$first_body" "$first_expected_source" "$first_expected_destination"

replay_body=$TMP_DIR/replay-body
replay_headers=$TMP_DIR/replay-headers
replay_request_body=$TMP_DIR/replay-request-body
replay_request_headers=$TMP_DIR/replay-request-headers
replay_amount=$(equivalent_amount)
replay_status=$(request "$replay_body" "$replay_headers" "$first_key" "$replay_amount" \
  "$replay_request_body" "$replay_request_headers")
print_exchange 'equivalent replay' "$replay_request_headers" "$replay_request_body" \
  "$replay_status" "$replay_headers" "$replay_body"
assert_status "$replay_status" 200 'equivalent replay'
assert_replay_header "$replay_headers" 'equivalent replay'
assert_same_json "$first_body" "$replay_body" 'equivalent replay'
sql_snapshot "$TMP_DIR/replay-after"
assert_snapshot_same "$TMP_DIR/first-after" "$TMP_DIR/replay-after" 'equivalent replay'

printf 'checking fingerprint mismatch\n'
mismatch_body=$TMP_DIR/mismatch-body
mismatch_headers=$TMP_DIR/mismatch-headers
mismatch_request_body=$TMP_DIR/mismatch-request-body
mismatch_request_headers=$TMP_DIR/mismatch-request-headers
mismatch_amount=$(numeric_calculation "$AMOUNT" 1.00 add)
mismatch_status=$(request "$mismatch_body" "$mismatch_headers" "$first_key" "$mismatch_amount" \
  "$mismatch_request_body" "$mismatch_request_headers")
print_exchange 'fingerprint mismatch' "$mismatch_request_headers" "$mismatch_request_body" \
  "$mismatch_status" "$mismatch_headers" "$mismatch_body"
assert_status "$mismatch_status" 409 'changed payload'
assert_no_replay_header "$mismatch_headers" 'changed payload'
sql_snapshot "$TMP_DIR/mismatch-after"
assert_snapshot_same "$TMP_DIR/first-after" "$TMP_DIR/mismatch-after" 'changed payload'

printf 'checking %s concurrent requests with one key\n' "$REQUEST_COUNT"
concurrent_key=$(new_key concurrent)
sql_snapshot "$TMP_DIR/concurrent-before"
for request_index in $(seq 1 "$REQUEST_COUNT"); do
  request_body=$TMP_DIR/concurrent-body-$request_index
  request_headers=$TMP_DIR/concurrent-headers-$request_index
  request_request_body=$TMP_DIR/concurrent-request-body-$request_index
  request_request_headers=$TMP_DIR/concurrent-request-headers-$request_index
  request_status=$TMP_DIR/concurrent-status-$request_index
  (
    request "$request_body" "$request_headers" "$concurrent_key" "$AMOUNT" \
      "$request_request_body" "$request_request_headers" > "$request_status"
  ) &
done
wait

fresh_count=0
replay_count=0
concurrent_fresh_body=''
for request_index in $(seq 1 "$REQUEST_COUNT"); do
  request_body=$TMP_DIR/concurrent-body-$request_index
  request_headers=$TMP_DIR/concurrent-headers-$request_index
  request_request_body=$TMP_DIR/concurrent-request-body-$request_index
  request_request_headers=$TMP_DIR/concurrent-request-headers-$request_index
  request_status=$(<"$TMP_DIR/concurrent-status-$request_index")
  print_exchange "concurrent request $request_index" "$request_request_headers" \
    "$request_request_body" "$request_status" "$request_headers" "$request_body"
  assert_status "$request_status" 200 "concurrent request $request_index"
  if awk 'tolower($0) == "idempotency-replayed: true\r" || tolower($0) == "idempotency-replayed: true" { found = 1 } END { exit !found }' "$request_headers"; then
    replay_count=$((replay_count + 1))
  else
    fresh_count=$((fresh_count + 1))
    concurrent_fresh_body=$request_body
  fi
done
if [[ $fresh_count -ne 1 || $replay_count -ne $((REQUEST_COUNT - 1)) ]]; then
  printf 'concurrent batch: expected one fresh and %s replay responses, got %s fresh and %s replay\n' \
    "$((REQUEST_COUNT - 1))" "$fresh_count" "$replay_count" >&2
  exit 1
fi
for request_index in $(seq 1 "$REQUEST_COUNT"); do
  assert_same_json "$concurrent_fresh_body" "$TMP_DIR/concurrent-body-$request_index" \
    "concurrent request $request_index"
done
sql_snapshot "$TMP_DIR/concurrent-after"
concurrent_expected_source=$(numeric_calculation "$(snapshot_value "$TMP_DIR/concurrent-before" 1)" "$AMOUNT" subtract)
concurrent_expected_destination=$(numeric_calculation "$(snapshot_value "$TMP_DIR/concurrent-before" 3)" "$AMOUNT" add)
if [[ $(snapshot_value "$TMP_DIR/concurrent-after" 1) != "$concurrent_expected_source" || \
      $(snapshot_value "$TMP_DIR/concurrent-after" 3) != "$concurrent_expected_destination" ]]; then
  printf 'concurrent batch: expected exactly one debit and one credit\n' >&2
  exit 1
fi
assert_transfer_response "$concurrent_fresh_body" "$concurrent_expected_source" "$concurrent_expected_destination"
if [[ $(snapshot_value "$TMP_DIR/concurrent-after" 2) -ne $(( $(snapshot_value "$TMP_DIR/concurrent-before" 2) + 1 )) || \
      $(snapshot_value "$TMP_DIR/concurrent-after" 4) -ne $(( $(snapshot_value "$TMP_DIR/concurrent-before" 4) + 1 )) ]]; then
  printf 'concurrent batch: expected one version increment per wallet\n' >&2
  exit 1
fi

printf 'idempotency verification passed: one transfer per distinct key\n'
