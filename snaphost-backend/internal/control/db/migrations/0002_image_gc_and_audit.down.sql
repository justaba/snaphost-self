-- Reversed. SQLite has supported ALTER TABLE ... DROP COLUMN since 3.35, and
-- modernc.org/sqlite is well past that, so the column goes rather than being
-- left behind as a tombstone.
drop index if exists idx_admin_audit_time;
drop index if exists idx_admin_audit_target;
drop index if exists idx_admin_audit_actor;
drop table if exists admin_audit_log;

drop index if exists idx_deploys_stopped_at;
drop index if exists idx_deploys_image_cleanup;
alter table deploys drop column image_deleted_at;
