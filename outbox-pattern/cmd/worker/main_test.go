package main

import (
	"testing"
	"time"
)

func TestParseWorkerFlags(t *testing.T) {
	config, sinkPath, dedupePath, failMode, err := parseWorkerFlags([]string{
		"--once",
		"--batch-size", "2",
		"--lease", "1s",
		"--poll-interval", "10ms",
		"--crash-point", "after-claim",
		"--sink-path", "/tmp/events.jsonl",
		"--dedupe-path", "/tmp/applied.jsonl",
		"--sink-fail-mode", "after-write",
	})
	if err != nil {
		t.Fatalf("parseWorkerFlags() error = %v", err)
	}
	if !config.Once || config.BatchSize != 2 || config.Lease != time.Second || config.PollInterval != 10*time.Millisecond || config.CrashPoint != "after-claim" || sinkPath != "/tmp/events.jsonl" || dedupePath != "/tmp/applied.jsonl" || failMode != "after-write" {
		t.Fatalf("config = %+v sinkPath = %q dedupePath = %q failMode = %q", config, sinkPath, dedupePath, failMode)
	}
}

func TestParseWorkerFlagsRejectsUnknownCrashPoint(t *testing.T) {
	if _, _, _, _, err := parseWorkerFlags([]string{"--crash-point", "unknown"}); err == nil {
		t.Fatal("parseWorkerFlags() error = nil, want unsupported crash point error")
	}
}
