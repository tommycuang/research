//go:build !windows

package sink_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"outbox-pattern/internal/sink"
)

func TestDeduplicatingSinkAppliesAnEventIDOnce(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "events.jsonl")
	dedupePath := filepath.Join(t.TempDir(), "applied.jsonl")
	fileSink, err := sink.NewFileSink(rawPath, "", dedupePath)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}
	envelope := sink.EventEnvelope{
		EventID:   "42",
		EventType: "wallet.transfer.completed",
		Payload:   json.RawMessage(`{"amount":"10.00"}`),
		Attempt:   1,
	}
	if err := fileSink.Emit(context.Background(), envelope); err != nil {
		t.Fatalf("first Emit() error = %v", err)
	}
	if err := fileSink.Emit(context.Background(), envelope); err != nil {
		t.Fatalf("second Emit() error = %v", err)
	}

	contents, err := os.ReadFile(dedupePath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if lines := nonEmptyLines(string(contents)); lines != 1 {
		t.Fatalf("applied effect lines = %d, want 1", lines)
	}
}

func TestDeduplicatingSinkReportsDuplicateDelivery(t *testing.T) {
	rawPath := filepath.Join(t.TempDir(), "events.jsonl")
	dedupePath := filepath.Join(t.TempDir(), "applied.jsonl")
	fileSink, err := sink.NewFileSink(rawPath, "", dedupePath)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}
	envelope := sink.EventEnvelope{EventID: "42", Payload: json.RawMessage(`{}`)}
	if err := fileSink.Emit(context.Background(), envelope); err != nil {
		t.Fatalf("first Emit() error = %v", err)
	}
	if err := fileSink.Emit(context.Background(), envelope); err != nil {
		t.Fatalf("second Emit() error = %v", err)
	}

	contents, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if lines := nonEmptyLines(string(contents)); lines != 2 {
		t.Fatalf("raw delivery lines = %d, want 2", lines)
	}
}

func nonEmptyLines(contents string) int {
	count := 0
	for _, line := range strings.Split(contents, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
