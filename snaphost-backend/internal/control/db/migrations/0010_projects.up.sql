-- =============================================================================
-- Migration: 0010_projects
-- Description: Task 16a — the permanent layer above a deploy. A project is
--              owner-scoped and permanent; a deploy stays what it already was
--              (one immutable build, permanently addressable at its own
--              subdomain). This is the entity a custom domain can be bound to
--              across redeploys — deploys expire, projects do not.
--              Also adds deploys.last_request_at, which turns TTL reaping into
--              inactivity-based GC for deploys an alias pins (16a item 7).
-- Dependencies: 0003_deploys (deploys table), 0001_wallets (handle_updated_at)
-- =============================================================================

create table if not exists projects (
    id          uuid primary key default gen_random_uuid(),
    user_id     uuid not null,
    slug        text not null,
    source_key  text not null,
    created_at  timestamptz not null default now(),
    updated_at  timestamptz not null default now()
);

comment on table  projects            is 'Permanent, owner-scoped identity above a deploy. Deploys are immutable build results with their own URL and a TTL; a project outlives all of them and is what an alias (custom_domains) points through. No FK to users — cross-service boundary, user_id UUID only.';
comment on column projects.user_id    is 'Owning user UUID (Supabase auth id), matching wallets.user_id.';
comment on column projects.slug       is 'Human-readable per-user identifier derived from the source (repo name or upload). Unique per user, not globally.';
comment on column projects.source_key is 'Deterministic key used to map an incoming deploy back to its project: git:<host>/<path>#<branch> for git sources, archive:<deploy_id> for uploads (which carry no stable identity, so each upload deploy gets its own project).';

create unique index if not exists uq_projects_user_slug       on projects(user_id, slug);
create unique index if not exists uq_projects_user_source_key on projects(user_id, source_key);
create index        if not exists idx_projects_user_id        on projects(user_id);

drop trigger if exists projects_updated_at on projects;
create trigger projects_updated_at
    before update on projects
    for each row execute function public.handle_updated_at();

-- ---------------------------------------------------------------------------
-- deploys → project membership and traffic bookkeeping
-- ---------------------------------------------------------------------------

alter table deploys
    add column if not exists project_id      uuid references projects(id) on delete set null,
    add column if not exists last_request_at timestamptz;

comment on column deploys.project_id      is 'The project this build belongs to. Null only for rows predating Task 16a that could not be grouped. On project deletion the deploy is kept and detached rather than removed, so its immutable URL history is not lost silently.';
comment on column deploys.last_request_at is 'Last time router lookup resolved a request to this deploy, updated at most once every few minutes. Drives inactivity-based GC for alias-pinned deploys: a hard timer cannot express "live but quiet", which is the normal state of a small published site.';

create index if not exists idx_deploys_project_created on deploys(project_id, created_at desc);

-- ---------------------------------------------------------------------------
-- Backfill: group existing deploys into per-user projects by source.
-- Archive deploys have no stable source identity, so each gets its own project.
-- ---------------------------------------------------------------------------

insert into projects (user_id, slug, source_key)
select
    d.user_id,
    left(regexp_replace(
        coalesce(nullif(regexp_replace(coalesce(d.repo_url, ''), '^.*/', ''), ''), 'project'),
        '[^a-zA-Z0-9-]', '-', 'g'), 40) || '-' || left(md5(coalesce(d.repo_url, '') || coalesce(d.branch, '')), 6),
    'git:' || coalesce(d.repo_url, '') || '#' || coalesce(d.branch, '')
from (
    select distinct user_id, repo_url, branch
    from deploys
    where project_id is null and repo_url is not null
) d
on conflict do nothing;

update deploys d
set project_id = p.id
from projects p
where d.project_id is null
  and d.repo_url is not null
  and p.user_id = d.user_id
  and p.source_key = 'git:' || coalesce(d.repo_url, '') || '#' || coalesce(d.branch, '');

insert into projects (user_id, slug, source_key)
select d.user_id,
       'upload-' || left(d.id::text, 8),
       'archive:' || d.id::text
from deploys d
where d.project_id is null
on conflict do nothing;

update deploys d
set project_id = p.id
from projects p
where d.project_id is null
  and p.user_id = d.user_id
  and p.source_key = 'archive:' || d.id::text;
