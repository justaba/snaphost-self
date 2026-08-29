-- =============================================================================
-- Migration: 0001_wallets
-- Description: Creates the wallets table for tracking user balances in
--              vibecoins, including reserved funds for in-progress deploys.
--              Also creates the generic updated_at trigger function and
--              the wallet_totals admin view.
-- Dependencies: None (first migration in SnapHost backend database)
-- =============================================================================

create extension if not exists pgcrypto;

-- ---------------------------------------------------------------------------
-- Generic updated_at trigger function (reused by later migrations)
-- ---------------------------------------------------------------------------

create or replace function public.handle_updated_at()
returns trigger
language plpgsql
volatile
as $$
begin
    new.updated_at = now();
    return new;
end;
$$;

-- ---------------------------------------------------------------------------
-- wallets table
-- ---------------------------------------------------------------------------

create table if not exists wallets (
    user_id     uuid primary key,
    balance     bigint not null default 0
                    constraint chk_wallets_balance_non_negative check (balance >= 0),
    reserved    bigint not null default 0
                    constraint chk_wallets_reserved_non_negative check (reserved >= 0),
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);

comment on table  wallets              is 'One wallet per user. Tracks available balance and reserved funds in vibecoins. Linked to Supabase profiles by user_id UUID (no FK — different database).';
comment on column wallets.user_id      is 'Primary key matching the Supabase auth user UUID (public.profiles.id). No foreign key since wallets live in a separate database.';
comment on column wallets.balance      is 'Available funds in vibecoins. Decremented on commit, incremented on topup/bonus/refund. Must be non-negative.';
comment on column wallets.reserved     is 'Funds locked by in-progress deploys via the saga reservation pattern. Released on commit (balance decremented) or refund (balance restored). Must be non-negative.';
comment on column wallets.created_at   is 'Timestamp when the wallet was created (typically during the new-user registration flow).';
comment on column wallets.updated_at   is 'Timestamp of the last wallet modification, maintained automatically by trigger.';

-- updated_at trigger for wallets
drop trigger if exists wallets_updated_at on wallets;
create trigger wallets_updated_at
    before update on wallets
    for each row execute function public.handle_updated_at();

-- Index for admin reports sorted by recent activity
create index if not exists idx_wallets_updated_at on wallets(updated_at desc);

-- ---------------------------------------------------------------------------
-- wallet_totals view (admin dashboards)
-- ---------------------------------------------------------------------------

create or replace view wallet_totals as
select
    count(*)        as wallet_count,
    sum(balance)    as total_balance,
    sum(reserved)   as total_reserved
from wallets;

comment on view wallet_totals is 'Aggregate view for admin dashboards showing total wallet count, combined balance, and combined reserved funds across all users.';
