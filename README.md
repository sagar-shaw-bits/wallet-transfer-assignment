# Wallet Transfer Service

A Go HTTP service for wallet-to-wallet transfers backed by PostgreSQL. It demonstrates a layered design, transactional balance updates, a double-entry ledger, and API-level idempotent replay.

> This is an assignment/demo service, not a complete production payment platform. Production deployment would also require managed schema migrations, authentication/authorization, operational monitoring, backups, and tested recovery procedures.

## Features

- `POST /transfers` accepts a transfer request with an idempotency key.
- PostgreSQL stores wallet balances, transfer records, state-transition history, ledger entries, and the replayable response.
- A successful transfer debits and credits the two wallets and writes matching ledger entries in one transaction.
- Wallet rows are locked in stable ID order while balances are checked and updated.
- A repeated idempotency key returns the original result without applying the transfer again.
- Insufficient-funds results are persisted and replayed for the same key.
- Every completed transfer records the state history `PENDING → PROCESSED` or `PENDING → FAILED`.

## Architecture

```mermaid
flowchart LR
    Client[HTTP client] --> Handler[Handler<br/>JSON binding and HTTP mapping]
    Handler --> Service[Service<br/>validation and orchestration]
    Service --> Repo[Repository interface]
    RepoImpl[PostgreSQL repository] -. implements .-> Repo
    RepoImpl --> DB[(PostgreSQL)]
    DB --- Wallets[wallets]
    DB --- Transfers[transfers]
    DB --- Ledger[ledger_entries]
    DB --- Idempotency[idempotency_records]
```

Package responsibilities:

| Package | Responsibility |
| --- | --- |
| `internal/handler` | HTTP binding, invoking the service, and mapping results/errors to status codes |
| `internal/service` | Request validation and transfer orchestration through an interface |
| `internal/repository` | PostgreSQL transactions, row locks, balance updates, ledger, and idempotency persistence |
| `internal/model` | Transfer request/result types and shared domain errors |
| `internal/db` | PostgreSQL connection and initial schema creation |
| `main.go` | Dependency wiring and HTTP server startup |

## Requirements

- Go version supported by the `go` directive in [`go.mod`](./go.mod)
- PostgreSQL running locally or remotely
- `psql` for the optional command-line wallet setup (a PostgreSQL GUI can also be used)

## Run locally (Windows)

### 1. Create the PostgreSQL database

Create a database named `wallet_transfer` using pgAdmin or `psql`. For example, from PowerShell (replace the username as needed):

```powershell
psql -h localhost -p 5432 -U postgres -d postgres -W
```

At the `psql` prompt:

```sql
CREATE DATABASE wallet_transfer;
\q
```

The app creates its tables at startup; it does not create the database itself.

### 2. Configure the database connection

Open PowerShell in the repository folder:

```powershell
cd D:\Study\GoProgram\wallet-transfer-assignment
$env:DATABASE_URL = "postgres://postgres:YOUR_PASSWORD@localhost:5432/wallet_transfer?sslmode=disable"
```

Replace `YOUR_PASSWORD` with the password for that PostgreSQL user. If the password contains URL-reserved characters (for example `@`, `:`, `/`, or `#`), URL-encode those characters in the connection string.

### 3. Start the API

```powershell
go run .
```

The server listens on `http://localhost:8080`. Keep this terminal open while using the API. To use another port, set it before starting the server:

```powershell
$env:PORT = "9090"
go run .
```

### 4. Create two wallets

In a second PowerShell window, connect to the same database and create wallets:

```powershell
psql -h localhost -p 5432 -U postgres -d wallet_transfer -W -c "INSERT INTO wallets (id, balance) VALUES ('wallet_1', 1000), ('wallet_2', 1000);"
```

Wallets are not created by the API. If these IDs already exist, choose different IDs for the request or add funds through your database client.

### 5. Send a transfer

In PowerShell:

```powershell
$body = @{
    idempotencyKey = "abc123"
    fromWalletId   = "wallet_1"
    toWalletId     = "wallet_2"
    amount         = 100
} | ConvertTo-Json

Invoke-RestMethod -Method Post `
    -Uri "http://localhost:8080/transfers" `
    -ContentType "application/json" `
    -Body $body
```

In Git Bash, use `curl` instead:

```bash
curl -X POST "http://localhost:8080/transfers" \
  -H "Content-Type: application/json" \
  -d '{"idempotencyKey":"abc123","fromWalletId":"wallet_1","toWalletId":"wallet_2","amount":100}'
```

The first successful request returns `201 Created`. Retrying with the same idempotency key returns the saved result and does not transfer funds again. Use a new key for a new transfer.

## API

### `POST /transfers`

Request:

```json
{
  "idempotencyKey": "abc123",
  "fromWalletId": "wallet_1",
  "toWalletId": "wallet_2",
  "amount": 100
}
```

`amount` is a positive integer in the smallest currency unit (for example, cents); currency and fractional amounts are not currently modeled.

| Response | Meaning |
| --- | --- |
| `201 Created` | New transfer processed |
| `200 OK` | Previously successful result replayed for the idempotency key |
| `400 Bad Request` | Malformed or invalid request |
| `404 Not Found` | Source or destination wallet does not exist |
| `409 Conflict` | Insufficient funds; this result is also saved and a retry replays the same failure |
| `500 Internal Server Error` | Unexpected server or database error |

The idempotency key is globally unique in the current schema. A retry with an already-used key replays its original result; clients should use a new key for a distinct operation.

## Persistence and consistency

The application initializes these tables on startup:

- `wallets`: current integer balance per wallet
- `transfers`: request details and final state
- `transfer_state_transitions`: ordered state history for each transfer
- `ledger_entries`: one `DEBIT` and one `CREDIT` row for each processed transfer
- `idempotency_records`: persisted response associated with the idempotency key

The repository uses a PostgreSQL `READ COMMITTED` transaction and locks both wallet rows with `SELECT ... FOR UPDATE`, ordered by wallet ID. This serializes conflicting balance updates while avoiding the need for serializable-transaction retries. A unique idempotency constraint plus `INSERT ... ON CONFLICT DO NOTHING RETURNING` handles concurrent requests sharing a key; the losing request reads the committed response. Balance changes, ledger entries, transfer state, transition history, and replay response commit atomically.

Each transfer row is inserted as `PENDING`, then moved to exactly one terminal state using a guarded update that requires the current state to be `PENDING`. The ordered state history is persisted in `transfer_state_transitions` in the same transaction. Other database clients cannot observe an in-flight `PENDING` record because this endpoint is synchronous and the transaction is atomic; the transition history records the lifecycle after commit. A failed transaction rolls back both the transfer and its history.

Failed insufficient-funds transfers do not change wallet balances or create ledger entries. They do persist a failed transfer and response for idempotent replay.

## Tests

Run unit tests from the repository folder:

```powershell
go test ./...
```

Unit tests use mocks and do not require PostgreSQL. PostgreSQL integration tests verify persisted transition history, balances and ledger entries; concurrent requests cannot overdraw a wallet; and concurrent calls with the same idempotency key perform only one transfer.

To run the database integration tests, point `TEST_DATABASE_URL` at a **disposable PostgreSQL database**. Each test creates and drops a uniquely named schema within that database:

```powershell
$env:TEST_DATABASE_URL = "postgres://postgres:YOUR_PASSWORD@localhost:5432/wallet_transfer_test?sslmode=disable"
go test ./internal/repository -run Postgres -count=1
```

The integration tests are skipped when `TEST_DATABASE_URL` is unset. The configured PostgreSQL role must be allowed to create and drop schemas. Do not point this variable at a database whose data must be preserved; the tests create and drop their own schemas.
