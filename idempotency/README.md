# Idempotent Wallet Transfers

Gin HTTP API demonstrating a PostgreSQL-backed wallet transfer that executes
once per idempotency key and replays the stored response for later equivalent
requests.

## Start

From the workspace root, start PostgreSQL and SQLPad:

```bash
docker compose up -d
```

Apply the migrations in order. Migration 001 creates the `researchs` database;
the remaining migrations run against that database:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres \
  < migrations/001_create_researchs.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/002_create_wallets.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/003_add_wallet_version.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/004_create_idempotency_records.sql
```

Seed the sample wallets:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < seeds/001_seed_wallets.sql
```

The seed creates five wallets, each with an initial balance of `1000000.00`.
The verifier changes these balances, so run the seed only when starting with a
fresh database or when resetting the experiment intentionally.

From this folder, start the API:

```bash
go run .
```

The API listens at <http://localhost:8080>. Check that it is running:

```bash
curl http://localhost:8080/
```

The application does not run migrations at startup. Apply migrations before
starting it.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable` | PostgreSQL connection |
| `PORT` | `8080` | HTTP server port |
| `PROCESSING_DELAY` | `500ms` | Simulated processing duration while wallet locks are held |

`PROCESSING_DELAY` accepts Go duration syntax. Set it to `0s` to disable the
simulated delay. An invalid value uses the default `500ms`.

Example:

```bash
DATABASE_URL='postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable' \
PORT=8080 \
PROCESSING_DELAY=2s \
go run .
```

## Transfer Request

Only `POST /transfer` performs a transfer. Requests must use
`Content-Type: application/json` and contain one JSON object with distinct,
positive wallet IDs and a positive JSON number amount with at most two decimal
places:

```bash
curl -X POST http://localhost:8080/transfer \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: transfer-demo-001' \
  -d '{"source_wallet_id":1,"destination_wallet_id":2,"amount":10.00}'
```

The request rejects unknown or duplicate fields, trailing JSON, numeric
strings, exponent notation, zero or negative amounts, and amounts outside
`NUMERIC(20,2)`. Equivalent amounts such as `10`, `10.0`, and `10.00`
normalize to `10.00` and have the same request fingerprint.

## Idempotency

Every valid transfer request requires exactly one `Idempotency-Key` header. The
key must contain 1-255 visible ASCII bytes (`!` through `~`). Whitespace,
control bytes, Unicode, and repeated header values are rejected. Keys are
case-sensitive and scoped to the stable `transfer` operation.

The first valid request for a key executes in the same PostgreSQL transaction
that reserves the key, updates both wallets, and stores the response before
commit. A later request with the same key and an equivalent payload returns the
stored status and body without changing wallet state. Such a response includes:

```text
Idempotency-Replayed: true
```

A first response does not include that header. Reusing a key with a different
source wallet, destination wallet, or canonical amount returns `409` with
`idempotency key already used with a different request`; the original record
and wallet state are unchanged. The same payload with a different key is a new
transfer.

Validation failures return `400` and are not stored. Infrastructure failures
return `500` and roll back the reservation and wallet changes, so the key can
be retried.

Completed business outcomes are permanent for their key. A missing source or
destination wallet is stored as a role-specific `404`. Insufficient source
balance and destination balance overflow are stored as `409` responses. The
source-balance check runs first when both business checks would fail. Replays
return these stored failures even if wallet state changes later; use a new key
for a new attempt.

## Verify

Keep PostgreSQL and the API running. From this folder, run:

```bash
./verify-idempotency.sh REQUEST_COUNT AMOUNT SOURCE_ID DESTINATION_ID
```

For example:

```bash
./verify-idempotency.sh 10 10 1 2
```

The script uses the root `compose.yaml` for SQL snapshots and checks missing-key
validation, one fresh transfer, equivalent-payload replay, fingerprint
mismatch, and concurrent requests sharing one key. It requires `curl`, `jq`,
Docker Compose, and seeded source and destination wallets. Set `BASE_URL` to
target another API address:

```bash
BASE_URL=http://localhost:9000 ./verify-idempotency.sh 10 10 1 2
```

The verifier intentionally modifies the seeded balances and leaves generated
idempotency records in PostgreSQL for inspection.

## Limitations

- Idempotency records never expire and there is no cleanup worker or TTL.
- The verifier changes seeded wallet balances and leaves those changes and its
  records behind.
- All wallets use one implicit currency; currency conversion and currency
  validation are outside this experiment.
- There is no transfer ledger, audit trail, or accounting history.
- The example demonstrates idempotency and transaction behavior, not a
  production accounting system.
