-- =============================================================================
-- Migration: 0012_users (down)
-- Description: Drops the users table. Emails collected since the up migration
--              are lost — they exist nowhere else in this database.
-- =============================================================================

drop trigger if exists users_updated_at on users;
drop index if exists idx_users_created_at;
drop index if exists idx_users_email_lower;
drop table if exists users;
