-- =============================================================================
-- Migration: 0004_deploys_container_id
-- Description: Adds container_id column to deploys table to track the
--              backend-specific container handle set by runner-svc.
-- =============================================================================

alter table deploys add column if not exists container_id text;

create index if not exists idx_deploys_container_id
    on deploys(container_id) where container_id is not null;

comment on column deploys.container_id is 'Backend-specific container handle (Docker container ID or cloud resource ID) set by runner-svc when the deploy starts running.';
