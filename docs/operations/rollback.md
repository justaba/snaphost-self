# Rollback

Status: Scripted and covered by fake-command tests; portable installation and a
current live rehearsal are not complete
Type: Operations
Updated: 2026-09-04

There are two different rollback operations.

## Release rollback

Application releases use exact `vMAJOR.MINOR.PATCH` image tags. infra/deploy.sh
records current.env, previous.env and in-progress.env under the deployment state
directory. Release rollback selects the saved previous version; it never
rebuilds a branch and never accepts `latest`.

For one transition from the old release model, rollback may read a saved
40-character `sha=` value. A SHA is not accepted as a new deploy target. This
keeps the pre-upgrade image recoverable without making the debugging tag part
of the supported version interface.

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

The command also refuses to touch containers when `SNAPHOST_VERSION` in the env
file disagrees with current.env. Reconcile that mismatch against the image
which is actually running; do not edit one side blindly.

If the runtime rollback succeeds but the env-file replacement fails, the
script immediately restores the original runtime and leaves the old state
contract intact. A failure of that compensation is reported as manual
intervention required.

The old repository-owner SSH release layout has been removed. The scripted
logic is tested, but the first-install and checkout-upgrade procedure is not yet
a supported third-party installation contract.

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
- the running image version/digest and restart count;
- current.env and previous.env ownership and contents;
- absence of an unexpected in-progress.env;
- a real deploy when the release touched builder or runtime behavior.

infra/tests/deploy_test.sh exercises SemVer guards, legacy-state transition,
env/state updates, migration compatibility decisions, missing images, health
and smoke failures, and lock contention with fake external commands. It cannot
prove Docker, disk, SQLite volume access or public routing on a real host.
