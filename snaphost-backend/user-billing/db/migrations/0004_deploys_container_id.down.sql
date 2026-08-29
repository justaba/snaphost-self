-- =============================================================================
-- Migration: 0004_deploys_container_id (down)
-- =============================================================================

drop index if exists idx_deploys_container_id;
alter table deploys drop column if exists container_id;
