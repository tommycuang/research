# PostgreSQL Indexing Resources

## Knowledge

- [PostgreSQL 16: Using EXPLAIN](https://www.postgresql.org/docs/16/using-explain.html)
  PostgreSQL authority. Use for plan trees, costs, estimated and actual rows,
  loops, filters, buffers, sorting, timing, and `EXPLAIN ANALYZE` caveats.
- [PostgreSQL 16: Introduction to Indexes](https://www.postgresql.org/docs/16/indexes-intro.html)
  PostgreSQL authority. Use for indexed lookup, planner choice, `ANALYZE`, and
  write/storage overhead from maintaining indexes.
- [PostgreSQL 16: Multicolumn Indexes](https://www.postgresql.org/docs/16/indexes-multicolumn.html)
  PostgreSQL authority. Use for leading B-tree columns and equality/range rules.
- [PostgreSQL 16: Indexes and ORDER BY](https://www.postgresql.org/docs/16/indexes-ordering.html)
  PostgreSQL authority. Use for ordered index scans, explicit sorts, scan
  direction, and the special value of ordering support with `LIMIT`.
- [PostgreSQL 16: Combining Multiple Indexes](https://www.postgresql.org/docs/16/indexes-bitmap-scans.html)
  PostgreSQL authority. Use for separate-versus-composite tradeoffs, bitmap
  combinations, lost ordering, and maintenance cost from extra indexes.

## Gaps

- Exact runtimes and plan choices depend on hardware, cache state, PostgreSQL
  statistics, and dataset size. This lab teaches evidence-based comparison, not
  universal timing cutoffs.
