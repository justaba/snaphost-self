create table ai_usage_log (
    id              uuid primary key default gen_random_uuid(),
    deploy_id       uuid not null,
    user_id         uuid not null,
    provider        text not null,
    model           text not null,
    operation       text not null check (operation in ('generate_dockerfile', 'heal_deploy')),
    input_tokens    int not null default 0,
    output_tokens   int not null default 0,
    cost_usd_micro  bigint not null default 0,
    duration_ms     int not null default 0,
    success         boolean not null,
    error_class     text,
    cache_hit       boolean not null default false,
    created_at      timestamptz not null default now()
);

create index idx_ai_usage_user on ai_usage_log(user_id, created_at desc);
create index idx_ai_usage_deploy on ai_usage_log(deploy_id);
create index idx_ai_usage_created on ai_usage_log(created_at desc);

comment on table ai_usage_log is 'Per-call audit log for LLM operations — used for billing and analytics';
comment on column ai_usage_log.cost_usd_micro is 'Cost in micro-USD (millionths of a dollar) to avoid floating-point';
