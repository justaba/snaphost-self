create extension if not exists pgcrypto;

create table ai_dockerfile_cache (
    signature      text primary key,
    project_type   text not null,
    dockerfile     text not null,
    expose_port    int not null,
    source         text not null check (source in ('template', 'llm')),
    used_count     bigint not null default 1,
    created_at     timestamptz not null default now(),
    last_used_at   timestamptz not null default now()
);

create index idx_ai_cache_last_used on ai_dockerfile_cache(last_used_at desc);
create index idx_ai_cache_source on ai_dockerfile_cache(source);

comment on table ai_dockerfile_cache is 'Caches generated Dockerfiles by project signature to avoid redundant LLM calls';
comment on column ai_dockerfile_cache.signature is 'SHA-256 of sorted file tree hashes plus key file contents — same signature means same project structure';
