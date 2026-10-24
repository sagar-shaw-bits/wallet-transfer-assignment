package service

import (
	"context"
	"errors"
	"fmt"

	"wallet-transfer-assignment/internal/model"
)

type TransferRepository interface {
	CreateTransfer(ctx context.Context, req model.TransferRequest) (model.TransferResult, error)
}

type TransferService interface {
	CreateTransfer(ctx context.Context, req model.TransferRequest) (model.TransferResult, error)
}

type Service struct {
	repo TransferRepository
}

func NewService(repo TransferRepository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateTransfer(ctx context.Context, req model.TransferRequest) (model.TransferResult, error) {
	if err := validateTransferRequest(req); err != nil {
		return model.TransferResult{}, err
	}

	result, err := s.repo.CreateTransfer(ctx, req)
	if err != nil {
		if errors.Is(err, model.ErrInsufficientFunds) {
			return result, err
		}
		if errors.Is(err, model.ErrWalletNotFound) {
			return result, fmt.Errorf("%w: wallet does not exist", model.ErrWalletNotFound)
		}
		if errors.Is(err, model.ErrInvalidRequest) {
			return result, err
		}
		return result, err
	}

	return result, nil
}

func validateTransferRequest(req model.TransferRequest) error {
	if req.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotencyKey is required", model.ErrInvalidRequest)
	}
	if req.FromWalletID == "" || req.ToWalletID == "" {
		return fmt.Errorf("%w: wallet identifiers are required", model.ErrInvalidRequest)
	}
	if req.FromWalletID == req.ToWalletID {
		return fmt.Errorf("%w: source and destination wallets must differ", model.ErrInvalidRequest)
	}
	if req.Amount <= 0 {
		return fmt.Errorf("%w: amount must be positive", model.ErrInvalidRequest)
	}
	return nil
}
