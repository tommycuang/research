package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"outbox-pattern/internal/outbox"
	"outbox-pattern/internal/sink"
)

func main() {
	config, sinkPath, dedupePath, failMode, err := parseWorkerFlags(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("pgx", databaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	eventSink, err := sink.NewFileSink(sinkPath, failMode, dedupePath)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := outbox.RunWorker(ctx, db, eventSink, config); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func parseWorkerFlags(args []string) (outbox.WorkerConfig, string, string, string, error) {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	once := flags.Bool("once", false, "claim and process one batch, then exit")
	batchSize := flags.Int("batch-size", 10, "maximum events claimed per batch")
	lease := flags.Duration("lease", 5*time.Second, "claim lease duration")
	pollInterval := flags.Duration("poll-interval", 250*time.Millisecond, "delay between polling batches")
	crashPoint := flags.String("crash-point", "none", "development failure point: none, after-claim, or after-emit")
	sinkPath := flags.String("sink-path", filepath.Join(os.TempDir(), "outbox-pattern-events.jsonl"), "JSONL sink path")
	dedupePath := flags.String("dedupe-path", "", "optional JSONL file for applied event IDs")
	sinkFailMode := flags.String("sink-fail-mode", "", "development failure mode: before-write or after-write")
	if err := flags.Parse(args); err != nil {
		return outbox.WorkerConfig{}, "", "", "", err
	}
	if flags.NArg() != 0 {
		return outbox.WorkerConfig{}, "", "", "", fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *batchSize <= 0 {
		return outbox.WorkerConfig{}, "", "", "", errors.New("batch-size must be positive")
	}
	if *lease <= 0 || *pollInterval <= 0 {
		return outbox.WorkerConfig{}, "", "", "", errors.New("lease and poll-interval must be positive")
	}
	if *crashPoint != "none" && *crashPoint != "after-claim" && *crashPoint != "after-emit" {
		return outbox.WorkerConfig{}, "", "", "", fmt.Errorf("unsupported crash point %q", *crashPoint)
	}
	if *sinkFailMode != "" && *sinkFailMode != "before-write" && *sinkFailMode != "after-write" {
		return outbox.WorkerConfig{}, "", "", "", fmt.Errorf("unsupported sink failure mode %q", *sinkFailMode)
	}
	return outbox.WorkerConfig{
		BatchSize:    *batchSize,
		Lease:        *lease,
		PollInterval: *pollInterval,
		CrashPoint:   *crashPoint,
		Once:         *once,
	}, *sinkPath, *dedupePath, *sinkFailMode, nil
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable"
}
