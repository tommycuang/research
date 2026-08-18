# Transactional Outbox Experiment

Gin HTTP API and polling worker demonstrating a PostgreSQL transactional
outbox, alongside the original concurrent balance-update strategies.

## Start

Start infrastructure and apply migrations first. See root [`README.md`](../README.md).

From this folder, apply the outbox migration after the shared migrations:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < ../migrations/005_create_outbox_events.sql
```

From this folder:

```bash
go run .
```

API listens at <http://localhost:8080>. Verify startup:

```bash
curl http://localhost:8080/
```

## Outbox Transfer

`POST /transfer/outbox` updates distinct source and destination wallets and
inserts a `wallet.transfer.completed` event in the same PostgreSQL transaction.
The API does not emit to the sink. Start the API with `go run .`, then send:

```bash
curl -X POST http://localhost:8080/transfer/outbox \
  -H 'Content-Type: application/json' \
  -d '{"source_wallet_id":1,"destination_wallet_id":2,"amount":10.00}'
```

The response is successful only after both wallet changes and the pending
`outbox_events` row commit.

## Worker

Run one batch against the pending events:

```bash
go run ./cmd/worker --once --sink-path /tmp/outbox-pattern-events.jsonl
```

Run continuously with a deduplicating applied-effect ledger:

```bash
go run ./cmd/worker \
  --sink-path /tmp/outbox-pattern-events.jsonl \
  --dedupe-path /tmp/outbox-pattern-applied.jsonl
```

The deduplicating sink uses an OS file lock and is supported on Unix-like
systems. Windows workers can run without `--dedupe-path`.

Development-only crash controls:

```bash
go run ./cmd/worker --once --lease 1s --crash-point after-emit
```

Development-only sink failures can exercise retry state:

```bash
go run ./cmd/worker --once --sink-fail-mode before-write
```

The worker marks an event `published` only after the sink write succeeds. A
crash after emission and before that update causes a retry after lease expiry.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP server port |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable` | PostgreSQL connection |
| `PROCESSING_DELAY` | `500ms` | Simulated slow processing duration |

Example:

```bash
DATABASE_URL='postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable' \
PORT=8080 \
PROCESSING_DELAY=2s \
go run .
```

Use `PROCESSING_DELAY=0s` to disable simulated delay.

## Baseline Request

The three baseline balance-update endpoints accept:

```json
{
  "wallet_id": 1,
  "amount": 100
}
```

## Endpoints

### Normal

```bash
curl -X POST http://localhost:8080/transfer \
  -H 'Content-Type: application/json' \
  -d '{"wallet_id":1,"amount":100}'
```

Uses one conditional atomic `UPDATE`.

### Pessimistic Lock

```bash
curl -X POST http://localhost:8080/transfer/pessimistic \
  -H 'Content-Type: application/json' \
  -d '{"wallet_id":1,"amount":100}'
```

Uses transaction and `SELECT ... FOR UPDATE`. Concurrent requests wait for row lock. Retryable database conflicts use bounded retries.

### Optimistic Lock

```bash
curl -X POST http://localhost:8080/transfer/optimistic \
  -H 'Content-Type: application/json' \
  -d '{"wallet_id":1,"amount":100}'
```

Reads wallet version and writes only when version remains unchanged. Conflicts retry up to 20 times with `100ms` delay.

## Responses

| Status | Meaning |
| --- | --- |
| `200` | Transfer completed |
| `400` | Invalid wallet ID or amount |
| `404` | Wallet not found |
| `409` | Insufficient balance or retry limit reached |
| `500` | Database operation failed |

## Verify Concurrency

Keep API running and use another terminal in this folder:

```bash
./verify-concurrent.sh normal 10 10 1
./verify-concurrent.sh pessimistic 10 10 1
./verify-concurrent.sh optimistic 10 10 1
```

Arguments:

```text
./verify-concurrent.sh MODE REQUEST_COUNT AMOUNT WALLET_ID
```

Script prints every HTTP response and verifies final balance/version. It modifies wallet balance and does not restore original values.

To target another API address:

```bash
BASE_URL=http://localhost:9000 ./verify-concurrent.sh optimistic 10 10 1
```

## Verify Outbox Guarantees

With PostgreSQL and the API running, use temporary wallets and inspect the
database and sink evidence:

```bash
./cmd/dualwrite/verify-dualwrite.sh
./verify-outbox-producer.sh
./verify-outbox-worker.sh
./verify-delivery-guarantees.sh
```

The final verifier demonstrates two raw deliveries with one applied effect
after a post-emit crash. It also shows how marking before emission can lose an
event. The scripts clean up their temporary wallets and outbox rows.
