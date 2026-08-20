package lab

import (
	"reflect"
	"testing"
	"time"
)

func TestGeneratorIsDeterministic(t *testing.T) {
	left := NewGenerator(42)
	right := NewGenerator(42)

	for sequence := int64(1); sequence <= 100; sequence++ {
		if got, want := left.Next(sequence), right.Next(sequence); !reflect.DeepEqual(got, want) {
			t.Fatalf("sequence %d differs: got %#v want %#v", sequence, got, want)
		}
	}
}

func TestGeneratorMaintainsTransactionInvariants(t *testing.T) {
	generator := NewGenerator(42)
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(-1, 0, 0)
	validStatus := map[string]bool{"completed": true, "pending": true, "failed": true}

	for sequence := int64(1); sequence <= 10_000; sequence++ {
		transaction := generator.Next(sequence)
		if transaction.SourceWalletID == transaction.DestinationWalletID {
			t.Fatalf("sequence %d moves money to the same wallet", sequence)
		}
		if transaction.AmountCents <= 0 {
			t.Fatalf("sequence %d has non-positive amount", sequence)
		}
		if !validStatus[transaction.Status] {
			t.Fatalf("sequence %d has status %q", sequence, transaction.Status)
		}
		if transaction.CreatedAt.Before(start) || !transaction.CreatedAt.Before(end) {
			t.Fatalf("sequence %d has timestamp %s outside dataset", sequence, transaction.CreatedAt)
		}
	}
}

func TestGeneratorCreatesUsefulSkew(t *testing.T) {
	generator := NewGenerator(42)
	statusCounts := map[string]int{}
	hotSources := 0
	const rows = 100_000

	for sequence := int64(1); sequence <= rows; sequence++ {
		transaction := generator.Next(sequence)
		statusCounts[transaction.Status]++
		if transaction.SourceWalletID <= 100 {
			hotSources++
		}
	}

	if completed := statusCounts["completed"]; completed < 82_000 || completed > 88_000 {
		t.Fatalf("completed rows = %d, want 82%%..88%%", completed)
	}
	if failed := statusCounts["failed"]; failed < 3_000 || failed > 7_000 {
		t.Fatalf("failed rows = %d, want 3%%..7%%", failed)
	}
	if hotSources < 37_000 || hotSources > 43_000 {
		t.Fatalf("hot source rows = %d, want 37%%..43%%", hotSources)
	}
}
