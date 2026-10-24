package db

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func DefaultDSN() string {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://postgres:postgres@localhost:5432/wallet_transfer?sslmode=disable"
}

func Open(dsn string) (*sql.DB, error) {
	if dsn == "" {
		dsn = DefaultDSN()
	}

	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(25)

	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func InitSchema(conn *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS wallets (
            id TEXT PRIMARY KEY,
            balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`,
		`CREATE TABLE IF NOT EXISTS transfers (
            id TEXT PRIMARY KEY,
            idempotency_key TEXT NOT NULL UNIQUE,
            from_wallet_id TEXT NOT NULL,
            to_wallet_id TEXT NOT NULL,
            amount BIGINT NOT NULL CHECK (amount > 0),
            state TEXT NOT NULL CHECK (state IN ('PENDING', 'PROCESSED', 'FAILED')),
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            FOREIGN KEY (from_wallet_id) REFERENCES wallets(id),
            FOREIGN KEY (to_wallet_id) REFERENCES wallets(id)
        )`,
		`CREATE TABLE IF NOT EXISTS transfer_state_transitions (
            transfer_id TEXT NOT NULL,
            transition_number SMALLINT NOT NULL,
            state TEXT NOT NULL CHECK (state IN ('PENDING', 'PROCESSED', 'FAILED')),
            created_at TIMESTAMPTZ NOT NULL,
            CHECK (transition_number IN (1, 2)),
            CHECK (
            	(transition_number = 1 AND state = 'PENDING') OR
            	(transition_number = 2 AND state IN ('PROCESSED', 'FAILED'))
            ),
            PRIMARY KEY (transfer_id, transition_number),
            FOREIGN KEY (transfer_id) REFERENCES transfers(id)
        )`,
		`CREATE TABLE IF NOT EXISTS ledger_entries (
            id TEXT PRIMARY KEY,
            transfer_id TEXT NOT NULL,
            wallet_id TEXT NOT NULL,
            type TEXT NOT NULL CHECK (type IN ('DEBIT', 'CREDIT')),
            amount BIGINT NOT NULL CHECK (amount > 0),
            UNIQUE(transfer_id, wallet_id, type),
            FOREIGN KEY (transfer_id) REFERENCES transfers(id),
            FOREIGN KEY (wallet_id) REFERENCES wallets(id)
        )`,
		`CREATE TABLE IF NOT EXISTS idempotency_records (
            idempotency_key TEXT PRIMARY KEY,
            transfer_id TEXT NOT NULL,
            response_json JSONB NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            FOREIGN KEY (transfer_id) REFERENCES transfers(id)
        )`,
		`CREATE INDEX IF NOT EXISTS idx_idempotency_records_transfer_id ON idempotency_records(transfer_id)`,
	}

	for _, stmt := range statements {
		if _, err := conn.Exec(stmt); err != nil {
			return fmt.Errorf("executing schema statement %q: %w", stmt, err)
		}
	}
	return nil
}
