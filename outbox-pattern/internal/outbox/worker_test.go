package outbox

import (
	"errors"
	"testing"
	"time"
)

func TestRetryDelayIncreasesWithAttempts(t *testing.T) {
	first := retryDelay(1)
	second := retryDelay(2)
	if second <= first {
		t.Fatalf("retry delay attempt 2 = %s, want greater than attempt 1 = %s", second, first)
	}
}

func TestExpiredPublishingLeaseIsEligible(t *testing.T) {
	now := time.Date(2026, time.August, 18, 18, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	if !eligibleForClaim(eventState{Status: "publishing", LeaseUntil: &expired}, now) {
		t.Fatal("expired publishing lease was not eligible for claim")
	}
}

func TestPublishedEventIsNotEligible(t *testing.T) {
	now := time.Date(2026, time.August, 18, 18, 0, 0, 0, time.UTC)
	if eligibleForClaim(eventState{Status: "published", AvailableAt: now.Add(-time.Hour)}, now) {
		t.Fatal("published event was eligible for claim")
	}
}

func TestSinkFailureReturnsEventToPending(t *testing.T) {
	now := time.Date(2026, time.August, 18, 18, 0, 0, 0, time.UTC)
	state := retryState(now, 2, errors.New("sink unavailable"))
	if state.Status != "pending" || state.LeaseUntil != nil || state.LastError != "sink unavailable" {
		t.Fatalf("retry state = %+v, want pending with cleared lease and error", state)
	}
	if !state.AvailableAt.After(now) {
		t.Fatalf("available_at = %s, want after %s", state.AvailableAt, now)
	}
}

func TestCrashAfterClaimStopsBeforeSink(t *testing.T) {
	if !shouldCrashAt("after-claim", "after-claim") {
		t.Fatal("after-claim crash point was not selected")
	}
	if shouldCrashAt("after-emit", "after-claim") {
		t.Fatal("after-emit crash point selected at claim boundary")
	}
}

func TestCrashAfterEmitStopsBeforePublishedUpdate(t *testing.T) {
	if !shouldCrashAt("after-emit", "after-emit") {
		t.Fatal("after-emit crash point was not selected")
	}
	if shouldCrashAt("after-claim", "after-emit") {
		t.Fatal("after-claim crash point selected at emit boundary")
	}
}
