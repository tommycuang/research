package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"outbox-pattern/internal/sink"
	"outbox-pattern/internal/transfer"
)

type transferEventPayload struct {
	SourceWalletID      int64     `json:"source_wallet_id"`
	DestinationWalletID int64     `json:"destination_wallet_id"`
	Amount              string    `json:"amount"`
	TransferredAt       time.Time `json:"transferred_at"`
}

func main() {
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	order := flags.String("order", "database-first", "write order: database-first or sink-first")
	failPoint := flags.String("fail-point", "none", "failure point: none, after-database, or after-sink")
	sourceWalletID := flags.Int64("source-wallet-id", 1, "source wallet ID")
	destinationWalletID := flags.Int64("destination-wallet-id", 2, "destination wallet ID")
	amount := flags.String("amount", "10", "positive amount")
	sinkPath := flags.String("sink-path", "/tmp/outbox-pattern-dualwrite.jsonl", "JSONL sink path")
	if err := flags.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	request, err := decodeFlags(*sourceWalletID, *destinationWalletID, *amount)
	if err != nil {
		log.Fatal(err)
	}
	if *order != "database-first" && *order != "sink-first" {
		log.Fatalf("unsupported order %q", *order)
	}
	if *failPoint != "none" && *failPoint != "after-database" && *failPoint != "after-sink" {
		log.Fatalf("unsupported fail point %q", *failPoint)
	}

	db, err := sql.Open("pgx", databaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	fileSink, err := sink.NewFileSink(*sinkPath, "", "")
	if err != nil {
		log.Fatal(err)
	}
	if err := runDualWrite(context.Background(), db, fileSink, request, *order, *failPoint); err != nil {
		log.Fatal(err)
	}
}

func runDualWrite(ctx context.Context, db *sql.DB, fileSink sink.Sink, request transfer.TransferRequest, order, failPoint string) error {
	eventID := fmt.Sprintf("dual-write-%d", time.Now().UnixNano())

	switch order {
	case "database-first":
		result, err := transfer.Transfer(ctx, db, request)
		if err != nil {
			return err
		}
		envelope, err := buildEnvelope(eventID, request, result)
		if err != nil {
			return err
		}
		if shouldInjectFailure(order, failPoint, "after-database") {
			return errors.New("injected failure after database commit")
		}
		return fileSink.Emit(ctx, envelope)
	case "sink-first":
		envelope, err := buildEnvelope(eventID, request, transfer.TransferResult{TransferredAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if err := fileSink.Emit(ctx, envelope); err != nil {
			return err
		}
		if shouldInjectFailure(order, failPoint, "after-sink") {
			return errors.New("injected failure after sink write")
		}
		_, err = transfer.Transfer(ctx, db, request)
		return err
	default:
		return fmt.Errorf("unsupported order %q", order)
	}
}

func shouldInjectFailure(order, failPoint, boundary string) bool {
	return failPoint != "none" && order == boundaryOrder(boundary) && failPoint == boundary
}

func boundaryOrder(boundary string) string {
	if boundary == "after-database" {
		return "database-first"
	}
	return "sink-first"
}

func decodeFlags(sourceWalletID, destinationWalletID int64, amount string) (transfer.TransferRequest, error) {
	payload := fmt.Sprintf(
		`{"source_wallet_id":%d,"destination_wallet_id":%d,"amount":%s}`,
		sourceWalletID,
		destinationWalletID,
		amount,
	)
	return transfer.DecodeTransferRequest(strings.NewReader(payload))
}

func buildEnvelope(eventID string, request transfer.TransferRequest, result transfer.TransferResult) (sink.EventEnvelope, error) {
	payload, err := json.Marshal(transferEventPayload{
		SourceWalletID:      request.SourceWalletID,
		DestinationWalletID: request.DestinationWalletID,
		Amount:              request.Amount,
		TransferredAt:       result.TransferredAt,
	})
	if err != nil {
		return sink.EventEnvelope{}, err
	}
	return sink.EventEnvelope{
		EventID:   eventID,
		EventType: "wallet.transfer.completed",
		Payload:   payload,
		Attempt:   1,
	}, nil
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable"
}
