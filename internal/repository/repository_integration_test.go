package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"wallet-transfer-assignment/internal/db"
	"wallet-transfer-assignment/internal/model"
)

func TestPostgresTransferPersistsStateLifecycleAndLedger(t *testing.T) {
	repo, conn := newPostgresIntegrationRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fromID, toID := uuid.NewString(), uuid.NewString()
	insertWallets(t, ctx, conn, fromID, 100, toID, 0)

	processed, err := repo.CreateTransfer(ctx, model.TransferRequest{
		IdempotencyKey: uuid.NewString(),
		FromWalletID:   fromID,
		ToWalletID:     toID,
		Amount:         40,
	})
	require.NoError(t, err)
	require.Equal(t, model.TransferStateProcessed, processed.State)
	assertTransferStates(t, ctx, conn, processed.ID, model.TransferStatePending, model.TransferStateProcessed)

	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = transitionTransferTx(ctx, tx, processed.ID, model.TransferStatePending, model.TransferStateFailed, time.Now().UTC())
	require.Error(t, err, "a terminal transfer must not transition from PENDING again")
	require.NoError(t, tx.Rollback())
	assertTransferStates(t, ctx, conn, processed.ID, model.TransferStatePending, model.TransferStateProcessed)

	failed, err := repo.CreateTransfer(ctx, model.TransferRequest{
		IdempotencyKey: uuid.NewString(),
		FromWalletID:   fromID,
		ToWalletID:     toID,
		Amount:         100,
	})
	require.ErrorIs(t, err, model.ErrInsufficientFunds)
	require.Equal(t, model.TransferStateFailed, failed.State)
	assertTransferStates(t, ctx, conn, failed.ID, model.TransferStatePending, model.TransferStateFailed)

	var debitCount, creditCount int64
	err = conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FILTER (WHERE type = 'DEBIT'), COUNT(*) FILTER (WHERE type = 'CREDIT')
		 FROM ledger_entries WHERE transfer_id = $1`,
		processed.ID,
	).Scan(&debitCount, &creditCount)
	require.NoError(t, err)
	require.EqualValues(t, 1, debitCount)
	require.EqualValues(t, 1, creditCount)

	err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries WHERE transfer_id = $1`, failed.ID).Scan(&debitCount)
	require.NoError(t, err)
	require.Zero(t, debitCount, "failed transfer must not create ledger entries")

	assertWalletBalance(t, ctx, conn, fromID, 60)
	assertWalletBalance(t, ctx, conn, toID, 40)
}

func TestPostgresConcurrentTransfersDoNotOverdrawWallet(t *testing.T) {
	repo, conn := newPostgresIntegrationRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const (
		initialBalance = int64(100)
		amount         = int64(10)
		transferCount  = 20
	)
	fromID, toID := uuid.NewString(), uuid.NewString()
	insertWallets(t, ctx, conn, fromID, initialBalance, toID, 0)

	type outcome struct {
		result model.TransferResult
		err    error
	}
	outcomes := make(chan outcome, transferCount)
	var wg sync.WaitGroup
	for i := 0; i < transferCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := repo.CreateTransfer(ctx, model.TransferRequest{
				IdempotencyKey: uuid.NewString(),
				FromWalletID:   fromID,
				ToWalletID:     toID,
				Amount:         amount,
			})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)

	processedCount := 0
	failedCount := 0
	for outcome := range outcomes {
		switch {
		case outcome.err == nil:
			require.Equal(t, model.TransferStateProcessed, outcome.result.State)
			processedCount++
		case isInsufficientFunds(outcome.err):
			require.Equal(t, model.TransferStateFailed, outcome.result.State)
			failedCount++
		default:
			require.NoError(t, outcome.err)
		}
	}

	require.Equal(t, 10, processedCount)
	require.Equal(t, 10, failedCount)
	assertWalletBalance(t, ctx, conn, fromID, 0)
	assertWalletBalance(t, ctx, conn, toID, initialBalance)

	var processedTransfers, failedTransfers, ledgerEntries, transitions int64
	require.NoError(t, conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FILTER (WHERE state = 'PROCESSED'), COUNT(*) FILTER (WHERE state = 'FAILED') FROM transfers`,
	).Scan(&processedTransfers, &failedTransfers))
	require.EqualValues(t, processedCount, processedTransfers)
	require.EqualValues(t, failedCount, failedTransfers)

	require.NoError(t, conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM ledger_entries`).Scan(&ledgerEntries))
	require.EqualValues(t, processedCount*2, ledgerEntries)
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM transfer_state_transitions`).Scan(&transitions))
	require.EqualValues(t, transferCount*2, transitions)
}

func TestPostgresConcurrentSameIdempotencyKeyTransfersOnce(t *testing.T) {
	repo, conn := newPostgresIntegrationRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	fromID, toID := uuid.NewString(), uuid.NewString()
	insertWallets(t, ctx, conn, fromID, 1000, toID, 0)
	request := model.TransferRequest{
		IdempotencyKey: uuid.NewString(),
		FromWalletID:   fromID,
		ToWalletID:     toID,
		Amount:         125,
	}

	const requests = 16
	results := make(chan model.TransferResult, requests)
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := repo.CreateTransfer(ctx, request)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	originalResults := 0
	for result := range results {
		require.Equal(t, model.TransferStateProcessed, result.State)
		if !result.Replayed {
			originalResults++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, originalResults, "one caller should perform the transfer")

	assertWalletBalance(t, ctx, conn, fromID, 875)
	assertWalletBalance(t, ctx, conn, toID, 125)
	assertTableRowCount(t, ctx, conn, "transfers", 1)
	assertTableRowCount(t, ctx, conn, "ledger_entries", 2)
	assertTableRowCount(t, ctx, conn, "idempotency_records", 1)
	assertTableRowCount(t, ctx, conn, "transfer_state_transitions", 2)
}

func newPostgresIntegrationRepository(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	testDSN := os.Getenv("TEST_DATABASE_URL")
	if testDSN == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database to run integration tests")
	}

	parsedDSN, err := url.Parse(testDSN)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, parsedDSN.Scheme, "TEST_DATABASE_URL must use a PostgreSQL URL")

	admin, err := db.Open(testDSN)
	require.NoError(t, err)
	schema := "wallet_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.ExecContext(context.Background(), `CREATE SCHEMA `+schema)
	require.NoError(t, err)

	query := parsedDSN.Query()
	query.Set("search_path", schema)
	parsedDSN.RawQuery = query.Encode()
	conn, err := db.Open(parsedDSN.String())
	require.NoError(t, err)
	require.NoError(t, db.InitSchema(conn))

	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop integration-test schema: %v", err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close admin database connection: %v", err)
		}
	})
	return NewRepository(conn), conn
}

func insertWallets(t *testing.T, ctx context.Context, conn *sql.DB, fromID string, fromBalance int64, toID string, toBalance int64) {
	t.Helper()
	_, err := conn.ExecContext(ctx,
		`INSERT INTO wallets (id, balance) VALUES ($1, $2), ($3, $4)`,
		fromID, fromBalance, toID, toBalance,
	)
	require.NoError(t, err)
}

func assertWalletBalance(t *testing.T, ctx context.Context, conn *sql.DB, walletID string, want int64) {
	t.Helper()
	var got int64
	err := conn.QueryRowContext(ctx, `SELECT balance FROM wallets WHERE id = $1`, walletID).Scan(&got)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func assertTransferStates(t *testing.T, ctx context.Context, conn *sql.DB, transferID string, want ...string) {
	t.Helper()
	rows, err := conn.QueryContext(ctx,
		`SELECT state FROM transfer_state_transitions WHERE transfer_id = $1 ORDER BY transition_number`,
		transferID,
	)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var state string
		require.NoError(t, rows.Scan(&state))
		got = append(got, state)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, want, got)
}

func assertTableRowCount(t *testing.T, ctx context.Context, conn *sql.DB, table string, want int64) {
	t.Helper()
	var got int64
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&got))
	require.Equal(t, want, got)
}

func isInsufficientFunds(err error) bool {
	return errors.Is(err, model.ErrInsufficientFunds)
}
