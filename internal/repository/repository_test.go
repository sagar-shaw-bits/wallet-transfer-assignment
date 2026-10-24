package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"wallet-transfer-assignment/internal/model"
)

func TestInsertTransferTx(t *testing.T) {
	tests := []struct {
		name    string
		rows    *sqlmock.Rows
		wantErr error
	}{
		{
			name: "claims a new idempotency key",
			rows: sqlmock.NewRows([]string{"id"}).AddRow("transfer-1"),
		},
		{
			name:    "reports an existing idempotency key",
			rows:    sqlmock.NewRows([]string{"id"}),
			wantErr: errIdempotencyKeyConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			mock.ExpectBegin()
			tx, err := db.BeginTx(context.Background(), nil)
			require.NoError(t, err)

			result := model.TransferResult{
				ID:             "transfer-1",
				State:          model.TransferStateProcessed,
				IdempotencyKey: "key-1",
				FromWalletID:   "wallet-1",
				ToWalletID:     "wallet-2",
				Amount:         25,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}
			mock.ExpectQuery(`INSERT INTO transfers .* ON CONFLICT \(idempotency_key\) DO NOTHING RETURNING id`).
				WithArgs(result.ID, result.IdempotencyKey, result.FromWalletID, result.ToWalletID,
					result.Amount, result.State, result.CreatedAt, result.UpdatedAt).
				WillReturnRows(tt.rows)

			err = insertTransferTx(context.Background(), tx, result)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestInsertTransferTxPropagatesDatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	result := model.TransferResult{ID: "transfer-1", IdempotencyKey: "key-1"}
	dbErr := errors.New("database unavailable")
	mock.ExpectQuery(`INSERT INTO transfers .* ON CONFLICT \(idempotency_key\) DO NOTHING RETURNING id`).
		WithArgs(result.ID, result.IdempotencyKey, result.FromWalletID, result.ToWalletID,
			result.Amount, result.State, result.CreatedAt, result.UpdatedAt).
		WillReturnError(dbErr)

	err = insertTransferTx(context.Background(), tx, result)
	assert.ErrorIs(t, err, dbErr)
	assert.NoError(t, mock.ExpectationsWereMet())
}
