# Database Transaction Research

Small Go and PostgreSQL project for comparing normal, pessimistic-lock, and optimistic-lock balance updates under concurrent requests.

## Requirements

- Docker with Docker Compose
- Go 1.26 or newer
- `curl`

## Project Map

| Path | Purpose | Documentation |
| --- | --- | --- |
| `compose.yaml` | PostgreSQL and SQLPad services | This file |
| `migrations/` | Database schema migrations | This file |
| `seeds/` | Sample wallet data | This file |
| `db-transactions/` | Gin API and concurrency verification | [`db-transactions/README.md`](db-transactions/README.md) |

## Infrastructure

Start PostgreSQL and SQLPad:

```bash
docker compose up -d
```

| Service | Address | Default credentials |
| --- | --- | --- |
| PostgreSQL | `localhost:5432` | `postgres` / `postgres` |
| SQLPad | <http://localhost:3001> | `admin` / `admin` |

PostgreSQL data persists in `postgres_data`; SQLPad state persists in `sqlpad_data`.

## First Run

1. Start services with `docker compose up -d`.
2. Apply migrations in order.
3. Seed sample wallets.
4. Start API using [`db-transactions/README.md`](db-transactions/README.md).

## Apply Migrations

Run from project root after starting Docker services:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres \
  < migrations/001_create_researchs.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/002_create_wallets.sql

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < migrations/003_add_wallet_version.sql
```

## Seed Wallets

Run after applying all migrations:

```bash
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U postgres -d researchs \
  < seeds/001_seed_wallets.sql
```

## Stop Services

Retain data:

```bash
docker compose down
```

Delete all persisted data:

```bash
docker compose down -v
```

Do not use `docker compose down -v` unless volume deletion is intentional.
