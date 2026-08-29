-- =============================================================================
-- Migration: 0008_deploy_source
-- Description: Task 14b-1 — deploy source abstraction. Adds source_type
--              (git_public | git_private | archive) and upload_id to both
--              deploys and deploy_sagas, and makes repo_url nullable so
--              archive deploys (14b-2) can omit it. Existing rows default
--              to git_public; behaviour of git_public deploys is unchanged.
-- Dependencies: 0003_deploys, 0005_deploy_saga
-- =============================================================================

alter table deploys
    add column if not exists source_type text not null default 'git_public'
        constraint chk_deploys_source_type check (source_type in (
            'git_public', 'git_private', 'archive'
        )),
    add column if not exists upload_id text;

alter table deploys
    alter column repo_url drop not null;

comment on column deploys.source_type is 'How the project source reaches the builder: git_public (clone public repo), git_private (clone with short-lived credential, 14b-3), archive (uploaded tar.gz blob, 14b-2).';
comment on column deploys.upload_id   is 'Redis blob key suffix (upload:<upload_id>) for source_type=archive. Null for git sources.';
comment on column deploys.repo_url    is 'Git repository URL to clone and build. Required for git sources, null for archive.';

alter table deploy_sagas
    add column if not exists source_type text not null default 'git_public'
        constraint chk_deploy_sagas_source_type check (source_type in (
            'git_public', 'git_private', 'archive'
        )),
    add column if not exists upload_id text;

comment on column deploy_sagas.source_type is 'Deploy source type mirrored from deploys so a resumed saga can rebuild the BuildRequest without the original job message.';
comment on column deploy_sagas.upload_id   is 'Archive upload id mirrored from deploys for saga resume. Null for git sources.';
