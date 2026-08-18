package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"

	"outbox-pattern/internal/transfer"
)

const TransferCompletedEventType = "wallet.transfer.completed"

func BuildTransferEvent(request transfer.TransferRequest, transferredAt time.Time) (string, []byte, error) {
	payload, err := json.Marshal(struct {
		SourceWalletID      int64     `json:"source_wallet_id"`
		DestinationWalletID int64     `json:"destination_wallet_id"`
		Amount              string    `json:"amount"`
		TransferredAt       time.Time `json:"transferred_at"`
	}{
		SourceWalletID:      request.SourceWalletID,
		DestinationWalletID: request.DestinationWalletID,
		Amount:              request.Amount,
		TransferredAt:       transferredAt,
	})
	if err != nil {
		return "", nil, err
	}
	return TransferCompletedEventType, payload, nil
}

func ProcessOutboxTransfer(ctx context.Context, db *sql.DB, request transfer.TransferRequest) (transfer.TransferResult, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return transfer.TransferResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := transfer.TransferInTx(ctx, tx, request)
	if err != nil {
		return transfer.TransferResult{}, err
	}
	eventType, payload, err := BuildTransferEvent(request, result.TransferredAt)
	if err != nil {
		return transfer.TransferResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (event_type, payload)
		VALUES ($1, $2::jsonb)
	`, eventType, payload); err != nil {
		return transfer.TransferResult{}, err
	}
	if os.Getenv("OUTBOX_FAIL_POINT") == "before-commit" {
		return transfer.TransferResult{}, errors.New("injected outbox failure before commit")
	}
	if err := tx.Commit(); err != nil {
		return transfer.TransferResult{}, err
	}
	committed = true
	return result, nil
}
