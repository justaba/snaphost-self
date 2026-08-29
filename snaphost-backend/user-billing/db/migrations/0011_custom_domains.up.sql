-- =============================================================================
-- Migration: 0011_custom_domains
-- Description: Task 16b — the alias layer. A custom domain is a *pointer*
--              (target_deploy_id) owned by a project, not a property of a
--              deploy: publishing moves the pointer at a new immutable deploy,
--              rollback moves it back with no rebuild. A domain must reach
--              status='verified' before it routes any traffic — without proof
--              of ownership any user could attach a hostname someone else
--              controls, and on an on-demand-TLS edge drive certificate
--              issuance for it.
-- Dependencies: 0010_projects, 0003_deploys, 0001_wallets (handle_updated_at)
-- =============================================================================

create table if not exists custom_domains (
    id                 uuid primary key default gen_random_uuid(),
    user_id            uuid not null,
    project_id         uuid not null references projects(id) on delete cascade,
    target_deploy_id   uuid references deploys(id) on delete set null,
    domain             text not null,
    verification_token text not null,
    status             text not null default 'pending'
                           constraint chk_custom_domains_status check (status in (
                               'pending', 'verified', 'failed', 'revoked'
                           )),
    last_error         text,
    verified_at        timestamptz,
    last_checked_at    timestamptz,
    created_at         timestamptz not null default now(),
    updated_at         timestamptz not null default now()
);

comment on table  custom_domains                    is 'User-owned hostnames attached to a project. The alias layer of the project/deploy/alias model: target_deploy_id is the pointer promotion and rollback move. Only verified rows route traffic.';
comment on column custom_domains.user_id            is 'Owning user UUID. Redundant with projects.user_id but kept for direct per-user quota and listing queries.';
comment on column custom_domains.project_id         is 'Project the domain publishes. Deleting the project detaches the domain with it.';
comment on column custom_domains.target_deploy_id   is 'The single deploy this hostname currently serves. Null means attached but not published yet (or unpinned by idle GC): the domain resolves to 404, exactly as a dead subdomain does.';
comment on column custom_domains.domain             is 'Normalized lowercase hostname without port or trailing dot. Unique across all non-revoked rows, so a detached name can be attached again by its real owner.';
comment on column custom_domains.verification_token is 'Random per-domain token the user must publish as a TXT record at _snaphost-verify.<domain>. Rotated on nothing — a stable token lets a user retry without re-editing DNS.';
comment on column custom_domains.status             is 'pending → verified on a successful TXT check; failed after repeated check failures; revoked on detach. Only verified routes traffic.';
comment on column custom_domains.last_error         is 'Stable reason code for the last failure (txt_not_found, txt_mismatch, dns_lookup_failed, unpinned_idle). A code rather than prose because the dashboard localizes it; the detail goes to the service logs.';
comment on column custom_domains.verified_at        is 'When ownership was last proven. Null while pending.';
comment on column custom_domains.last_checked_at    is 'When the verifier last resolved this domain. Drives both the pending-check and the periodic re-verification that catches a domain transferred away from us.';

-- A revoked row keeps the audit trail but must not block re-attaching the name.
create unique index if not exists uq_custom_domains_domain_active
    on custom_domains(domain) where status <> 'revoked';
create index if not exists idx_custom_domains_user    on custom_domains(user_id) where status <> 'revoked';
create index if not exists idx_custom_domains_project on custom_domains(project_id);
create index if not exists idx_custom_domains_target  on custom_domains(target_deploy_id) where status = 'verified';
-- Verifier sweep: oldest-checked non-terminal rows first.
create index if not exists idx_custom_domains_checked
    on custom_domains(last_checked_at nulls first) where status in ('pending', 'verified');

drop trigger if exists custom_domains_updated_at on custom_domains;
create trigger custom_domains_updated_at
    before update on custom_domains
    for each row execute function public.handle_updated_at();
