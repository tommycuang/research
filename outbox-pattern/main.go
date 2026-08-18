package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var db *sql.DB

const (
	maxTransferAttempts = 20
	transferRetryDelay  = 100 * time.Millisecond
	defaultProcessDelay = 500 * time.Millisecond
)

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
	router.POST("/transfer/pessimistic", HandlePessimisticTransfer)
	router.POST("/transfer/optimistic", HandleOptimisticTransfer)
	router.POST("/transfer/outbox", HandleOutboxTransfer)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("server listening on http://localhost:%s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func HandleTransfer(c *gin.Context) {
	var request struct {
		WalletID int64       `json:"wallet_id" binding:"required,gt=0"`
		Amount   json.Number `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wallet_id and a positive amount are required"})
		return
	}

	amount, err := strconv.ParseFloat(request.Amount.String(), 64)
	if err != nil || amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be a positive number"})
		return
	}
	if !waitForProcessing(c) {
		return
	}

	var balance string
	var updatedAt time.Time
	err = db.QueryRowContext(c.Request.Context(), `
		UPDATE wallets
		SET balance = balance - $2::numeric,
			updated_at = CURRENT_TIMESTAMP,
			version = version + 1
		WHERE id = $1
			AND balance >= $2::numeric
		RETURNING balance::text, updated_at
	`, request.WalletID, request.Amount.String()).Scan(&balance, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if lookupErr := db.QueryRowContext(
			c.Request.Context(),
			"SELECT EXISTS (SELECT 1 FROM wallets WHERE id = $1)",
			request.WalletID,
		).Scan(&exists); lookupErr != nil {
			log.Printf("look up wallet: %v", lookupErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not process transfer"})
			return
		}
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "insufficient balance"})
		return
	}
	if err != nil {
		log.Printf("update wallet: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not process transfer"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"wallet_id":  request.WalletID,
		"balance":    balance,
		"updated_at": updatedAt,
	})
}

func HandlePessimisticTransfer(c *gin.Context) {
	walletID, amount, ok := bindTransfer(c)
	if !ok {
		return
	}

	for attempt := 1; attempt <= maxTransferAttempts; attempt++ {
		tx, err := db.BeginTx(c.Request.Context(), nil)
		if err != nil {
			internalError(c, "begin pessimistic transfer", err)
			return
		}

		var balance string
		err = tx.QueryRowContext(c.Request.Context(), `
			SELECT balance::text
			FROM wallets
			WHERE id = $1
			FOR UPDATE
		`, walletID).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
			return
		}
		if err != nil {
			tx.Rollback()
			if isRetryableTransferError(err) {
				if waitForRetry(c, attempt) {
					continue
				}
				break
			}
			internalError(c, "lock wallet", err)
			return
		}
		if !waitForProcessing(c) {
			tx.Rollback()
			return
		}

		var hasFunds bool
		err = tx.QueryRowContext(c.Request.Context(), "SELECT $1::numeric >= $2::numeric", balance, amount).Scan(&hasFunds)
		if err != nil {
			tx.Rollback()
			internalError(c, "compare wallet balance", err)
			return
		}
		if !hasFunds {
			tx.Rollback()
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient balance"})
			return
		}

		var updatedAt time.Time
		err = tx.QueryRowContext(c.Request.Context(), `
			UPDATE wallets
			SET balance = balance - $2::numeric,
				updated_at = CURRENT_TIMESTAMP,
				version = version + 1
			WHERE id = $1
			RETURNING balance::text, updated_at
		`, walletID, amount).Scan(&balance, &updatedAt)
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err == nil {
			transferResponse(c, walletID, balance, updatedAt)
			return
		}
		if isRetryableTransferError(err) {
			if waitForRetry(c, attempt) {
				continue
			}
			break
		}
		internalError(c, "complete pessimistic transfer", err)
		return
	}

	c.JSON(http.StatusConflict, gin.H{"error": "wallet remained locked; retry transfer"})
}

func HandleOptimisticTransfer(c *gin.Context) {
	walletID, amount, ok := bindTransfer(c)
	if !ok {
		return
	}

	for attempt := 1; attempt <= maxTransferAttempts; attempt++ {
		var balance string
		var version int64
		err := db.QueryRowContext(c.Request.Context(), `
			SELECT balance::text, version
			FROM wallets
			WHERE id = $1
		`, walletID).Scan(&balance, &version)
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
			return
		}
		if err != nil {
			internalError(c, "read wallet version", err)
			return
		}
		if !waitForProcessing(c) {
			return
		}

		var hasFunds bool
		if err := db.QueryRowContext(c.Request.Context(), "SELECT $1::numeric >= $2::numeric", balance, amount).Scan(&hasFunds); err != nil {
			internalError(c, "compare wallet balance", err)
			return
		}
		if !hasFunds {
			c.JSON(http.StatusConflict, gin.H{"error": "insufficient balance"})
			return
		}

		var updatedAt time.Time
		err = db.QueryRowContext(c.Request.Context(), `
			UPDATE wallets
			SET balance = balance - $2::numeric,
				updated_at = CURRENT_TIMESTAMP,
				version = version + 1
			WHERE id = $1
				AND version = $3
			RETURNING balance::text, updated_at
		`, walletID, amount, version).Scan(&balance, &updatedAt)
		if err == nil {
			transferResponse(c, walletID, balance, updatedAt)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			internalError(c, "update wallet version", err)
			return
		}
		if !waitForRetry(c, attempt) {
			break
		}
	}

	c.JSON(http.StatusConflict, gin.H{"error": "wallet kept changing; retry transfer"})
}

func isRetryableTransferError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01" || pgErr.Code == "55P03")
}

func waitForRetry(c *gin.Context, attempt int) bool {
	if attempt >= maxTransferAttempts {
		return false
	}
	timer := time.NewTimer(transferRetryDelay)
	defer timer.Stop()
	select {
	case <-c.Request.Context().Done():
		return false
	case <-timer.C:
		return true
	}
}

func waitForProcessing(c *gin.Context) bool {
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
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-c.Request.Context().Done():
		return false
	case <-timer.C:
		return true
	}
}

func bindTransfer(c *gin.Context) (int64, string, bool) {
	var request struct {
		WalletID int64       `json:"wallet_id" binding:"required,gt=0"`
		Amount   json.Number `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wallet_id and a positive amount are required"})
		return 0, "", false
	}
	amount, err := strconv.ParseFloat(request.Amount.String(), 64)
	if err != nil || amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "amount must be a positive number"})
		return 0, "", false
	}
	return request.WalletID, request.Amount.String(), true
}

func transferResponse(c *gin.Context, walletID int64, balance string, updatedAt time.Time) {
	c.JSON(http.StatusOK, gin.H{
		"wallet_id":  walletID,
		"balance":    balance,
		"updated_at": updatedAt,
	})
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
