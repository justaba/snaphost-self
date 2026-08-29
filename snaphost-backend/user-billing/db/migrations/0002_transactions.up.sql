-- =============================================================================
-- Migration: 0002_transactions
-- Description: Creates the transactions table for recording all financial
--              events (reserves, commits, refunds, topups, bonuses) in the
--              vibecoin economy. Supports saga-pattern orchestration and
--              idempotency for payment operations.
-- Dependencies: 0001_wallets (uses public.handle_updated_at function)
-- =============================================================================

create extension if not exists pgcrypto;

-- ---------------------------------------------------------------------------
-- transactions table
-- ---------------------------------------------------------------------------

create table if not exists transactions (
    id                uuid primary key default gen_random_uuid(),
    user_id           uuid not null,
    deploy_id         uuid,
    type              text not null
                          constraint chk_transactions_type check (type in ('reserve', 'commit', 'refund', 'topup', 'bonus')),
    amount            bigint not null
                          constraint chk_transactions_amount_positive check (amount > 0),
    status            text not null default 'pending'
                          constraint chk_transactions_status check (status in ('pending', 'completed', 'cancelled', 'failed')),
    idempotency_key   text
                          constraint uq_transactions_idempotency_key unique,
    metadata          jsonb not null default '{}'::jsonb,
    created_at        timestamptz not null default now(),
    completed_at      timestamptz
);

comment on table  transactions                    is 'Immutable ledger of all financial events in the vibecoin economy. Transactions are never deleted — they form the audit trail for every balance change.';
comment on column transactions.id                 is 'Auto-generated UUID primary key for each transaction.';
comment on column transactions.user_id            is 'The user whose wallet is affected. Not a FK to wallets — transactions are retained even if the wallet is deleted for auditability.';
comment on column transactions.deploy_id          is 'Optional reference to the deploy that triggered this transaction. Null for topups and bonuses.';
comment on column transactions.type               is 'Transaction type: reserve (lock funds), commit (deduct reserved), refund (release reserved back to balance), topup (add purchased funds), bonus (add promotional funds).';
comment on column transactions.amount             is 'Transaction amount in vibecoins. Always positive — the type field determines the direction of the balance change.';
comment on column transactions.status             is 'Lifecycle status: pending → completed/cancelled/failed. Pending reserves are swept by the saga compensator.';
comment on column transactions.idempotency_key    is 'Optional unique key for external-facing operations (e.g. payment provider reference) to prevent duplicate processing. Null for internal transactions.';
comment on column transactions.metadata           is 'Flexible JSONB for storing provider transaction IDs, admin notes, refund reasons, or other context.';
comment on column transactions.created_at         is 'Timestamp when the transaction was created.';
comment on column transactions.completed_at       is 'Timestamp when the transaction reached a terminal state (completed, cancelled, or failed). Null while pending.';

-- ---------------------------------------------------------------------------
-- Indexes
-- ---------------------------------------------------------------------------

create index if not exists idx_transactions_user_id          on transactions(user_id);
create index if not exists idx_transactions_deploy_id_partial on transactions(deploy_id) where deploy_id is not null;
create index if not exists idx_transactions_status_type      on transactions(status, type);
create index if not exists idx_transactions_created_at       on transactions(created_at desc);

-- Partial unique index on idempotency_key (the unique constraint covers all rows
-- including nulls, but this explicit partial index ensures efficient lookups for
-- non-null keys only)
create unique index if not exists idx_transactions_idempotency_key_partial
    on transactions(idempotency_key)
    where idempotency_key is not null;

-- ---------------------------------------------------------------------------
-- user_transaction_summary view
-- ---------------------------------------------------------------------------

create or replace view user_transaction_summary as
select
    user_id,
    count(*)                                                                         as transaction_count,
    sum(case when type = 'commit' and status = 'completed' then amount else 0 end)   as total_spent,
    sum(case when type = 'topup'  and status = 'completed' then amount else 0 end)   as total_topped_up,
    max(created_at)                                                                  as last_transaction_at
from transactions
group by user_id;

comment on view user_transaction_summary is 'Per-user aggregate of transaction activity: total count, total vibecoins spent (committed), total topped up, and last transaction timestamp.';
