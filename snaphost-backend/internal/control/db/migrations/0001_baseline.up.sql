-- =============================================================================
-- Baseline schema.
--
-- Squashed from the thirteen PostgreSQL migrations this repository forked with.
-- A fork starts with an empty database, so their history bought nothing and
-- carried the vibecoin columns forward; what mattered was the shape they had
-- arrived at, and that is what this file is.
--
-- Translated to SQLite (Task 1 item 6). The differences worth knowing:
--
--   * No uuid type. Ids are TEXT and generated in Go — PostgreSQL's
--     gen_random_uuid() default is gone, so an INSERT must supply one.
--   * No timestamptz. Timestamps are TEXT in RFC 3339 with a Z suffix, which
--     sorts correctly as a string precisely because it is always UTC. Anything
--     writing a local time here breaks ordering silently.
--   * No jsonb. Metadata is TEXT holding JSON.
--   * No booleans. SQLite stores 0/1 and the driver maps Go bools onto them.
--   * No shared trigger function, so each updated_at trigger carries its own
--     body rather than calling one.
--
-- Foreign keys are declared and are only enforced when the connection sets
-- `PRAGMA foreign_keys = ON`, which the store does per connection. Left off,
-- the constraints below are documentation and nothing more.
-- =============================================================================

-- ---------------------------------------------------------------------------
-- users — the accounts this control plane knows about
--
-- Kept, and every other table keeps its user_id, even though this platform has
-- exactly one operator. A password needs a row to hang off, and the column is
-- already the isolation a second operator or a service account would need.
--
-- password_hash is where identity stopped being somebody else's problem. It
-- used to live in Supabase, and the platform could not be logged into without
-- an external SaaS or ten more containers. It is an argon2id PHC string, and
-- it is nullable: a row that never logs in with a password — a service account
-- reaching the API with an sk_ key — has none rather than an unusable one.
--
-- role replaces the `snaphost_role` JWT claim, which a hand-configured
-- Supabase Postgres hook wrote. It is a column now, so the authorisation the
-- admin console depends on is in the same file as everything else it reads.
-- ---------------------------------------------------------------------------

create table users (
    id            text primary key,
    email         text,
    password_hash text,
    role          text not null default 'user' check (role in ('user', 'admin')),
    created_at    text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index idx_users_created_at on users (created_at desc);
-- Unique rather than merely indexed: login resolves an account by address, and
-- two rows sharing one would make which account answers a question of row
-- order. Partial, because a row without an address is allowed.
create unique index uq_users_email_lower on users (lower(email)) where email is not null;

create trigger users_updated_at after update on users for each row
begin
    update users set updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') where id = new.id;
end;

-- ---------------------------------------------------------------------------
-- sessions — what a browser presents instead of a password
--
-- Only the SHA-256 of the token is stored, the same bargain api_keys makes: a
-- copied database file — a backup, a dump, a stolen volume — hands over no live
-- session. SHA-256 rather than argon2id because the token is 256 bits from
-- crypto/rand and there is nothing in it to guess; the password below it is the
-- only value with entropy low enough to need a slow hash.
--
-- Server-side rather than a self-signed JWT, and that is the whole reason this
-- table exists: logout has to revoke, and changing the password has to revoke
-- every other session. A stateless token would need a denylist to do either,
-- which is this table with a worse name.
-- ---------------------------------------------------------------------------

create table sessions (
    token_hash text primary key,
    user_id    text not null references users (id) on delete cascade,
    expires_at text not null,
    created_at text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index idx_sessions_user    on sessions (user_id);
create index idx_sessions_expires on sessions (expires_at);

-- ---------------------------------------------------------------------------
-- projects — the permanent, owner-scoped publish target
--
-- A deploy is one immutable build; a project outlives all of them and is what
-- a custom domain is attached to. source_key is what makes a repeat deploy of
-- the same source join the project it created the first time.
-- ---------------------------------------------------------------------------

create table projects (
    id         text primary key,
    user_id    text not null,
    slug       text not null,
    source_key text not null,
    created_at text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index        idx_projects_user_id        on projects (user_id);
create unique index uq_projects_user_slug       on projects (user_id, slug);
create unique index uq_projects_user_source_key on projects (user_id, source_key);

create trigger projects_updated_at after update on projects for each row
begin
    update projects set updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') where id = new.id;
end;

-- ---------------------------------------------------------------------------
-- deploys — one build, immutable, addressable at its own subdomain
--
-- 'reserved' is gone from the status list. It existed for the coin reservation
-- and became unreachable when billing was removed; the saga now goes straight
-- from pending to building.
-- ---------------------------------------------------------------------------

create table deploys (
    id              text primary key,
    user_id         text not null,
    project_id      text references projects (id) on delete set null,
    source_type     text not null default 'git_public'
                        check (source_type in ('git_public', 'git_private', 'archive')),
    repo_url        text,
    branch          text not null default 'main',
    upload_id       text,
    commit_sha      text,
    status          text not null default 'pending'
                        check (status in ('pending', 'building', 'provisioning',
                                          'running', 'failed', 'stopped', 'deleted')),
    image_ref       text,
    endpoint_url    text,
    subdomain       text unique,
    container_id    text,
    ttl_expires_at  text,
    last_request_at text,
    failure_reason  text,
    metadata        text not null default '{}',
    created_at      text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    stopped_at      text
);

create index idx_deploys_user_id         on deploys (user_id);
create index idx_deploys_status          on deploys (status);
create index idx_deploys_created_at      on deploys (created_at desc);
create index idx_deploys_user_status     on deploys (user_id, status);
create index idx_deploys_project_created on deploys (project_id, created_at desc);
create index idx_deploys_container_id    on deploys (container_id) where container_id is not null;
-- The watchdog's sweep: only a running deploy can be past its TTL.
create index idx_deploys_ttl_partial     on deploys (ttl_expires_at) where status = 'running';

create trigger deploys_updated_at after update on deploys for each row
begin
    update deploys set updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') where id = new.id;
end;

-- ---------------------------------------------------------------------------
-- deploy_sagas — what actually happened, as against deploys.status, which is
-- what the user is shown
-- ---------------------------------------------------------------------------

create table deploy_sagas (
    deploy_id        text primary key,
    user_id          text not null,
    current_step     text not null default 'pending'
                         check (current_step in ('pending', 'building', 'built',
                                                 'provisioning', 'running', 'failed',
                                                 'compensating', 'compensated')),
    source_type      text not null default 'git_public'
                         check (source_type in ('git_public', 'git_private', 'archive')),
    upload_id        text,
    credential_id    text,
    image_built      integer not null default 0,
    container_running integer not null default 0,
    image_ref        text,
    commit_sha       text,
    app_port         integer check (app_port is null or (app_port > 0 and app_port < 65536)),
    container_id     text,
    endpoint_url     text,
    failure_reason   text,
    last_error       text,
    retry_count      integer not null default 0,
    created_at       text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at       text,
    completed_at     text
);

create index idx_deploy_sagas_user on deploy_sagas (user_id);
-- The resume sweeper reads this: everything not in a terminal state.
create index idx_deploy_sagas_step on deploy_sagas (current_step)
    where current_step not in ('running', 'failed', 'compensated');

create trigger deploy_sagas_updated_at after update on deploy_sagas for each row
begin
    update deploy_sagas set updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') where deploy_id = new.deploy_id;
end;

-- ---------------------------------------------------------------------------
-- custom_domains — the alias layer
--
-- target_deploy_id is the pointer publishing and rollback move. Only a
-- 'verified' row ever routes, which is why the partial indexes below are
-- scoped the way they are.
-- ---------------------------------------------------------------------------

create table custom_domains (
    id                 text primary key,
    user_id            text not null,
    project_id         text not null references projects (id) on delete cascade,
    target_deploy_id   text references deploys (id) on delete set null,
    domain             text not null,
    verification_token text not null,
    status             text not null default 'pending'
                           check (status in ('pending', 'verified', 'failed', 'revoked')),
    last_error         text,
    verified_at        text,
    last_checked_at    text,
    created_at         text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- A revoked domain releases its name for someone else to attach.
create unique index uq_custom_domains_domain_active on custom_domains (domain) where status <> 'revoked';
create index        idx_custom_domains_project      on custom_domains (project_id);
create index        idx_custom_domains_user         on custom_domains (user_id) where status <> 'revoked';
create index        idx_custom_domains_target       on custom_domains (target_deploy_id) where status = 'verified';
-- The verifier's queue: never-checked rows sort first.
create index        idx_custom_domains_checked      on custom_domains (last_checked_at)
    where status in ('pending', 'verified');

create trigger custom_domains_updated_at after update on custom_domains for each row
begin
    update custom_domains set updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') where id = new.id;
end;

-- ---------------------------------------------------------------------------
-- api_keys — the credential every non-browser client authenticates with
--
-- Only the hash is stored; the plaintext is shown once at creation. Revoked
-- rows are kept rather than deleted, so the unique index that matters is the
-- partial one over active keys.
-- ---------------------------------------------------------------------------

create table api_keys (
    id           text primary key,
    user_id      text not null,
    key_hash     text not null,
    key_prefix   text not null,
    name         text not null default '',
    created_at   text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at text,
    revoked_at   text
);

create unique index idx_api_keys_hash_active on api_keys (key_hash) where revoked_at is null;
create index        idx_api_keys_user_active on api_keys (user_id)  where revoked_at is null;

-- ---------------------------------------------------------------------------
-- ai_dockerfile_cache and ai_usage_log
--
-- The generator's own two tables. They were a separate migration history
-- against the same database because the generator was a separate service; it
-- is a package now, so they are here.
-- ---------------------------------------------------------------------------

create table ai_dockerfile_cache (
    signature    text primary key,
    project_type text not null,
    dockerfile   text not null,
    expose_port  integer not null,
    source       text not null check (source in ('template', 'llm')),
    used_count   integer not null default 1,
    created_at   text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index idx_ai_cache_last_used on ai_dockerfile_cache (last_used_at desc);
create index idx_ai_cache_source    on ai_dockerfile_cache (source);

create table ai_usage_log (
    id             text primary key,
    deploy_id      text not null,
    user_id        text not null,
    provider       text not null,
    model          text not null,
    operation      text not null check (operation in ('generate_dockerfile', 'heal_deploy')),
    input_tokens   integer not null default 0,
    output_tokens  integer not null default 0,
    -- Micro-USD rather than a float: a cost that cannot be summed exactly is
    -- not a cost record.
    cost_usd_micro integer not null default 0,
    duration_ms    integer not null default 0,
    success        integer not null,
    error_class    text,
    cache_hit      integer not null default 0,
    created_at     text not null default (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

create index idx_ai_usage_deploy  on ai_usage_log (deploy_id);
create index idx_ai_usage_user    on ai_usage_log (user_id, created_at desc);
create index idx_ai_usage_created on ai_usage_log (created_at desc);
