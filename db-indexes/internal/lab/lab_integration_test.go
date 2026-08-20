package lab

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestBenchmarkRollbackResultIncludesBothFailures(t *testing.T) {
	err := benchmarkRollbackResult(errors.New("copy failed"), errors.New("rollback failed"))
	if !strings.Contains(err.Error(), "copy failed") || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error = %v", err)
	}
}

func openIntegrationLab(t *testing.T) *Lab {
	t.Helper()
	databaseURL := os.Getenv("DB_INDEXES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DB_INDEXES_TEST_DATABASE_URL is not set")
	}
	lab, err := Open(context.Background(), databaseURL, io.Discard)
	if err != nil {
		t.Fatalf("open lab: %v", err)
	}
	t.Cleanup(lab.Close)
	return lab
}

func TestSetupAndSeed(t *testing.T) {
	ctx := context.Background()
	lab := openIntegrationLab(t)
	if err := lab.Setup(ctx); err != nil {
		t.Fatalf("setup: %v", err)
	}

	report, err := lab.Seed(ctx, SeedOptions{Rows: 2_000, Seed: 42, BatchSize: 250})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if report.Rows != 2_000 || report.Completed+report.Pending+report.Failed != 2_000 {
		t.Fatalf("unexpected report: %#v", report)
	}

	var secondaryIndexes int
	err = lab.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_indexes
		WHERE schemaname = 'db_indexes'
		  AND indexname <> 'transactions_pkey'
	`).Scan(&secondaryIndexes)
	if err != nil || secondaryIndexes != 0 {
		t.Fatalf("secondary indexes = %d, err = %v", secondaryIndexes, err)
	}
}

func TestSeedRejectsInvalidOptions(t *testing.T) {
	lab := openIntegrationLab(t)
	_, err := lab.Seed(context.Background(), SeedOptions{Rows: 0, Seed: 42, BatchSize: 100})
	if err == nil || !strings.Contains(err.Error(), "rows must be positive") {
		t.Fatalf("error = %v", err)
	}
	_, err = lab.Seed(context.Background(), SeedOptions{Rows: 100, Seed: 42, BatchSize: 0})
	if err == nil || !strings.Contains(err.Error(), "batch size must be positive") {
		t.Fatalf("error = %v", err)
	}
}

func TestSeedReplacesExistingRows(t *testing.T) {
	ctx := context.Background()
	lab := openIntegrationLab(t)
	if err := lab.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := lab.Seed(ctx, SeedOptions{Rows: 100, Seed: 42, BatchSize: 25}); err != nil {
		t.Fatal(err)
	}
	report, err := lab.Seed(ctx, SeedOptions{Rows: 50, Seed: 42, BatchSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows != 50 {
		t.Fatalf("rows = %d, want 50", report.Rows)
	}
}

func TestSeedAndResetRequireSetup(t *testing.T) {
	ctx := context.Background()
	lab := openIntegrationLab(t)
	if _, err := lab.pool.Exec(ctx, `DROP SCHEMA IF EXISTS db_indexes CASCADE`); err != nil {
		t.Fatal(err)
	}

	_, seedErr := lab.Seed(ctx, SeedOptions{Rows: 10, Seed: 42, BatchSize: 10})
	if seedErr == nil || !strings.Contains(seedErr.Error(), "run setup first") {
		t.Fatalf("seed error = %v", seedErr)
	}
	_, resetErr := lab.ResetIndexes(ctx)
	if resetErr == nil || !strings.Contains(resetErr.Error(), "run setup first") {
		t.Fatalf("reset error = %v", resetErr)
	}
}

func TestResetIndexesPreservesPrimaryKey(t *testing.T) {
	ctx := context.Background()
	lab := openIntegrationLab(t)
	if err := lab.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := lab.pool.Exec(ctx, `CREATE INDEX learner_test_idx ON db_indexes.transactions (amount_cents)`); err != nil {
		t.Fatal(err)
	}

	dropped, err := lab.ResetIndexes(ctx)
	if err != nil {
		t.Fatalf("reset indexes: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != "learner_test_idx" {
		t.Fatalf("dropped = %#v", dropped)
	}

	var primaryExists bool
	if err := lab.pool.QueryRow(ctx, `SELECT to_regclass('db_indexes.transactions_pkey') IS NOT NULL`).Scan(&primaryExists); err != nil || !primaryExists {
		t.Fatalf("primary key exists = %v, err = %v", primaryExists, err)
	}
}

func TestBenchmarkRollsBackRows(t *testing.T) {
	ctx := context.Background()
	lab := openIntegrationLab(t)
	if err := lab.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := lab.Seed(ctx, SeedOptions{Rows: 1_000, Seed: 42, BatchSize: 250}); err != nil {
		t.Fatal(err)
	}

	report, err := lab.BenchmarkWrites(ctx, BenchmarkOptions{Rows: 500, Seed: 99})
	if err != nil {
		t.Fatalf("benchmark: %v", err)
	}
	if report.Rows != 500 || report.Elapsed <= 0 || report.RowsPerSecond <= 0 || report.IndexBytes <= 0 {
		t.Fatalf("unexpected report: %#v", report)
	}

	var rows int64
	if err := lab.pool.QueryRow(ctx, `SELECT count(*) FROM db_indexes.transactions`).Scan(&rows); err != nil || rows != 1_000 {
		t.Fatalf("rows after benchmark = %d, err = %v", rows, err)
	}
}
