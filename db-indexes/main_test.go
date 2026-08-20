package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunRequiresCommand(t *testing.T) {
	err := run(context.Background(), nil, func(string) string { return "" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsUnknownCommandBeforeConnecting(t *testing.T) {
	err := run(context.Background(), []string{"unknown"}, func(string) string { return "" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunReportsInvalidFlagBeforeConnecting(t *testing.T) {
	err := run(context.Background(), []string{"seed", "-rows", "many"}, func(string) string { return "" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "invalid value \"many\" for flag -rows") {
		t.Fatalf("error = %v", err)
	}
}

func TestPositiveInt64(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
		valid bool
	}{
		{"500000", 500000, true},
		{"1", 1, true},
		{"0", 0, false},
		{"-1", 0, false},
		{"many", 0, false},
	} {
		got, err := positiveInt64(test.input, "rows")
		if (err == nil) != test.valid || got != test.want {
			t.Fatalf("input %q: got %d, err %v", test.input, got, err)
		}
	}
}
