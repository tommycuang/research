package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleOutboxTransferRejectsInvalidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/transfer/outbox", HandleOutboxTransfer)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/transfer/outbox", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
