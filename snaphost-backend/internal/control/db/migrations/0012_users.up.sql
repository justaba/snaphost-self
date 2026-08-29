-- =============================================================================
-- Migration: 0012_users
-- Description: Creates the users table — the control plane's own record of who
--              a user_id belongs to. Until now the only identity here was
--              wallets.user_id, a bare UUID: the Supabase webhook received an
--              email and discarded it, so no operator surface could name an
--              account without querying a different database.
-- Dependencies: 0001_wallets (backfill source), 0002_transactions, 0003_deploys
-- =============================================================================

create table if not exists users (
    id         uuid primary key,
    email      text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

comment on table  users            is 'One row per account known to the control plane, seeded by the Supabase user webhook. Authoritative identity still lives in Supabase auth.users; this is the local projection needed to name an account without a cross-database query.';
comment on column users.id         is 'Supabase auth user UUID. Matches wallets.user_id, deploys.user_id, transactions.user_id. No foreign key — Supabase is a separate database.';
comment on column users.email      is 'Email as reported by the Supabase webhook. Null for rows backfilled from pre-existing wallets and deploys, whose email was never stored.';
comment on column users.created_at is 'When the control plane first learned about this account, not when the account was created in Supabase.';

drop trigger if exists users_updated_at on users;
create trigger users_updated_at
    before update on users
    for each row execute function public.handle_updated_at();

-- Case-insensitive lookup for the admin user search.
create index if not exists idx_users_email_lower on users(lower(email));
create index if not exists idx_users_created_at on users(created_at desc);

-- ---------------------------------------------------------------------------
-- Backfill
-- ---------------------------------------------------------------------------
-- Every user_id the control plane has ever seen gets a row, so the admin user
-- list is complete rather than only covering accounts created after this
-- migration. Emails stay null: they were never recorded and cannot be
-- reconstructed from this database.
--
-- wallets alone is not enough. A registered user whose seed webhook never fired
-- has no wallet (a known production failure mode) but may still own deploys or
-- transactions, and that is exactly the account an operator needs to find.

insert into users (id, created_at)
select user_id, min(created_at)
from (
    select user_id, created_at from wallets
    union all
    select user_id, created_at from deploys
    union all
    select user_id, created_at from transactions
) seen
group by user_id
on conflict (id) do nothing;
