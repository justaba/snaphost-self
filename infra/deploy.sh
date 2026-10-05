#!/usr/bin/env bash
set -Eeuo pipefail

COMPOSE_FILE=${SNAPHOST_COMPOSE_FILE:-/opt/snaphost/infra/docker-compose.prod.yml}
COMPOSE_PROJECT=${SNAPHOST_COMPOSE_PROJECT:-snaphost}
ENV_FILE=${SNAPHOST_ENV_FILE:-/opt/snaphost/env/production.env}
STATE_DIR=${SNAPHOST_STATE_DIR:-/opt/snaphost/state}
BACKUP_DIR=${SNAPHOST_BACKUP_DIR:-/opt/snaphost/backups}
PUBLIC_SMOKE_URL=${SNAPHOST_PUBLIC_SMOKE_URL:-}
DRY_RUN=${SNAPHOST_DRY_RUN:-false}
ALLOW_HTTP_SMOKE=${SNAPHOST_ALLOW_HTTP_SMOKE:-}
MIGRATIONS_BACKWARD_COMPATIBLE=${MIGRATIONS_BACKWARD_COMPATIBLE:-false}
MIN_FREE_KB=${SNAPHOST_MIN_FREE_KB:-5242880}
READINESS_TIMEOUT=${SNAPHOST_READINESS_TIMEOUT:-180}
STABILITY_DELAY=${SNAPHOST_STABILITY_DELAY:-5}
# Path inside the container, matching DATABASE_PATH in docker-compose.prod.yml.
# Overridable so the two can be moved together, not so they can drift.
DATABASE_PATH=${SNAPHOST_DATABASE_PATH:-/var/snaphost/data/snaphost.db}

# Rollout order, and the only lists of services in this file. Infrastructure
# goes first, then the application, then its public edge, because `--no-deps`
# means Compose will not order them for us. EXPECTED_SERVICES is derived rather
# than written twice: a second
# list is what let the rollback path keep naming seven services that had not
# existed for two commits.
INFRA_SERVICES=(buildkitd)
EDGE_SERVICES=(caddy)
EXPECTED_SERVICES=("${INFRA_SERVICES[@]}" snaphost "${EDGE_SERVICES[@]}")
SNAPHOST_IMAGES=(snaphost)
PHASE=preflight
TARGET_VERSION=
PREVIOUS_VERSION=
BACKUP_PATH=
IMAGE_DIGESTS=
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
Usage: deploy.sh [--dry-run] preflight <vMAJOR.MINOR.PATCH>
       deploy.sh [--dry-run] deploy <vMAJOR.MINOR.PATCH>
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
  SNAPHOST_VERSION="$TARGET_VERSION" docker compose --project-name "$COMPOSE_PROJECT" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

is_version() {
  [[ ${1:-} =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

is_legacy_sha() {
  [[ ${1:-} =~ ^[[:xdigit:]]{40}$ ]]
}

validate_version() {
  is_version "$1" || die "version must match vMAJOR.MINOR.PATCH (for example v1.2.3)"
}

# State written before Task 7 used an exact Git SHA as the release identifier.
# It is accepted only while reading or preserving saved state, so an existing
# installation can upgrade once and still roll back. User-facing
# preflight/deploy commands never accept it; successful new deploys write a
# semantic version.
validate_saved_release() {
  is_version "$1" || is_legacy_sha "$1" || die "saved release must be vMAJOR.MINOR.PATCH or a legacy 40-character Git SHA"
}

check_tools() {
  (( BASH_VERSINFO[0] >= 4 )) || die "Bash 4 or newer is required"
  local tool
  for tool in docker curl flock sha256sum awk grep sed stat df mktemp ln chown; do
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
  local image rendered_image found
  for image in "${SNAPHOST_IMAGES[@]}"; do
    found=false
    while IFS= read -r rendered_image; do
      if [[ "$rendered_image" == */"$image:$TARGET_VERSION" ]]; then
        found=true
        break
      fi
    done <<<"$images"
    [[ "$found" == true ]] || die "SnapHost image $image is not pinned to target version"
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

preflight_checks() {
  [[ "$COMPOSE_PROJECT" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || die "SNAPHOST_COMPOSE_PROJECT must match ^[a-z0-9][a-z0-9_-]{0,62}$"
  check_tools
  [[ -f "$COMPOSE_FILE" ]] || die "Compose file does not exist: $COMPOSE_FILE"
  check_protected_file "production env" "$ENV_FILE"
  if awk '
    /^[[:space:]]*(#|$)/ { next }
    tolower($0) ~ /(replace-with-|example-|example\.com|example-registry)/ { found=1 }
    END { exit found ? 0 : 1 }
  ' "$ENV_FILE"; then
    die "production env contains a template placeholder"
  fi
  # Three required operator values; AI generation is a separate opt-in.
  #
  #   SNAPHOST_VERSION    the version to run; the whole point of an upgrade
  #   SNAPHOST_CONTROL_DOMAIN  the fixed panel/API hostname Caddy serves
  #   SNAPHOST_ACME_EMAIL      the ACME account contact Caddy uses
  #
  # The variables Compose itself interpolates — the image prefix, the published
  # address and port, both CPU and memory pairs — are not on this list either,
  # because they now carry defaults in the manifest. An empty value there would
  # render an invalid manifest rather than fall back, which is why they need the
  # default at that layer instead of an assertion at this one.
  local key configured_version configured_value
  for key in SNAPHOST_VERSION SNAPHOST_CONTROL_DOMAIN SNAPHOST_ACME_EMAIL; do
    require_env "$key"
  done
  local llm_enabled
  llm_enabled=$(env_value LLM_ENABLED 2>/dev/null || true)
  case "$llm_enabled" in
    ''|false) ;;
    true)
      require_env OPENROUTER_API_KEY
      [[ $(env_value OPENROUTER_API_KEY) =~ [^[:space:]] ]] || die "OPENROUTER_API_KEY is empty when LLM_ENABLED=true"
      ;;
    *) die "LLM_ENABLED must be true or false" ;;
  esac
  local acme_ca
  acme_ca=$(env_value SNAPHOST_ACME_CA 2>/dev/null || true)
  case "$acme_ca" in
    ''|https://acme-v02.api.letsencrypt.org/directory|https://acme-staging-v02.api.letsencrypt.org/directory) ;;
    *) die "SNAPHOST_ACME_CA must be the Let's Encrypt production or staging directory" ;;
  esac
  configured_version=$(env_value SNAPHOST_VERSION)
  is_version "$configured_version" || is_legacy_sha "$configured_version" || \
    die "SNAPHOST_VERSION in env file must be vMAJOR.MINOR.PATCH (or a legacy 40-character Git SHA during transition)"
  if [[ -z "$PUBLIC_SMOKE_URL" ]]; then
    PUBLIC_SMOKE_URL=$(env_value SNAPHOST_PUBLIC_SMOKE_URL 2>/dev/null || true)
  fi
  if [[ -z "$ALLOW_HTTP_SMOKE" ]]; then
    configured_value=$(env_value SNAPHOST_ALLOW_HTTP_SMOKE 2>/dev/null || true)
    ALLOW_HTTP_SMOKE=${configured_value:-false}
  fi
  # The Docker socket is the runtime's largest privilege and has no
  # environment-specific identity check.
  #
  # A public smoke URL is optional: a host part-way through its first install
  # has no public HTTPS yet, and refusing to deploy until it does would make
  # the first deploy the one an operator cannot perform. When it is set it is
  # still checked, and still has to be HTTPS unless explicitly allowed.
  if [[ -n "$PUBLIC_SMOKE_URL" && "$ALLOW_HTTP_SMOKE" != true && ! "$PUBLIC_SMOKE_URL" =~ ^https:// ]]; then
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
  log "Preflight passed for $TARGET_VERSION"
}

preflight() {
  TARGET_VERSION=$1
  validate_version "$TARGET_VERSION"
  preflight_checks
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

state_release() {
  local file=$1 value
  value=$(state_value "$file" version)
  if [[ -z "$value" ]]; then
    value=$(state_value "$file" sha)
  fi
  printf '%s\n' "$value"
}

assert_env_matches_current() {
  [[ -f "$STATE_DIR/current.env" ]] || return 0
  local configured current
  configured=$(env_value SNAPHOST_VERSION)
  current=$(state_release current.env)
  validate_saved_release "$current"
  [[ "$configured" == "$current" ]] || \
    die "SNAPHOST_VERSION in env file ($configured) does not match current deployment state ($current)"
}

set_env_release() {
  local version=$1 dir tmp
  validate_saved_release "$version"
  dir=$(dirname "$ENV_FILE")
  tmp=$(mktemp "$dir/.production.env.XXXXXX")
  if ! awk -v version="$version" '
    BEGIN { replaced=0 }
    /^SNAPHOST_VERSION=/ {
      if (!replaced) print "SNAPHOST_VERSION=" version
      replaced=1
      next
    }
    { print }
    END { if (!replaced) print "SNAPHOST_VERSION=" version }
  ' "$ENV_FILE" >"$tmp"; then
    rm -f -- "$tmp"
    die "cannot update SNAPHOST_VERSION in env file"
  fi
  chmod --reference="$ENV_FILE" "$tmp" || { rm -f -- "$tmp"; die "cannot preserve env file permissions"; }
  chown --reference="$ENV_FILE" "$tmp" || { rm -f -- "$tmp"; die "cannot preserve env file ownership"; }
  mv -f -- "$tmp" "$ENV_FILE" || { rm -f -- "$tmp"; die "cannot publish updated env file"; }
}

release_state_line() {
  local version=$1
  if is_version "$version"; then
    printf 'version=%s\n' "$version"
  else
    printf 'sha=%s\n' "$version"
  fi
}

write_progress() {
  atomic_state in-progress.env \
    "version=$TARGET_VERSION" "previous_version=$PREVIOUS_VERSION" "started_at=$(date -u +%FT%TZ)" \
    "status=$1" "backup_path=$BACKUP_PATH" "migration_status=$2" "smoke_status=$3" \
    "image_digests=$IMAGE_DIGESTS"
}

acquire_lock() {
  mkdir -p "$STATE_DIR" "$BACKUP_DIR"
  exec 9>"$STATE_DIR/deploy.lock"
  flock -n 9 || die "another deployment holds $STATE_DIR/deploy.lock"
}

# The package is public, so there is no `docker login`, no token file on the
# host and no credential for us to issue and rotate per operator. That was the
# registry half of Task 7's version decision.
#
# Pulled image digests are recorded. A
# version tag is immutable by convention, not by the registry; these hashes
# record what actually ran.
pull_images() {
  action "pull all target images" compose pull snaphost buildkitd caddy
  [[ "$DRY_RUN" == true ]] && { IMAGE_DIGESTS="dry-run"; return; }
  local image digest entries=()
  while IFS= read -r image; do
    digest=$(docker image inspect --format '{{index .RepoDigests 0}}' "$image") || die "cannot resolve digest for pulled image"
    [[ -n "$digest" && "$digest" != '<no value>' ]] || die "image has no digest or ID: $image"
    entries+=("$digest")
  done < <(compose config --images)
  IMAGE_DIGESTS=$(IFS=,; echo "${entries[*]}")
}

ensure_previous_images() {
  [[ -n "$PREVIOUS_VERSION" ]] || return 0
  local target=$TARGET_VERSION
  TARGET_VERSION=$PREVIOUS_VERSION
  action "pull previous rollback images" compose pull snaphost buildkitd caddy
  TARGET_VERSION=$target
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
  tmp=$(mktemp "$BACKUP_DIR/.database-${timestamp}-${TARGET_VERSION}.XXXXXX")
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
  # stdin to the container, so whenever this script is itself being read from
  # stdin — `ssh host bash -s <<EOF`, `curl … | bash`, a heredoc in someone's
  # runbook — the dump swallows the remaining lines. Every command after it
  # then silently never runs and the caller still sees exit 0. That is how the
  # `/opt/snaphost/current` symlink went missing on 2026-08-03, back when CI
  # deployed over SSH. CI no longer does, but an operator piping an install
  # script to a shell is the same hazard, so the redirect stays on every
  # `compose exec` and `compose run` in this file.
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
      [[ "$status" == healthy ]] && return
      [[ "$status" == exited || "$status" == dead ]] && break
    fi
    sleep 2
  done
  die "readiness timeout for $service"
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
    # Bind-mounted daemon configuration is not part of Compose's container
    # hash. Recreate infrastructure so a release that changes buildkitd.toml
    # actually applies it instead of waiting for the next host reboot.
    action "update $service" compose up -d --no-deps --force-recreate "$service"
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

  # The application is one process, so it is updated in one atomic container
  # restart after infrastructure and migrations are ready.
  action "update snaphost" compose up -d --no-deps snaphost
  wait_health snaphost
  check_stable_container snaphost

  # Caddy owns the public sockets and consumes a bind-mounted configuration.
  # Recreate it after the new application is healthy so config changes are
  # applied during both upgrades and same-version recovery runs.
  for service in "${EDGE_SERVICES[@]}"; do
    action "update $service" compose up -d --no-deps --force-recreate "$service"
    wait_health "$service"
    check_stable_container "$service"
  done
}

smoke() {
  [[ "$DRY_RUN" == true ]] && { log "DRY-RUN: run public HTTPS smoke checks"; return; }
  # Skipped rather than failed when no public URL is configured. The internal
  # readiness probe and the restart-loop check have already run by this point,
  # so what is lost is confirmation that the edge in front of this host works —
  # which is exactly what a box that has not been given one yet cannot show.
  # Announced, not silent: a deploy that verified less should say so.
  if [[ -z "$PUBLIC_SMOKE_URL" ]]; then
    log "Public smoke skipped: SNAPHOST_PUBLIC_SMOKE_URL is not set"
    return
  fi
  curl --fail --silent --max-time 15 "${PUBLIC_SMOKE_URL%/}/health" >/dev/null || die "public API health smoke failed"
  local code
  if ! code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 15 "${PUBLIC_SMOKE_URL%/}/api/v1/projects"); then
    die "authentication-boundary smoke request failed"
  fi
  [[ "$code" == 401 ]] || die "unauthenticated API smoke returned unexpected status $code"
}

rollback_to() {
  local version=$1 service
  validate_saved_release "$version" || return 1
  TARGET_VERSION=$version
  # Roll back the same manifest-derived service list used for rollout.
  for service in "${EXPECTED_SERVICES[@]}"; do
    action "rollback $service" compose up -d --no-deps --force-recreate "$service" || return 1
    wait_health "$service" || return 1
  done
  smoke || return 1
}

handle_failure() {
  local rc=$?
  trap - ERR
  if [[ "$PHASE" == migrations-started || "$PHASE" == migrations-applied ]]; then
    if [[ "$MIGRATIONS_BACKWARD_COMPATIBLE" == true && -n "$PREVIOUS_VERSION" ]]; then
      log "Deployment failed after migrations; compatibility confirmed, rolling images back"
      if rollback_to "$PREVIOUS_VERSION" && set_env_release "$PREVIOUS_VERSION"; then
        write_progress rolled-back applied failed
      else
        write_progress manual-intervention-required applied failed
      fi
    else
      write_progress manual-intervention-required "$PHASE" failed
      printf 'ERROR: deployment failed after migrations; automatic image rollback is blocked.\nPrevious version: %s\nBackup: %s\n' "$PREVIOUS_VERSION" "$BACKUP_PATH" >&2
    fi
  else
    write_progress failed-before-migrations not-started pending
  fi
  exit "$rc"
}

deploy() {
  TARGET_VERSION=$1
  preflight "$TARGET_VERSION"
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
  assert_env_matches_current
  if [[ -f "$STATE_DIR/current.env" ]]; then
    PREVIOUS_VERSION=$(state_release current.env)
    validate_saved_release "$PREVIOUS_VERSION"
  fi
  write_progress pulling not-started pending
  trap handle_failure ERR
  pull_images
  ensure_previous_images
  write_progress backed-up not-started pending
  backup_database
  write_progress backed-up not-started pending
  rollout
  smoke
  set_env_release "$TARGET_VERSION"
  if [[ -n "$PREVIOUS_VERSION" ]]; then
    atomic_state previous.env "$(release_state_line "$PREVIOUS_VERSION")" "replaced_at=$(date -u +%FT%TZ)"
  fi
  atomic_state current.env "version=$TARGET_VERSION" "deployed_at=$(date -u +%FT%TZ)" "status=success" "backup_path=$BACKUP_PATH" "migration_status=applied" "smoke_status=passed" "image_digests=$IMAGE_DIGESTS"
  rm -f "$STATE_DIR/in-progress.env"
  trap - ERR
  log "Deployment completed: $TARGET_VERSION"
}

rollback() {
  if [[ "$DRY_RUN" != true ]]; then
    acquire_lock
  fi
  [[ -f "$STATE_DIR/current.env" && -f "$STATE_DIR/previous.env" ]] || die "current/previous deployment state is unavailable"
  assert_env_matches_current
  local current previous migration
  current=$(state_release current.env)
  previous=$(state_release previous.env)
  migration=$(state_value current.env migration_status)
  validate_saved_release "$current"; validate_saved_release "$previous"
  if [[ "$migration" == applied && "$MIGRATIONS_BACKWARD_COMPATIBLE" != true ]]; then
    die "rollback blocked: migrations ran and backward compatibility is not confirmed"
  fi
  TARGET_VERSION=$previous
  preflight_checks
  check_images_present
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: rollback to saved version, readiness, smoke, atomic state update"
    return
  fi
  rollback_to "$previous"
  if ! set_env_release "$previous"; then
    log "Rollback version could not be persisted; restoring the original runtime"
    if rollback_to "$current"; then
      die "rollback cancelled because the env file could not be updated; original runtime restored"
    fi
    die "rollback changed the runtime but could not update the env file or restore the original runtime; manual intervention is required"
  fi
  atomic_state previous.env "$(release_state_line "$current")" "replaced_at=$(date -u +%FT%TZ)"
  atomic_state current.env "$(release_state_line "$previous")" "deployed_at=$(date -u +%FT%TZ)" "status=rollback-success" "migration_status=$migration" "smoke_status=passed"
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
