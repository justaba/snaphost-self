#!/usr/bin/env bash
set -Eeuo pipefail

COMPOSE_FILE=${SNAPHOST_COMPOSE_FILE:-/opt/snaphost/infra/docker-compose.prod.yml}
COMPOSE_PROJECT=${SNAPHOST_COMPOSE_PROJECT:-snaphost}
ENV_FILE=${SNAPHOST_ENV_FILE:-/opt/snaphost/env/production.env}
STATE_DIR=${SNAPHOST_STATE_DIR:-/opt/snaphost/state}
BACKUP_DIR=${SNAPHOST_BACKUP_DIR:-/opt/snaphost/backups}
GHCR_TOKEN_FILE=${SNAPHOST_GHCR_TOKEN_FILE:-/opt/snaphost/secrets/ghcr-token}
GHCR_USERNAME=${GHCR_USERNAME:-}
PUBLIC_SMOKE_URL=${SNAPHOST_PUBLIC_SMOKE_URL:-}
DRY_RUN=${SNAPHOST_DRY_RUN:-false}
ALLOW_HTTP_SMOKE=${SNAPHOST_ALLOW_HTTP_SMOKE:-false}
MIGRATIONS_BACKWARD_COMPATIBLE=${MIGRATIONS_BACKWARD_COMPATIBLE:-false}
MIN_FREE_KB=${SNAPHOST_MIN_FREE_KB:-5242880}
READINESS_TIMEOUT=${SNAPHOST_READINESS_TIMEOUT:-180}
STABILITY_DELAY=${SNAPHOST_STABILITY_DELAY:-5}
# Path inside the container, matching DATABASE_PATH in docker-compose.prod.yml.
# Overridable so the two can be moved together, not so they can drift.
DATABASE_PATH=${SNAPHOST_DATABASE_PATH:-/var/snaphost/data/snaphost.db}

# Rollout order, and the only list of services in this file. Infrastructure
# first, the application last, because `--no-deps` means Compose will not order
# them for us. EXPECTED_SERVICES is derived rather than written twice: a second
# list is what let the rollback path keep naming seven services that had not
# existed for two commits.
INFRA_SERVICES=(redis buildkitd)
EXPECTED_SERVICES=("${INFRA_SERVICES[@]}" snaphost)
SNAPHOST_IMAGES=(snaphost)
PHASE=preflight
TARGET_SHA=
PREVIOUS_SHA=
BACKUP_PATH=
IMAGE_DIGESTS=
SENSITIVE_TEMP_FILE=

cleanup_sensitive_temp() {
  if [[ -n "$SENSITIVE_TEMP_FILE" ]]; then
    rm -f -- "$SENSITIVE_TEMP_FILE"
    SENSITIVE_TEMP_FILE=
  fi
}
trap cleanup_sensitive_temp EXIT
trap 'cleanup_sensitive_temp; exit 130' INT
trap 'cleanup_sensitive_temp; exit 143' TERM

log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
action() {
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: $1"
  else
    shift
    "$@"
  fi
}

usage() {
  cat <<'EOF'
Usage: deploy.sh [--dry-run] preflight <40-char-git-sha>
       deploy.sh [--dry-run] deploy <40-char-git-sha>
       deploy.sh [--dry-run] rollback
EOF
}

env_value() {
  local key=$1
  awk -v key="$key" 'index($0, key "=")==1 {sub(/^[^=]*=/, ""); print; found=1; exit} END {if (!found) exit 1}' "$ENV_FILE"
}

require_env() {
  local key=$1 value
  value=$(env_value "$key") || die "required variable $key is missing from env file"
  [[ -n "$value" ]] || die "required variable $key is empty in env file"
}

check_protected_file() {
  local label=$1 path=$2 mode
  [[ -f "$path" && ! -L "$path" ]] || die "$label file does not exist or is not a regular file: $path"
  mode=$(stat -c '%a' "$path") || die "cannot inspect permissions for $label file: $path"
  case "$mode" in
    400|600) ;;
    *) die "$label file permissions must be 0600 or 0400: $path" ;;
  esac
}

compose() {
  SNAPHOST_VERSION="$TARGET_SHA" docker compose --project-name "$COMPOSE_PROJECT" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

validate_sha() {
  [[ ${1:-} =~ ^[[:xdigit:]]{40}$ ]] || die "version must be exactly 40 hexadecimal characters"
}

check_tools() {
  (( BASH_VERSINFO[0] >= 4 )) || die "Bash 4 or newer is required"
  local tool
  for tool in docker curl flock sha256sum awk grep sed stat df mktemp ln; do
    command -v "$tool" >/dev/null || die "required command is unavailable: $tool"
  done
  docker compose version >/dev/null || die "Docker Compose v2 is unavailable"
  docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
  docker compose up --help 2>&1 | grep -q -- '--wait' || die "Docker Compose does not support up --wait"
  docker compose config --help 2>&1 | grep -q -- '--quiet' || die "Docker Compose does not support config --quiet"
}

validate_rendered_compose() {
  local services images rendered service count=0
  compose config --quiet || die "Compose configuration validation failed"
  services=$(compose config --services) || die "cannot list Compose services"
  for service in "${EXPECTED_SERVICES[@]}"; do
    grep -qx "$service" <<<"$services" || die "expected Compose service is missing: $service"
    ((count += 1))
  done
  [[ $(wc -l <<<"$services" | tr -d ' ') -eq ${#EXPECTED_SERVICES[@]} ]] || die "Compose must contain exactly ${#EXPECTED_SERVICES[@]} default services"

  images=$(compose config --images) || die "cannot list Compose images"
  grep -Eq '(^|[/:])latest$' <<<"$images" && die "latest image tag is forbidden"
  local image
  for image in "${SNAPHOST_IMAGES[@]}"; do
    grep -Fq "/$image:$TARGET_SHA" <<<"$images" || die "SnapHost image $image is not pinned to target SHA"
  done
  grep -Eq '^[[:space:]]*build:' "$COMPOSE_FILE" && die "build directives are forbidden"

  rendered=$(mktemp)
  chmod 600 "$rendered"
  compose config >"$rendered" || { rm -f "$rendered"; die "cannot inspect rendered Compose"; }
  for service in "${INFRA_SERVICES[@]}"; do
    if sed -n "/^  $service:/,/^  [a-zA-Z0-9_-]*:/p" "$rendered" | grep -q '^    ports:'; then
      rm -f "$rendered"
      die "$service must not publish host ports"
    fi
  done
  rm -f "$rendered"
}

preflight() {
  TARGET_SHA=$1
  validate_sha "$TARGET_SHA"
  [[ "$COMPOSE_PROJECT" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || die "SNAPHOST_COMPOSE_PROJECT must match ^[a-z0-9][a-z0-9_-]{0,62}$"
  check_tools
  [[ -f "$COMPOSE_FILE" ]] || die "Compose file does not exist: $COMPOSE_FILE"
  check_protected_file "production env" "$ENV_FILE"
  if grep -Eqi 'replace-with-|example-|example\.com|example-registry' "$ENV_FILE"; then
    die "production env contains a template placeholder"
  fi
  local key
  for key in \
    SNAPHOST_VERSION GHCR_IMAGE_PREFIX API_GATEWAY_BIND_ADDRESS API_GATEWAY_PORT DOMAIN_SUFFIX \
    WEBHOOK_SECRET CORS_ALLOW_ORIGINS RUN_MIGRATIONS \
    SAGA_WORKER_ENABLED SAGA_BUILD_TIMEOUT_MIN SAGA_RESUME_INTERVAL_SEC \
    DEPLOY_DEFAULT_PORT LOG_LEVEL \
    REGISTRY_PREFIX MAX_REPO_SIZE_MB MAX_BUILD_TIME_MIN MAX_CONCURRENT_PER_USER \
    ALLOWED_GIT_HOSTS ALLOWED_BASE_IMAGES ALLOWED_BASE_IMAGES_PERMISSIVE REGISTRY_INSECURE SCAN_FAIL_ON_CRITICAL \
    CONTAINER_CPU_LIMIT CONTAINER_MEMORY_MB CONTAINER_DEFAULT_TTL_MIN WATCHDOG_INTERVAL_SEC STRICT_IMAGE_VALIDATION \
    RUNTIME_PROBE_ENABLED RUNTIME_PROBE_TIMEOUT_SEC RESERVED_DOMAINS \
    DEPLOY_TTL_MIN DEPLOY_TTL_MAX_MIN MAX_DOMAINS_PER_USER DOMAIN_ATTACH_REQUIRE_IDENTITY DOMAIN_ATTACH_PER_HOUR \
    DOMAIN_VERIFY_INTERVAL_SEC DOMAIN_REVERIFY_HOURS DOMAIN_VERIFY_GRACE_HOURS ALIAS_IDLE_GC_DAYS PROJECT_DEPLOY_RETENTION \
    LLM_BASE_URL LLM_JSON_MODE OPENROUTER_API_KEY OPENROUTER_MODEL OPENROUTER_REFERER OPENROUTER_APP_NAME LLM_TIMEOUT LLM_MAX_RETRIES \
    CACHE_TTL_DAYS MAX_FILE_SIZE_KB MAX_FILES_PER_REQUEST CONTROL_PLANE_CPU_LIMIT CONTROL_PLANE_MEMORY_LIMIT \
    REDIS_CPU_LIMIT REDIS_MEMORY_LIMIT BUILDKIT_CPU_LIMIT BUILDKIT_MEMORY_LIMIT; do
    require_env "$key"
  done
  # The builder and runner service-account keys, and the guard that checked a
  # key belonged to this environment, went with the cloud runtime. The
  # privilege they represented did not disappear — it moved to the Docker
  # socket the runner now mounts, which is a larger one and has no equivalent
  # identity check.
  if [[ -n "$GHCR_USERNAME" ]]; then
    check_protected_file "GHCR token" "$GHCR_TOKEN_FILE"
  fi
  [[ -n "$PUBLIC_SMOKE_URL" ]] || die "SNAPHOST_PUBLIC_SMOKE_URL is required"
  if [[ "$ALLOW_HTTP_SMOKE" != true && ! "$PUBLIC_SMOKE_URL" =~ ^https:// ]]; then
    die "public smoke URL must use HTTPS"
  fi
  validate_rendered_compose
  local free_kb disk_path requested
  for requested in "$STATE_DIR" "$BACKUP_DIR"; do
    disk_path=$requested
    while [[ ! -e "$disk_path" && "$disk_path" != / ]]; do disk_path=$(dirname "$disk_path"); done
    free_kb=$(df -Pk "$disk_path" 2>/dev/null | awk 'NR==2 {print $4}') || free_kb=0
    (( free_kb >= MIN_FREE_KB )) || die "insufficient free disk space for $requested"
  done
  log "Preflight passed for $TARGET_SHA"
}

atomic_state() {
  local file=$1; shift
  local tmp
  mkdir -p "$STATE_DIR"
  tmp=$(mktemp "$STATE_DIR/.state.XXXXXX")
  chmod 600 "$tmp"
  printf '%s\n' "$@" >"$tmp"
  mv -f "$tmp" "$STATE_DIR/$file"
}

state_value() {
  local file=$1 key=$2
  awk -F= -v key="$key" '$1==key {sub(/^[^=]*=/, ""); print; exit}' "$STATE_DIR/$file"
}

write_progress() {
  atomic_state in-progress.env \
    "sha=$TARGET_SHA" "previous_sha=$PREVIOUS_SHA" "started_at=$(date -u +%FT%TZ)" \
    "status=$1" "backup_path=$BACKUP_PATH" "migration_status=$2" "smoke_status=$3" \
    "image_digests=$IMAGE_DIGESTS"
}

acquire_lock() {
  mkdir -p "$STATE_DIR" "$BACKUP_DIR"
  exec 9>"$STATE_DIR/deploy.lock"
  flock -n 9 || die "another deployment holds $STATE_DIR/deploy.lock"
}

login_and_pull() {
  if [[ -n "$GHCR_USERNAME" ]]; then
    action "login to GHCR" docker login ghcr.io --username "$GHCR_USERNAME" --password-stdin <"$GHCR_TOKEN_FILE"
  else
    log "GHCR login skipped; using existing Docker credentials"
  fi
  action "pull all target images" compose pull
  [[ "$DRY_RUN" == true ]] && { IMAGE_DIGESTS="dry-run"; return; }
  local image digest entries=()
  while IFS= read -r image; do
    digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "$image") || die "cannot resolve digest for pulled image"
    [[ -n "$digest" && "$digest" != '<no value>' ]] || die "pulled image has no repository digest"
    entries+=("$digest")
  done < <(compose config --images)
  IMAGE_DIGESTS=$(IFS=,; echo "${entries[*]}")
}

ensure_previous_images() {
  [[ -n "$PREVIOUS_SHA" ]] || return 0
  local target=$TARGET_SHA
  TARGET_SHA=$PREVIOUS_SHA
  action "pull previous rollback images" compose pull
  TARGET_SHA=$target
}

check_images_present() {
  local image
  while IFS= read -r image; do
    docker image inspect "$image" >/dev/null 2>&1 || die "saved rollback image is unavailable locally: $image"
  done < <(compose config --images)
}

snaphost_running() {
  local cid
  cid=$(compose ps -q snaphost 2>/dev/null) || return 1
  [[ -n "$cid" ]] && [[ $(docker inspect -f '{{.State.Running}}' "$cid") == true ]]
}

# backup_database dumps the store before migrations run.
#
# The store used to be a PostgreSQL container this script could reach over a
# socket. It is a SQLite file in the application's own volume now, so the dump
# runs inside the application container — the only one that has the file. The
# tempting alternative, copying the volume, is wrong: under WAL the committed
# state is spread across the database and its -wal, and a copy of the two is a
# torn snapshot that restores without complaining.
backup_database() {
  if ! snaphost_running; then
    [[ -f "$STATE_DIR/current.env" ]] && die "the application is not running; refusing deployment with existing state"
    BACKUP_PATH=bootstrap-no-existing-database
    log "Bootstrap deployment: no existing container to back up"
    return
  fi
  local timestamp tmp final checksum checksum_tmp base
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  tmp=$(mktemp "$BACKUP_DIR/.database-${timestamp}-${TARGET_SHA}.XXXXXX")
  base=${tmp##*/}
  base=${base#.}
  final="$BACKUP_DIR/$base.sql"
  if [[ "$DRY_RUN" == true ]]; then
    rm -f -- "$tmp"
    BACKUP_PATH=$final
    log "DRY-RUN: create SQLite dump"
    return
  fi
  umask 077
  # </dev/null is load-bearing, not tidiness. `compose exec -T` forwards our
  # stdin to the container, and this script is routinely fed to a remote shell
  # as `ssh host bash -s <<EOF`. Without the redirect the dump consumes the
  # rest of that heredoc, so every line after the deploy call silently never
  # runs and the caller still sees exit 0 — which is exactly how the
  # `/opt/snaphost/current` symlink went missing on 2026-08-03.
  compose exec -T snaphost sqlite3 "$DATABASE_PATH" .dump >"$tmp" </dev/null || { rm -f "$tmp"; die "database backup failed"; }
  [[ -s "$tmp" ]] || { rm -f "$tmp"; die "database backup is empty"; }
  # sqlite3 exits 0 on some read failures after printing a partial dump, so the
  # terminating COMMIT is what says the transaction actually finished. A dump
  # cut short still restores — into a database missing whatever came after the
  # cut, which is the failure mode a backup exists to prevent.
  tail -n 1 "$tmp" | grep -qx 'COMMIT;' || { rm -f "$tmp"; die "database backup is truncated: no terminating COMMIT"; }
  chmod 600 "$tmp"
  ln -- "$tmp" "$final" || { rm -f -- "$tmp"; die "backup destination already exists: $final"; }
  rm -f -- "$tmp"
  checksum=$(sha256sum "$final" | awk '{print $1}')
  checksum_tmp=$(mktemp "$BACKUP_DIR/.checksum.XXXXXX")
  printf '%s  %s\n' "$checksum" "$(basename "$final")" >"$checksum_tmp"
  chmod 600 "$checksum_tmp"
  ln -- "$checksum_tmp" "${final}.sha256" || {
    rm -f -- "$checksum_tmp" "$final"
    die "backup checksum destination already exists: ${final}.sha256"
  }
  rm -f -- "$checksum_tmp"
  BACKUP_PATH=$final
}

wait_health() {
  local service=$1 deadline=$((SECONDS + READINESS_TIMEOUT)) cid status
  [[ "$DRY_RUN" == true ]] && { log "DRY-RUN: wait for $service readiness"; return; }
  while (( SECONDS < deadline )); do
    cid=$(compose ps -q "$service")
    if [[ -n "$cid" ]]; then
      status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid")
      [[ "$status" == healthy || "$status" == running ]] && return
      [[ "$status" == exited || "$status" == dead ]] && break
    fi
    sleep 2
  done
  die "readiness timeout for $service"
}

probe_internal() {
  local service=$1 url=$2 deadline=$((SECONDS + READINESS_TIMEOUT))
  [[ "$DRY_RUN" == true ]] && { log "DRY-RUN: probe $service"; return; }
  # Retry until the readiness deadline: a container can report healthy before
  # its HTTP listener is up, and a single-shot probe turned that into a false
  # deployment failure on 2026-07-12. The delay then was a JWKS prefetch from
  # Supabase; that is gone, but migrations and the operator bootstrap still run
  # before the listener binds.
  while (( SECONDS < deadline )); do
    # </dev/null for the same reason as backup_database: `compose run` attaches
    # our stdin to the container.
    if compose run --rm --no-deps --entrypoint curl snaphost --fail --silent --max-time 10 "$url" >/dev/null </dev/null; then
      return
    fi
    sleep 3
  done
  die "HTTP readiness failed for $service"
}

check_stable_container() {
  local service=$1 cid before after
  [[ "$DRY_RUN" == true ]] && { log "DRY-RUN: verify $service is stable"; return; }
  cid=$(compose ps -q "$service")
  [[ -n "$cid" ]] || die "$service container is missing"
  before=$(docker inspect -f '{{.RestartCount}}' "$cid")
  sleep "$STABILITY_DELAY"
  after=$(docker inspect -f '{{.RestartCount}}' "$cid")
  [[ "$before" == "$after" && $(docker inspect -f '{{.State.Running}}' "$cid") == true ]] || die "$service is stopped or in a restart loop"
}

rollout() {
  local service
  for service in "${INFRA_SERVICES[@]}"; do
    action "update $service" compose up -d --no-deps "$service"
    wait_health "$service"
  done

  PHASE=migrations-started
  write_progress migrations-running started pending
  # The application is deliberately still down here. Two containers holding one
  # SQLite file is the thing the RUN_MIGRATIONS=false setting exists to avoid,
  # and `run --rm` finishing is what sequences them.
  #
  # </dev/null: `compose run` attaches stdin, see backup_database.
  action "run migrations" compose --profile migration run --rm --no-deps snaphost-migrate </dev/null
  PHASE=migrations-applied
  write_progress rolling-out applied pending

  # One service, so the dependency-ordered rollout that used to publish the
  # gateway last collapses into a single step. What it cost to have seven was
  # a partially-updated control plane on any failure between them; what it
  # costs to have one is that the whole thing restarts at once.
  action "update snaphost" compose up -d --no-deps snaphost
  check_stable_container snaphost
  probe_internal snaphost http://snaphost:8080/health
}

smoke() {
  [[ "$DRY_RUN" == true ]] && { log "DRY-RUN: run public HTTPS smoke checks"; return; }
  curl --fail --silent --max-time 15 "${PUBLIC_SMOKE_URL%/}/health" >/dev/null || die "public API health smoke failed"
  local secret code
  secret=$(env_value WEBHOOK_SECRET)
  SENSITIVE_TEMP_FILE=$(mktemp)
  chmod 600 "$SENSITIVE_TEMP_FILE"
  printf 'header = "X-Webhook-Secret: %s"\n' "$secret" >"$SENSITIVE_TEMP_FILE"
  if ! code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 15 --config "$SENSITIVE_TEMP_FILE" "${PUBLIC_SMOKE_URL%/}/internal/routes?host=definitely-absent.invalid"); then
    cleanup_sensitive_temp
    die "authenticated route lookup smoke request failed"
  fi
  cleanup_sensitive_temp
  [[ "$code" == 404 ]] || die "authenticated route lookup smoke returned unexpected status $code"
  code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 15 -H 'X-Webhook-Secret: intentionally-wrong' "${PUBLIC_SMOKE_URL%/}/internal/routes?host=definitely-absent.invalid")
  [[ "$code" == 401 ]] || die "invalid-secret smoke returned unexpected status $code"
}

rollback_to() {
  local sha=$1 service
  validate_sha "$sha" || return 1
  TARGET_SHA=$sha
  # This list named seven services that stopped existing when the control plane
  # became one process, so the first `compose up -d --no-deps user-billing`
  # returned non-zero and every rollback failed at its first step. The tests did
  # not catch it because they fake `docker`, and a fake does not object to a
  # service that is not in the manifest. Rolling back is the operation nobody
  # exercises until they need it.
  for service in "${EXPECTED_SERVICES[@]}"; do
    action "rollback $service" compose up -d --no-deps "$service" || return 1
    wait_health "$service" || return 1
  done
  smoke || return 1
}

handle_failure() {
  local rc=$?
  trap - ERR
  if [[ "$PHASE" == migrations-started || "$PHASE" == migrations-applied ]]; then
    if [[ "$MIGRATIONS_BACKWARD_COMPATIBLE" == true && -n "$PREVIOUS_SHA" ]]; then
      log "Deployment failed after migrations; compatibility confirmed, rolling images back"
      if rollback_to "$PREVIOUS_SHA"; then
        write_progress rolled-back applied failed
      else
        write_progress manual-intervention-required applied failed
      fi
    else
      write_progress manual-intervention-required "$PHASE" failed
      printf 'ERROR: deployment failed after migrations; automatic image rollback is blocked.\nPrevious SHA: %s\nBackup: %s\n' "$PREVIOUS_SHA" "$BACKUP_PATH" >&2
    fi
  else
    write_progress failed-before-migrations not-started pending
  fi
  exit "$rc"
}

deploy() {
  TARGET_SHA=$1
  preflight "$TARGET_SHA"
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: lock, pull target and previous images, backup, migrations, ordered rollout, readiness, smoke, atomic state update"
    return
  fi
  acquire_lock
  if [[ -f "$STATE_DIR/in-progress.env" ]]; then
    # A failed-before-migrations attempt touched no database and left current.env
    # unchanged, so its state is safe to clear automatically — otherwise one
    # flaky deploy would block every later deploy until manual cleanup. Any other
    # unfinished status (migrations started/applied, manual-intervention-required)
    # still halts and requires an operator.
    local stale_status
    stale_status=$(state_value in-progress.env status)
    if [[ "$stale_status" == failed-before-migrations ]]; then
      log "Clearing safe failed-before-migrations state from a prior attempt"
      rm -f "$STATE_DIR/in-progress.env"
    else
      die "unfinished deployment state exists: $STATE_DIR/in-progress.env (status=$stale_status)"
    fi
  fi
  if [[ -f "$STATE_DIR/current.env" ]]; then PREVIOUS_SHA=$(state_value current.env sha); fi
  write_progress pulling not-started pending
  trap handle_failure ERR
  login_and_pull
  ensure_previous_images
  write_progress backed-up not-started pending
  backup_database
  write_progress backed-up not-started pending
  rollout
  smoke
  if [[ -n "$PREVIOUS_SHA" ]]; then
    validate_sha "$PREVIOUS_SHA"
    atomic_state previous.env "sha=$PREVIOUS_SHA" "replaced_at=$(date -u +%FT%TZ)"
  fi
  atomic_state current.env "sha=$TARGET_SHA" "deployed_at=$(date -u +%FT%TZ)" "status=success" "backup_path=$BACKUP_PATH" "migration_status=applied" "smoke_status=passed" "image_digests=$IMAGE_DIGESTS"
  rm -f "$STATE_DIR/in-progress.env"
  trap - ERR
  log "Deployment completed: $TARGET_SHA"
}

rollback() {
  if [[ "$DRY_RUN" != true ]]; then
    acquire_lock
  fi
  [[ -f "$STATE_DIR/current.env" && -f "$STATE_DIR/previous.env" ]] || die "current/previous deployment state is unavailable"
  local current previous migration
  current=$(state_value current.env sha)
  previous=$(state_value previous.env sha)
  migration=$(state_value current.env migration_status)
  validate_sha "$current"; validate_sha "$previous"
  if [[ "$migration" == applied && "$MIGRATIONS_BACKWARD_COMPATIBLE" != true ]]; then
    die "rollback blocked: migrations ran and backward compatibility is not confirmed"
  fi
  TARGET_SHA=$previous
  preflight "$TARGET_SHA"
  check_images_present
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: rollback to saved SHA, readiness, smoke, atomic state update"
    return
  fi
  rollback_to "$previous"
  atomic_state current.env "sha=$previous" "deployed_at=$(date -u +%FT%TZ)" "status=rollback-success" "migration_status=$migration" "smoke_status=passed"
  atomic_state previous.env "sha=$current" "replaced_at=$(date -u +%FT%TZ)"
  log "Rollback completed: $previous"
}

if [[ ${1:-} == --dry-run ]]; then DRY_RUN=true; shift; fi
command=${1:-}; shift || true
case "$command" in
  preflight) [[ $# -eq 1 ]] || { usage; exit 2; }; preflight "$1" ;;
  deploy) [[ $# -eq 1 ]] || { usage; exit 2; }; deploy "$1" ;;
  rollback) [[ $# -eq 0 ]] || { usage; exit 2; }; rollback ;;
  *) usage; exit 2 ;;
esac
