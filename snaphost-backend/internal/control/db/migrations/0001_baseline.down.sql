-- =============================================================================
-- Baseline, reversed.
--
-- Down from the baseline is an empty database, which for a single-file SQLite
-- store is the same thing as deleting the file. It exists so `migrate down`
-- has a defined behaviour rather than failing, not because rolling the whole
-- schema back is a useful operation.
--
-- Tables go in reverse dependency order: custom_domains references projects
-- and deploys, deploys references projects, sessions references users.
-- =============================================================================

drop table if exists ai_usage_log;
drop table if exists ai_dockerfile_cache;
drop table if exists api_keys;
drop table if exists custom_domains;
drop table if exists deploy_sagas;
drop table if exists deploys;
drop table if exists projects;
drop table if exists sessions;
drop table if exists users;
