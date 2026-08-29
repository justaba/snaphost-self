#!/usr/bin/env bash
set -Eeuo pipefail

# Scheduled PostgreSQL backup for the SnapHost control plane.
#
# deploy.sh already dumps the database before migrations, but that is a
# deployment safety net, not a backup schedule: a week without a deploy is a
# week without a copy, and nothing ever prunes what it writes. This script is
# the schedule, the retention policy, and the off-host copy.
#
# It shares deploy.sh's lock, so a backup never runs against a database that
# is mid-migration. If a deployment holds the lock, this run exits successfully
# without a dump — that deployment is taking its own.
#
#   backup.sh [--dry-run] run             dump, verify, encrypt, upload, prune
#   backup.sh [--dry-run] tls             back up the TLS edge's certificate store
#   backup.sh [--dry-run] prune           apply retention only
#   backup.sh verify <dump>               checksum + structural check of one file
#   backup.sh list                        show what is on disk
#
# `tls` runs as root from its own timer, not as the deployment user. The
# certificate store is caddy:caddy 0700 and must stay that way: the deployer
# account is the one CD logs in as, and widening it to read TLS private keys
# would be a downgrade. Encryption therefore happens while the artifact is
# still root-owned, so plaintext key material never reaches a less-privileged
# account.

COMPOSE_FILE=${SNAPHOST_COMPOSE_FILE:-/opt/snaphost/infra/docker-compose.prod.yml}
COMPOSE_PROJECT=${SNAPHOST_COMPOSE_PROJECT:-snaphost}
ENV_FILE=${SNAPHOST_ENV_FILE:-/opt/snaphost/env/production.env}
STATE_DIR=${SNAPHOST_STATE_DIR:-/opt/snaphost/state}
BACKUP_DIR=${SNAPHOST_BACKUP_DIR:-/opt/snaphost/backups}
DRY_RUN=${SNAPHOST_DRY_RUN:-false}

# Retention. Scheduled dumps are the ones this script owns. Deploy dumps are
# kept much longer and pruned far more conservatively: each one is the last
# state before a specific migration ran.
KEEP_DAYS=${SNAPHOST_BACKUP_KEEP_DAYS:-14}
KEEP_MIN=${SNAPHOST_BACKUP_KEEP_MIN:-7}
DEPLOY_KEEP_DAYS=${SNAPHOST_DEPLOY_BACKUP_KEEP_DAYS:-90}
DEPLOY_KEEP_MIN=${SNAPHOST_DEPLOY_BACKUP_KEEP_MIN:-5}

# Encryption. The host holds only the public key, so a compromised host cannot
# read the backups it produced yesterday. Required before anything leaves the
# machine.
AGE_RECIPIENT=${SNAPHOST_BACKUP_AGE_RECIPIENT:-}

# Off-host copy. S3-compatible; Yandex Object Storage is the expected target,
# with credentials in the standard AWS environment/profile files.
REMOTE=${SNAPHOST_BACKUP_REMOTE:-}
S3_ENDPOINT=${SNAPHOST_BACKUP_S3_ENDPOINT:-https://storage.yandexcloud.net}

# Dead-man's switch. Pinged only after a backup is verified, uploaded, and
# pruned, so a silent failure shows up as a missing ping rather than nothing.
HEARTBEAT_URL=${SNAPHOST_BACKUP_HEARTBEAT_URL:-}

LOCK_WAIT=${SNAPHOST_BACKUP_LOCK_WAIT:-300}
MIN_FREE_KB=${SNAPHOST_BACKUP_MIN_FREE_KB:-2097152}

# Caddy's certificate store for the custom-domain edge (ADR 0007): issued
# certificates, their private keys, and the ACME account key. Losing it means
# every customer domain re-issues at once on its next request, against Let's
# Encrypt limits we do not control — and the visible symptom is a browser
# security warning on someone else's published site, not a 404.
TLS_STATE_DIR=${SNAPHOST_TLS_STATE_DIR:-/var/lib/caddy/.local/share/caddy}
TLS_KEEP_DAYS=${SNAPHOST_TLS_BACKUP_KEEP_DAYS:-30}
TLS_KEEP_MIN=${SNAPHOST_TLS_BACKUP_KEEP_MIN:-7}

# Losing any of these is unrecoverable: they carry money, ownership, and what
# is currently published. The ai_* tables are cache and accounting logs and are
# deliberately not required — a dump that lost them is still worth keeping.
REQUIRED_TABLES=(wallets transactions deploys deploy_sagas api_keys projects custom_domains)

TEMP_FILES=()

cleanup_temp() {
  local f
  for f in "${TEMP_FILES[@]:-}"; do
    [[ -n "$f" ]] && rm -f -- "$f"
  done
  TEMP_FILES=()
}
trap cleanup_temp EXIT
trap 'cleanup_temp; exit 130' INT
trap 'cleanup_temp; exit 143' TERM

log() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: backup.sh [--dry-run] run
       backup.sh [--dry-run] tls
       backup.sh [--dry-run] prune
       backup.sh verify <dump-path>
       backup.sh list
EOF
}

env_value() {
  local key=$1
  awk -v key="$key" 'index($0, key "=")==1 {sub(/^[^=]*=/, ""); print; found=1; exit} END {if (!found) exit 1}' "$ENV_FILE"
}

state_value() {
  local file=$1 key=$2
  [[ -f "$STATE_DIR/$file" ]] || return 1
  awk -F= -v key="$key" '$1==key {print $2; found=1; exit} END {if (!found) exit 1}' "$STATE_DIR/$file"
}

# Compose renders image references from SNAPHOST_VERSION. Without it every
# service resolves to an empty tag and even `ps` fails, so resolve the running
# version the same way deploy.sh records it.
resolve_version() {
  local version
  if version=$(state_value current.env sha 2>/dev/null) && [[ -n "$version" ]]; then
    printf '%s' "$version"
    return
  fi
  if version=$(env_value SNAPHOST_VERSION 2>/dev/null) && [[ -n "$version" ]]; then
    printf '%s' "$version"
    return
  fi
  die "cannot determine the deployed version from $STATE_DIR/current.env or $ENV_FILE"
}

compose() {
  SNAPHOST_VERSION="$VERSION" docker compose --project-name "$COMPOSE_PROJECT" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

check_tools() {
  (( BASH_VERSINFO[0] >= 4 )) || die "Bash 4 or newer is required"
  local tool
  for tool in docker flock sha256sum awk date find stat df mktemp ln; do
    command -v "$tool" >/dev/null || die "required command is unavailable: $tool"
  done
  docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
  [[ -f "$ENV_FILE" ]] || die "env file does not exist: $ENV_FILE"
  [[ -d "$BACKUP_DIR" ]] || die "backup directory does not exist: $BACKUP_DIR"

  # Refuse to produce a plaintext copy destined for someone else's disk.
  if [[ -n "$REMOTE" ]]; then
    [[ -n "$AGE_RECIPIENT" ]] || die "SNAPHOST_BACKUP_REMOTE is set without SNAPHOST_BACKUP_AGE_RECIPIENT; refusing to upload an unencrypted backup"
    command -v aws >/dev/null || die "required command is unavailable: aws (needed for the off-host copy)"
  fi
  if [[ -n "$AGE_RECIPIENT" ]]; then
    command -v age >/dev/null || die "required command is unavailable: age (needed for backup encryption)"
  fi
}

check_space() {
  local free
  free=$(df -Pk "$BACKUP_DIR" | awk 'NR==2 {print $4}')
  (( free >= MIN_FREE_KB )) || die "insufficient free space in $BACKUP_DIR: ${free}KB available, ${MIN_FREE_KB}KB required"
}

postgres_running() {
  local cid
  cid=$(compose ps -q postgres 2>/dev/null) || return 1
  [[ -n "$cid" ]] && [[ $(docker inspect -f '{{.State.Running}}' "$cid") == true ]]
}

# A dump that restores to an empty schema is worse than no dump, because it
# looks like a backup. Check the archive's table of contents before publishing.
verify_archive() {
  local path=$1 toc table
  toc=$(compose exec -T postgres pg_restore --list <"$path" 2>/dev/null) || die "backup is not a readable PostgreSQL archive: $path"
  for table in "${REQUIRED_TABLES[@]}"; do
    grep -Eq "TABLE DATA [^ ]+ $table " <<<"$toc" || die "backup is missing table data for $table: $path"
  done
}

publish() {
  local tmp=$1 final=$2
  ln -- "$tmp" "$final" || die "backup destination already exists: $final"
  rm -f -- "$tmp"
}

write_checksum() {
  local final=$1 checksum tmp
  checksum=$(sha256sum "$final" | awk '{print $1}')
  tmp=$(mktemp "$BACKUP_DIR/.checksum.XXXXXX")
  TEMP_FILES+=("$tmp")
  printf '%s  %s\n' "$checksum" "$(basename "$final")" >"$tmp"
  chmod 600 "$tmp"
  ln -- "$tmp" "${final}.sha256" || die "checksum destination already exists: ${final}.sha256"
  rm -f -- "$tmp"
}

upload() {
  local path=$1 name
  [[ -n "$REMOTE" ]] || return 0
  name=$(basename "$path")
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: upload $name to ${REMOTE%/}/"
    return 0
  fi
  aws --endpoint-url "$S3_ENDPOINT" s3 cp --only-show-errors "$path" "${REMOTE%/}/$name" \
    || die "off-host upload failed for $name"
  aws --endpoint-url "$S3_ENDPOINT" s3api head-object --bucket "$(remote_bucket)" --key "$(remote_prefix)$name" >/dev/null \
    || die "off-host upload could not be confirmed for $name"
  log "Uploaded $name to ${REMOTE%/}/"
}

remote_bucket() { local r=${REMOTE#s3://}; printf '%s' "${r%%/*}"; }
remote_prefix() {
  local r=${REMOTE#s3://} prefix
  prefix=${r#*/}
  [[ "$prefix" == "$r" ]] && { printf ''; return; }
  printf '%s/' "${prefix%/}"
}

# Never prune a dump the deployment state still points at: those are the two
# restore targets an operator reaches for during an incident.
referenced_backups() {
  local file path
  for file in current.env previous.env in-progress.env; do
    path=$(state_value "$file" backup_path 2>/dev/null) || continue
    [[ -n "$path" && "$path" != bootstrap-no-existing-database ]] && basename "$path"
  done
}

prune_group() {
  local pattern=$1 keep_days=$2 keep_min=$3 label=$4
  local -a files=()
  local file removed=0 total kept referenced
  referenced=$(referenced_backups || true)

  # Checksums are pruned with the dump they belong to, never counted as
  # backups of their own — otherwise the retention minimum protects a pile of
  # 70-byte text files instead of the dumps.
  while IFS= read -r file; do
    [[ -n "$file" ]] && files+=("$file")
  done < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name "$pattern" ! -name '*.sha256' -printf '%T@ %p\n' 2>/dev/null | sort -rn | awk '{print $2}')

  total=${#files[@]}
  (( total == 0 )) && { log "Retention ($label): nothing on disk"; return 0; }
  kept=$total

  local index=0
  for file in "${files[@]}"; do
    index=$((index + 1))
    # Newest first: the first keep_min are always kept, whatever their age.
    (( index <= keep_min )) && continue
    if grep -Fqx "$(basename "$file")" <<<"$referenced" 2>/dev/null; then
      continue
    fi
    if [[ -n $(find "$file" -maxdepth 0 -mtime "+$keep_days" -print 2>/dev/null) ]]; then
      if [[ "$DRY_RUN" == true ]]; then
        log "DRY-RUN: prune $(basename "$file")"
      else
        rm -f -- "$file" "${file}.sha256"
      fi
      removed=$((removed + 1))
      kept=$((kept - 1))
    fi
  done
  log "Retention ($label): kept $kept, removed $removed, policy ${keep_days}d / min ${keep_min}"
}

prune() {
  prune_group 'scheduled-*.dump*' "$KEEP_DAYS" "$KEEP_MIN" scheduled
  prune_group 'postgres-*.dump' "$DEPLOY_KEEP_DAYS" "$DEPLOY_KEEP_MIN" pre-deploy
}

heartbeat() {
  [[ -n "$HEARTBEAT_URL" ]] || return 0
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: ping the backup heartbeat"
    return 0
  fi
  command -v curl >/dev/null || { log "WARNING: curl is unavailable; heartbeat not sent"; return 0; }
  # A failed ping must not fail the backup that already succeeded.
  curl --fail --silent --max-time 15 --output /dev/null "$HEARTBEAT_URL" \
    || log "WARNING: backup heartbeat ping failed"
}

record_state() {
  local artifact=$1 bytes=$2 tmp
  tmp=$(mktemp "$STATE_DIR/.last-backup.XXXXXX")
  TEMP_FILES+=("$tmp")
  chmod 600 "$tmp"
  {
    printf 'artifact=%s\n' "$(basename "$artifact")"
    printf 'completed_at=%s\n' "$(date -u +%FT%TZ)"
    printf 'bytes=%s\n' "$bytes"
    printf 'encrypted=%s\n' "$([[ -n "$AGE_RECIPIENT" ]] && echo true || echo false)"
    printf 'offhost=%s\n' "$([[ -n "$REMOTE" ]] && echo true || echo false)"
  } >"$tmp"
  mv -- "$tmp" "$STATE_DIR/last-backup.env"
  TEMP_FILES=()
}

run() {
  check_tools
  check_space

  # A dry run touches nothing: it does not take the lock, because creating the
  # lock file is itself a write, and an operator checking configuration must
  # not be able to disturb a real deployment.
  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: take the lock, dump PostgreSQL, verify the archive, encrypt, publish, upload"
    prune
    return 0
  fi

  mkdir -p "$STATE_DIR"
  # Shared with deploy.sh: a dump taken while migrations are running is not a
  # consistent restore point for either schema.
  exec 9>"$STATE_DIR/deploy.lock"
  if ! flock -w "$LOCK_WAIT" 9; then
    log "A deployment holds the lock and is taking its own backup; skipping this scheduled run"
    return 0
  fi

  postgres_running || die "PostgreSQL is not running; no backup was taken"

  local timestamp tmp base final artifact bytes
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  tmp=$(mktemp "$BACKUP_DIR/.scheduled-${timestamp}.XXXXXX")
  TEMP_FILES+=("$tmp")
  base=${tmp##*/}
  base=${base#.}
  final="$BACKUP_DIR/$base.dump"

  umask 077
  # </dev/null: `compose exec -T` forwards our stdin to the container, which
  # would swallow the rest of this script when it is piped to a shell.
  compose exec -T postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB"' >"$tmp" </dev/null \
    || die "PostgreSQL dump failed"
  [[ -s "$tmp" ]] || die "PostgreSQL dump is empty"
  chmod 600 "$tmp"
  verify_archive "$tmp"

  if [[ -n "$AGE_RECIPIENT" ]]; then
    local encrypted="$tmp.age"
    TEMP_FILES+=("$encrypted")
    age -r "$AGE_RECIPIENT" -o "$encrypted" "$tmp" || die "backup encryption failed"
    [[ -s "$encrypted" ]] || die "encrypted backup is empty"
    chmod 600 "$encrypted"
    rm -f -- "$tmp"
    final="$final.age"
    publish "$encrypted" "$final"
  else
    publish "$tmp" "$final"
  fi
  TEMP_FILES=()

  write_checksum "$final"
  artifact=$final
  bytes=$(stat -c '%s' "$final")
  log "Backup written: $(basename "$artifact") (${bytes} bytes)"

  upload "$artifact"
  upload "${artifact}.sha256"
  prune
  record_state "$artifact" "$bytes"
  heartbeat
  log "Scheduled backup completed"
}

# back_up_tls archives the TLS edge's certificate store.
#
# Unlike the database dump, this artifact is *entirely* key material, so
# encryption is not optional here: there is no version of it that is safe to
# write in the clear, even on the host that already holds the originals.
back_up_tls() {
  (( BASH_VERSINFO[0] >= 4 )) || die "Bash 4 or newer is required"
  local tool
  for tool in tar age sha256sum awk date find stat df mktemp ln; do
    command -v "$tool" >/dev/null || die "required command is unavailable: $tool"
  done
  [[ -n "$AGE_RECIPIENT" ]] || die "SNAPHOST_BACKUP_AGE_RECIPIENT is required for the TLS store: this artifact is private keys and is never written unencrypted"
  [[ -d "$TLS_STATE_DIR" ]] || die "TLS state directory does not exist: $TLS_STATE_DIR"
  [[ -d "$BACKUP_DIR" ]] || die "backup directory does not exist: $BACKUP_DIR"
  if [[ -n "$REMOTE" ]]; then
    command -v aws >/dev/null || die "required command is unavailable: aws (needed for the off-host copy)"
  fi
  check_space

  local timestamp final tmp bytes
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  final="$BACKUP_DIR/tls-${timestamp}.tar.gz.age"

  if [[ "$DRY_RUN" == true ]]; then
    log "DRY-RUN: archive $TLS_STATE_DIR, encrypt, publish, upload"
    prune_group 'tls-*.tar.gz.age' "$TLS_KEEP_DAYS" "$TLS_KEEP_MIN" tls
    return 0
  fi

  umask 077
  tmp=$(mktemp "$BACKUP_DIR/.tls-${timestamp}.XXXXXX")
  TEMP_FILES+=("$tmp")
  # Piped straight into age: the plaintext archive is never a file on disk,
  # not even briefly, not even root-owned.
  if ! tar -czf - -C "$(dirname "$TLS_STATE_DIR")" "$(basename "$TLS_STATE_DIR")" | age -r "$AGE_RECIPIENT" -o "$tmp"; then
    die "TLS state archive failed"
  fi
  [[ -s "$tmp" ]] || die "TLS state archive is empty"
  chmod 600 "$tmp"
  publish "$tmp" "$final"
  TEMP_FILES=()

  write_checksum "$final"
  bytes=$(stat -c '%s' "$final")
  log "TLS state archived: $(basename "$final") (${bytes} bytes)"

  upload "$final"
  upload "${final}.sha256"
  prune_group 'tls-*.tar.gz.age' "$TLS_KEEP_DAYS" "$TLS_KEEP_MIN" tls
  heartbeat
  log "TLS state backup completed"
}

verify_one() {
  local path=$1
  [[ -f "$path" ]] || die "no such backup: $path"
  ( cd "$(dirname "$path")" && sha256sum --check --status "$(basename "$path").sha256" ) \
    || die "checksum mismatch or missing checksum for $path"
  log "Checksum OK: $(basename "$path")"
  if [[ "$path" == *.age ]]; then
    log "Encrypted archive; decrypt with the offline key before a structural check"
    return 0
  fi
  VERSION=$(resolve_version)
  verify_archive "$path"
  log "Archive structure OK: all required tables present"
}

list_backups() {
  local file
  printf '%-46s %12s %s\n' NAME BYTES MODIFIED
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    printf '%-46s %12s %s\n' "$(basename "$file")" "$(stat -c '%s' "$file")" "$(date -u -r "$file" +%FT%TZ)"
  done < <(find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'scheduled-*.dump*' -o -name 'postgres-*.dump' -o -name 'tls-*.tar.gz.age' \) ! -name '*.sha256' -printf '%T@ %p\n' | sort -rn | awk '{print $2}')
}

if [[ ${1:-} == --dry-run ]]; then DRY_RUN=true; shift; fi
command=${1:-}; shift || true
case "$command" in
  run) [[ $# -eq 0 ]] || { usage; exit 2; }; VERSION=$(resolve_version); run ;;
  tls) [[ $# -eq 0 ]] || { usage; exit 2; }; back_up_tls ;;
  prune) [[ $# -eq 0 ]] || { usage; exit 2; }; prune ;;
  verify) [[ $# -eq 1 ]] || { usage; exit 2; }; verify_one "$1" ;;
  list) [[ $# -eq 0 ]] || { usage; exit 2; }; list_backups ;;
  *) usage; exit 2 ;;
esac
