# DB Transactions API

Gin HTTP API demonstrating three concurrent balance-update strategies against PostgreSQL.

## Start

Start infrastructure and apply migrations first. See root [`README.md`](../README.md).

From this folder:

```bash
go run .
```

API listens at <http://localhost:8080>. Verify startup:

```bash
curl http://localhost:8080/
```

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

## Request

All transfer endpoints accept:

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
