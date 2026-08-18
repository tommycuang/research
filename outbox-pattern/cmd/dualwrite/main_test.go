package main

import "testing"

func TestShouldInjectFailureAtDatabaseBoundary(t *testing.T) {
	if !shouldInjectFailure("database-first", "after-database", "after-database") {
		t.Fatal("database-first after-database failure was not selected")
	}
	if shouldInjectFailure("sink-first", "after-database", "after-sink") {
		t.Fatal("database boundary failure crossed into sink-first order")
	}
}

func TestShouldInjectFailureAtSinkBoundary(t *testing.T) {
	if !shouldInjectFailure("sink-first", "after-sink", "after-sink") {
		t.Fatal("sink-first after-sink failure was not selected")
	}
	if shouldInjectFailure("database-first", "after-sink", "after-database") {
		t.Fatal("sink boundary failure crossed into database-first order")
	}
}
