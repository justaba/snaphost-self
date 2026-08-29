-- =============================================================================
-- Migration: 0010_projects (down)
-- =============================================================================

drop index if exists idx_deploys_project_created;

alter table deploys
    drop column if exists project_id,
    drop column if exists last_request_at;

drop trigger if exists projects_updated_at on projects;
drop table if exists projects;
