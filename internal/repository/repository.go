package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/model"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateTransfer(ctx context.Context, req model.TransferRequest) (model.TransferResult, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return model.TransferResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if existing, ok, err := getExistingResponseTx(ctx, tx, req.IdempotencyKey); err != nil {
		return model.TransferResult{}, err
	} else if ok {
		if err := tx.Commit(); err != nil {
			return model.TransferResult{}, err
		}
		return existing, nil
	}

	walletBalances, err := loadWalletBalancesTx(ctx, tx, req.FromWalletID, req.ToWalletID)
	if err != nil {
		return model.TransferResult{}, err
	}
	if _, exists := walletBalances[req.FromWalletID]; !exists {
		return model.TransferResult{}, model.ErrWalletNotFound
	}
	if _, exists := walletBalances[req.ToWalletID]; !exists {
		return model.TransferResult{}, model.ErrWalletNotFound
	}

	fromBalance := walletBalances[req.FromWalletID]
	now := time.Now().UTC()
	result := model.TransferResult{
		ID:             uuid.NewString(),
		State:          model.TransferStatePending,
		IdempotencyKey: req.IdempotencyKey,
		FromWalletID:   req.FromWalletID,
		ToWalletID:     req.ToWalletID,
		Amount:         req.Amount,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := insertTransferTx(ctx, tx, result); err != nil {
		if errors.Is(err, errIdempotencyKeyConflict) {
			return replayTransferTx(ctx, tx, req.IdempotencyKey)
		}
		return model.TransferResult{}, err
	}
	if err := recordTransferStateTx(ctx, tx, result.ID, model.TransferStatePending, now); err != nil {
		return model.TransferResult{}, err
	}

	if fromBalance < req.Amount {
		result = failedTransferResult(result, time.Now().UTC())
		if err := transitionTransferTx(ctx, tx, result.ID, model.TransferStatePending, model.TransferStateFailed, result.UpdatedAt); err != nil {
			return model.TransferResult{}, err
		}

		if err := persistIdempotencyRecordTx(ctx, tx, req.IdempotencyKey, result.ID, result, now); err != nil {
			return model.TransferResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return model.TransferResult{}, err
		}
		return result, model.ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET balance = balance - $1, updated_at = $2 WHERE id = $3`, req.Amount, now, req.FromWalletID,
	); err != nil {
		return model.TransferResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET balance = balance + $1, updated_at = $2 WHERE id = $3`, req.Amount, now, req.ToWalletID,
	); err != nil {
		return model.TransferResult{}, err
	}

	debitID := uuid.NewString()
	creditID := uuid.NewString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount) VALUES ($1, $2, $3, $4, $5)`,
		debitID, result.ID, req.FromWalletID, model.LedgerTypeDebit, req.Amount,
	); err != nil {
		return model.TransferResult{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount) VALUES ($1, $2, $3, $4, $5)`,
		creditID, result.ID, req.ToWalletID, model.LedgerTypeCredit, req.Amount,
	); err != nil {
		return model.TransferResult{}, err
	}

	result.State = model.TransferStateProcessed
	result.UpdatedAt = time.Now().UTC()
	if err := transitionTransferTx(ctx, tx, result.ID, model.TransferStatePending, model.TransferStateProcessed, result.UpdatedAt); err != nil {
		return model.TransferResult{}, err
	}

	if err := persistIdempotencyRecordTx(ctx, tx, req.IdempotencyKey, result.ID, result, now); err != nil {
		return model.TransferResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.TransferResult{}, err
	}
	return result, nil
}

var errIdempotencyKeyConflict = errors.New("idempotency key already claimed")

func insertTransferTx(ctx context.Context, tx *sql.Tx, result model.TransferResult) error {
	var insertedID string
	err := tx.QueryRowContext(ctx,
		`INSERT INTO transfers (id, idempotency_key, from_wallet_id, to_wallet_id, amount, state, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (idempotency_key) DO NOTHING
		 RETURNING id`,
		result.ID, result.IdempotencyKey, result.FromWalletID, result.ToWalletID,
		result.Amount, result.State, result.CreatedAt, result.UpdatedAt,
	).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		return errIdempotencyKeyConflict
	}
	return err
}

func recordTransferStateTx(ctx context.Context, tx *sql.Tx, transferID, state string, at time.Time) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 1, $2, $3)`,
		transferID, state, at,
	)
	return err
}

func transitionTransferTx(ctx context.Context, tx *sql.Tx, transferID, fromState, toState string, at time.Time) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE transfers SET state = $1, updated_at = $2 WHERE id = $3 AND state = $4`,
		toState, at, transferID, fromState,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("transfer %q cannot transition from %s to %s", transferID, fromState, toState)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 2, $2, $3)`,
		transferID, toState, at,
	)
	return err
}

func replayTransferTx(ctx context.Context, tx *sql.Tx, key string) (model.TransferResult, error) {
	existing, ok, err := getExistingResponseTx(ctx, tx, key)
	if err != nil {
		return model.TransferResult{}, err
	}
	if !ok {
		return model.TransferResult{}, fmt.Errorf("idempotency key %q exists without a replay record", key)
	}
	if err := tx.Commit(); err != nil {
		return model.TransferResult{}, err
	}
	return existing, nil
}

func failedTransferResult(result model.TransferResult, now time.Time) model.TransferResult {
	result.State = model.TransferStateFailed
	result.Error = "insufficient_funds"
	result.Message = "source wallet does not have enough balance for this transfer"
	result.CreatedAt = now
	result.UpdatedAt = now
	return result
}

func loadWalletBalancesTx(ctx context.Context, tx *sql.Tx, walletIDs ...string) (map[string]int64, error) {
	if len(walletIDs) != 2 {
		return nil, fmt.Errorf("%w: expected exactly two wallet IDs", model.ErrInvalidRequest)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, balance FROM wallets WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`, walletIDs[0], walletIDs[1])
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	balances := map[string]int64{}
	for rows.Next() {
		var id string
		var balance int64
		if err := rows.Scan(&id, &balance); err != nil {
			return nil, err
		}
		balances[id] = balance
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return balances, nil
}

func persistIdempotencyRecordTx(ctx context.Context, tx *sql.Tx, key, transferID string, result model.TransferResult, now time.Time) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO idempotency_records (idempotency_key, transfer_id, response_json, created_at)
		 VALUES ($1, $2, $3::jsonb, $4)`,
		key, transferID, string(payload), now,
	)
	return err
}

func getExistingResponseTx(ctx context.Context, tx *sql.Tx, key string) (model.TransferResult, bool, error) {
	var responseJSON string
	row := tx.QueryRowContext(ctx, `SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`, key)
	if err := row.Scan(&responseJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.TransferResult{}, false, nil
		}
		return model.TransferResult{}, false, err
	}

	var result model.TransferResult
	if err := json.Unmarshal([]byte(responseJSON), &result); err != nil {
		return model.TransferResult{}, false, fmt.Errorf("decode idempotency response: %w", err)
	}
	result.Replayed = true
	return result, true, nil
}
