package model

import "errors"

var (
	ErrInsufficientFunds = errors.New("insufficient_funds")
	ErrWalletNotFound    = errors.New("wallet_not_found")
	ErrInvalidRequest    = errors.New("invalid_request")
)
