package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"wallet-transfer-assignment/internal/model"
	"wallet-transfer-assignment/internal/repository"
)

func TestServiceCreateTransferRejectsInvalidRequest(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	svc := NewService(repository.NewRepository(db))
	_, err = svc.CreateTransfer(context.Background(), model.TransferRequest{IdempotencyKey: "", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 10})
	assert.ErrorIs(t, err, model.ErrInvalidRequest)
}

func TestServiceCreateTransferValidRequest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	service := NewService(repository.NewRepository(db))
	request := model.TransferRequest{IdempotencyKey: "k-1", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 100}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`)).
		WithArgs("k-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, balance FROM wallets WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`)).
		WithArgs("wallet_1", "wallet_2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow("wallet_1", int64(1000)).AddRow("wallet_2", int64(1000)))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO transfers (id, idempotency_key, from_wallet_id, to_wallet_id, amount, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`)).
		WithArgs(sqlmock.AnyArg(), "k-1", "wallet_1", "wallet_2", int64(100), model.TransferStatePending, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("tx-1"))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 1, $2, $3)`)).
		WithArgs(sqlmock.AnyArg(), model.TransferStatePending, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE wallets SET balance = balance - $1, updated_at = $2 WHERE id = $3`)).
		WithArgs(int64(100), sqlmock.AnyArg(), "wallet_1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE wallets SET balance = balance + $1, updated_at = $2 WHERE id = $3`)).
		WithArgs(int64(100), sqlmock.AnyArg(), "wallet_2").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount) VALUES ($1, $2, $3, $4, $5)`)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "wallet_1", model.LedgerTypeDebit, int64(100)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount) VALUES ($1, $2, $3, $4, $5)`)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "wallet_2", model.LedgerTypeCredit, int64(100)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE transfers SET state = $1, updated_at = $2 WHERE id = $3 AND state = $4`)).
		WithArgs(model.TransferStateProcessed, sqlmock.AnyArg(), sqlmock.AnyArg(), model.TransferStatePending).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 2, $2, $3)`)).
		WithArgs(sqlmock.AnyArg(), model.TransferStateProcessed, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO idempotency_records (idempotency_key, transfer_id, response_json, created_at) VALUES ($1, $2, $3::jsonb, $4)`)).
		WithArgs("k-1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := service.CreateTransfer(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateProcessed, result.State)
	assert.EqualValues(t, 100, result.Amount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceCreateTransferDuplicateKeyReturnsOriginalResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	service := NewService(repository.NewRepository(db))
	request := model.TransferRequest{IdempotencyKey: "k-dup", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 50}

	payload, err := json.Marshal(model.TransferResult{
		ID:             "tx-1",
		State:          model.TransferStateProcessed,
		IdempotencyKey: "k-dup",
		FromWalletID:   "wallet_1",
		ToWalletID:     "wallet_2",
		Amount:         50,
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`)).
		WithArgs("k-dup").
		WillReturnRows(sqlmock.NewRows([]string{"response_json::text"}).AddRow(string(payload)))
	mock.ExpectCommit()

	result, err := service.CreateTransfer(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, result.Replayed)
	assert.Equal(t, "tx-1", result.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceCreateTransferRecoversConcurrentIdempotencyConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	service := NewService(repository.NewRepository(db))
	request := model.TransferRequest{IdempotencyKey: "k-race", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 50}
	payload, err := json.Marshal(model.TransferResult{
		ID:             "tx-winner",
		State:          model.TransferStateProcessed,
		IdempotencyKey: "k-race",
		FromWalletID:   "wallet_1",
		ToWalletID:     "wallet_2",
		Amount:         50,
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`)).
		WithArgs("k-race").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, balance FROM wallets WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`)).
		WithArgs("wallet_1", "wallet_2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow("wallet_1", int64(1000)).AddRow("wallet_2", int64(1000)))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO transfers (id, idempotency_key, from_wallet_id, to_wallet_id, amount, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`)).
		WithArgs(sqlmock.AnyArg(), "k-race", "wallet_1", "wallet_2", int64(50), model.TransferStatePending, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`)).
		WithArgs("k-race").
		WillReturnRows(sqlmock.NewRows([]string{"response_json::text"}).AddRow(string(payload)))
	mock.ExpectCommit()

	result, err := service.CreateTransfer(context.Background(), request)
	require.NoError(t, err)
	assert.True(t, result.Replayed)
	assert.Equal(t, "tx-winner", result.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestServiceCreateTransferFailsWhenFundsAreInsufficient(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	service := NewService(repository.NewRepository(db))
	request := model.TransferRequest{IdempotencyKey: "k-low", FromWalletID: "wallet_1", ToWalletID: "wallet_2", Amount: 500}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT response_json::text FROM idempotency_records WHERE idempotency_key = $1`)).
		WithArgs("k-low").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, balance FROM wallets WHERE id IN ($1, $2) ORDER BY id FOR UPDATE`)).
		WithArgs("wallet_1", "wallet_2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow("wallet_1", int64(100)).AddRow("wallet_2", int64(1000)))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO transfers (id, idempotency_key, from_wallet_id, to_wallet_id, amount, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`)).
		WithArgs(sqlmock.AnyArg(), "k-low", "wallet_1", "wallet_2", int64(500), model.TransferStatePending, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("tx-2"))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 1, $2, $3)`)).
		WithArgs(sqlmock.AnyArg(), model.TransferStatePending, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE transfers SET state = $1, updated_at = $2 WHERE id = $3 AND state = $4`)).
		WithArgs(model.TransferStateFailed, sqlmock.AnyArg(), sqlmock.AnyArg(), model.TransferStatePending).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO transfer_state_transitions (transfer_id, transition_number, state, created_at) VALUES ($1, 2, $2, $3)`)).
		WithArgs(sqlmock.AnyArg(), model.TransferStateFailed, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO idempotency_records (idempotency_key, transfer_id, response_json, created_at) VALUES ($1, $2, $3::jsonb, $4)`)).
		WithArgs("k-low", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := service.CreateTransfer(context.Background(), request)
	assert.ErrorIs(t, err, model.ErrInsufficientFunds)
	assert.Equal(t, model.TransferStateFailed, result.State)
	assert.NoError(t, mock.ExpectationsWereMet())
}
