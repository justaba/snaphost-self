#!/usr/bin/env bash
set -Eeuo pipefail

# Exercises infra/backup.sh against fake docker/age/aws/curl commands. No real
# Docker daemon, database, credentials, or network access is used.

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SCRIPT="$ROOT/infra/backup.sh"
SHA=0123456789abcdef0123456789abcdef01234567
TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT
BIN="$TMP_ROOT/bin"
mkdir -p "$BIN"

cat >"$BIN/docker" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "docker $*" >>"${FAKE_LOG:?}"
if [[ ${1:-} == info ]]; then exit 0; fi
if [[ ${1:-} == inspect ]]; then
  if [[ ${APP_STOPPED:-0} == 1 ]]; then echo false; else echo true; fi
  exit 0
fi
[[ ${1:-} == compose ]] || exit 0
shift
op=
for arg in "$@"; do
  case "$arg" in ps|exec) op=$arg; break;; esac
done
case "$op" in
  ps)
    [[ ${APP_MISSING:-0} != 1 ]] || exit 1
    echo snaphost-container-id
    ;;
  exec)
    # `sqlite3 <db> .dump`. There is no separate table-of-contents command any
    # more: the dump is SQL text and verify_archive reads the same bytes that
    # get published, so this fake produces the artifact rather than a listing
    # of it. The old split is how a check for two tables deleted in item 3
    # stayed green — the fake listing was written from the same list.
    [[ ${FAIL_DUMP:-0} != 1 ]] || exit 1
    [[ ${EMPTY_DUMP:-0} != 1 ]] || exit 0
    printf 'PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n'
    for table in ${FAKE_TABLES:-}; do
      printf 'CREATE TABLE %s (id text primary key);\n' "$table"
      printf "INSERT INTO %s VALUES('row');\n" "$table"
    done
    [[ ${TRUNCATED_DUMP:-0} == 1 ]] || printf 'COMMIT;\n'
    ;;
esac
FAKE

cat >"$BIN/age" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "age $*" >>"${FAKE_LOG:?}"
[[ ${FAIL_ENCRYPT:-0} != 1 ]] || exit 1
out=
prev=
for arg in "$@"; do
  [[ "$prev" == -o ]] && out=$arg
  prev=$arg
done
# `age -r R -o OUT` reads stdin; `age -r R -o OUT IN` reads the file. Draining
# stdin in the first case is what the real binary does, and without it the
# `tar | age` pipeline gets EPIPE and fails under pipefail.
if [[ $# -lt 5 ]]; then
  cat >/dev/null 2>&1 || true
fi
printf 'age-encrypted-blob' >"${out:?}"
FAKE

cat >"$BIN/aws" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "aws $*" >>"${FAKE_LOG:?}"
if [[ "$*" == *head-object* ]]; then
  [[ ${FAIL_HEAD:-0} != 1 ]] || exit 1
  echo '{"ContentLength": 20}'
  exit 0
fi
[[ ${FAIL_UPLOAD:-0} != 1 ]] || exit 1
FAKE

cat >"$BIN/curl" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "curl $*" >>"${FAKE_LOG:?}"
[[ ${FAIL_HEARTBEAT:-0} != 1 ]] || exit 22
FAKE

chmod +x "$BIN/docker" "$BIN/age" "$BIN/aws" "$BIN/curl"

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf 'ok - %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'not ok - %s\n' "$1"; cat "$OUTPUT" 2>/dev/null || true; }

setup_case() {
  CASE_DIR=$(mktemp -d "$TMP_ROOT/case.XXXXXX")
  mkdir -p "$CASE_DIR/state" "$CASE_DIR/backups"
  ENV_FILE="$CASE_DIR/production.env"
  COMPOSE="$CASE_DIR/compose.yml"
  FAKE_LOG="$CASE_DIR/commands.log"
  : >"$FAKE_LOG"
  : >"$COMPOSE"
  cat >"$ENV_FILE" <<EOF
SNAPHOST_VERSION=$SHA
WEBHOOK_SECRET=internal-secret-value
EOF
  chmod 600 "$ENV_FILE"
  write_tables users deploys deploy_sagas api_keys projects custom_domains
  export PATH="$BIN:$PATH" FAKE_LOG FAKE_TABLES
  export SNAPHOST_COMPOSE_FILE="$COMPOSE" SNAPHOST_ENV_FILE="$ENV_FILE"
  export SNAPHOST_COMPOSE_PROJECT=snaphost-test
  export SNAPHOST_STATE_DIR="$CASE_DIR/state" SNAPHOST_BACKUP_DIR="$CASE_DIR/backups"
  export SNAPHOST_BACKUP_MIN_FREE_KB=0 SNAPHOST_BACKUP_LOCK_WAIT=1
  unset SNAPHOST_BACKUP_AGE_RECIPIENT SNAPHOST_BACKUP_REMOTE SNAPHOST_BACKUP_HEARTBEAT_URL
  unset SNAPHOST_BACKUP_KEEP_DAYS SNAPHOST_BACKUP_KEEP_MIN
  unset SNAPHOST_DEPLOY_BACKUP_KEEP_DAYS SNAPHOST_DEPLOY_BACKUP_KEEP_MIN
  unset SNAPHOST_TLS_STATE_DIR SNAPHOST_TLS_BACKUP_KEEP_DAYS SNAPHOST_TLS_BACKUP_KEEP_MIN
  unset FAIL_DUMP EMPTY_DUMP TRUNCATED_DUMP FAIL_ENCRYPT FAIL_UPLOAD FAIL_HEAD FAIL_HEARTBEAT APP_STOPPED APP_MISSING
}

# Which tables the fake dump contains. Read straight by the fake `docker`, so a
# test that removes one is removing it from the artifact rather than from a
# listing that describes the artifact.
write_tables() { FAKE_TABLES="$*"; export FAKE_TABLES; }

run_capture() {
  OUTPUT="$CASE_DIR/output"
  set +e
  "$SCRIPT" "$@" >"$OUTPUT" 2>&1
  RC=$?
  set -e
}

count_dumps() { find "$CASE_DIR/backups" -maxdepth 1 -type f -name "$1" ! -name '*.sha256' | wc -l | tr -d ' '; }

# Creates a dump-shaped file with a specific age in days.
seed_backup() {
  # Separate statements: a single `local` declares every name before it
  # assigns any, so `path=".../$name"` there would read the outer `name`.
  local name=$1 age_days=$2
  local path="$CASE_DIR/backups/$name"
  printf 'seeded' >"$path"
  printf '%s  %s\n' "$(sha256sum "$path" | awk '{print $1}')" "$name" >"$path.sha256"
  touch -d "$age_days days ago" "$path" "$path.sha256"
}

snapshot_case() {
  (cd "$CASE_DIR" && find . -type f ! -name output ! -name commands.log -print | sort | xargs sha256sum) | sha256sum
}

# --- happy path ---------------------------------------------------------

setup_case
run_capture run
if [[ $RC -eq 0 && $(count_dumps 'scheduled-*.sql') -eq 1 ]]; then
  pass 'scheduled backup produces one dump'
else fail 'scheduled backup produces one dump'; fi

setup_case
run_capture run
dump=$(find "$CASE_DIR/backups" -name 'scheduled-*.sql' | head -1)
if [[ -f "${dump}.sha256" ]] && (cd "$CASE_DIR/backups" && sha256sum --check --status "$(basename "$dump").sha256"); then
  pass 'checksum is written and matches'
else fail 'checksum is written and matches'; fi

setup_case
run_capture run
mode=$(stat -c '%a' "$(find "$CASE_DIR/backups" -name 'scheduled-*.sql' | head -1)")
if [[ "$mode" == 600 ]]; then pass 'dump is mode 0600'; else fail 'dump is mode 0600'; fi

setup_case
run_capture run
if [[ -f "$CASE_DIR/state/last-backup.env" ]] && grep -q '^completed_at=' "$CASE_DIR/state/last-backup.env"; then
  pass 'completion is recorded in state'
else fail 'completion is recorded in state'; fi

# --- failure paths ------------------------------------------------------

setup_case; export FAIL_DUMP=1; run_capture run
if [[ $RC -ne 0 && $(count_dumps 'scheduled-*.sql') -eq 0 ]]; then
  pass 'dump failure publishes nothing'
else fail 'dump failure publishes nothing'; fi

setup_case; export EMPTY_DUMP=1; run_capture run
if [[ $RC -ne 0 && $(count_dumps 'scheduled-*.sql') -eq 0 ]]; then
  pass 'empty dump is rejected'
else fail 'empty dump is rejected'; fi

# `users` is the one that carries the operator's password hash, so a dump
# without it restores into a platform nobody can log into.
setup_case; write_tables deploys deploy_sagas api_keys projects custom_domains; run_capture run
if [[ $RC -ne 0 && $(count_dumps 'scheduled-*.sql') -eq 0 ]]; then
  pass 'dump missing a required table is rejected'
else fail 'dump missing a required table is rejected'; fi

setup_case; write_tables; run_capture run
if [[ $RC -ne 0 ]]; then pass 'dump with no table data at all is rejected'; else fail 'dump with no table data at all is rejected'; fi

# A dump cut short is the case a checksum cannot catch: the file is intact, it
# is just not all of the database. The terminating COMMIT is the evidence.
setup_case; export TRUNCATED_DUMP=1; run_capture run
if [[ $RC -ne 0 && $(count_dumps 'scheduled-*.sql') -eq 0 ]] && grep -q 'truncated' "$OUTPUT"; then
  pass 'truncated dump is rejected'
else fail 'truncated dump is rejected'; fi

setup_case; export APP_STOPPED=1; run_capture run
if [[ $RC -ne 0 ]] && grep -q 'the application is not running' "$OUTPUT"; then
  pass 'a stopped application fails loudly'
else fail 'a stopped application fails loudly'; fi

setup_case
mkdir -p "$CASE_DIR/state"
: >"$CASE_DIR/state/deploy.lock"
flock -x "$CASE_DIR/state/deploy.lock" sleep 5 &
holder=$!
sleep 0.3
run_capture run
wait "$holder" 2>/dev/null || true
if [[ $RC -eq 0 && $(count_dumps 'scheduled-*.sql') -eq 0 ]] && grep -q 'holds the lock' "$OUTPUT"; then
  pass 'a running deployment defers the scheduled backup'
else fail 'a running deployment defers the scheduled backup'; fi

# --- encryption ---------------------------------------------------------

setup_case; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient; run_capture run
# The plaintext dump must not survive next to the encrypted one.
if [[ $RC -eq 0 && $(count_dumps 'scheduled-*.sql.age') -eq 1 && $(count_dumps 'scheduled-*.sql') -eq 0 ]]; then
  pass 'encryption publishes only the .age artifact'
else fail 'encryption publishes only the .age artifact'; fi

setup_case; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient; run_capture run
if ! grep -Fq 'CREATE TABLE users' "$(find "$CASE_DIR/backups" -name '*.age' | head -1)"; then
  pass 'the published artifact is the encrypted one'
else fail 'the published artifact is the encrypted one'; fi

setup_case; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient FAIL_ENCRYPT=1; run_capture run
if [[ $RC -ne 0 && $(count_dumps 'scheduled-*') -eq 0 ]]; then
  pass 'encryption failure publishes nothing'
else fail 'encryption failure publishes nothing'; fi

setup_case; export SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod; run_capture run
if [[ $RC -ne 0 ]] && grep -q 'refusing to upload an unencrypted backup' "$OUTPUT"; then
  pass 'off-host upload without encryption is refused'
else fail 'off-host upload without encryption is refused'; fi

# --- off-host copy ------------------------------------------------------

setup_case
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod
run_capture run
if [[ $RC -eq 0 ]] && grep -q 's3 cp .* s3://snaphost-backups/prod/scheduled-' "$FAKE_LOG" \
  && grep -q 's3 cp .*\.sha256 ' "$FAKE_LOG"; then
  pass 'dump and checksum are both uploaded'
else fail 'dump and checksum are both uploaded'; fi

setup_case
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod FAIL_UPLOAD=1
run_capture run
if [[ $RC -ne 0 ]]; then pass 'upload failure fails the run'; else fail 'upload failure fails the run'; fi

setup_case
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod FAIL_HEAD=1
run_capture run
if [[ $RC -ne 0 ]] && grep -q 'could not be confirmed' "$OUTPUT"; then
  pass 'an unconfirmed upload fails the run'
else fail 'an unconfirmed upload fails the run'; fi

setup_case
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://bucket-only
run_capture run
if [[ $RC -eq 0 ]] && grep -q 'head-object --bucket bucket-only --key scheduled-' "$FAKE_LOG"; then
  pass 'a bucket-root remote produces no empty key prefix'
else fail 'a bucket-root remote produces no empty key prefix'; fi

# --- retention ----------------------------------------------------------

setup_case
export SNAPHOST_BACKUP_KEEP_DAYS=14 SNAPHOST_BACKUP_KEEP_MIN=2
seed_backup scheduled-20260101T000000Z.sql 40
seed_backup scheduled-20260102T000000Z.sql 39
seed_backup scheduled-20260103T000000Z.sql 38
seed_backup scheduled-20260720T000000Z.sql 1
run_capture prune
if [[ $RC -eq 0 && $(count_dumps 'scheduled-*.sql') -eq 2 ]]; then
  pass 'retention removes dumps past the age limit'
else fail 'retention removes dumps past the age limit'; fi

setup_case
export SNAPHOST_BACKUP_KEEP_DAYS=14 SNAPHOST_BACKUP_KEEP_MIN=3
seed_backup scheduled-20260101T000000Z.sql 40
seed_backup scheduled-20260102T000000Z.sql 39
seed_backup scheduled-20260103T000000Z.sql 38
run_capture prune
if [[ $(count_dumps 'scheduled-*.sql') -eq 3 ]]; then
  pass 'the retention minimum outranks the age limit'
else fail 'the retention minimum outranks the age limit'; fi

setup_case
export SNAPHOST_BACKUP_KEEP_DAYS=1 SNAPHOST_BACKUP_KEEP_MIN=0
seed_backup scheduled-20260101T000000Z.sql 40
run_capture prune
if [[ ! -f "$CASE_DIR/backups/scheduled-20260101T000000Z.sql.sha256" ]]; then
  pass 'a pruned dump takes its checksum with it'
else fail 'a pruned dump takes its checksum with it'; fi

setup_case
export SNAPHOST_BACKUP_KEEP_DAYS=1 SNAPHOST_BACKUP_KEEP_MIN=0
seed_backup scheduled-20260101T000000Z.sql 40
printf 'sha=%s\nbackup_path=%s\n' "$SHA" "$CASE_DIR/backups/scheduled-20260101T000000Z.sql" >"$CASE_DIR/state/current.env"
run_capture prune
if [[ -f "$CASE_DIR/backups/scheduled-20260101T000000Z.sql" ]]; then
  pass 'a backup referenced by deployment state is never pruned'
else fail 'a backup referenced by deployment state is never pruned'; fi

setup_case
export SNAPHOST_DEPLOY_BACKUP_KEEP_DAYS=90 SNAPHOST_DEPLOY_BACKUP_KEEP_MIN=1 SNAPHOST_BACKUP_KEEP_DAYS=1 SNAPHOST_BACKUP_KEEP_MIN=0
seed_backup "database-20260101T000000Z-$SHA-abc.sql" 40
seed_backup scheduled-20260101T000000Z.sql 40
run_capture prune
if [[ -f "$CASE_DIR/backups/database-20260101T000000Z-$SHA-abc.sql" && ! -f "$CASE_DIR/backups/scheduled-20260101T000000Z.sql" ]]; then
  pass 'pre-deploy dumps keep their own longer retention'
else fail 'pre-deploy dumps keep their own longer retention'; fi

setup_case
export SNAPHOST_BACKUP_KEEP_DAYS=1 SNAPHOST_BACKUP_KEEP_MIN=2
seed_backup scheduled-20260101T000000Z.sql 40
seed_backup scheduled-20260102T000000Z.sql 39
seed_backup scheduled-20260103T000000Z.sql 38
run_capture prune
# If checksums counted as backups, the two newest "files" would be checksums
# and every real dump would be eligible for deletion.
if [[ $(count_dumps 'scheduled-*.sql') -eq 2 ]]; then
  pass 'checksums are not counted against the retention minimum'
else fail 'checksums are not counted against the retention minimum'; fi

# --- heartbeat ----------------------------------------------------------

setup_case; export SNAPHOST_BACKUP_HEARTBEAT_URL=https://hc.invalid/ping; run_capture run
if [[ $RC -eq 0 ]] && grep -q 'hc.invalid/ping' "$FAKE_LOG"; then
  pass 'a successful backup pings the heartbeat'
else fail 'a successful backup pings the heartbeat'; fi

setup_case; export SNAPHOST_BACKUP_HEARTBEAT_URL=https://hc.invalid/ping FAIL_DUMP=1; run_capture run
if [[ $RC -ne 0 ]] && ! grep -q 'hc.invalid/ping' "$FAKE_LOG"; then
  pass 'a failed backup does not ping the heartbeat'
else fail 'a failed backup does not ping the heartbeat'; fi

setup_case; export SNAPHOST_BACKUP_HEARTBEAT_URL=https://hc.invalid/ping FAIL_HEARTBEAT=1; run_capture run
if [[ $RC -eq 0 && $(count_dumps 'scheduled-*.sql') -eq 1 ]]; then
  pass 'a failed heartbeat does not fail a good backup'
else fail 'a failed heartbeat does not fail a good backup'; fi

# --- TLS certificate store (ADR 0007) -----------------------------------
# Losing this store means every customer domain re-issues at once against
# Let's Encrypt limits, and the visible symptom is a browser security warning
# on someone else's site. It is also pure key material, so unlike the database
# dump there is no version of it that may be written unencrypted.

seed_tls_state() {
  TLS_DIR="$CASE_DIR/caddy-state"
  mkdir -p "$TLS_DIR/certificates/le/app.example.com" "$TLS_DIR/acme/le/users/default"
  printf 'PRIVATE-KEY-MATERIAL\n' >"$TLS_DIR/certificates/le/app.example.com/app.example.com.key"
  printf 'CERT\n' >"$TLS_DIR/certificates/le/app.example.com/app.example.com.crt"
  printf 'ACME-ACCOUNT-KEY\n' >"$TLS_DIR/acme/le/users/default/default.key"
  export SNAPHOST_TLS_STATE_DIR="$TLS_DIR"
}

setup_case; seed_tls_state; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient
run_capture tls
if [[ $RC -eq 0 && $(count_dumps 'tls-*.tar.gz.age') -eq 1 ]]; then
  pass 'TLS store is archived and encrypted'
else fail 'TLS store is archived and encrypted'; fi

setup_case; seed_tls_state
run_capture tls
if [[ $RC -ne 0 ]] && grep -q 'never written unencrypted' "$OUTPUT" && [[ $(count_dumps 'tls-*') -eq 0 ]]; then
  pass 'the TLS store is refused without an encryption recipient'
else fail 'the TLS store is refused without an encryption recipient'; fi

# The plaintext archive must never exist as a file, not even briefly.
setup_case; seed_tls_state; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient
run_capture tls
leaked=0
for f in "$CASE_DIR"/backups/*; do
  grep -Fq 'PRIVATE-KEY-MATERIAL' "$f" 2>/dev/null && leaked=1
  grep -Fq 'ACME-ACCOUNT-KEY' "$f" 2>/dev/null && leaked=1
done
if [[ $leaked -eq 0 ]]; then pass 'no key material is readable in the published artifact'; else fail 'no key material is readable in the published artifact'; fi

setup_case; seed_tls_state
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod
run_capture tls
if [[ $RC -eq 0 ]] && grep -q 's3 cp .* s3://snaphost-backups/prod/tls-' "$FAKE_LOG" \
  && grep -q 's3 cp .*tls-.*\.sha256 ' "$FAKE_LOG"; then
  pass 'TLS archive and checksum are both uploaded off-host'
else fail 'TLS archive and checksum are both uploaded off-host'; fi

setup_case; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_TLS_STATE_DIR="$CASE_DIR/absent"
run_capture tls
if [[ $RC -ne 0 ]] && grep -q 'TLS state directory does not exist' "$OUTPUT"; then
  pass 'a missing TLS store fails loudly rather than archiving nothing'
else fail 'a missing TLS store fails loudly rather than archiving nothing'; fi

setup_case; seed_tls_state; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient
export SNAPHOST_TLS_BACKUP_KEEP_DAYS=1 SNAPHOST_TLS_BACKUP_KEEP_MIN=0
seed_backup tls-20260101T000000Z.tar.gz.age 40
seed_backup scheduled-20260101T000000Z.sql 40
run_capture tls
if [[ ! -f "$CASE_DIR/backups/tls-20260101T000000Z.tar.gz.age" && -f "$CASE_DIR/backups/scheduled-20260101T000000Z.sql" ]]; then
  pass 'TLS retention prunes its own group and leaves database dumps alone'
else fail 'TLS retention prunes its own group and leaves database dumps alone'; fi

setup_case; seed_tls_state; export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient
before=$(snapshot_case)
run_capture --dry-run tls
after=$(snapshot_case)
if [[ $RC -eq 0 && "$before" == "$after" ]]; then
  pass 'dry-run tls is filesystem read-only'
else fail 'dry-run tls is filesystem read-only'; fi

# --- dry run, verify, secrets ------------------------------------------

setup_case
seed_backup scheduled-20260101T000000Z.sql 40
export SNAPHOST_BACKUP_KEEP_DAYS=1 SNAPHOST_BACKUP_KEEP_MIN=0
before=$(snapshot_case)
run_capture --dry-run run
after=$(snapshot_case)
if [[ $RC -eq 0 && "$before" == "$after" ]]; then
  pass 'dry-run run is filesystem read-only'
else fail 'dry-run run is filesystem read-only'; fi

setup_case
run_capture run
dump=$(find "$CASE_DIR/backups" -name 'scheduled-*.sql' | head -1)
run_capture verify "$dump"
if [[ $RC -eq 0 ]] && grep -q 'Checksum OK' "$OUTPUT"; then
  pass 'verify accepts an intact backup'
else fail 'verify accepts an intact backup'; fi

setup_case
run_capture run
dump=$(find "$CASE_DIR/backups" -name 'scheduled-*.sql' | head -1)
printf 'corrupted' >>"$dump"
run_capture verify "$dump"
if [[ $RC -ne 0 ]]; then pass 'verify rejects a corrupted backup'; else fail 'verify rejects a corrupted backup'; fi

setup_case
export SNAPHOST_BACKUP_AGE_RECIPIENT=age1testrecipient SNAPHOST_BACKUP_REMOTE=s3://snaphost-backups/prod SNAPHOST_BACKUP_HEARTBEAT_URL=https://hc.invalid/ping
run_capture run
leaked=0
for secret in production-password internal-secret-value; do
  if grep -R -Fq "$secret" "$CASE_DIR/state" "$OUTPUT" "$FAKE_LOG"; then leaked=1; fi
done
if [[ $leaked -eq 0 ]]; then pass 'no secret reaches output, state, or a command line'; else fail 'no secret reaches output, state, or a command line'; fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[[ $FAIL -eq 0 ]]
