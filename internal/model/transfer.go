package model

import "time"

const (
	TransferStatePending   = "PENDING"
	TransferStateProcessed = "PROCESSED"
	TransferStateFailed    = "FAILED"
	LedgerTypeDebit        = "DEBIT"
	LedgerTypeCredit       = "CREDIT"
)

type TransferRequest struct {
	IdempotencyKey string `json:"idempotencyKey" binding:"required"`
	FromWalletID   string `json:"fromWalletId" binding:"required"`
	ToWalletID     string `json:"toWalletId" binding:"required"`
	Amount         int64  `json:"amount" binding:"required"`
}

type TransferResult struct {
	ID             string    `json:"id"`
	State          string    `json:"state"`
	IdempotencyKey string    `json:"idempotencyKey"`
	FromWalletID   string    `json:"fromWalletId"`
	ToWalletID     string    `json:"toWalletId"`
	Amount         int64     `json:"amount"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Error          string    `json:"error,omitempty"`
	Message        string    `json:"message,omitempty"`
	Replayed       bool      `json:"-"`
}

func (r TransferResult) IsFailed() bool {
	return r.State == TransferStateFailed || r.Error != ""
}
