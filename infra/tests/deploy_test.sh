#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SCRIPT="$ROOT/infra/deploy.sh"
DOCKERFILE="$ROOT/snaphost-backend/docker/Dockerfile"
PROD_COMPOSE="$ROOT/infra/docker-compose.prod.yml"
RUNTIME_CONFIG="$ROOT/snaphost-backend/internal/runtime/config/config.go"
VERSION=v1.2.3
OLD_VERSION=v1.2.2
LEGACY_SHA=0123456789abcdef0123456789abcdef01234567
# Service accounts this environment's config expects its keys to belong to.
TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT
BIN="$TMP_ROOT/bin"
mkdir -p "$BIN"

# The services the manifest actually contains. The fake `docker` answers
# `config --services` from this and refuses `up` for anything else, so a script
# that names a service the manifest dropped fails here the way it would on the
# box — which is the bug this list was added after.
export MANIFEST_SERVICES="snaphost buildkitd"

cat >"$BIN/docker" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "docker $*" >>"${FAKE_LOG:?}"
# `docker login` is gone with the public package (Task 7). A fake that still
# answered it would keep a deleted step looking supported.
if [[ ${1:-} == login ]]; then echo "docker login is not used" >&2; exit 1; fi
if [[ ${1:-} == info ]]; then exit 0; fi
if [[ ${1:-} == image && ${2:-} == inspect ]]; then
  # check_images_present inspects without --format; pull_images inspects
  # with one. Only the former simulates a missing rollback image.
  if [[ "$*" != *--format* && ${MISSING_ROLLBACK_IMAGE:-0} == 1 ]]; then exit 1; fi
  echo 'repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; exit 0
fi
if [[ ${1:-} == inspect ]]; then
  if [[ "$*" == *'.State.Running'* ]]; then echo true
  elif [[ "$*" == *'.RestartCount'* ]]; then echo 0
  elif [[ ${FAIL_READINESS:-0} == 1 ]]; then echo unhealthy
  else echo healthy
  fi
  exit 0
fi
[[ ${1:-} == compose ]] || exit 0
shift
if [[ ${1:-} == version ]]; then echo 'Docker Compose version v5.1.1'; exit 0; fi
if [[ "$*" == *'up --help'* ]]; then echo '--wait'; exit 0; fi
if [[ "$*" == *'config --help'* ]]; then echo '--quiet'; exit 0; fi
op=
for arg in "$@"; do
  case "$arg" in config|pull|ps|exec|up|run) op=$arg; break;; esac
done
case "$op" in
  config)
    [[ ${FAIL_CONFIG:-0} != 1 ]] || exit 1
    if [[ "$*" == *'--services'* ]]; then
      printf '%s\n' $MANIFEST_SERVICES
    elif [[ "$*" == *'--images'* ]]; then
      version=${WRONG_IMAGE_VERSION:-${SNAPHOST_VERSION:?}}
      printf 'ghcr.io/acme/repo/%s:%s\n' snaphost "$version"
    elif [[ "$*" != *'--quiet'* ]]; then
      # snaphost publishes a port; the infrastructure service must not.
      # The rendered-compose check greps exactly this shape.
      printf 'services:\n  snaphost:\n    healthcheck:\n      test: [CMD, curl, --fail, http://127.0.0.1:8080/health]\n    ports:\n      - target: 8080\n  buildkitd:\n    image: buildkit\n'
    fi
    ;;
  pull) [[ ${FAIL_PULL:-0} != 1 ]] ;;
  ps) echo cid ;;
  exec)
    # Real `compose exec -T` forwards stdin to the container. Draining it here
    # is what makes the stdin-consumption regression test meaningful.
    cat >/dev/null 2>&1 || true
    [[ ${FAIL_BACKUP:-0} != 1 ]] || exit 1
    if [[ ${TRUNCATED_BACKUP:-0} == 1 ]]; then
      # What a dump cut short looks like: valid SQL, no terminating COMMIT.
      printf 'PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\nCREATE TABLE users (id text primary key);\n'
    else
      printf 'PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\nCREATE TABLE users (id text primary key);\nCOMMIT;\n'
    fi
    ;;
  run)
    # Real `compose run` attaches stdin too.
    cat >/dev/null 2>&1 || true
    if [[ "$*" == *'migrate'* && ${FAIL_MIGRATION:-0} == 1 ]]; then exit 1; fi
    exit 0
    ;;
  up)
    # A fake that accepts any service name is exactly why the rollback path kept
    # naming seven services deleted two commits earlier, with every test green.
    # Refuse what the real `compose up` would refuse: a service the manifest
    # does not contain.
    service=${!#}
    found=0
    for known in $MANIFEST_SERVICES; do
      [[ "$service" == "$known" ]] && found=1
    done
    if [[ $found -ne 1 ]]; then
      echo "no such service: $service" >&2
      exit 1
    fi
    exit 0
    ;;
esac
FAKE

cat >"$BIN/curl" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "curl $*" >>"${FAKE_LOG:?}"
[[ ${FAIL_SMOKE:-0} != 1 ]] || exit 22
if [[ "$*" == *'%{http_code}'* ]]; then
  [[ ${FAIL_AUTH_SMOKE:-0} != 1 ]] || exit 22
  printf 401
fi
FAKE
chmod +x "$BIN/docker" "$BIN/curl"

cat >"$BIN/ln" <<'FAKE'
#!/usr/bin/env bash
set -u
destination=${!#}
if [[ ${FAIL_CHECKSUM_PUBLISH:-0} == 1 && "$destination" == *.sha256 ]]; then
  exit 1
fi
exec /bin/ln "$@"
FAKE
chmod +x "$BIN/ln"

cat >"$BIN/chown" <<'FAKE'
#!/usr/bin/env bash
set -u
[[ ${FAIL_ENV_UPDATE:-0} != 1 ]] || exit 1
exec /usr/bin/chown "$@"
FAKE
chmod +x "$BIN/chown"

PASS=0
FAIL=0

setup_case() {
  CASE_DIR=$(mktemp -d "$TMP_ROOT/case.XXXXXX")
  mkdir -p "$CASE_DIR/state" "$CASE_DIR/backups"
  COMPOSE="$CASE_DIR/compose.yml"
  ENV_FILE="$CASE_DIR/production.env"
  FAKE_LOG="$CASE_DIR/commands.log"
  : >"$FAKE_LOG"
  : >"$COMPOSE"
  cat >"$ENV_FILE" <<EOF
SNAPHOST_VERSION=$OLD_VERSION
GHCR_IMAGE_PREFIX=ghcr.io/acme/repo
SNAPHOST_BIND_ADDRESS=0.0.0.0
SNAPHOST_PORT=8080
DOMAIN_SUFFIX=apps.prod.invalid
CORS_ALLOW_ORIGINS=https://app.prod.invalid
RUN_MIGRATIONS=false
SAGA_BUILD_TIMEOUT_MIN=15
SAGA_RESUME_INTERVAL_SEC=60
DEPLOY_DEFAULT_PORT=3000
LOG_LEVEL=info
MAX_REPO_SIZE_MB=500
MAX_BUILD_TIME_MIN=15
MAX_CONCURRENT_PER_USER=3
ALLOWED_GIT_HOSTS=github.com
ALLOWED_BASE_IMAGES=alpine:
ALLOWED_BASE_IMAGES_PERMISSIVE=alpine:
CONTAINER_CPU_LIMIT=0.5
CONTAINER_MEMORY_MB=512
CONTAINER_DEFAULT_TTL_MIN=1440
WATCHDOG_INTERVAL_SEC=30
STRICT_IMAGE_VALIDATION=true
ALLOWED_IMAGE_PREFIXES=snaphost/
RUNTIME_PROBE_ENABLED=true
RUNTIME_PROBE_TIMEOUT_SEC=45
RESERVED_DOMAINS=control.prod.invalid
DEPLOY_TTL_MIN=1440
DEPLOY_TTL_MAX_MIN=1440
DOMAIN_CNAME_TARGET=
DOMAIN_A_RECORD_TARGET=
MAX_DOMAINS_PER_USER=1
DOMAIN_ATTACH_REQUIRE_IDENTITY=false
DOMAIN_ATTACH_PER_HOUR=5
DOMAIN_VERIFY_INTERVAL_SEC=60
DOMAIN_REVERIFY_HOURS=24
DOMAIN_VERIFY_GRACE_HOURS=24
PROJECT_DEPLOY_RETENTION=3
OPENROUTER_API_KEY=openrouter-test-key
OPENROUTER_MODEL=openai/test
OPENROUTER_REFERER=https://control.prod.invalid
OPENROUTER_APP_NAME=SnapHost
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_JSON_MODE=true
LLM_TIMEOUT=30s
SNAPHOST_CPU_LIMIT=1
SNAPHOST_MEMORY_LIMIT=512M
BUILDKIT_CPU_LIMIT=4
BUILDKIT_MEMORY_LIMIT=4G
EOF
  chmod 600 "$ENV_FILE"
  export PATH="$BIN:$PATH" FAKE_LOG
  export SNAPHOST_COMPOSE_FILE="$COMPOSE" SNAPHOST_ENV_FILE="$ENV_FILE"
  export SNAPHOST_COMPOSE_PROJECT=snaphost-test
  export SNAPHOST_STATE_DIR="$CASE_DIR/state" SNAPHOST_BACKUP_DIR="$CASE_DIR/backups"
  export SNAPHOST_PUBLIC_SMOKE_URL=https://control.invalid
  export SNAPHOST_MIN_FREE_KB=0 SNAPHOST_READINESS_TIMEOUT=1 SNAPHOST_STABILITY_DELAY=0
  unset SNAPHOST_ALLOW_HTTP_SMOKE
  unset FAIL_CONFIG FAIL_PULL FAIL_BACKUP TRUNCATED_BACKUP FAIL_MIGRATION FAIL_READINESS FAIL_SMOKE FAIL_AUTH_SMOKE FAIL_CHECKSUM_PUBLISH FAIL_ENV_UPDATE MIGRATIONS_BACKWARD_COMPATIBLE MISSING_ROLLBACK_IMAGE WRONG_IMAGE_VERSION TMPDIR
}

# Deployment state as it looks after a successful release, which is the only
# state a rollback is ever launched from.
set_case_env_version() {
  sed -i "s/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=$1/" "$ENV_FILE"
}

seed_deployed_state() {
  local migration=${1:-not-started}
  set_case_env_version "$VERSION"
  cat >"$CASE_DIR/state/current.env" <<EOF
version=$VERSION
migration_status=$migration
status=success
EOF
  echo "version=$OLD_VERSION" >"$CASE_DIR/state/previous.env"
}

run_capture() {
  OUTPUT="$CASE_DIR/output"
  set +e
  "$SCRIPT" "$@" >"$OUTPUT" 2>&1
  RC=$?
  set -e
}

snapshot_case() {
  (cd "$CASE_DIR" && find . -type f ! -name output ! -name commands.log -print | sort | xargs sha256sum) | sha256sum
}

pass() { PASS=$((PASS + 1)); printf 'ok - %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'not ok - %s\n' "$1"; cat "$OUTPUT" 2>/dev/null || true; }
expect_failure() { local name=$1; shift; setup_case; "$@"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass "$name" || fail "$name"; }

setup_case; run_capture preflight bad; [[ $RC -ne 0 ]] && pass 'invalid version' || fail 'invalid version'
setup_case; run_capture preflight 1.2.3; [[ $RC -ne 0 ]] && pass 'version requires v prefix' || fail 'version requires v prefix'
setup_case; run_capture preflight v1.2; [[ $RC -ne 0 ]] && pass 'version requires three components' || fail 'version requires three components'
setup_case; run_capture preflight v01.2.3; [[ $RC -ne 0 ]] && pass 'version rejects leading zero' || fail 'version rejects leading zero'
setup_case; run_capture preflight latest; [[ $RC -ne 0 ]] && pass 'latest version is rejected' || fail 'latest version is rejected'
setup_case; run_capture preflight "$LEGACY_SHA"; [[ $RC -ne 0 ]] && pass 'SHA is not an upgrade target' || fail 'SHA is not an upgrade target'
setup_case; sed -i 's/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=latest/' "$ENV_FILE"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass 'floating version in env is rejected' || fail 'floating version in env is rejected'
setup_case; rm "$ENV_FILE"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass 'missing env' || fail 'missing env'
setup_case; echo 'PUBLIC_HOST=example.com' >>"$ENV_FILE"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass 'placeholder env' || fail 'placeholder env'
setup_case
cp "$ROOT/infra/.env.production.example" "$ENV_FILE"
sed -i \
  -e "s/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=$OLD_VERSION/" \
  -e 's/^DOMAIN_SUFFIX=.*/DOMAIN_SUFFIX=apps.prod.invalid/' \
  -e 's/^OPENROUTER_API_KEY=.*/OPENROUTER_API_KEY=key/' \
  "$ENV_FILE"
chmod 600 "$ENV_FILE"
run_capture preflight "$VERSION"
[[ $RC -eq 0 ]] && pass 'comments in the complete production template are not placeholders' || fail 'comments in the complete production template are not placeholders'
setup_case; chmod 640 "$ENV_FILE"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass '0640 production env rejected' || fail '0640 production env rejected'
setup_case; chmod 644 "$ENV_FILE"; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass '0644 production env rejected' || fail '0644 production env rejected'
setup_case; export FAIL_CONFIG=1; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass 'Compose validation failure' || fail 'Compose validation failure'
setup_case; export WRONG_IMAGE_VERSION=v1.2.30; run_capture preflight "$VERSION"; [[ $RC -ne 0 ]] && pass 'image tag must exactly match target version' || fail 'image tag must exactly match target version'

# The old readiness probe started this image with `--entrypoint curl`, although
# the image did not contain curl. The fake accepted any entrypoint, so every
# test passed while every real rollout failed after migrations. The runtime now
# owns a Docker healthcheck and contains the client that healthcheck executes;
# deploy.sh waits for that health state instead of inventing a second container.
if sed -n '/^FROM debian:12-slim/,$p' "$DOCKERFILE" | grep -qE '^[[:space:]]+curl[[:space:]]+\\$' \
  && sed -n '/^  snaphost:/,/^  [a-zA-Z0-9_-]*:/p' "$PROD_COMPOSE" | grep -Fq 'http://127.0.0.1:8080/health' \
  && ! grep -q -- '--entrypoint curl' "$SCRIPT"; then
  pass 'runtime image and production healthcheck share a real readiness client'
else
  fail 'runtime image and production healthcheck share a real readiness client'
fi

# The control process asks the host Docker daemon for this exact network name
# when it starts a user container. Compose prefixes an unnamed network with its
# project, which passed rendered-manifest checks but failed the first real VPS
# deploy with "network snaphost-net not found".
if sed -n '/^  control:$/,/^  data:$/p' "$PROD_COMPOSE" | grep -Fqx '    name: snaphost-net' \
  && grep -Fq 'const TraefikNetwork = "snaphost-net"' "$RUNTIME_CONFIG"; then
  pass 'production control network matches the host runtime network'
else
  fail 'production control network matches the host runtime network'
fi

# Task 7 item 4: preflight asserts three variables, not forty-five. Everything
# else has a default in the application, and an unset variable reaches it as an
# empty string, which its config readers already treat as unset.
setup_case
cat >"$ENV_FILE" <<EOF
SNAPHOST_VERSION=$OLD_VERSION
DOMAIN_SUFFIX=apps.prod.invalid
OPENROUTER_API_KEY=key
EOF
chmod 600 "$ENV_FILE"
run_capture preflight "$VERSION"
[[ $RC -eq 0 ]] && pass 'preflight passes on the three required variables alone' || fail 'preflight passes on the three required variables alone'

# ...and still refuses when one of the three is absent, which is what stops the
# shorter list from becoming no list.
for missing in SNAPHOST_VERSION DOMAIN_SUFFIX OPENROUTER_API_KEY; do
  setup_case
  cat >"$ENV_FILE" <<EOF
SNAPHOST_VERSION=$OLD_VERSION
DOMAIN_SUFFIX=apps.prod.invalid
OPENROUTER_API_KEY=key
EOF
  grep -v "^$missing=" "$ENV_FILE" >"$ENV_FILE.tmp" && mv "$ENV_FILE.tmp" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  run_capture preflight "$VERSION"
  [[ $RC -ne 0 ]] && pass "preflight refuses a missing $missing" || fail "preflight refuses a missing $missing"
done

# A host part-way through its first install has no public HTTPS yet. The public
# smoke is skipped rather than fatal, and says so — a deploy that verified less
# must not look identical to one that verified everything.
setup_case; unset SNAPHOST_PUBLIC_SMOKE_URL; run_capture deploy "$VERSION"
if [[ $RC -eq 0 ]] && grep -q 'Public smoke skipped' "$OUTPUT"; then
  pass 'deploy without a public smoke URL succeeds and announces the skip'
else
  fail 'deploy without a public smoke URL succeeds and announces the skip'
fi

# snaphostctl persists the smoke contract in production.env because systemd or
# a later operator shell does not inherit the environment of the first install.
setup_case
unset SNAPHOST_PUBLIC_SMOKE_URL
cat >>"$ENV_FILE" <<'EOF'
SNAPHOST_PUBLIC_SMOKE_URL=http://192.0.2.10:8080
SNAPHOST_ALLOW_HTTP_SMOKE=true
EOF
run_capture deploy "$VERSION"
if [[ $RC -eq 0 ]] && grep -q 'curl .*http://192.0.2.10:8080/health' "$FAKE_LOG"; then
  pass 'deploy reads the persisted HTTP smoke opt-in from the env file'
else
  fail 'deploy reads the persisted HTTP smoke opt-in from the env file'
fi

setup_case
run_capture preflight "$VERSION"
run_capture preflight "$OLD_VERSION"
projects=$(grep -o -- '--project-name [^ ]*' "$FAKE_LOG" | sort -u)
if [[ "$projects" == '--project-name snaphost-test' ]]; then pass 'Compose project is stable across versions'; else fail 'Compose project is stable across versions'; fi

setup_case; export FAIL_PULL=1; run_capture deploy "$VERSION"; if [[ $RC -ne 0 ]] && ! grep -q ' compose .* up ' "$FAKE_LOG"; then pass 'pull failure leaves runtime'; else fail 'pull failure leaves runtime'; fi
setup_case; export FAIL_BACKUP=1; run_capture deploy "$VERSION"; if [[ $RC -ne 0 ]] && ! grep -q ' compose .* up ' "$FAKE_LOG"; then pass 'backup failure stops rollout'; else fail 'backup failure stops rollout'; fi
# A dump cut short is the dangerous case, because it is not an error: sqlite3
# can exit 0 after printing part of one, and the result restores cleanly into a
# database missing whatever came after the cut. The terminating COMMIT is the
# only thing that says the read transaction finished.
setup_case; export TRUNCATED_BACKUP=1; run_capture deploy "$VERSION"
if [[ $RC -ne 0 ]] && ! grep -q ' compose .* up ' "$FAKE_LOG" && [[ -z $(find "$CASE_DIR/backups" -maxdepth 1 -name '*.sql' -print -quit) ]]; then
  pass 'truncated backup stops rollout and publishes nothing'
else
  fail 'truncated backup stops rollout and publishes nothing'
fi
setup_case; export FAIL_MIGRATION=1; run_capture deploy "$VERSION"; [[ $RC -ne 0 && -f "$CASE_DIR/state/in-progress.env" ]] && pass 'migration failure recorded' || fail 'migration failure recorded'
setup_case; export FAIL_READINESS=1; run_capture deploy "$VERSION"; [[ $RC -ne 0 ]] && pass 'readiness timeout' || fail 'readiness timeout'
setup_case; export FAIL_SMOKE=1; run_capture deploy "$VERSION"; [[ $RC -ne 0 ]] && pass 'smoke failure' || fail 'smoke failure'

setup_case
set_case_env_version "$VERSION"
cat >"$CASE_DIR/state/current.env" <<EOF
version=$VERSION
migration_status=not-started
EOF
echo "version=$OLD_VERSION" >"$CASE_DIR/state/previous.env"
run_capture rollback
[[ $RC -eq 0 ]] && pass 'rollback before migrations' || fail 'rollback before migrations'

setup_case
set_case_env_version "$VERSION"
cat >"$CASE_DIR/state/current.env" <<EOF
version=$VERSION
migration_status=applied
EOF
echo "version=$OLD_VERSION" >"$CASE_DIR/state/previous.env"
run_capture rollback
[[ $RC -ne 0 ]] && pass 'rollback blocked after migrations' || fail 'rollback blocked after migrations'

setup_case; run_capture --dry-run deploy "$VERSION"
leaked=0
for secret in production-password openrouter-test-key token; do
  if grep -R -Fq "$secret" "$CASE_DIR/state" "$OUTPUT"; then leaked=1; fi
done
if [[ $leaked -eq 0 ]]; then pass 'secrets absent from output/state'; else fail 'secrets absent from output/state'; fi

setup_case
rm -rf "$CASE_DIR/backups"
set_case_env_version "$VERSION"
cat >"$CASE_DIR/state/current.env" <<EOF
version=$VERSION
migration_status=not-started
EOF
echo "version=$OLD_VERSION" >"$CASE_DIR/state/previous.env"
before=$(snapshot_case)
run_capture --dry-run rollback
after=$(snapshot_case)
if [[ $RC -eq 0 && "$before" == "$after" && ! -e "$CASE_DIR/backups" && ! -e "$CASE_DIR/state/deploy.lock" ]]; then pass 'dry-run rollback is filesystem read-only'; else fail 'dry-run rollback is filesystem read-only'; fi

setup_case
export FAIL_MIGRATION=1
run_capture deploy "$VERSION"
rm -f "$CASE_DIR/state/in-progress.env"
run_capture deploy "$OLD_VERSION"
dumps=$(find "$CASE_DIR/backups" -maxdepth 1  -name '*.sql' | wc -l)
checksums=$(find "$CASE_DIR/backups" -maxdepth 1  -name '*.sql.sha256' | wc -l)
if [[ $dumps -eq 2 && $checksums -eq 2 ]]; then
  pass 'backup and checksum are not overwritten'
else
  printf 'backup counts: dumps=%s checksums=%s\n' "$dumps" "$checksums"
  find "$CASE_DIR/backups" -maxdepth 1 -type f -print
  find "$CASE_DIR/state" -maxdepth 1 -type f -print -exec sed -n '1,8p' {} \;
  cat "$FAKE_LOG"
  fail 'backup and checksum are not overwritten'
fi

setup_case
run_capture deploy "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ ! -e "$CASE_DIR/state/previous.env" ]] \
  && [[ $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$VERSION" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$ENV_FILE") == "$VERSION" ]]; then
  pass 'first deployment records and persists its version'
else fail 'first deployment records and persists its version'; fi

# Existing hosts have SHA-shaped state from the pre-Task-7 deploy script. A
# semantic-version deploy must consume that state and retain it as the one-time
# rollback target without making SHA a valid user-facing deploy argument.
setup_case
set_case_env_version "$LEGACY_SHA"
cat >"$CASE_DIR/state/current.env" <<EOF
sha=$LEGACY_SHA
migration_status=not-started
status=success
EOF
run_capture deploy "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$VERSION" ]] \
  && [[ $(awk -F= '$1=="sha"{print $2}' "$CASE_DIR/state/previous.env") == "$LEGACY_SHA" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$ENV_FILE") == "$VERSION" ]]; then
  pass 'semantic-version deploy preserves a legacy SHA rollback target'
else fail 'semantic-version deploy preserves a legacy SHA rollback target'; fi

export MIGRATIONS_BACKWARD_COMPATIBLE=true
run_capture rollback
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="sha"{print $2}' "$CASE_DIR/state/current.env") == "$LEGACY_SHA" ]] \
  && [[ $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/previous.env") == "$VERSION" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$ENV_FILE") == "$LEGACY_SHA" ]]; then
  pass 'legacy rollback remains available for the first SemVer upgrade'
else fail 'legacy rollback remains available for the first SemVer upgrade'; fi

setup_case
seed_deployed_state
set_case_env_version "$OLD_VERSION"
run_capture rollback
if [[ $RC -ne 0 ]] && grep -q 'does not match current deployment state' "$OUTPUT" \
  && ! grep -q ' compose .* up ' "$FAKE_LOG"; then
  pass 'env and state drift blocks rollback before runtime changes'
else fail 'env and state drift blocks rollback before runtime changes'; fi

setup_case
export FAIL_CHECKSUM_PUBLISH=1
run_capture deploy "$VERSION"
dumps=$(find "$CASE_DIR/backups" -maxdepth 1  -name '*.sql' | wc -l)
checksums=$(find "$CASE_DIR/backups" -maxdepth 1  -name '*.sql.sha256' | wc -l)
if [[ $RC -ne 0 && $dumps -eq 0 && $checksums -eq 0 ]]; then pass 'checksum publish failure removes current dump'; else fail 'checksum publish failure removes current dump'; fi

setup_case
(
  exec 8>"$CASE_DIR/state/deploy.lock"
  flock 8
  sleep 3
) &
lock_pid=$!
sleep 1
run_capture deploy "$VERSION"
kill "$lock_pid" 2>/dev/null || true
wait "$lock_pid" 2>/dev/null || true
[[ $RC -ne 0 ]] && pass 'concurrent deployment lock' || fail 'concurrent deployment lock'

# A non-recoverable unfinished state (migrations touched) must still block.
setup_case
cat >"$CASE_DIR/state/current.env" <<EOF
version=$VERSION
migration_status=applied
EOF
cat >"$CASE_DIR/state/in-progress.env" <<EOF
version=$VERSION
status=manual-intervention-required
migration_status=applied
EOF
run_capture deploy "$OLD_VERSION"
if [[ $RC -ne 0 ]] && grep -q 'unfinished deployment state exists' "$OUTPUT"; then pass 'manual-intervention state still blocks deploy'; else fail 'manual-intervention state still blocks deploy'; fi

# A failed-before-migrations state touched no database and is cleared automatically.
setup_case
cat >"$CASE_DIR/state/in-progress.env" <<EOF
version=$VERSION
status=failed-before-migrations
migration_status=not-started
EOF
run_capture deploy "$OLD_VERSION"
if grep -q 'Clearing safe failed-before-migrations' "$OUTPUT" && ! grep -q 'unfinished deployment state exists' "$OUTPUT"; then pass 'failed-before-migrations state auto-cleared'; else fail 'failed-before-migrations state auto-cleared'; fi

# --- stdin consumption ---------------------------------------------------
# CD runs this script as `ssh host bash -s <<EOF`, so the deployment commands
# and the script text share one stdin. `docker compose exec/run` forward stdin
# to the container; when they drained it, every line after the deploy call was
# silently discarded and the run still exited 0. That is how the
# /opt/snaphost/current symlink went missing on 2026-08-03 with a green CD run.

setup_case
marker="$CASE_DIR/after-deploy-marker"
printf '%s\n' \
  "\"$SCRIPT\" deploy \"$VERSION\" >/dev/null 2>&1" \
  "touch \"$marker\"" | bash -s
if [[ -f "$marker" ]]; then
  pass 'commands after a piped deploy still run (stdin is not consumed)'
else fail 'commands after a piped deploy still run (stdin is not consumed)'; fi

setup_case
marker="$CASE_DIR/after-rollback-marker"
seed_deployed_state
printf '%s\n' \
  "\"$SCRIPT\" rollback >/dev/null 2>&1" \
  "touch \"$marker\"" | bash -s
if [[ -f "$marker" ]]; then
  pass 'commands after a piped rollback still run'
else fail 'commands after a piped rollback still run'; fi

# --- rollback ------------------------------------------------------------
# The rollback path is the one an operator uses under pressure and the one
# least exercised in normal operation, so it carries its own scenarios rather
# than only the two guard checks above.

setup_case
seed_deployed_state applied
export MIGRATIONS_BACKWARD_COMPATIBLE=true
run_capture rollback
if [[ $RC -eq 0 ]] && grep -q "Rollback completed: $OLD_VERSION" "$OUTPUT"; then
  pass 'rollback after migrations proceeds once compatibility is confirmed'
else fail 'rollback after migrations proceeds once compatibility is confirmed'; fi

setup_case
seed_deployed_state
run_capture rollback
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$OLD_VERSION" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$ENV_FILE") == "$OLD_VERSION" ]] \
  && [[ $(awk -F= '$1=="status"{print $2}' "$CASE_DIR/state/current.env") == rollback-success ]]; then
  pass 'rollback records the restored version and a rollback status'
else fail 'rollback records the restored version and a rollback status'; fi

# Rolling back twice must return to where it started, otherwise an operator who
# rolls back one release too far has no way back.
setup_case
seed_deployed_state
run_capture rollback
run_capture rollback
if [[ $RC -eq 0 && $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$VERSION" ]]; then
  pass 'a second rollback returns to the original version'
else fail 'a second rollback returns to the original version'; fi

setup_case
seed_deployed_state applied
export MIGRATIONS_BACKWARD_COMPATIBLE=true
run_capture rollback
if [[ $(awk -F= '$1=="migration_status"{print $2}' "$CASE_DIR/state/current.env") == applied ]]; then
  pass 'rollback preserves the recorded migration status'
else fail 'rollback preserves the recorded migration status'; fi

setup_case
seed_deployed_state
rm -f "$CASE_DIR/state/previous.env"
run_capture rollback
if [[ $RC -ne 0 ]] && grep -q 'current/previous deployment state is unavailable' "$OUTPUT"; then
  pass 'rollback without previous state refuses'
else fail 'rollback without previous state refuses'; fi

# The recorded version is what gets deployed, so a corrupted state file must stop
# the rollback rather than resolve to some arbitrary image tag.
setup_case
seed_deployed_state
echo 'version=not-a-version' >"$CASE_DIR/state/previous.env"
run_capture rollback
if [[ $RC -ne 0 ]]; then pass 'rollback refuses a malformed previous version'; else fail 'rollback refuses a malformed previous version'; fi

setup_case
seed_deployed_state
export MISSING_ROLLBACK_IMAGE=1
run_capture rollback
if [[ $RC -ne 0 ]] && grep -q 'saved rollback image is unavailable locally' "$OUTPUT" \
  && ! grep -q ' compose .* up ' "$FAKE_LOG"; then
  pass 'rollback stops before touching the runtime when the image is gone'
else fail 'rollback stops before touching the runtime when the image is gone'; fi

# A rollback whose smoke fails has left the runtime on the previous images, so
# claiming success in the state file would misdirect the next operator.
setup_case
seed_deployed_state
export FAIL_SMOKE=1
run_capture rollback
if [[ $RC -ne 0 && $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$VERSION" ]]; then
  pass 'a failed rollback smoke does not record success'
else fail 'a failed rollback smoke does not record success'; fi

# If the runtime moved but the pinned env version cannot be published, leaving
# the old state behind would make the next operation believe the old runtime is
# still live. Roll back the runtime change while the old env/state still agree.
setup_case
seed_deployed_state
export FAIL_ENV_UPDATE=1
run_capture rollback
ups=$(grep -c ' compose .* up ' "$FAKE_LOG")
if [[ $RC -ne 0 && $ups -eq 4 ]] \
  && grep -q 'original runtime restored' "$OUTPUT" \
  && [[ $(awk -F= '$1=="version"{print $2}' "$CASE_DIR/state/current.env") == "$VERSION" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$ENV_FILE") == "$VERSION" ]]; then
  pass 'env update failure restores the original runtime and state contract'
else fail 'env update failure restores the original runtime and state contract'; fi

setup_case
seed_deployed_state
(
  exec 8>"$CASE_DIR/state/deploy.lock"
  flock 8
  sleep 3
) &
lock_pid=$!
sleep 1
run_capture rollback
kill "$lock_pid" 2>/dev/null || true
wait "$lock_pid" 2>/dev/null || true
if [[ $RC -ne 0 ]]; then pass 'rollback refuses while another operation holds the lock'; else fail 'rollback refuses while another operation holds the lock'; fi

setup_case
seed_deployed_state
run_capture rollback
leaked=0
for secret in production-password openrouter-test-key; do
  if grep -R -Fq "$secret" "$CASE_DIR/state" "$OUTPUT"; then leaked=1; fi
done
if [[ $leaked -eq 0 ]]; then pass 'rollback leaks no secret into output or state'; else fail 'rollback leaks no secret into output or state'; fi

printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[[ $FAIL -eq 0 ]]
