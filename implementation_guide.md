# Implementation Guide

## Scope

This implementation is a synchronous Go HTTP service using Gin and PostgreSQL. It demonstrates transactional wallet transfers, API-level idempotency, and a double-entry ledger. It is an assignment/demo implementation, not a complete payment platform.

## Architecture

```text
HTTP client
    |
    v
internal/handler       HTTP binding and response mapping
    |
    v
internal/service       request validation and orchestration
    |
    v
service.TransferRepository interface
    |
    v
internal/repository    PostgreSQL transactions and persistence
    |
    v
internal/db            connection setup and initial schema
```

- The handler owns HTTP parsing and status-code mapping.
- The service owns request validation and orchestration.
- The repository owns persistence and transaction behavior.
- Shared request/result types and domain errors live in `internal/model`.
- `main.go` wires the concrete repository into the service and HTTP handler.

## Persistence

The application creates the following tables at startup if they do not exist:

- `wallets`: integer balance and timestamps
- `transfers`: transfer request data, globally unique idempotency key, and state
- `transfer_state_transitions`: ordered transition history
- `ledger_entries`: debit/credit records linked to a transfer and wallet
- `idempotency_records`: original JSON response linked to the idempotency key and transfer

Amounts are positive `BIGINT` values in the smallest currency unit. The implementation does not model currency codes or fractional amounts. Schema creation is currently performed by `InitSchema`; a versioned migration system is not included.

## Transfer and idempotency behavior

For each request, the service validates the key, wallet IDs, distinct source/destination, and positive amount. The repository then:

1. Starts a `READ COMMITTED` database transaction and looks for a completed idempotency response.
2. Locks the source and destination wallet rows in stable ID order and checks their balances.
3. Claims the idempotency key by inserting a `PENDING` transfer with `ON CONFLICT (idempotency_key) DO NOTHING RETURNING id` and records the initial state.
4. If another transaction already claimed the key, reads and returns that transaction's committed response without applying side effects.
5. For sufficient funds, updates both balances, inserts one debit and one credit ledger entry, then performs a guarded `PENDING → PROCESSED` update and records the transition.
6. For insufficient funds, performs a guarded `PENDING → FAILED` update and records the transition without changing balances or creating ledger entries.
7. Persists the response for replay and commits all changes together; any persistence failure rolls back the transaction and state history.

The unique key prevents a second transfer for a previously used key. A client must use a new key for a distinct operation. Reusing a key returns the original result, even if the retry's payload differs; clients should therefore keep each key associated with one logical request.

## Concurrency and consistency

Wallet row locks serialize transfers involving the same wallet. Under PostgreSQL `READ COMMITTED`, a waiting `SELECT ... FOR UPDATE` observes the current committed wallet row before the balance check. The balance changes, transfer, ledger entries, and idempotency response are committed atomically.

Processed transfers create exactly two matching ledger rows in application logic. Insufficient-funds results create none. The database schema constrains row types and uniqueness, but does not independently enforce the two-row ledger invariant with a database constraint.

The endpoint is synchronous. `PENDING` is inserted first and guarded terminal updates enforce the allowed `PENDING → PROCESSED` or `PENDING → FAILED` transition. State history is committed with the transfer. Since the whole operation uses one transaction, other database clients do not see the uncommitted intermediate row state; after commit they can inspect its recorded transition history.

## API contract

`POST /transfers` accepts:

```json
{
  "idempotencyKey": "abc123",
  "fromWalletId": "wallet_1",
  "toWalletId": "wallet_2",
  "amount": 100
}
```

Successful new transfers return `201 Created`; successful replays return `200 OK`. Invalid input returns `400`, unknown wallets return `404`, insufficient funds return `409`, and unexpected errors return `500`.

## Testing strategy and known gaps

`go test ./...` runs unit tests without PostgreSQL. Integration tests in `internal/repository` cover actual Postgres state history, ledger and balance consistency, concurrent overdraw prevention, and concurrent same-key retries. Set `TEST_DATABASE_URL` to a disposable PostgreSQL database to run them; tests create and remove isolated schemas. These tests still do not replace production operational validation, migrations, or deployment safeguards.
