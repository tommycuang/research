//go:build windows

package sink_test

import (
	"path/filepath"
	"testing"

	"outbox-pattern/internal/sink"
)

func TestDeduplicatingSinkRejectsUnsupportedWindowsLock(t *testing.T) {
	_, err := sink.NewFileSink(filepath.Join(t.TempDir(), "events.jsonl"), "", filepath.Join(t.TempDir(), "applied.jsonl"))
	if err == nil {
		t.Fatal("NewFileSink() error = nil, want unsupported Windows dedupe error")
	}
}
