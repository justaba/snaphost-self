-- =============================================================================
-- Down migration: 0007_api_keys
-- =============================================================================

drop index if exists idx_api_keys_user_active;
drop index if exists idx_api_keys_hash_active;
drop table if exists api_keys;
