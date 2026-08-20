# Mission: PostgreSQL Indexing Interviews

## Why

Prepare for backend engineering interviews by diagnosing PostgreSQL query
performance from execution-plan evidence and defending workload-driven index
decisions.

## Success looks like

- Read an execution plan from child nodes upward.
- Distinguish planner costs and estimates from actual rows and elapsed time.
- Explain when single-column and composite indexes fit a query.
- Defend composite column order using equality, range, and ordering behavior.
- Compare offset and keyset pagination.
- Explain why a sequential scan can be the correct plan.
- Balance read speed against index storage and write maintenance.

## Constraints

- Use the shared PostgreSQL 16 service and the isolated `db_indexes` schema.
- Start with 500,000 deterministic transactions and no secondary indexes.
- Investigate with `EXPLAIN (ANALYZE, BUFFERS)` before changing an index.
- Submit a plan interpretation and index proposal before seeing a recommendation.
- Prefer observed rows, buffers, sorting, and relative timing over fixed thresholds.

## Out of scope

- Production rollout procedures, partitioning, replicas, and server tuning.
- GIN, GiST, BRIN, hash, expression, partial, and covering indexes.
