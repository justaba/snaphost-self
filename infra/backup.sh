#!/usr/bin/env bash
set -Eeuo pipefail

# Scheduled database backup for the SnapHost control plane.
#
# deploy.sh already dumps the database before migrations, but that is a
# deployment safety net, not a backup schedule: a week without a deploy is a
# week without a copy, and nothing ever prunes what it writes. This script is
# the schedule, the retention policy, and the off-host copy.
#
# The store is one SQLite file (item 6), so the dump runs inside the
# application container rather than against a database over a socket. Copying
# the volume is the obvious alternative and it is wrong: under WAL the
# committed state is spread across the database and its -wal, and a copy of the
# two restores as a torn snapshot without reporting anything.
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
# `tls` runs as root from its own timer. The Compose Caddy store is root-owned
# and 0700; widening it so an ordinary host account can read TLS private keys
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
TLS_STATE_DIR=${SNAPHOST_TLS_STATE_DIR:-/opt/snaphost/state/caddy/data}
TLS_KEEP_DAYS=${SNAPHOST_TLS_BACKUP_KEEP_DAYS:-30}
TLS_KEEP_MIN=${SNAPHOST_TLS_BACKUP_KEEP_MIN:-7}

# Path inside the container, matching DATABASE_PATH in docker-compose.prod.yml.
DATABASE_PATH=${SNAPHOST_DATABASE_PATH:-/var/snaphost/data/snaphost.db}

# Losing any of these is unrecoverable: they carry identity, ownership, and
# what is currently published. The ai_* tables are a cache and an accounting
# log and are deliberately not required — a dump that lost them is still worth
# keeping. schema_migrations is not required either: it is reconstructible.
#
# `wallets` and `transactions` were in this list until now, and they were
# dropped with billing in item 3 — so verify_archive looked for table data that
# could not exist and every backup would have been refused as incomplete. The
# test suite passed because its fake dump was written from this same list.
# `users` is here in their place: it is where the operator's password hash
# lives, and losing it means losing the ability to log in.
#
# `admin_audit_log` is here because it is the only record of destructive
# operator actions, and a project deletion is not recoverable from the panel.
# A backup that silently lost it would keep the effect and drop the account of
# who caused it, which is the one thing an audit row exists to prevent.
#
# Note the shape of the hazard this list keeps re-creating: the suite's fake
# dump is written from this same list by hand, so the check and its fixture
# agree with each other and nothing else. Changing this line means changing
# `write_tables` in infra/tests/backup_test.sh, and the fact that both have to
# move together is the weakness, not the procedure.
REQUIRED_TABLES=(users deploys deploy_sagas api_keys projects custom_domains admin_audit_log)

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
  if version=$(state_value current.env version 2>/dev/null) && [[ -n "$version" ]]; then
    printf '%s' "$version"
    return
  fi
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

snaphost_running() {
  local cid
  cid=$(compose ps -q snaphost 2>/dev/null) || return 1
  [[ -n "$cid" ]] && [[ $(docker inspect -f '{{.State.Running}}' "$cid") == true ]]
}

# A dump that restores to an empty schema is worse than no dump, because it
# looks like a backup. Check it before publishing.
#
# A SQLite dump is SQL text rather than an archive with a table of contents, so
# there is nothing to ask a tool about — the check reads the file. Two things
# are asserted: it ends with COMMIT;, which is the only evidence the read
# transaction finished rather than the dump being cut short, and every table
# that cannot be reconstructed is in it.
verify_archive() {
  local path=$1 table
  tail -n 1 "$path" | grep -qx 'COMMIT;' || die "backup is truncated, no terminating COMMIT: $path"
  grep -q '^BEGIN TRANSACTION;$' "$path" || die "backup is not a readable SQLite dump: $path"
  for table in "${REQUIRED_TABLES[@]}"; do
    # ${table} rather than $table: the regex that follows opens with a
    # bracket, and shellcheck reads `$table[` as a malformed array index
    # (SC1087) — an error, so it fails the CI lint gate.
    grep -Eq "^CREATE TABLE (IF NOT EXISTS )?[\"']?${table}[\"']?[ (]" "$path" \
      || die "backup is missing table $table: $path"
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
  # The patterns follow what each producer writes: scheduled-*.sql[.age] here,
  # database-*.sql from deploy.sh. Both were *.dump when the dumps were
  # PostgreSQL archives, and a pattern left behind would quietly stop pruning
  # its group rather than fail.
  prune_group 'scheduled-*.sql*' "$KEEP_DAYS" "$KEEP_MIN" scheduled
  prune_group 'database-*.sql' "$DEPLOY_KEEP_DAYS" "$DEPLOY_KEEP_MIN" pre-deploy
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
    log "DRY-RUN: take the lock, dump the database, verify it, encrypt, publish, upload"
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

  snaphost_running || die "the application is not running; no backup was taken"

  local timestamp tmp base final artifact bytes
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  tmp=$(mktemp "$BACKUP_DIR/.scheduled-${timestamp}.XXXXXX")
  TEMP_FILES+=("$tmp")
  base=${tmp##*/}
  base=${base#.}
  final="$BACKUP_DIR/$base.sql"

  umask 077
  # </dev/null: `compose exec -T` forwards our stdin to the container, which
  # would swallow the rest of this script when it is piped to a shell.
  compose exec -T snaphost sqlite3 "$DATABASE_PATH" .dump >"$tmp" </dev/null \
    || die "database dump failed"
  [[ -s "$tmp" ]] || die "database dump is empty"
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
  # No longer resolves the deployed version first. Checking a dump used to mean
  # running pg_restore inside the database container, so verifying a backup
  # needed a working deployment — exactly what an operator does not have when
  # they reach for one. It reads the file now.
  verify_archive "$path"
  log "Dump structure OK: all required tables present"
}

list_backups() {
  local file
  printf '%-46s %12s %s\n' NAME BYTES MODIFIED
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    printf '%-46s %12s %s\n' "$(basename "$file")" "$(stat -c '%s' "$file")" "$(date -u -r "$file" +%FT%TZ)"
  done < <(find "$BACKUP_DIR" -maxdepth 1 -type f \( -name 'scheduled-*.sql*' -o -name 'database-*.sql' -o -name 'tls-*.tar.gz.age' \) ! -name '*.sha256' -printf '%T@ %p\n' | sort -rn | awk '{print $2}')
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
