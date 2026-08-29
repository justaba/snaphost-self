drop trigger if exists deploy_sagas_updated_at on deploy_sagas;
drop index if exists idx_deploy_sagas_step;
drop index if exists idx_deploy_sagas_user;
drop table if exists deploy_sagas;
