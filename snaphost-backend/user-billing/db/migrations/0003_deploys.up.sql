-- =============================================================================
-- Migration: 0003_deploys
-- Description: Creates the deploys table tracking the full lifecycle of user
--              deployments — from pending through building/provisioning to
--              running and eventual stop/deletion. Includes TTL-based
--              auto-stop support and links to the reservation transaction.
-- Dependencies: 0001_wallets (uses public.handle_updated_at function)
-- =============================================================================

create extension if not exists pgcrypto;

-- ---------------------------------------------------------------------------
-- deploys table
-- ---------------------------------------------------------------------------

create table if not exists deploys (
    id                  uuid primary key default gen_random_uuid(),
    user_id             uuid not null,
    repo_url            text not null,
    branch              text not null default 'main',
    commit_sha          text,
    status              text not null default 'pending'
                            constraint chk_deploys_status check (status in (
                                'pending', 'reserved', 'building', 'provisioning',
                                'running', 'failed', 'stopped', 'deleted'
                            )),
    image_ref           text,
    endpoint_url        text,
    subdomain           text
                            constraint uq_deploys_subdomain unique,
    cost_vibecoins      bigint not null
                            constraint chk_deploys_cost_non_negative check (cost_vibecoins >= 0),
    reservation_tx_id   uuid,
    ttl_expires_at      timestamptz,
    failure_reason      text,
    metadata            jsonb not null default '{}'::jsonb,
    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    stopped_at          timestamptz
);

comment on table  deploys                        is 'Tracks every deployment through its full lifecycle. Each deploy consumes vibecoins via the saga reservation pattern and runs until TTL expiry, manual stop, or failure.';
comment on column deploys.id                     is 'Auto-generated UUID primary key for each deployment.';
comment on column deploys.user_id                is 'The user who initiated this deploy. Matches Supabase profiles.id and wallets.user_id (no FK — cross-service boundary).';
comment on column deploys.repo_url               is 'Git repository URL to clone and build (e.g. https://github.com/user/repo).';
comment on column deploys.branch                 is 'Git branch to build. Defaults to main.';
comment on column deploys.commit_sha             is 'Exact commit SHA checked out during the build. Populated by repo-handler after clone completes.';
comment on column deploys.status                 is 'Current lifecycle state: pending → reserved → building → provisioning → running → stopped/deleted, or failed at any stage.';
comment on column deploys.image_ref              is 'Docker image reference (e.g. registry/image:tag) produced by builder-svc. Null until build completes.';
comment on column deploys.endpoint_url           is 'Public URL where the deployed app is accessible. Populated by infra-provisioner after container is running.';
comment on column deploys.subdomain              is 'Unique subdomain for the deploy (e.g. proj-abc123 for proj-abc123.snaphost.app). Must be globally unique.';
comment on column deploys.cost_vibecoins         is 'Total cost of this deploy in vibecoins. Determined at creation time based on resource tier and TTL.';
comment on column deploys.reservation_tx_id      is 'References transactions.id for the reservation transaction that locked funds for this deploy. Used to commit or refund on completion/failure.';
comment on column deploys.ttl_expires_at         is 'When this deploy should be auto-stopped by the watchdog cron. Null means no auto-stop (admin or unlimited-tier deploys).';
comment on column deploys.failure_reason         is 'Human-readable error message populated when status transitions to failed. Null for non-failed deploys.';
comment on column deploys.metadata               is 'Flexible JSONB for storing build logs references, resource tier config, environment variables hash, or other deploy-specific context.';
comment on column deploys.created_at             is 'Timestamp when the deploy was created (user clicked "Deploy").';
comment on column deploys.updated_at             is 'Timestamp of the last status or metadata change, maintained automatically by trigger.';
comment on column deploys.stopped_at             is 'Timestamp when the deploy was stopped or deleted. Null while active.';

-- ---------------------------------------------------------------------------
-- updated_at trigger
-- ---------------------------------------------------------------------------

drop trigger if exists deploys_updated_at on deploys;
create trigger deploys_updated_at
    before update on deploys
    for each row execute function public.handle_updated_at();

-- ---------------------------------------------------------------------------
-- Indexes
-- ---------------------------------------------------------------------------

create index if not exists idx_deploys_user_id       on deploys(user_id);
create index if not exists idx_deploys_status        on deploys(status);
create index if not exists idx_deploys_created_at    on deploys(created_at desc);
create index if not exists idx_deploys_user_status   on deploys(user_id, status);

-- Partial index for the watchdog cron: efficiently find running deploys past their TTL
create index if not exists idx_deploys_ttl_partial
    on deploys(ttl_expires_at)
    where status = 'running';

-- Note: idx for subdomain is implicitly created by the unique constraint (uq_deploys_subdomain)

-- ---------------------------------------------------------------------------
-- active_deploys_per_user view
-- ---------------------------------------------------------------------------

create or replace view active_deploys_per_user as
select
    user_id,
    count(*) filter (where status = 'running')                                              as running_count,
    count(*) filter (where status in ('pending', 'reserved', 'building', 'provisioning'))    as in_progress_count,
    count(*) filter (where status = 'failed')                                               as failed_count,
    count(*)                                                                                as total_count
from deploys
group by user_id;

comment on view active_deploys_per_user is 'Per-user summary of deploy counts by lifecycle state. Used by the dashboard and rate-limiting logic to enforce concurrent deploy limits.';

-- =============================================================================
-- ARCHITECTURE NOTES — Saga Pattern & Cross-Table Relationships
-- =============================================================================
--
-- Reservation ↔ Wallet Relationship
-- ----------------------------------
-- wallets.reserved reflects the total vibecoins currently locked across all
-- in-progress deploys for a user. The invariant is:
--
--     wallets.reserved = SUM(d.cost_vibecoins)
--       FROM deploys d
--       WHERE d.user_id = wallets.user_id
--         AND d.status IN ('reserved', 'building', 'provisioning')
--
-- This is NOT enforced by a database constraint (for performance and to allow
-- the saga to operate across service boundaries). The user-billing service
-- maintains this invariant in application code.
--
-- Saga Pattern Flow
-- -----------------
-- 1. user-billing receives a deploy request from api-gateway
-- 2. ATOMICALLY in a single transaction:
--    a. INSERT INTO transactions (type='reserve', status='pending', amount=cost)
--    b. UPDATE wallets SET balance = balance - cost, reserved = reserved + cost
--       WHERE user_id = ? AND balance >= cost
--    c. If the UPDATE affects 0 rows → insufficient funds → rollback
-- 3. On success, user-billing returns the transaction ID to api-gateway
-- 4. api-gateway forwards to repo-handler, which creates the deploys row
--    with reservation_tx_id = the transaction ID from step 3
-- 5. On successful deploy (status → 'running'):
--    a. UPDATE transactions SET status='completed', completed_at=now()
--    b. UPDATE wallets SET reserved = reserved - cost
-- 6. On failed deploy (status → 'failed'):
--    a. INSERT INTO transactions (type='refund', status='completed', amount=cost)
--    b. UPDATE wallets SET reserved = reserved - cost, balance = balance + cost
--
-- Watchdog Cron Query
-- -------------------
-- The infra-provisioner watchdog runs periodically to auto-stop expired deploys:
--
--   SELECT id
--   FROM deploys
--   WHERE status = 'running'
--     AND ttl_expires_at < now()
--   ORDER BY ttl_expires_at
--   LIMIT 100;
--
-- This query is served efficiently by idx_deploys_ttl_partial.
-- =============================================================================
