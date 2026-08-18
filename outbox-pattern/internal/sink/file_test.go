package sink_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"outbox-pattern/internal/sink"
)

func TestFileSinkWritesOneJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	fileSink, err := sink.NewFileSink(path, "", "")
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}

	want := sink.EventEnvelope{
		EventID:   "42",
		EventType: "wallet.transfer.completed",
		Payload:   json.RawMessage(`{"amount":"10.00"}`),
		Attempt:   3,
	}
	if err := fileSink.Emit(context.Background(), want); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open() error = %v", err)
	}
	defer file.Close()

	var got sink.EventEnvelope
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("scanner.Scan() = false, error = %v", scanner.Err())
	}
	if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if scanner.Scan() {
		t.Fatal("sink wrote more than one line")
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner.Err() = %v", err)
	}
	if got.EventID != want.EventID || got.EventType != want.EventType || got.Attempt != want.Attempt || string(got.Payload) != string(want.Payload) {
		t.Fatalf("envelope = %+v, want %+v", got, want)
	}
}

func TestFileSinkFailureBeforeWriteLeavesFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	fileSink, err := sink.NewFileSink(path, "before-write", "")
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}

	err = fileSink.Emit(context.Background(), sink.EventEnvelope{EventID: "42"})
	if err == nil {
		t.Fatal("Emit() error = nil, want injected failure")
	}

	file, readErr := os.Open(path)
	if errors.Is(readErr, os.ErrNotExist) {
		return
	}
	if readErr != nil {
		t.Fatalf("os.Open() error = %v", readErr)
	}
	defer file.Close()
	if _, err := io.ReadAll(file); err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	t.Fatal("sink created output despite before-write failure")
}

func TestFileSinkFailureAfterWriteLeavesOneLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	fileSink, err := sink.NewFileSink(path, "after-write", "")
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}

	err = fileSink.Emit(context.Background(), sink.EventEnvelope{EventID: "42"})
	if err == nil {
		t.Fatal("Emit() error = nil, want injected failure after write")
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("os.Open() error = %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("scanner.Scan() = false, error = %v", scanner.Err())
	}
	if scanner.Scan() {
		t.Fatal("sink wrote more than one line")
	}
}
