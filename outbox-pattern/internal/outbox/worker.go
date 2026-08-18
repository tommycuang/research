package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"outbox-pattern/internal/sink"
)

const (
	defaultWorkerBatchSize    = 10
	defaultWorkerLease        = 5 * time.Second
	defaultWorkerPollInterval = 250 * time.Millisecond
	minimumRetryDelay         = 100 * time.Millisecond
	maximumRetryDelay         = 5 * time.Second
)

type WorkerConfig struct {
	BatchSize    int
	Lease        time.Duration
	PollInterval time.Duration
	CrashPoint   string
	Once         bool
}

type eventState struct {
	Status      string
	AvailableAt time.Time
	LeaseUntil  *time.Time
	LastError   string
}

type claimedEvent struct {
	ID        int64
	EventType string
	Payload   json.RawMessage
	Attempts  int
}

func RunWorker(ctx context.Context, db *sql.DB, eventSink sink.Sink, config WorkerConfig) error {
	config = normalizeWorkerConfig(config)
	for {
		if _, err := processBatch(ctx, db, eventSink, config); err != nil {
			return err
		}
		if config.Once {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func processBatch(ctx context.Context, db *sql.DB, eventSink sink.Sink, config WorkerConfig) (int, error) {
	events, err := claimEvents(ctx, db, config)
	if err != nil {
		return 0, err
	}
	if shouldCrashAt(config.CrashPoint, "after-claim") && len(events) > 0 {
		return len(events), errors.New("injected worker crash after claim")
	}

	for _, event := range events {
		envelope := sink.EventEnvelope{
			EventID:   strconv.FormatInt(event.ID, 10),
			EventType: event.EventType,
			Payload:   event.Payload,
			Attempt:   event.Attempts,
		}
		if err := eventSink.Emit(ctx, envelope); err != nil {
			if retryErr := markRetry(ctx, db, event.ID, event.Attempts, err); retryErr != nil {
				return len(events), retryErr
			}
			continue
		}
		if shouldCrashAt(config.CrashPoint, "after-emit") {
			return len(events), errors.New("injected worker crash after emit")
		}
		if err := markPublished(ctx, db, event.ID, event.Attempts); err != nil {
			return len(events), err
		}
	}
	return len(events), nil
}

func shouldCrashAt(crashPoint, boundary string) bool {
	return crashPoint != "none" && crashPoint == boundary
}

func claimEvents(ctx context.Context, db *sql.DB, config WorkerConfig) ([]claimedEvent, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM outbox_events
			WHERE available_at <= clock_timestamp()
				AND (
					status = 'pending'
					OR (status = 'publishing' AND lease_until <= clock_timestamp())
				)
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE outbox_events AS event
		SET status = 'publishing',
			attempts = attempts + 1,
			lease_until = clock_timestamp() + $2::interval,
			last_error = NULL
		FROM candidates
		WHERE event.id = candidates.id
		RETURNING event.id, event.event_type, event.payload, event.attempts
	`, config.BatchSize, config.Lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []claimedEvent
	for rows.Next() {
		var event claimedEvent
		var payload []byte
		if err := rows.Scan(&event.ID, &event.EventType, &payload, &event.Attempts); err != nil {
			return nil, err
		}
		event.Payload = append(json.RawMessage(nil), payload...)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return events, nil
}

func markPublished(ctx context.Context, db *sql.DB, eventID int64, attempt int) error {
	result, err := db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'published',
			published_at = clock_timestamp(),
			lease_until = NULL
		WHERE id = $1
			AND status = 'publishing'
			AND attempts = $2
	`, eventID, attempt)
	if err != nil {
		return err
	}
	return requireOneRow(result, "mark event published")
}

func markRetry(ctx context.Context, db *sql.DB, eventID int64, attempt int, cause error) error {
	availableAt := time.Now().UTC().Add(retryDelay(attempt))
	result, err := db.ExecContext(ctx, `
		UPDATE outbox_events
		SET status = 'pending',
			available_at = $2,
			lease_until = NULL,
			last_error = $3
		WHERE id = $1
			AND status = 'publishing'
			AND attempts = $4
	`, eventID, availableAt, cause.Error(), attempt)
	if err != nil {
		return err
	}
	return requireOneRow(result, "return event to pending")
}

func requireOneRow(result sql.Result, operation string) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("%s affected %d rows", operation, rowsAffected)
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	delay := minimumRetryDelay
	if attempt < 1 {
		attempt = 1
	}
	for i := 1; i < attempt && delay < maximumRetryDelay; i++ {
		delay *= 2
		if delay > maximumRetryDelay {
			return maximumRetryDelay
		}
	}
	return delay
}

func eligibleForClaim(state eventState, now time.Time) bool {
	switch state.Status {
	case "pending":
		return !state.AvailableAt.After(now)
	case "publishing":
		return state.LeaseUntil != nil && !state.LeaseUntil.After(now)
	default:
		return false
	}
}

func retryState(now time.Time, attempt int, cause error) eventState {
	return eventState{
		Status:      "pending",
		AvailableAt: now.Add(retryDelay(attempt)),
		LastError:   cause.Error(),
	}
}

func normalizeWorkerConfig(config WorkerConfig) WorkerConfig {
	if config.BatchSize <= 0 {
		config.BatchSize = defaultWorkerBatchSize
	}
	if config.Lease <= 0 {
		config.Lease = defaultWorkerLease
	}
	if config.PollInterval <= 0 {
		config.PollInterval = defaultWorkerPollInterval
	}
	return config
}
