#!/usr/bin/env bash
set -Eeuo pipefail

SHA=${1:-}
[[ "$SHA" =~ ^[0-9a-fA-F]{40}$ ]] || { echo "invalid deployment SHA" >&2; exit 1; }

for name in DEPLOY_HOST DEPLOY_USER DEPLOY_PORT DEPLOY_ROOT DEPLOY_SMOKE_URL DEPLOY_GHCR_USERNAME DEPLOY_COMPOSE_PROJECT SSH_KEY_FILE SSH_KNOWN_HOSTS_FILE GHCR_IMAGE_PREFIX; do
  [[ -n ${!name:-} ]] || { echo "required deployment setting is missing: $name" >&2; exit 1; }
done
[[ "$DEPLOY_USER" != root ]] || { echo "root SSH deployment is forbidden" >&2; exit 1; }
[[ "$DEPLOY_USER" =~ ^[a-z_][a-z0-9_-]*$ ]] || { echo "invalid deployment user" >&2; exit 1; }
[[ "$DEPLOY_PORT" =~ ^[0-9]{1,5}$ ]] || { echo "invalid SSH port" >&2; exit 1; }
(( DEPLOY_PORT >= 1 && DEPLOY_PORT <= 65535 )) || { echo "invalid SSH port" >&2; exit 1; }
[[ "$DEPLOY_HOST" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || { echo "invalid deployment host" >&2; exit 1; }
[[ "$DEPLOY_ROOT" =~ ^/opt/[a-zA-Z0-9_][a-zA-Z0-9._-]*(/[a-zA-Z0-9_][a-zA-Z0-9._-]*)*$ ]] || {
  echo "DEPLOY_ROOT must be a normalized child path below /opt" >&2
  exit 1
}
[[ "$DEPLOY_SMOKE_URL" =~ ^https://[a-zA-Z0-9][a-zA-Z0-9.-]*(:[0-9]{1,5})?(/[a-zA-Z0-9._~%+,-]+)*$ ]] || {
  echo "staging/production smoke URL must be a safe HTTPS URL without userinfo" >&2
  exit 1
}
[[ "$DEPLOY_GHCR_USERNAME" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$ ]] || {
  echo "invalid GHCR username" >&2
  exit 1
}
[[ "$DEPLOY_COMPOSE_PROJECT" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || { echo "invalid Compose project name" >&2; exit 1; }
[[ "$GHCR_IMAGE_PREFIX" =~ ^ghcr\.io/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || { echo "invalid GHCR image prefix" >&2; exit 1; }
[[ -f "$SSH_KEY_FILE" && -f "$SSH_KNOWN_HOSTS_FILE" ]] || { echo "SSH identity files are unavailable" >&2; exit 1; }
run_id=${GITHUB_RUN_ID:-manual}
[[ "$run_id" == manual || "$run_id" =~ ^[0-9]+$ ]] || { echo "invalid workflow run ID" >&2; exit 1; }

images=(api-gateway user-billing builder-svc runner-svc ai-orchestrator)
for image in "${images[@]}"; do
  docker manifest inspect "$GHCR_IMAGE_PREFIX/$image:$SHA" >/dev/null || {
    echo "required image is unavailable: $image:$SHA" >&2
    exit 1
  }
done

archive=$(mktemp)
trap 'rm -f -- "$archive"' EXIT
tar -czf "$archive" \
  infra/deploy.sh \
  infra/docker-compose.prod.yml \
  infra/buildkitd.prod.toml

ssh_opts=(
  -i "$SSH_KEY_FILE"
  -p "$DEPLOY_PORT"
  -o BatchMode=yes
  -o IdentitiesOnly=yes
  -o StrictHostKeyChecking=yes
  -o "UserKnownHostsFile=$SSH_KNOWN_HOSTS_FILE"
)
remote="$DEPLOY_USER@$DEPLOY_HOST"
incoming="$DEPLOY_ROOT/releases/.incoming-$SHA-$run_id.tgz"

encode() { printf '%s' "$1" | base64 | tr -d '\r\n'; }
sha_b64=$(encode "$SHA")
root_b64=$(encode "$DEPLOY_ROOT")
incoming_b64=$(encode "$incoming")
smoke_b64=$(encode "$DEPLOY_SMOKE_URL")
username_b64=$(encode "$DEPLOY_GHCR_USERNAME")
project_b64=$(encode "$DEPLOY_COMPOSE_PROJECT")

ssh "${ssh_opts[@]}" "$remote" bash -s -- "$root_b64" <<'PREPARE'
set -Eeuo pipefail
root=$(printf '%s' "$1" | base64 -d)
[[ "$root" =~ ^/opt/[a-zA-Z0-9_][a-zA-Z0-9._-]*(/[a-zA-Z0-9_][a-zA-Z0-9._-]*)*$ ]]
mkdir -p -- "$root/releases"
PREPARE
scp "${ssh_opts[@]/-p/-P}" "$archive" "$remote:$incoming"

ssh "${ssh_opts[@]}" "$remote" bash -s -- \
  "$sha_b64" "$root_b64" "$incoming_b64" "$smoke_b64" "$username_b64" "$project_b64" <<'REMOTE'
set -Eeuo pipefail
decode() { printf '%s' "$1" | base64 -d; }
sha=$(decode "$1")
root=$(decode "$2")
incoming=$(decode "$3")
smoke_url=$(decode "$4")
ghcr_username=$(decode "$5")
compose_project=$(decode "$6")
[[ "$sha" =~ ^[0-9a-fA-F]{40}$ ]]
[[ "$root" =~ ^/opt/[a-zA-Z0-9_][a-zA-Z0-9._-]*(/[a-zA-Z0-9_][a-zA-Z0-9._-]*)*$ ]]
[[ "$incoming" == "$root/releases/.incoming-$sha-"*.tgz ]]
[[ "$smoke_url" =~ ^https://[a-zA-Z0-9][a-zA-Z0-9.-]*(:[0-9]{1,5})?(/[a-zA-Z0-9._~%+,-]+)*$ ]]
[[ "$ghcr_username" =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$ ]]
[[ "$compose_project" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]]
release="$root/releases/$sha"
candidate=$(mktemp -d "$root/releases/.candidate-$sha.XXXXXX")
link_tmp="$root/.current-$sha-$$"
cleanup() { rm -rf -- "$candidate" "$incoming" "$link_tmp"; }
trap cleanup EXIT INT TERM

tar -xzf "$incoming" -C "$candidate"
test -f "$candidate/infra/deploy.sh"
test -f "$candidate/infra/docker-compose.prod.yml"
test -f "$candidate/infra/buildkitd.prod.toml"
chmod 0755 "$candidate/infra/deploy.sh"

if [[ -d "$release" ]]; then
  diff -qr "$candidate/infra" "$release" >/dev/null || {
    echo "release directory exists with different content" >&2
    exit 1
  }
else
  mv -- "$candidate/infra" "$release"
fi

common_env=(
  "SNAPHOST_COMPOSE_FILE=$release/docker-compose.prod.yml"
  "SNAPHOST_COMPOSE_PROJECT=$compose_project"
  "SNAPHOST_ENV_FILE=$root/env/production.env"
  "SNAPHOST_STATE_DIR=$root/state"
  "SNAPHOST_BACKUP_DIR=$root/backups"
  "SNAPHOST_GHCR_TOKEN_FILE=$root/secrets/ghcr-token"
  "SNAPHOST_PUBLIC_SMOKE_URL=$smoke_url"
  "GHCR_USERNAME=$ghcr_username"
)

# This whole block is the script `ssh ... bash -s` is reading from stdin, so
# anything invoked here that consumes stdin eats the lines below it. deploy.sh
# runs `docker compose exec/run`, which forward stdin to the container; it now
# redirects internally, and </dev/null here is the second layer. Without it the
# `mv` below vanished silently and the run still reported success, which is how
# `current` came to be missing on 2026-08-03.
env "${common_env[@]}" "$release/deploy.sh" preflight "$sha" </dev/null
ln -s -- "$release" "$link_tmp"
env "${common_env[@]}" "$release/deploy.sh" deploy "$sha" </dev/null
mv -Tf -- "$link_tmp" "$root/current"
# Fail loudly if the symlink did not survive, rather than reporting success.
[[ -L "$root/current" ]]
REMOTE
