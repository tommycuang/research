package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const createTransactionsSQL = `CREATE TABLE db_indexes.transactions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_wallet_id BIGINT NOT NULL,
    destination_wallet_id BIGINT NOT NULL,
    status TEXT NOT NULL,
    amount_cents BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    reference TEXT NOT NULL,
    description TEXT NOT NULL,
    CHECK (source_wallet_id <> destination_wallet_id),
    CHECK (amount_cents > 0),
    CHECK (status IN ('completed', 'pending', 'failed'))
)`

type Lab struct {
	pool   *pgxpool.Pool
	output io.Writer
}

type SeedOptions struct {
	Rows      int64
	Seed      int64
	BatchSize int
}

type SeedReport struct {
	Rows      int64
	Completed int64
	Pending   int64
	Failed    int64
	Wallets   int64
}

type BenchmarkOptions struct {
	Rows int64
	Seed int64
}

type BenchmarkReport struct {
	Rows          int64
	Elapsed       time.Duration
	RowsPerSecond float64
	IndexBytes    int64
}

func Open(ctx context.Context, databaseURL string, output io.Writer) (*Lab, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	if output == nil {
		output = io.Discard
	}
	return &Lab{pool: pool, output: output}, nil
}

func (lab *Lab) Close() {
	lab.pool.Close()
}

func (lab *Lab) Setup(ctx context.Context) error {
	tx, err := lab.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema setup: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, statement := range []string{
		`DROP SCHEMA IF EXISTS db_indexes CASCADE`,
		`CREATE SCHEMA db_indexes`,
		createTransactionsSQL,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("recreate db_indexes schema: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit schema setup: %w", err)
	}
	return nil
}

func (lab *Lab) Seed(ctx context.Context, options SeedOptions) (SeedReport, error) {
	if options.Rows <= 0 {
		return SeedReport{}, errors.New("rows must be positive")
	}
	if options.BatchSize <= 0 {
		return SeedReport{}, errors.New("batch size must be positive")
	}

	exists, err := lab.transactionsExist(ctx)
	if err != nil {
		return SeedReport{}, err
	}
	if !exists {
		return SeedReport{}, errors.New("db_indexes.transactions is missing; run setup first")
	}
	if _, err := lab.pool.Exec(ctx, `TRUNCATE db_indexes.transactions RESTART IDENTITY`); err != nil {
		return SeedReport{}, fmt.Errorf("clear transactions: %w", err)
	}

	generator := NewGenerator(options.Seed)
	columns := []string{
		"source_wallet_id", "destination_wallet_id", "status", "amount_cents",
		"created_at", "reference", "description",
	}
	for first := int64(1); first <= options.Rows; first += int64(options.BatchSize) {
		last := min(first+int64(options.BatchSize)-1, options.Rows)
		rows := make([][]any, 0, last-first+1)
		for sequence := first; sequence <= last; sequence++ {
			transaction := generator.Next(sequence)
			rows = append(rows, []any{
				transaction.SourceWalletID,
				transaction.DestinationWalletID,
				transaction.Status,
				transaction.AmountCents,
				transaction.CreatedAt,
				transaction.Reference,
				transaction.Description,
			})
		}
		if _, err := lab.pool.CopyFrom(
			ctx,
			pgx.Identifier{"db_indexes", "transactions"},
			columns,
			pgx.CopyFromRows(rows),
		); err != nil {
			return SeedReport{}, fmt.Errorf("copy rows %d..%d: %w", first, last, err)
		}
		fmt.Fprintf(lab.output, "seeded %d/%d rows\n", last, options.Rows)
	}

	if _, err := lab.pool.Exec(ctx, `ANALYZE db_indexes.transactions`); err != nil {
		return SeedReport{}, fmt.Errorf("analyze transactions: %w", err)
	}
	return lab.seedReport(ctx, options.Rows)
}

func (lab *Lab) seedReport(ctx context.Context, expectedRows int64) (SeedReport, error) {
	const query = `
WITH transaction_metrics AS (
    SELECT
        count(*) AS rows,
        count(*) FILTER (WHERE status = 'completed') AS completed,
        count(*) FILTER (WHERE status = 'pending') AS pending,
        count(*) FILTER (WHERE status = 'failed') AS failed,
        count(*) FILTER (WHERE source_wallet_id = destination_wallet_id) AS same_wallet,
        count(*) FILTER (WHERE amount_cents <= 0) AS invalid_amount,
        count(*) FILTER (
            WHERE created_at < TIMESTAMPTZ '2025-01-01 00:00:00+00'
               OR created_at >= TIMESTAMPTZ '2026-01-01 00:00:00+00'
        ) AS invalid_timestamp,
        count(*) FILTER (WHERE status NOT IN ('completed', 'pending', 'failed')) AS invalid_status
    FROM db_indexes.transactions
), wallet_metrics AS (
    SELECT count(DISTINCT wallet_id) AS wallets
    FROM (
        SELECT source_wallet_id AS wallet_id FROM db_indexes.transactions
        UNION ALL
        SELECT destination_wallet_id FROM db_indexes.transactions
    ) AS transaction_wallets
)
SELECT
    transaction_metrics.rows,
    transaction_metrics.completed,
    transaction_metrics.pending,
    transaction_metrics.failed,
    wallet_metrics.wallets,
    transaction_metrics.same_wallet,
    transaction_metrics.invalid_amount,
    transaction_metrics.invalid_timestamp,
    transaction_metrics.invalid_status
FROM transaction_metrics
CROSS JOIN wallet_metrics`

	var report SeedReport
	var sameWallet, invalidAmount, invalidTimestamp, invalidStatus int64
	err := lab.pool.QueryRow(ctx, query).Scan(
		&report.Rows,
		&report.Completed,
		&report.Pending,
		&report.Failed,
		&report.Wallets,
		&sameWallet,
		&invalidAmount,
		&invalidTimestamp,
		&invalidStatus,
	)
	if err != nil {
		return SeedReport{}, fmt.Errorf("check seed invariants: %w", err)
	}

	if report.Rows != expectedRows {
		return SeedReport{}, fmt.Errorf("row count invariant failed: got %d, want %d", report.Rows, expectedRows)
	}
	if sameWallet != 0 {
		return SeedReport{}, fmt.Errorf("wallet invariant failed: %d transactions have matching source and destination", sameWallet)
	}
	if invalidAmount != 0 {
		return SeedReport{}, fmt.Errorf("amount invariant failed: %d transactions have non-positive amounts", invalidAmount)
	}
	if invalidTimestamp != 0 {
		return SeedReport{}, fmt.Errorf("timestamp invariant failed: %d transactions are outside dataset bounds", invalidTimestamp)
	}
	if invalidStatus != 0 || report.Completed+report.Pending+report.Failed != report.Rows {
		return SeedReport{}, fmt.Errorf("status invariant failed: %d invalid statuses", invalidStatus)
	}
	if expectedRows >= 10_000 && (report.Completed*100 < report.Rows*80 || report.Completed*100 > report.Rows*90) {
		return SeedReport{}, fmt.Errorf("status skew invariant failed: %d of %d rows are completed", report.Completed, report.Rows)
	}
	if expectedRows >= 100_000 && report.Wallets < 9_000 {
		return SeedReport{}, fmt.Errorf("wallet cardinality invariant failed: got %d, want at least 9000", report.Wallets)
	}
	return report, nil
}

func (lab *Lab) ResetIndexes(ctx context.Context) ([]string, error) {
	exists, err := lab.transactionsExist(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("db_indexes.transactions is missing; run setup first")
	}

	const query = `
SELECT index_class.relname
FROM pg_index AS index_metadata
JOIN pg_class AS table_class ON table_class.oid = index_metadata.indrelid
JOIN pg_namespace AS table_namespace ON table_namespace.oid = table_class.relnamespace
JOIN pg_class AS index_class ON index_class.oid = index_metadata.indexrelid
WHERE table_namespace.nspname = 'db_indexes'
  AND table_class.relname = 'transactions'
  AND NOT index_metadata.indisprimary
ORDER BY index_class.relname`

	rows, err := lab.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list secondary indexes: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read secondary index: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list secondary indexes: %w", err)
	}
	rows.Close()

	for _, name := range names {
		identifier := pgx.Identifier{"db_indexes", name}.Sanitize()
		if _, err := lab.pool.Exec(ctx, "DROP INDEX "+identifier); err != nil {
			return nil, fmt.Errorf("drop secondary index %q: %w", name, err)
		}
	}
	return names, nil
}

func (lab *Lab) BenchmarkWrites(ctx context.Context, options BenchmarkOptions) (BenchmarkReport, error) {
	if options.Rows <= 0 {
		return BenchmarkReport{}, errors.New("rows must be positive")
	}

	generator := NewGenerator(options.Seed)
	rows := make([][]any, 0, options.Rows)
	for offset := int64(0); offset < options.Rows; offset++ {
		transaction := generator.Next(1_000_000_000 + offset)
		rows = append(rows, []any{
			transaction.SourceWalletID,
			transaction.DestinationWalletID,
			transaction.Status,
			transaction.AmountCents,
			transaction.CreatedAt,
			transaction.Reference,
			transaction.Description,
		})
	}

	tx, err := lab.pool.Begin(ctx)
	if err != nil {
		return BenchmarkReport{}, fmt.Errorf("begin benchmark: %w", err)
	}
	defer tx.Rollback(ctx)

	started := time.Now()
	_, copyErr := tx.CopyFrom(
		ctx,
		pgx.Identifier{"db_indexes", "transactions"},
		[]string{
			"source_wallet_id", "destination_wallet_id", "status", "amount_cents",
			"created_at", "reference", "description",
		},
		pgx.CopyFromRows(rows),
	)
	elapsed := time.Since(started)
	if copyErr != nil {
		return BenchmarkReport{}, benchmarkRollbackResult(copyErr, tx.Rollback(ctx))
	}
	if err := tx.Rollback(ctx); err != nil {
		return BenchmarkReport{}, fmt.Errorf("roll back benchmark rows: %w", err)
	}

	var indexBytes int64
	if err := lab.pool.QueryRow(ctx, `SELECT pg_indexes_size('db_indexes.transactions'::regclass)`).Scan(&indexBytes); err != nil {
		return BenchmarkReport{}, fmt.Errorf("measure index size: %w", err)
	}
	if _, err := lab.pool.Exec(ctx, `VACUUM (ANALYZE) db_indexes.transactions`); err != nil {
		return BenchmarkReport{}, fmt.Errorf("vacuum after benchmark: %w", err)
	}

	return BenchmarkReport{
		Rows:          options.Rows,
		Elapsed:       elapsed,
		RowsPerSecond: float64(options.Rows) / elapsed.Seconds(),
		IndexBytes:    indexBytes,
	}, nil
}

func (lab *Lab) transactionsExist(ctx context.Context) (bool, error) {
	var exists bool
	if err := lab.pool.QueryRow(ctx, `SELECT to_regclass('db_indexes.transactions') IS NOT NULL`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check setup: %w", err)
	}
	return exists, nil
}

func benchmarkRollbackResult(copyErr, rollbackErr error) error {
	if rollbackErr != nil {
		return fmt.Errorf("copy benchmark rows: %w; roll back benchmark rows: %v", copyErr, rollbackErr)
	}
	return fmt.Errorf("copy benchmark rows: %w", copyErr)
}
