#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SOURCE="$ROOT/infra/snaphostctl"
VERSION=v1.2.3
CURRENT_VERSION=v1.2.2
OLDER_VERSION=v1.2.1
CURRENT_COMMIT=2222222222222222222222222222222222222222
TARGET_COMMIT=3333333333333333333333333333333333333333
OLDER_COMMIT=1111111111111111111111111111111111111111
NEWEST_COMMIT=4444444444444444444444444444444444444444

TMP_ROOT=$(mktemp -d)
trap 'rm -rf "$TMP_ROOT"' EXIT
BIN="$TMP_ROOT/bin"
mkdir -p "$BIN"

cat >"$BIN/git" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "git $*" >>"${FAKE_LOG:?}"
if [[ ${1:-} == -C ]]; then shift 2; fi
op=${1:-}; shift || true
tag_commit() {
  case "$1" in
    refs/tags/v1.2.1* ) printf '%s\n' "${OLDER_COMMIT:?}" ;;
    refs/tags/v1.2.2* ) printf '%s\n' "${CURRENT_COMMIT:?}" ;;
    refs/tags/v1.2.3* ) printf '%s\n' "${TARGET_COMMIT:?}" ;;
    refs/tags/v2.0.0* ) printf '%s\n' "${NEWEST_COMMIT:?}" ;;
    * ) return 1 ;;
  esac
}
case "$op" in
  rev-parse)
    case "$*" in
      --is-inside-work-tree) echo true ;;
      --show-toplevel) echo "${FAKE_CHECKOUT:?}" ;;
      HEAD) cat "${FAKE_GIT_HEAD:?}" ;;
      --verify*) tag_commit "${!#}" ;;
      *) exit 1 ;;
    esac
    ;;
  status)
    [[ ${DIRTY_CHECKOUT:-0} == 1 ]] && echo ' M infra/docker-compose.prod.yml'
    exit 0
    ;;
  fetch)
    [[ ${FAIL_FETCH:-0} != 1 ]] || exit 1
    ;;
  for-each-ref)
    printf '%s\n' v2.0.0 v1.2.3 v1.2.2 v1.2.1 v01.0.0
    ;;
  merge-base)
    [[ ${TAG_OFF_MAIN:-0} != 1 ]] || exit 1
    ;;
  checkout)
    [[ ${FAIL_CHECKOUT:-0} != 1 ]] || exit 1
    commit=${!#}
    printf '%s\n' "$commit" >"${FAKE_GIT_HEAD:?}"
    ;;
  *) exit 1 ;;
esac
FAKE

cat >"$BIN/docker" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "docker $*" >>"${FAKE_LOG:?}"
if [[ ${1:-} == info ]]; then exit 0; fi
if [[ ${1:-} == compose && ${2:-} == version ]]; then
  echo 'Docker Compose version v5.4.0'
  exit 0
fi
if [[ ${1:-} == compose && "$*" == *' logs '* ]]; then
  printf '%s\n' 'snaphost-1 | {"msg":"operator account created","password":"first-login-password"}'
  exit 0
fi
exit 1
FAKE

cat >"$BIN/apparmor_parser" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "apparmor_parser $*" >>"${FAKE_LOG:?}"
count=0
[[ ! -f ${APPARMOR_COUNT_FILE:?} ]] || count=$(<"$APPARMOR_COUNT_FILE")
count=$((count + 1))
printf '%s\n' "$count" >"$APPARMOR_COUNT_FILE"
if [[ ${FAIL_APPARMOR_FIRST:-0} == 1 && $count -eq 1 ]]; then exit 1; fi
exit 0
FAKE

cat >"$BIN/systemctl" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "systemctl $*" >>"${FAKE_LOG:?}"
[[ ${FAIL_SYSTEMD:-0} != 1 ]]
FAKE

cat >"$BIN/nproc" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_NPROC:-2}"
FAKE

cat >"$BIN/ss" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "ss $*" >>"${FAKE_LOG:?}"
[[ ${PORTS_BUSY:-0} != 1 ]] || printf '%s\n' 'LISTEN 0 4096 0.0.0.0:443 0.0.0.0:*'
FAKE

chmod +x "$BIN/git" "$BIN/docker" "$BIN/apparmor_parser" "$BIN/systemctl" "$BIN/nproc" "$BIN/ss"

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf 'ok - %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'not ok - %s\n' "$1"; cat "${OUTPUT:-/dev/null}" 2>/dev/null || true; }

setup_case() {
  CASE_DIR=$(mktemp -d "$TMP_ROOT/case.XXXXXX")
  CHECKOUT="$CASE_DIR/checkout"
  mkdir -p "$CHECKOUT/infra/apparmor" "$CHECKOUT/infra/systemd" "$CASE_DIR/etc/apparmor.d" "$CASE_DIR/etc/systemd"
  cp "$SOURCE" "$CHECKOUT/infra/snaphostctl"
  cp "$ROOT/infra/.env.production.example" "$CHECKOUT/infra/.env.production.example"
  cp "$ROOT/infra/Caddyfile.production.example" "$CHECKOUT/infra/Caddyfile.production.example"
  cp "$ROOT/infra/apparmor/snaphost-buildkit-rootless" "$CHECKOUT/infra/apparmor/snaphost-buildkit-rootless"
  cp "$ROOT/infra/systemd/snaphost-backup.service" "$CHECKOUT/infra/systemd/snaphost-backup.service"
  cp "$ROOT/infra/systemd/snaphost-backup.timer" "$CHECKOUT/infra/systemd/snaphost-backup.timer"
  cp "$ROOT/infra/systemd/snaphost-tls-backup.service" "$CHECKOUT/infra/systemd/snaphost-tls-backup.service"
  cp "$ROOT/infra/systemd/snaphost-tls-backup.timer" "$CHECKOUT/infra/systemd/snaphost-tls-backup.timer"
  : >"$CHECKOUT/infra/docker-compose.prod.yml"
  chmod +x "$CHECKOUT/infra/snaphostctl"

  cat >"$CHECKOUT/infra/deploy.sh" <<'FAKE'
#!/usr/bin/env bash
set -u
echo "deploy $*" >>"${FAKE_LOG:?}"
[[ ${FAIL_DEPLOY:-0} != 1 ]] || exit 42
dry_run=false
if [[ ${1:-} == --dry-run ]]; then dry_run=true; shift; fi
if [[ ${1:-} == rollback && "$dry_run" == false && ${FAIL_ROLLBACK_ACTUAL:-0} == 1 ]]; then exit 42; fi
case "${1:-}" in
  deploy)
    [[ $# -eq 2 ]] || exit 2
    version=$2
    sed -i "s/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=$version/" "${SNAPHOST_ENV_FILE:?}"
    mkdir -p "${SNAPHOST_STATE_DIR:?}"
    printf 'version=%s\nmigration_status=applied\nstatus=success\n' "$version" >"$SNAPHOST_STATE_DIR/current.env"
    ;;
  rollback)
    [[ $# -eq 1 ]] || exit 2
    if [[ "$dry_run" == true ]]; then exit 0; fi
    current=$(awk -F= '$1=="version" {print $2; exit}' "$SNAPHOST_STATE_DIR/current.env")
    previous=$(awk -F= '$1=="version" {print $2; exit}' "$SNAPHOST_STATE_DIR/previous.env")
    sed -i "s/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=$previous/" "${SNAPHOST_ENV_FILE:?}"
    printf 'version=%s\nmigration_status=applied\nstatus=rollback-success\n' "$previous" >"$SNAPHOST_STATE_DIR/current.env"
    printf 'version=%s\nstatus=replaced\n' "$current" >"$SNAPHOST_STATE_DIR/previous.env"
    ;;
  *) exit 2 ;;
esac
FAKE
  chmod +x "$CHECKOUT/infra/deploy.sh"

  KEY_FILE="$CASE_DIR/openrouter.key"
  printf '%s\n' 'sk-or-v1-test-key' >"$KEY_FILE"
  chmod 600 "$KEY_FILE"
  FAKE_LOG="$CASE_DIR/commands.log"
  FAKE_GIT_HEAD="$CASE_DIR/git-head"
  APPARMOR_COUNT_FILE="$CASE_DIR/apparmor-count"
  printf '%s\n' "$TARGET_COMMIT" >"$FAKE_GIT_HEAD"
  : >"$FAKE_LOG"

  export PATH="$BIN:/usr/bin:/bin"
  export FAKE_LOG FAKE_GIT_HEAD APPARMOR_COUNT_FILE
  export FAKE_CHECKOUT="$CHECKOUT" CURRENT_COMMIT TARGET_COMMIT OLDER_COMMIT NEWEST_COMMIT
  export SNAPHOST_INSTALL_ROOT="$CHECKOUT"
  export SNAPHOST_ENV_FILE="$CHECKOUT/env/production.env"
  export SNAPHOST_STATE_DIR="$CHECKOUT/state"
  export SNAPHOST_BACKUP_DIR="$CHECKOUT/backups"
  export SNAPHOST_COMPOSE_FILE="$CHECKOUT/infra/docker-compose.prod.yml"
  export SNAPHOST_APPARMOR_DIR="$CASE_DIR/etc/apparmor.d"
  export SNAPHOST_SYSTEMD_DIR="$CASE_DIR/etc/systemd"
  export SNAPHOST_CLI_TARGET="$CASE_DIR/usr/local/sbin/snaphostctl"
  export SNAPHOST_TEST_EUID=0 SNAPHOST_TEST_DOCKER_SOCKET_GID=998
  export SNAPHOST_INSTALL_CONTROL_DOMAIN=panel.control.test
  export SNAPHOST_INSTALL_ACME_EMAIL=acme@control.test
  unset SNAPHOST_INSTALL_OPENROUTER_KEY_FILE SNAPHOST_INSTALL_LLM_ENABLED
  export SNAPHOST_INSTALL_OPERATOR_EMAIL=operator@example.test
  export SNAPHOST_INSTALL_PUBLIC_URL=https://panel.control.test
  export FAKE_NPROC=2
  unset SNAPHOST_INSTALL_ALLOW_HTTP DIRTY_CHECKOUT FAIL_FETCH TAG_OFF_MAIN FAIL_CHECKOUT FAIL_DEPLOY FAIL_ROLLBACK_ACTUAL FAIL_APPARMOR_FIRST FAIL_SYSTEMD PORTS_BUSY
  CTL="$CHECKOUT/infra/snaphostctl"
}

seed_installed() {
  mkdir -p "$CHECKOUT/env" "$CHECKOUT/state" "$CHECKOUT/backups" "$(dirname "$SNAPHOST_CLI_TARGET")"
  cp "$SOURCE" "$SNAPHOST_CLI_TARGET"
  chmod +x "$SNAPHOST_CLI_TARGET"
  CTL=$SNAPHOST_CLI_TARGET
  cat >"$SNAPHOST_ENV_FILE" <<EOF
SNAPHOST_VERSION=$CURRENT_VERSION
OPENROUTER_API_KEY=sk-or-v1-test-key
SNAPHOST_CONTROL_DOMAIN=panel.control.test
SNAPHOST_ACME_EMAIL=acme@control.test
SNAPHOST_CADDY_STATE_DIR=$CHECKOUT/state/caddy
RESERVED_DOMAINS=panel.control.test
DOMAIN_CNAME_TARGET=panel.control.test
DOCKER_SOCKET_GID=998
EOF
  chmod 600 "$SNAPHOST_ENV_FILE"
  cat >"$SNAPHOST_STATE_DIR/current.env" <<EOF
version=$CURRENT_VERSION
migration_status=applied
status=success
EOF
  printf '%s\n' "$CURRENT_COMMIT" >"$FAKE_GIT_HEAD"
}

seed_previous() {
  cat >"$SNAPHOST_STATE_DIR/previous.env" <<EOF
version=$OLDER_VERSION
status=replaced
EOF
}

run_capture() {
  OUTPUT="$CASE_DIR/output"
  set +e
  "$CTL" "$@" >"$OUTPUT" 2>&1
  RC=$?
  set -e
}

setup_case
export SNAPHOST_TEST_EUID=1000
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'must run as root' "$OUTPUT"; then pass 'install requires root'; else fail 'install requires root'; fi

setup_case
export PORTS_BUSY=1
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'must be free for Compose Caddy' "$OUTPUT" \
  && [[ ! -e "$CHECKOUT/env" ]]; then
  pass 'install refuses occupied public Caddy ports before writing host state'
else fail 'install refuses occupied public Caddy ports before writing host state'; fi

setup_case
run_capture install v1.2
if [[ $RC -ne 0 ]] && ! grep -q '^docker ' "$FAKE_LOG"; then pass 'install rejects a malformed version before host changes'; else fail 'install rejects a malformed version before host changes'; fi

setup_case
export DIRTY_CHECKOUT=1
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'local changes' "$OUTPUT" && [[ ! -e "$CHECKOUT/env" ]]; then pass 'install refuses a dirty checkout'; else fail 'install refuses a dirty checkout'; fi

setup_case
printf '%s\n' "$CURRENT_COMMIT" >"$FAKE_GIT_HEAD"
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'does not match installed release' "$OUTPUT"; then pass 'install requires checkout at requested release tag'; else fail 'install requires checkout at requested release tag'; fi

setup_case
chmod 644 "$KEY_FILE"
export SNAPHOST_INSTALL_LLM_ENABLED=true SNAPHOST_INSTALL_OPENROUTER_KEY_FILE="$KEY_FILE"
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'permissions must be 0600 or 0400' "$OUTPUT"; then pass 'install refuses a readable API-key file'; else fail 'install refuses a readable API-key file'; fi

setup_case
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] && grep -qx 'LLM_ENABLED=false' "$SNAPHOST_ENV_FILE" \
  && ! grep -q '^OPENROUTER_API_KEY=' "$SNAPHOST_ENV_FILE"; then
  pass 'install succeeds without a key or AI prompt and persists disabled AI'
else fail 'install succeeds without a key or AI prompt and persists disabled AI'; fi

setup_case
export SNAPHOST_INSTALL_LLM_ENABLED=true SNAPHOST_INSTALL_OPENROUTER_KEY_FILE="$KEY_FILE"
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] && grep -qx 'LLM_ENABLED=true' "$SNAPHOST_ENV_FILE" \
  && grep -qx 'OPENROUTER_API_KEY=sk-or-v1-test-key' "$SNAPHOST_ENV_FILE" \
  && ! grep -q 'sk-or-v1-test-key' "$OUTPUT" "$FAKE_LOG"; then
  pass 'explicit AI install saves a protected key without logging it'
else fail 'explicit AI install saves a protected key without logging it'; fi

setup_case
export SNAPHOST_INSTALL_LLM_ENABLED=true
run_capture install "$VERSION" </dev/null
if [[ $RC -ne 0 ]] && grep -q 'KEY_FILE is required when' "$OUTPUT" \
  && ! grep -q '^deploy ' "$FAKE_LOG"; then
  pass 'AI opt-in without a key fails before deployment'
else fail 'AI opt-in without a key fails before deployment'; fi

setup_case
export SNAPHOST_INSTALL_OPENROUTER_KEY_FILE="$KEY_FILE"
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'set SNAPHOST_INSTALL_LLM_ENABLED=true' "$OUTPUT"; then
  pass 'an API-key file alone does not silently enable AI'
else fail 'an API-key file alone does not silently enable AI'; fi

setup_case
export SNAPHOST_INSTALL_LLM_ENABLED=typo
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'must be true or false' "$OUTPUT"; then
  pass 'install rejects an invalid AI switch'
else fail 'install rejects an invalid AI switch'; fi

setup_case
unset SNAPHOST_INSTALL_ACME_EMAIL
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_ACME_EMAIL"{print $2}' "$SNAPHOST_ENV_FILE") == operator@example.test ]]; then
  pass 'install defaults ACME contact to the configured operator email'
else fail 'install defaults ACME contact to the configured operator email'; fi

setup_case
export SNAPHOST_INSTALL_ACME_CA=https://acme-staging-v02.api.letsencrypt.org/directory
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_ACME_CA"{print $2}' "$SNAPHOST_ENV_FILE") == "$SNAPHOST_INSTALL_ACME_CA" ]]; then
  pass 'install persists the explicit staging ACME directory'
else fail 'install persists the explicit staging ACME directory'; fi
unset SNAPHOST_INSTALL_ACME_CA

setup_case
mkdir -p "$CHECKOUT/env"
cp "$ROOT/infra/.env.production.example" "$SNAPHOST_ENV_FILE"
sed -i \
  -e "s/^SNAPHOST_VERSION=.*/SNAPHOST_VERSION=$VERSION/" \
  -e 's/^SNAPHOST_CONTROL_DOMAIN=.*/SNAPHOST_CONTROL_DOMAIN=panel.control.test/' \
  -e 's/^SNAPHOST_ACME_EMAIL=.*/SNAPHOST_ACME_EMAIL=acme@control.test/' \
  "$SNAPHOST_ENV_FILE"
printf '%s\n' 'BUILDKIT_CPU_LIMIT=4.0' >>"$SNAPHOST_ENV_FILE"
chmod 600 "$SNAPHOST_ENV_FILE"
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'exceeds the host capacity of 2 CPUs' "$OUTPUT" \
  && ! grep -q '^deploy ' "$FAKE_LOG"; then
  pass 'install refuses a BuildKit CPU limit larger than the host'
else fail 'install refuses a BuildKit CPU limit larger than the host'; fi

setup_case
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$SNAPHOST_ENV_FILE") == "$VERSION" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_CONTROL_DOMAIN"{print $2}' "$SNAPHOST_ENV_FILE") == panel.control.test ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_ACME_EMAIL"{print $2}' "$SNAPHOST_ENV_FILE") == acme@control.test ]] \
  && [[ $(awk -F= '$1=="DOMAIN_CNAME_TARGET"{print $2}' "$SNAPHOST_ENV_FILE") == panel.control.test ]] \
  && [[ $(awk -F= '$1=="DOCKER_SOCKET_GID"{print $2}' "$SNAPHOST_ENV_FILE") == 998 ]] \
  && [[ $(awk -F= '$1=="BUILDKIT_CPU_LIMIT"{print $2}' "$SNAPHOST_ENV_FILE") == 2.0 ]] \
  && [[ $(stat -c '%a' "$SNAPHOST_ENV_FILE") == 600 ]] \
  && [[ $(stat -c '%a' "$CHECKOUT/state") == 700 ]] \
  && [[ $(stat -c '%a' "$CHECKOUT/state/caddy/data") == 700 ]] \
  && [[ $(stat -c '%a' "$CHECKOUT/backups") == 700 ]] \
  && [[ -x "$SNAPHOST_CLI_TARGET" ]] \
  && grep -q 'first-login-password' "$OUTPUT" \
  && grep -q "WorkingDirectory=$CHECKOUT" "$CASE_DIR/etc/systemd/snaphost-backup.service" \
  && grep -q "SNAPHOST_TLS_STATE_DIR=$CHECKOUT/state/caddy/data" "$CASE_DIR/etc/systemd/snaphost-tls-backup.service" \
  && grep -q 'systemctl enable --now snaphost-backup.timer' "$FAKE_LOG" \
  && ! grep -q 'systemctl enable --now snaphost-tls-backup.timer' "$FAKE_LOG"; then
  pass 'install creates protected env, deploys, installs host files and prints first login'
else fail 'install creates protected env, deploys, installs host files and prints first login'; fi

setup_case
export SNAPHOST_INSTALL_PUBLIC_URL=http://192.0.2.10:8080
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'must use HTTPS' "$OUTPUT"; then pass 'install refuses implicit HTTP'; else fail 'install refuses implicit HTTP'; fi

setup_case
export SNAPHOST_INSTALL_PUBLIC_URL=http://192.0.2.10:8080 SNAPHOST_INSTALL_ALLOW_HTTP=true
run_capture install "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="SESSION_COOKIE_SECURE"{print $2}' "$SNAPHOST_ENV_FILE") == false ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_ALLOW_HTTP_SMOKE"{print $2}' "$SNAPHOST_ENV_FILE") == true ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_BIND_ADDRESS"{print $2}' "$SNAPHOST_ENV_FILE") == 0.0.0.0 ]]; then
  pass 'explicit HTTP install makes cookie and smoke behavior consistent'
else fail 'explicit HTTP install makes cookie and smoke behavior consistent'; fi

setup_case
export FAIL_DEPLOY=1
run_capture install "$VERSION"
first_rc=$RC
unset FAIL_DEPLOY
run_capture install "$VERSION"
if [[ $first_rc -ne 0 && $RC -eq 0 ]] && grep -q 'Resuming incomplete installation' "$OUTPUT"; then pass 'install resumes after a failed first deploy'; else fail 'install resumes after a failed first deploy'; fi

setup_case
seed_installed
run_capture install "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'already installed' "$OUTPUT"; then pass 'install refuses to overwrite an installation'; else fail 'install refuses to overwrite an installation'; fi

setup_case
seed_installed
run_capture upgrade "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(<"$FAKE_GIT_HEAD") == "$TARGET_COMMIT" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$SNAPHOST_ENV_FILE") == "$VERSION" ]] \
  && grep -q "deploy deploy $VERSION" "$FAKE_LOG" \
  && grep -q 'Upgrade completed: v1.2.2 -> v1.2.3' "$OUTPUT"; then
  pass 'upgrade fetches, checks out and deploys an explicit release'
else fail 'upgrade fetches, checks out and deploys an explicit release'; fi

setup_case
seed_installed
sed -i '/^SNAPHOST_CONTROL_DOMAIN=/d; /^SNAPHOST_ACME_EMAIL=/d; /^SNAPHOST_CADDY_STATE_DIR=/d; /^RESERVED_DOMAINS=/d; /^DOMAIN_CNAME_TARGET=/d' "$SNAPHOST_ENV_FILE"
run_capture upgrade "$VERSION"
if [[ $RC -eq 0 ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_CONTROL_DOMAIN"{print $2}' "$SNAPHOST_ENV_FILE") == panel.control.test ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_ACME_EMAIL"{print $2}' "$SNAPHOST_ENV_FILE") == acme@control.test ]] \
  && [[ -d "$CHECKOUT/state/caddy/data" ]]; then
  pass 'first edge-aware upgrade migrates the protected Caddy configuration'
else fail 'first edge-aware upgrade migrates the protected Caddy configuration'; fi

setup_case
seed_installed
run_capture upgrade
if [[ $RC -eq 0 && $(<"$FAKE_GIT_HEAD") == "$NEWEST_COMMIT" ]] \
  && grep -q 'Newest published version: v2.0.0' "$OUTPUT"; then pass 'upgrade without argument pins newest SemVer'; else fail 'upgrade without argument pins newest SemVer'; fi

setup_case
seed_installed
export DIRTY_CHECKOUT=1
run_capture upgrade "$VERSION"
if [[ $RC -ne 0 ]] && ! grep -q 'git fetch' "$FAKE_LOG" && ! grep -q '^deploy ' "$FAKE_LOG"; then pass 'upgrade refuses a dirty checkout before fetch'; else fail 'upgrade refuses a dirty checkout before fetch'; fi

setup_case
seed_installed
run_capture upgrade "$OLDER_VERSION"
if [[ $RC -ne 0 ]] && grep -q 'cannot move backward' "$OUTPUT" && ! grep -q '^deploy ' "$FAKE_LOG"; then pass 'upgrade refuses a downgrade'; else fail 'upgrade refuses a downgrade'; fi

setup_case
seed_installed
export TAG_OFF_MAIN=1
run_capture upgrade "$VERSION"
if [[ $RC -ne 0 ]] && grep -q 'not reachable' "$OUTPUT" && ! grep -q '^deploy ' "$FAKE_LOG"; then pass 'upgrade refuses a tag outside remote main'; else fail 'upgrade refuses a tag outside remote main'; fi

setup_case
seed_installed
export FAIL_DEPLOY=1
run_capture upgrade "$VERSION"
if [[ $RC -eq 42 && $(<"$FAKE_GIT_HEAD") == "$CURRENT_COMMIT" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$SNAPHOST_ENV_FILE") == "$CURRENT_VERSION" ]] \
  && [[ $(<"$APPARMOR_COUNT_FILE") == 2 ]] \
  && grep -q 'restoring checkout v1.2.2' "$OUTPUT"; then
  pass 'failed upgrade restores checkout and AppArmor while preserving release state'
else fail 'failed upgrade restores checkout and AppArmor while preserving release state'; fi

setup_case
seed_installed
export FAIL_APPARMOR_FIRST=1
run_capture upgrade "$VERSION"
if [[ $RC -ne 0 && $(<"$FAKE_GIT_HEAD") == "$CURRENT_COMMIT" ]] \
  && [[ $(<"$APPARMOR_COUNT_FILE") == 2 ]] \
  && ! grep -q '^deploy ' "$FAKE_LOG"; then
  pass 'target AppArmor failure restores the original checkout/profile'
else fail 'target AppArmor failure restores the original checkout/profile'; fi

setup_case
seed_installed
seed_previous
before=$(sha256sum "$SNAPHOST_ENV_FILE" "$SNAPHOST_STATE_DIR/current.env" "$SNAPHOST_STATE_DIR/previous.env")
run_capture rollback --dry-run
after=$(sha256sum "$SNAPHOST_ENV_FILE" "$SNAPHOST_STATE_DIR/current.env" "$SNAPHOST_STATE_DIR/previous.env")
if [[ $RC -eq 0 && "$before" == "$after" && ! -e "$CHECKOUT/state/caddy" \
  && $(<"$FAKE_GIT_HEAD") == "$CURRENT_COMMIT" ]] \
  && grep -q 'checkout would move from v1.2.2 to v1.2.1' "$OUTPUT"; then
  pass 'rollback dry run validates runtime without switching checkout or state'
else fail 'rollback dry run validates runtime without switching checkout or state'; fi

setup_case
seed_installed
seed_previous
run_capture rollback
if [[ $RC -eq 0 && $(<"$FAKE_GIT_HEAD") == "$OLDER_COMMIT" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$SNAPHOST_ENV_FILE") == "$OLDER_VERSION" ]] \
  && [[ $(awk -F= '$1=="version"{print $2}' "$SNAPHOST_STATE_DIR/current.env") == "$OLDER_VERSION" ]] \
  && [[ -x "$SNAPHOST_CLI_TARGET" ]] \
  && grep -q 'Checkout rollback completed: v1.2.2 -> v1.2.1' "$OUTPUT"; then
  pass 'rollback keeps runtime, state, checkout and stable CLI on one release'
else fail 'rollback keeps runtime, state, checkout and stable CLI on one release'; fi

setup_case
seed_installed
run_capture rollback
if [[ $RC -ne 0 ]] && grep -q 'current/previous deployment state is unavailable' "$OUTPUT" \
  && ! grep -q '^deploy ' "$FAKE_LOG"; then
  pass 'rollback refuses when previous release state is unavailable'
else fail 'rollback refuses when previous release state is unavailable'; fi

setup_case
seed_installed
seed_previous
export FAIL_ROLLBACK_ACTUAL=1
run_capture rollback
if [[ $RC -eq 42 && $(<"$FAKE_GIT_HEAD") == "$CURRENT_COMMIT" ]] \
  && [[ $(awk -F= '$1=="SNAPHOST_VERSION"{print $2}' "$SNAPHOST_ENV_FILE") == "$CURRENT_VERSION" ]] \
  && [[ $(<"$APPARMOR_COUNT_FILE") == 2 ]]; then
  pass 'failed runtime rollback leaves checkout and state on the current release'
else fail 'failed runtime rollback leaves checkout and state on the current release'; fi

printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[[ $FAIL -eq 0 ]]
