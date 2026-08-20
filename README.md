# Research Workspace

Collection of small, independent code experiments. Each topic lives in its own folder and includes a local README with its purpose, startup instructions, and verification steps.

## Requirements

Requirements vary by topic. Current experiments use:

- Docker with Docker Compose
- Go 1.26 or newer
- `curl`
- `lsof` for automatic verifier port selection

## Continuous Integration

GitHub Actions runs the Go CI workflow for every pull request and for pushes
to `main`. The workflow checks `db-transactions/`, `db-indexes/`,
`idempotency/`, and `outbox-pattern/` with:

- `gofmt` formatting validation
- `go vet ./...`
- `go test ./...`
- `go build ./...`

Run the same checks locally from the workspace root:

```bash
set -e
for module in db-transactions db-indexes idempotency outbox-pattern; do
  (
    cd "$module" &&
    test -z "$(gofmt -l .)" &&
    go vet ./... &&
    go test ./... &&
    go build ./...
  )
done
```

## Research Topics

| Topic | Description | Documentation |
| --- | --- | --- |
| `db-transactions/` | Compare normal, pessimistic-lock, and optimistic-lock database updates under concurrency | [`db-transactions/README.md`](db-transactions/README.md) |
| `db-indexes/` | Practice PostgreSQL execution plans, indexing, pagination, and read/write tradeoffs | [`db-indexes/README.md`](db-indexes/README.md) |
| `outbox-pattern/` | Demonstrate transactional outbox production, polling, retries, and delivery guarantees | [`outbox-pattern/README.md`](outbox-pattern/README.md) |
| `idempotency/` | Demonstrate transactional, idempotent wallet transfers under concurrency | [`idempotency/README.md`](idempotency/README.md) |

## Shared Infrastructure

Root `compose.yaml` provides PostgreSQL and SQLPad for database-oriented
experiments, including the isolated `db_indexes` schema used by the indexing
lab.

```bash
docker compose up -d
```

| Service | Address | Default credentials |
| --- | --- | --- |
| PostgreSQL | `localhost:5432` | `postgres` / `postgres` |
| SQLPad | <http://localhost:3001> | `admin` / `admin` |

Data persists in Docker named volumes across restarts.

## Database Setup

Apply current database migrations from workspace root:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres \
  < migrations/001_create_researchs.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/002_create_wallets.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/003_add_wallet_version.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/004_create_idempotency_records.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/005_create_outbox_events.sql
```

Seed sample data:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < seeds/001_seed_wallets.sql
```

## Stop Infrastructure

Retain persisted data:

```bash
docker compose down
```

Delete all persisted data:

```bash
docker compose down -v
```

Do not use `docker compose down -v` unless volume deletion is intentional.
