package main

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"outbox-pattern/internal/outbox"
	"outbox-pattern/internal/transfer"
)

func HandleOutboxTransfer(c *gin.Context) {
	request, err := transfer.DecodeTransferRequest(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := outbox.ProcessOutboxTransfer(c.Request.Context(), db, request)
	if err != nil {
		var businessErr *transfer.BusinessError
		if errors.As(err, &businessErr) {
			c.JSON(businessErr.Status, gin.H{"error": businessErr.Message})
			return
		}
		internalError(c, "process outbox transfer", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"source_wallet_id":      request.SourceWalletID,
		"destination_wallet_id": request.DestinationWalletID,
		"amount":                request.Amount,
		"source_balance":        result.SourceBalance,
		"destination_balance":   result.DestinationBalance,
		"transferred_at":        result.TransferredAt,
	})
}
