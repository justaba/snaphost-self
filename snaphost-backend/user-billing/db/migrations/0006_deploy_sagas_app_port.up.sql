-- Persist the application listen port resolved by builder-svc (from
-- EXPOSE or a language heuristic) so the saga can hand it to runner-svc
-- on resume after a crash.
alter table deploy_sagas
    add column app_port int
    constraint chk_saga_app_port check (app_port is null or (app_port > 0 and app_port < 65536));

comment on column deploy_sagas.app_port is
    'Application listen port published by builder-svc in the build event. Saga forwards this to runner-svc as the Traefik load-balancer target port. Null means use the user-billing DEPLOY_DEFAULT_PORT fallback.';
