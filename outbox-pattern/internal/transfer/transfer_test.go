package transfer_test

import (
	"strings"
	"testing"

	"outbox-pattern/internal/transfer"
)

func TestDecodeTransferRequestNormalizesAmount(t *testing.T) {
	for _, raw := range []string{"10", "10.0", "10.00"} {
		t.Run(raw, func(t *testing.T) {
			request, err := transfer.DecodeTransferRequest(strings.NewReader(
				`{"source_wallet_id":1,"destination_wallet_id":2,"amount":` + raw + `}`,
			))
			if err != nil {
				t.Fatalf("DecodeTransferRequest() error = %v", err)
			}
			if request.Amount != "10.00" {
				t.Fatalf("Amount = %q, want %q", request.Amount, "10.00")
			}
		})
	}
}

func TestDecodeTransferRequestRejectsSameWallet(t *testing.T) {
	_, err := transfer.DecodeTransferRequest(strings.NewReader(
		`{"source_wallet_id":1,"destination_wallet_id":1,"amount":10}`,
	))
	if err == nil {
		t.Fatal("DecodeTransferRequest() error = nil, want same-wallet validation error")
	}
}
