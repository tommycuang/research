# PostgreSQL Indexing Interview Lab

Practice diagnosing slow PostgreSQL queries, reading execution plans, and
defending index choices in a backend engineering interview. The lab starts with
500,000 deterministic wallet transactions and no secondary indexes.

Read the [learning process and measured summary](learning-summary.pdf).

Do not look for a solution file: none is included. For each exercise, inspect
the plan and propose an index, or argue for no index, before asking the agent for
its recommendation.

## Start

From the repository root:

```bash
docker compose up -d postgres
go -C db-indexes run . setup
go -C db-indexes run . seed
docker compose exec postgres psql -U postgres -d app
```

Inside `psql`, the queries are in [`exercises.sql`](exercises.sql). Copy one
numbered `EXPLAIN (ANALYZE, BUFFERS)` statement at a time. Run it twice and use
the second plan for your main comparison unless you are discussing cache
effects.

The default connection is:

```text
postgres://postgres:postgres@localhost:5432/app?sslmode=disable
```

Set `DATABASE_URL` to override it. Setup drops and recreates only the
`db_indexes` schema, but that removes all lab data and learner-created indexes.

## Seed Options

The normal dataset is 500,000 rows. A smaller seed is useful for checking setup,
but may hide performance differences:

```bash
go -C db-indexes run . seed -rows 10000 -seed 42 -batch-size 1000
```

Seeding truncates the table, streams deterministic batches, runs `ANALYZE`, and
checks row, wallet, status, amount, and timestamp invariants.

## Investigation Loop

For each query:

1. Predict the expensive operation.
2. Run `EXPLAIN (ANALYZE, BUFFERS)` twice.
3. Interpret scan, row, filter, buffer, sort, and timing evidence.
4. Decide whether the planner's choice is reasonable.
5. Propose exact `CREATE INDEX` SQL, or explain why no index should exist.
6. Paste your answer into chat before requesting a recommendation.
7. Test the recommendation and compare both plans.

Use this answer template:

```text
Exercise:
Plan node(s):
Estimated vs actual rows:
Rows removed / loops:
Buffers and sort details:
Planning / execution time:
Why the planner chose this plan:
My proposed CREATE INDEX, or why no index should exist:
```

Absolute timing varies with hardware and cache state. Prefer relative work:
rows visited, rows removed, buffers touched, sorting, and whether a limit lets
execution stop early.

## Reset Secondary Indexes

Return to the no-secondary-index baseline without reseeding:

```bash
go -C db-indexes run . reset-indexes
```

This preserves `transactions_pkey` and drops only secondary indexes on
`db_indexes.transactions`.

## Benchmark Writes

Measure identical rollback-only inserts under the current index set:

```bash
go -C db-indexes run . benchmark-writes
go -C db-indexes run . benchmark-writes -rows 25000 -seed 99
```

The command reports elapsed time, rows per second, and total index bytes. It
rolls back inserted rows and vacuums outside the measured interval. Compare a
baseline, your useful index set, and an intentionally excessive set.

## Tests

```bash
go -C db-indexes test ./...
go -C db-indexes vet ./...
go -C db-indexes build ./...
```

PostgreSQL integration tests are opt-in:

```bash
DB_INDEXES_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/app?sslmode=disable' \
  go -C db-indexes test ./internal/lab -v
```
