-- =============================================================================
-- Migration: 0007_api_keys
-- Description: Long-lived API keys for non-browser clients (MCP server, editor
--              extension, CLI). A key authenticates as a user without the
--              Supabase browser JWT flow. Only the SHA-256 hash of the secret is
--              stored; the plaintext is shown to the user once at creation.
-- Dependencies: 0001_wallets (pgcrypto extension, handle_updated_at function)
-- =============================================================================

create table if not exists api_keys (
    id           uuid primary key default gen_random_uuid(),
    user_id      uuid not null,
    key_hash     text not null unique,
    key_prefix   text not null,
    name         text not null default '',
    created_at   timestamptz not null default now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

comment on table  api_keys              is 'Long-lived API keys for non-browser clients. One row per key; a user may hold several. No FK to wallets — resolved by user_id UUID only.';
comment on column api_keys.user_id      is 'Owning user UUID (Supabase auth id), matching wallets.user_id.';
comment on column api_keys.key_hash     is 'Lowercase hex SHA-256 of the full secret. The plaintext secret is never stored; verification hashes the presented key and compares.';
comment on column api_keys.key_prefix   is 'Non-secret display prefix (e.g. sk_live_ab12cd34) shown in listings so a user can identify a key without revealing it.';
comment on column api_keys.name         is 'Optional user-supplied label, e.g. "cursor-laptop".';
comment on column api_keys.last_used_at is 'Updated on successful verification for activity/audit. Best-effort; not on the hot-path critical section.';
comment on column api_keys.revoked_at   is 'Set when the key is revoked. A non-null value makes the key unusable; rows are kept for audit rather than deleted.';

-- Active-key lookups by hash (verification hot path) and by user (listings).
create unique index if not exists idx_api_keys_hash_active
    on api_keys(key_hash) where revoked_at is null;
create index if not exists idx_api_keys_user_active
    on api_keys(user_id) where revoked_at is null;
