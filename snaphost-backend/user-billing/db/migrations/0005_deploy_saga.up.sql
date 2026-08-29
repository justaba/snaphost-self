-- Saga state tracking. One row per saga execution. Most fields nullable
-- because they're filled in as the saga progresses.
create table deploy_sagas (
    deploy_id           uuid primary key references deploys(id) on delete cascade,
    user_id             uuid not null,
    current_step        text not null default 'pending'
        constraint chk_saga_step check (current_step in (
            'pending', 'reserved', 'building', 'built',
            'provisioning', 'running', 'failed', 'compensating', 'compensated'
        )),
    -- Track which compensations have already run, so resuming is safe.
    coins_reserved      boolean not null default false,
    image_built         boolean not null default false,
    container_running   boolean not null default false,
    coins_committed     boolean not null default false,

    reservation_tx_id   uuid,             -- transactions.id for the reserve
    image_ref           text,             -- filled after build
    commit_sha          text,             -- filled after build
    container_id        text,             -- filled after run
    endpoint_url        text,             -- filled after run

    failure_reason      text,             -- filled on failure
    last_error          text,             -- last transient error (for retry visibility)
    retry_count         int not null default 0,

    created_at          timestamptz not null default now(),
    updated_at          timestamptz not null default now(),
    started_at          timestamptz,      -- when worker first picked it up
    completed_at        timestamptz       -- when reached terminal state
);

create index idx_deploy_sagas_step on deploy_sagas(current_step)
    where current_step not in ('running', 'failed', 'compensated');
create index idx_deploy_sagas_user on deploy_sagas(user_id);

drop trigger if exists deploy_sagas_updated_at on deploy_sagas;
create trigger deploy_sagas_updated_at
    before update on deploy_sagas
    for each row execute function public.handle_updated_at();

comment on table deploy_sagas is 'Tracks the orchestration state of each deploy. Saga worker uses this to resume after crashes.';
comment on column deploy_sagas.current_step is 'State machine position. Compensating means rollback in progress.';
