-- Task 14b-3: git_private deploys carry a short-lived credential in Redis
-- (gitcred:<uuid>, TTL). Only the opaque id is persisted, and only on
-- deploy_sagas so the resume sweeper can rebuild the BuildRequest. The
-- secret itself is never written to Postgres.

alter table deploy_sagas
    add column if not exists credential_id text;

comment on column deploy_sagas.credential_id is 'Redis key suffix (gitcred:<id>) of the short-lived git credential for source_type=git_private. Opaque id only — the secret lives in Redis with a TTL and is deleted by the builder after clone.';
