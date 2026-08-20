package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"db-indexes/internal/lab"
)

const (
	defaultDatabaseURL   = "postgres://postgres:postgres@localhost:5432/app?sslmode=disable"
	defaultSeedRows      = int64(500_000)
	defaultSeed          = int64(42)
	defaultBatchSize     = 10_000
	defaultBenchmarkRows = int64(25_000)
	defaultBenchmarkSeed = int64(99)
)

const usage = `usage:
  db-indexes setup
  db-indexes seed [-rows 500000] [-seed 42] [-batch-size 10000]
  db-indexes reset-indexes
  db-indexes benchmark-writes [-rows 25000] [-seed 99]`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	if output == nil {
		output = io.Discard
	}

	type commandFunc func(*lab.Lab) error
	var execute commandFunc
	switch args[0] {
	case "setup":
		if len(args) != 1 {
			return fmt.Errorf("setup takes no arguments\n%s", usage)
		}
		execute = func(database *lab.Lab) error {
			if err := database.Setup(ctx); err != nil {
				return err
			}
			fmt.Fprintln(output, "db_indexes schema recreated; existing lab data and learner indexes were removed")
			return nil
		}
	case "seed":
		flags := flag.NewFlagSet("seed", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		rows := flags.Int64("rows", defaultSeedRows, "number of transactions")
		seed := flags.Int64("seed", defaultSeed, "random seed")
		batchSize := flags.Int("batch-size", defaultBatchSize, "COPY batch size")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("invalid seed arguments: %w\n%s", err, usage)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("invalid seed arguments\n%s", usage)
		}
		if *rows <= 0 {
			return errors.New("rows must be positive")
		}
		if *batchSize <= 0 {
			return errors.New("batch size must be positive")
		}
		execute = func(database *lab.Lab) error {
			report, err := database.Seed(ctx, lab.SeedOptions{Rows: *rows, Seed: *seed, BatchSize: *batchSize})
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "seed complete: rows=%d completed=%d pending=%d failed=%d wallets=%d\n",
				report.Rows, report.Completed, report.Pending, report.Failed, report.Wallets)
			return nil
		}
	case "reset-indexes":
		if len(args) != 1 {
			return fmt.Errorf("reset-indexes takes no arguments\n%s", usage)
		}
		execute = func(database *lab.Lab) error {
			dropped, err := database.ResetIndexes(ctx)
			if err != nil {
				return err
			}
			if len(dropped) == 0 {
				fmt.Fprintln(output, "no secondary indexes to drop")
				return nil
			}
			for _, name := range dropped {
				fmt.Fprintf(output, "dropped db_indexes.%s\n", name)
			}
			return nil
		}
	case "benchmark-writes":
		flags := flag.NewFlagSet("benchmark-writes", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		rows := flags.Int64("rows", defaultBenchmarkRows, "number of benchmark transactions")
		seed := flags.Int64("seed", defaultBenchmarkSeed, "random seed")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("invalid benchmark-writes arguments: %w\n%s", err, usage)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("invalid benchmark-writes arguments\n%s", usage)
		}
		if *rows <= 0 {
			return errors.New("rows must be positive")
		}
		execute = func(database *lab.Lab) error {
			report, err := database.BenchmarkWrites(ctx, lab.BenchmarkOptions{Rows: *rows, Seed: *seed})
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "write benchmark: rows=%d elapsed=%s rows/second=%.0f index-bytes=%s\n",
				report.Rows, report.Elapsed, report.RowsPerSecond, humanBytes(report.IndexBytes))
			return nil
		}
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}

	databaseURL := getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	database, err := lab.Open(ctx, databaseURL, output)
	if err != nil {
		return err
	}
	defer database.Close()
	return execute(database)
}

func positiveInt64(value, name string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func humanBytes(bytes int64) string {
	const kibibyte = int64(1024)
	if bytes < kibibyte {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < kibibyte*kibibyte {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(kibibyte))
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(kibibyte*kibibyte))
}
