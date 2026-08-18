package outbox_test

import (
	"encoding/json"
	"testing"
	"time"

	"outbox-pattern/internal/outbox"
	"outbox-pattern/internal/transfer"
)

func TestBuildTransferEventCanonicalPayload(t *testing.T) {
	transferredAt := time.Date(2026, time.August, 18, 18, 0, 0, 0, time.UTC)
	eventType, payload, err := outbox.BuildTransferEvent(transfer.TransferRequest{
		SourceWalletID:      11,
		DestinationWalletID: 12,
		Amount:              "10.00",
	}, transferredAt)
	if err != nil {
		t.Fatalf("BuildTransferEvent() error = %v", err)
	}

	var got struct {
		SourceWalletID      int64     `json:"source_wallet_id"`
		DestinationWalletID int64     `json:"destination_wallet_id"`
		Amount              string    `json:"amount"`
		TransferredAt       time.Time `json:"transferred_at"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.SourceWalletID != 11 || got.DestinationWalletID != 12 || got.Amount != "10.00" || !got.TransferredAt.Equal(transferredAt) {
		t.Fatalf("payload = %+v, want transfer values at %s", got, transferredAt)
	}
	if eventType != "wallet.transfer.completed" {
		t.Fatalf("event type = %q, want %q", eventType, "wallet.transfer.completed")
	}
}

func TestBuildTransferEventUsesWalletTransferType(t *testing.T) {
	eventType, _, err := outbox.BuildTransferEvent(transfer.TransferRequest{Amount: "1.00"}, time.Time{})
	if err != nil {
		t.Fatalf("BuildTransferEvent() error = %v", err)
	}
	if eventType != outbox.TransferCompletedEventType {
		t.Fatalf("event type = %q, want exported transfer event type %q", eventType, outbox.TransferCompletedEventType)
	}
}
