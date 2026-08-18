# Idempotent Wallet Transfers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the debit-only endpoint with an atomic, PostgreSQL-backed wallet transfer that executes once per idempotency key and replays its original response.

**Architecture:** A PostgreSQL transaction reserves `(operation, idempotency_key)`, locks both wallets in ID order, validates and applies the transfer, and saves the HTTP response before committing. Concurrent requests with the same key wait for that transaction, then replay its response without changing either wallet again.

**Tech Stack:** Go 1.26, Gin, `database/sql`, pgx, PostgreSQL 16, Bash, curl

**Spec:** Approved design captured in the Resolved Design section below.

## Global Constraints

- Keep `GET /` and only `POST /transfer`; remove pessimistic and optimistic transfer routes.
- Leave `db-transactions/` unchanged.
- Require exactly one `Idempotency-Key` containing 1-255 visible ASCII bytes (`!` through `~`).
- Scope keys to stable operation name `transfer`; key comparison is case-sensitive.
- Treat different keys as distinct transfers even when their payloads match.
- Keep idempotency records indefinitely; add no TTL or cleanup worker.
- Persist completed `200`, `404`, and business `409` responses; do not persist validation errors or infrastructure `500` responses.
- Use exact decimal arithmetic. Accept positive JSON numbers with at most two decimal places; reject strings and exponent notation.
- Use PostgreSQL `READ COMMITTED` transactions and client context cancellation.
- Keep `PROCESSING_DELAY` after wallet validation while both wallet row locks are held.
- Use the existing root migration workflow; do not create tables at application startup.
- Use a shell verification script against real PostgreSQL rather than a PostgreSQL integration test suite.
- Add no transfer ledger; this remains a focused research experiment, not production accounting.

---

## Resolved Design

### HTTP Contract

`POST /transfer` accepts:

```json
{
  "source_wallet_id": 1,
  "destination_wallet_id": 2,
  "amount": 100
}
```

Validation order is content type, idempotency key, then JSON body. Accept `application/json` with parameters such as `charset=utf-8`; return `400` for an unsupported content type. Reject unknown fields, duplicate fields, trailing JSON, equal wallet IDs, nonpositive IDs, numeric strings, exponent notation, zero or negative amounts, amounts with more than two decimal places, and amounts outside `NUMERIC(20,2)`.

Equivalent amounts such as `100`, `100.0`, and `100.00` normalize to `100.00` and produce the same request fingerprint.

Successful responses return exact decimal strings:

```json
{
  "source_wallet_id": 1,
  "destination_wallet_id": 2,
  "amount": "100.00",
  "source_balance": "999900.00",
  "destination_balance": "1000100.00",
  "transferred_at": "2026-08-14T12:00:00Z"
}
```

### Idempotency Semantics

- Missing or invalid key: `400`, not stored.
- First valid key and payload: execute transfer and store completed response.
- Same key and equivalent payload: return stored status/body with `Idempotency-Replayed: true`.
- Same key and different payload: `409`, without changing original record.
- Multiple simultaneous requests with same key: one executes; others wait and replay.
- Same payload under different keys: execute once per key.
- Stored business failure remains final even if wallet state later changes; a new attempt requires a new key.
- Database failure before commit rolls back reservation and wallet changes, allowing same-key retry.
- Client cancellation rolls back active transaction. A waiting duplicate may then reserve key and execute.
- Do not echo or log idempotency keys.

### Full Transfer Semantics

- Transfer moves one amount from distinct source wallet to destination wallet atomically.
- All wallets use one implicit currency.
- Lock both wallet rows in ascending ID order to prevent opposite-direction deadlocks.
- Return and persist role-specific `404` errors: `source wallet not found` or `destination wallet not found`.
- Check source funds before destination capacity; source insufficiency wins when both checks would fail.
- Return and persist `409` for insufficient funds or destination `NUMERIC(20,2)` overflow.
- Increment both wallet versions and assign both rows one shared `updated_at` value.
- Return both resulting balances and shared timestamp.

---

### Task 1: Domain Language And Database Schema

**Files:**
- Create: `CONTEXT.md`
- Create: `migrations/004_create_idempotency_records.sql`
- Modify: `README.md`

**Interfaces:**
- Consumes: Existing `wallets` table from migrations 002 and 003.
- Produces: `idempotency_records` keyed by `(operation, idempotency_key)` for Task 3.

- [ ] **Step 1: Create focused domain glossary**

Create root `CONTEXT.md` using the repository's single context:

```markdown
# Wallet Transfers

This context models movement of money between wallets for database behavior experiments.

## Language

**Wallet**:
A balance holder denominated in the experiment's single implicit currency.

**Transfer**:
An atomic movement of money from one source wallet to one distinct destination wallet.
_Avoid_: Debit, withdrawal
```

Do not add `Idempotency Key`, `Idempotency Record`, or `Replay`; these are general technical concepts rather than project domain language.

- [ ] **Step 2: Create migration**

Create `migrations/004_create_idempotency_records.sql`:

```sql
CREATE TABLE idempotency_records (
    operation TEXT NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    request_fingerprint BYTEA NOT NULL
        CHECK (octet_length(request_fingerprint) = 32),
    response_status SMALLINT,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (operation, idempotency_key),
    CHECK (
        (
            response_status IS NULL
            AND response_body IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            response_status BETWEEN 100 AND 599
            AND response_body IS NOT NULL
            AND completed_at IS NOT NULL
        )
    )
);
```

The nullable response fields support an uncommitted reservation. Application must complete the row before commit, so incomplete rows remain invisible and roll back on failure.

- [ ] **Step 3: Document migration command**

Add migration 004 to root database setup instructions:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/004_create_idempotency_records.sql
```

- [ ] **Step 4: Apply migration**

Run from repository root:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/004_create_idempotency_records.sql
```

Expected: `CREATE TABLE`.

- [ ] **Step 5: Verify schema**

Run:

```bash
docker compose exec -T postgres psql -U postgres -d researchs -c '\d idempotency_records'
```

Expected: composite primary key, 32-byte fingerprint check, JSONB response, status, and both timestamp columns.

### Task 2: Go Module And Strict Request Validation

**Files:**
- Create: `idempotency/go.mod`
- Create: `idempotency/go.sum`
- Create: `idempotency/main_test.go`
- Modify: `idempotency/main.go`

**Interfaces:**
- Produces: `readIdempotencyKey(http.Header) (string, error)`
- Produces: `decodeTransferRequest(io.Reader) (transferRequest, error)`
- Produces: `normalizeAmount(json.Number) (string, error)`
- Produces: `requestFingerprint(transferRequest) ([32]byte, error)`
- Produces: canonical `transferRequest` with `SourceWalletID`, `DestinationWalletID`, and two-decimal `Amount`.

- [ ] **Step 1: Create independent Go module**

Create `idempotency/go.mod` with module name `idempotency`, Go 1.26, Gin `v1.12.0`, and pgx `v5.10.0`. Run `go mod tidy` from `idempotency/` to generate `go.sum`.

- [ ] **Step 2: Write failing key-validation tests**

Add table-driven tests covering missing key, repeated header lines, zero length, 256 bytes, whitespace, Unicode, control bytes, and valid visible ASCII. Assert `ABC` and `abc` remain distinct valid values.

- [ ] **Step 3: Write failing JSON-validation tests**

Cover:

```text
valid: 100, 100.0, 100.00
invalid: "100", 1e2, 0, -1, 0.001
invalid: missing IDs, zero IDs, equal IDs
invalid: unknown field, duplicate field, second JSON object, trailing text
invalid: value above 999999999999999999.99
```

Assert all valid equivalent amount spellings produce canonical `100.00`.

- [ ] **Step 4: Write failing fingerprint tests**

Assert equivalent amount spellings have identical SHA-256 fingerprints. Assert changing source, destination, or canonical amount changes fingerprint.

- [ ] **Step 5: Run tests to verify red state**

Run:

```bash
go test ./...
```

Expected: compilation failures for undefined validation and fingerprint helpers.

- [ ] **Step 6: Implement key validation**

Read all `Idempotency-Key` values from `http.Header`. Require exactly one value, length 1-255 bytes, and every byte between `!` and `~` inclusive.

- [ ] **Step 7: Implement strict JSON decoding**

Use `json.Decoder` token traversal to require exactly one object, detect duplicate keys before overwrite, reject unknown fields, decode `amount` as `json.Number`, and require EOF after trailing whitespace.

- [ ] **Step 8: Implement exact amount normalization**

Reject exponent markers and require decimal syntax with at most two fractional digits. Convert accepted amount to exactly two decimal places without `float64`; enforce positive value and `NUMERIC(20,2)` maximum.

- [ ] **Step 9: Implement deterministic fingerprint**

Marshal a fixed-field struct containing source ID, destination ID, and canonical amount, then hash bytes using `sha256.Sum256`.

- [ ] **Step 10: Verify green state**

Run:

```bash
gofmt -w main.go main_test.go
go test ./...
go vet ./...
```

Expected: all tests pass and vet exits zero.

### Task 3: Transactional Idempotent Transfer

**Files:**
- Modify: `idempotency/main.go`
- Create: `idempotency/verify-idempotency.sh`

**Interfaces:**
- Consumes: validation helpers from Task 2.
- Consumes: `idempotency_records` from Task 1.
- Produces: `processTransfer(context.Context, transferRequest, string, [32]byte) (storedResponse, bool, error)` where bool reports replay.
- Produces: `completeIdempotencyRecord(*sql.Tx, int, []byte) error`.

- [ ] **Step 1: Write red full-contract verifier**

Create executable `verify-idempotency.sh` with required arguments:

```bash
./verify-idempotency.sh REQUEST_COUNT AMOUNT SOURCE_ID DESTINATION_ID
```

Use `set -euo pipefail`, `BASE_URL=${BASE_URL:-http://localhost:8080}`, root `compose.yaml`, temporary response files, and unique keys based on timestamp plus PID. Query both wallet balances and versions through PostgreSQL before each assertion group.

- [ ] **Step 2: Define verifier assertions**

Verifier must fail unless all conditions hold:

```text
missing key -> 400 and no wallet changes
first keyed request -> 200 and one debit/credit
same key with equivalent amount -> same result, replay header, no changes
same key with changed amount -> 409 and no changes
N concurrent requests with one new key -> N successful responses
concurrent batch -> one fresh response and N-1 replay responses
concurrent batch -> one debit/credit and one version increment per wallet
```

Leave generated idempotency rows and seeded wallet changes intact for inspection.

- [ ] **Step 3: Run verifier to confirm red state**

Run current server, then:

```bash
./verify-idempotency.sh 10 10 1 2
```

Expected: failure because current endpoint has debit-only payload and no idempotency behavior.

- [ ] **Step 4: Remove obsolete transfer strategies**

Delete pessimistic and optimistic route registration, handlers, retry helpers, retry constants, and pgconn import. Retain `GET /`, `POST /transfer`, database setup, configurable port, `PROCESSING_DELAY`, and database URL behavior.

- [ ] **Step 5: Implement record reservation**

Begin a `sql.LevelReadCommitted` transaction and execute:

```sql
INSERT INTO idempotency_records (
    operation,
    idempotency_key,
    request_fingerprint
)
VALUES ('transfer', $1, $2)
ON CONFLICT DO NOTHING
RETURNING operation;
```

Concurrent inserts for same key wait on PostgreSQL uniqueness resolution.

- [ ] **Step 6: Implement replay and mismatch behavior**

When insert returns no row, select fingerprint, response status, and JSONB body for `(transfer, key)`. Return `409` when fingerprints differ. Return stored status/body and replay flag when fingerprints match. Do not inspect or lock wallets on either path.

- [ ] **Step 7: Lock and identify wallets**

Select both wallet rows with balances ordered by ID:

```sql
SELECT id, balance::text
FROM wallets
WHERE id IN ($1, $2)
ORDER BY id
FOR UPDATE;
```

Map results back to source/destination roles. Complete and commit a role-specific `404` response if either row is missing.

- [ ] **Step 8: Validate business conflicts**

Using exact decimal values, check source balance first and destination maximum second. Complete and commit `409` response for `insufficient balance` or `destination balance limit exceeded` without changing wallet rows.

- [ ] **Step 9: Apply simulated processing delay**

After successful wallet/business validation, wait for `PROCESSING_DELAY` while both wallet locks remain held. Use request context; cancellation must return an error and roll back transaction.

- [ ] **Step 10: Update both wallets atomically**

Obtain one database timestamp, then debit source and credit destination in the same transaction. Set both `updated_at` values to that timestamp and increment both versions. Capture both resulting balances as text.

- [ ] **Step 11: Save response before commit**

Marshal the response to JSON and execute:

```sql
UPDATE idempotency_records
SET response_status = $3,
    response_body = $4::jsonb,
    completed_at = clock_timestamp()
WHERE operation = 'transfer'
  AND idempotency_key = $1
  AND request_fingerprint = $2;
```

Require exactly one affected row. Commit transaction before sending response. On any error, roll back and return `500` without storing a completed result.

- [ ] **Step 12: Wire HTTP responses**

Return stored or fresh JSON with its saved status. Add `Idempotency-Replayed: true` only for replay. Do not echo key. Log operation errors without key values.

- [ ] **Step 13: Verify implementation**

Run:

```bash
gofmt -w main.go main_test.go
go test ./...
go vet ./...
./verify-idempotency.sh 10 10 1 2
```

Expected: unit checks pass, vet exits zero, and verifier reports one transfer per distinct key.

### Task 4: Experiment Documentation And Final Review

**Files:**
- Create: `idempotency/README.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: final HTTP contract, migration, and verifier.
- Produces: complete setup and verification instructions for users.

- [ ] **Step 1: Document experiment locally**

Cover infrastructure startup, migrations 001-004, wallet seeds, `go run .`, configuration variables, request example, required key rules, replay response header, mismatch behavior, permanent business failures, and verifier arguments.

- [ ] **Step 2: Document research limitations**

State that records never expire, verifier modifies seeded balances and leaves records, all wallets share one implicit currency, and no transfer ledger exists. Clarify that example demonstrates idempotency and transaction behavior rather than production accounting.

- [ ] **Step 3: Update workspace topic index**

Add `idempotency/` to root Research Topics table with link to `idempotency/README.md`.

- [ ] **Step 4: Run complete static verification**

From `idempotency/`:

```bash
gofmt -w main.go main_test.go
go test ./...
go vet ./...
```

Expected: all commands exit zero.

- [ ] **Step 5: Run complete runtime verification**

Start infrastructure, apply migration 004, seed wallets if needed, run API, then execute:

```bash
./verify-idempotency.sh 10 10 1 2
```

Expected: all HTTP and SQL assertions pass.

- [ ] **Step 6: Inspect persisted records**

Run:

```bash
docker compose -f ../compose.yaml exec -T postgres \
  psql -U postgres -d researchs -x -c \
  "SELECT operation, idempotency_key, encode(request_fingerprint, 'hex') AS fingerprint, response_status, response_body, created_at, completed_at FROM idempotency_records ORDER BY created_at DESC LIMIT 10"
```

Expected: one completed record per distinct verification key, including replayed and business-failure outcomes.

- [ ] **Step 7: Review final scope**

Inspect final diff. Confirm `db-transactions/` is unchanged, only one transfer route remains in `idempotency/`, no idempotency key logging exists, and no TTL, ledger, or startup migration behavior was introduced.
