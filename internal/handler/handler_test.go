package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"wallet-transfer-assignment/internal/model"
)

type fakeTransferService struct {
	result model.TransferResult
	err    error
	calls  int
}

func (f *fakeTransferService) CreateTransfer(ctx context.Context, req model.TransferRequest) (model.TransferResult, error) {
	f.calls++
	return f.result, f.err
}

func TestCreateTransferInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader("{not-valid-json"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Zero(t, fake.calls, "service should not be called on invalid JSON")
}

func TestCreateTransferSuccessfulResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{result: model.TransferResult{ID: "tx-1", State: model.TransferStateProcessed, IdempotencyKey: "k-1", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 100}}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	reqBody := `{"idempotencyKey":"k-1","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":100}`
	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, 1, fake.calls)
}

func TestCreateTransferInsufficientFunds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{
		result: model.TransferResult{State: model.TransferStateFailed, Error: "insufficient_funds", Message: "source wallet does not have enough balance for this transfer"},
		err:    model.ErrInsufficientFunds,
	}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	reqBody := `{"idempotencyKey":"k-2","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":500}`
	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "insufficient_funds")
}

func TestCreateTransferReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{
		result: model.TransferResult{ID: "tx-99", State: model.TransferStateProcessed, IdempotencyKey: "k-dup", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 15, Replayed: true},
	}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	reqBody := `{"idempotencyKey":"k-dup","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":15}`
	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "tx-99")
}

func TestCreateTransferValidationError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{err: model.ErrInvalidRequest}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	reqBody := `{"idempotencyKey":"","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":10}`
	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateTransferHidesInternalErrorDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	fake := &fakeTransferService{err: errors.New("database credentials rejected")}
	r.POST("/transfers", NewHandler(fake).CreateTransfer)

	reqBody := `{"idempotencyKey":"k-3","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":10}`
	req := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "database credentials rejected")
}
