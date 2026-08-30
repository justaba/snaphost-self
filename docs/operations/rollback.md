# Rollback

Status: Scripted and covered by fake-command tests; portable installation and a
current live rehearsal are not complete
Type: Operations
Updated: 2026-08-30

There are two different rollback operations.

## Release rollback

Application images are immutable and tagged by a full Git SHA. infra/deploy.sh
records current.env, previous.env and in-progress.env under the deployment state
directory. Release rollback selects the saved previous SHA; it never rebuilds a
branch and never accepts latest.

~~~bash
infra/deploy.sh --dry-run rollback
infra/deploy.sh rollback
~~~

The dry run validates saved state, the manifest and local image availability
without changing services or state files.

Before migrations start, application rollback may proceed. After migration
start it is blocked unless an operator has reviewed the exact schema change and
explicitly sets:

~~~bash
MIGRATIONS_BACKWARD_COMPATIBLE=true infra/deploy.sh rollback
~~~

This flag means the previous binary can safely use the current schema. It does
not roll the database back.

If a deploy or rollback leaves in-progress.env, inspect it and the current
container image before taking another action. Do not delete the file merely to
make the guard pass.

The deployment scripts target the repository owner's SSH release layout. Their
logic is tested, but their paths and release model are not a supported
third-party installation contract.

## Site rollback

A project custom domain points at one immutable running deploy. Rolling a site
back does not invoke deploy.sh and does not change the control-plane version.
Move the alias to an earlier running deploy:

~~~json
POST /api/v1/domains/<domain-id>/target
{
  "deploy_id": "<previous-running-deploy-uuid>"
}
~~~

This is one SQLite pointer update and performs no build. The custom-domain edge
is not yet complete, so this control-plane behavior is not currently a portable
end-to-end traffic rollback.

## Database restore is separate

Release rollback must not restore SQLite automatically. Restore discards writes
after the selected backup and must be reviewed independently. Before a release
with a destructive migration, define either a forward fix or a tested restore
plan.

See [backup and restore](backups.md).

## Verification

After release rollback verify:

- /health;
- anonymous protected API returns 401;
- operator login and project listing;
- the running image SHA and restart count;
- current.env and previous.env ownership and contents;
- absence of an unexpected in-progress.env;
- a real deploy when the release touched builder or runtime behavior.

infra/tests/deploy_test.sh exercises guards, state transitions, migration
compatibility decisions, missing images, smoke failures and lock contention
with fake external commands. It cannot prove Docker, disk, credentials, SQLite
volume access or public routing on a real host.
