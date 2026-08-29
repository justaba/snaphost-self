-- Revert 0008_deploy_source. Restoring NOT NULL on repo_url requires that no
-- archive deploys (repo_url is null) exist; backfill with '' to be safe.

alter table deploy_sagas
    drop column if exists upload_id,
    drop column if exists source_type;

update deploys set repo_url = '' where repo_url is null;

alter table deploys
    alter column repo_url set not null;

alter table deploys
    drop column if exists upload_id,
    drop column if exists source_type;
