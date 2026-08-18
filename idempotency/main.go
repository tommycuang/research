package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var db *sql.DB

const (
	defaultProcessDelay  = 500 * time.Millisecond
	maximumWalletBalance = "999999999999999999.99"
)

type transferRequest struct {
	SourceWalletID      int64
	DestinationWalletID int64
	Amount              string
}

type storedResponse struct {
	status int
	body   []byte
}

type transferResponseBody struct {
	SourceWalletID      int64     `json:"source_wallet_id"`
	DestinationWalletID int64     `json:"destination_wallet_id"`
	Amount              string    `json:"amount"`
	SourceBalance       string    `json:"source_balance"`
	DestinationBalance  string    `json:"destination_balance"`
	TransferredAt       time.Time `json:"transferred_at"`
}

func readIdempotencyKey(header http.Header) (string, error) {
	values := header.Values("Idempotency-Key")
	if len(values) != 1 {
		return "", errors.New("exactly one Idempotency-Key is required")
	}

	key := values[0]
	if len(key) < 1 || len(key) > 255 {
		return "", errors.New("Idempotency-Key must contain 1-255 bytes")
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '!' || key[i] > '~' {
			return "", errors.New("Idempotency-Key must contain visible ASCII bytes")
		}
	}
	return key, nil
}

func decodeTransferRequest(reader io.Reader) (transferRequest, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	first, err := decoder.Token()
	if err != nil {
		return transferRequest{}, errors.New("request must contain one JSON object")
	}
	if delimiter, ok := first.(json.Delim); !ok || delimiter != '{' {
		return transferRequest{}, errors.New("request must contain one JSON object")
	}

	var request transferRequest
	var amount json.Number
	var hasSourceWalletID bool
	var hasDestinationWalletID bool
	var hasAmount bool
	seen := make(map[string]bool)

	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return transferRequest{}, errors.New("invalid JSON object")
		}
		key, ok := keyToken.(string)
		if !ok {
			return transferRequest{}, errors.New("invalid JSON object key")
		}
		if seen[key] {
			return transferRequest{}, errors.New("duplicate JSON field")
		}
		seen[key] = true

		switch key {
		case "source_wallet_id":
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return transferRequest{}, errors.New("invalid source_wallet_id")
			}
			number, ok := value.(json.Number)
			if !ok {
				return transferRequest{}, errors.New("source_wallet_id must be a positive integer")
			}
			request.SourceWalletID, err = strconv.ParseInt(number.String(), 10, 64)
			if err != nil || request.SourceWalletID <= 0 {
				return transferRequest{}, errors.New("source_wallet_id must be a positive integer")
			}
			hasSourceWalletID = true
		case "destination_wallet_id":
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return transferRequest{}, errors.New("invalid destination_wallet_id")
			}
			number, ok := value.(json.Number)
			if !ok {
				return transferRequest{}, errors.New("destination_wallet_id must be a positive integer")
			}
			request.DestinationWalletID, err = strconv.ParseInt(number.String(), 10, 64)
			if err != nil || request.DestinationWalletID <= 0 {
				return transferRequest{}, errors.New("destination_wallet_id must be a positive integer")
			}
			hasDestinationWalletID = true
		case "amount":
			value, err := decodeJSONValue(decoder)
			if err != nil {
				return transferRequest{}, errors.New("invalid amount")
			}
			var ok bool
			amount, ok = value.(json.Number)
			if !ok {
				return transferRequest{}, errors.New("amount must be a positive JSON number")
			}
			request.Amount, err = normalizeAmount(amount)
			if err != nil {
				return transferRequest{}, err
			}
			hasAmount = true
		default:
			return transferRequest{}, errors.New("unknown JSON field")
		}
	}

	end, err := decoder.Token()
	if err != nil {
		return transferRequest{}, errors.New("invalid JSON object")
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != '}' {
		return transferRequest{}, errors.New("invalid JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return transferRequest{}, errors.New("trailing JSON data")
	}

	if !hasSourceWalletID || !hasDestinationWalletID || !hasAmount {
		return transferRequest{}, errors.New("source_wallet_id, destination_wallet_id, and amount are required")
	}
	if request.SourceWalletID == request.DestinationWalletID {
		return transferRequest{}, errors.New("source and destination wallets must differ")
	}
	return request, nil
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func normalizeAmount(number json.Number) (string, error) {
	raw := number.String()
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

func requestFingerprint(request transferRequest) ([32]byte, error) {
	canonical := struct {
		SourceWalletID      int64  `json:"source_wallet_id"`
		DestinationWalletID int64  `json:"destination_wallet_id"`
		Amount              string `json:"amount"`
	}{
		SourceWalletID:      request.SourceWalletID,
		DestinationWalletID: request.DestinationWalletID,
		Amount:              request.Amount,
	}

	payload, err := json.Marshal(canonical)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func main() {
	var err error
	db, err = sql.Open("pgx", databaseURL())
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("connect to database: %v", err)
	}

	router := gin.Default()
	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "server is running"})
	})

	router.POST("/transfer", HandleTransfer)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("server listening on http://localhost:%s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func waitForProcessing(ctx context.Context) error {
	delay := defaultProcessDelay
	if value := os.Getenv("PROCESSING_DELAY"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			log.Printf("invalid PROCESSING_DELAY %q; using %s", value, delay)
		} else {
			delay = parsed
		}
	}
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func HandleTransfer(c *gin.Context) {
	if !acceptsJSON(c.GetHeader("Content-Type")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type must be application/json"})
		return
	}

	key, err := readIdempotencyKey(c.Request.Header)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	request, err := decodeTransferRequest(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fingerprint, err := requestFingerprint(request)
	if err != nil {
		internalError(c, "fingerprint transfer request", err)
		return
	}

	response, replay, err := processTransfer(c.Request.Context(), request, key, fingerprint)
	if err != nil {
		internalError(c, "process transfer", err)
		return
	}
	if replay {
		c.Header("Idempotency-Replayed", "true")
	}
	c.Data(response.status, "application/json", response.body)
}

func acceptsJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func processTransfer(ctx context.Context, request transferRequest, key string, fingerprint [32]byte) (storedResponse, bool, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return storedResponse{}, false, err
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback()
		}
	}()

	var operation string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO idempotency_records (
			operation,
			idempotency_key,
			request_fingerprint
		)
		VALUES ('transfer', $1, $2)
		ON CONFLICT DO NOTHING
		RETURNING operation
	`, key, fingerprint[:]).Scan(&operation)
	if errors.Is(err, sql.ErrNoRows) {
		var storedFingerprint []byte
		var responseStatus sql.NullInt64
		var responseBody []byte
		err = tx.QueryRowContext(ctx, `
			SELECT request_fingerprint, response_status, response_body
			FROM idempotency_records
			WHERE operation = 'transfer'
				AND idempotency_key = $1
		`, key).Scan(&storedFingerprint, &responseStatus, &responseBody)
		if err != nil {
			return storedResponse{}, false, err
		}
		if !bytes.Equal(storedFingerprint, fingerprint[:]) {
			response := errorResponse(http.StatusConflict, "idempotency key already used with a different request")
			if err := tx.Rollback(); err != nil {
				return storedResponse{}, false, err
			}
			finished = true
			return response, false, nil
		}
		if !responseStatus.Valid || responseBody == nil {
			return storedResponse{}, false, errors.New("idempotency record is incomplete")
		}
		response := storedResponse{status: int(responseStatus.Int64), body: append([]byte(nil), responseBody...)}
		if err := tx.Rollback(); err != nil {
			return storedResponse{}, false, err
		}
		finished = true
		return response, true, nil
	}
	if err != nil {
		return storedResponse{}, false, err
	}
	_ = operation

	balances := make(map[int64]string, 2)
	rows, err := tx.QueryContext(ctx, `
		SELECT id, balance::text
		FROM wallets
		WHERE id IN ($1, $2)
		ORDER BY id
		FOR UPDATE
	`, request.SourceWalletID, request.DestinationWalletID)
	if err != nil {
		return storedResponse{}, false, err
	}
	for rows.Next() {
		var id int64
		var balance string
		if err := rows.Scan(&id, &balance); err != nil {
			_ = rows.Close()
			return storedResponse{}, false, err
		}
		balances[id] = balance
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return storedResponse{}, false, err
	}
	if err := rows.Close(); err != nil {
		return storedResponse{}, false, err
	}

	sourceBalance, sourceExists := balances[request.SourceWalletID]
	destinationBalance, destinationExists := balances[request.DestinationWalletID]
	if !sourceExists {
		return completeTransferResponse(ctx, tx, &finished, key, fingerprint, errorResponse(http.StatusNotFound, "source wallet not found"))
	}
	if !destinationExists {
		return completeTransferResponse(ctx, tx, &finished, key, fingerprint, errorResponse(http.StatusNotFound, "destination wallet not found"))
	}

	var hasFunds bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric >= $2::numeric`, sourceBalance, request.Amount).Scan(&hasFunds); err != nil {
		return storedResponse{}, false, err
	}
	if !hasFunds {
		return completeTransferResponse(ctx, tx, &finished, key, fingerprint, errorResponse(http.StatusConflict, "insufficient balance"))
	}

	var hasDestinationCapacity bool
	if err := tx.QueryRowContext(ctx, `
		SELECT $1::numeric + $2::numeric <= $3::numeric
	`, destinationBalance, request.Amount, maximumWalletBalance).Scan(&hasDestinationCapacity); err != nil {
		return storedResponse{}, false, err
	}
	if !hasDestinationCapacity {
		return completeTransferResponse(ctx, tx, &finished, key, fingerprint, errorResponse(http.StatusConflict, "destination balance limit exceeded"))
	}

	if err := waitForProcessing(ctx); err != nil {
		return storedResponse{}, false, err
	}

	var transferredAt time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&transferredAt); err != nil {
		return storedResponse{}, false, err
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE wallets
		SET balance = balance - $2::numeric,
			updated_at = $3,
			version = version + 1
		WHERE id = $1
		RETURNING balance::text
	`, request.SourceWalletID, request.Amount, transferredAt).Scan(&sourceBalance); err != nil {
		return storedResponse{}, false, err
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE wallets
		SET balance = balance + $2::numeric,
			updated_at = $3,
			version = version + 1
		WHERE id = $1
		RETURNING balance::text
	`, request.DestinationWalletID, request.Amount, transferredAt).Scan(&destinationBalance); err != nil {
		return storedResponse{}, false, err
	}

	body, err := json.Marshal(transferResponseBody{
		SourceWalletID:      request.SourceWalletID,
		DestinationWalletID: request.DestinationWalletID,
		Amount:              request.Amount,
		SourceBalance:       sourceBalance,
		DestinationBalance:  destinationBalance,
		TransferredAt:       transferredAt,
	})
	if err != nil {
		return storedResponse{}, false, err
	}
	return completeTransferResponse(ctx, tx, &finished, key, fingerprint, storedResponse{status: http.StatusOK, body: body})
}

func completeTransferResponse(ctx context.Context, tx *sql.Tx, finished *bool, key string, fingerprint [32]byte, response storedResponse) (storedResponse, bool, error) {
	if err := completeIdempotencyRecord(ctx, tx, key, fingerprint, response.status, response.body); err != nil {
		return storedResponse{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return storedResponse{}, false, err
	}
	*finished = true
	return response, false, nil
}

func completeIdempotencyRecord(ctx context.Context, tx *sql.Tx, key string, fingerprint [32]byte, status int, body []byte) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE idempotency_records
		SET response_status = $3,
			response_body = $4::jsonb,
			completed_at = clock_timestamp()
		WHERE operation = 'transfer'
			AND idempotency_key = $1
			AND request_fingerprint = $2
	`, key, fingerprint[:], status, body)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errors.New("idempotency record completion affected unexpected row count")
	}
	return nil
}

func errorResponse(status int, message string) storedResponse {
	body, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: message})
	return storedResponse{status: status, body: body}
}

func internalError(c *gin.Context, operation string, err error) {
	log.Printf("%s: %v", operation, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "could not process transfer"})
}

func databaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://postgres:postgres@localhost:5432/researchs?sslmode=disable"
}
