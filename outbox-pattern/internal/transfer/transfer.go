package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maximumWalletBalance = "999999999999999999.99"

type TransferRequest struct {
	SourceWalletID      int64
	DestinationWalletID int64
	Amount              string
}

type TransferResult struct {
	SourceBalance      string
	DestinationBalance string
	TransferredAt      time.Time
}

type BusinessError struct {
	Status  int
	Message string
}

func (e *BusinessError) Error() string {
	return e.Message
}

func DecodeTransferRequest(reader io.Reader) (TransferRequest, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	decoder.DisallowUnknownFields()

	var raw struct {
		SourceWalletID      json.Number `json:"source_wallet_id"`
		DestinationWalletID json.Number `json:"destination_wallet_id"`
		Amount              json.Number `json:"amount"`
	}
	if err := decoder.Decode(&raw); err != nil {
		return TransferRequest{}, errors.New("request must contain one valid JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return TransferRequest{}, errors.New("request must contain one JSON object")
	}

	sourceWalletID, err := parsePositiveID(raw.SourceWalletID)
	if err != nil {
		return TransferRequest{}, errors.New("source_wallet_id must be a positive integer")
	}
	destinationWalletID, err := parsePositiveID(raw.DestinationWalletID)
	if err != nil {
		return TransferRequest{}, errors.New("destination_wallet_id must be a positive integer")
	}
	amount, err := normalizeAmount(raw.Amount)
	if err != nil {
		return TransferRequest{}, err
	}
	if sourceWalletID == destinationWalletID {
		return TransferRequest{}, errors.New("source and destination wallets must differ")
	}

	return TransferRequest{
		SourceWalletID:      sourceWalletID,
		DestinationWalletID: destinationWalletID,
		Amount:              amount,
	}, nil
}

func Transfer(ctx context.Context, db *sql.DB, request TransferRequest) (TransferResult, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return TransferResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := TransferInTx(ctx, tx, request)
	if err != nil {
		return TransferResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TransferResult{}, err
	}
	committed = true
	return result, nil
}

func TransferInTx(ctx context.Context, tx *sql.Tx, request TransferRequest) (TransferResult, error) {
	if request.SourceWalletID <= 0 || request.DestinationWalletID <= 0 {
		return TransferResult{}, &BusinessError{Status: http.StatusBadRequest, Message: "wallet IDs must be positive"}
	}
	if request.SourceWalletID == request.DestinationWalletID {
		return TransferResult{}, &BusinessError{Status: http.StatusBadRequest, Message: "source and destination wallets must differ"}
	}
	if _, err := normalizeAmount(json.Number(request.Amount)); err != nil {
		return TransferResult{}, &BusinessError{Status: http.StatusBadRequest, Message: err.Error()}
	}

	balanceByID := make(map[int64]string, 2)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, balance::text
		FROM wallets
		WHERE id IN ($1, $2)
		ORDER BY id
		FOR UPDATE
	`, request.SourceWalletID, request.DestinationWalletID)
	if err != nil {
		return TransferResult{}, err
	}
	for rows.Next() {
		var id int64
		var balance string
		if err := rows.Scan(&id, &balance); err != nil {
			_ = rows.Close()
			return TransferResult{}, err
		}
		balanceByID[id] = balance
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return TransferResult{}, err
	}
	if err := rows.Close(); err != nil {
		return TransferResult{}, err
	}

	sourceBalance, sourceExists := balanceByID[request.SourceWalletID]
	if !sourceExists {
		return TransferResult{}, &BusinessError{Status: http.StatusNotFound, Message: "source wallet not found"}
	}
	destinationBalance, destinationExists := balanceByID[request.DestinationWalletID]
	if !destinationExists {
		return TransferResult{}, &BusinessError{Status: http.StatusNotFound, Message: "destination wallet not found"}
	}

	var hasFunds bool
	if err := tx.QueryRowContext(ctx, `
		SELECT $1::numeric >= $2::numeric
	`, sourceBalance, request.Amount).Scan(&hasFunds); err != nil {
		return TransferResult{}, err
	}
	if !hasFunds {
		return TransferResult{}, &BusinessError{Status: http.StatusConflict, Message: "insufficient balance"}
	}

	var hasDestinationCapacity bool
	if err := tx.QueryRowContext(ctx, `
		SELECT $1::numeric + $2::numeric <= $3::numeric
	`, destinationBalance, request.Amount, maximumWalletBalance).Scan(&hasDestinationCapacity); err != nil {
		return TransferResult{}, err
	}
	if !hasDestinationCapacity {
		return TransferResult{}, &BusinessError{Status: http.StatusConflict, Message: "destination balance limit exceeded"}
	}

	var transferredAt time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&transferredAt); err != nil {
		return TransferResult{}, err
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE wallets
		SET balance = balance - $2::numeric,
			updated_at = $3,
			version = version + 1
		WHERE id = $1
		RETURNING balance::text
	`, request.SourceWalletID, request.Amount, transferredAt).Scan(&sourceBalance); err != nil {
		return TransferResult{}, err
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE wallets
		SET balance = balance + $2::numeric,
			updated_at = $3,
			version = version + 1
		WHERE id = $1
		RETURNING balance::text
	`, request.DestinationWalletID, request.Amount, transferredAt).Scan(&destinationBalance); err != nil {
		return TransferResult{}, err
	}

	return TransferResult{
		SourceBalance:      sourceBalance,
		DestinationBalance: destinationBalance,
		TransferredAt:      transferredAt,
	}, nil
}

func parsePositiveID(value json.Number) (int64, error) {
	if value.String() == "" {
		return 0, errors.New("missing ID")
	}
	id, err := strconv.ParseInt(value.String(), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid ID")
	}
	return id, nil
}

func normalizeAmount(number json.Number) (string, error) {
	raw := number.String()
	if raw == "" {
		return "", errors.New("amount must be a positive decimal within NUMERIC(20,2)")
	}
	dot := strings.IndexByte(raw, '.')
	integer := raw
	fraction := ""
	if dot >= 0 {
		integer = raw[:dot]
		fraction = raw[dot+1:]
	}
	if integer == "" || len(integer) > 18 || (len(integer) > 1 && integer[0] == '0') {
		return "", errors.New("amount must be a positive decimal within NUMERIC(20,2)")
	}
	for i := 0; i < len(integer); i++ {
		if integer[i] < '0' || integer[i] > '9' {
			return "", errors.New("amount must be a positive decimal within NUMERIC(20,2)")
		}
	}
	if dot >= 0 && (len(fraction) == 0 || len(fraction) > 2) {
		return "", errors.New("amount must have at most two fractional digits")
	}
	for i := 0; i < len(fraction); i++ {
		if fraction[i] < '0' || fraction[i] > '9' {
			return "", errors.New("amount must be a positive decimal within NUMERIC(20,2)")
		}
	}
	if strings.Trim(integer+fraction, "0") == "" {
		return "", errors.New("amount must be positive")
	}

	switch len(fraction) {
	case 0:
		fraction = "00"
	case 1:
		fraction += "0"
	}
	return integer + "." + fraction, nil
}
