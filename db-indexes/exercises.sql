\set ON_ERROR_STOP on
SET search_path TO db_indexes, public;
\timing on

-- PostgreSQL Indexing Interview Lab
--
-- Run every EXPLAIN twice. Compare the second execution unless discussing
-- cache effects. Before asking for a recommendation, report scan nodes,
-- estimates vs actual rows, loops, rows removed, buffers, sort details,
-- planning time, and execution time.
--
-- For every exercise answer:
-- 1. What would you inspect first?
-- 2. Which node does most work?
-- 3. How selective is the predicate?
-- 4. Are estimated and actual rows reasonably close?
-- 5. Is sort work present, and does it happen before LIMIT?
-- 6. Is the planner's choice reasonable?
-- 7. What index, if any, would you propose, and why?

-- Exercise 1: learn to read a plan tree
-- This small result introduces plan fields. Do not optimize it. Read the
-- indented nodes child-first and identify cost, actual time, rows, loops,
-- buffers, planning time, and execution time.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
LIMIT 5;

-- Exercise 2: selective single-column lookup
-- Investigate how many rows PostgreSQL reads compared with how many it returns.
-- Propose one index or explain why no index should exist.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 4242;

-- Exercise 3: multiple predicates
-- Distinguish an access condition from a filter. Compare the workload fit of
-- separate and composite access paths before proposing anything.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 42
  AND status = 'failed';

-- Exercise 4: equality plus range and column ordering
-- Propose two column orders. Predict which one limits the scanned index range,
-- then validate from Index Cond, Filter, rows, and buffers.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 42
  AND created_at >= TIMESTAMPTZ '2025-12-01 00:00:00+00';

-- Exercise 5: ORDER BY plus LIMIT
-- Identify scan and sort work performed before PostgreSQL can return 50 rows.
-- Decide whether one access path could help filtering and ordering together.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE status = 'completed'
ORDER BY created_at DESC, id DESC
LIMIT 50;

-- Exercise 6: low-selectivity column
-- Quantify both distinct-value count and returned fraction. Low cardinality by
-- itself is not a complete index decision; use this query's measured work.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE status = 'failed';

-- Exercise 7a: deep offset pagination
-- First run this early page and record its plan. Then run the deep page and
-- explain how many matching rows must be processed before 50 can be returned.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 42
ORDER BY created_at DESC, id DESC
LIMIT 50;

EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 42
ORDER BY created_at DESC, id DESC
OFFSET 1500
LIMIT 50;

-- Exercise 7b: keyset template
-- To compare the same target page, run the page immediately before it and copy
-- that page's final created_at and id values. Do not use the target page's final
-- row, which would instead request the following page.
-- Set both psql variables, then run this plan. Keep both order columns in the
-- cursor and explain why the id tie-breaker makes pagination deterministic.
-- Example variable syntax (replace values; these are not dataset answers):
-- \set cursor_created_at '2025-01-01 00:00:00+00'
-- \set cursor_id 1
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE source_wallet_id = 42
  AND (created_at, id) < (:'cursor_created_at'::timestamptz, :cursor_id::bigint)
ORDER BY created_at DESC, id DESC
LIMIT 50;

-- Exercise 8: broad query where a sequential scan can be correct
-- Keep the secondary-index state you tested after Exercise 6. Do not force a
-- plan type. Defend or challenge PostgreSQL's choice from returned fraction,
-- heap access, buffers, and execution time.
EXPLAIN (ANALYZE, BUFFERS)
SELECT *
FROM transactions
WHERE status = 'completed';

-- Exercise 9: write-performance tradeoff
-- No data-changing SQL is supplied. Run the CLI benchmark with:
-- 1. Only the primary key.
-- 2. The useful indexes you retained after the read exercises.
-- 3. An intentionally excessive or redundant index set that you propose.
-- Record elapsed time, rows/second, and index bytes for each state. Explain
-- which minimal workload-driven set you would defend in an interview.
