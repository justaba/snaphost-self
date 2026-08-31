-- =============================================================================
-- Image garbage collection, and the operator audit log.
--
-- This is a migration rather than an edit to the baseline, which is the
-- opposite of what this repository did until now. The baseline rule held while
-- the answer to "who has this installed" was nobody: a fork starts empty, so
-- folding a column in beat carrying history. That stopped being true the
-- moment a database existed with version 1 recorded — golang-migrate applies
-- only migrations above the stored version, so an edited 0001 never runs
-- again and the process comes up querying a column that is not there.
--
-- The failure is not a startup error either. The schema loads, and the first
-- request that touches deploys answers `no such column: image_deleted_at`.
-- =============================================================================

-- When the local Docker image named by image_ref was confirmed absent.
--
-- There is no registry, so the disk that fills is the one the platform runs
-- on. image_ref is kept for diagnostics after the artifact is gone, which is
-- why this is a separate marker rather than nulling that column: a row with an
-- image_ref and no timestamp is cleanup the watchdog still owes, and the sweep
-- is idempotent because the timestamp is only written after Docker says the
-- image is not there.
alter table deploys add column image_deleted_at text;

-- The image sweep's queue: a dead deploy still holding a built artifact.
create index if not exists idx_deploys_image_cleanup on deploys (status, updated_at)
    where image_ref is not null and image_deleted_at is null;

-- The reclaim sweep's queue: a stopped deploy is startable from its image, so
-- it keeps one — but not forever, or a TTL expiry would leak a build's worth
-- of disk on every deploy that was ever paused.
create index if not exists idx_deploys_stopped_at on deploys (stopped_at)
    where status = 'stopped';

-- ---------------------------------------------------------------------------
-- admin_audit_log — what the operator changed, as against what they read
--
-- The panel is a read surface with a small number of destructive exceptions,
-- and the price of admitting one is a row here, written in the same
-- transaction as the change. There is no foreign key to users: the point of an
-- audit row is to outlive whatever it describes.
--
-- details is a JSON object holding the counts and names that the deleted rows
-- would otherwise take with them.
-- ---------------------------------------------------------------------------

create table if not exists admin_audit_log (
    id            text primary key,
    actor_user_id text not null,
    action        text not null,
    target_type   text not null,
    target_id     text not null,
    details       text not null default '{}',
    created_at    text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index if not exists idx_admin_audit_actor  on admin_audit_log (actor_user_id, created_at desc);
create index if not exists idx_admin_audit_target on admin_audit_log (target_type, target_id, created_at desc);
create index if not exists idx_admin_audit_time   on admin_audit_log (created_at desc);
