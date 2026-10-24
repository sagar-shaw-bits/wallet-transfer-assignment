## Summary

This submission will implement a wallet transfer service in Go using PostgreSQL and Gin. The solution is designed around a transactional transfer workflow with a double-entry ledger, strict idempotency handling, and concurrency-safe wallet updates.

The planned approach is to keep responsibilities cleanly separated:

- Handler layer: HTTP validation and request mapping
- Service layer: transfer orchestration, business rules, idempotency handling
- Repository layer: database access and transaction execution
- Model layer: transfer, wallet, and ledger domain objects and validation

The primary target is to ensure that duplicate requests with the same idempotency key return the original result without creating a second transfer, while wallet balances stay correct even when multiple transfer requests compete for the same wallet.

## AI disclosure

This plan was prepared with the assistance of Copilot SDK in VS Code.

1. Tool used: Copilot SDK in VS Code
2. How it is generally used: to review the assignment, reason through the required architecture and concurrency constraints, and draft the approach before implementation.
3. Prompts used: the task was to review the assignment requirements and outline a robust implementation strategy for a wallet transfer service with idempotency, ledger integrity, and concurrency safety.

## Schema Design

Planned tables:

- wallets
  - id (UUID primary key)
  - balance (NUMERIC, non-negative)
  - created_at / updated_at

- transfers
  - id (UUID primary key)
  - idempotency_key (unique)
  - from_wallet_id / to_wallet_id (FKs)
  - amount (positive numeric)
  - state (PENDING, PROCESSED, FAILED)
  - created_at / updated_at

- ledger_entries
  - id (UUID primary key)
  - transfer_id (FK)
  - wallet_id (FK)
  - type (DEBIT or CREDIT)
  - amount (positive numeric)
  - unique constraint on (transfer_id, wallet_id, type)

- idempotency_records
  - idempotency_key (primary key)
  - transfer_id (FK)
  - response_body (JSONB)
  - created_at

The design intentionally enforces unique idempotency keys and ensures that every transfer creates exactly two ledger rows: one debit and one credit.

## Idempotency Strategy

The system will store a unique idempotency key for every transfer request. The handler will validate and map the request before invoking the service. The service will check the idempotency record and return the original response for a completed request instead of creating a second transfer.

Implementation plan:

- enforce uniqueness on transfers.idempotency_key or on a dedicated idempotency table
- persist the stored response body for the original successful (or failed) request in the same transaction as the transfer side effects
- have the service perform the replay decision and conflict recovery after the database has accepted or rejected the unique idempotency claim
- ensure that retries do not produce duplicate ledger rows or rebalance wallet funds
- treat the idempotency key as the durable source of truth for request replay

## Concurrency Strategy

The transfer workflow will execute inside a single database transaction. Both wallet rows involved in the transfer will be locked with a row-level lock before any balance check or update is performed.

This prevents cases where two concurrent transfers debit the same wallet at the same time and both read the same balance before one writes. The final behavior will be:

- safe balance checks
- no double spending
- consistent ledger entries
- atomic commit or rollback for the whole transfer

The transaction will update both wallet balances and insert both ledger rows as a single atomic unit.

## How to Run

- Setup PostgreSQL
- Configure environment variables for database connection
- Run migrations
- Start the Gin server
- Send requests to POST /transfers

## How to Test

- success path for a valid transfer
- duplicate idempotency key returns original result
- insufficient funds failure leaves balances unchanged
- ledger entries are balanced for every transfer
- concurrent transfers on the same wallet do not overdraw funds
- state transitions remain safe and valid

## Tradeoffs / Assumptions

- PostgreSQL is the preferred database and will be used for the final implementation.
- Wallet balances will be stored and updated transactionally; the ledger remains the source of truth for auditability.
- API semantics will be exactly-once for requests sharing the same idempotency key.
- Validation errors will fail fast before starting the transfer workflow.

## Checklist

- [ ] Tests pass
- [ ] Lint passes
- [ ] Format check passes
- [ ] README or notes updated
- [ ] PR description explains schema, idempotency, and concurrency
