# Research Workspace

Collection of small, independent code experiments. Each topic lives in its own folder and includes a local README with its purpose, startup instructions, and verification steps.

## Requirements

Requirements vary by topic. Current experiments use:

- Docker with Docker Compose
- Go 1.26 or newer
- `curl`

## Research Topics

| Topic | Description | Documentation |
| --- | --- | --- |
| `db-transactions/` | Compare normal, pessimistic-lock, and optimistic-lock database updates under concurrency | [`db-transactions/README.md`](db-transactions/README.md) |

## Shared Infrastructure

Root `compose.yaml` currently provides PostgreSQL and SQLPad for database-oriented experiments.

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
