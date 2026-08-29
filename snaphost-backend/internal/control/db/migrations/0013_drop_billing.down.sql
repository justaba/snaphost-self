-- =============================================================================
-- Migration: 0013_drop_billing (down)
-- Description: Recreates the billing schema so a down/up cycle leaves the
--              database where it started structurally.
--
--              **It does not restore data, and cannot.** Rolling 0013 back
--              gives every account a zero balance and an empty ledger. If a
--              deployment ever has to go backwards across this migration and
--              the balances mattered, restore from a dump taken before it ran
--              — this file is not a substitute for one.
--
--              No application code reads these tables any more, so the
--              recreated schema is inert unless the code is also rolled back.
-- =============================================================================

create table if not exists wallets (
    user_id     uuid primary key,
    balance     bigint not null default 0
                    constraint chk_wallets_balance_non_negative check (balance >= 0),
    reserved    bigint not null default 0
                    constraint chk_wallets_reserved_non_negative check (reserved >= 0),
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);

drop trigger if exists wallets_updated_at on wallets;
create trigger wallets_updated_at
    before update on wallets
    for each row execute function public.handle_updated_at();

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

create index if not exists idx_transactions_user_id    on transactions(user_id);
create index if not exists idx_transactions_deploy_id  on transactions(deploy_id);
create index if not exists idx_transactions_created_at on transactions(created_at desc);

alter table deploy_sagas
    add column if not exists coins_reserved    boolean not null default false,
    add column if not exists coins_committed   boolean not null default false,
    add column if not exists reservation_tx_id uuid;

-- Restored without the non-negative check constraint the original carried:
-- every existing row would get 0 here, and re-adding a constraint that the
-- restored data trivially satisfies would imply the values mean something.
alter table deploys
    add column if not exists cost_vibecoins    bigint not null default 0,
    add column if not exists reservation_tx_id uuid;
